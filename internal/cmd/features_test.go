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

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
)

// adminRequestsFixture mirrors the console feature-request listing payload
// with two requests belonging to different apps (app_a and app_b) in the same
// workspace — what the server returns when no appId filter narrows the page.
const adminRequestsFixture = `{
	"requests": [{
		"id": "fr_a_1",
		"appId": "app_a",
		"title": "App A request",
		"description": "Belongs to app a",
		"status": "open",
		"voteCount": 3,
		"createdAt": "2026-09-01T12:00:00.000Z",
		"updatedAt": "2026-09-01T12:00:00.000Z"
	}, {
		"id": "fr_b_1",
		"appId": "app_b",
		"title": "App B request",
		"description": "Belongs to app b",
		"status": "open",
		"voteCount": 5,
		"createdAt": "2026-09-02T12:00:00.000Z",
		"updatedAt": "2026-09-02T12:00:00.000Z"
	}],
	"total": 2
}`

// defaultAppConfig is a config with the saved default workspace/app context
// that `workspaces use` + `apps use` would produce.
const defaultAppConfig = `{"defaultWorkspace":"ws_1","workspaces":{"ws_1":{"defaultApp":"app_a"}}}`

// filteredFixture encodes the fixture records under appID (all records when
// appID is empty), reproducing the server's appId filtering.
func filteredFixture(t *testing.T, appID string) []byte {
	t.Helper()
	var resp struct {
		Requests []api.AdminFeatureRequest `json:"requests"`
		Total    int                       `json:"total"`
	}
	if err := json.Unmarshal([]byte(adminRequestsFixture), &resp); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if appID != "" {
		filtered := resp.Requests[:0]
		for _, req := range resp.Requests {
			if req.AppID == appID {
				filtered = append(filtered, req)
			}
		}
		resp.Requests = filtered
	}
	resp.Total = len(resp.Requests)
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return body
}

// requestsHandler answers the console feature-requests listing like the real
// API: records are filtered by the appId query filter. Non-GET requests (the
// mutations and the forward endpoint) are counted in *mutations so tests can
// assert none was issued; forwardedTo, when non-nil, receives the path of any
// /github/forward call and gets a success payload back.
func requestsHandler(t *testing.T, mutations *int, forwardedTo *string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/github/forward") {
			if forwardedTo != nil {
				*forwardedTo = r.URL.Path
			}
			_, _ = w.Write([]byte(`{"success":true,"targetType":"discussion","url":"https://github.com/acme/ios/discussions/9"}`))
			return
		}
		if r.Method != http.MethodGet && mutations != nil {
			*mutations++
		}
		_, _ = w.Write(filteredFixture(t, r.URL.Query().Get("appId")))
	}
}

// runRootWithSeededConfig behaves like runRoot but seeds the throwaway config file
// with cfgJSON, so tests can exercise the saved default workspace/app path.
func runRootWithSeededConfig(t *testing.T, serverURL, cfgJSON string, args ...string) (string, error) {
	t.Helper()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

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

// TestFeaturesGetScopesToSavedDefaultApp covers the issue #86 core case: with
// a saved default app, ID resolution must send appId=app_a and must not
// resolve a request that belongs to a different app, even though the
// workspace-wide listing would return it.
func TestFeaturesGetScopesToSavedDefaultApp(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	var mutations int
	server := httptest.NewServer(requestsHandler(t, &mutations, nil))
	t.Cleanup(server.Close)

	out, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "features", "get", "fr_a_1")
	if err != nil {
		t.Fatalf("features get: %v", err)
	}
	if !strings.Contains(out, "App A request") {
		t.Errorf("output missing request title:\n%s", out)
	}

	// A request from app B is invisible under the saved default app.
	_, err = runRootWithSeededConfig(t, server.URL, defaultAppConfig, "features", "get", "fr_b_1")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("features get fr_b_1 error = %v, want not-found under app_a", err)
	}
}

// TestFeaturesLookupSendsSavedDefaultAppID asserts the wire contract behind
// the scoping: the listing request carries appId=app_a (the saved default)
// without an explicit --app flag.
func TestFeaturesLookupSendsSavedDefaultAppID(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	var gotAppID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAppID = r.URL.Query().Get("appId")
		_, _ = w.Write(filteredFixture(t, gotAppID))
	}))
	t.Cleanup(server.Close)

	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "features", "get", "fr_a_1"); err != nil {
		t.Fatalf("features get: %v", err)
	}
	if gotAppID != "app_a" {
		t.Errorf("appId filter = %q, want app_a (the saved default)", gotAppID)
	}
}

