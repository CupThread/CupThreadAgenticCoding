package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/CupThread/CupThreadAgenticCoding/internal/auth"
	"github.com/CupThread/CupThreadAgenticCoding/internal/config"
	"github.com/spf13/cobra"
)

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Log in, log out, and inspect your credentials",
	}
	cmd.AddCommand(newAuthLoginCmd(), newAuthLogoutCmd(), newAuthStatusCmd())
	return cmd
}

func newAuthLoginCmd() *cobra.Command {
	var (
		token     string
		useDevice bool
	)
	login := &cobra.Command{
		Use:   "login",
		Short: "Log in to CupThread (OAuth via browser, or --token)",
		Long: `Log in to CupThread.

With no flags this starts an OAuth login: a browser window opens, you approve
access, and the CLI stores a long-lived token pair (auto-refreshed).

Use --token to log in with a personal access token created in the Console
(Settings → API Tokens). Pass "-" to read the token from stdin, which avoids
leaking it into your shell history.

The token is trimmed of surrounding whitespace; a value that still contains
embedded spaces, tabs, or control characters is rejected with a
self-diagnosing error instead of a net/http transport failure.

If a previous login on this machine saved a default workspace or app that the
new account cannot see, those saved defaults are cleared with a warning
instead of silently targeting the previous user's workspace.

Logging in against a non-default API endpoint (--base-url or
$CUPTHREAD_BASE_URL) remembers that endpoint in the config file, so later
invocations reach the same server without the flag. --base-url and
$CUPTHREAD_BASE_URL still override it per invocation; 'cupthread auth
logout' forgets it.

With --json/--output yaml every method prints a single structured document
on stdout — {method, email, tokenPrefix, baseUrl} where method is "token",
"oauth" or "device" — and all progress (browser URL, device-flow
verification URI and user code, context-reconcile warnings) moves to
stderr. The device payload also echoes verificationUri and userCode.`,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if token != "" {
				return loginWithToken(cmd.Context(), token)
			}
			if useDevice {
				return loginWithDevice(cmd.Context())
			}
			return loginWithPKCE(cmd.Context())
		},
	}
	login.Flags().StringVar(&token, "token", "", "Personal access token (cpt_...); \"-\" reads from stdin")
	login.Flags().BoolVar(&useDevice, "device", false, "Log in with the device code flow (for SSH/containers without a local browser)")
	return login
}

// loginResult is the payload of a successful 'auth login' in structured
// mode; the same fields feed the human confirmation line. Email is omitted
// when the account has no address on record, and the device-flow fields are
// only set by --device.
type loginResult struct {
	Method      string `json:"method"` // "token", "oauth" or "device"
	Email       string `json:"email,omitempty"`
	TokenPrefix string `json:"tokenPrefix"`
	BaseURL     string `json:"baseUrl"`
	// VerificationURI and UserCode echo where the pending --device login
	// is approved, so an agent that relayed them can cross-check what it
	// waited for.
	VerificationURI string `json:"verificationUri,omitempty"`
	UserCode        string `json:"userCode,omitempty"`
}

// reportLogin emits the login outcome: one JSON/YAML document on stdout in
// structured mode, the human confirmation line otherwise.
func (a *app) reportLogin(res loginResult) error {
	if a.structured() {
		return a.out.Structured(res)
	}
	email := res.Email
	if email == "" {
		email = "<unknown email>"
	}
	if res.Method == "token" {
		a.out.Printf("✓ Logged in as %s (token %s…) at %s", email, res.TokenPrefix, res.BaseURL)
	} else {
		a.out.Printf("✓ Logged in as %s (OAuth, token %s…) at %s", email, res.TokenPrefix, res.BaseURL)
	}
	return nil
}

func loginWithToken(ctx context.Context, token string) error {
	if token == "-" {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return fmt.Errorf("read token from stdin: %w", err)
		}
		token = strings.TrimSpace(line)
	} else {
		token = strings.TrimSpace(token)
	}
	if token == "" {
		return errors.New("empty token")
	}
	if err := config.ValidateToken(token); err != nil {
		return fmt.Errorf("invalid --token: %w", err)
	}

	probe := api.New(A.baseURL())
	probe.Token = func(context.Context) (string, error) { return token, nil }
	var me api.MeResponse
	if err := probe.Do(ctx, "GET", "/api/v1/console/me", nil, nil, &me); err != nil {
		return fmt.Errorf("token rejected by %s: %w", A.baseURL(), err)
	}

	prefix := token
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	A.cfg.Auth = &config.Auth{
		Method:      "token",
		AccessToken: token,
		TokenPrefix: prefix,
	}
	reconcileWorkspaceContext(&me, A.warnf)
	A.rememberLoginBaseURL()
	if err := A.saveConfig(); err != nil {
		return err
	}
	res := loginResult{
		Method:      "token",
		TokenPrefix: prefix,
		BaseURL:     A.baseURL(),
	}
	if me.Email != nil {
		res.Email = *me.Email
	}
	return A.reportLogin(res)
}

