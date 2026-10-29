package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// changelogListFixture exercises the pagination metadata from issue #31
// (total, hasMore) on the console listing.
const changelogListFixture = `{
	"entries": [{
		"id": "cl_entry_1",
		"appId": "app_1",
		"title": "v1.2.0 Release",
		"body": "## Highlights",
		"versionLabel": "1.2.0",
		"versionId": null,
		"publishedAt": "2026-09-01T12:00:00.000Z",
		"scheduledAt": null,
		"notifiedAt": null,
		"linkedRequests": [{"id": "fr_1", "title": "Dark mode"}],
		"subscriberCount": 7,
		"createdAt": "2026-08-30T09:00:00.000Z",
		"updatedAt": "2026-09-01T12:00:00.000Z"
	}],
	"total": 150,
	"hasMore": true
}`

// changelogServer returns a server that replies with respBody and records
// the request path and query.
func changelogServer(t *testing.T, respBody string) (*httptest.Server, *string, *url.Values) {
	t.Helper()
	var gotPath string
	var gotQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respBody))
	}))
	t.Cleanup(server.Close)
	return server, &gotPath, &gotQuery
}

// TestChangelogListSendsPagination covers issue #31: the console listing
// forwards limit/offset and the table surfaces the hasMore hint with the
// next offset.
func TestChangelogListSendsPagination(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	server, gotPath, gotQuery := changelogServer(t, changelogListFixture)

	out, err := runRoot(t, server.URL, "changelog", "list", "--workspace", "ws_1", "--app", "app_1",
		"--limit", "50", "--offset", "100")
	if err != nil {
		t.Fatalf("changelog list: %v", err)
	}
	if *gotPath != "/api/v1/console/workspaces/ws_1/changelog" {
		t.Errorf("request path = %s", *gotPath)
	}
	if got := gotQuery.Get("appId"); got != "app_1" {
		t.Errorf("appId = %q, want app_1", got)
	}
	if got := gotQuery.Get("limit"); got != "50" {
		t.Errorf("limit = %q, want 50", got)
	}
	if got := gotQuery.Get("offset"); got != "100" {
		t.Errorf("offset = %q, want 100", got)
	}
	for _, want := range []string{"v1.2.0 Release", "published", "Showing 1 of 150 entries", "--offset 101"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestChangelogListDefaultsToServerPaging keeps the wire format aligned with
// the API defaults (limit=100, offset=0) when the flags are not passed.
func TestChangelogListDefaultsToServerPaging(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	server, _, gotQuery := changelogServer(t, changelogListFixture)

	if _, err := runRoot(t, server.URL, "changelog", "list", "--workspace", "ws_1", "--app", "app_1"); err != nil {
		t.Fatalf("changelog list: %v", err)
	}
	if got := gotQuery.Get("limit"); got != "100" {
		t.Errorf("limit = %q, want the API default 100", got)
	}
	if got := gotQuery.Get("offset"); got != "0" {
		t.Errorf("offset = %q, want 0", got)
	}
}

// TestChangelogListJSONKeepsPaginationFields verifies machine-readable
// output preserves the raw API contract field names.
func TestChangelogListJSONKeepsPaginationFields(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	server, _, _ := changelogServer(t, changelogListFixture)

	out, err := runRoot(t, server.URL, "changelog", "list", "--workspace", "ws_1", "--app", "app_1", "--json")
	if err != nil {
		t.Fatalf("changelog list --json: %v", err)
	}
	for _, want := range []string{`"total": 150`, `"hasMore": true`, `"versionLabel": "1.2.0"`} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON output missing %q:\n%s", want, out)
		}
	}
}

// publicChangelogFixture mirrors ListPublicChangelogResponseSchema (PROD-28).
const publicChangelogFixture = `{
	"entries": [{
		"id": "cl_pub_1",
		"title": "v1.2.0 Release",
		"body": "## Highlights",
		"versionLabel": "1.2.0",
		"publishedAt": "2026-09-01T12:00:00.000Z",
		"linkedRequests": [{"id": "fr_1", "title": "Dark mode"}]
	}],
	"hasMore": true,
	"nextCursor": "MjAyNi0wOS0wMXQxMjowMDowMC4wMDBafGNsX3B1Yl8x"
}`

