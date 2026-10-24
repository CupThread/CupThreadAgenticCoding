package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// escapeServer records the exact wire form of every request: EscapedPath (not
// the decoded r.URL.Path) is what upstream routing sees, and RawQuery proves
// no '?' in an id was promoted to a query string.
func escapeServer(t *testing.T, methods *[]string, paths *[]string, queries *[]string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*methods = append(*methods, r.Method)
		*paths = append(*paths, r.URL.EscapedPath())
		*queries = append(*queries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	return server
}

// TestHostileIDsStayOnePathSegment pins the issue #175 escaping on every
// ID-taking command: an id pasted from untrusted content containing '#', '?',
// '/', '../' or an invalid percent-escape must reach the wire percent-encoded
// as exactly one path segment, never truncating the route's /suffix (an
// authenticated mutation aimed at a different endpoint), never injecting a
// query string, and never adding or traversing segments. Before the fix each
// of these ids silently reshaped the request; after it the server's 404 for
// the encoded-but-unknown id is the command's honest outcome.
func TestHostileIDsStayOnePathSegment(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	// ws_1 comes from defaultAppConfig; %s is where the escaped id lands.
	// @ID@ marks the positional the hostile id is spliced into.
	cases := []struct {
		name     string
		args     []string
		wantPath string
	}{
		{"comments moderation list", []string{"comments", "moderation", "list", "@ID@"}, "/api/v1/console/workspaces/ws_1/feature-requests/%s/comments"},
		{"comments moderation hide", []string{"comments", "moderation", "hide", "@ID@"}, "/api/v1/console/workspaces/ws_1/comments/%s/hide"},
		{"comments moderation unhide", []string{"comments", "moderation", "unhide", "@ID@"}, "/api/v1/console/workspaces/ws_1/comments/%s/hide"},
		{"comments moderation delete", []string{"comments", "moderation", "delete", "@ID@", "--yes"}, "/api/v1/console/workspaces/ws_1/comments/%s"},
		{"comments list public", []string{"comments", "list", "@ID@"}, "/api/v1/feature-requests/%s/comments"},
		{"users profile", []string{"users", "profile", "@ID@"}, "/api/v1/users/%s/profile"},
		{"notifications read", []string{"notifications", "read", "@ID@"}, "/api/v1/console/workspaces/ws_1/notifications/%s/read"},
		{"changelog update", []string{"changelog", "update", "@ID@", "--title", "t"}, "/api/v1/console/workspaces/ws_1/changelog/%s"},
		{"changelog delete", []string{"changelog", "delete", "@ID@", "--yes"}, "/api/v1/console/workspaces/ws_1/changelog/%s"},
		{"changelog publish", []string{"changelog", "publish", "@ID@"}, "/api/v1/console/workspaces/ws_1/changelog/%s/publish"},
		{"changelog unpublish", []string{"changelog", "unpublish", "@ID@"}, "/api/v1/console/workspaces/ws_1/changelog/%s/unpublish"},
		{"imports get", []string{"imports", "get", "@ID@"}, "/api/v1/console/workspaces/ws_1/imports/%s"},
		{"imports cancel", []string{"imports", "cancel", "@ID@", "--yes"}, "/api/v1/console/workspaces/ws_1/imports/%s/cancel"},
		{"imports rerun", []string{"imports", "rerun", "@ID@"}, "/api/v1/console/workspaces/ws_1/imports/%s/rerun"},
		{"workspaces members set-role", []string{"workspaces", "members", "set-role", "@ID@", "--role", "admin"}, "/api/v1/console/workspaces/ws_1/members/%s"},
		{"workspaces members remove", []string{"workspaces", "members", "remove", "@ID@", "--yes"}, "/api/v1/console/workspaces/ws_1/members/%s"},
		{"workspaces invitations revoke", []string{"workspaces", "invitations", "revoke", "@ID@"}, "/api/v1/console/workspaces/ws_1/invitations/%s"},
		{"columns update", []string{"columns", "update", "@ID@", "--name", "n"}, "/api/v1/console/workspaces/ws_1/columns/%s"},
		{"columns delete", []string{"columns", "delete", "@ID@", "--yes"}, "/api/v1/console/workspaces/ws_1/columns/%s"},
		{"versions update", []string{"versions", "update", "@ID@", "--label", "v"}, "/api/v1/console/workspaces/ws_1/versions/%s"},
		{"versions delete", []string{"versions", "delete", "@ID@", "--yes"}, "/api/v1/console/workspaces/ws_1/versions/%s"},
		{"inbox get", []string{"inbox", "get", "@ID@"}, "/api/v1/console/workspaces/ws_1/submissions/%s"},
		{"inbox priority", []string{"inbox", "priority", "@ID@", "!!!"}, "/api/v1/console/workspaces/ws_1/submissions/%s/priority"},
		{"inbox triage", []string{"inbox", "triage", "@ID@", "open"}, "/api/v1/console/workspaces/ws_1/submissions/%s/triage"},
		{"inbox assign", []string{"inbox", "assign", "@ID@"}, "/api/v1/console/workspaces/ws_1/submissions/%s/assign"},
		{"inbox retry", []string{"inbox", "retry", "@ID@"}, "/api/v1/console/workspaces/ws_1/submissions/%s/retry"},
	}

	// Each hostile id from the issue table with its expected wire encoding:
	// '#' must not swallow the /suffix, '?' must not become a query, '/' and
	// '../' must stay inside the segment, and an invalid escape must be
	// re-encoded instead of failing http.NewRequest with a confusing
	// invalid URL escape error.
	ids := []struct {
		raw    string
		wireID string
	}{
		{"req_1#x", "req_1%23x"},
		{"user_1?q", "user_1%3Fq"},
		{"a/b", "a%2Fb"},
		{"a/../b", "a%2F..%2Fb"},
		{"a%zz", "a%25zz"},
	}

	for _, tc := range cases {
		for _, id := range ids {
			name := tc.name + " " + id.raw
			t.Run(name, func(t *testing.T) {
				var methods, paths, queries []string
				server := escapeServer(t, &methods, &paths, &queries)

				full := make([]string, 0, len(tc.args)+1)
				for _, a := range tc.args {
					if a == "@ID@" {
						a = id.raw
					}
					full = append(full, a)
				}
				// The command failing after the round trip (e.g. on an
				// empty response body) is irrelevant here; the pin is the
				// request shape.
				_, _ = runRootWithSeededConfig(t, server.URL, defaultAppConfig, full...)

				wantPath := strings.Replace(tc.wantPath, "%s", id.wireID, 1)
				if len(paths) != 1 {
					t.Fatalf("issued %d request(s), want exactly 1 (paths %q)", len(paths), paths)
				}
				if paths[0] != wantPath {
					t.Errorf("wire path = %s, want %s (id escaped, one segment)", paths[0], wantPath)
				}
				if queries[0] != "" {
					t.Errorf("wire query = %q, want none ('?' in the id must stay encoded in the path)", queries[0])
				}
			})
		}
	}
}

// TestPathEscapeKeepsPlainIDsVerbatim pins that the escaping changed nothing
// for well-formed ids — no double encoding: a plain id reaches the wire
// byte-for-byte, and the comment thread commands keep their /comments suffix
// (the regression the issue flags as today's sharpest case for
// body-carrying writes).
func TestPathEscapeKeepsPlainIDsVerbatim(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	var methods, paths, queries []string
	server := escapeServer(t, &methods, &paths, &queries)

	for _, tc := range []struct {
		args      []string
		wantPath  string
		wantQuery string
	}{
		{args: []string{"inbox", "get", "sub_1"}, wantPath: "/api/v1/console/workspaces/ws_1/submissions/sub_1"},
		{args: []string{"changelog", "publish", "entry_1"}, wantPath: "/api/v1/console/workspaces/ws_1/changelog/entry_1/publish"},
		{args: []string{"comments", "moderation", "list", "req_abc"}, wantPath: "/api/v1/console/workspaces/ws_1/feature-requests/req_abc/comments"},
		{args: []string{"comments", "list", "req_abc"}, wantPath: "/api/v1/feature-requests/req_abc/comments"},
		{args: []string{"users", "profile", "u_0123456789abcdef0123456789abcdef", "--app-key", "ak_1"}, wantPath: "/api/v1/users/u_0123456789abcdef0123456789abcdef/profile", wantQuery: "appKey=ak_1"},
		{args: []string{"workspaces", "members", "remove", "user_1", "--yes"}, wantPath: "/api/v1/console/workspaces/ws_1/members/user_1"},
	} {
		if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, tc.args...); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		got := paths[len(paths)-1]
		if got != tc.wantPath {
			t.Errorf("%v wire path = %s, want %s (verbatim, no double encoding)", tc.args, got, tc.wantPath)
		}
		if q := queries[len(queries)-1]; q != tc.wantQuery {
			t.Errorf("%v wire query = %q, want %q", tc.args, q, tc.wantQuery)
		}
	}
}

// TestPathEscapeShapesWorkspaceInPath pins wsPath itself (issue #175
// workspaces.go:167): a --workspace value carrying a metacharacter stays one
// encoded segment instead of reshaping every workspace-scoped route.
func TestPathEscapeShapesWorkspaceInPath(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	var methods, paths, queries []string
	server := escapeServer(t, &methods, &paths, &queries)

	cfg := `{"defaultWorkspace":"ws_1#x","workspaces":{"ws_1#x":{"defaultApp":"app_a"}}}`
	if _, err := runRootWithSeededConfig(t, server.URL, cfg, "notifications", "read", "notif_1"); err != nil {
		t.Fatalf("notifications read: %v", err)
	}

	want := "/api/v1/console/workspaces/ws_1%23x/notifications/notif_1/read"
	if len(paths) != 1 {
		t.Fatalf("issued %d request(s), want exactly 1 (paths %q)", len(paths), paths)
	}
	if paths[0] != want {
		t.Errorf("wire path = %s, want %s", paths[0], want)
	}
}