// TestFeaturesMutationsScopedToSavedDefaultApp pins the cross-app mutation
// footgun from issue #86: update/approve/delete resolve the ID under the
// saved default app, so a request belonging to another app fails before any
// mutation request is issued.
func TestFeaturesMutationsScopedToSavedDefaultApp(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	cases := []struct {
		name string
		args []string
	}{
		{"update", []string{"features", "update", "fr_b_1", "--title", "hijack"}},
		{"approve", []string{"features", "approve", "fr_b_1"}},
		// --yes skips the destructive-command confirm so the test reaches the
		// resolution whose app scoping it actually pins (issue #80's guard).
		{"delete", []string{"features", "delete", "fr_b_1", "--yes"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mutations int
			server := httptest.NewServer(requestsHandler(t, &mutations, nil))
			t.Cleanup(server.Close)

			_, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, tc.args...)
			if err == nil || !strings.Contains(err.Error(), "not found") {
				t.Fatalf("%s fr_b_1 error = %v, want not-found under app_a", tc.name, err)
			}
			if mutations != 0 {
				t.Errorf("%s issued %d mutation requests, want 0", tc.name, mutations)
			}
		})
	}
}

// TestFeaturesAppFlagWinsOverSavedDefault keeps --app authoritative: with a
// saved default app_a, passing -a app_b must scope the lookup to app_b and
// resolve app B's request.
func TestFeaturesAppFlagWinsOverSavedDefault(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	var gotAppID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAppID = r.URL.Query().Get("appId")
		_, _ = w.Write(filteredFixture(t, gotAppID))
	}))
	t.Cleanup(server.Close)

	out, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig,
		"features", "get", "fr_b_1", "--app", "app_b")
	if err != nil {
		t.Fatalf("features get -a app_b: %v", err)
	}
	if gotAppID != "app_b" {
		t.Errorf("appId filter = %q, want app_b (the --app flag)", gotAppID)
	}
	if !strings.Contains(out, "App B request") {
		t.Errorf("output missing request title:\n%s", out)
	}
}

// TestFeaturesNoAppKeepsWorkspaceWideResolution preserves the fallback: with
// no --app flag and no saved default app, the lookup sends no appId filter at
// all and a request from any app still resolves.
func TestFeaturesNoAppKeepsWorkspaceWideResolution(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	var gotAppID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAppID = r.URL.Query().Get("appId")
		_, _ = w.Write(filteredFixture(t, gotAppID))
	}))
	t.Cleanup(server.Close)

	out, err := runRootWithSeededConfig(t, server.URL, `{"defaultWorkspace":"ws_1"}`,
		"features", "get", "fr_b_1")
	if err != nil {
		t.Fatalf("features get without default app: %v", err)
	}
	if gotAppID != "" {
		t.Errorf("appId filter = %q, want no filter (workspace-wide)", gotAppID)
	}
	if !strings.Contains(out, "App B request") {
		t.Errorf("output missing request title:\n%s", out)
	}
}

// TestFeaturesForwardPathUsesResolvedApp covers the forward inconsistency:
// with a saved default app, the lookup is scoped to app_a and the POST path
// must name the same app; a request that is not under the resolved app fails
// before the forward endpoint is contacted.
func TestFeaturesForwardPathUsesResolvedApp(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	var forwardedTo string
	server := httptest.NewServer(requestsHandler(t, nil, &forwardedTo))
	t.Cleanup(server.Close)

	// Consistent resolution: fr_a_1 lives under app_a (the saved default), so
	// the forward path must use app_a — the same app the lookup was scoped to.
	out, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig,
		"features", "forward", "fr_a_1", "--target", "discussion")
	if err != nil {
		t.Fatalf("features forward fr_a_1: %v", err)
	}
	if forwardedTo != "/api/v1/console/workspaces/ws_1/apps/app_a/features/fr_a_1/github/forward" {
		t.Errorf("forward path = %s, want the app_a-scoped path", forwardedTo)
	}
	if !strings.Contains(out, "https://github.com/acme/ios/discussions/9") {
		t.Errorf("output missing forwarded URL:\n%s", out)
	}

	// fr_b_1 is not under the resolved app: the lookup misses and forward
	// fails before the forward endpoint is hit.
	forwardedTo = ""
	_, err = runRootWithSeededConfig(t, server.URL, defaultAppConfig,
		"features", "forward", "fr_b_1", "--target", "discussion")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("features forward fr_b_1 error = %v, want not-found under app_a", err)
	}
	if forwardedTo != "" {
		t.Errorf("forward endpoint hit at %s, want no request after the scoped lookup misses", forwardedTo)
	}
}
