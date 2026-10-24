package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
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
		"importedVotes": 8,
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
		`"importedVotes": 8`,
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

// TestAppsPublicFeatureRequestsContractPinNoInternalFields pins the issue #262
// sync of SaaS #1320 (PR #1323): GET /api/v1/feature-requests no longer serves
// the ten undocumented tenant-internal keys (importSource, importRef,
// githubIssueId, githubIssueNumber, githubIssueUrl, githubDiscussionId,
// githubDiscussionNumber, githubDiscussionUrl, githubLastSyncedAt,
// githubSyncStatus), while preserving the documented public FeatureRequest
// fields (including importedVotes, voteCount, hasVoted, and the column/version
// fields). The CLI's PublicFeatureRequest struct and `apps
// public-feature-requests --json` must emit only the documented public keys
// and never expose any of the ten removed tenant-internal keys.
func TestAppsPublicFeatureRequestsContractPinNoInternalFields(t *testing.T) {
	removedInternalKeys := []string{
		"importSource",
		"importRef",
		"githubIssueId",
		"githubIssueNumber",
		"githubIssueUrl",
		"githubDiscussionId",
		"githubDiscussionNumber",
		"githubDiscussionUrl",
		"githubLastSyncedAt",
		"githubSyncStatus",
	}

	sortedKeys := func(t *testing.T, m map[string]any) []string {
		t.Helper()
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys
	}

	t.Run("served public payload round-trips documented keys with no tenant-internal keys", func(t *testing.T) {
		server, _, _ := publicRequestsServer(t, publicRequestsFixture)

		out, err := runRoot(t, server.URL, "apps", "public-feature-requests", "app_key_1", "--json")
		if err != nil {
			t.Fatalf("public-feature-requests --json: %v", err)
		}
		var top map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &top); err != nil {
			t.Fatalf("unmarshal structured output: %v", err)
		}

		wantTop := []string{"hasMore", "nextCursor", "requests", "total"}
		if got := sortedKeys(t, top); !reflect.DeepEqual(got, wantTop) {
			t.Errorf("top-level keys = %v, want %v", got, wantTop)
		}

		reqs, ok := top["requests"].([]any)
		if !ok || len(reqs) != 1 {
			t.Fatalf("requests = %#v, want 1-element array", top["requests"])
		}
		req0, ok := reqs[0].(map[string]any)
		if !ok {
			t.Fatalf("requests[0] is %T, want object", reqs[0])
		}
		wantReq := []string{
			"columnName",
			"commentCount",
			"createdAt",
			"description",
			"hasVoted",
			"id",
			"importedVotes",
			"status",
			"title",
			"updatedAt",
			"versionLabel",
			"voteCount",
		}
		if got := sortedKeys(t, req0); !reflect.DeepEqual(got, wantReq) {
			t.Errorf("requests[0] keys = %v, want %v", got, wantReq)
		}
		for _, internalKey := range removedInternalKeys {
			if _, present := req0[internalKey]; present {
				t.Errorf("requests[0] unexpectedly contains removed tenant-internal key %q", internalKey)
			}
		}
	})

	t.Run("legacy or proxy payload carrying removed internal keys strips all ten on --json decode", func(t *testing.T) {
		legacyPayload := `{
			"requests": [{
				"id": "fr_pub_legacy",
				"appId": "app_1",
				"title": "Offline sync",
				"description": "Support offline cache",
				"status": "planned",
				"columnName": "Planned",
				"versionLabel": "2.0.0",
				"voteCount": 42,
				"importedVotes": 10,
				"hasVoted": true,
				"isOwnRequest": false,
				"commentCount": 5,
				"createdAt": "2026-09-01T12:00:00.000Z",
				"updatedAt": "2026-09-02T12:00:00.000Z",
				"importSource": null,
				"importRef": null,
				"githubIssueId": null,
				"githubIssueNumber": null,
				"githubIssueUrl": null,
				"githubDiscussionId": null,
				"githubDiscussionNumber": null,
				"githubDiscussionUrl": null,
				"githubLastSyncedAt": null,
				"githubSyncStatus": null
			}],
			"total": 1,
			"hasMore": false,
			"nextCursor": null
		}`
		server, _, _ := publicRequestsServer(t, legacyPayload)

		out, err := runRoot(t, server.URL, "apps", "public-feature-requests", "app_key_1", "--json")
		if err != nil {
			t.Fatalf("public-feature-requests --json: %v", err)
		}
		var top map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &top); err != nil {
			t.Fatalf("unmarshal structured output: %v", err)
		}
		reqs, ok := top["requests"].([]any)
		if !ok || len(reqs) != 1 {
			t.Fatalf("requests = %#v, want 1-element array", top["requests"])
		}
		req0, ok := reqs[0].(map[string]any)
		if !ok {
			t.Fatalf("requests[0] is %T, want object", reqs[0])
		}
		for _, internalKey := range removedInternalKeys {
			if _, present := req0[internalKey]; present {
				t.Errorf("requests[0] leaked removed tenant-internal key %q in --json output", internalKey)
			}
		}
		if got := req0["importedVotes"]; got != float64(10) {
			t.Errorf("importedVotes = %v, want 10 preserved", got)
		}
		if got := req0["voteCount"]; got != float64(42) {
			t.Errorf("voteCount = %v, want 42 preserved", got)
		}
		if got := req0["hasVoted"]; got != true {
			t.Errorf("hasVoted = %v, want true preserved", got)
		}
	})
}