// TestAppsPublicChangelog covers the new first-class command for the public
// feed: path shape, default paging params, and the table hint for the next
// cursor.
func TestAppsPublicChangelog(t *testing.T) {
	server, gotPath, gotQuery := changelogServer(t, publicChangelogFixture)

	out, err := runRoot(t, server.URL, "apps", "public-changelog", "key_live_1")
	if err != nil {
		t.Fatalf("public-changelog: %v", err)
	}
	if *gotPath != "/api/v1/public/apps/key_live_1/changelog" {
		t.Errorf("request path = %s", *gotPath)
	}
	if got := gotQuery.Get("limit"); got != "100" {
		t.Errorf("limit = %q, want the API default 100", got)
	}
	if got := gotQuery.Get("cursor"); got != "" {
		t.Errorf("cursor sent without the flag: %q", got)
	}
	for _, want := range []string{"v1.2.0 Release", "1.2.0", "More entries available", "--cursor MjAyNi0wOS0wMXQxMjowMDowMC4wMDBafGNsX3B1Yl8x"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestAppsPublicChangelogForwardsCursor verifies --limit/--cursor map to the
// query parameters and opaque cursors survive URL encoding.
func TestAppsPublicChangelogForwardsCursor(t *testing.T) {
	server, gotPath, gotQuery := changelogServer(t, publicChangelogFixture)

	cursor := "2026-09-01T12:00:00.000Z|cl_pub_1"
	if _, err := runRoot(t, server.URL, "apps", "public-changelog", "key_live_1",
		"--limit", "50", "--cursor", cursor); err != nil {
		t.Fatalf("public-changelog: %v", err)
	}
	if *gotPath != "/api/v1/public/apps/key_live_1/changelog" {
		t.Errorf("request path = %s", *gotPath)
	}
	if got := gotQuery.Get("limit"); got != "50" {
		t.Errorf("limit = %q, want 50", got)
	}
	if got := gotQuery.Get("cursor"); got != cursor {
		t.Errorf("cursor = %q, want %q (round-trip through query encoding)", got, cursor)
	}
}

// TestAppsPublicChangelogJSONKeepsContract verifies --json output keeps the
// raw hasMore/nextCursor contract field names.
func TestAppsPublicChangelogJSONKeepsContract(t *testing.T) {
	server, _, _ := changelogServer(t, publicChangelogFixture)

	out, err := runRoot(t, server.URL, "apps", "public-changelog", "key_live_1", "--json")
	if err != nil {
		t.Fatalf("public-changelog --json: %v", err)
	}
	for _, want := range []string{
		`"hasMore": true`,
		`"nextCursor": "MjAyNi0wOS0wMXQxMjowMDowMC4wMDBafGNsX3B1Yl8x"`,
		`"publishedAt": "2026-09-01T12:00:00.000Z"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON output missing %q:\n%s", want, out)
		}
	}
}

// TestAppsPublicChangelogLastPage checks the table stays quiet when the feed
// is exhausted (nextCursor null, hasMore false) and that --json still emits a
// present `nextCursor: null` / `hasMore: false` envelope on the last page.
func TestAppsPublicChangelogLastPage(t *testing.T) {
	server, _, _ := changelogServer(t, `{
		"entries": [{
			"id": "cl_pub_9",
			"title": "v1.0.0",
			"body": "Initial release",
			"versionLabel": null,
			"publishedAt": "2026-01-15T08:00:00.000Z",
			"linkedRequests": []
		}],
		"hasMore": false,
		"nextCursor": null
	}`)

	out, err := runRoot(t, server.URL, "apps", "public-changelog", "key_live_1")
	if err != nil {
		t.Fatalf("public-changelog: %v", err)
	}
	if strings.Contains(out, "More entries available") {
		t.Errorf("last page should not hint at a next cursor:\n%s", out)
	}
	if !strings.Contains(out, "v1.0.0") {
		t.Errorf("output missing entry title:\n%s", out)
	}

	jsonOut, err := runRoot(t, server.URL, "apps", "public-changelog", "key_live_1", "--json")
	if err != nil {
		t.Fatalf("public-changelog --json: %v", err)
	}
	var top map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(jsonOut)), &top); err != nil {
		t.Fatalf("unmarshal last-page JSON: %v", err)
	}
	if got, ok := top["hasMore"].(bool); !ok || got {
		t.Errorf("hasMore = %v, want false", top["hasMore"])
	}
	if got, present := top["nextCursor"]; !present || got != nil {
		t.Errorf("nextCursor = %v (present=%t), want present null", got, present)
	}
	entries, ok := top["entries"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("entries = %#v, want a 1-element array", top["entries"])
	}
	entry, ok := entries[0].(map[string]any)
	if !ok {
		t.Fatalf("entries[0] = %#v, want an object", entries[0])
	}
	linked, present := entry["linkedRequests"]
	if !present {
		t.Fatalf("linkedRequests missing from output; the field is required even when empty (issue #259)")
	}
	if _, ok := linked.([]any); !ok {
		t.Errorf("linkedRequests = %#v, want an array even when empty (issue #259)", linked)
	}
}

// TestAppsPublicChangelogRequiresAppKey verifies argument validation.
func TestAppsPublicChangelogRequiresAppKey(t *testing.T) {
	server, gotPath, _ := changelogServer(t, publicChangelogFixture)

	_, err := runRoot(t, server.URL, "apps", "public-changelog")
	if err == nil || !strings.Contains(err.Error(), "accepts 1 arg") {
		t.Fatalf("error = %v, want missing-argument error", err)
	}
	if *gotPath != "" {
		t.Errorf("no request should be made, got %s", *gotPath)
	}
}

// sec40ForbiddenServer returns a server that answers the given 403 body on
// the changelog publish paths and records the request method, path, and JSON
// body for wire-format assertions.
func sec40ForbiddenServer(t *testing.T, respBody string) (*httptest.Server, *string, *string) {
	t.Helper()
	var gotMethod, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(respBody))
	}))
	t.Cleanup(server.Close)
	return server, &gotMethod, &gotPath
}

// TestChangelogPublishInteractiveSessionRequired covers SEC-40 end to end: a
// cpt_ API token calling POST .../publish surfaces the server's 403
// interactive_session_required with the Console-web-UI hint and no re-login
// advice (issue #58 — an OAuth login cannot help either).
func TestChangelogPublishInteractiveSessionRequired(t *testing.T) {
	server, gotMethod, gotPath := sec40ForbiddenServer(t,
		`{"error":"This action requires an interactive session; API tokens are not permitted","code":"interactive_session_required"}`)

	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	_, err := runRoot(t, server.URL, "changelog", "publish", "cl_entry_1", "--workspace", "ws_1")
	if err == nil {
		t.Fatal("expected an error")
	}
	if *gotMethod != http.MethodPost || *gotPath != "/api/v1/console/workspaces/ws_1/changelog/cl_entry_1/publish" {
		t.Errorf("request = %s %s", *gotMethod, *gotPath)
	}
	for _, want := range []string{
		"interactive_session_required",
		"API tokens are not permitted",
		"Console web UI",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "auth login") {
		t.Errorf("error %q still recommends 'auth login' as a remedy", err)
	}
}

// TestChangelogPublishCapabilityRequired covers the role-based denial
// (SEC-40): a member-role credential hitting POST .../publish surfaces 403
// capability_required with the ask-an-admin hint.
func TestChangelogPublishCapabilityRequired(t *testing.T) {
	server, _, _ := sec40ForbiddenServer(t,
		`{"error":"Access denied: your workspace role does not include the 'changelog.publish' capability","code":"capability_required"}`)

	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	_, err := runRoot(t, server.URL, "changelog", "publish", "cl_entry_1", "--workspace", "ws_1")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{
		"capability_required",
		"changelog.publish",
		"ask a workspace admin or owner to perform it",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

// TestChangelogCreatePublishNowInteractiveSessionRequired covers SEC-40 on
// the create path: a cpt_ API token sending publishNow: true still reaches
// the wire (the create itself stays allowed) but the server's 403
// interactive_session_required surfaces with the sign-in hint.
func TestChangelogCreatePublishNowInteractiveSessionRequired(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/console/workspaces/ws_1/changelog" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"This action requires an interactive session; API tokens are not permitted","code":"interactive_session_required"}`))
	}))
	defer server.Close()

	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	_, err := runRoot(t, server.URL, "changelog", "create", "--workspace", "ws_1", "--app", "app_1",
		"--title", "v1.2.0", "--publish-now")
	if err == nil {
		t.Fatal("expected an error")
	}
	if gotBody["publishNow"] != true {
		t.Errorf("request body publishNow = %v, want true", gotBody["publishNow"])
	}
	for _, want := range []string{
		"interactive_session_required",
		"Console web UI",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "auth login") {
		t.Errorf("error %q still recommends 'auth login' as a remedy", err)
	}
}

