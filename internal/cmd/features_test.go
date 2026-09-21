package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
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

// pagedRequests builds a synthetic workspace listing of n requests with IDs
// req_0000…, all under app_a so the saved-default-app scoping matches.
func pagedRequests(n int) []api.AdminFeatureRequest {
	reqs := make([]api.AdminFeatureRequest, n)
	for i := range reqs {
		reqs[i] = api.AdminFeatureRequest{
			ID:        fmt.Sprintf("req_%04d", i),
			AppID:     "app_a",
			Title:     fmt.Sprintf("Request %d", i),
			Status:    "open",
			CreatedAt: "2026-09-01T12:00:00.000Z",
		}
	}
	return reqs
}

// pagedHandler answers the console feature-requests listing with the
// server's paging contract: limit clamped to 200, limit/offset query
// slicing, appId filtering, and an authoritative total. Listing offsets are
// appended to *offsets in request order; non-GET requests are recorded in
// *mutations as "METHOD path" and answered with an empty JSON object, which
// update/approve/delete do not decode.
func pagedHandler(reqs []api.AdminFeatureRequest, offsets *[]int, mutations *[]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			if mutations != nil {
				*mutations = append(*mutations, r.Method+" "+r.URL.Path)
			}
			_, _ = w.Write([]byte(`{}`))
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		if offsets != nil {
			*offsets = append(*offsets, offset)
		}
		if limit > featureRequestPageSize {
			limit = featureRequestPageSize
		}
		start, end := offset, offset+limit
		if start > len(reqs) {
			start = len(reqs)
		}
		if end > len(reqs) {
			end = len(reqs)
		}
		page := reqs[start:end]
		if appID := r.URL.Query().Get("appId"); appID != "" {
			filtered := make([]api.AdminFeatureRequest, 0, len(page))
			for _, req := range page {
				if req.AppID == appID {
					filtered = append(filtered, req)
				}
			}
			page = filtered
		}
		body, err := json.Marshal(api.AdminListFeatureRequestsResponse{Requests: page, Total: len(reqs)})
		if err != nil {
			panic(err)
		}
		_, _ = w.Write(body)
	}
}

// TestFeaturesGetResolvesBeyondFirstPage covers the issue #59 core case: a
// request living past the newest 200 resolves, and resolution walks the
// listing pages in order (offset 0, 200, 400 for a 450-request workspace).
func TestFeaturesGetResolvesBeyondFirstPage(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	reqs := pagedRequests(450)
	var offsets []int
	server := httptest.NewServer(pagedHandler(reqs, &offsets, nil))
	t.Cleanup(server.Close)

	out, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "features", "get", "req_0400")
	if err != nil {
		t.Fatalf("features get req_0400: %v", err)
	}
	if !strings.Contains(out, "Request 400") {
		t.Errorf("output missing deep request title:\n%s", out)
	}
	if want := []int{0, 200, 400}; !reflect.DeepEqual(offsets, want) {
		t.Errorf("listing offsets = %v, want %v", offsets, want)
	}
}

// TestFeaturesApproveResolvesDeepRequest pins that a mutating command
// operates on the resolved full ID of a request past the first page.
func TestFeaturesApproveResolvesDeepRequest(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	reqs := pagedRequests(450)
	var offsets []int
	var mutations []string
	server := httptest.NewServer(pagedHandler(reqs, &offsets, &mutations))
	t.Cleanup(server.Close)

	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "features", "approve", "req_0209"); err != nil {
		t.Fatalf("features approve req_0209: %v", err)
	}
	want := []string{"POST /api/v1/console/workspaces/ws_1/feature-requests/req_0209/approve"}
	if !reflect.DeepEqual(mutations, want) {
		t.Errorf("mutations = %v, want %v", mutations, want)
	}
	if want := []int{0, 200}; !reflect.DeepEqual(offsets, want) {
		t.Errorf("listing offsets = %v, want %v", offsets, want)
	}
}

