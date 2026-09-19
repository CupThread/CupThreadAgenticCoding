package cmd

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// publicRequestsFixture mirrors the wire shape of GET /api/v1/feature-requests
// (DATA-01): hasMore/nextCursor are always present, records are camelCase.
const publicRequestsFixture = `{
	"requests": [{
		"id": "fr_pub_1",
		"appId": "app_1",
		"title": "Dark mode",
		"description": "Please add a dark theme",
		"status": "open",
		"columnName": "Inbox",
		"columnSlug": "inbox",
		"versionLabel": "1.2.0",
		"voteCount": 12,
		"hasVoted": false,
		"commentCount": 3,
		"createdAt": "2026-09-01T12:00:00.000Z",
		"updatedAt": "2026-09-01T12:00:00.000Z"
	}],
	"total": 123,
	"hasMore": true,
	"nextCursor": "MjAyNi0wOS0wMXQxMjowMDowMC4wMDBafGZyX3B1Yl8x"
}`

// publicRequestsServer returns a server that replies with respBody and
// records the request path and query.
func publicRequestsServer(t *testing.T, respBody string) (*httptest.Server, *string, *url.Values) {
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

// TestAppsPublicFeatureRequests covers the public feed command: unauthenticated
// path shape, default paging params, table surfacing, and the next-cursor hint.
func TestAppsPublicFeatureRequests(t *testing.T) {
	server, gotPath, gotQuery := publicRequestsServer(t, publicRequestsFixture)

	out, err := runRoot(t, server.URL, "apps", "public-feature-requests", "app_key_1")
	if err != nil {
		t.Fatalf("public-feature-requests: %v", err)
	}
	if *gotPath != "/api/v1/feature-requests" {
		t.Errorf("request path = %s", *gotPath)
	}
	if got := gotQuery.Get("appKey"); got != "app_key_1" {
		t.Errorf("appKey = %q, want app_key_1", got)
	}
	if got := gotQuery.Get("limit"); got != "50" {
		t.Errorf("limit = %q, want the API default 50", got)
	}
	if got := gotQuery.Get("cursor"); got != "" {
		t.Errorf("cursor sent without the flag: %q", got)
	}
	if got := gotQuery.Get("offset"); got != "" {
		t.Errorf("offset sent without the flag: %q", got)
	}
	for _, want := range []string{"Dark mode", "open", "12", "More requests available", "--cursor MjAyNi0wOS0wMXQxMjowMDowMC4wMDBafGZyX3B1Yl8x", "123 total"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestAppsPublicFeatureRequestsForwardsCursor verifies --limit/--cursor map to
// the query parameters and opaque cursors (base64url with padding stripped)
// survive URL encoding.
func TestAppsPublicFeatureRequestsForwardsCursor(t *testing.T) {
	server, gotPath, gotQuery := publicRequestsServer(t, publicRequestsFixture)

	cursor := "MjAyNi0wOS0wMVQwMDo1MDozNS4xNzNafGYzZGZhMzdmLTNiZGQtNGI4NC04ZGVlLTVlNDI3MDgxZTQ1Ng"
	if _, err := runRoot(t, server.URL, "apps", "public-feature-requests", "app_key_1",
		"--limit", "100", "--cursor", cursor); err != nil {
		t.Fatalf("public-feature-requests: %v", err)
	}
	if *gotPath != "/api/v1/feature-requests" {
		t.Errorf("request path = %s", *gotPath)
	}
	if got := gotQuery.Get("limit"); got != "100" {
		t.Errorf("limit = %q, want 100", got)
	}
	if got := gotQuery.Get("cursor"); got != cursor {
		t.Errorf("cursor = %q, want %q (round-trip through query encoding)", got, cursor)
	}
}

// TestAppsPublicFeatureRequestsCursorSuppressesOffset pins the DATA-01 rule
// that a cursor takes the place of offset: when both flags are given the CLI
// does not send the offset at all.
func TestAppsPublicFeatureRequestsCursorSuppressesOffset(t *testing.T) {
	server, _, gotQuery := publicRequestsServer(t, publicRequestsFixture)

	if _, err := runRoot(t, server.URL, "apps", "public-feature-requests", "app_key_1",
		"--cursor", "abc123", "--offset", "999"); err != nil {
		t.Fatalf("public-feature-requests: %v", err)
	}
	if got := gotQuery.Get("cursor"); got != "abc123" {
		t.Errorf("cursor = %q, want abc123", got)
	}
	if got := gotQuery.Get("offset"); got != "" {
		t.Errorf("offset = %q, want it dropped next to --cursor", got)
	}
}

// TestAppsPublicFeatureRequestsLegacyOffset keeps offset paging working for
// callers that have not migrated to cursors.
func TestAppsPublicFeatureRequestsLegacyOffset(t *testing.T) {
	server, _, gotQuery := publicRequestsServer(t, publicRequestsFixture)

	if _, err := runRoot(t, server.URL, "apps", "public-feature-requests", "app_key_1",
		"--offset", "100"); err != nil {
		t.Fatalf("public-feature-requests: %v", err)
	}
	if got := gotQuery.Get("offset"); got != "100" {
		t.Errorf("offset = %q, want 100", got)
	}
	if got := gotQuery.Get("cursor"); got != "" {
		t.Errorf("cursor = %q, want none", got)
	}
}

// TestAppsPublicFeatureRequestsSearch forwards the q filter to the endpoint.
func TestAppsPublicFeatureRequestsSearch(t *testing.T) {
	server, _, gotQuery := publicRequestsServer(t, publicRequestsFixture)

	if _, err := runRoot(t, server.URL, "apps", "public-feature-requests", "app_key_1",
		"--q", "dark mode"); err != nil {
		t.Fatalf("public-feature-requests: %v", err)
	}
	if got := gotQuery.Get("q"); got != "dark mode" {
		t.Errorf("q = %q, want \"dark mode\"", got)
	}
}

// TestAppsPublicFeatureRequestsJSONKeepsContract verifies --json output keeps
// the raw hasMore/nextCursor contract field names.
func TestAppsPublicFeatureRequestsJSONKeepsContract(t *testing.T) {
	server, _, _ := publicRequestsServer(t, publicRequestsFixture)

	out, err := runRoot(t, server.URL, "apps", "public-feature-requests", "app_key_1", "--json")
	if err != nil {
		t.Fatalf("public-feature-requests --json: %v", err)
	}
	for _, want := range []string{
		`"total": 123`,
		`"hasMore": true`,
		`"nextCursor": "MjAyNi0wOS0wMXQxMjowMDowMC4wMDBafGZyX3B1Yl8x"`,
		`"voteCount": 12`,
		`"commentCount": 3`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON output missing %q:\n%s", want, out)
		}
	}
}

// TestAppsPublicFeatureRequestsLastPage checks the table stays quiet when the
// feed is exhausted (nextCursor null, hasMore false).
func TestAppsPublicFeatureRequestsLastPage(t *testing.T) {
	server, _, _ := publicRequestsServer(t, `{
		"requests": [{
			"id": "fr_pub_9",
			"title": "Linux build",
			"description": "Ship a Linux binary",
			"status": "shipped",
			"columnName": null,
			"versionLabel": null,
			"voteCount": 1,
			"hasVoted": false,
			"commentCount": 0,
			"createdAt": "2026-01-15T08:00:00.000Z",
			"updatedAt": "2026-01-15T08:00:00.000Z"
		}],
		"total": 1,
		"hasMore": false,
		"nextCursor": null
	}`)

	out, err := runRoot(t, server.URL, "apps", "public-feature-requests", "app_key_1")
	if err != nil {
		t.Fatalf("public-feature-requests: %v", err)
	}
	if strings.Contains(out, "More requests available") {
		t.Errorf("last page should not hint at a next cursor:\n%s", out)
	}
	if !strings.Contains(out, "Linux build") {
		t.Errorf("output missing request title:\n%s", out)
	}
}

// TestAppsPublicFeatureRequestsInvalidCursor covers the server's 400
// {"error": "Invalid cursor"} contract surfacing as a command error.
func TestAppsPublicFeatureRequestsInvalidCursor(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error": "Invalid cursor"}`))
	}))
	t.Cleanup(server.Close)

	_, err := runRoot(t, server.URL, "apps", "public-feature-requests", "app_key_1", "--cursor", "not-a-cursor")
	if err == nil || !strings.Contains(err.Error(), "Invalid cursor") || !strings.Contains(err.Error(), "HTTP 400") {
		t.Fatalf("error = %v, want the Invalid cursor 400 body", err)
	}
	if gotPath != "/api/v1/feature-requests" {
		t.Errorf("request path = %s", gotPath)
	}
}

// TestAppsPublicFeatureRequestsSignInRequired covers boards with anonymous
// roadmap view disabled: the unauthenticated feed 401s and the command adds
// the sign-in context.
func TestAppsPublicFeatureRequestsSignInRequired(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error": "Sign in is required to view this roadmap", "code": "authentication_required"}`))
	}))
	t.Cleanup(server.Close)

	_, err := runRoot(t, server.URL, "apps", "public-feature-requests", "app_key_1")
	if err == nil || !strings.Contains(err.Error(), "requires sign-in") {
		t.Fatalf("error = %v, want the sign-in-required hint", err)
	}
}

// TestAppsPublicFeatureRequestsRequiresAppKey verifies argument validation.
func TestAppsPublicFeatureRequestsRequiresAppKey(t *testing.T) {
	server, gotPath, _ := publicRequestsServer(t, publicRequestsFixture)

	_, err := runRoot(t, server.URL, "apps", "public-feature-requests")
	if err == nil || !strings.Contains(err.Error(), "accepts 1 arg") {
		t.Fatalf("error = %v, want missing-argument error", err)
	}
	if *gotPath != "" {
		t.Errorf("no request should be made, got %s", *gotPath)
	}
}