// TestChangelogCreatePublishNowScheduleAtMutuallyExclusive is the issue #369
// guard: with both flags passed, the server silently resolved the pair in
// favor of --publish-now (createChangelogEntry drops scheduledAt when
// publishNow is set), so the entry published immediately and notified every
// board subscriber while the requested schedule was dropped, with exit code
// 0 — the opposite of what the invocation asked for, and irreversible once
// the notification fan-out ran. The pair is now refused before any request,
// mirroring the --enable/--disable and --events/--all-events guards in
// notifications prefs set (issues #364, #367). The contradiction also
// outranks the --schedule-at datetime validation, so a caller with a
// badly-formatted datetime sees the conflict first instead of fixing the
// format and hitting the same refusal on the next run.
func TestChangelogCreatePublishNowScheduleAtMutuallyExclusive(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	var refused []byte
	refuseServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refused, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cl_entry_1"}`))
	}))
	t.Cleanup(refuseServer.Close)

	_, err := runRoot(t, refuseServer.URL, "changelog", "create", "--workspace", "ws_1", "--app", "app_1",
		"--title", "v1.2.0", "--body", "Hello", "--publish-now", "--schedule-at", "2026-11-05T09:00:00Z")
	if err == nil || !strings.Contains(err.Error(), "--publish-now and --schedule-at are mutually exclusive") {
		t.Fatalf("error = %v, want the mutual-exclusion refusal", err)
	}
	if len(refused) != 0 {
		t.Errorf("conflicting flags reached the API: %s", refused)
	}

	_, err = runRoot(t, refuseServer.URL, "changelog", "create", "--workspace", "ws_1", "--app", "app_1",
		"--title", "v1.2.0", "--body", "Hello", "--publish-now", "--schedule-at", "2026-11-05")
	if err == nil || !strings.Contains(err.Error(), "--publish-now and --schedule-at are mutually exclusive") {
		t.Fatalf("error = %v, want the mutual-exclusion refusal to outrank the datetime check", err)
	}
	if len(refused) != 0 {
		t.Errorf("conflicting flags reached the API: %s", refused)
	}

	// A passed-but-empty --schedule-at is not a contradiction: on create ""
	// is simply omitted from the body, so the invocation is just
	// --publish-now and must still send publishNow: true with no scheduledAt
	// key rather than being refused.
	var gotBody []byte
	publishServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cl_new_1"}`))
	}))
	t.Cleanup(publishServer.Close)

	if _, err := runRoot(t, publishServer.URL, "changelog", "create", "--workspace", "ws_1", "--app", "app_1",
		"--title", "v1.2.0", "--body", "Hello", "--publish-now", "--schedule-at", ""); err != nil {
		t.Fatalf(`create --publish-now --schedule-at "": %v`, err)
	}
	if string(gotBody) != `{"appId":"app_1","body":"Hello","linkedRequestIds":[],"publishNow":true,"title":"v1.2.0"}` {
		t.Errorf("request body = %s, want publishNow: true with no scheduledAt key", gotBody)
	}
}

