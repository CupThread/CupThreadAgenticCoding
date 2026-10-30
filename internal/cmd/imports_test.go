package cmd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// importJobsArray renders n minimal import jobs as a JSON array body
// (without brackets), every third one failed so the truncation advisories'
// "including failed ones worth re-running" stakes are visible in the rows
// under test.
func importJobsArray(n int) string {
	jobs := make([]string, 0, n)
	for i := 0; i < n; i++ {
		status := "completed"
		lastError := "null"
		if i%3 == 1 {
			status = "failed"
			lastError = `"commit rejected: duplicate slug"`
		}
		jobs = append(jobs, fmt.Sprintf(
			`{"id":"imp_%03d","workspaceId":"ws_1","appId":"app_a","source":"github_issues","mode":"commit","status":%q,"attempts":1,"lastError":%s,"createdAt":"2026-10-%02dT00:00:00.000Z","updatedAt":"2026-10-%02dT00:00:00.000Z"}`,
			i, status, lastError, (i%28)+1, (i%28)+1))
	}
	return strings.Join(jobs, ",")
}

// importJobsFixture wraps importJobsArray as today's routes answer: a
// {"jobs": [...]} document with no total (issue #346).
func importJobsFixture(n int) string {
	return `{"jobs":[` + importJobsArray(n) + `]}`
}

// seededConfigPath writes defaultAppConfig to a throwaway config file for
// runRootCapture, which needs an explicit config path.
func seededConfigPath(t *testing.T) string {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(defaultAppConfig), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return cfgPath
}

// TestImportsListDisclosesServerCap pins the issue #346 client-side half for
// the app-scoped listing: the route serves only the newest 50 jobs with no
// total and no paging, so a cap-sized answer must say so instead of reading
// as the whole set. A 50-job response renders the advisory after the table; a
// 49-job response stays advisory-free (stable output shape).
func TestImportsListDisclosesServerCap(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var captured capturedRequest
	server := inboxServer(t, http.StatusOK, importJobsFixture(importsAppPageSize), &captured)
	defer server.Close()

	cfgPath := seededConfigPath(t)
	out, _, err := runRootCapture(t, cfgPath, server.URL, "imports", "list")
	if err != nil {
		t.Fatalf("imports list: %v", err)
	}
	if got := captured.Path; got != "/api/v1/console/workspaces/ws_1/apps/app_a/imports" {
		t.Errorf("imports list hit %q, want the app-scoped imports route", got)
	}
	if !strings.Contains(out, "imp_000") || !strings.Contains(out, "failed") {
		t.Errorf("table output missing job rows:\n%s", out)
	}
	if !strings.Contains(out, "⚠ Import job list may be truncated: this listing serves only the newest 50 jobs (server cap, no paging or total); use 'cupthread imports history' for the workspace-wide 100-job view") {
		t.Errorf("cap-sized output missing truncation advisory:\n%s", out)
	}
	if strings.Contains(out, "shown,") {
		t.Errorf("no-total response must not print the shown/total footer:\n%s", out)
	}

	server2 := inboxServer(t, http.StatusOK, importJobsFixture(importsAppPageSize-1), &captured)
	defer server2.Close()
	out, _, err = runRootCapture(t, cfgPath, server2.URL, "imports", "list")
	if err != nil {
		t.Fatalf("imports list (49 jobs): %v", err)
	}
	if strings.Contains(out, "⚠") {
		t.Errorf("sub-cap output produced an advisory:\n%s", out)
	}
}

