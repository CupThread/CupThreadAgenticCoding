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
	"time"

	"github.com/CupThread/CupThreadAgenticCoding/internal/auth"
	"github.com/CupThread/CupThreadAgenticCoding/internal/config"
	"github.com/CupThread/CupThreadAgenticCoding/internal/output"
)

// runRootWithCfgPath executes the CLI with an explicit config path and no
// implicit --base-url, so base-URL resolution follows the real precedence
// chain (flag → $CUPTHREAD_BASE_URL → config → default). Set env values with
// t.Setenv before calling.
func runRootWithCfgPath(t *testing.T, cfgPath string, args ...string) (string, error) {
	t.Helper()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	// Flag globals persist across Execute calls in one test binary; reset
	// the ones this helper leaves unset so runs are hermetic.
	flagBaseURL = ""
	flagWorkspace = ""
	flagApp = ""
	flagJSON = false
	flagOutput = ""

	root := newRootCmd()
	full := append(append([]string{}, args...), "--config", cfgPath)
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

// meServer is a mock API answering /console/me (and everything else) with
// the standard me fixture.
func meServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(meFixture))
	}))
}

// TestLoginTokenPersistsNonDefaultBaseURL pins the core of issue #81: a
// login against a non-default endpoint stores that endpoint, and a later
// bare invocation resolves to it instead of production.
func TestLoginTokenPersistsNonDefaultBaseURL(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "")
	t.Setenv("CUPTHREAD_BASE_URL", "")

	server := meServer(t)
	defer server.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	out, err := runRootWithCfgPath(t, cfgPath, "auth", "login",
		"--token", "cpt_persist_tok_01", "--base-url", server.URL)
	if err != nil {
		t.Fatalf("auth login: %v", err)
	}
	if !strings.Contains(out, "✓ Logged in as") {
		t.Errorf("login output missing success line:\n%s", out)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if cfg.BaseURL != server.URL {
		t.Errorf("stored baseUrl = %q, want %q", cfg.BaseURL, server.URL)
	}
	if cfg.Auth == nil || cfg.Auth.AccessToken != "cpt_persist_tok_01" {
		t.Errorf("stored auth = %+v, want token cpt_persist_tok_01", cfg.Auth)
	}

	// A bare invocation (no flag, no env) must reach the stored endpoint.
	out, err = runRootWithCfgPath(t, cfgPath, "auth", "status")
	if err != nil {
		t.Fatalf("bare auth status: %v", err)
	}
	for _, want := range []string{server.URL, "Credential issued for", "dev@example.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q:\n%s", want, out)
		}
	}

	// Structured output carries the issued-for URL as its own field.
	out, err = runRootWithCfgPath(t, cfgPath, "auth", "status", "--json")
	if err != nil {
		t.Fatalf("json auth status: %v", err)
	}
	if !strings.Contains(out, `"issuedBaseUrl": "`+server.URL+`"`) {
		t.Errorf("json status missing issuedBaseUrl %q:\n%s", server.URL, out)
	}
}

// TestRememberLoginBaseURLOmitsDefault verifies the default URL is never
// written to the config (keeping it self-healing) and a trailing slash on an
// override is trimmed.
func TestRememberLoginBaseURLOmitsDefault(t *testing.T) {
	t.Setenv("CUPTHREAD_BASE_URL", "")

	oldFlag := flagBaseURL
	defer func() { flagBaseURL = oldFlag }()

	// No flag/env and nothing stored: effective URL is the default, so the
	// config stays empty and keeps following config.DefaultBaseURL.
	a := &app{cfg: &config.Config{}}
	flagBaseURL = ""
	a.rememberLoginBaseURL()
	if a.cfg.BaseURL != "" {
		t.Errorf("default login stored baseUrl = %q, want empty", a.cfg.BaseURL)
	}

	// Logging in against the default endpoint explicitly (flag equals the
	// default) drops any previously stored non-default value.
	a = &app{cfg: &config.Config{BaseURL: "https://stale.example.com"}}
	flagBaseURL = config.DefaultBaseURL
	a.rememberLoginBaseURL()
	if a.cfg.BaseURL != "" {
		t.Errorf("default-flag login stored baseUrl = %q, want empty", a.cfg.BaseURL)
	}

	// Non-default flag: stored, trailing slash trimmed.
	flagBaseURL = "http://127.0.0.1:8081/"
	a.rememberLoginBaseURL()
	if want := "http://127.0.0.1:8081"; a.cfg.BaseURL != want {
		t.Errorf("stored baseUrl = %q, want %q", a.cfg.BaseURL, want)
	}
}