// TestChangelogCreateSinglePublishFlagWireShapes pins the two
// non-contradictory create bodies the issue #369 guard must not reshape:
// --publish-now alone sends publishNow: true with no scheduledAt key, and
// --schedule-at alone sends the datetime with publishNow: false — the
// server resolves an absent scheduledAt to null either way, so only the
// contradictory pair may be refused.
func TestChangelogCreateSinglePublishFlagWireShapes(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	for _, tc := range []struct {
		name string
		flag []string
		want string
	}{
		{
			name: "publish-now alone",
			flag: []string{"--publish-now"},
			want: `{"appId":"app_1","body":"Hello","linkedRequestIds":[],"publishNow":true,"title":"v1.2.0"}`,
		},
		{
			name: "schedule-at alone",
			flag: []string{"--schedule-at", "2026-11-05T09:00:00Z"},
			want: `{"appId":"app_1","body":"Hello","linkedRequestIds":[],"publishNow":false,"scheduledAt":"2026-11-05T09:00:00Z","title":"v1.2.0"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotBody []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotBody, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"cl_new_1"}`))
			}))
			t.Cleanup(server.Close)

			args := append([]string{"changelog", "create", "--workspace", "ws_1", "--app", "app_1",
				"--title", "v1.2.0", "--body", "Hello"}, tc.flag...)
			if _, err := runRoot(t, server.URL, args...); err != nil {
				t.Fatalf("changelog create %v: %v", tc.flag, err)
			}
			if string(gotBody) != tc.want {
				t.Errorf("request body = %s, want %s", gotBody, tc.want)
			}
		})
	}
}

