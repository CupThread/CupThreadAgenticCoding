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

	// A request from app B is invisible under the saved default app, and the
	// not-found error names the app the scan actually covered plus the --app
	// escape hatch (issue #187's error contract) instead of mislabeling the
	// scan workspace-wide.
	_, err = runRootWithSeededConfig(t, server.URL, defaultAppConfig, "features", "get", "fr_b_1")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("features get fr_b_1 error = %v, want not-found under app_a", err)
	}
	for _, want := range []string{"app app_a", "workspace ws_1", "--app"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("not-found error missing %q:\n%v", want, err)
		}
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

// TestFeaturesForwardBodyContract pins the forward request-body contract
// (issue #197): the server schema declares labels: z.array(...).default([]),
// and a zod default only substitutes for an absent key — an explicit null is
// rejected with 400 "Invalid forward input". So an unset --labels must omit
// the key entirely (like the neighboring owner/repo/categoryId), and a set
// --labels must marshal as a real string array.
func TestFeaturesForwardBodyContract(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	var forwardBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/github/forward") {
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read forward body: %v", err)
			}
			if err := json.Unmarshal(raw, &forwardBody); err != nil {
				t.Errorf("decode forward body %q: %v", raw, err)
			}
			_, _ = w.Write([]byte(`{"success":true,"targetType":"discussion","url":"https://github.com/acme/ios/discussions/9"}`))
			return
		}
		_, _ = w.Write(filteredFixture(t, r.URL.Query().Get("appId")))
	}))
	t.Cleanup(server.Close)

	cases := []struct {
		name    string
		args    []string
		want    map[string]any
		wantKey []string
	}{
		{
			name:    "default invocation omits labels",
			args:    []string{"features", "forward", "fr_a_1"},
			want:    map[string]any{"targetType": "discussion"},
			wantKey: []string{"labels", "owner", "repo", "categoryId"},
		},
		{
			name:    "labels marshal as an array",
			args:    []string{"features", "forward", "fr_a_1", "--labels", "a,b"},
			want:    map[string]any{"targetType": "discussion", "labels": []any{"a", "b"}},
			wantKey: []string{"owner", "repo", "categoryId"},
		},
		{
			name:    "target and repository hints pass through",
			args:    []string{"features", "forward", "fr_a_1", "--target", "issue", "--owner", "acme", "--repo", "ios"},
			want:    map[string]any{"targetType": "issue", "owner": "acme", "repo": "ios"},
			wantKey: []string{"labels", "categoryId"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			forwardBody = nil
			if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, tc.args...); err != nil {
				t.Fatalf("%v: %v", tc.args, err)
			}
			for _, k := range tc.wantKey {
				if got, ok := forwardBody[k]; ok {
					t.Errorf("body[%q] = %#v, want the key absent", k, got)
				}
			}
			for k, want := range tc.want {
				if got := forwardBody[k]; !reflect.DeepEqual(got, want) {
					t.Errorf("body[%q] = %#v, want %#v", k, got, want)
				}
			}
		})
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
	if err == nil || !strings.Contains(err.Error(), "ambiguous feature request prefix") {
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

// TestFeaturesNotFoundReportsScannedScope pins the truthful not-found error
// (issue #187's error contract): under a resolved app it names the app the
// scan actually covered and the --app escape hatch instead of mislabeling the
// scan workspace-wide; with no app resolved it keeps the workspace wording.
func TestFeaturesNotFoundReportsScannedScope(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	server := httptest.NewServer(pagedHandler(pagedRequests(450), nil, nil))
	t.Cleanup(server.Close)

	_, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "features", "get", "req_9999")
	if err == nil {
		t.Fatal("features get req_9999 under app_a, want not-found error")
	}
	for _, want := range []string{
		"not found among the 450 requests of app app_a in workspace ws_1",
		"--app",
		"apps use",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("app-scoped not-found error missing %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "scanned 450 requests in workspace") {
		t.Errorf("app-scoped error still mislabels the scan workspace-wide:\n%v", err)
	}

	// No app resolved: the scan really was workspace-wide and the wording
	// says so.
	_, err = runRootWithSeededConfig(t, server.URL, `{"defaultWorkspace":"ws_1"}`, "features", "get", "req_9999")
	if err == nil || !strings.Contains(err.Error(), "not found (scanned 450 requests in workspace ws_1)") {
		t.Fatalf("workspace-wide features get req_9999 error = %v, want scanned-workspace message", err)
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
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("scans at most %d pages", maxFeatureRequestPages)) || !strings.Contains(err.Error(), fmt.Sprintf("first %d requests", maxFeatureRequestPages*featureRequestPageSize)) {
		t.Fatalf("features get error = %v, want the page-cap message naming %d pages / %d requests", err, maxFeatureRequestPages, maxFeatureRequestPages*featureRequestPageSize)
	}
	if served != maxFeatureRequestPages {
		t.Errorf("served %d listing pages, want %d (the cap)", served, maxFeatureRequestPages)
	}
}

// TestFeatureRequestResolvePagesCoverTheOffsetCap pins the arithmetic of the
// changelog twin (maxChangelogResolvePages, fixed in the PR #414 review): the
// resolution walk must include the offset-10,000 clamp page, because the
// server clamps offset at the shared 10,000-entry cap and still answers 200
// with that final page — consoleListMaxOffset/featureRequestPageSize alone
// stops one page short and re-opens the wrong-row mutation the +1 closes
// (issue #419).
func TestFeatureRequestResolvePagesCoverTheOffsetCap(t *testing.T) {
	if maxFeatureRequestPages != consoleListMaxOffset/featureRequestPageSize+1 {
		t.Errorf("maxFeatureRequestPages = %d, want consoleListMaxOffset/featureRequestPageSize + 1 = %d — the offset-10,000 clamp page must stay scanned (issue #419)", maxFeatureRequestPages, consoleListMaxOffset/featureRequestPageSize+1)
	}
}

// TestFeatureRequestPrefixScanCoversTheOffsetCapPage ports the changelog
// boundary test (TestChangelogPrefixScanCoversTheOffsetCapPage): a request
// living only on the offset-10,000 clamp page resolves, with the walk's last
// observed listing offset equal to the cap — instead of a false not-found
// that hides the row behind "the first 10000 requests".
func TestFeatureRequestPrefixScanCoversTheOffsetCapPage(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	// 10,000 requests fill pages 0..49 (offsets 0..9,800) and the target
	// sits alone on the offset-10,000 clamp page, so the authoritative total
	// is 10,001: only the clamp page exhausts the listing.
	const totalRequests = featureRequestPageSize*(maxFeatureRequestPages-1) + 1
	fillers := pagedRequests(featureRequestPageSize)
	target := pagedRequests(1)[0]
	target.ID = "fr_target000123ffffffffffff"
	target.Title = "Cap page request"

	pages := make([][]api.AdminFeatureRequest, maxFeatureRequestPages)
	for p := range pages {
		pages[p] = fillers
	}
	pages[maxFeatureRequestPages-1] = []api.AdminFeatureRequest{target}
	capOffset := strconv.Itoa((maxFeatureRequestPages - 1) * featureRequestPageSize)

	var offsets []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		offset := r.URL.Query().Get("offset")
		offsets = append(offsets, offset)
		page, _ := strconv.Atoi(offset)
		body, err := json.Marshal(api.AdminListFeatureRequestsResponse{Requests: pages[page/featureRequestPageSize], Total: totalRequests})
		if err != nil {
			t.Errorf("encode page: %v", err)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)

	out, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "features", "get", "fr_target000")
	if err != nil {
		t.Fatalf("features get on the offset-cap page: %v", err)
	}
	if got := offsets[len(offsets)-1]; got != capOffset {
		t.Errorf("last listing offset = %q, want the offset-cap page %s scanned", got, capOffset)
	}
	if len(offsets) != maxFeatureRequestPages {
		t.Errorf("scanned %d pages, want %d (every page through the offset cap)", len(offsets), maxFeatureRequestPages)
	}
	if !strings.Contains(out, "Cap page request") || !strings.Contains(out, target.ID) {
		t.Errorf("output must carry the resolved cap-page request:\n%s", out)
	}
}

