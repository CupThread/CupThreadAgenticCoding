package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CupThread/CupThreadAgenticCoding/internal/config"
)

// The tests in this file pin the issue #184 disclosure contract: while
// $CUPTHREAD_TOKEN overrides the effective credential, the two commands that
// change the stored credential must say so at the moment they run —
// 'auth logout' cannot claim a de-provisioning that has not happened, and
// 'auth login' cannot describe a credential that is not in effect. The
// warning goes to stderr in every output mode (exit code stays 0), the
// structured payloads carry envOverride / effectiveCredential only while the
// override is set, and without the variable every output stays byte-identical
// to the pre-#184 behavior.

const envOverrideToken = "cpt_env_override_123"

// logoutEnvOverrideWarning is the exact stderr line 'auth logout' prints in
// every output mode while $CUPTHREAD_TOKEN is set.
const logoutEnvOverrideWarning = "⚠ $CUPTHREAD_TOKEN is set and still authenticates every command — unset it to fully de-provision this machine"

// loginEnvOverrideWarning is the exact stderr line every 'auth login' method
// prints in every output mode while $CUPTHREAD_TOKEN is set.
const loginEnvOverrideWarning = "⚠ $CUPTHREAD_TOKEN is set and still authenticates every command — the freshly saved login stays inactive until it is unset"

// logoutEnvOverrideFixture seeds a stored credential plus inherited context,
// so the table-mode byte-identity assertion below also covers the cleared
// line printed after the removal confirmation.
func logoutEnvOverrideFixture(t *testing.T) string {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	pre := &config.Config{
		BaseURL:          "https://stale.example.com",
		DefaultWorkspace: "ws_1",
		Workspaces:       map[string]*config.WorkspacePrefs{"ws_1": {DefaultApp: "app_1"}},
		Auth:             &config.Auth{Method: "token", AccessToken: "cpt_stored_000000", TokenPrefix: "cpt_stored_00"},
	}
	if err := pre.Save(cfgPath); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	return cfgPath
}