// TestChangelogUpdateScheduleAtCapabilityRequired covers the last SEC-40
// gate: scheduling via update --schedule-at requires changelog.publish, so a
// member-role credential gets 403 capability_required.
func TestChangelogUpdateScheduleAtCapabilityRequired(t *testing.T) {
	server, gotMethod, gotPath := sec40ForbiddenServer(t,
		`{"error":"Access denied: your workspace role does not include the 'changelog.publish' capability","code":"capability_required"}`)

	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	_, err := runRoot(t, server.URL, "changelog", "update", "cl_entry_1", "--workspace", "ws_1",
		"--schedule-at", "2026-10-01T09:00:00Z")
	if err == nil {
		t.Fatal("expected an error")
	}
	if *gotMethod != http.MethodPut || *gotPath != "/api/v1/console/workspaces/ws_1/changelog/cl_entry_1" {
		t.Errorf("request = %s %s", *gotMethod, *gotPath)
	}
	for _, want := range []string{
		"capability_required",
		"changelog.publish",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

// TestChangelogUpdateScheduleAtEmptySendsNull pins the ungated clear path
// (issue #61): the server schema is z.string().datetime().nullable(), so a
// literal "" would 400 — the CLI must translate a passed-but-empty
// --schedule-at into JSON null, which the PUT publish gate skips
// (typeof scheduledAt === "string") so plain content.manage suffices.
func TestChangelogUpdateScheduleAtEmptySendsNull(t *testing.T) {
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cl_entry_1"}`))
	}))
	t.Cleanup(server.Close)

	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	out, err := runRoot(t, server.URL, "changelog", "update", "cl_entry_1", "--workspace", "ws_1",
		"--schedule-at", "")
	if err != nil {
		t.Fatalf(`update --schedule-at "": %v`, err)
	}
	if !strings.Contains(out, "cl_entry_1") {
		t.Errorf("output %q missing entry id", out)
	}
	if string(gotBody) != `{"scheduledAt":null}` {
		t.Errorf("request body = %s, want {\"scheduledAt\":null}", gotBody)
	}
}

// TestChangelogUpdateScheduleAtValueSendsString pins the gated sibling case:
// a non-empty --schedule-at travels as a datetime string, which is the shape
// the server gates behind changelog.publish.
func TestChangelogUpdateScheduleAtValueSendsString(t *testing.T) {
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cl_entry_1"}`))
	}))
	t.Cleanup(server.Close)

	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	if _, err := runRoot(t, server.URL, "changelog", "update", "cl_entry_1", "--workspace", "ws_1",
		"--schedule-at", "2026-10-01T09:00:00Z"); err != nil {
		t.Fatalf("update --schedule-at <datetime>: %v", err)
	}
	if string(gotBody) != `{"scheduledAt":"2026-10-01T09:00:00Z"}` {
		t.Errorf("request body = %s, want {\"scheduledAt\":\"2026-10-01T09:00:00Z\"}", gotBody)
	}
}

