package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// listWithUnassignedFixture mirrors the console feature-request listing
// envelope with the QUAL-03 unassignedTotal field: 3 requests on the board,
// 2 of them without a version assignment.
const listWithUnassignedFixture = `{
	"requests": [{
		"id": "fr_1",
		"appId": "app_a",
		"title": "Dark mode",
		"description": "Please",
		"status": "open",
		"voteCount": 3,
		"createdAt": "2026-09-01T12:00:00.000Z",
		"updatedAt": "2026-09-01T12:00:00.000Z"
	}],
	"total": 3,
	"unassignedTotal": 2
}`

// listWithoutUnassignedFixture keeps the payload shape the API served before
// QUAL-03 so the unassigned line's zero rendering stays pinned.
const listWithoutUnassignedFixture = `{
	"requests": [],
	"total": 3
}`

// unassignedListHandler serves the given fixture for the console listing.
func unassignedListHandler(t *testing.T, body string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/feature-requests") {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

// TestFeaturesListSurfacesUnassignedTotal covers the issue #79 human-output
// contract: the unassigned backlog count renders as a trailing parenthetical,
// always present (even at 0) so the output shape is stable for parsers.
func TestFeaturesListSurfacesUnassignedTotal(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	server := httptest.NewServer(unassignedListHandler(t, listWithUnassignedFixture))
	defer server.Close()

	out, err := runRoot(t, server.URL, "features", "list", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("features list: %v", err)
	}
	for _, want := range []string{"(1 shown, 3 total)", "(2 of 3 unassigned)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestFeaturesListUnassignedZeroStillRenders pins the always-on variant: a
// payload without unassignedTotal decodes to 0 and the line still renders,
// reading "0 of 3 unassigned".
func TestFeaturesListUnassignedZeroStillRenders(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	server := httptest.NewServer(unassignedListHandler(t, listWithoutUnassignedFixture))
	defer server.Close()

	out, err := runRoot(t, server.URL, "features", "list", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("features list: %v", err)
	}
	if !strings.Contains(out, "(0 of 3 unassigned)") {
		t.Errorf("output missing the zero unassigned line:\n%s", out)
	}
}

// TestFeaturesListJSONIncludesUnassignedTotal verifies machine-readable
// output keeps the raw field name from the API contract.
func TestFeaturesListJSONIncludesUnassignedTotal(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	server := httptest.NewServer(unassignedListHandler(t, listWithUnassignedFixture))
	defer server.Close()

	out, err := runRoot(t, server.URL, "features", "list", "--workspace", "ws_1", "--json")
	if err != nil {
		t.Fatalf("features list --json: %v", err)
	}
	for _, want := range []string{`"total": 3`, `"unassignedTotal": 2`} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON output missing %s:\n%s", want, out)
		}
	}
}

// TestFeaturesListSanitizesTruncatedEscapeSequence covers the truncate-then-
// sanitize order from issue #70: truncate() cuts the poisoned title inside
// the unterminated OSC 8 sequence, and sanitizing afterwards must still leave
// zero control bytes on stdout (the dangling ESC is stripped, not emitted).
func TestFeaturesListSanitizesTruncatedEscapeSequence(t *testing.T) {
	titleJSON, err := json.Marshal("\x1b]8;;https://evil.example/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\x1b\\Legit Title Text")
	if err != nil {
		t.Fatalf("marshal title: %v", err)
	}
	fixture := `{
		"requests": [{
			"id": "fr_evil1",
			"appId": "app_a",
			"title": ` + string(titleJSON) + `,
			"description": "Please",
			"status": "open",
			"voteCount": 3,
			"createdAt": "2026-09-01T12:00:00.000Z",
			"updatedAt": "2026-09-01T12:00:00.000Z"
		}],
		"total": 1,
		"unassignedTotal": 1
	}`
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	server := httptest.NewServer(unassignedListHandler(t, fixture))
	defer server.Close()

	out, err := runRoot(t, server.URL, "features", "list", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("features list: %v", err)
	}
	for i := 0; i < len(out); i++ {
		b := out[i]
		if (b < 0x20 && b != '\t' && b != '\n') || b == 0x7f {
			t.Fatalf("output contains control byte 0x%02x: %q", b, out)
		}
	}
	if !strings.Contains(out, "fr_evil1") || !strings.Contains(out, "(1 shown, 1 total)") {
		t.Errorf("row did not render:\n%s", out)
	}
}

// TestFeaturesListSendsSavedDefaultAppID covers the issue #187 list-side wire
// contract: with a saved default app, 'features list' carries appId=app_a —
// the same scope the ID-taking commands resolve under — while --all-apps (the
// workspace-wide escape hatch) and an app-less config send no appId filter.
func TestFeaturesListSendsSavedDefaultAppID(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	var gotAppID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAppID = r.URL.Query().Get("appId")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(filteredFixture(t, gotAppID))
	}))
	t.Cleanup(server.Close)

	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "features", "list"); err != nil {
		t.Fatalf("features list: %v", err)
	}
	if gotAppID != "app_a" {
		t.Errorf("appId filter = %q, want app_a (the saved default)", gotAppID)
	}

	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "features", "list", "--all-apps"); err != nil {
		t.Fatalf("features list --all-apps: %v", err)
	}
	if gotAppID != "" {
		t.Errorf("--all-apps appId filter = %q, want no filter (workspace-wide)", gotAppID)
	}

	if _, err := runRootWithSeededConfig(t, server.URL, `{"defaultWorkspace":"ws_1"}`, "features", "list"); err != nil {
		t.Fatalf("features list without default app: %v", err)
	}
	if gotAppID != "" {
		t.Errorf("app-less appId filter = %q, want no filter (workspace-wide fallback)", gotAppID)
	}
}

