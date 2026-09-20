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

	"github.com/CupThread/CupThreadAgenticCoding/internal/config"
)

// Issue #88: display names are not unique server-side (only ids and slugs
// are), so name-based references must resolve ambiguously only when they
// match exactly one record. These tests pin the lookupApp/lookupWorkspace
// contract: unique id/slug short-circuit, a single name match resolves,
// and an ambiguous name fails loudly — listing every candidate — without
// issuing mutating requests or persisting anything to the config.

// twoAppsSameNameFixture is the two-app reproduction from issue #88: both
// apps legally share the display name "Mobile App"; only their ids and
// slugs differ.
const twoAppsSameNameFixture = `{"apps":[` +
	`{"appId":"app_A","appKey":"key_live_a","slug":"mobile-prod","name":"Mobile App","allowPublic":true,"allowedPlatforms":["ios"],"maxAttachmentBytes":10485760},` +
	`{"appId":"app_B","appKey":"key_live_b","slug":"mobile-staging","name":"Mobile App","allowPublic":false,"allowedPlatforms":["ios"],"maxAttachmentBytes":10485760}` +
	`],"total":2}`

// distinctAppsFixture has multiple apps with unique names.
const distinctAppsFixture = `{"apps":[` +
	`{"appId":"app_1","appKey":"key_live_1","slug":"ios","name":"Acme iOS","allowPublic":true,"allowedPlatforms":["ios"],"maxAttachmentBytes":10485760},` +
	`{"appId":"app_2","appKey":"key_live_2","slug":"android","name":"Acme Android","allowPublic":true,"allowedPlatforms":["android"],"maxAttachmentBytes":10485760}` +
	`],"total":2}`

// idVsNameTwinFixture pins the resolution priority: app_A's display name is
// literally "app_B", so a bare name pass would see two matches for ref
// "app_B" — but the id/slug pass must short-circuit to the real app_B.
const idVsNameTwinFixture = `{"apps":[` +
	`{"appId":"app_A","appKey":"key_live_a","slug":"twin","name":"app_B","allowPublic":true,"allowedPlatforms":["ios"],"maxAttachmentBytes":10485760},` +
	`{"appId":"app_B","appKey":"key_live_b","slug":"real","name":"Real App","allowPublic":true,"allowedPlatforms":["ios"],"maxAttachmentBytes":10485760}` +
	`],"total":2}`

// twoWorkspacesSameNameMeFixture is the /console/me twin of the app
// fixture: two workspaces named "Acme".
const twoWorkspacesSameNameMeFixture = `{"clerkUserId":"user_1","email":null,"workspaces":[` +
	`{"workspace":{"id":"ws_first","name":"Acme","slug":"acme-prod","createdAt":"2026-09-01T00:00:00Z","updatedAt":"2026-09-01T00:00:00Z"},` +
	`"membership":{"id":"mem_1","workspaceId":"ws_first","clerkUserId":"user_1","role":"owner","displayName":null,"email":null,"createdAt":"2026-09-01T00:00:00Z","updatedAt":"2026-09-01T00:00:00Z"}},` +
	`{"workspace":{"id":"ws_second","name":"Acme","slug":"acme-lab","createdAt":"2026-09-02T00:00:00Z","updatedAt":"2026-09-02T00:00:00Z"},` +
	`"membership":{"id":"mem_2","workspaceId":"ws_second","clerkUserId":"user_1","role":"owner","displayName":null,"email":null,"createdAt":"2026-09-02T00:00:00Z","updatedAt":"2026-09-02T00:00:00Z"}}` +
	`]}`

// runRootWithConfig executes the CLI against serverURL with an explicit
// config path (runRoot's throwaway TempDir config is not observable), so
// tests can assert exactly what the command persisted.
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

// serveRecords dispatches the lookup endpoints the tests exercise and
// records every request's method and path.
func serveRecords(t *testing.T, appsJSON, meJSON string) (*httptest.Server, *[]string) {
	t.Helper()
	seen := &[]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/console/workspaces/ws_1/apps":
			_, _ = w.Write([]byte(appsJSON))
		case "/api/v1/console/me":
			_, _ = w.Write([]byte(meJSON))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	return server, seen
}

