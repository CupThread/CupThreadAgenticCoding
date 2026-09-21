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
		token    string
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

If a previous login on this machine saved a default workspace or app that the
new account cannot see, those saved defaults are cleared with a warning
instead of silently targeting the previous user's workspace.`,
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

func loginWithToken(ctx context.Context, token string) error {
	if token == "-" {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return fmt.Errorf("read token from stdin: %w", err)
		}
		token = strings.TrimSpace(line)
	}
	if token == "" {
		return errors.New("empty token")
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
	reconcileWorkspaceContext(&me, A.out.Printf)
	if err := A.saveConfig(); err != nil {
		return err
	}
	email := "<unknown email>"
	if me.Email != nil {
		email = *me.Email
	}
	A.out.Printf("✓ Logged in as %s (token %s…) at %s", email, prefix, A.baseURL())
	return nil
}

func loginWithPKCE(ctx context.Context) error {
	authorizeURL, tokenURL, _, _ := auth.Endpoints(A.baseURL())
	set, err := auth.LoginPKCE(ctx, authorizeURL, tokenURL, auth.FirstPartyClientID, nil)
	if err != nil {
		return err
	}
	return finishOAuthLogin(set)
}

func loginWithDevice(ctx context.Context) error {
	_, tokenURL, deviceAuthorizeURL, _ := auth.Endpoints(A.baseURL())
	start, err := auth.StartDevice(ctx, deviceAuthorizeURL, tokenURL, auth.FirstPartyClientID)
	if err != nil {
		return err
	}
	A.out.Printf("First, open:  %s", start.VerificationURI)
	A.out.Printf("Enter code:   %s", start.UserCode)
	set, err := start.Wait(ctx)
	if err != nil {
		return err
	}
	return finishOAuthLogin(set)
}

func finishOAuthLogin(set *auth.TokenSet) error {
	A.applyTokenSet(set)

	var me api.MeResponse
	if err := A.client.Do(context.Background(), "GET", "/api/v1/console/me", nil, nil, &me); err != nil {
		return fmt.Errorf("login succeeded but session check failed: %w", err)
	}
	reconcileWorkspaceContext(&me, A.out.Printf)
	if err := A.saveConfig(); err != nil {
		return err
	}
	email := "<unknown email>"
	if me.Email != nil {
		email = *me.Email
	}
	A.out.Printf("✓ Logged in as %s (OAuth, token %s…) at %s", email, A.cfg.Auth.TokenPrefix, A.baseURL())
	return nil
}

func newAuthLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove stored credentials from this machine",
		Long: `Remove stored credentials from this machine.

This only clears local state. To revoke the token server-side, delete it in
the Console (Settings → API Tokens / Authorized Apps) or use
'cupthread api request DELETE /api/v1/console/tokens/<id>' once the token
management API is available.

Logout also clears the saved default workspace, per-workspace app defaults
and base URL, so the next login starts from a clean slate instead of
inheriting the previous account's context.`,
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
			A.out.Printf("✓ Credentials removed from %s", A.cfgPath)
			if len(cleared) > 0 {
				A.out.Printf("  Cleared saved context from the previous login: %s", strings.Join(cleared, ", "))
			}
			return nil
		},
	}
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
		Use:   "status",
		Short: "Show the current login and defaults",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			type statusRow struct {
				BaseURL          string `json:"baseUrl"`
				Method           string `json:"method"`
				TokenPrefix      string `json:"tokenPrefix,omitempty"`
				ExpiresAt        string `json:"expiresAt,omitempty"`
				DefaultWorkspace string `json:"defaultWorkspace,omitempty"`
				DefaultApp       string `json:"defaultApp,omitempty"`
				User             string `json:"user,omitempty"`
			}
			row := statusRow{BaseURL: A.baseURL(), Method: "not logged in"}
			if env := config.EnvToken(); env != "" {
				row.Method = "token ($CUPTHREAD_TOKEN)"
				row.TokenPrefix = mask(env)
			}
			if A.cfg.Auth != nil {
				row.Method = A.cfg.Auth.Method
				row.TokenPrefix = A.cfg.Auth.TokenPrefix
				row.ExpiresAt = A.cfg.Auth.ExpiresAt
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
			A.out.Table([]string{"Field", "Value"}, [][]string{
				{"Base URL", row.BaseURL},
				{"Auth", row.Method},
				{"Token", orDash(row.TokenPrefix)},
				{"Expires", orDash(row.ExpiresAt)},
				{"User", orDash(row.User)},
				{"Default workspace", orDash(row.DefaultWorkspace)},
				{"Default app", orDash(row.DefaultApp)},
			})
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