// TestFeaturesListAppFlagWinsOverSavedDefault keeps --app authoritative on
// the list side too, and pins that --all-apps refuses to combine with --app
// instead of silently picking a scope.
func TestFeaturesListAppFlagWinsOverSavedDefault(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	var gotAppID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAppID = r.URL.Query().Get("appId")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(filteredFixture(t, gotAppID))
	}))
	t.Cleanup(server.Close)

	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "features", "list", "--app", "app_b"); err != nil {
		t.Fatalf("features list --app app_b: %v", err)
	}
	if gotAppID != "app_b" {
		t.Errorf("appId filter = %q, want app_b (the --app flag)", gotAppID)
	}

	_, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "features", "list", "--all-apps", "--app", "app_b")
	if err == nil || !strings.Contains(err.Error(), "--all-apps") || !strings.Contains(err.Error(), "--app") {
		t.Fatalf("features list --all-apps --app error = %v, want a mutual-exclusion error", err)
	}
}

// TestFeaturesListToGetRoundTripUnderDefaultApp covers the issue #187 core
// regression: in the default configuration, every ID 'features list' shows
// resolves via 'features get' — the discover→act round trip the SKILL-documented
// triage workflow depends on. The listing is scoped to app_a (so app B's
// request does not appear) and the table carries an App column so a
// --all-apps view stays legible across apps.
func TestFeaturesListToGetRoundTripUnderDefaultApp(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	server := httptest.NewServer(requestsHandler(t, nil, nil))
	t.Cleanup(server.Close)

	out, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "features", "list")
	if err != nil {
		t.Fatalf("features list: %v", err)
	}
	if !strings.Contains(out, "App A request") {
		t.Errorf("output missing the app A row:\n%s", out)
	}
	if strings.Contains(out, "App B request") {
		t.Errorf("listing leaked app B's request under the app_a scope:\n%s", out)
	}

	// Round-trip every ID the listing shows through features get.
	ids := listedRequestIDs(t, out)
	if len(ids) != 1 || ids[0] != "fr_a_1" {
		t.Fatalf("listed IDs = %v, want [fr_a_1]", ids)
	}
	for _, id := range ids {
		if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "features", "get", id); err != nil {
			t.Errorf("features get %s (shown by features list): %v", id, err)
		}
	}

	// The cross-app escape hatch shows both apps, with the App column naming
	// each row's owner so the workspace-wide view stays legible.
	out, err = runRootWithSeededConfig(t, server.URL, defaultAppConfig, "features", "list", "--all-apps")
	if err != nil {
		t.Fatalf("features list --all-apps: %v", err)
	}
	for _, want := range []string{"app_a", "app_b", "App A request", "App B request"} {
		if !strings.Contains(out, want) {
			t.Errorf("--all-apps output missing %q:\n%s", want, out)
		}
	}
}

// listedRequestIDs extracts the request IDs from a 'features list' table:
// the first whitespace-separated field of every row whose ID column carries
// a request reference (skipping the header row and the parenthesized footer
// lines).
func listedRequestIDs(t *testing.T, out string) []string {
	t.Helper()
	var ids []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if strings.HasPrefix(fields[0], "fr_") {
			ids = append(ids, fields[0])
		}
	}
	return ids
}
