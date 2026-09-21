package cmd

import (
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
