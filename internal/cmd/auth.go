package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/CupThread/CupThreadAgenticCoding/internal/auth"
	"github.com/CupThread/CupThreadAgenticCoding/internal/config"
	"github.com/CupThread/CupThreadAgenticCoding/internal/output"
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

The interactive flows (browser and device) store the token pair as soon as
the server issues it; the session check that follows is advisory. If the API
cannot be reached right after login, the command still succeeds and prints a
warning — saved workspace defaults are then cleared because they could not
be verified against the new login. 'cupthread auth status' confirms the
session once the API is reachable again.

Logging in against a non-default API endpoint (--base-url or
$CUPTHREAD_BASE_URL) remembers that endpoint in the config file, so later
invocations reach the same server without the flag. --base-url and
$CUPTHREAD_BASE_URL still override it per invocation; 'cupthread auth
logout' forgets it. Whatever endpoint issued the credential stays its
issuer: token refresh and 'auth logout --revoke' always target that server
even when an override retargets ordinary API calls (a divergence prints one
warning on stderr).

$CUPTHREAD_TOKEN outranks any stored credential: while it is set, a login
here is saved but stays inactive — every command keeps authenticating with
the environment token — and this command says so with a warning.

With --json/--output yaml every method prints a single structured document
on stdout — {method, email, tokenPrefix, baseUrl} where method is "token",
"oauth" or "device" — and all progress (browser URL, device-flow
verification URI and user code, context-reconcile warnings) moves to
stderr. The device payload also echoes verificationUri and userCode, and
effectiveCredential is set to "env ($CUPTHREAD_TOKEN)" when the
environment token is overriding the saved login.`,
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
	// EffectiveCredential names what requests actually authenticate as after
	// this login: "env ($CUPTHREAD_TOKEN)" when the environment token
	// overrides the just-saved credential — the stored login is inactive
	// until the variable is unset (issue #184). Empty means the saved login
	// itself is effective.
	EffectiveCredential string `json:"effectiveCredential,omitempty"`
}

// reportLogin emits the login outcome: one JSON/YAML document on stdout in
// structured mode, the human confirmation line otherwise. Either way the
// $CUPTHREAD_TOKEN override is disclosed when set: the confirmation line
// would otherwise describe a credential that is not, and will not be, in
// effect (issue #184).
func (a *app) reportLogin(res loginResult) error {
	res.EffectiveCredential = envCredentialOverride()
	a.warnEnvTokenOverride("the freshly saved login stays inactive until it is unset")
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
		// The piped credential goes through the shared bounded reader like
		// every other piped secret (--secret -, integration --token -): a
		// newline-free runaway pipe fails with input_too_large at the 64 KB
		// cap instead of buffering stdin without bound (issue #189).
		data, err := readInputFile(token, maxSecretBytes)
		if err != nil {
			if perr := A.reportInputTooLarge(err); perr != nil {
				return perr
			}
			return err
		}
		token = strings.TrimSpace(string(data))
	} else {
		token = strings.TrimSpace(token)
	}
	if token == "" {
		return errors.New("empty token")
	}
	if err := config.ValidateToken(token); err != nil {
		return fmt.Errorf("invalid --token: %w", err)
	}

	probe := A.unauthenticatedClient()
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

// printDeviceProgress writes the device-flow verification URI and user code
// to stderr. Both come from the device-authorization endpoint, so they pass
// through the strict terminal-control stripper before reaching the terminal;
// the final login result still echoes them verbatim for agents relaying them
// to a human.
func printDeviceProgress(verificationURI, userCode string) {
	fmt.Fprintf(os.Stderr, "First, open:  %s\n", output.StripTerminalControls(verificationURI))
	fmt.Fprintf(os.Stderr, "Enter code:   %s\n", output.StripTerminalControls(userCode))
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
	printDeviceProgress(start.VerificationURI, start.UserCode)
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
	A.rememberLoginBaseURL()
	// Persist the freshly issued pair BEFORE the session check: the pair was
	// just minted by the server's own token endpoint and the check is
	// advisory, so a transient failure (or an interrupted probe) must never
	// abandon it — redoing the interactive browser/device approval would
	// mint a second rotating refresh chain while the first stays valid until
	// expiry (issue #69).
	if err := A.saveConfig(); err != nil {
		return err
	}

	var me api.MeResponse
	if err := A.client.Do(ctx, "GET", "/api/v1/console/me", nil, nil, &me); err != nil {
		warnf := func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		}
		warnf("warning: logged in, but could not verify the session yet: %v — check 'cupthread auth status'", err)
		if clearUnverifiedWorkspaceContext(warnf) {
			if err := A.saveConfig(); err != nil {
				return err
			}
		}
		if A.structured() {
			res.TokenPrefix = A.cfg.Auth.TokenPrefix
			res.BaseURL = A.baseURL()
			res.EffectiveCredential = envCredentialOverride()
			A.warnEnvTokenOverride("the freshly saved login stays inactive until it is unset")
			return A.out.Structured(res)
		}
		A.out.Printf("✓ Logged in (OAuth, token %s…) at %s", A.cfg.Auth.TokenPrefix, A.baseURL())
		A.warnEnvTokenOverride("the freshly saved login stays inactive until it is unset")
		return nil
	}
	if reconcileWorkspaceContext(&me, A.warnf) {
		if err := A.saveConfig(); err != nil {
			return err
		}
	}
	if me.Email != nil {
		res.Email = *me.Email
	}
	res.TokenPrefix = A.cfg.Auth.TokenPrefix
	res.BaseURL = A.baseURL()
	return A.reportLogin(res)
}

// rememberLoginBaseURL records, at login time, the server the fresh
// credential was issued against: the config-level BaseURL so later
// invocations without --base-url/$CUPTHREAD_BASE_URL reach the same server
// instead of silently falling back to production, and Auth.IssuedBaseURL so
// the credential protocol (token refresh, logout --revoke) stays pinned to
// that issuer even when a later invocation overrides the API endpoint
// (issue #191). The default URL is never stored: empty fields keep following
// config.DefaultBaseURL. Both values must move together or the remembered
// endpoint and the credential's pinned issuer would disagree.
func (a *app) rememberLoginBaseURL() {
	url := a.baseURL()
	if url == config.DefaultBaseURL {
		a.cfg.BaseURL = ""
	} else {
		a.cfg.BaseURL = url
	}
	if a.cfg.Auth != nil {
		a.cfg.Auth.IssuedBaseURL = a.cfg.BaseURL
	}
}

func newAuthLogoutCmd() *cobra.Command {
	var revoke bool
	logout := &cobra.Command{
		Use:   "logout",
		Short: "Remove stored credentials from this machine",
		Long: `Remove stored credentials from this machine.