func loginWithPKCE(ctx context.Context) error {
	authorizeURL, tokenURL, _, _ := auth.Endpoints(A.baseURL())
	set, err := auth.LoginPKCE(ctx, authorizeURL, tokenURL, auth.FirstPartyClientID, nil)
	if err != nil {
		return err
	}
	return finishOAuthLogin(ctx, set, loginResult{Method: "oauth"})
}

func loginWithDevice(ctx context.Context) error {
	_, tokenURL, deviceAuthorizeURL, _ := auth.Endpoints(A.baseURL())
	start, err := auth.StartDevice(ctx, deviceAuthorizeURL, tokenURL, auth.FirstPartyClientID)
	if err != nil {
		return err
	}
	// The verification URI and user code are progress, not results: they
	// go to stderr in both modes so stdout carries at most one
	// machine-readable document — the final login result, which also
	// echoes the URI and code for agents relaying them to a human.
	fmt.Fprintf(os.Stderr, "First, open:  %s\n", start.VerificationURI)
	fmt.Fprintf(os.Stderr, "Enter code:   %s\n", start.UserCode)
	set, err := start.Wait(ctx)
	if err != nil {
		return err
	}
	return finishOAuthLogin(ctx, set, loginResult{
		Method:          "device",
		VerificationURI: start.VerificationURI,
		UserCode:        start.UserCode,
	})
}

func finishOAuthLogin(ctx context.Context, set *auth.TokenSet, res loginResult) error {
	A.applyTokenSet(set)

	var me api.MeResponse
	if err := A.client.Do(ctx, "GET", "/api/v1/console/me", nil, nil, &me); err != nil {
		return fmt.Errorf("login succeeded but session check failed: %w", err)
	}
	reconcileWorkspaceContext(&me, A.warnf)
	A.rememberLoginBaseURL()
	if err := A.saveConfig(); err != nil {
		return err
	}
	if me.Email != nil {
		res.Email = *me.Email
	}
	res.TokenPrefix = A.cfg.Auth.TokenPrefix
	res.BaseURL = A.baseURL()
	return A.reportLogin(res)
}

// rememberLoginBaseURL stores the base URL the credential was issued against,
// so later invocations without --base-url/$CUPTHREAD_BASE_URL reach the same
// server instead of silently falling back to production. The default URL is
// never stored: an empty field keeps following config.DefaultBaseURL.
func (a *app) rememberLoginBaseURL() {
	if url := a.baseURL(); url != config.DefaultBaseURL {
		a.cfg.BaseURL = url
		return
	}
	a.cfg.BaseURL = ""
}

func newAuthLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove stored credentials from this machine",
		Long: `Remove stored credentials from this machine.

Also forgets the API base URL a non-default login stored, so the next login
starts from the default endpoint again.

This only clears local state. To revoke the token server-side, delete it in
the Console (Settings → API Tokens / Authorized Apps) or use
'cupthread api request DELETE /api/v1/console/tokens/<id>' once the token
management API is available.

Logout also clears the saved default workspace, per-workspace app defaults
and base URL, so the next login starts from a clean slate instead of
inheriting the previous account's context.

With --json/--output yaml, stdout carries a single
{"loggedOut":true,"configPath":…,"cleared":[…]} document.`,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var cleared []string
			if A.cfg.DefaultWorkspace != "" {
				cleared = append(cleared, "default workspace "+A.cfg.DefaultWorkspace)
			}
			if len(A.cfg.Workspaces) > 0 {
				cleared = append(cleared, "per-workspace app defaults")
			}
			if A.cfg.BaseURL != "" {
				cleared = append(cleared, "base URL")
			}
			A.cfg.Auth = nil
			A.cfg.DefaultWorkspace = ""
			A.cfg.Workspaces = nil
			A.cfg.BaseURL = ""
			if err := A.saveConfig(); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(logoutResult{
					LoggedOut:  true,
					ConfigPath: A.cfgPath,
					Cleared:    cleared,
				})
			}
			A.out.Printf("✓ Credentials removed from %s", A.cfgPath)
			if len(cleared) > 0 {
				A.out.Printf("  Cleared saved context from the previous login: %s", strings.Join(cleared, ", "))
			}
			return nil
		},
	}
}

// logoutResult is the machine-readable payload of 'auth logout'. Cleared
// names the inherited context that was wiped along with the credential.
type logoutResult struct {
	LoggedOut  bool     `json:"loggedOut"`
	ConfigPath string   `json:"configPath"`
	Cleared    []string `json:"cleared,omitempty"`
}

