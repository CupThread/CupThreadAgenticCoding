package cmd

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/CupThread/CupThreadAgenticCoding/internal/config"
)

// runAuthRoot executes the CLI against an explicit config path so tests can
// seed state before a command and inspect the file afterwards. It pins the
// same config across invocations (runRoot in apps_test.go always starts from
// a fresh temp file); the distinct name avoids clashing with config-pinning
// helpers added by parallel PRs.
func runAuthRoot(t *testing.T, cfgPath, serverURL string, args ...string) (string, error) {
	t.Helper()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	root := newRootCmd()
	full := append(append([]string{}, args...), "--base-url", serverURL, "--config", cfgPath)
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

func seedAuthConfig(t *testing.T, cfgPath string, cfg *config.Config) {
	t.Helper()
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("seed config: %v", err)
	}
}

func readAuthConfig(t *testing.T, cfgPath string) *config.Config {
	t.Helper()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

// otherUserMeFixture is the /console/me payload for a second account whose
// only workspace (ws_b2) is invisible to the account that seeded the config.
const otherUserMeFixture = `{
	"clerkUserId": "user_B",
	"email": "b@example.com",
	"workspaces": [
		{
			"workspace": {
				"id": "ws_b2",
				"name": "Beta Work",
				"slug": "beta-work",
				"createdAt": "2026-09-03T00:00:00Z",
				"updatedAt": "2026-09-03T00:00:00Z"
			},
			"membership": {
				"id": "mem_b2",
				"workspaceId": "ws_b2",
				"clerkUserId": "user_B",
				"role": "owner",
				"displayName": null,
				"email": null,
				"createdAt": "2026-09-03T00:00:00Z",
				"updatedAt": "2026-09-03T00:00:00Z"
			},
			"subscription": null
		}
	]
}`

// TestLogoutClearsWorkspaceContext covers issue #82: logout must restore the
// pristine first-run config, not keep the previous user's default workspace,
// per-workspace app defaults or base URL next to no credential at all.
func TestLogoutClearsWorkspaceContext(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	seedAuthConfig(t, cfgPath, &config.Config{
		DefaultWorkspace: "ws_a",
		BaseURL:          "https://api.staging.example.com",
		Workspaces: map[string]*config.WorkspacePrefs{
			"ws_a": {DefaultApp: "app_1"},
			"ws_b": {DefaultApp: "app_2"},
		},
		Auth: &config.Auth{Method: "token", AccessToken: "cpt_userAAAA1111", TokenPrefix: "cpt_userAAAA"},
	})

	// Logout never calls the API, so the base URL is irrelevant here.
	out, err := runAuthRoot(t, cfgPath, "http://127.0.0.1:1", "auth", "logout")
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if !strings.Contains(out, "Credentials removed") {
		t.Errorf("logout output missing confirmation:\n%s", out)
	}
	if !strings.Contains(out, "default workspace ws_a") {
		t.Errorf("logout output should name the cleared workspace context:\n%s", out)
	}

	cfg := readAuthConfig(t, cfgPath)
	if cfg.Auth != nil {
		t.Errorf("auth survived logout: %+v", cfg.Auth)
	}
	if cfg.DefaultWorkspace != "" {
		t.Errorf("default workspace %q survived logout", cfg.DefaultWorkspace)
	}
	if len(cfg.Workspaces) != 0 {
		t.Errorf("per-workspace defaults survived logout: %v", cfg.Workspaces)
	}
	if cfg.BaseURL != "" {
		t.Errorf("base URL %q survived logout", cfg.BaseURL)
	}
}

// TestLoginResetsStaleDefaultWorkspace covers the account-switch scenario
// from issue #82: logging in as a user who cannot see the saved default
// workspace must clear it (and the invisible per-workspace defaults) with a
// warning, and a following workspace-scoped command must fail locally with
// the standard "no workspace selected" error instead of sending the new
// user's credential at the previous user's workspace.
func TestLoginResetsStaleDefaultWorkspace(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		_, _ = w.Write([]byte(otherUserMeFixture))
	}))
	defer server.Close()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	seedAuthConfig(t, cfgPath, &config.Config{
		DefaultWorkspace: "ws_a",
		Workspaces: map[string]*config.WorkspacePrefs{
			"ws_a": {DefaultApp: "app_1"},
			"ws_b": {DefaultApp: "app_2"},
		},
		Auth: &config.Auth{Method: "token", AccessToken: "cpt_userAAAA1111", TokenPrefix: "cpt_userAAAA"},
	})

	out, err := runAuthRoot(t, cfgPath, server.URL, "auth", "login", "--token", "cpt_userBBBB2222")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !strings.Contains(out, "ws_a") || !strings.Contains(out, "previous login") {
		t.Errorf("login output missing stale-default warning:\n%s", out)
	}

	cfg := readAuthConfig(t, cfgPath)
	if cfg.DefaultWorkspace != "" {
		t.Errorf("stale default workspace %q survived login by another user", cfg.DefaultWorkspace)
	}
	if len(cfg.Workspaces) != 0 {
		t.Errorf("stale per-workspace defaults survived login by another user: %v", cfg.Workspaces)
	}
	if cfg.Auth == nil || cfg.Auth.AccessToken != "cpt_userBBBB2222" {
		t.Errorf("new credential not stored: %+v", cfg.Auth)
	}

	// Apps list resolves the workspace before any HTTP call, so after the
	// switch it must fail locally — no request may reach the previous user's
	// workspace with the new user's credential.
	_, err = runAuthRoot(t, cfgPath, server.URL, "apps", "list")
	if err == nil {
		t.Fatal("apps list after account switch should fail without a default workspace")
	}
	if !strings.Contains(err.Error(), "no workspace selected") {
		t.Errorf("apps list error should be the standard no-workspace one, got: %v", err)
	}
	if len(requests) != 1 {
		t.Errorf("expected exactly the login /console/me probe on the wire, got: %v", requests)
	}
	for _, req := range requests {
		if strings.Contains(req, "ws_a") {
			t.Errorf("request reached the previous user's workspace: %v", requests)
		}
	}
}