Also forgets the API base URL a non-default login stored, so the next login
starts from the default endpoint again.

Logout also clears the saved default workspace, per-workspace app defaults
and base URL, so the next login starts from a clean slate instead of
inheriting the previous account's context.

$CUPTHREAD_TOKEN is not touched: while it is set it still authenticates every
command after logout, so the confirmation carries a warning — unset it to
fully de-provision this machine.

By default this only clears local state. Pass --revoke to also invalidate
the stored credential server-side before it is removed: for an OAuth login
the CLI posts the stored refresh token to the issuing server's RFC 7009
revocation endpoint (pinned to the credential's issuer, regardless of any
--base-url override), which disables the whole token pair (the access token
dies with it). Revocation is best-effort — a network failure or server
error prints a warning and the local credentials are removed anyway, so
logout never gets stuck on a unreachable server.

Personal access tokens (auth login --token) have no CLI-reachable
revocation endpoint: --revoke then prints the Console path that revokes
them (Settings → API Tokens) instead of sending a request that cannot
succeed.

With --json/--output yaml, stdout carries a single
{"loggedOut":true,"configPath":…,"cleared":[…]} document (envOverride names
$CUPTHREAD_TOKEN when it is still authenticating every command); revocation
notices and warnings go to stderr so scripts can parse stdout directly.`,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if revoke {
				revokeStoredCredential(cmd.Context(), A)
			}
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
			A.warnEnvTokenOverride("unset it to fully de-provision this machine")
			if A.structured() {
				return A.out.Structured(logoutResult{
					LoggedOut:   true,
					ConfigPath:  A.cfgPath,
					Cleared:     cleared,
					EnvOverride: envTokenOverrideName(),
				})
			}
			A.out.Printf("✓ Credentials removed from %s", A.cfgPath)
			if len(cleared) > 0 {
				A.out.Printf("  Cleared saved context from the previous login: %s", strings.Join(cleared, ", "))
			}
			return nil
		},
	}
	logout.Flags().BoolVar(&revoke, "revoke", false, "Also revoke the stored OAuth token pair server-side (RFC 7009) before clearing local state")
	return logout
}

// revokeStoredCredential invalidates the stored credential server-side, as
// far as the API allows, before logout clears the local config. OAuth
// credentials are revoked through the public RFC 7009 endpoint: posting the
// refresh token cascades to its paired access token, killing the whole
// chain. Personal access tokens have no CLI-reachable revocation (the
// console token-management routes require an interactive session and 403
// every cpt_ credential), so the warning names the Console path and prints
// the stored prefix so the user can find the right row. Best-effort by
// design: every outcome lets logout proceed with clearing local state,
// because a failed revocation must never trap credentials on the machine.
// Every notice goes through warnf, so in --json/--output yaml mode it lands
// on stderr and stdout keeps carrying exactly one logoutResult document.
func revokeStoredCredential(ctx context.Context, a *app) {
	authState := a.cfg.Auth
	if authState == nil {
		a.warnf("No stored credential to revoke; clearing local state only.")
		return
	}
	if authState.Method != "oauth" {
		prefix := authState.TokenPrefix
		if prefix == "" {
			prefix = mask(authState.AccessToken)
		}
		a.warnf("⚠ Personal access tokens cannot be revoked from the CLI — revoke it in the Console (Settings → API Tokens, prefix %s…) if it should die now. Local credentials are removed anyway.", orDash(prefix))
		return
	}
	token, kind := authState.RefreshToken, "token pair"
	if token == "" {
		token, kind = authState.AccessToken, "access token"
	}
	if token == "" {
		a.warnf("⚠ Stored OAuth credential holds no tokens to revoke; clearing local state only.")
		return
	}
	clientID := authState.ClientID
	if clientID == "" {
		clientID = auth.FirstPartyClientID
	}
	// Revocation is part of the credential protocol: it targets the server
	// that issued the token, not whatever --base-url/$CUPTHREAD_BASE_URL
	// names — sending a long-lived refresh token to any other host would be
	// the exact leak the issuer pinning exists to prevent (issue #191).
	issuer := authState.IssuerBaseURL(a.cfg.BaseURL)
	if err := auth.Revoke(ctx, auth.RevokeEndpoint(issuer), clientID, token); err != nil {
		a.warnf("⚠ Server-side revocation failed (%v): the credential may still be live — revoke it in the Console (Settings → Authorized Apps). Local credentials are removed anyway.", err)
		return
	}
	a.warnf("✓ Revoked the server-side %s at %s", kind, issuer)
}

// logoutResult is the machine-readable payload of 'auth logout'. Cleared
// names the inherited context that was wiped along with the credential.
type logoutResult struct {
	LoggedOut  bool     `json:"loggedOut"`
	ConfigPath string   `json:"configPath"`
	Cleared    []string `json:"cleared,omitempty"`
	// EnvOverride names the environment variable that still authenticates
	// every command after this logout: the stored credential is gone, but the
	// machine is not de-provisioned while the variable lives on (issue #184).
	// Empty when no override is set.
	EnvOverride string `json:"envOverride,omitempty"`
}

// envTokenOverrideName returns the environment variable outranking the stored
// credential, or "" when none is set — the value logoutResult.EnvOverride
// carries so structured consumers can tell a de-provisioned machine from one
// where $CUPTHREAD_TOKEN still authenticates every command (issue #184).
func envTokenOverrideName() string {
	if config.EnvToken() == "" {
		return ""
	}
	return "CUPTHREAD_TOKEN"
}

// envCredentialOverride is the loginResult.EffectiveCredential value for a
// login that stored a credential while $CUPTHREAD_TOKEN is set; empty means
// the stored login itself is what requests use (issue #184).
func envCredentialOverride() string {
	name := envTokenOverrideName()
	if name == "" {
		return ""
	}
	return "env ($" + name + ")"
}

// warnEnvTokenOverride discloses, on stderr in every output mode, that
// $CUPTHREAD_TOKEN still outranks the credential this command just stored or
// cleared: logout's "Credentials removed" would otherwise claim a
// de-provisioning that has not happened, and login's confirmation would
// describe a credential that is not in effect (issue #184). It bypasses
// warnf so the warning reaches stderr in table mode too — the moment the
// misleading action runs is exactly when the operator must hear about it.
// Disclosure only: the command still succeeds with exit code 0, and in
// structured mode stdout keeps carrying exactly one document.
func (a *app) warnEnvTokenOverride(consequence string) {
	if config.EnvToken() == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "⚠ $CUPTHREAD_TOKEN is set and still authenticates every command — %s\n", consequence)
}

// reconcileWorkspaceContext drops workspace context inherited from a previous
// login that the just-authenticated account cannot see. The config holds
// exactly one credential, so its user-scoped defaults must never outlive that
// credential: a saved default workspace invisible to the new account would
// route every workspace-scoped command at the previous user's workspace (or
// fail with an unrelated 403). Both login paths already fetch /console/me,
// so the new account's workspace list is in hand at no extra cost.
// It reports whether the config changed, so callers can decide to re-persist.
func reconcileWorkspaceContext(me *api.MeResponse, warnf func(string, ...any)) bool {
	if A.cfg.DefaultWorkspace == "" && len(A.cfg.Workspaces) == 0 {
		return false
	}
	changed := false
	visible := make(map[string]bool, len(me.Workspaces))
	for i := range me.Workspaces {
		visible[me.Workspaces[i].Workspace.ID] = true
	}
	if A.cfg.DefaultWorkspace != "" && !visible[A.cfg.DefaultWorkspace] {
		warnf("⚠ Cleared saved default workspace %s: it is not visible to the logged-in account (saved by a previous login)", A.cfg.DefaultWorkspace)
		A.cfg.DefaultWorkspace = ""
		changed = true
	}
	dropped := []string{}
	for id := range A.cfg.Workspaces {
		if !visible[id] {
			delete(A.cfg.Workspaces, id)
			dropped = append(dropped, id)
			changed = true
		}
	}
	if len(dropped) > 0 {
		sort.Strings(dropped)
		warnf("⚠ Dropped saved per-workspace app default(s) for %s: not visible to the logged-in account", strings.Join(dropped, ", "))
	}
	if len(A.cfg.Workspaces) == 0 {
		A.cfg.Workspaces = nil
	}
	return changed
}

// clearUnverifiedWorkspaceContext is the conservative counterpart of
// reconcileWorkspaceContext for the session check failing: nothing about the
// freshly issued credential could be verified against the server, so saved
// workspace defaults from a previous login are dropped wholesale instead of
// kept unverified — an OAuth approval may have switched accounts, and a stale
// invisible default would route the new credential at the previous user's
// workspace (issue #82's invariant, applied to issue #69's failure path).
// It reports whether the config changed, so callers can decide to re-persist.
func clearUnverifiedWorkspaceContext(warnf func(string, ...any)) bool {
	if A.cfg.DefaultWorkspace == "" && len(A.cfg.Workspaces) == 0 {
		return false
	}
	if A.cfg.DefaultWorkspace != "" {
		warnf("⚠ Cleared saved default workspace %s: the session check failed, so it could not be verified against the new login (restore it with 'cupthread workspaces use' once the API is reachable)", A.cfg.DefaultWorkspace)
	}
	if len(A.cfg.Workspaces) > 0 {
		ids := make([]string, 0, len(A.cfg.Workspaces))
		for id := range A.cfg.Workspaces {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		warnf("⚠ Dropped saved per-workspace app default(s) for %s: the session check failed, so they could not be verified against the new login", strings.Join(ids, ", "))
	}
	A.cfg.DefaultWorkspace = ""
	A.cfg.Workspaces = nil
	return true
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
			// The credential's issuing server comes from the credential
			// itself (Auth.IssuedBaseURL, pinned at login), falling back to
			// the remembered config base URL for pre-pinning credentials —
			// so the line stays truthful even if cfg.BaseURL is later
			// changed or an override retargets API requests (issue #191).
			if issuer := A.cfg.Auth.IssuerBaseURL(A.cfg.BaseURL); issuer != "" && issuer != config.DefaultBaseURL {
				row.IssuedBaseURL = issuer
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