// reconcileWorkspaceContext drops workspace context inherited from a previous
// login that the just-authenticated account cannot see. The config holds
// exactly one credential, so its user-scoped defaults must never outlive that
// credential: a saved default workspace invisible to the new account would
// route every workspace-scoped command at the previous user's workspace (or
// fail with an unrelated 403). Both login paths already fetch /console/me,
// so the new account's workspace list is in hand at no extra cost.
func reconcileWorkspaceContext(me *api.MeResponse, warnf func(string, ...any)) {
	if A.cfg.DefaultWorkspace == "" && len(A.cfg.Workspaces) == 0 {
		return
	}
	visible := make(map[string]bool, len(me.Workspaces))
	for i := range me.Workspaces {
		visible[me.Workspaces[i].Workspace.ID] = true
	}
	if A.cfg.DefaultWorkspace != "" && !visible[A.cfg.DefaultWorkspace] {
		warnf("⚠ Cleared saved default workspace %s: it is not visible to the logged-in account (saved by a previous login)", A.cfg.DefaultWorkspace)
		A.cfg.DefaultWorkspace = ""
	}
	dropped := []string{}
	for id := range A.cfg.Workspaces {
		if !visible[id] {
			delete(A.cfg.Workspaces, id)
			dropped = append(dropped, id)
		}
	}
	if len(dropped) > 0 {
		sort.Strings(dropped)
		warnf("⚠ Dropped saved per-workspace app default(s) for %s: not visible to the logged-in account", strings.Join(dropped, ", "))
	}
	if len(A.cfg.Workspaces) == 0 {
		A.cfg.Workspaces = nil
	}
}

func newAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:                   "status",
		Short:                 "Show the current login and defaults",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			type statusRow struct {
				BaseURL       string `json:"baseUrl"`
				IssuedBaseURL string `json:"issuedBaseUrl,omitempty"`
				Method        string `json:"method"`
				TokenPrefix   string `json:"tokenPrefix,omitempty"`
				ExpiresAt     string `json:"expiresAt,omitempty"`
				// Stored* mirror the login saved in the config file. They are
				// only set when $CUPTHREAD_TOKEN overrides it, so `method`
				// always names the credential requests actually use.
				StoredMethod      string `json:"storedMethod,omitempty"`
				StoredTokenPrefix string `json:"storedTokenPrefix,omitempty"`
				StoredExpiresAt   string `json:"storedExpiresAt,omitempty"`
				DefaultWorkspace  string `json:"defaultWorkspace,omitempty"`
				DefaultApp        string `json:"defaultApp,omitempty"`
				User              string `json:"user,omitempty"`
			}
			row := statusRow{BaseURL: A.baseURL(), Method: "not logged in"}
			if stored := strings.TrimRight(A.cfg.BaseURL, "/"); stored != "" {
				row.IssuedBaseURL = stored
			}
			env := config.EnvToken()
			if env != "" {
				row.Method = "token ($CUPTHREAD_TOKEN)"
				row.TokenPrefix = mask(env)
			}
			if A.cfg.Auth != nil {
				if env != "" {
					row.StoredMethod = A.cfg.Auth.Method
					row.StoredTokenPrefix = A.cfg.Auth.TokenPrefix
					row.StoredExpiresAt = A.cfg.Auth.ExpiresAt
				} else {
					row.Method = A.cfg.Auth.Method
					row.TokenPrefix = A.cfg.Auth.TokenPrefix
					row.ExpiresAt = A.cfg.Auth.ExpiresAt
				}
			}
			row.DefaultWorkspace = A.cfg.DefaultWorkspace
			if prefs, ok := A.cfg.Workspaces[A.cfg.DefaultWorkspace]; ok {
				row.DefaultApp = prefs.DefaultApp
			}

			var me api.MeResponse
			if err := A.client.Do(cmd.Context(), "GET", "/api/v1/console/me", nil, nil, &me); err == nil {
				if me.Email != nil {
					row.User = *me.Email
				} else {
					row.User = me.ClerkUserID
				}
			}

			if A.structured() {
				return A.out.Structured(row)
			}
			table := [][]string{
				{"Base URL", row.BaseURL},
				{"Auth", row.Method},
				{"Token", orDash(row.TokenPrefix)},
				{"Expires", orDash(row.ExpiresAt)},
				{"User", orDash(row.User)},
				{"Default workspace", orDash(row.DefaultWorkspace)},
				{"Default app", orDash(row.DefaultApp)},
			}
			if row.StoredMethod != "" {
				table = append(table, []string{
					"Stored login (inactive — overridden by $CUPTHREAD_TOKEN)",
					fmt.Sprintf("%s, token %s, expires %s",
						row.StoredMethod, orDash(row.StoredTokenPrefix), orDash(row.StoredExpiresAt)),
				})
			}
			if row.IssuedBaseURL != "" {
				table = append(table, []string{"Credential issued for", row.IssuedBaseURL})
			}
			A.out.Table([]string{"Field", "Value"}, table)
			return nil
		},
	}
}

func mask(token string) string {
	if len(token) > 12 {
		return token[:12]
	}
	return token
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