// TestFeaturesPrefixUniqueInWindowButAmbiguousOnCapPage pins the wrong-row
// guard the +1 page buys (issue #419): a prefix matching both a request
// inside the scanned window and a second one on the offset-10,000 clamp page
// must fail locally as ambiguous — without the cap page in the walk, the
// in-window match is the only one seen and acceptPrefixUnderCap resolves the
// delete onto the wrong row.
func TestFeaturesPrefixUniqueInWindowButAmbiguousOnCapPage(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	const totalRequests = featureRequestPageSize*(maxFeatureRequestPages-1) + 1
	fillers := pagedRequests(featureRequestPageSize)
	fillers[3].ID = "fr_dup0000aaaaaaaaaaaaaaaaa"
	fillers[3].Title = "In-window duplicate"
	capPage := pagedRequests(1)
	capPage[0].ID = "fr_dup0000bbbbbbbbbbbbbbbbb"
	capPage[0].Title = "Cap page duplicate"

	pages := make([][]api.AdminFeatureRequest, maxFeatureRequestPages)
	for p := range pages {
		pages[p] = fillers
	}
	pages[maxFeatureRequestPages-1] = capPage

	var mutations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			mutations = append(mutations, r.Method+" "+r.URL.Path)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		body, err := json.Marshal(api.AdminListFeatureRequestsResponse{Requests: pages[page/featureRequestPageSize], Total: totalRequests})
		if err != nil {
			t.Errorf("encode page: %v", err)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)

	_, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "features", "delete", "fr_dup0000", "--yes")
	if err == nil || !strings.Contains(err.Error(), "ambiguous feature request prefix") {
		t.Fatalf("features delete fr_dup0000 error = %v, want ambiguous prefix spanning the cap page", err)
	}
	if len(mutations) != 0 {
		t.Errorf("mutations = %v, want zero — the ambiguity must stop the delete before any request", mutations)
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

// singleRequestFixture is the dangerous shape of issue #289: a workspace
// where exactly one request is in scope. An empty reference prefix-matches
// every id (strings.HasPrefix(id, "") is always true), and with a single
// match the ambiguity guard does not fire — before the guard, the empty ref
// silently resolved to this request.
const singleRequestFixture = `{
	"requests": [{
		"id": "fr_solo",
		"appId": "app_a",
		"title": "The only request",
		"description": "single",
		"status": "open",
		"voteCount": 0,
		"createdAt": "2026-09-01T12:00:00.000Z",
		"updatedAt": "2026-09-01T12:00:00.000Z"
	}],
	"total": 1
}`

// TestFeaturesEmptyReferenceRejectedBeforeAnyRequest pins the issue #289
// guard: every ID-taking feature command with an empty or whitespace-only
// reference fails locally with the self-diagnosing error and never contacts
// the server — no lookup, no mutation, no round trip.
func TestFeaturesEmptyReferenceRejectedBeforeAnyRequest(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(singleRequestFixture))
	}))
	t.Cleanup(server.Close)

	cases := []struct {
		name string
		args []string
	}{
		{"get empty ref", []string{"features", "get", ""}},
		{"get whitespace ref", []string{"features", "get", " "}},
		{"update empty ref", []string{"features", "update", "", "--approved"}},
		{"update whitespace ref", []string{"features", "update", " \t", "--approved"}},
		{"approve empty ref", []string{"features", "approve", ""}},
		{"approve whitespace ref", []string{"features", "approve", "\n"}},
		{"delete empty ref", []string{"features", "delete", "", "--yes"}},
		{"delete whitespace ref", []string{"features", "delete", " ", "--yes"}},
		{"forward empty ref", []string{"features", "forward", ""}},
		{"forward whitespace ref", []string{"features", "forward", " "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hits = 0
			_, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, tc.args...)
			if err == nil || !strings.Contains(err.Error(), "feature request reference is empty") {
				t.Fatalf("%v error = %v, want the local empty-reference error", tc.args, err)
			}
			if hits != 0 {
				t.Errorf("%v issued %d HTTP requests, want 0 (rejected before any round trip)", tc.args, hits)
			}
		})
	}
}

