package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// interactiveSessionServer answers 403 interactive_session_required the way
// the API does for interactive-only capabilities and records the request
// method and path.
func interactiveSessionServer(t *testing.T) (*httptest.Server, *string, *string) {
	t.Helper()
	var gotMethod, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"This action requires an interactive session; API tokens are not permitted","code":"interactive_session_required"}`))
	}))
	t.Cleanup(server.Close)
	return server, &gotMethod, &gotPath
}

// assertNoReloginAdvice checks the issue #58 contract on a surfaced error:
// the message must name the Console web UI and must not recommend
// 'cupthread auth login' — the browser OAuth login also issues a cpt_ token,
// so that advice is a dead-end re-login loop.
func assertNoReloginAdvice(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"interactive_session_required", "Console web UI"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "auth login") {
		t.Errorf("error %q still recommends 'auth login' as a remedy", err)
	}
}

// TestBillingCheckoutInteractiveSessionRequired covers billing.manage: a cpt_
// token calling POST .../billing/checkout surfaces the server's 403 with the
// Console-web-UI hint instead of re-login advice.
func TestBillingCheckoutInteractiveSessionRequired(t *testing.T) {
	server, gotMethod, gotPath := interactiveSessionServer(t)

	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	_, err := runRoot(t, server.URL, "billing", "checkout", "--workspace", "ws_1")
	assertNoReloginAdvice(t, err)
	if *gotMethod != http.MethodPost || *gotPath != "/api/v1/console/workspaces/ws_1/billing/checkout" {
		t.Errorf("request = %s %s", *gotMethod, *gotPath)
	}
}

// TestIntegrationsGitHubConnectInteractiveSessionRequired covers
// integration.manage: a cpt_ token calling POST .../integrations/github/
// manual-token surfaces the server's 403 with the Console-web-UI hint
// instead of re-login advice.
func TestIntegrationsGitHubConnectInteractiveSessionRequired(t *testing.T) {
	server, gotMethod, gotPath := interactiveSessionServer(t)

	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	_, err := runRoot(t, server.URL, "integrations", "github", "connect", "--token", "ghp_test", "--workspace", "ws_1")
	assertNoReloginAdvice(t, err)
	if *gotMethod != http.MethodPost || *gotPath != "/api/v1/console/workspaces/ws_1/integrations/github/manual-token" {
		t.Errorf("request = %s %s", *gotMethod, *gotPath)
	}
}

// TestInteractiveOnlyLongHelpsDoNotRecommendAuthLogin is the doc guard for
// issue #58: no help text under the command groups that can hit 403
// interactive_session_required (workspaces members/invitations, billing,
// integrations, changelog) may recommend 'cupthread auth login' — no CLI
// credential can perform those actions, so re-login advice is false.
func TestInteractiveOnlyLongHelpsDoNotRecommendAuthLogin(t *testing.T) {
	groups := map[string]*cobra.Command{
		"workspaces members":     newWorkspaceMembersCmd(),
		"workspaces invitations": newWorkspaceInvitationsCmd(),
		"billing":                newBillingCmd(),
		"integrations":           newIntegrationsCmd(),
		"changelog":              newChangelogCmd(),
	}
	for name, group := range groups {
		var walk func(path string, c *cobra.Command)
		walk = func(path string, c *cobra.Command) {
			for _, text := range []string{c.Short, c.Long, c.Example} {
				if strings.Contains(text, "auth login") {
					t.Errorf("%s: help of %q recommends 'auth login', which cannot help on interactive_session_required (issue #58):\n%s", name, c.Use, text)
				}
			}
			for _, sub := range c.Commands() {
				walk(path+" "+sub.Name(), sub)
			}
		}
		walk(name, group)
	}
}