// TestLogoutClearsRememberedBaseURL verifies logout forgets the stored
// endpoint together with the credential, so the next login starts from the
// default environment.
func TestLogoutClearsRememberedBaseURL(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "")
	t.Setenv("CUPTHREAD_BASE_URL", "")

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	pre := &config.Config{
		BaseURL: "https://staging.cupthread.example",
		Auth:    &config.Auth{Method: "token", AccessToken: "cpt_staging", TokenPrefix: "cpt_staging"},
	}
	if err := pre.Save(cfgPath); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	if _, err := runRootWithCfgPath(t, cfgPath, "auth", "logout"); err != nil {
		t.Fatalf("auth logout: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if cfg.BaseURL != "" {
		t.Errorf("baseUrl after logout = %q, want empty", cfg.BaseURL)
	}
	if cfg.Auth != nil {
		t.Errorf("auth after logout = %+v, want nil", cfg.Auth)
	}
}

// TestBaseURLEnvOverridesStored pins the precedence contract: an explicit
// $CUPTHREAD_BASE_URL beats a stored non-default endpoint, so one-off pivots
// keep working after the fix.
func TestBaseURLEnvOverridesStored(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "")

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	pre := &config.Config{
		// Unroutable: if the stored value won, the probe fails fast.
		BaseURL: "http://127.0.0.1:1",
		Auth:    &config.Auth{Method: "token", AccessToken: "cpt_tok", TokenPrefix: "cpt_tok"},
	}
	if err := pre.Save(cfgPath); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(meFixture))
	}))
	defer server.Close()

	t.Setenv("CUPTHREAD_BASE_URL", server.URL)
	out, err := runRootWithCfgPath(t, cfgPath, "auth", "status")
	if err != nil {
		t.Fatalf("auth status with env override: %v", err)
	}
	if gotPath != "/api/v1/console/me" {
		t.Errorf("probe path = %q, want the env-pointed mock to serve /api/v1/console/me", gotPath)
	}
	if !strings.Contains(out, server.URL) {
		t.Errorf("status output missing env URL %q:\n%s", server.URL, out)
	}
}

// TestOAuthLoginPersistsBaseURL drives finishOAuthLogin against a mock and
// verifies the OAuth pair and the non-default endpoint are persisted
// together.
func TestOAuthLoginPersistsBaseURL(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "")
	t.Setenv("CUPTHREAD_BASE_URL", "")

	server := meServer(t)
	defer server.Close()

	oldFlag := flagBaseURL
	oldA := A
	defer func() {
		flagBaseURL = oldFlag
		A = oldA
	}()
	flagBaseURL = server.URL

	cfgPath := filepath.Join(t.TempDir(), "config.json")

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	A = &app{out: output.New(os.Stdout, output.FormatTable), cfg: &config.Config{}, cfgPath: cfgPath}
	A.client = A.buildClient()

	loginErr := finishOAuthLogin(&auth.TokenSet{
		AccessToken:  "cpt_oauth_access_tok",
		RefreshToken: "cpt_oauth_refresh_tok",
		ExpiresIn:    3600,
	})

	os.Stdout = oldStdout
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	if _, err := io.ReadAll(r); err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	if loginErr != nil {
		t.Fatalf("finishOAuthLogin: %v", loginErr)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if cfg.BaseURL != server.URL {
		t.Errorf("stored baseUrl = %q, want %q", cfg.BaseURL, server.URL)
	}
	if cfg.Auth == nil || cfg.Auth.Method != "oauth" || cfg.Auth.RefreshToken != "cpt_oauth_refresh_tok" {
		t.Errorf("stored auth = %+v, want oauth pair with refresh token", cfg.Auth)
	}
}

// TestRefreshUsesStoredBaseURL verifies the transparent token refresh inside
// every authenticated command targets the stored endpoint when no flag/env
// override is present — the misroute that sent staging refresh tokens to
// production before the fix.
func TestRefreshUsesStoredBaseURL(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "")
	t.Setenv("CUPTHREAD_BASE_URL", "")

	var tokenPostSeen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/oauth/token" {
			tokenPostSeen = true
			_, _ = w.Write([]byte(`{"access_token":"cpt_fresh_access","refresh_token":"cpt_fresh_refresh","token_type":"bearer","expires_in":3600,"scope":"console"}`))
			return
		}
		_, _ = w.Write([]byte(meFixture))
	}))
	defer server.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	pre := &config.Config{
		BaseURL: server.URL,
		Auth: &config.Auth{
			Method:       "oauth",
			AccessToken:  "cpt_stale_access",
			RefreshToken: "cpt_stale_refresh",
			ExpiresAt:    time.Now().Add(-5 * time.Minute).UTC().Format(time.RFC3339),
			ClientID:     auth.FirstPartyClientID,
		},
	}
	if err := pre.Save(cfgPath); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	if _, err := runRootWithCfgPath(t, cfgPath, "auth", "status"); err != nil {
		t.Fatalf("auth status with expired oauth token: %v", err)
	}
	if !tokenPostSeen {
		t.Error("refresh POST never reached /api/v1/oauth/token on the stored base URL")
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if cfg.Auth == nil || cfg.Auth.RefreshToken != "cpt_fresh_refresh" {
		t.Errorf("rotated refresh token = %+v, want cpt_fresh_refresh", cfg.Auth)
	}
}