// TestImportsHistoryDisclosesServerCap pins the issue #346 client-side half
// for the workspace-wide history: the route serves only the newest 100 jobs
// with no total and no paging. A 100-job response renders the advisory; a
// 99-job response stays advisory-free. A 50-job answer — cap-sized for
// 'imports list' but sub-cap here — must stay silent, proving the two
// listings key off their own caps.
func TestImportsHistoryDisclosesServerCap(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var captured capturedRequest
	server := inboxServer(t, http.StatusOK, importJobsFixture(importsWorkspacePageSize), &captured)
	defer server.Close()

	// runRootCapture drains stdout while the command runs; the 100-row table
	// can exceed a Windows pipe buffer, so the read-after-exit runRoot would
	// risk blocking the test there (CodeRabbit review of #356).
	cfgPath := seededConfigPath(t)
	out, _, err := runRootCapture(t, cfgPath, server.URL, "imports", "history", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("imports history: %v", err)
	}
	if got := captured.Path; got != "/api/v1/console/workspaces/ws_1/imports" {
		t.Errorf("imports history hit %q, want the workspace-scoped imports route", got)
	}
	if !strings.Contains(out, "⚠ Import job history may be truncated: this listing serves only the newest 100 jobs (server cap, no paging or total); older jobs — including failed ones worth re-running — are not shown") {
		t.Errorf("cap-sized output missing truncation advisory:\n%s", out)
	}
	if strings.Contains(out, "shown,") {
		t.Errorf("no-total response must not print the shown/total footer:\n%s", out)
	}

	server2 := inboxServer(t, http.StatusOK, importJobsFixture(importsWorkspacePageSize-1), &captured)
	defer server2.Close()
	out, _, err = runRootCapture(t, cfgPath, server2.URL, "imports", "history", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("imports history (99 jobs): %v", err)
	}
	if strings.Contains(out, "⚠") {
		t.Errorf("sub-cap output produced an advisory:\n%s", out)
	}

	server3 := inboxServer(t, http.StatusOK, importJobsFixture(importsAppPageSize), &captured)
	defer server3.Close()
	out, _, err = runRootCapture(t, cfgPath, server3.URL, "imports", "history", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("imports history (50 jobs): %v", err)
	}
	if strings.Contains(out, "⚠") {
		t.Errorf("history below its own cap produced an advisory:\n%s", out)
	}
}

// TestImportsListingsJSONNoticeOnStderr pins the structured half of #346:
// stdout stays a single byte-faithful jobs document (no total key, no human
// markers) and the advisory moves to stderr — and the 50-job answer that
// trips 'imports list' stays silent under 'imports history', whose cap is
// 100.
func TestImportsListingsJSONNoticeOnStderr(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	server := inboxServer(t, http.StatusOK, importJobsFixture(importsAppPageSize), &capturedRequest{})
	defer server.Close()

	cfgPath := seededConfigPath(t)

	stdout, stderr, err := runRootCapture(t, cfgPath, server.URL, "imports", "list", "--json")
	if err != nil {
		t.Fatalf("imports list --json: %v", err)
	}
	assertNoTableOutput(t, stdout)
	doc := unmarshalOneJSON(t, stdout)
	jobs, ok := doc["jobs"].([]any)
	if !ok || len(jobs) != importsAppPageSize {
		t.Fatalf("stdout jobs = %T len %v, want %d entries", doc["jobs"], len(jobs), importsAppPageSize)
	}
	if _, present := doc["total"]; present {
		t.Errorf("structured stdout grew a total key the server never sent:\n%s", stdout)
	}
	if !strings.Contains(stderr, "⚠ Import job list may be truncated: this listing serves only the newest 50 jobs") {
		t.Errorf("json stderr missing truncation advisory:\n%s", stderr)
	}

	stdout, stderr, err = runRootCapture(t, cfgPath, server.URL, "imports", "history", "--json", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("imports history --json: %v", err)
	}
	assertNoTableOutput(t, stdout)
	unmarshalOneJSON(t, stdout)
	if strings.Contains(stderr, "⚠") {
		t.Errorf("history below its own cap warned in json mode:\n%s", stderr)
	}

	server2 := inboxServer(t, http.StatusOK, importJobsFixture(importsWorkspacePageSize), &capturedRequest{})
	defer server2.Close()
	stdout, stderr, err = runRootCapture(t, cfgPath, server2.URL, "imports", "history", "--json", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("imports history --json (100 jobs): %v", err)
	}
	assertNoTableOutput(t, stdout)
	unmarshalOneJSON(t, stdout)
	if !strings.Contains(stderr, "⚠ Import job history may be truncated: this listing serves only the newest 100 jobs") {
		t.Errorf("json stderr missing truncation advisory:\n%s", stderr)
	}
}

