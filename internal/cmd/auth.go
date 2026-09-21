package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
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

Logging in against a non-default API endpoint (--base-url or
$CUPTHREAD_BASE_URL) remembers that endpoint in the config file, so later
invocations reach the same server without the flag. --base-url and
$CUPTHREAD_BASE_URL still override it per invocation; 'cupthread auth
logout' forgets it.`,
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
	A.rememberLoginBaseURL()
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
	A.rememberLoginBaseURL()
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
management API is available.`,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			A.cfg.Auth = nil
			A.cfg.BaseURL = ""
			if err := A.saveConfig(); err != nil {
				return err
			}
			A.out.Printf("✓ Credentials removed from %s", A.cfgPath)
			return nil
		},
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
				IssuedBaseURL    string `json:"issuedBaseUrl,omitempty"`
				Method           string `json:"method"`
				TokenPrefix      string `json:"tokenPrefix,omitempty"`
				ExpiresAt        string `json:"expiresAt,omitempty"`
				DefaultWorkspace string `json:"defaultWorkspace,omitempty"`
				DefaultApp       string `json:"defaultApp,omitempty"`
				User             string `json:"user,omitempty"`
			}
			row := statusRow{BaseURL: A.baseURL(), Method: "not logged in"}
			if stored := strings.TrimRight(A.cfg.BaseURL, "/"); stored != "" {
				row.IssuedBaseURL = stored
			}
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
			rows := [][]string{
				{"Base URL", row.BaseURL},
				{"Auth", row.Method},
				{"Token", orDash(row.TokenPrefix)},
				{"Expires", orDash(row.ExpiresAt)},
				{"User", orDash(row.User)},
				{"Default workspace", orDash(row.DefaultWorkspace)},
				{"Default app", orDash(row.DefaultApp)},
			}
			if row.IssuedBaseURL != "" {
				rows = append(rows, []string{"Credential issued for", row.IssuedBaseURL})
			}
			A.out.Table([]string{"Field", "Value"}, rows)
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