// TestChangelogListClampsLimit pins the local --limit clamp on the changelog
// listing (issue #201): the server's parseListPagination caps this route's
// page at 100 (not the 200 of the other console listings), so the CLI clamps
// first — over-cap to 100, 0/negative to 1 (matching features list) — and
// announces the rewrite: stdout in table mode, stderr in --json mode, and
// nothing at all on the verbatim 100 boundary.
func TestChangelogListClampsLimit(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	server, _, gotQuery := changelogServer(t, changelogListFixture)

	for _, tc := range []struct{ requested, sent string }{
		{"500", "100"},
		{"101", "100"},
		{"0", "1"},
		{"-5", "1"},
	} {
		out, err := runRoot(t, server.URL, "changelog", "list", "--workspace", "ws_1", "--app", "app_1",
			"--limit", tc.requested)
		if err != nil {
			t.Fatalf("changelog list --limit %s: %v", tc.requested, err)
		}
		if got := gotQuery.Get("limit"); got != tc.sent {
			t.Errorf("--limit %s sent limit = %q, want %q", tc.requested, got, tc.sent)
		}
		if !strings.Contains(out, "⚠ --limit "+tc.requested+" is outside the server's 1-100 page range") {
			t.Errorf("--limit %s output missing clamp warning:\n%s", tc.requested, out)
		}
	}

	out, err := runRoot(t, server.URL, "changelog", "list", "--workspace", "ws_1", "--app", "app_1",
		"--limit", "100")
	if err != nil {
		t.Fatalf("changelog list --limit 100: %v", err)
	}
	if got := gotQuery.Get("limit"); got != "100" {
		t.Errorf("boundary --limit 100 sent limit = %q, want 100", got)
	}
	if strings.Contains(out, "⚠") {
		t.Errorf("boundary --limit 100 produced a warning:\n%s", out)
	}

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	stdout, stderr, err := runRootCapture(t, cfgPath, server.URL,
		"changelog", "list", "--workspace", "ws_1", "--app", "app_1", "--limit", "500", "--json")
	if err != nil {
		t.Fatalf("changelog list --json --limit 500: %v", err)
	}
	unmarshalOneJSON(t, stdout)
	assertNoTableOutput(t, stdout)
	if !strings.Contains(stderr, "⚠ --limit 500 is outside the server's 1-100 page range; requesting 100 instead") {
		t.Errorf("json stderr missing clamp warning:\n%s", stderr)
	}
}

// TestChangelogListClampsOffset pins the local --offset clamp on the
// changelog listing (issue #178): the server's parseListPagination silently
// degrades an offset above the shared console cap of 10,000 to the cap
// (still answering 200) and a negative one to 0, so a walk past the cap
// re-serves the same final page with no hint anything changed. The CLI
// clamps first and announces the rewrite: stdout in table mode, stderr in
// --json mode, and nothing at all on the verbatim 10,000 boundary.
func TestChangelogListClampsOffset(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	server, _, gotQuery := changelogServer(t, changelogListFixture)

	for _, tc := range []struct{ requested, sent string }{
		{"15000", "10000"},
		{"10001", "10000"},
		{"-5", "0"},
	} {
		out, err := runRoot(t, server.URL, "changelog", "list", "--workspace", "ws_1", "--app", "app_1",
			"--offset", tc.requested)
		if err != nil {
			t.Fatalf("changelog list --offset %s: %v", tc.requested, err)
		}
		if got := gotQuery.Get("offset"); got != tc.sent {
			t.Errorf("--offset %s sent offset = %q, want %q", tc.requested, got, tc.sent)
		}
		if !strings.Contains(out, "⚠ --offset "+tc.requested+" is outside the server's 0-10000 page range") {
			t.Errorf("--offset %s output missing clamp warning:\n%s", tc.requested, out)
		}
	}

	out, err := runRoot(t, server.URL, "changelog", "list", "--workspace", "ws_1", "--app", "app_1",
		"--offset", "10000")
	if err != nil {
		t.Fatalf("changelog list --offset 10000: %v", err)
	}
	if got := gotQuery.Get("offset"); got != "10000" {
		t.Errorf("boundary --offset 10000 sent offset = %q, want 10000", got)
	}
	if strings.Contains(out, "⚠") {
		t.Errorf("boundary --offset 10000 produced a warning:\n%s", out)
	}

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	stdout, stderr, err := runRootCapture(t, cfgPath, server.URL,
		"changelog", "list", "--workspace", "ws_1", "--app", "app_1", "--offset", "15000", "--json")
	if err != nil {
		t.Fatalf("changelog list --json --offset 15000: %v", err)
	}
	unmarshalOneJSON(t, stdout)
	assertNoTableOutput(t, stdout)
	if !strings.Contains(stderr, "⚠ --offset 15000 is outside the server's 0-10000 page range; requesting 10000 instead") {
		t.Errorf("json stderr missing clamp warning:\n%s", stderr)
	}
}