// TestLoginKeepsValidDefaultWorkspace pins the other half of issue #82: a
// saved default the new account can still see is kept untouched, with no
// warning noise.
func TestLoginKeepsValidDefaultWorkspace(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(meFixture))
	}))
	defer server.Close()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	seedAuthConfig(t, cfgPath, &config.Config{
		DefaultWorkspace: "ws_1",
		Workspaces: map[string]*config.WorkspacePrefs{
			"ws_1": {DefaultApp: "app_9"},
		},
		Auth: &config.Auth{Method: "token", AccessToken: "cpt_userAAAA1111", TokenPrefix: "cpt_userAAAA"},
	})

	out, err := runAuthRoot(t, cfgPath, server.URL, "auth", "login", "--token", "cpt_userBBBB2222")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if strings.Contains(out, "⚠") {
		t.Errorf("login should not warn when the saved default is still valid:\n%s", out)
	}

	cfg := readAuthConfig(t, cfgPath)
	if cfg.DefaultWorkspace != "ws_1" {
		t.Errorf("valid default workspace was cleared, got %q", cfg.DefaultWorkspace)
	}
	if prefs := cfg.Workspaces["ws_1"]; prefs == nil || prefs.DefaultApp != "app_9" {
		t.Errorf("valid per-workspace default was dropped: %v", cfg.Workspaces)
	}
}

// TestRequireAppIDResetsWithWorkspace verifies the apps-use path from issue
// #82: after logout, requireAppID must not resolve the previous user's
// per-workspace default app.
func TestRequireAppIDResetsWithWorkspace(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	seedAuthConfig(t, cfgPath, &config.Config{
		DefaultWorkspace: "ws_a",
		Workspaces: map[string]*config.WorkspacePrefs{
			"ws_a": {DefaultApp: "app_1"},
		},
		Auth: &config.Auth{Method: "token", AccessToken: "cpt_userAAAA1111", TokenPrefix: "cpt_userAAAA"},
	})

	if _, err := runAuthRoot(t, cfgPath, "http://127.0.0.1:1", "auth", "logout"); err != nil {
		t.Fatalf("logout: %v", err)
	}
	// A still holds the logout invocation's freshly cleared config.
	if _, err := A.requireAppID(); err == nil {
		t.Fatal("requireAppID resolved an app after logout cleared the per-workspace defaults")
	} else if !strings.Contains(err.Error(), "no workspace selected") {
		t.Fatalf("unexpected requireAppID error: %v", err)
	}
}

// TestReconcileWorkspaceContextPartialOverlap pins the mixed case at the
// helper level: a default workspace the new account still sees stays (with
// its saved app default), while invisible per-workspace entries are dropped.
func TestReconcileWorkspaceContextPartialOverlap(t *testing.T) {
	A = &app{cfg: &config.Config{
		DefaultWorkspace: "ws_keep",
		Workspaces: map[string]*config.WorkspacePrefs{
			"ws_keep":  {DefaultApp: "app_1"},
			"ws_stale": {DefaultApp: "app_2"},
		},
	}}
	me := &api.MeResponse{
		Workspaces: []api.MeWorkspaceEntry{{Workspace: api.Workspace{ID: "ws_keep"}}},
	}
	var warnings []string
	reconcileWorkspaceContext(me, func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	})

	if A.cfg.DefaultWorkspace != "ws_keep" {
		t.Errorf("visible default workspace was cleared, got %q", A.cfg.DefaultWorkspace)
	}
	if len(A.cfg.Workspaces) != 1 || A.cfg.Workspaces["ws_keep"] == nil {
		t.Errorf("expected only the visible workspace to survive, got: %v", A.cfg.Workspaces)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "ws_stale") {
		t.Errorf("expected exactly one warning naming ws_stale, got: %q", warnings)
	}
}
