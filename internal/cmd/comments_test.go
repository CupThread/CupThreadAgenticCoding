package cmd

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// moderationCommentsFixture exercises the Console Moderation list payload,
// including a hidden comment that must surface in the output.
const moderationCommentsFixture = `{
	"comments": [
		{
			"id": "cmt_visible_1234",
			"featureRequestId": "fr_1",
			"authorName": "Ada",
			"body": "Please add dark mode",
			"isHidden": false,
			"createdAt": "2026-09-18T01:02:03Z"
		},
		{
			"id": "cmt_hidden_5678",
			"featureRequestId": "fr_1",
			"authorName": "Bob",
			"body": "spam message",
			"isHidden": true,
			"createdAt": "2026-09-18T04:05:06Z"
		}
	]
}`

// runModerationRoot executes the CLI with a test bearer token, since the
// Console Moderation endpoints require authentication.
func runModerationRoot(t *testing.T, serverURL string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	return runRoot(t, serverURL, args...)
}

// runPublicRoot also executes the CLI with a test bearer token: the public
// thread endpoint itself is anonymous-capable, but the CLI gates every
// command behind a login.
func runPublicRoot(t *testing.T, serverURL string, args ...string) (string, error) {
	t.Helper()
	return runModerationRoot(t, serverURL, args...)
}

// TestCommentsModerationList covers the workspace-scoped moderation list
// endpoint and asserts the path id is authoritative (no X-Workspace-Id).
func TestCommentsModerationList(t *testing.T) {
	var gotMethod, gotPath, gotWorkspace string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotWorkspace = r.Method, r.URL.Path, r.Header.Get("X-Workspace-Id")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(moderationCommentsFixture))
	}))
	defer server.Close()

	out, err := runModerationRoot(t, server.URL, "comments", "moderation", "list", "fr_1", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("moderation list: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/api/v1/console/workspaces/ws_1/feature-requests/fr_1/comments" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	if gotWorkspace != "" {
		t.Errorf("X-Workspace-Id = %q, want no header (path id is authoritative)", gotWorkspace)
	}
	for _, want := range []string{"Ada", "Please add dark mode", "Bob", "spam", "yes", "(2 comments)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestCommentsModerationListNotFound verifies the 404 response (feature
// request missing from the workspace) becomes an explicit error.
func TestCommentsModerationListNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Feature request not found in this workspace"}`))
	}))
	defer server.Close()

	_, err := runModerationRoot(t, server.URL, "comments", "moderation", "list", "fr_missing", "--workspace", "ws_1")
	if err == nil || !strings.Contains(err.Error(), `feature request "fr_missing" not found in workspace ws_1`) {
		t.Fatalf("error = %v, want a not-found message naming the workspace", err)
	}
}

// TestCommentsModerationHideUnhide covers both variants of the hide endpoint,
// including the isHidden request body.
func TestCommentsModerationHideUnhide(t *testing.T) {
	cases := []struct {
		name         string
		args         []string
		wantBody     string
		wantResponse string
	}{
		{name: "hide", args: []string{"hide", "cmt_1"}, wantBody: `{"isHidden":true}`, wantResponse: "hidden"},
		{name: "unhide", args: []string{"unhide", "cmt_1"}, wantBody: `{"isHidden":false}`, wantResponse: "unhidden"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotMethod, gotPath, gotBody string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath = r.Method, r.URL.Path
				body, _ := io.ReadAll(r.Body)
				gotBody = string(body)
				_, _ = w.Write([]byte(`{"success":true}`))
			}))
			defer server.Close()

			args := append([]string{"comments", "moderation"}, append(tc.args, "--workspace", "ws_1")...)
			out, err := runModerationRoot(t, server.URL, args...)
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if gotMethod != http.MethodPatch || gotPath != "/api/v1/console/workspaces/ws_1/comments/cmt_1/hide" {
				t.Errorf("request = %s %s", gotMethod, gotPath)
			}
			if gotBody != tc.wantBody {
				t.Errorf("body = %s, want %s", gotBody, tc.wantBody)
			}
			if !strings.Contains(out, tc.wantResponse) {
				t.Errorf("output missing %q:\n%s", tc.wantResponse, out)
			}
		})
	}
}

// TestCommentsModerationHideNotFound verifies a stale comment id on the hide
// endpoint surfaces as 404, not a success:false body.
func TestCommentsModerationHideNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Comment not found in this workspace"}`))
	}))
	defer server.Close()

	_, err := runModerationRoot(t, server.URL, "comments", "moderation", "hide", "cmt_gone", "--workspace", "ws_1")
	if err == nil || !strings.Contains(err.Error(), `comment "cmt_gone" not found in workspace ws_1`) {
		t.Fatalf("error = %v, want a not-found message naming the workspace", err)
	}
}