func TestLookupAppNameAmbiguousFailsClean(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	server, seen := serveRecords(t, twoAppsSameNameFixture, "{}")

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	seed := "{\"defaultWorkspace\":\"ws_1\"}\n"
	if err := os.WriteFile(cfgPath, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := runRootWithConfig(t, server.URL, cfgPath, "apps", "use", "Mobile App", "--workspace", "ws_1")
	if err == nil {
		t.Fatal("ambiguous name must fail, got nil error")
	}
	for _, want := range []string{
		`app "Mobile App" is ambiguous`,
		"matches 2 apps in this workspace",
		"Mobile App (app_A, slug mobile-prod)",
		"Mobile App (app_B, slug mobile-staging)",
		"retry with the app id or slug instead",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
	// Only the lookup GET may hit the wire — zero mutating requests.
	for _, req := range *seen {
		if req != "GET /api/v1/console/workspaces/ws_1/apps" {
			t.Errorf("unexpected request on the ambiguous path: %s", req)
		}
	}
	// The saved default must be untouched.
	got, readErr := os.ReadFile(cfgPath)
	if readErr != nil {
		t.Fatalf("read config: %v", readErr)
	}
	if string(got) != seed {
		t.Errorf("config mutated on the ambiguous path:\n%s\nwant:\n%s", got, seed)
	}
}

func TestLookupAppUniqueRefsStillResolve(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	server, _ := serveRecords(t, twoAppsSameNameFixture, "{}")

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	out, err := runRootWithConfig(t, server.URL, cfgPath, "apps", "use", "mobile-staging", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("apps use by slug: %v", err)
	}
	if !strings.Contains(out, "Default app for ws_1: Mobile App (app_B)") {
		t.Errorf("output missing resolved app:\n%s", out)
	}

	cfgPath2 := filepath.Join(t.TempDir(), "config.json")
	if _, err := runRootWithConfig(t, server.URL, cfgPath2, "apps", "use", "app_A", "--workspace", "ws_1"); err != nil {
		t.Fatalf("apps use by id: %v", err)
	}

	for path, want := range map[string]string{cfgPath: "app_B", cfgPath2: "app_A"} {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read config: %v", readErr)
		}
		var cfg config.Config
		if err := json.Unmarshal(data, &cfg); err != nil {
			t.Fatalf("parse config: %v", err)
		}
		prefs := cfg.Workspaces["ws_1"]
		if prefs == nil || prefs.DefaultApp != want {
			t.Errorf("config defaultApp = %+v, want %s", prefs, want)
		}
	}
}

func TestLookupAppSingleNameMatchResolves(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	server, _ := serveRecords(t, distinctAppsFixture, "{}")

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if _, err := runRootWithConfig(t, server.URL, cfgPath, "apps", "use", "Acme Android", "--workspace", "ws_1"); err != nil {
		t.Fatalf("apps use by unique name: %v", err)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if prefs := cfg.Workspaces["ws_1"]; prefs == nil || prefs.DefaultApp != "app_2" {
		t.Errorf("config defaultApp = %+v, want app_2", cfg.Workspaces["ws_1"])
	}
}

func TestLookupAppIDBeatsNameTwin(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	server, _ := serveRecords(t, idVsNameTwinFixture, "{}")

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	out, err := runRootWithConfig(t, server.URL, cfgPath, "apps", "use", "app_B", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("apps use by id with a name twin: %v", err)
	}
	if !strings.Contains(out, "Real App (app_B)") {
		t.Errorf("id reference should short-circuit to the real app_B, got:\n%s", out)
	}
}

func TestLookupWorkspaceNameAmbiguousFailsClean(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	server, seen := serveRecords(t, "{}", twoWorkspacesSameNameMeFixture)

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := runRootWithConfig(t, server.URL, cfgPath, "workspaces", "use", "Acme")
	if err == nil {
		t.Fatal("ambiguous name must fail, got nil error")
	}
	for _, want := range []string{
		`workspace "Acme" is ambiguous`,
		"matches 2 of your workspaces",
		"Acme (ws_first, slug acme-prod)",
		"Acme (ws_second, slug acme-lab)",
		"retry with the workspace id or slug instead",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
	for _, req := range *seen {
		if req != "GET /api/v1/console/me" {
			t.Errorf("unexpected request on the ambiguous path: %s", req)
		}
	}
	got, readErr := os.ReadFile(cfgPath)
	if readErr != nil {
		t.Fatalf("read config: %v", readErr)
	}
	if string(got) != "{}\n" {
		t.Errorf("config mutated on the ambiguous path:\n%s", got)
	}
}

func TestLookupWorkspaceUniqueRefsStillResolve(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	server, _ := serveRecords(t, "{}", twoWorkspacesSameNameMeFixture)

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	out, err := runRootWithConfig(t, server.URL, cfgPath, "workspaces", "use", "acme-lab")
	if err != nil {
		t.Fatalf("workspaces use by slug: %v", err)
	}
	if !strings.Contains(out, "Default workspace: Acme (ws_second)") {
		t.Errorf("output missing resolved workspace:\n%s", out)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if cfg.DefaultWorkspace != "ws_second" {
		t.Errorf("config defaultWorkspace = %q, want ws_second", cfg.DefaultWorkspace)
	}

	cfgPath2 := filepath.Join(t.TempDir(), "config.json")
	if _, err := runRootWithConfig(t, server.URL, cfgPath2, "workspaces", "use", "ws_first"); err != nil {
		t.Fatalf("workspaces use by id: %v", err)
	}
	data, err = os.ReadFile(cfgPath2)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if cfg.DefaultWorkspace != "ws_first" {
		t.Errorf("config defaultWorkspace = %q, want ws_first", cfg.DefaultWorkspace)
	}
}