// TestAppsPublicChangelogClampsLimit pins the local --limit clamp on the
// public changelog feed (issue #271, the #201 discipline): the PROD-28 route
// clamps ?limit= to 1-100 and silently degrades 0 to the default 100, so the
// CLI clamps first — over-cap to 100, 0/negative to 1 — and announces the
// rewrite: stdout in table mode, stderr in --json mode, and nothing at all on
// the verbatim 100 boundary.
func TestAppsPublicChangelogClampsLimit(t *testing.T) {
	server, _, gotQuery := changelogServer(t, publicChangelogFixture)

	for _, tc := range []struct{ requested, sent string }{
		{"500", "100"},
		{"101", "100"},
		{"0", "1"},
		{"-5", "1"},
	} {
		out, err := runRoot(t, server.URL, "apps", "public-changelog", "key_live_1",
			"--limit", tc.requested)
		if err != nil {
			t.Fatalf("public-changelog --limit %s: %v", tc.requested, err)
		}
		if got := gotQuery.Get("limit"); got != tc.sent {
			t.Errorf("--limit %s sent limit = %q, want %q", tc.requested, got, tc.sent)
		}
		if !strings.Contains(out, "⚠ --limit "+tc.requested+" is outside the server's 1-100 page range") {
			t.Errorf("--limit %s output missing clamp warning:\n%s", tc.requested, out)
		}
	}

	out, err := runRoot(t, server.URL, "apps", "public-changelog", "key_live_1", "--limit", "100")
	if err != nil {
		t.Fatalf("public-changelog --limit 100: %v", err)
	}
	if got := gotQuery.Get("limit"); got != "100" {
		t.Errorf("boundary --limit 100 sent limit = %q, want 100", got)
	}
	if strings.Contains(out, "⚠") {
		t.Errorf("boundary --limit 100 produced a warning:\n%s", out)
	}

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	stdout, stderr, err := runRootCapture(t, cfgPath, server.URL,
		"apps", "public-changelog", "key_live_1", "--limit", "500", "--json")
	if err != nil {
		t.Fatalf("public-changelog --json --limit 500: %v", err)
	}
	unmarshalOneJSON(t, stdout)
	assertNoTableOutput(t, stdout)
	if !strings.Contains(stderr, "⚠ --limit 500 is outside the server's 1-100 page range; requesting 100 instead") {
		t.Errorf("json stderr missing clamp warning:\n%s", stderr)
	}
}

