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
		{"changelog update", []string{"changelog", "update", "@ID@", "--title", "Test entry"}, "/api/v1/console/workspaces/ws_1/changelog/%s"},
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
		{args: []string{"columns", "list", "--app", "app_a"}, wantPath: "/api/v1/console/workspaces/ws_1/apps/app_a/columns"},
		{args: []string{"versions", "create", "--app", "app_a", "--label", "v"}, wantPath: "/api/v1/console/workspaces/ws_1/apps/app_a/versions"},
		{args: []string{"imports", "create", "--app", "app_a", "--source", "github_issues"}, wantPath: "/api/v1/console/workspaces/ws_1/apps/app_a/imports"},
		{args: []string{"integrations", "github", "categories", "--owner", "acme", "--repo", "ios"}, wantPath: "/api/v1/console/workspaces/ws_1/integrations/github/repos/acme/ios/categories"},
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

// TestHostileAppIDsStayOnePathSegment pins the issue #303 escaping on the
// app-scoped commands. --app and the saved default app are free-form strings
// at the same trust level as --workspace, and their raw fmt.Sprintf
// interpolation let a metacharacter reshape the authenticated request the
// way #175 documented for ids: '#' dropped the /columns, /versions or
// /imports route (the POST mutations fired, bearer attached, against the
// truncated app-detail route), '?' turned the tail into a query string,
// '/'/'../' added or traversed segments, and an invalid '%' failed the
// request build with an opaque invalid URL escape error. After the fix every
// hostile value stays one encoded segment and the command method lands on
// the full route.
func TestHostileAppIDsStayOnePathSegment(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	cases := []struct {
		name       string
		args       []string
		wantMethod string
		wantPath   string
	}{
		{"columns list", []string{"columns", "list", "--app", "@APP@"}, "GET", "/api/v1/console/workspaces/ws_1/apps/%s/columns"},
		{"columns create", []string{"columns", "create", "--app", "@APP@", "--name", "n"}, "POST", "/api/v1/console/workspaces/ws_1/apps/%s/columns"},
		{"versions list", []string{"versions", "list", "--app", "@APP@"}, "GET", "/api/v1/console/workspaces/ws_1/apps/%s/versions"},
		{"versions create", []string{"versions", "create", "--app", "@APP@", "--label", "v"}, "POST", "/api/v1/console/workspaces/ws_1/apps/%s/versions"},
		{"imports list", []string{"imports", "list", "--app", "@APP@"}, "GET", "/api/v1/console/workspaces/ws_1/apps/%s/imports"},
		{"imports create", []string{"imports", "create", "--app", "@APP@", "--source", "github_issues"}, "POST", "/api/v1/console/workspaces/ws_1/apps/%s/imports"},
	}

	apps := []struct {
		raw    string
		wireID string
	}{
		{"app_1#x", "app_1%23x"},
		{"app_1?q", "app_1%3Fq"},
		{"a/b", "a%2Fb"},
		{"a/../b", "a%2F..%2Fb"},
		{"a%zz", "a%25zz"},
	}

	for _, tc := range cases {
		for _, id := range apps {
			name := tc.name + " " + id.raw
			t.Run(name, func(t *testing.T) {
				var methods, paths, queries []string
				server := escapeServer(t, &methods, &paths, &queries)

				args := make([]string, 0, len(tc.args))
				for _, a := range tc.args {
					if a == "@APP@" {
						a = id.raw
					}
					args = append(args, a)
				}
				// The command failing after the round trip (e.g. on an empty
				// response body) is irrelevant here; the pin is the request
				// shape.
				_, _ = runRootWithSeededConfig(t, server.URL, defaultAppConfig, args...)

				wantPath := strings.Replace(tc.wantPath, "%s", id.wireID, 1)
				if len(paths) != 1 {
					t.Fatalf("issued %d request(s), want exactly 1 (paths %q)", len(paths), paths)
				}
				if methods[0] != tc.wantMethod {
					t.Errorf("wire method = %s, want %s", methods[0], tc.wantMethod)
				}
				if paths[0] != wantPath {
					t.Errorf("wire path = %s, want %s (app id escaped, route suffix intact)", paths[0], wantPath)
				}
				if queries[0] != "" {
					t.Errorf("wire query = %q, want none ('?' in the app id must stay encoded in the path)", queries[0])
				}
			})
		}
	}
}

