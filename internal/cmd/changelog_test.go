package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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
// is exhausted (nextCursor null, hasMore false).
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