// TestImportsListingsTotalFooterAndNotice pins the forward-compatible half of
// #346: once the SaaS routes answer total, the CLI prints the sibling
// (N shown, M total) footer and keys the notice off total > shown — present
// only when rows were actually cut.
func TestImportsListingsTotalFooterAndNotice(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var captured capturedRequest
	server := inboxServer(t, http.StatusOK, `{"jobs":[`+importJobsArray(2)+`], "total": 57}`, &captured)
	defer server.Close()

	cfgPath := seededConfigPath(t)
	out, _, err := runRootCapture(t, cfgPath, server.URL, "imports", "list")
	if err != nil {
		t.Fatalf("imports list (total 57): %v", err)
	}
	if !strings.Contains(out, "(2 shown, 57 total)") {
		t.Errorf("output missing sibling footer:\n%s", out)
	}
	if !strings.Contains(out, "⚠ Import jobs are truncated: showing the 2 newest of 57 jobs; older jobs (including failed ones) are not retrievable through this listing") {
		t.Errorf("output missing truncation notice:\n%s", out)
	}

	server2 := inboxServer(t, http.StatusOK, `{"jobs":[`+importJobsArray(2)+`], "total": 2}`, &captured)
	defer server2.Close()
	out, _, err = runRootCapture(t, cfgPath, server2.URL, "imports", "list")
	if err != nil {
		t.Fatalf("imports list (total 2): %v", err)
	}
	if !strings.Contains(out, "(2 shown, 2 total)") {
		t.Errorf("complete listing missing sibling footer:\n%s", out)
	}
	if strings.Contains(out, "⚠") {
		t.Errorf("complete listing produced a truncation notice:\n%s", out)
	}
}

// truncatedJobFullID is a UUID-shaped job id whose first 12 characters are
// exactly what the imports tables print (issue #413): the truncated form is
// what an agent copies, and the job-taking commands must resolve it.
const (
	truncatedJobFullID  = "imp_abc1234567deadbeef000000000000"
	truncatedJobPrefix  = "imp_abc1234567"
	truncatedJobJSONJob = `{"id":"` + truncatedJobFullID + `","workspaceId":"ws_1","appId":"app_a","source":"github_issues","mode":"commit","status":"failed","attempts":1,"createdAt":"2026-10-01T00:00:00.000Z","updatedAt":"2026-10-01T00:00:00.000Z"}`
)

// importsResolutionServer plays the two listing shapes plus the mutation the
// resolution flow touches (issue #413): the exact-job GET 404s for anything
// but the full id, the history window serves jobsJSON, and every mutation is
// recorded in *mutations as "METHOD path".
func importsResolutionServer(t *testing.T, jobsJSON string, mutations *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method != http.MethodGet:
			*mutations = append(*mutations, r.Method+" "+r.URL.Path)
			_, _ = w.Write([]byte(`{"job":{"id":"` + truncatedJobFullID + `","source":"github_issues","mode":"commit","status":"queued","attempts":0,"createdAt":"2026-10-01T00:00:00Z","updatedAt":"2026-10-01T00:00:00Z"}}`))
		case strings.HasSuffix(r.URL.Path, "/imports/"+truncatedJobFullID):
			_, _ = w.Write([]byte(`{"job":` + truncatedJobJSONJob + `}`))
		case strings.HasSuffix(r.URL.Path, "/imports"):
			_, _ = w.Write([]byte(`{"jobs":[` + jobsJSON + `]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Import job not found"}`))
		}
	}))
}

// TestImportsGetResolvesTruncatedJobID covers the core #413 case for get:
// the exact GET answers 404 for the 12-character table id, the history
// window resolves it to the full id, and the rendered row carries the full
// id — one history round trip, no invented paging.
func TestImportsGetResolvesTruncatedJobID(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var mutations []string
	server := importsResolutionServer(t, truncatedJobJSONJob, &mutations)
	defer server.Close()

	cfgPath := seededConfigPath(t)
	out, _, err := runRootCapture(t, cfgPath, server.URL, "imports", "get", truncatedJobPrefix, "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("imports get %s: %v", truncatedJobPrefix, err)
	}
	if !strings.Contains(out, truncatedJobFullID) {
		t.Errorf("output must carry the resolved full id:\n%s", out)
	}
	if len(mutations) != 0 {
		t.Errorf("get mutated: %v", mutations)
	}
}