// TestHostileAppIDForwardKeepsRoute drives features forward, the one
// app-segment command with a pre-flight listing lookup: the hostile --app
// must not only survive the listing round trip but keep the follow-up POST
// on the full encoded /github/forward route (issue #303's sharpest case —
// an authenticated mutation). The stub answers the listing with the shared
// fixture so the lookup finds fr_a_1.
func TestHostileAppIDForwardKeepsRoute(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	for _, id := range []struct{ raw, wireID string }{
		{"app_1#x", "app_1%23x"},
		{"app_1?q", "app_1%3Fq"},
		{"a/b", "a%2Fb"},
		{"a/../b", "a%2F..%2Fb"},
		{"a%zz", "a%25zz"},
	} {
		t.Run(id.raw, func(t *testing.T) {
			var methods, paths, queries []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				methods = append(methods, r.Method)
				paths = append(paths, r.URL.EscapedPath())
				queries = append(queries, r.URL.RawQuery)
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.EscapedPath(), "/github/forward") {
					_, _ = w.Write([]byte(`{"success":true,"targetType":"discussion","url":"https://github.com/acme/ios/discussions/9"}`))
					return
				}
				_, _ = w.Write([]byte(adminRequestsFixture))
			}))
			t.Cleanup(server.Close)

			_, _ = runRootWithSeededConfig(t, server.URL, defaultAppConfig,
				"features", "forward", "fr_a_1", "--app", id.raw)

			forwardIdx := -1
			for i, p := range paths {
				if strings.HasSuffix(p, "/github/forward") {
					forwardIdx = i
					break
				}
			}
			if forwardIdx < 0 {
				t.Fatalf("no /github/forward request reached the wire (paths %q)", paths)
			}
			want := "/api/v1/console/workspaces/ws_1/apps/" + id.wireID + "/features/fr_a_1/github/forward"
			if methods[forwardIdx] != http.MethodPost {
				t.Errorf("forward method = %s, want POST", methods[forwardIdx])
			}
			if paths[forwardIdx] != want {
				t.Errorf("forward wire path = %s, want %s (app id escaped, route intact)", paths[forwardIdx], want)
			}
			if queries[forwardIdx] != "" {
				t.Errorf("forward query = %q, want none", queries[forwardIdx])
			}
		})
	}
}

// TestPathEscapeShapesLookupApp pins lookupApp, the last raw workspace
// interpolation (issue #303 root.go): it bypassed wsPath entirely, so a
// --workspace metacharacter reshaped the apps listing every lookupApp caller
// relies on — '?' turned /apps into a query string, '#' dropped it, and the
// redundant X-Workspace-Id header is suppressed on workspace-scoped paths,
// leaving the server no hint. The route feeds apps get/use/update, apps
// settings, and the integrations github commands, so pinning two callers
// covers the shared path; the plain-workspace case pins the listing route
// verbatim (no double encoding) through a listing the lookup matches.
func TestPathEscapeShapesLookupApp(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	t.Run("metacharacter workspace stays one segment", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			args []string
			ws   string // pre-escaped wire form of the --workspace value
		}{
			{"apps get", []string{"apps", "get", "app_a", "--workspace", "a?b"}, "a%3Fb"},
			{"integrations github config", []string{"integrations", "github", "config", "app_a", "--workspace", "x#y", "--repo", "r"}, "x%23y"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var methods, paths, queries []string
				server := escapeServer(t, &methods, &paths, &queries)

				// lookupApp fails on the empty listing and the command errors;
				// the pin is the listing request shape.
				_, _ = runRootWithSeededConfig(t, server.URL, defaultAppConfig, tc.args...)

				want := "/api/v1/console/workspaces/" + tc.ws + "/apps"
				if len(paths) != 1 {
					t.Fatalf("%v: issued %d request(s), want exactly 1 (paths %q)", tc.args, len(paths), paths)
				}
				if paths[0] != want {
					t.Errorf("%v wire path = %s, want %s", tc.args, paths[0], want)
				}
				if queries[0] != "" {
					t.Errorf("%v wire query = %q, want none", tc.args, queries[0])
				}
			})
		}
	})

	t.Run("plain workspace keeps listing route verbatim", func(t *testing.T) {
		var methods, paths, queries []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			methods = append(methods, r.Method)
			paths = append(paths, r.URL.EscapedPath())
			queries = append(queries, r.URL.RawQuery)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"apps":[{"appId":"app_a","appKey":"ak_1","slug":"app-a","name":"App A"}],"total":1}`))
		}))
		t.Cleanup(server.Close)

		if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "apps", "get", "app_a"); err != nil {
			t.Fatalf("apps get: %v", err)
		}

		want := "/api/v1/console/workspaces/ws_1/apps"
		if len(paths) != 1 {
			t.Fatalf("issued %d request(s), want exactly 1 (paths %q)", len(paths), paths)
		}
		if paths[0] != want {
			t.Errorf("wire path = %s, want %s (verbatim, no double encoding)", paths[0], want)
		}
	})
}

// TestHostileGitHubOwnerRepoStayOnePathSegment pins the integrations github
// categories route (issue #303): --owner/--repo are pasted from GitHub URLs
// — untrusted content — and their raw interpolation let '#' truncate the
// route and '?' inject a query string.
func TestHostileGitHubOwnerRepoStayOnePathSegment(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	for _, tc := range []struct{ owner, repo, wireOwner, wireRepo string }{
		{"o#x", "r", "o%23x", "r"},
		{"o", "r?q", "o", "r%3Fq"},
		{"o", "a/b", "o", "a%2Fb"},
		{"a%zz", "r", "a%25zz", "r"},
	} {
		t.Run(tc.owner+" "+tc.repo, func(t *testing.T) {
			var methods, paths, queries []string
			server := escapeServer(t, &methods, &paths, &queries)

			_, _ = runRootWithSeededConfig(t, server.URL, defaultAppConfig,
				"integrations", "github", "categories", "--owner", tc.owner, "--repo", tc.repo)

			want := "/api/v1/console/workspaces/ws_1/integrations/github/repos/" + tc.wireOwner + "/" + tc.wireRepo + "/categories"
			if len(paths) != 1 {
				t.Fatalf("issued %d request(s), want exactly 1 (paths %q)", len(paths), paths)
			}
			if paths[0] != want {
				t.Errorf("wire path = %s, want %s (owner/repo escaped, route intact)", paths[0], want)
			}
			if queries[0] != "" {
				t.Errorf("wire query = %q, want none", queries[0])
			}
		})
	}
}
