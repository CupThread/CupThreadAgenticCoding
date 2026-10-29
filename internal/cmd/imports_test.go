package cmd

import (
	"fmt"
	"net/http"
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
