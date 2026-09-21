package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CupThread/CupThreadAgenticCoding/internal/auth"
	"github.com/CupThread/CupThreadAgenticCoding/internal/config"
)

// oauthLogoutFixture mirrors a real post-login config: an OAuth token pair
// plus saved workspace context and a remembered (stale) base URL.
const oauthLogoutFixture = `{"auth":{"method":"oauth","accessToken":"cpt_test_access","refreshToken":"cpr_test_refresh","clientId":"cupthread-cli","expiresAt":"2030-01-01T00:00:00Z","tokenPrefix":"cpt_test_acce"},"defaultWorkspace":"ws_123","workspaces":{"ws_123":{"defaultApp":"app_1"}},"baseUrl":"https://stale.example.com"}`

func writeLogoutConfig(t *testing.T, contents string) string {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return cfgPath
}

// assertLoggedOut pins the contract every logout path shares: the saved
// credential and all workspace/base-URL context are gone from the file.
func assertLoggedOut(t *testing.T, cfgPath string) {
	t.Helper()
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config after logout: %v", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config after logout: %v", err)
	}
	if cfg.Auth != nil {
		t.Errorf("auth section still present after logout: %+v", cfg.Auth)
	}
	if cfg.DefaultWorkspace != "" || len(cfg.Workspaces) > 0 || cfg.BaseURL != "" {
		t.Errorf("saved context survived logout: workspace=%q workspaces=%v baseUrl=%q", cfg.DefaultWorkspace, cfg.Workspaces, cfg.BaseURL)
	}
}

// TestLogoutRevokeOAuthPostsRefreshToken pins the --revoke wire contract:
// the stored refresh token (not the access token) is posted to the RFC 7009
// endpoint with the first-party client id, because the server cascades
// refresh-token revocation to the paired access token. Revocation is the
// only network call, the output confirms it, and local state is cleared.
func TestLogoutRevokeOAuthPostsRefreshToken(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "") // keep the test hermetic
	var revokeForms []map[string]string
	var otherRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == auth.RevokePath {
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse revoke form: %v", err)
				return
			}
			revokeForms = append(revokeForms, map[string]string{
				"token":     r.Form.Get("token"),
				"client_id": r.Form.Get("client_id"),
			})
			w.WriteHeader(http.StatusOK)
			return
		}
		otherRequests++
	}))
	defer server.Close()

	cfgPath := writeLogoutConfig(t, oauthLogoutFixture)
	out, err := runRootCfg(t, cfgPath, server.URL, "auth", "logout", "--revoke")
	if err != nil {
		t.Fatalf("logout --revoke: %v\n%s", err, out)
	}
	if len(revokeForms) != 1 {
		t.Fatalf("server saw %d revoke requests, want 1", len(revokeForms))
	}
	if revokeForms[0]["token"] != "cpr_test_refresh" {
		t.Errorf("revoke token = %q, want the stored refresh token cpr_test_refresh", revokeForms[0]["token"])
	}
	if revokeForms[0]["client_id"] != auth.FirstPartyClientID {
		t.Errorf("revoke client_id = %q, want %q", revokeForms[0]["client_id"], auth.FirstPartyClientID)
	}
	if otherRequests != 0 {
		t.Errorf("server saw %d non-revoke requests, want 0", otherRequests)
	}
	if !strings.Contains(out, "Revoked the server-side token pair") {
		t.Errorf("output missing revocation confirmation:\n%s", out)
	}
	assertLoggedOut(t, cfgPath)
}

// TestLogoutRevokeBestEffort pins the best-effort rule: revocation failure
// (server error or unreachable server) never fails the command and never
// keeps credentials on disk — it prints a warning and logout completes.
func TestLogoutRevokeBestEffort(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "") // keep the test hermetic
	t.Run("server returns 500", func(t *testing.T) {
		var requests int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer server.Close()

		cfgPath := writeLogoutConfig(t, oauthLogoutFixture)
		out, err := runRootCfg(t, cfgPath, server.URL, "auth", "logout", "--revoke")
		if err != nil {
			t.Fatalf("logout --revoke must succeed even when revocation fails: %v\n%s", err, out)
		}
		if requests != 1 {
			t.Errorf("server saw %d requests, want 1", requests)
		}
		if !strings.Contains(out, "Server-side revocation failed") {
			t.Errorf("output missing revocation warning:\n%s", out)
		}
		assertLoggedOut(t, cfgPath)
	})
	t.Run("server unreachable", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		url := server.URL
		server.Close() // nothing listens there anymore

		cfgPath := writeLogoutConfig(t, oauthLogoutFixture)
		out, err := runRootCfg(t, cfgPath, url, "auth", "logout", "--revoke")
		if err != nil {
			t.Fatalf("logout --revoke must succeed against a dead server: %v\n%s", err, out)
		}
		if !strings.Contains(out, "Server-side revocation failed") {
			t.Errorf("output missing revocation warning:\n%s", out)
		}
		assertLoggedOut(t, cfgPath)
	})
}