// TestAppsPublicFeatureRequestsClampsLimit pins the local --limit clamp on
// the public board feed (issue #271, the #201 discipline): the DATA-01 route
// caps ?limit= at 200 and silently degrades 0/negative to the default 50, so
// the CLI clamps first — over-cap to 200, 0/negative to 1 — and announces the
// rewrite: stdout in table mode, stderr in --json mode, and nothing at all on
// the verbatim 200 boundary.
func TestAppsPublicFeatureRequestsClampsLimit(t *testing.T) {
	server, _, gotQuery := publicRequestsServer(t, publicRequestsFixture)

	for _, tc := range []struct{ requested, sent string }{
		{"500", "200"},
		{"201", "200"},
		{"0", "1"},
		{"-5", "1"},
	} {
		out, err := runRoot(t, server.URL, "apps", "public-feature-requests", "app_key_1",
			"--limit", tc.requested)
		if err != nil {
			t.Fatalf("public-feature-requests --limit %s: %v", tc.requested, err)
		}
		if got := gotQuery.Get("limit"); got != tc.sent {
			t.Errorf("--limit %s sent limit = %q, want %q", tc.requested, got, tc.sent)
		}
		if !strings.Contains(out, "⚠ --limit "+tc.requested+" is outside the server's 1-200 page range") {
			t.Errorf("--limit %s output missing clamp warning:\n%s", tc.requested, out)
		}
	}

	out, err := runRoot(t, server.URL, "apps", "public-feature-requests", "app_key_1", "--limit", "200")
	if err != nil {
		t.Fatalf("public-feature-requests --limit 200: %v", err)
	}
	if got := gotQuery.Get("limit"); got != "200" {
		t.Errorf("boundary --limit 200 sent limit = %q, want 200", got)
	}
	if strings.Contains(out, "⚠") {
		t.Errorf("boundary --limit 200 produced a warning:\n%s", out)
	}

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	stdout, stderr, err := runRootCapture(t, cfgPath, server.URL,
		"apps", "public-feature-requests", "app_key_1", "--limit", "500", "--json")
	if err != nil {
		t.Fatalf("public-feature-requests --json --limit 500: %v", err)
	}
	unmarshalOneJSON(t, stdout)
	assertNoTableOutput(t, stdout)
	if !strings.Contains(stderr, "⚠ --limit 500 is outside the server's 1-200 page range; requesting 200 instead") {
		t.Errorf("json stderr missing clamp warning:\n%s", stderr)
	}
}

// TestAppsPublicFeatureRequestsClampsOffset pins the local --offset clamp on
// the public board feed (issue #271, the #178 discipline): the DATA-01 route
// clamps ?offset= at the shared 10,000 cap and still answers 200, so the CLI
// rewrites an out-of-range value locally with a ⚠ notice; a negative one
// clamps to 0, which stays out of the query (the first page). In-range values
// pass through byte-identically with no notice, and a cursor still suppresses
// the offset entirely even when the rewrite fires.
func TestAppsPublicFeatureRequestsClampsOffset(t *testing.T) {
	server, _, gotQuery := publicRequestsServer(t, publicRequestsFixture)

	for _, tc := range []struct{ requested, sent string }{
		{"15000", "10000"},
		{"10001", "10000"},
		{"-5", ""}, // clamped to 0, the first page: not sent at all
	} {
		out, err := runRoot(t, server.URL, "apps", "public-feature-requests", "app_key_1",
			"--offset", tc.requested)
		if err != nil {
			t.Fatalf("public-feature-requests --offset %s: %v", tc.requested, err)
		}
		if got := gotQuery.Get("offset"); got != tc.sent {
			t.Errorf("--offset %s sent offset = %q, want %q", tc.requested, got, tc.sent)
		}
		if !strings.Contains(out, "⚠ --offset "+tc.requested+" is outside the server's 0-10000 page range") {
			t.Errorf("--offset %s output missing clamp warning:\n%s", tc.requested, out)
		}
	}

	for _, boundary := range []string{"10000", "100"} {
		out, err := runRoot(t, server.URL, "apps", "public-feature-requests", "app_key_1",
			"--offset", boundary)
		if err != nil {
			t.Fatalf("public-feature-requests --offset %s: %v", boundary, err)
		}
		if got := gotQuery.Get("offset"); got != boundary {
			t.Errorf("in-range --offset %s sent offset = %q, want %q", boundary, got, boundary)
		}
		if strings.Contains(out, "⚠") {
			t.Errorf("in-range --offset %s produced a warning:\n%s", boundary, out)
		}
	}

	if _, err := runRoot(t, server.URL, "apps", "public-feature-requests", "app_key_1",
		"--cursor", "abc123", "--offset", "15000"); err != nil {
		t.Fatalf("public-feature-requests --cursor --offset: %v", err)
	}
	if got := gotQuery.Get("offset"); got != "" {
		t.Errorf("clamped --offset sent next to --cursor = %q, want it dropped entirely", got)
	}

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	stdout, stderr, err := runRootCapture(t, cfgPath, server.URL,
		"apps", "public-feature-requests", "app_key_1", "--offset", "15000", "--json")
	if err != nil {
		t.Fatalf("public-feature-requests --json --offset 15000: %v", err)
	}
	unmarshalOneJSON(t, stdout)
	assertNoTableOutput(t, stdout)
	if !strings.Contains(stderr, "⚠ --offset 15000 is outside the server's 0-10000 page range; requesting 10000 instead") {
		t.Errorf("json stderr missing clamp warning:\n%s", stderr)
	}
}