// TestLogoutEnvTokenOverrideDisclosure pins the logout half of issue #184:
// with $CUPTHREAD_TOKEN set the config file is cleared exactly as before
// (exit code 0, no behavior change) but the command discloses on stderr — in
// every output mode — that every command keeps authenticating, and the
// structured document names the overriding variable in envOverride.
func TestLogoutEnvTokenOverrideDisclosure(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", envOverrideToken)

	t.Run("json", func(t *testing.T) {
		cfgPath := logoutEnvOverrideFixture(t)
		stdout, stderr, err := runRootCapture(t, cfgPath, "http://127.0.0.1:1", "auth", "logout", "--json")
		if err != nil {
			t.Fatalf("logout --json with env override: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		assertNoTableOutput(t, stdout)
		doc := unmarshalOneJSON(t, stdout)
		if doc["envOverride"] != "CUPTHREAD_TOKEN" {
			t.Errorf("envOverride = %v, want CUPTHREAD_TOKEN", doc["envOverride"])
		}
		if doc["loggedOut"] != true {
			t.Errorf("loggedOut = %v, want true", doc["loggedOut"])
		}
		if !strings.Contains(stderr, logoutEnvOverrideWarning) {
			t.Errorf("stderr missing override warning:\n%s", stderr)
		}
		assertLoggedOut(t, cfgPath)
	})

	t.Run("yaml", func(t *testing.T) {
		cfgPath := logoutEnvOverrideFixture(t)
		stdout, stderr, err := runRootCapture(t, cfgPath, "http://127.0.0.1:1", "auth", "logout", "--output", "yaml")
		if err != nil {
			t.Fatalf("logout --output yaml with env override: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		doc := unmarshalOneYAMLDocument(t, stdout)
		if doc["envOverride"] != "CUPTHREAD_TOKEN" {
			t.Errorf("envOverride = %v, want CUPTHREAD_TOKEN", doc["envOverride"])
		}
		if !strings.Contains(stderr, logoutEnvOverrideWarning) {
			t.Errorf("stderr missing override warning:\n%s", stderr)
		}
		assertLoggedOut(t, cfgPath)
	})

	t.Run("table", func(t *testing.T) {
		cfgPath := logoutEnvOverrideFixture(t)
		stdout, stderr, err := runRootCapture(t, cfgPath, "http://127.0.0.1:1", "auth", "logout")
		if err != nil {
			t.Fatalf("logout with env override: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		want := "✓ Credentials removed from " + cfgPath + "\n" +
			"  Cleared saved context from the previous login: default workspace ws_1, per-workspace app defaults, base URL\n"
		if stdout != want {
			t.Errorf("table stdout = %q, want %q", stdout, want)
		}
		if !strings.Contains(stderr, logoutEnvOverrideWarning) {
			t.Errorf("stderr missing override warning:\n%s", stderr)
		}
		assertLoggedOut(t, cfgPath)
	})
}

// TestLogoutWithoutEnvTokenStaysSilent pins the other half: without
// $CUPTHREAD_TOKEN neither the warning nor the envOverride field appears —
// structured stdout keeps exactly the pre-#184 field set.
func TestLogoutWithoutEnvTokenStaysSilent(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "")

	cfgPath := logoutEnvOverrideFixture(t)
	stdout, stderr, err := runRootCapture(t, cfgPath, "http://127.0.0.1:1", "auth", "logout", "--json")
	if err != nil {
		t.Fatalf("logout --json without env override: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	doc := unmarshalOneJSON(t, stdout)
	if _, ok := doc["envOverride"]; ok {
		t.Errorf("envOverride present without an env token: %v", doc["envOverride"])
	}
	if strings.Contains(stderr, "CUPTHREAD_TOKEN") {
		t.Errorf("stderr mentions the override without an env token:\n%s", stderr)
	}
	assertLoggedOut(t, cfgPath)
}

// TestLoginTokenEnvOverrideDisclosure pins the --token half of issue #184:
// the fresh credential is stored exactly as before, but the confirmation
// discloses on stderr (both modes) that the environment token is what
// requests actually use, and the JSON document carries effectiveCredential.
func TestLoginTokenEnvOverrideDisclosure(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", envOverrideToken)

	server := meServer(t)
	defer server.Close()

	t.Run("json", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.json")
		stdout, stderr, err := runRootCapture(t, cfgPath, server.URL, "auth", "login", "--token", "cpt_new_tok_12345", "--json")
		if err != nil {
			t.Fatalf("login --json with env override: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		assertNoTableOutput(t, stdout)
		doc := unmarshalOneJSON(t, stdout)
		if doc["effectiveCredential"] != "env ($CUPTHREAD_TOKEN)" {
			t.Errorf("effectiveCredential = %v, want env ($CUPTHREAD_TOKEN)", doc["effectiveCredential"])
		}
		if doc["tokenPrefix"] != "cpt_new_tok_" {
			t.Errorf("tokenPrefix = %v, want the fresh credential's prefix", doc["tokenPrefix"])
		}
		if !strings.Contains(stderr, loginEnvOverrideWarning) {
			t.Errorf("stderr missing override warning:\n%s", stderr)
		}

		data, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatalf("read config: %v", err)
		}
		var cfg config.Config
		if err := json.Unmarshal(data, &cfg); err != nil {
			t.Fatalf("parse config: %v", err)
		}
		if cfg.Auth == nil || cfg.Auth.AccessToken != "cpt_new_tok_12345" {
			t.Errorf("stored auth = %+v, want the fresh cpt_new_tok_12345 credential (disclosure must not change what is stored)", cfg.Auth)
		}
	})

	t.Run("table", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.json")
		stdout, stderr, err := runRootCapture(t, cfgPath, server.URL, "auth", "login", "--token", "cpt_new_tok_12345")
		if err != nil {
			t.Fatalf("login with env override: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		want := "✓ Logged in as dev@example.com (token cpt_new_tok_…) at " + server.URL + "\n"
		if stdout != want {
			t.Errorf("table stdout = %q, want %q", stdout, want)
		}
		if !strings.Contains(stderr, loginEnvOverrideWarning) {
			t.Errorf("stderr missing override warning:\n%s", stderr)
		}
	})
}

// TestLoginWithoutEnvTokenStaysSilent pins byte-identity for the no-override
// case: no warning line, no effectiveCredential field.
func TestLoginWithoutEnvTokenStaysSilent(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "")

	server := meServer(t)
	defer server.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	stdout, stderr, err := runRootCapture(t, cfgPath, server.URL, "auth", "login", "--token", "cpt_new_tok_12345", "--json")
	if err != nil {
		t.Fatalf("login --json without env override: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	doc := unmarshalOneJSON(t, stdout)
	if _, ok := doc["effectiveCredential"]; ok {
		t.Errorf("effectiveCredential present without an env token: %v", doc["effectiveCredential"])
	}
	if strings.Contains(stderr, "CUPTHREAD_TOKEN") {
		t.Errorf("stderr mentions the override without an env token:\n%s", stderr)
	}
}

// TestLoginDeviceEnvOverrideDisclosure covers the interactive funnel
// (finishOAuthLogin via the device flow) with the override set: the oauth
// pair is stored, the JSON document carries effectiveCredential, and the
// stderr progress stream carries the override warning after the verification
// prompt lines.
func TestLoginDeviceEnvOverrideDisclosure(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", envOverrideToken)

	verifyURI := "https://cupthread.example/activate"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/oauth/device/authorize":
			_, _ = w.Write([]byte(`{"device_code":"dev_code_1","user_code":"ABCD-EFGH","verification_uri":"` + verifyURI + `","interval":1,"expires_in":120}`))
		case r.URL.Path == "/api/v1/oauth/token":
			_, _ = w.Write([]byte(`{"access_token":"cpt_device_tok_0000","refresh_token":"cpt_device_refresh","token_type":"bearer","expires_in":3600,"scope":"console"}`))
		default:
			_, _ = w.Write([]byte(meFixture))
		}
	}))
	defer server.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	stdout, stderr, err := runRootCapture(t, cfgPath, server.URL, "auth", "login", "--device", "--json")
	if err != nil {
		t.Fatalf("login --device --json with env override: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	doc := unmarshalOneJSON(t, stdout)
	if doc["effectiveCredential"] != "env ($CUPTHREAD_TOKEN)" {
		t.Errorf("effectiveCredential = %v, want env ($CUPTHREAD_TOKEN)", doc["effectiveCredential"])
	}
	if !strings.Contains(stderr, loginEnvOverrideWarning) {
		t.Errorf("stderr missing override warning:\n%s", stderr)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if cfg.Auth == nil || cfg.Auth.Method != "oauth" || cfg.Auth.RefreshToken != "cpt_device_refresh" {
		t.Errorf("stored auth = %+v, want the oauth pair (disclosure must not change what is stored)", cfg.Auth)
	}
}

// TestLoginSessionCheckFailureStillDiscloses covers finishOAuthLogin's
// advisory-failure branch (the success path cannot run /console/me): the
// override warning and effectiveCredential appear exactly when
// $CUPTHREAD_TOKEN is set, and stay absent otherwise — while the just-issued
// pair is stored in both cases.
func TestLoginSessionCheckFailureStillDiscloses(t *testing.T) {
	newServer := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.URL.Path == "/api/v1/oauth/device/authorize":
				_, _ = w.Write([]byte(`{"device_code":"dev_code_1","user_code":"ABCD-EFGH","verification_uri":"https://cupthread.example/activate","interval":1,"expires_in":120}`))
			case r.URL.Path == "/api/v1/oauth/token":
				_, _ = w.Write([]byte(`{"access_token":"cpt_device_tok_0000","refresh_token":"cpt_device_refresh","token_type":"bearer","expires_in":3600,"scope":"console"}`))
			default:
				http.Error(w, "session check unavailable", http.StatusInternalServerError)
			}
		}))
	}

	t.Run("with env token", func(t *testing.T) {
		t.Setenv("CUPTHREAD_TOKEN", envOverrideToken)
		server := newServer()
		defer server.Close()

		cfgPath := filepath.Join(t.TempDir(), "config.json")
		stdout, stderr, err := runRootCapture(t, cfgPath, server.URL, "auth", "login", "--device", "--json")
		if err != nil {
			t.Fatalf("login --device --json with failed session check: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		doc := unmarshalOneJSON(t, stdout)
		if doc["effectiveCredential"] != "env ($CUPTHREAD_TOKEN)" {
			t.Errorf("effectiveCredential = %v, want env ($CUPTHREAD_TOKEN)", doc["effectiveCredential"])
		}
		if !strings.Contains(stderr, loginEnvOverrideWarning) {
			t.Errorf("stderr missing override warning:\n%s", stderr)
		}

		data, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatalf("read config: %v", err)
		}
		var cfg config.Config
		if err := json.Unmarshal(data, &cfg); err != nil {
			t.Fatalf("parse config: %v", err)
		}
		if cfg.Auth == nil || cfg.Auth.RefreshToken != "cpt_device_refresh" {
			t.Errorf("stored auth = %+v, want the issued oauth pair", cfg.Auth)
		}
	})

	t.Run("without env token", func(t *testing.T) {
		t.Setenv("CUPTHREAD_TOKEN", "")
		server := newServer()
		defer server.Close()

		cfgPath := filepath.Join(t.TempDir(), "config.json")
		stdout, stderr, err := runRootCapture(t, cfgPath, server.URL, "auth", "login", "--device", "--json")
		if err != nil {
			t.Fatalf("login --device --json with failed session check: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		doc := unmarshalOneJSON(t, stdout)
		if _, ok := doc["effectiveCredential"]; ok {
			t.Errorf("effectiveCredential present without an env token: %v", doc["effectiveCredential"])
		}
		if !strings.Contains(stderr, "could not verify the session") {
			t.Errorf("stderr missing the advisory session warning:\n%s", stderr)
		}
		if strings.Contains(stderr, "CUPTHREAD_TOKEN") {
			t.Errorf("stderr mentions the override without an env token:\n%s", stderr)
		}
	})
}

// TestAuthHelpDocumentsEnvTokenOverride pins the help-text half: both
// commands' Long help name $CUPTHREAD_TOKEN and its consequence, so the
// precedence is documented where the misleading action is invoked.
func TestAuthHelpDocumentsEnvTokenOverride(t *testing.T) {
	t.Run("login help", func(t *testing.T) {
		stdout, _, err := runRootCapture(t, filepath.Join(t.TempDir(), "unused.json"), "http://127.0.0.1:1", "auth", "login", "--help")
		if err != nil {
			t.Fatalf("auth login --help: %v", err)
		}
		if !strings.Contains(stdout, "$CUPTHREAD_TOKEN outranks any stored credential") {
			t.Errorf("login help missing the override precedence:\n%s", stdout)
		}
	})
	t.Run("logout help", func(t *testing.T) {
		stdout, _, err := runRootCapture(t, filepath.Join(t.TempDir(), "unused.json"), "http://127.0.0.1:1", "auth", "logout", "--help")
		if err != nil {
			t.Fatalf("auth logout --help: %v", err)
		}
		for _, want := range []string{"$CUPTHREAD_TOKEN is not touched", "envOverride"} {
			if !strings.Contains(stdout, want) {
				t.Errorf("logout help missing %q:\n%s", want, stdout)
			}
		}
	})
}