// TestLogoutPATWarnsInsteadOfRevoke pins the PAT path: no request is sent
// (the token-management API 403s every cpt_ credential), the warning names
// the Console path and the stored prefix so the user can find the row, and
// local state is still cleared.
func TestLogoutPATWarnsInsteadOfRevoke(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "") // keep the test hermetic
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
	}))
	defer server.Close()

	cfgPath := writeLogoutConfig(t, `{"auth":{"method":"token","accessToken":"cpt_pat1234567890","tokenPrefix":"cpt_pat123"}}`)
	out, err := runRootCfg(t, cfgPath, server.URL, "auth", "logout", "--revoke")
	if err != nil {
		t.Fatalf("logout --revoke with PAT: %v\n%s", err, out)
	}
	if requests != 0 {
		t.Errorf("server saw %d requests, want 0 (PATs have no CLI-reachable revocation)", requests)
	}
	for _, want := range []string{"Personal access tokens cannot be revoked", "Settings → API Tokens", "cpt_pat123"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	assertLoggedOut(t, cfgPath)
}

// TestLogoutPlainUnchanged pins that plain logout keeps its local-only
// behavior: no network call with a live OAuth credential on disk, no
// revocation claims in the output, config cleared.
func TestLogoutPlainUnchanged(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "") // keep the test hermetic
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
	}))
	defer server.Close()

	cfgPath := writeLogoutConfig(t, oauthLogoutFixture)
	out, err := runRootCfg(t, cfgPath, server.URL, "auth", "logout")
	if err != nil {
		t.Fatalf("plain logout: %v\n%s", err, out)
	}
	if requests != 0 {
		t.Errorf("plain logout made %d network requests, want 0", requests)
	}
	if strings.Contains(out, "Revoked") {
		t.Errorf("plain logout must not claim revocation:\n%s", out)
	}
	if !strings.Contains(out, "✓ Credentials removed from") {
		t.Errorf("output missing removal confirmation:\n%s", out)
	}
	assertLoggedOut(t, cfgPath)
}

// TestLogoutRevokeWithoutCredential covers --revoke with nothing stored:
// no request is possible, the command says so, and local context clearing
// still runs.
func TestLogoutRevokeWithoutCredential(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "") // keep the test hermetic
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
	}))
	defer server.Close()

	cfgPath := writeLogoutConfig(t, `{"defaultWorkspace":"ws_123"}`)
	out, err := runRootCfg(t, cfgPath, server.URL, "auth", "logout", "--revoke")
	if err != nil {
		t.Fatalf("logout --revoke without credential: %v\n%s", err, out)
	}
	if requests != 0 {
		t.Errorf("server saw %d requests, want 0", requests)
	}
	if !strings.Contains(out, "No stored credential to revoke") {
		t.Errorf("output missing no-credential note:\n%s", out)
	}
	assertLoggedOut(t, cfgPath)
}

// TestLogoutHelpDropsStaleRevocationGuidance pins the help-text fix: the
// removed text recommended a DELETE against /api/v1/console/tokens (403 for
// every CLI credential) and claimed the token management API did not exist
// yet. The help must document --revoke and the PAT limitation instead.
func TestLogoutHelpDropsStaleRevocationGuidance(t *testing.T) {
	out, err := runRootCfg(t, filepath.Join(t.TempDir(), "unused.json"), "https://api.example.invalid", "auth", "logout", "--help")
	if err != nil {
		t.Fatalf("auth logout --help: %v", err)
	}
	for _, banned := range []string{
		"once the token management API is available",
		"/api/v1/console/tokens",
	} {
		if strings.Contains(out, banned) {
			t.Errorf("help still carries the stale guidance %q:\n%s", banned, out)
		}
	}
	for _, want := range []string{"--revoke", "RFC 7009", "Settings → API Tokens"} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing %q:\n%s", want, out)
		}
	}
}