// TestCommentsModerationDelete covers the permanent delete endpoint and its
// 404 path.
func TestCommentsModerationDelete(t *testing.T) {
	var gotMethod, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		if strings.HasSuffix(r.URL.Path, "cmt_gone") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Comment not found in this workspace"}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()

	out, err := runModerationRoot(t, server.URL, "comments", "moderation", "delete", "cmt_1", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("moderation delete: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/api/v1/console/workspaces/ws_1/comments/cmt_1" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(out, "deleted") {
		t.Errorf("output missing confirmation:\n%s", out)
	}

	_, err = runModerationRoot(t, server.URL, "comments", "moderation", "delete", "cmt_gone", "--workspace", "ws_1")
	if err == nil || !strings.Contains(err.Error(), `comment "cmt_gone" not found in workspace ws_1`) {
		t.Fatalf("error = %v, want a not-found message naming the workspace", err)
	}
}

// TestCommentsModerationListJSON keeps machine-readable output on the raw API
// contract field names.
func TestCommentsModerationListJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(moderationCommentsFixture))
	}))
	defer server.Close()

	out, err := runModerationRoot(t, server.URL, "comments", "moderation", "list", "fr_1", "--workspace", "ws_1", "--json")
	if err != nil {
		t.Fatalf("moderation list --json: %v", err)
	}
	for _, want := range []string{`"isHidden": true`, `"authorName": "Ada"`} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON output missing %q:\n%s", want, out)
		}
	}
}

// recordedCommentRequest captures one request a comment-thread command made.
type recordedCommentRequest struct {
	method  string
	path    string
	query   string
	appKey  string
	userTok string
}

// serveCommentPages serves the given JSON bodies one per request in order
// (the last one repeats forever) and records every request.
func serveCommentPages(t *testing.T, pages []string) (*httptest.Server, *[]recordedCommentRequest) {
	t.Helper()
	var seen []recordedCommentRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, recordedCommentRequest{
			method:  r.Method,
			path:    r.URL.Path,
			query:   r.URL.RawQuery,
			appKey:  r.Header.Get("X-App-Key"),
			userTok: r.Header.Get("X-User-Token"),
		})
		body := pages[len(pages)-1]
		if n := len(seen); n <= len(pages) {
			body = pages[n-1]
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	return server, &seen
}

// commentPage builds one keyset-paginated thread page (PROD-31); an empty
// nextCursor means the last page.
func commentPage(commentsJSON string, total int, nextCursor string) string {
	hasMore := "false"
	cursor := "null"
	if nextCursor != "" {
		hasMore = "true"
		cursor = `"` + nextCursor + `"`
	}
	return fmt.Sprintf(`{"comments": [%s], "total": %d, "hasMore": %s, "nextCursor": %s}`,
		commentsJSON, total, hasMore, cursor)
}

func testComment(id, body string) string {
	return fmt.Sprintf(`{"id": %q, "featureRequestId": "fr_1", "authorName": "A %s", "body": %q, "isHidden": false, "createdAt": "2026-09-18T01:02:03Z"}`,
		id, id, body)
}

