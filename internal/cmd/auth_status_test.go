package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// statusMeFixture answers GET /api/v1/console/me with the account the env
// token belongs to (account B in issue #57).
const statusMeFixture = `{"clerkUserId":"user_b","email":"b@example.com","workspaces":[]}`

// statusStoredToken is account A's OAuth access token seeded into the config
// file; $CUPTHREAD_TOKEN must override it at runtime.
const statusStoredToken = "cpt_storedAAAAAAAAAAAAAA"

// writeStatusStoredLogin seeds a config file holding account A's OAuth login
// and returns its path.
func writeStatusStoredLogin(t *testing.T) string {
	t.Helper()
	cfg := `{
		"auth": {
			"method": "oauth",
			"accessToken": "` + statusStoredToken + `",
			"refreshToken": "rt_stored",
			"expiresAt": "2027-01-01T00:00:00Z",
			"tokenPrefix": "cpt_storedAA",
			"clientId": "client_stored"
		},
		"defaultWorkspace": "ws_a"
	}`
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// runRootWithConfig is runRoot with an explicit config file, so tests can
// pre-seed a stored login (runRoot always points --config at a fresh temp
// file).
func runRootWithConfig(t *testing.T, serverURL, configPath string, args ...string) (string, error) {
	t.Helper()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	root := newRootCmd()
	full := append(append([]string{}, args...), "--base-url", serverURL, "--config", configPath)
	root.SetArgs(full)
	execErr := root.Execute()

	os.Stdout = oldStdout
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return string(out), execErr
}

// statusMeServer serves /api/v1/console/me and records the Authorization
// header the probe actually sent.
func statusMeServer(t *testing.T, gotAuth *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(statusMeFixture))
	}))
}

// authStatusJSON mirrors the auth status --json schema, including the
// stored* override fields from issue #57.
type authStatusJSON struct {
	BaseURL     string `json:"baseUrl"`
	Method      string `json:"method"`
	TokenPrefix string `json:"tokenPrefix"`
	ExpiresAt   string `json:"expiresAt"`

	StoredMethod      string `json:"storedMethod"`
	StoredTokenPrefix string `json:"storedTokenPrefix"`
	StoredExpiresAt   string `json:"storedExpiresAt"`

	User string `json:"user"`
}

func decodeAuthStatus(t *testing.T, out string) authStatusJSON {
	t.Helper()
	var row authStatusJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &row); err != nil {
		t.Fatalf("decode auth status JSON %q: %v", out, err)
	}
	return row
}

// TestAuthStatusEnvTokenWinsOverStoredLogin pins the fix for issue #57: when
// $CUPTHREAD_TOKEN and a stored login coexist, the primary credential is the
// env token and the stored login is demoted to stored* fields. This fails on
// the pre-fix behavior, which overwrote method/token/expiry with the stored
// config's account A while probing with account B's env token.
func TestAuthStatusEnvTokenWinsOverStoredLogin(t *testing.T) {
	envToken := "cpt_envBBBBBBBBBBBBBBBB"
	var gotAuth string
	server := statusMeServer(t, &gotAuth)
	defer server.Close()

	cfgPath := writeStatusStoredLogin(t)
	t.Setenv("CUPTHREAD_TOKEN", envToken)

	out, err := runRootWithConfig(t, server.URL, cfgPath, "auth", "status", "--json")
	if err != nil {
		t.Fatalf("auth status --json: %v", err)
	}

	row := decodeAuthStatus(t, out)
	want := authStatusJSON{
		BaseURL:           server.URL,
		Method:            "token ($CUPTHREAD_TOKEN)",
		TokenPrefix:       "cpt_envBBBBB",
		StoredMethod:      "oauth",
		StoredTokenPrefix: "cpt_storedAA",
		StoredExpiresAt:   "2027-01-01T00:00:00Z",
		User:              "b@example.com",
	}
	if row != want {
		t.Errorf("status row =\n  %+v\nwant\n  %+v", row, want)
	}
	if gotAuth != "Bearer "+envToken {
		t.Errorf("probe Authorization = %q, want the env token", gotAuth)
	}
}