// TestFeaturesEmptyRefGuardKeepsNonEmptyResolution pins that the issue #289
// guard changes nothing for non-empty references: exact ids and unique
// prefixes keep resolving (including in a one-request workspace, where the
// empty ref used to resolve), and an ambiguous prefix still errors.
func TestFeaturesEmptyRefGuardKeepsNonEmptyResolution(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	var mutations int
	server := httptest.NewServer(requestsHandler(t, &mutations, nil))
	t.Cleanup(server.Close)

	// One-request workspace under the saved default app: a non-empty unique
	// prefix must still resolve — prefix matching is untouched.
	out, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "features", "get", "fr_a")
	if err != nil {
		t.Fatalf("features get fr_a: %v", err)
	}
	if !strings.Contains(out, "App A request") {
		t.Errorf("unique prefix output missing request title:\n%s", out)
	}

	// Exact id keeps resolving and the mutation still happens.
	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "features", "approve", "fr_a_1"); err != nil {
		t.Fatalf("features approve fr_a_1: %v", err)
	}
	if mutations != 1 {
		t.Errorf("mutations = %d, want 1 (exact-id approve)", mutations)
	}

	// Ambiguous prefix across the workspace-wide listing (no default app so
	// both apps' requests are in scope) still errors as before.
	_, err = runRootWithSeededConfig(t, server.URL, `{"defaultWorkspace":"ws_1"}`, "features", "get", "fr_")
	if err == nil || !strings.Contains(err.Error(), "ambiguous feature request prefix") {
		t.Fatalf("features get fr_ error = %v, want ambiguous-prefix error", err)
	}
}

