package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #309: a config file that carries an explicit JSON null for a
// Workspaces entry decodes to a nil *WorkspacePrefs, and four cmd-layer
// readers dereferenced it unchecked — 'auth status' (the CLI's most basic
// informational command), the --workspace app resolution behind every
// app-scoped command, 'features list', and the 'apps list' default mark —
// so each died with a SIGSEGV before doing anything else. These tests drive
// the crash scenarios against a null-entry config fixture and pin the clean
// post-fix behavior: a normal result or the standard "no app selected"
// error, never a panic. Reverting the config.Load normalization fails all
// of them on main (the panics kill the test process).

// nullPrefsConfig seeds a config holding the null Workspaces entry from the
// issue reproduction and returns its path.
func nullPrefsConfig(t *testing.T) string {
	t.Helper()
	cfg := `{"defaultWorkspace":"ws_1","workspaces":{"ws_1":null},` +
		`"auth":{"method":"token","accessToken":"cpt_x","tokenPrefix":"cpt_x"}}`
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// nullPrefsStatusJSON mirrors the auth status --json fields this test pins,
// including the defaultApp row that used to crash the command.
type nullPrefsStatusJSON struct {
	BaseURL          string `json:"baseUrl"`
	Method           string `json:"method"`
	TokenPrefix      string `json:"tokenPrefix"`
	DefaultWorkspace string `json:"defaultWorkspace"`
	DefaultApp       string `json:"defaultApp"`
	User             string `json:"user"`
}

func TestNullWorkspacePrefsAuthStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/console/me" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"clerkUserId":"user_b","email":"b@example.com","workspaces":[]}`))
	}))
	defer server.Close()

	t.Setenv("CUPTHREAD_TOKEN", "")
	cfgPath := nullPrefsConfig(t)

	out, err := runRootWithConfig(t, server.URL, cfgPath, "auth", "status", "--json")
	if err != nil {
		t.Fatalf("auth status with a null workspaces entry: %v", err)
	}
	var row nullPrefsStatusJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &row); err != nil {
		t.Fatalf("decode auth status JSON %q: %v", out, err)
	}
	want := nullPrefsStatusJSON{
		BaseURL:          server.URL,
		Method:           "token",
		TokenPrefix:      "cpt_x",
		DefaultWorkspace: "ws_1",
		User:             "b@example.com",
	}
	if row != want {
		t.Errorf("status row =\n  %+v\nwant\n  %+v", row, want)
	}
}

func TestNullWorkspacePrefsFeaturesList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/workspaces/ws_1/feature-requests") {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"requests":[],"total":0}`))
	}))
	defer server.Close()

	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	cfgPath := nullPrefsConfig(t)

	out, err := runRootWithConfig(t, server.URL, cfgPath, "features", "list", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("features list with a null workspaces entry: %v", err)
	}
	if !strings.Contains(out, "0 total") {
		t.Errorf("features list output missing the empty total:\n%s", out)
	}
}

func TestNullWorkspacePrefsAppScopedCommandFailsClean(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s — app resolution must fail before the wire", r.Method, r.URL.Path)
	}))
	defer server.Close()

	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	cfgPath := nullPrefsConfig(t)

	_, err := runRootWithConfig(t, server.URL, cfgPath, "columns", "list", "--workspace", "ws_1")
	if err == nil {
		t.Fatal("columns list without an app must fail with the standard error, got nil")
	}
	if !strings.Contains(err.Error(), "no app selected") {
		t.Errorf("error = %v, want the standard no-app-selected message", err)
	}
}

func TestNullWorkspacePrefsAppsListTable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/console/workspaces/ws_1/apps" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"apps":[{"appId":"app_1","appKey":"key_live_1","slug":"ios",` +
			`"name":"Acme iOS","allowPublic":true,"allowedPlatforms":["ios"],"maxAttachmentBytes":10485760}],"total":1}`))
	}))
	defer server.Close()

	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	cfgPath := nullPrefsConfig(t)

	// The human table dereferences the prefs entry to render the default-app
	// mark; the null entry must render as "no default", not a panic.
	out, err := runRootWithConfig(t, server.URL, cfgPath, "apps", "list", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("apps list with a null workspaces entry: %v", err)
	}
	if !strings.Contains(out, "Acme iOS") {
		t.Errorf("apps list output missing the app row:\n%s", out)
	}
	if strings.Contains(out, " *") {
		t.Errorf("apps list marked a default app despite the null prefs entry:\n%s", out)
	}
}