// TestAuthStatusEnvTokenOnly verifies the env-token-only shape: no stored
// fields, and expiresAt stays empty because env tokens carry no client-side
// expiry.
func TestAuthStatusEnvTokenOnly(t *testing.T) {
	envToken := "cpt_envBBBBBBBBBBBBBBBB"
	var gotAuth string
	server := statusMeServer(t, &gotAuth)
	defer server.Close()

	t.Setenv("CUPTHREAD_TOKEN", envToken)
	cfgPath := filepath.Join(t.TempDir(), "config.json") // does not exist

	out, err := runRootWithConfig(t, server.URL, cfgPath, "auth", "status", "--json")
	if err != nil {
		t.Fatalf("auth status --json: %v", err)
	}

	row := decodeAuthStatus(t, out)
	want := authStatusJSON{
		BaseURL:     server.URL,
		Method:      "token ($CUPTHREAD_TOKEN)",
		TokenPrefix: "cpt_envBBBBB",
		User:        "b@example.com",
	}
	if row != want {
		t.Errorf("status row =\n  %+v\nwant\n  %+v", row, want)
	}
	if gotAuth != "Bearer "+envToken {
		t.Errorf("probe Authorization = %q, want the env token", gotAuth)
	}
}

// TestAuthStatusStoredLoginOnly verifies the stored-config-only shape is
// unchanged from the pre-fix behavior (env token unset).
func TestAuthStatusStoredLoginOnly(t *testing.T) {
	var gotAuth string
	server := statusMeServer(t, &gotAuth)
	defer server.Close()

	cfgPath := writeStatusStoredLogin(t)
	t.Setenv("CUPTHREAD_TOKEN", "")

	out, err := runRootWithConfig(t, server.URL, cfgPath, "auth", "status", "--json")
	if err != nil {
		t.Fatalf("auth status --json: %v", err)
	}

	row := decodeAuthStatus(t, out)
	want := authStatusJSON{
		BaseURL:     server.URL,
		Method:      "oauth",
		TokenPrefix: "cpt_storedAA",
		ExpiresAt:   "2027-01-01T00:00:00Z",
		User:        "b@example.com",
	}
	if row != want {
		t.Errorf("status row =\n  %+v\nwant\n  %+v", row, want)
	}
	if gotAuth != "Bearer "+statusStoredToken {
		t.Errorf("probe Authorization = %q, want the stored access token", gotAuth)
	}

	// Table mode shows the same precedence without a stored-login row.
	table, err := runRootWithConfig(t, server.URL, cfgPath, "auth", "status")
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	for _, want := range []string{"oauth", "cpt_storedAA", "2027-01-01T00:00:00Z"} {
		if !strings.Contains(table, want) {
			t.Errorf("table output missing %q:\n%s", want, table)
		}
	}
	if strings.Contains(table, "Stored login") {
		t.Errorf("table output shows a stored-login row without an env override:\n%s", table)
	}
}

// TestAuthStatusNotLoggedIn verifies the empty shape is unchanged.
func TestAuthStatusNotLoggedIn(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected probe request without credentials")
	}))
	defer server.Close()

	t.Setenv("CUPTHREAD_TOKEN", "")
	cfgPath := filepath.Join(t.TempDir(), "config.json") // does not exist

	out, err := runRootWithConfig(t, server.URL, cfgPath, "auth", "status", "--json")
	if err != nil {
		t.Fatalf("auth status --json: %v", err)
	}

	row := decodeAuthStatus(t, out)
	want := authStatusJSON{
		BaseURL: server.URL,
		Method:  "not logged in",
	}
	if row != want {
		t.Errorf("status row =\n  %+v\nwant\n  %+v", row, want)
	}
}

// TestAuthStatusTableShowsStoredLoginInactive verifies the human table
// reports the env token as primary and demotes the stored login to a clearly
// labeled inactive row.
func TestAuthStatusTableShowsStoredLoginInactive(t *testing.T) {
	var gotAuth string
	server := statusMeServer(t, &gotAuth)
	defer server.Close()

	cfgPath := writeStatusStoredLogin(t)
	t.Setenv("CUPTHREAD_TOKEN", "cpt_envBBBBBBBBBBBBBBBB")

	out, err := runRootWithConfig(t, server.URL, cfgPath, "auth", "status")
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	for _, want := range []string{
		"token ($CUPTHREAD_TOKEN)",
		"cpt_envBBBBB",
		"Stored login (inactive — overridden by $CUPTHREAD_TOKEN)",
		"oauth",
		"cpt_storedAA",
		"2027-01-01T00:00:00Z",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
}