// TestFeaturesListClampsOffsetToConsoleCap pins the client-side clamp of
// 'features list --offset' (issue #178): the console listing's server-side
// parseListPagination silently degrades an offset above the shared console
// cap of 10,000 to the cap (still answering 200) and a negative one to 0, so
// a walk past the cap re-serves the same final page with no hint anything
// changed. The CLI clamps first and announces the rewrite, and the 10,000
// boundary passes through verbatim with no warning.
func TestFeaturesListClampsOffsetToConsoleCap(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query().Get("offset")
		_, _ = w.Write(filteredFixture(t, ""))
	}))
	t.Cleanup(server.Close)

	for _, tc := range []struct{ requested, want string }{
		{"15000", "10000"},
		{"10001", "10000"},
		{"-5", "0"},
	} {
		out, err := runRootWithSeededConfig(t, server.URL, `{"defaultWorkspace":"ws_1"}`, "features", "list", "--offset", tc.requested)
		if err != nil {
			t.Fatalf("features list --offset %s: %v", tc.requested, err)
		}
		if got != tc.want {
			t.Errorf("--offset %s sent offset=%q, want %q", tc.requested, got, tc.want)
		}
		if !strings.Contains(out, "⚠ --offset "+tc.requested+" is outside the server's 0-10000 page range") {
			t.Errorf("--offset %s output missing clamp warning:\n%s", tc.requested, out)
		}
	}

	out, err := runRootWithSeededConfig(t, server.URL, `{"defaultWorkspace":"ws_1"}`, "features", "list", "--offset", "10000")
	if err != nil {
		t.Fatalf("features list --offset 10000: %v", err)
	}
	if got != "10000" {
		t.Errorf("boundary --offset 10000 sent offset=%q, want 10000", got)
	}
	if strings.Contains(out, "⚠") {
		t.Errorf("boundary --offset 10000 produced a warning:\n%s", out)
	}
}

// TestFeaturesListClampsLimit pins the local --limit clamp on the feature
// requests listing (issue #337): the server's parseListPagination silently
// clamps a parsed page size to its maxLimit (200) and degrades 0/negative to
// 1, so an offset walk stepped by a larger requested limit would skip rows
// without any hint. 'features list' was the last console listing still doing
// this inline and silently; it now shares A.clampListLimit with the sibling
// listings (issue #201), which announces the rewrite through warnf: table
// mode keeps it on stdout, --json mode moves it to stderr so stdout stays a
// single document. The 200 boundary passes through verbatim with no warning.
func TestFeaturesListClampsLimit(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query().Get("limit")
		_, _ = w.Write(filteredFixture(t, ""))
	}))
	t.Cleanup(server.Close)

	for _, tc := range []struct{ requested, want string }{
		{"500", "200"},
		{"201", "200"},
		{"0", "1"},
		{"-5", "1"},
	} {
		out, err := runRootWithSeededConfig(t, server.URL, `{"defaultWorkspace":"ws_1"}`, "features", "list", "--limit", tc.requested)
		if err != nil {
			t.Fatalf("features list --limit %s: %v", tc.requested, err)
		}
		if got != tc.want {
			t.Errorf("--limit %s sent limit=%q, want %q", tc.requested, got, tc.want)
		}
		if !strings.Contains(out, "⚠ --limit "+tc.requested+" is outside the server's 1-200 page range") {
			t.Errorf("--limit %s output missing clamp warning:\n%s", tc.requested, out)
		}
	}

	out, err := runRootWithSeededConfig(t, server.URL, `{"defaultWorkspace":"ws_1"}`, "features", "list", "--limit", "200")
	if err != nil {
		t.Fatalf("features list --limit 200: %v", err)
	}
	if got != "200" {
		t.Errorf("boundary --limit 200 sent limit=%q, want 200", got)
	}
	if strings.Contains(out, "⚠") {
		t.Errorf("boundary --limit 200 produced a warning:\n%s", out)
	}

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	stdout, stderr, err := runRootCapture(t, cfgPath, server.URL,
		"features", "list", "--workspace", "ws_1", "--limit", "500", "--json")
	if err != nil {
		t.Fatalf("features list --json --limit 500: %v", err)
	}
	unmarshalOneJSON(t, stdout)
	assertNoTableOutput(t, stdout)
	if !strings.Contains(stderr, "⚠ --limit 500 is outside the server's 1-200 page range; requesting 200 instead") {
		t.Errorf("json stderr missing clamp warning:\n%s", stderr)
	}
}
