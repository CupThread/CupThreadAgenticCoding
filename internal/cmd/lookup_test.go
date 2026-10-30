package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/CupThread/CupThreadAgenticCoding/internal/config"
	"github.com/CupThread/CupThreadAgenticCoding/internal/output"
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

// serveScopedTraffic records every request's X-Workspace-Id header and path
// as "<header> <path>", serving the given apps payload for any
// workspace-scoped /apps path and an empty /console/me (issue #379's
// observations).
func serveScopedTraffic(t *testing.T, appsJSON string) (*httptest.Server, *[]string) {
	t.Helper()
	seen := &[]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.Header.Get("X-Workspace-Id")+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/console/workspaces/"):
			_, _ = w.Write([]byte(appsJSON))
		case r.URL.Path == "/api/v1/console/me":
			_, _ = w.Write([]byte("{}"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	return server, seen
}

// TestLookupAppDoesNotRescopeSharedClient pins the issue #379 invariant at
// the command level: lookupApp scopes its own request, not the process-wide
// client. The run resolves the workspace from the saved default (no
// --workspace flag), so buildClient seeds A.client.WorkspaceID as "" — the
// lookup must carry ws_1 only on its own request, never by writing the
// shared client in place. Before the fix the lookup aliased the pointer and
// A.client.WorkspaceID became ws_1 for the rest of the process.
func TestLookupAppDoesNotRescopeSharedClient(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	server, seen := serveScopedTraffic(t, distinctAppsFixture)

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte("{\"defaultWorkspace\":\"ws_1\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runRootWithConfig(t, server.URL, cfgPath, "apps", "get", "Acme iOS")
	if err != nil {
		t.Fatalf("apps get: %v", err)
	}
	if !strings.Contains(out, "Acme iOS") {
		t.Errorf("output missing resolved app:\n%s", out)
	}
	// The lookup request went out scoped to the resolved workspace: the
	// wsPath segment is the scope, so no X-Workspace-Id header rides along.
	if len(*seen) != 1 || (*seen)[0] != " /api/v1/console/workspaces/ws_1/apps" {
		t.Errorf("wire requests = %v, want one ws_1-scoped /apps GET", *seen)
	}
	// The shared client kept the scope buildClient gave it (the flag was
	// empty, so "" — not the lookup's ws_1).
	if A.client.WorkspaceID != "" {
		t.Errorf("shared client WorkspaceID = %q after lookupApp, want \"\" (buildClient's flag-derived scope)", A.client.WorkspaceID)
	}
}

// TestLookupAppScopesPerCall exercises the per-call copy directly (issue
// #379): two lookups resolving different workspaces must each target their
// own workspace-scoped path, and a later request through the shared client
// must carry no workspace scope at all — before the fix it silently rode the
// last lookup's leaked ws_second.
func TestLookupAppScopesPerCall(t *testing.T) {
	oldA := A
	defer func() { A = oldA }()
	oldWS := flagWorkspace
	flagWorkspace = ""
	defer func() { flagWorkspace = oldWS }()

	server, seen := serveScopedTraffic(t, distinctAppsFixture)
	A = &app{
		out: output.New(io.Discard, output.FormatTable),
		cfg: &config.Config{DefaultWorkspace: "ws_first"},
	}
	A.client = api.New(server.URL)
	A.client.Token = func(context.Context) (string, error) { return "cpt_test", nil }

	for _, tc := range []struct {
		ws, ref string
	}{
		{"ws_first", "app_1"},
		{"ws_second", "app_2"},
	} {
		A.cfg.DefaultWorkspace = tc.ws
		appRec, err := A.lookupApp(context.Background(), tc.ref)
		if err != nil {
			t.Fatalf("lookupApp %q in %s: %v", tc.ref, tc.ws, err)
		}
		if appRec.AppID != tc.ref {
			t.Errorf("lookupApp %q returned %q", tc.ref, appRec.AppID)
		}
	}
	// A request through the shared client afterwards: /console/me is not
	// workspace-scoped, so it exposes whatever scope the shared client
	// carries — which must still be buildClient's "".
	var me api.MeResponse
	if err := A.client.Do(context.Background(), "GET", "/api/v1/console/me", nil, nil, &me); err != nil {
		t.Fatalf("shared-client /console/me: %v", err)
	}
	want := []string{
		" /api/v1/console/workspaces/ws_first/apps",
		" /api/v1/console/workspaces/ws_second/apps",
		" /api/v1/console/me",
	}
	if !reflect.DeepEqual(*seen, want) {
		t.Errorf("wire requests = %v\n     want = %v (no X-Workspace-Id on any request)", *seen, want)
	}
	if A.client.WorkspaceID != "" {
		t.Errorf("shared client WorkspaceID = %q after two lookups, want \"\"", A.client.WorkspaceID)
	}
}