// TestAppsPublicChangelogContractPinCorrectedSchema pins the corrected public
// changelog entry contract (issue #259, SaaS #1226 / PR #1273): --json output
// carries exactly the corrected entry keys — no workspace-internal `appId`,
// nullable `versionLabel` (never `versionNumber`), and always-present
// `linkedRequests` — and a legacy payload that still carries the removed keys
// decodes the corrected fields without re-emitting them.
func TestAppsPublicChangelogContractPinCorrectedSchema(t *testing.T) {
	sortedKeys := func(t *testing.T, m map[string]any) []string {
		t.Helper()
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys
	}

	t.Run("served payload round-trips exactly the corrected entry keys", func(t *testing.T) {
		server, _, _ := changelogServer(t, publicChangelogFixture)

		out, err := runRoot(t, server.URL, "apps", "public-changelog", "key_live_1", "--json")
		if err != nil {
			t.Fatalf("public-changelog --json: %v", err)
		}
		var top map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &top); err != nil {
			t.Fatalf("unmarshal structured output: %v", err)
		}
		wantTop := []string{"entries", "hasMore", "nextCursor"}
		if got := sortedKeys(t, top); !reflect.DeepEqual(got, wantTop) {
			t.Errorf("top-level keys = %v, want %v", got, wantTop)
		}

		entries, ok := top["entries"].([]any)
		if !ok || len(entries) != 1 {
			t.Fatalf("entries = %#v, want a 1-element array", top["entries"])
		}
		entry, ok := entries[0].(map[string]any)
		if !ok {
			t.Fatalf("entries[0] = %#v, want an object", entries[0])
		}
		wantEntry := []string{"body", "id", "linkedRequests", "publishedAt", "title", "versionLabel"}
		if got := sortedKeys(t, entry); !reflect.DeepEqual(got, wantEntry) {
			t.Errorf("entry keys = %v, want exactly %v", got, wantEntry)
		}
		for _, banned := range []string{"appId", "versionNumber"} {
			if _, present := entry[banned]; present {
				t.Errorf("entry emits key %q; the public feed never serves it (issue #259)", banned)
			}
		}

		linked, ok := entry["linkedRequests"].([]any)
		if !ok {
			t.Fatalf("linkedRequests = %#v, want an array (always present, even when empty)", entry["linkedRequests"])
		}
		if len(linked) != 1 {
			t.Fatalf("linkedRequests = %#v, want 1 element", linked)
		}
		linked0, ok := linked[0].(map[string]any)
		if !ok {
			t.Fatalf("linkedRequests[0] = %#v, want an object", linked[0])
		}
		wantLinked := []string{"id", "title"}
		if got := sortedKeys(t, linked0); !reflect.DeepEqual(got, wantLinked) {
			t.Errorf("linkedRequests[0] keys = %v, want %v", got, wantLinked)
		}
	})

	t.Run("legacy payload with removed keys decodes the corrected shape", func(t *testing.T) {
		legacy := `{
			"entries": [{
				"id": "cl_old_1",
				"appId": "app_internal_9",
				"title": "v0.9.0",
				"body": "Legacy entry",
				"versionNumber": 9,
				"versionLabel": null,
				"publishedAt": "2025-12-01T00:00:00.000Z",
				"linkedRequests": []
			}],
			"hasMore": false,
			"nextCursor": null
		}`
		server, _, _ := changelogServer(t, legacy)

		out, err := runRoot(t, server.URL, "apps", "public-changelog", "key_live_1", "--json")
		if err != nil {
			t.Fatalf("public-changelog --json on legacy payload: %v", err)
		}
		var top struct {
			Entries []map[string]any `json:"entries"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &top); err != nil {
			t.Fatalf("unmarshal structured output: %v", err)
		}
		if len(top.Entries) != 1 {
			t.Fatalf("entries = %#v, want 1", top.Entries)
		}
		entry := top.Entries[0]
		if got, present := entry["versionLabel"]; !present || got != nil {
			t.Errorf("versionLabel = %v (present=%t), want the nullable field passed through", got, present)
		}
		linked, present := entry["linkedRequests"]
		if !present {
			t.Fatalf("linkedRequests missing from output; the field is required even when empty (issue #259)")
		}
		if _, ok := linked.([]any); !ok {
			t.Errorf("linkedRequests = %#v, want an array even when empty (issue #259)", linked)
		}
		for _, banned := range []string{"appId", "versionNumber"} {
			if got, present := entry[banned]; present {
				t.Errorf("legacy key %q reached the output as %v; the corrected model must not emit it (issue #259)", banned, got)
			}
		}
	})
}