// TestImportsCancelAndRerunResolveTruncatedJobID pins the mutating half:
// cancel and rerun resolve the truncated table id through the history
// window and mutate the FULL resolved id — the exact disclosure the issue
// demands (the success lines name the full job).
func TestImportsCancelAndRerunResolveTruncatedJobID(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	for _, tc := range []struct {
		name     string
		args     []string
		wantVerb string
	}{
		{"cancel", []string{"imports", "cancel", truncatedJobPrefix, "--yes"}, "POST /api/v1/console/workspaces/ws_1/imports/" + truncatedJobFullID + "/cancel"},
		{"rerun", []string{"imports", "rerun", truncatedJobPrefix}, "POST /api/v1/console/workspaces/ws_1/imports/" + truncatedJobFullID + "/rerun"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mutations []string
			server := importsResolutionServer(t, truncatedJobJSONJob, &mutations)
			defer server.Close()

			cfgPath := seededConfigPath(t)
			out, _, err := runRootCapture(t, cfgPath, server.URL, append(tc.args, "--workspace", "ws_1")...)
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if len(mutations) != 1 || mutations[0] != tc.wantVerb {
				t.Errorf("mutations = %v, want exactly [%s]", mutations, tc.wantVerb)
			}
			if !strings.Contains(out, truncatedJobFullID) {
				t.Errorf("output must carry the resolved full id:\n%s", out)
			}
		})
	}
}

// TestImportsExactJobIDKeepsSingleRoundTrip pins the fast path: a full job
// id resolves through exactly one exact GET — the history window is never
// consulted — and the mutation follows on the same id.
func TestImportsExactJobIDKeepsSingleRoundTrip(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var mutations []string
	var historyHits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/imports") {
			historyHits++
			_, _ = w.Write([]byte(`{"jobs":[]}`))
			return
		}
		if r.Method != http.MethodGet {
			mutations = append(mutations, r.Method+" "+r.URL.Path)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_, _ = w.Write([]byte(`{"job":` + truncatedJobJSONJob + `}`))
	}))
	defer server.Close()

	cfgPath := seededConfigPath(t)
	if _, _, err := runRootCapture(t, cfgPath, server.URL, "imports", "cancel", truncatedJobFullID, "--yes", "--workspace", "ws_1"); err != nil {
		t.Fatalf("imports cancel <full id>: %v", err)
	}
	if historyHits != 0 {
		t.Errorf("history window hit %d time(s) for an exact id, want 0", historyHits)
	}
	if len(mutations) != 1 || mutations[0] != "POST /api/v1/console/workspaces/ws_1/imports/"+truncatedJobFullID+"/cancel" {
		t.Errorf("mutations = %v, want one cancel on the full id", mutations)
	}
}

// TestImportsAmbiguousJobPrefixFailsWithoutMutation pins the ambiguity
// decision over the history window: a prefix matching two jobs errors
// locally and no mutation is ever sent.
func TestImportsAmbiguousJobPrefixFailsWithoutMutation(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	twin := `{"id":"imp_abc1234567twin_a000000000000","workspaceId":"ws_1","appId":"app_a","source":"linear","mode":"preview","status":"queued","attempts":0,"createdAt":"2026-10-01T00:00:00Z","updatedAt":"2026-10-01T00:00:00Z"},` +
		`{"id":"imp_abc1234567twin_b000000000000","workspaceId":"ws_1","appId":"app_a","source":"linear","mode":"preview","status":"queued","attempts":0,"createdAt":"2026-10-02T00:00:00Z","updatedAt":"2026-10-02T00:00:00Z"}`
	var mutations []string
	server := importsResolutionServer(t, twin, &mutations)
	defer server.Close()

	cfgPath := seededConfigPath(t)
	_, _, err := runRootCapture(t, cfgPath, server.URL, "imports", "rerun", truncatedJobPrefix, "--workspace", "ws_1")
	if err == nil || !strings.Contains(err.Error(), `ambiguous import job prefix "`+truncatedJobPrefix+`" matches 2 jobs; use a longer prefix`) {
		t.Fatalf("error = %v, want the ambiguity error", err)
	}
	if len(mutations) != 0 {
		t.Errorf("mutations = %v, want none on an ambiguous prefix", mutations)
	}
}

// TestImportsAbsentPrefixReportsTheWindow pins the honest not-found shape:
// a prefix no job in the history window matches errors naming the scanned
// window and its server cap, instead of a bare 404 that implies the job
// cannot exist.
func TestImportsAbsentPrefixReportsTheWindow(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var mutations []string
	server := importsResolutionServer(t, truncatedJobJSONJob, &mutations)
	defer server.Close()

	cfgPath := seededConfigPath(t)
	_, _, err := runRootCapture(t, cfgPath, server.URL, "imports", "get", "imp_nothing1234", "--workspace", "ws_1")
	if err == nil || !strings.Contains(err.Error(), `import job "imp_nothing1234" not found`) || !strings.Contains(err.Error(), "newest 100") {
		t.Fatalf("error = %v, want the window-naming not-found error", err)
	}
}
