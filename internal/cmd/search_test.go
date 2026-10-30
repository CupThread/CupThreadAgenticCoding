package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// searchResultsFixture builds a console search response body with n result
// rows and the given total, so footer tests can pin the shown-vs-total
// disclosure contract independent of the server's row caps.
func searchResultsFixture(t *testing.T, n, total int) string {
	t.Helper()
	items := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		items = append(items, map[string]any{
			"id":            fmt.Sprintf("ent_%02d", i),
			"type":          "feature_request",
			"title":         fmt.Sprintf("Result %d", i),
			"snippet":       "search snippet",
			"workspaceId":   "ws_1",
			"workspaceName": "WS One",
		})
	}
	body, err := json.Marshal(map[string]any{"query": "needle", "results": items, "total": total})
	if err != nil {
		t.Fatalf("marshal search fixture: %v", err)
	}
	return string(body)
}

// newSearchServer serves the given body for the console search route.
func newSearchServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/api/v1/console/search") {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

// TestSearchFooterDisclosesTruncation pins the issue #404 contract: when the
// console search serves fewer rows than it reports total (the merged window
// caps at the top-scored 30 hits, no paging), the footer must say "showing N
// of M" and the truncation advisory must render exactly once — stdout in
// table mode, stderr in --json mode so the structured document stays
// byte-faithful. A response whose total matches the shown rows keeps the
// pre-#404 (%d results) footer with no advisory, so existing parsers are
// unaffected for the common complete case.
func TestSearchFooterDisclosesTruncation(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	// Truncated: 30 shown of 47 total — the footer names both, the advisory
	// renders once, and the bare-total shape is gone.
	server := newSearchServer(t, searchResultsFixture(t, consoleSearchWindow, 47))
	out, err := runRoot(t, server.URL, "search", "needle")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.Contains(out, "(showing 30 of 47 results)") {
		t.Errorf("truncated output missing shown/total footer:\n%s", out)
	}
	if strings.Contains(out, "(47 results)") {
		t.Errorf("truncated output kept the bare-total footer:\n%s", out)
	}
	if n := strings.Count(out, "⚠ Search results are truncated"); n != 1 {
		t.Errorf("truncated output carried %d truncation advisories, want exactly 1:\n%s", n, out)
	}

	// Structured mode: stdout stays a single byte-faithful document (no
	// human markers); the advisory moves to stderr.
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	stdout, stderr, err := runRootCapture(t, cfgPath, server.URL, "search", "needle", "--json")
	if err != nil {
		t.Fatalf("search --json: %v", err)
	}
	assertNoTableOutput(t, stdout)
	doc := unmarshalOneJSON(t, stdout)
	results, ok := doc["results"].([]any)
	if !ok || len(results) != consoleSearchWindow {
		t.Fatalf("stdout results = %T len %v, want %d entries", doc["results"], len(results), consoleSearchWindow)
	}
	if strings.Contains(stdout, "⚠") {
		t.Errorf("structured stdout carries human markers:\n%s", stdout)
	}
	if !strings.Contains(stderr, "⚠ Search results are truncated") {
		t.Errorf("json stderr missing truncation advisory:\n%s", stderr)
	}

	// Complete: total == shown keeps the bare footer, advisory-free, so
	// existing parsers are unaffected for the common case.
	server2 := newSearchServer(t, searchResultsFixture(t, 2, 2))
	out, err = runRoot(t, server2.URL, "search", "needle")
	if err != nil {
		t.Fatalf("search (complete): %v", err)
	}
	if !strings.Contains(out, "(2 results)") {
		t.Errorf("complete output missing bare-total footer:\n%s", out)
	}
	if strings.Contains(out, "⚠") {
		t.Errorf("complete output produced a truncation advisory:\n%s", out)
	}
}