// TestFeaturesAmbiguousPrefixAcrossPages pins the ambiguity decision over
// the whole listing: a prefix that matches exactly one request on page 1
// must still error once page 2 adds a second match (and vice versa: a
// prefix matching only a page-2 request resolves).
func TestFeaturesAmbiguousPrefixAcrossPages(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	reqs := pagedRequests(250)
	reqs[0].ID = "dup_first"
	reqs[220].ID = "dup_second"
	server := httptest.NewServer(pagedHandler(reqs, nil, nil))
	t.Cleanup(server.Close)

	_, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "features", "get", "dup_")
	if err == nil || !strings.Contains(err.Error(), "ambiguous request prefix") {
		t.Fatalf("features get dup_ error = %v, want ambiguous-prefix error spanning pages", err)
	}

	out, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "features", "get", "dup_seco")
	if err != nil {
		t.Fatalf("features get dup_seco: %v", err)
	}
	if !strings.Contains(out, "Request 220") {
		t.Errorf("output missing page-2 request title:\n%s", out)
	}
}

// TestFeaturesNotFoundReportsScannedScope pins the truthful not-found
// error: it names the scanned request count and workspace instead of
// blaming --app.
func TestFeaturesNotFoundReportsScannedScope(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	server := httptest.NewServer(pagedHandler(pagedRequests(450), nil, nil))
	t.Cleanup(server.Close)

	_, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "features", "get", "req_9999")
	if err == nil || !strings.Contains(err.Error(), "scanned 450 requests in workspace ws_1") {
		t.Fatalf("features get req_9999 error = %v, want scanned-scope message", err)
	}
}

// TestFeaturesSinglePageResolvesInOneRequest keeps small workspaces on the
// fast path: resolution makes exactly one listing call and no extra
// round-trips.
func TestFeaturesSinglePageResolvesInOneRequest(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	var offsets []int
	server := httptest.NewServer(pagedHandler(pagedRequests(10), &offsets, nil))
	t.Cleanup(server.Close)

	out, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "features", "get", "req_0003")
	if err != nil {
		t.Fatalf("features get req_0003: %v", err)
	}
	if !strings.Contains(out, "Request 3") {
		t.Errorf("output missing request title:\n%s", out)
	}
	if want := []int{0}; !reflect.DeepEqual(offsets, want) {
		t.Errorf("listing offsets = %v, want %v (single listing call)", offsets, want)
	}
}

// TestFeaturesResolutionPageCap pins the pathological-loop guard: when the
// server keeps reporting more pages, resolution stops after the page cap
// with a clear error instead of looping forever.
func TestFeaturesResolutionPageCap(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	body, err := json.Marshal(api.AdminListFeatureRequestsResponse{Requests: pagedRequests(featureRequestPageSize), Total: 1_000_000})
	if err != nil {
		t.Fatalf("encode page: %v", err)
	}
	var served int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)

	_, err = runRootWithSeededConfig(t, server.URL, defaultAppConfig, "features", "get", "zzz_unknown")
	if err == nil || !strings.Contains(err.Error(), "scans at most 50 pages") || !strings.Contains(err.Error(), "first 10000 requests") {
		t.Fatalf("features get error = %v, want page-cap message", err)
	}
	if served != maxFeatureRequestPages {
		t.Errorf("served %d listing pages, want %d (the cap)", served, maxFeatureRequestPages)
	}
}

// TestFeaturesListClampsLimitToServerPageCap pins the client-side clamp of
// 'features list --limit': values beyond 200 go on the wire as 200 and
// values below 1 as 1, matching the server's silent page-size clamp.
func TestFeaturesListClampsLimitToServerPageCap(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query().Get("limit")
		_, _ = w.Write(filteredFixture(t, ""))
	}))
	t.Cleanup(server.Close)

	for _, tc := range []struct{ flag, want string }{{"500", "200"}, {"0", "1"}, {"-5", "1"}} {
		if _, err := runRootWithSeededConfig(t, server.URL, `{"defaultWorkspace":"ws_1"}`, "features", "list", "--limit", tc.flag); err != nil {
			t.Fatalf("features list --limit %s: %v", tc.flag, err)
		}
		if got != tc.want {
			t.Errorf("--limit %s sent limit=%q, want %q", tc.flag, got, tc.want)
		}
	}
}