// TestAppsPublicFeatureRequestsOffsetWalkSurfacesEveryRow is the regression
// canary for the row-skipping mode issue #271 fixes. The mock server clamps
// ?limit= to 200 the way the real DATA-01 route silently does, and the walk
// steps --offset by the page size the CLI's own ⚠ clamp notice discloses.
// Before the fix the notice was absent and limit=500 went out raw, so a
// walker trusting its requested limit stepped by 500 and never saw rows
// 200-499, 700-999, …; after it, every row of the 450-row fixture is
// surfaced exactly once, in feed order.
func TestAppsPublicFeatureRequestsOffsetWalkSurfacesEveryRow(t *testing.T) {
	const totalRows = 450
	rows := make([]string, totalRows)
	for i := range rows {
		rows[i] = fmt.Sprintf(`{"id": "fr_walk_%04d", "title": "Row %04d", "status": "open", "voteCount": %d, "commentCount": 0, "createdAt": "2026-01-01T00:00:00.000Z"}`, i, i, i)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		if limit > 200 {
			limit = 200 // the DATA-01 route's silent page cap
		}
		if limit < 1 {
			limit = 50 // the route default for a degraded 0
		}
		start := offset
		if start > totalRows {
			start = totalRows
		}
		end := start + limit
		if end > totalRows {
			end = totalRows
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(`{"requests": [%s], "total": %d, "hasMore": %t, "nextCursor": null}`,
			strings.Join(rows[start:end], ","), totalRows, end < totalRows)))
	}))
	t.Cleanup(server.Close)

	var seen []string
	pageSize := 0
	for offset := 0; offset < totalRows; offset += pageSize {
		cfgPath := filepath.Join(t.TempDir(), "config.json")
		stdout, stderr, err := runRootCapture(t, cfgPath, server.URL,
			"apps", "public-feature-requests", "app_key_1",
			"--limit", "500", "--offset", strconv.Itoa(offset), "--json")
		if err != nil {
			t.Fatalf("walk --offset %d: %v", offset, err)
		}
		if pageSize == 0 {
			// The walker trusts the CLI's clamp notice, not its own requested
			// limit, to learn the page size the server actually serves.
			m := regexp.MustCompile(`requesting (\d+) instead`).FindStringSubmatch(stderr)
			if m == nil {
				t.Fatalf("walk --offset %d: stderr missing the --limit clamp notice:\n%s", offset, stderr)
			}
			if pageSize, err = strconv.Atoi(m[1]); err != nil || pageSize != 200 {
				t.Fatalf("clamp notice reports page size %q, want 200:\n%s", m[1], stderr)
			}
		}
		var page struct {
			Requests []struct {
				ID string `json:"id"`
			} `json:"requests"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &page); err != nil {
			t.Fatalf("walk --offset %d: unmarshal structured output: %v\n%s", offset, err, stdout)
		}
		for _, r := range page.Requests {
			seen = append(seen, r.ID)
		}
	}

	if len(seen) != totalRows {
		t.Fatalf("walk surfaced %d rows, want %d", len(seen), totalRows)
	}
	for i, id := range seen {
		if want := fmt.Sprintf("fr_walk_%04d", i); id != want {
			t.Fatalf("row %d = %q, want %q — the walk skipped or reordered rows", i, id, want)
		}
	}
}