// TestCommentsListWalksAllPages covers the public thread listing following
// nextCursor to the end: the first request keeps the historic empty query,
// the second echoes the cursor back, and the identity headers ride on every
// page. The trailing count is the server's authoritative total.
func TestCommentsListWalksAllPages(t *testing.T) {
	server, seen := serveCommentPages(t, []string{
		commentPage(testComment("cmt_a1", "first page one"), 3, "cur_2"),
		commentPage(testComment("cmt_b2", "second page two")+","+testComment("cmt_c3", "second page three"), 3, ""),
	})
	defer server.Close()

	out, err := runPublicRoot(t, server.URL, "comments", "list", "fr_1", "--app-key", "key_live_x", "--user-token", "usr_1")
	if err != nil {
		t.Fatalf("comments list: %v", err)
	}
	if len(*seen) != 2 {
		t.Fatalf("requests = %d, want 2 pages", len(*seen))
	}
	first, second := (*seen)[0], (*seen)[1]
	if first.query != "" {
		t.Errorf("page 1 query = %q, want empty (no cursor on the first page)", first.query)
	}
	if !strings.Contains(second.query, "cursor=cur_2") {
		t.Errorf("page 2 query = %q, want the echoed cursor", second.query)
	}
	for i, req := range *seen {
		if req.appKey != "key_live_x" || req.userTok != "usr_1" {
			t.Errorf("page %d headers X-App-Key=%q X-User-Token=%q, want both identity headers forwarded", i+1, req.appKey, req.userTok)
		}
	}
	for _, want := range []string{"first page one", "second page two", "second page three", "(3 comments)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestCommentsListCountsByTotalNotPageLength pins the issue's core fix: the
// human count line reports the server's total (a comment posted between page
// fetches makes total exceed the aggregated rows), not len(comments).
func TestCommentsListCountsByTotalNotPageLength(t *testing.T) {
	server, seen := serveCommentPages(t, []string{
		commentPage(testComment("cmt_a1", "one"), 4, "cur_2"),
		commentPage(testComment("cmt_b2", "two")+","+testComment("cmt_c3", "three"), 4, ""),
	})
	defer server.Close()

	out, err := runPublicRoot(t, server.URL, "comments", "list", "fr_1")
	if err != nil {
		t.Fatalf("comments list: %v", err)
	}
	if len(*seen) != 2 {
		t.Fatalf("requests = %d, want 2 pages", len(*seen))
	}
	if !strings.Contains(out, "(4 comments)") {
		t.Errorf("count line must show the server total 4, got:\n%s", out)
	}
}

// TestCommentsListOldShapeSinglePage verifies a pre-pagination response
// (comments array only, no total/nextCursor) still renders: one request, and
// the count falls back to len(comments).
func TestCommentsListOldShapeSinglePage(t *testing.T) {
	server, seen := serveCommentPages(t, []string{
		`{"comments": [` + testComment("cmt_a1", "legacy one") + `,` + testComment("cmt_b2", "legacy two") + `]}`,
	})
	defer server.Close()

	out, err := runPublicRoot(t, server.URL, "comments", "list", "fr_1")
	if err != nil {
		t.Fatalf("comments list: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("requests = %d, want a single page for an old-shape response", len(*seen))
	}
	if !strings.Contains(out, "(2 comments)") {
		t.Errorf("output missing legacy count:\n%s", out)
	}
}

// TestCommentsListJSONAggregatesPages checks that --json emits the whole
// walked thread with the final page's paging metadata (hasMore false,
// nextCursor null) rather than just the first wire page.
func TestCommentsListJSONAggregatesPages(t *testing.T) {
	server, _ := serveCommentPages(t, []string{
		commentPage(testComment("cmt_a1", "json one"), 3, "cur_2"),
		commentPage(testComment("cmt_b2", "json two")+","+testComment("cmt_c3", "json three"), 3, ""),
	})
	defer server.Close()

	out, err := runPublicRoot(t, server.URL, "comments", "list", "fr_1", "--json")
	if err != nil {
		t.Fatalf("comments list --json: %v", err)
	}
	for _, want := range []string{`"id": "cmt_a1"`, `"id": "cmt_b2"`, `"id": "cmt_c3"`, `"total": 3`, `"hasMore": false`, `"nextCursor": null`} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON output missing %q:\n%s", want, out)
		}
	}
}

// TestCommentsModerationListWalksPages covers the Console moderation listing
// walking the same keyset pagination, with a hidden comment on the second
// page still rendered.
func TestCommentsModerationListWalksPages(t *testing.T) {
	hidden := `{"id": "cmt_hidden_9", "featureRequestId": "fr_1", "authorName": "Eve", "body": "spam message", "isHidden": true, "createdAt": "2026-09-18T09:09:09Z"}`
	server, seen := serveCommentPages(t, []string{
		commentPage(testComment("cmt_a1", "visible one"), 3, "cur_2"),
		commentPage(testComment("cmt_b2", "visible two")+","+hidden, 3, ""),
	})
	defer server.Close()

	out, err := runModerationRoot(t, server.URL, "comments", "moderation", "list", "fr_1", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("moderation list: %v", err)
	}
	if len(*seen) != 2 {
		t.Fatalf("requests = %d, want 2 pages", len(*seen))
	}
	for i, req := range *seen {
		if req.path != "/api/v1/console/workspaces/ws_1/feature-requests/fr_1/comments" {
			t.Errorf("page %d path = %s", i+1, req.path)
		}
	}
	if !strings.Contains((*seen)[1].query, "cursor=cur_2") {
		t.Errorf("page 2 query = %q, want the echoed cursor", (*seen)[1].query)
	}
	for _, want := range []string{"visible one", "visible two", "spam message", "yes", "(3 comments)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestCommentsListPageCapGuard verifies the runaway-paging guard: a server
// that never reports a last page stops the CLI after maxCommentPages pages
// with an error naming the cap, instead of paging forever.
func TestCommentsListPageCapGuard(t *testing.T) {
	var n int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		body := fmt.Sprintf(`{"comments": [%s], "total": 100000, "hasMore": true, "nextCursor": "cur_%d"}`,
			testComment(fmt.Sprintf("cmt_%d", n), "endless"), n)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	_, err := runPublicRoot(t, server.URL, "comments", "list", "fr_1")
	if err == nil || !strings.Contains(err.Error(), "did not end within 50 pages") {
		t.Fatalf("error = %v, want the page-cap message", err)
	}
	if n != maxCommentPages {
		t.Errorf("requests = %d, want exactly maxCommentPages (%d)", n, maxCommentPages)
	}
}
