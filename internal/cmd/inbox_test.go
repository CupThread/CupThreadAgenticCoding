package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// inboxSubmissionFixture is a SubmissionRecord exercising the triage fields
// from issue #19 (triageStatus, assignedTo, firstTriagedAt, resolvedAt). It
// also keeps the delivery status distinct ("received") to guard the
// delivery-vs-triage separation.
const inboxSubmissionFixture = `{
	"submissionId": "sub_1111111111abcdef",
	"appId": "app_1",
	"appKey": "key_live_1",
	"workspaceSlug": "acme",
	"title": "Crash on launch",
	"description": "The app crashes on launch.",
	"reporterName": "Ada",
	"reporterEmail": "ada@example.com",
	"platform": "ios",
	"appVersion": "1.2.3",
	"buildNumber": "42",
	"priority": "!!",
	"metadataJson": "{}",
	"status": "received",
	"triageStatus": "in_progress",
	"assignedTo": "user_abcdef123456",
	"firstTriagedAt": "2026-09-16T10:00:00.000Z",
	"resolvedAt": null,
	"githubDiscussionId": null,
	"githubDiscussionUrl": null,
	"githubError": null,
	"createdAt": "2026-09-15T08:00:00.000Z",
	"updatedAt": "2026-09-16T10:00:00.000Z"
}`

// inboxDetailFixture mirrors SubmissionDetailResponseSchema, including the
// activity log.
const inboxDetailFixture = `{
	"submission": ` + inboxSubmissionFixture + `,
	"app": {"appId": "app_1", "name": "Acme iOS", "slug": "ios"},
	"metadata": [{"key": "locale", "value": "en-US", "redacted": false, "truncated": false}],
	"attachments": [{
		"attachmentId": "att_1",
		"kind": "image",
		"filename": "crash.png",
		"mimeType": "image/png",
		"sizeBytes": 2048,
		"createdAt": "2026-09-15T08:00:00.000Z"
	}],
	"deliveryAttempts": [{
		"attemptId": "att_d1",
		"submissionId": "sub_1111111111abcdef",
		"attemptedAt": "2026-09-15T08:05:00.000Z",
		"status": "failed",
		"githubDiscussionId": null,
		"githubDiscussionUrl": null,
		"errorMessage": "github unreachable"
	}],
	"activity": [
		{
			"id": "evt_2",
			"submissionId": "sub_1111111111abcdef",
			"workspaceId": "ws_1",
			"actorId": "user_abcdef123456",
			"actorType": "user",
			"kind": "triage_status_changed",
			"payload": {"from": "open", "to": "in_progress"},
			"createdAt": "2026-09-16T10:00:00.000Z"
		},
		{
			"id": "evt_1",
			"submissionId": "sub_1111111111abcdef",
			"workspaceId": "ws_1",
			"actorId": "user_abcdef123456",
			"actorType": "user",
			"kind": "assigned",
			"payload": {"to": "user_abcdef123456"},
			"createdAt": "2026-09-16T09:00:00.000Z"
		}
	]
}`

// capturedRequest records what the CLI actually sent.
type capturedRequest struct {
	Method string
	Path   string
	Query  url.Values
	Body   string
}

// inboxServer responds with status/respBody and captures the single request.
func inboxServer(t *testing.T, status int, respBody string, captured *capturedRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		captured.Method = r.Method
		captured.Path = r.URL.Path
		captured.Query = r.URL.Query()
		captured.Body = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(respBody))
	}))
}

// TestInboxListDecodesTriageFields covers issue #19: the list table must show
// the triage lifecycle next to the (unchanged) delivery status, and decode
// assignee + triage timestamps in --json output.
func TestInboxListDecodesTriageFields(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var captured capturedRequest
	server := inboxServer(t, http.StatusOK,
		`{"submissions": [`+inboxSubmissionFixture+`], "total": 1}`, &captured)
	defer server.Close()

	out, err := runRoot(t, server.URL, "inbox", "list", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("inbox list: %v", err)
	}
	for _, want := range []string{"Triage", "Assignee", "in_progress", "user_abcdef", "received"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
	if got := captured.Query.Get("triage_status"); got != "" {
		t.Errorf("triage_status sent without the flag: %q", got)
	}
	if got := captured.Query.Get("limit"); got != "50" {
		t.Errorf("limit = %q, want 50", got)
	}

	out, err = runRoot(t, server.URL, "inbox", "list", "--workspace", "ws_1", "--json")
	if err != nil {
		t.Fatalf("inbox list --json: %v", err)
	}
	for _, want := range []string{
		`"triageStatus": "in_progress"`,
		`"assignedTo": "user_abcdef123456"`,
		`"firstTriagedAt": "2026-09-16T10:00:00.000Z"`,
		`"resolvedAt": null`,
		`"status": "received"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON output missing %q:\n%s", want, out)
		}
	}
}

// TestInboxListForwardsTriageFilters verifies the new list filters map to the
// API's query parameters (triage_status, assigned_to, priority, app_id, q).
func TestInboxListForwardsTriageFilters(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var captured capturedRequest
	server := inboxServer(t, http.StatusOK, `{"submissions": [], "total": 0}`, &captured)
	defer server.Close()

	if _, err := runRoot(t, server.URL, "inbox", "list", "--workspace", "ws_1",
		"--triage-status", "active",
		"--assigned-to", "unassigned",
		"--priority", "!!",
		"--app-id", "app_1",
		"--q", "crash"); err != nil {
		t.Fatalf("inbox list with filters: %v", err)
	}
	want := map[string]string{
		"triage_status": "active",
		"assigned_to":   "unassigned",
		"priority":      "!!",
		"app_id":        "app_1",
		"q":             "crash",
	}
	for param, value := range want {
		if got := captured.Query.Get(param); got != value {
			t.Errorf("query %s = %q, want %q", param, got, value)
		}
	}
}

// TestInboxListRejectsInvalidTriageFilter keeps typos from silently disabling
// the filter (the API ignores unknown triage_status values).
func TestInboxListRejectsInvalidTriageFilter(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	server := inboxServer(t, http.StatusOK, `{"submissions": [], "total": 0}`,
		&capturedRequest{Method: "GET"})
	defer server.Close()

	if _, err := runRoot(t, server.URL, "inbox", "list", "--workspace", "ws_1",
		"--triage-status", "bogus"); err == nil || !strings.Contains(err.Error(), "invalid --triage-status") {
		t.Fatalf("error = %v, want invalid --triage-status message", err)
	}
	if _, err := runRoot(t, server.URL, "inbox", "list", "--workspace", "ws_1",
		"--priority", "!!!!"); err == nil || !strings.Contains(err.Error(), "invalid --priority") {
		t.Fatalf("error = %v, want invalid --priority message", err)
	}
}

// TestInboxGetRendersDetailAndActivity covers the triage detail endpoint:
// field rendering plus the workspace activity log.
func TestInboxGetRendersDetailAndActivity(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var captured capturedRequest
	server := inboxServer(t, http.StatusOK, inboxDetailFixture, &captured)
	defer server.Close()

	out, err := runRoot(t, server.URL, "inbox", "get", "sub_1111111111abcdef", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("inbox get: %v", err)
	}
	if captured.Method != "GET" {
		t.Errorf("method = %s, want GET", captured.Method)
	}
	if captured.Path != "/api/v1/console/workspaces/ws_1/submissions/sub_1111111111abcdef" {
		t.Errorf("path = %s", captured.Path)
	}
	for _, want := range []string{
		"Triage status", "in_progress",
		"Assignee", "user_abcdef123456",
		"Delivery status", "received",
		"Acme iOS (ios)",
		"Activity (2)",
		"triage_status_changed",
		"assigned",
		"crash.png",
		"github unreachable",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestInboxGetJSONKeepsRawContract verifies agents get the raw triage fields
// and activity payload through --json.
func TestInboxGetJSONKeepsRawContract(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	server := inboxServer(t, http.StatusOK, inboxDetailFixture, &capturedRequest{})
	defer server.Close()

	out, err := runRoot(t, server.URL, "inbox", "get", "sub_1", "--workspace", "ws_1", "--json")
	if err != nil {
		t.Fatalf("inbox get --json: %v", err)
	}
	for _, want := range []string{
		`"triageStatus": "in_progress"`,
		`"assignedTo": "user_abcdef123456"`,
		`"kind": "triage_status_changed"`,
		`"from": "open"`,
		`"to": "in_progress"`,
		`"deliveryAttempts"`,
		`"metadata"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON output missing %q:\n%s", want, out)
		}
	}
}

// TestInboxGetMissingSubSurfacesAPIError checks the API's 404 message reaches
// the user (foreign submissions are indistinguishable from missing ones).
func TestInboxGetMissingSubSurfacesAPIError(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	server := inboxServer(t, http.StatusNotFound,
		`{"error": "Submission not found in this workspace"}`, &capturedRequest{})
	defer server.Close()

	_, err := runRoot(t, server.URL, "inbox", "get", "sub_missing", "--workspace", "ws_1")
	if err == nil || !strings.Contains(err.Error(), "Submission not found in this workspace") {
		t.Fatalf("error = %v, want API 404 message", err)
	}
}

// TestInboxTriageSetsStatus covers PATCH .../triage with client-side enum
// validation. Triage status is written only through this command — never via
// a delivery-status flag.
func TestInboxTriageSetsStatus(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var captured capturedRequest
	server := inboxServer(t, http.StatusOK, inboxSubmissionFixture, &captured)
	defer server.Close()

	out, err := runRoot(t, server.URL, "inbox", "triage", "sub_1", "in_progress", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("inbox triage: %v", err)
	}
	if captured.Method != "PATCH" {
		t.Errorf("method = %s, want PATCH", captured.Method)
	}
	if captured.Path != "/api/v1/console/workspaces/ws_1/submissions/sub_1/triage" {
		t.Errorf("path = %s", captured.Path)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(captured.Body), &body); err != nil {
		t.Fatalf("decode request body %q: %v", captured.Body, err)
	}
	if body["triageStatus"] != "in_progress" {
		t.Errorf("triageStatus = %v, want in_progress", body["triageStatus"])
	}
	if !strings.Contains(out, "✓ Triage status of sub_1 set to in_progress") {
		t.Errorf("output missing confirmation:\n%s", out)
	}

	// Invalid statuses are rejected before any request is made.
	server2 := inboxServer(t, http.StatusOK, inboxSubmissionFixture, &capturedRequest{})
	defer server2.Close()
	if _, err := runRoot(t, server2.URL, "inbox", "triage", "sub_1", "done", "--workspace", "ws_1"); err == nil ||
		!strings.Contains(err.Error(), "invalid triage status") {
		t.Fatalf("error = %v, want invalid triage status message", err)
	}
}

// TestInboxAssignAndUnassign covers PUT .../assign with and without a user ID.
func TestInboxAssignAndUnassign(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var captured capturedRequest
	server := inboxServer(t, http.StatusOK, inboxSubmissionFixture, &captured)
	defer server.Close()

	out, err := runRoot(t, server.URL, "inbox", "assign", "sub_1", "user_1", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("inbox assign: %v", err)
	}
	if captured.Method != "PUT" {
		t.Errorf("method = %s, want PUT", captured.Method)
	}
	if captured.Path != "/api/v1/console/workspaces/ws_1/submissions/sub_1/assign" {
		t.Errorf("path = %s", captured.Path)
	}
	if captured.Body != `{"assignedTo":"user_1"}` {
		t.Errorf("body = %s, want assignedTo user_1", captured.Body)
	}
	if !strings.Contains(out, "✓ Submission sub_1 assigned to user_1") {
		t.Errorf("output missing confirmation:\n%s", out)
	}

	// No user ID means unassign: the API must receive an explicit null.
	out, err = runRoot(t, server.URL, "inbox", "assign", "sub_1", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("inbox assign (unassign): %v", err)
	}
	if captured.Body != `{"assignedTo":null}` {
		t.Errorf("body = %s, want assignedTo null", captured.Body)
	}
	if !strings.Contains(out, "✓ Submission sub_1 unassigned") {
		t.Errorf("output missing unassign confirmation:\n%s", out)
	}
}

// TestInboxBulkTriage covers POST .../bulk-triage: body shape, response
// rendering, and the client-side validations (1-50 IDs, at least one of
// triageStatus/assignedTo, assignee vs unassign). The server answers the
// detail GETs of the up-front ID resolution (issue #418) by echoing the
// requested id back, so the resolved body is assertable.
func TestInboxBulkTriage(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var captured capturedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		captured.Method = r.Method
		captured.Path = r.URL.Path
		captured.Query = r.URL.Query()
		captured.Body = string(body)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			seg := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			_, _ = w.Write([]byte(`{"submission": {"submissionId": "` + seg + `", "title": "T"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"updatedCount": 1, "updatedIds": ["sub_1"]}`))
	}))
	defer server.Close()

	out, err := runRoot(t, server.URL, "inbox", "bulk-triage", "sub_1", "sub_2",
		"--triage-status", "resolved", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("inbox bulk-triage: %v", err)
	}
	if captured.Method != "POST" {
		t.Errorf("method = %s, want POST", captured.Method)
	}
	if captured.Path != "/api/v1/console/workspaces/ws_1/submissions/bulk-triage" {
		t.Errorf("path = %s", captured.Path)
	}
	var body struct {
		SubmissionIds []string `json:"submissionIds"`
		TriageStatus  string   `json:"triageStatus"`
		AssignedTo    *string  `json:"assignedTo"`
	}
	if err := json.Unmarshal([]byte(captured.Body), &body); err != nil {
		t.Fatalf("decode request body %q: %v", captured.Body, err)
	}
	if len(body.SubmissionIds) != 2 || body.SubmissionIds[0] != "sub_1" || body.SubmissionIds[1] != "sub_2" {
		t.Errorf("submissionIds = %v", body.SubmissionIds)
	}
	if body.TriageStatus != "resolved" || body.AssignedTo != nil {
		t.Errorf("triageStatus = %q, assignedTo = %v", body.TriageStatus, body.AssignedTo)
	}
	if !strings.Contains(out, "✓ Updated 1 submission(s)") || !strings.Contains(out, "sub_1") {
		t.Errorf("output missing bulk result:\n%s", out)
	}

	// --assignee carries the Clerk user ID; --unassign sends an explicit null.
	if _, err := runRoot(t, server.URL, "inbox", "bulk-triage", "sub_1",
		"--assignee", "user_9", "--workspace", "ws_1"); err != nil {
		t.Fatalf("bulk-triage --assignee: %v", err)
	}
	if !strings.Contains(captured.Body, `"assignedTo":"user_9"`) {
		t.Errorf("body = %s, want assignedTo user_9", captured.Body)
	}
	if _, err := runRoot(t, server.URL, "inbox", "bulk-triage", "sub_1",
		"--unassign", "--workspace", "ws_1"); err != nil {
		t.Fatalf("bulk-triage --unassign: %v", err)
	}
	if !strings.Contains(captured.Body, `"assignedTo":null`) {
		t.Errorf("body = %s, want assignedTo null", captured.Body)
	}

	// Validation errors never reach the API.
	validationCases := []struct {
		name string
		args []string
		want string
	}{
		{"no ids", []string{"inbox", "bulk-triage", "--triage-status", "open", "--workspace", "ws_1"}, "at least 1 arg"},
		{"no changes", []string{"inbox", "bulk-triage", "sub_1", "--workspace", "ws_1"}, "nothing to do"},
		{"invalid status", []string{"inbox", "bulk-triage", "sub_1", "--triage-status", "done", "--workspace", "ws_1"}, "invalid --triage-status"},
		{"assignee and unassign", []string{"inbox", "bulk-triage", "sub_1", "--assignee", "user_9", "--unassign", "--workspace", "ws_1"}, "mutually exclusive"},
	}
	emptyServer := inboxServer(t, http.StatusOK, `{"updatedCount": 0, "updatedIds": []}`,
		&capturedRequest{})
	defer emptyServer.Close()
	for _, tc := range validationCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := runRoot(t, emptyServer.URL, tc.args...); err == nil ||
				!strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}

	// More than 50 IDs is rejected up front.
	ids := make([]string, 51)
	for i := range ids {
		ids[i] = "sub_" + strconv.Itoa(i)
	}
	args := append([]string{"inbox", "bulk-triage", "--triage-status", "open", "--workspace", "ws_1"}, ids...)
	if _, err := runRoot(t, emptyServer.URL, args...); err == nil ||
		!strings.Contains(err.Error(), "at most 50") {
		t.Fatalf("error = %v, want at-most-50 message", err)
	}
}

// TestInboxBulkTriageRejectsBlankRefsBeforeAnyRequest pins the multi-reference
// extension of the #293 blank-argument contract (issue #427): bulk-triage is
// the one command taking several positional ids, so a blank one — the unset-
// shell-variable accident — must fail in the Args layer with an error naming
// the blank argument's 1-based position, before RunE resolves anything. The
// zero request count is the point: the resolution loop interpolates each ref
// into the detail-GET path, so a blank ref would fire
// GET .../submissions/ (a trailing-slash collection probe that only the
// server's strict routing answers 404 — a slash-normalizing proxy would
// decode it as a zero-value record and let the blank id into the bulk body).
func TestInboxBulkTriageRejectsBlankRefsBeforeAnyRequest(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			seg := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			_, _ = w.Write([]byte(`{"submission": {"submissionId": "` + seg + `", "title": "T"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"updatedCount": 1, "updatedIds": ["sub_1"]}`))
	}))
	defer server.Close()

	// --triage-status is passed so that a failed guard would run the real
	// resolution path: only the Args-layer refusal can explain success-free,
	// request-free outcomes.
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"blank only", []string{""}, "argument 1 is empty"},
		{"whitespace only", []string{"   "}, "argument 1 is empty"},
		{"blank first of three", []string{"", "sub_1", "sub_2"}, "argument 1 is empty"},
		{"blank last of two", []string{"sub_1", ""}, "argument 2 is empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hits = 0
			full := append([]string{"inbox", "bulk-triage", "--triage-status", "open", "--workspace", "ws_1"}, tc.args...)
			_, err := runRoot(t, server.URL, full...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), "pass a non-blank submission id") {
				t.Errorf("error = %v, want the self-diagnosing non-blank hint", err)
			}
			if hits != 0 {
				t.Errorf("server saw %d request(s), want 0 (refused before any round trip)", hits)
			}
		})
	}
}

// TestInboxListClampsLimit pins the local --limit clamp on the submissions
// listing (issue #201): the server clamps a parsed page size to its
// parseListPagination maxLimit (200) silently, so an offset walk stepped by a
// larger requested limit would skip rows without any hint. The CLI clamps
// first — over-cap to 200, 0/negative to 1 (matching features list) — and
// announces the rewrite through warnf: table mode keeps it on stdout, --json
// mode moves it to stderr so stdout stays a single document. The 200 boundary
// passes through verbatim with no warning.
func TestInboxListClampsLimit(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var captured capturedRequest
	server := inboxServer(t, http.StatusOK, `{"submissions": [], "total": 0}`, &captured)
	defer server.Close()

	for _, tc := range []struct{ requested, sent string }{
		{"500", "200"},
		{"0", "1"},
		{"-5", "1"},
	} {
		out, err := runRoot(t, server.URL, "inbox", "list", "--workspace", "ws_1", "--limit", tc.requested)
		if err != nil {
			t.Fatalf("inbox list --limit %s: %v", tc.requested, err)
		}
		if got := captured.Query.Get("limit"); got != tc.sent {
			t.Errorf("--limit %s sent limit = %q, want %q", tc.requested, got, tc.sent)
		}
		if !strings.Contains(out, "⚠ --limit "+tc.requested+" is outside the server's 1-200 page range") {
			t.Errorf("--limit %s output missing clamp warning:\n%s", tc.requested, out)
		}
	}

	out, err := runRoot(t, server.URL, "inbox", "list", "--workspace", "ws_1", "--limit", "200")
	if err != nil {
		t.Fatalf("inbox list --limit 200: %v", err)
	}
	if got := captured.Query.Get("limit"); got != "200" {
		t.Errorf("boundary --limit 200 sent limit = %q, want 200", got)
	}
	if strings.Contains(out, "⚠") {
		t.Errorf("boundary --limit 200 produced a warning:\n%s", out)
	}

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	stdout, stderr, err := runRootCapture(t, cfgPath, server.URL,
		"inbox", "list", "--workspace", "ws_1", "--limit", "500", "--json")
	if err != nil {
		t.Fatalf("inbox list --json --limit 500: %v", err)
	}
	unmarshalOneJSON(t, stdout)
	assertNoTableOutput(t, stdout)
	if !strings.Contains(stderr, "⚠ --limit 500 is outside the server's 1-200 page range; requesting 200 instead") {
		t.Errorf("json stderr missing clamp warning:\n%s", stderr)
	}
}

// TestInboxListClampsOffset pins the local --offset clamp on the submissions
// listing (issue #178): the server silently degrades an offset above the
// shared console cap of 10,000 to the cap (still answering 200) and a
// negative one to 0, so a walk past the cap re-serves the same final page
// with no hint anything changed. The CLI clamps first and announces the
// rewrite through warnf: table mode keeps it on stdout, --json mode moves it
// to stderr so stdout stays a single document. The 10,000 boundary passes
// through verbatim with no warning.
func TestInboxListClampsOffset(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var captured capturedRequest
	server := inboxServer(t, http.StatusOK, `{"submissions": [], "total": 0}`, &captured)
	defer server.Close()

	for _, tc := range []struct{ requested, sent string }{
		{"15000", "10000"},
		{"10001", "10000"},
		{"-5", "0"},
	} {
		out, err := runRoot(t, server.URL, "inbox", "list", "--workspace", "ws_1", "--offset", tc.requested)
		if err != nil {
			t.Fatalf("inbox list --offset %s: %v", tc.requested, err)
		}
		if got := captured.Query.Get("offset"); got != tc.sent {
			t.Errorf("--offset %s sent offset = %q, want %q", tc.requested, got, tc.sent)
		}
		if !strings.Contains(out, "⚠ --offset "+tc.requested+" is outside the server's 0-10000 page range") {
			t.Errorf("--offset %s output missing clamp warning:\n%s", tc.requested, out)
		}
	}

	out, err := runRoot(t, server.URL, "inbox", "list", "--workspace", "ws_1", "--offset", "10000")
	if err != nil {
		t.Fatalf("inbox list --offset 10000: %v", err)
	}
	if got := captured.Query.Get("offset"); got != "10000" {
		t.Errorf("boundary --offset 10000 sent offset = %q, want 10000", got)
	}
	if strings.Contains(out, "⚠") {
		t.Errorf("boundary --offset 10000 produced a warning:\n%s", out)
	}

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	stdout, stderr, err := runRootCapture(t, cfgPath, server.URL,
		"inbox", "list", "--workspace", "ws_1", "--offset", "15000", "--json")
	if err != nil {
		t.Fatalf("inbox list --json --offset 15000: %v", err)
	}
	unmarshalOneJSON(t, stdout)
	assertNoTableOutput(t, stdout)
	if !strings.Contains(stderr, "⚠ --offset 15000 is outside the server's 0-10000 page range; requesting 10000 instead") {
		t.Errorf("json stderr missing clamp warning:\n%s", stderr)
	}
}

// deliveryJobsArray renders n minimal delivery jobs as a JSON array body
// (without brackets), half of them failed so the truncation advisory's
// "including failed or dead ones" stakes are visible in the rows under test.
func deliveryJobsArray(n int) string {
	jobs := make([]string, 0, n)
	for i := 0; i < n; i++ {
		status := "succeeded"
		lastError := "null"
		if i%2 == 1 {
			status = "failed"
			lastError = `"github 500"`
		}
		jobs = append(jobs, fmt.Sprintf(
			`{"id":"job_%03d","submissionId":"sub_%03d000000000000","workspaceId":"ws_1","status":%q,"attempts":3,"maxAttempts":3,"nextAttemptAt":"","lastError":%s,"createdAt":"2026-10-%02dT00:00:00.000Z","updatedAt":"2026-10-%02dT00:00:00.000Z"}`,
			i, i, status, lastError, (i%28)+1, (i%28)+1))
	}
	return strings.Join(jobs, ",")
}

// deliveryJobsFixture wraps deliveryJobsArray as today's route answer: a
// {"jobs": [...]} document with no total (issue #352).
func deliveryJobsFixture(n int) string {
	return `{"jobs":[` + deliveryJobsArray(n) + `]}`
}

// TestInboxDeliveriesDisclosesServerCap pins the issue #352 client-side half:
// the route hardcodes a newest-50 window with no total and no paging, so a
// cap-sized answer must say so instead of reading as a complete queue. A
// 50-job response renders the advisory after the table; a 49-job response
// stays advisory-free (stable output shape).
func TestInboxDeliveriesDisclosesServerCap(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var captured capturedRequest
	server := inboxServer(t, http.StatusOK, deliveryJobsFixture(deliveryJobsPageSize), &captured)
	defer server.Close()

	out, err := runRoot(t, server.URL, "inbox", "deliveries", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("inbox deliveries: %v", err)
	}
	if !strings.Contains(out, "job_000") || !strings.Contains(out, "failed") {
		t.Errorf("table output missing job rows:\n%s", out)
	}
	if !strings.Contains(out, "⚠ Delivery queue may be truncated: this listing serves only the newest 50 jobs (server cap, no paging or total); older jobs — including failed or dead ones — are not shown") {
		t.Errorf("cap-sized output missing truncation advisory:\n%s", out)
	}
	if strings.Contains(out, "shown,") {
		t.Errorf("no-total response must not print the shown/total footer:\n%s", out)
	}

	server2 := inboxServer(t, http.StatusOK, deliveryJobsFixture(deliveryJobsPageSize-1), &captured)
	defer server2.Close()
	out, err = runRoot(t, server2.URL, "inbox", "deliveries", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("inbox deliveries (49 jobs): %v", err)
	}
	if strings.Contains(out, "⚠") {
		t.Errorf("sub-cap output produced an advisory:\n%s", out)
	}
}

// TestInboxDeliveriesJSONNoticeOnStderr pins the structured half of #352:
// stdout stays a single byte-faithful jobs document (no total key, no human
// markers) and the advisory moves to stderr.
func TestInboxDeliveriesJSONNoticeOnStderr(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	server := inboxServer(t, http.StatusOK, deliveryJobsFixture(deliveryJobsPageSize), &capturedRequest{})
	defer server.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	stdout, stderr, err := runRootCapture(t, cfgPath, server.URL,
		"inbox", "deliveries", "--workspace", "ws_1", "--json")
	if err != nil {
		t.Fatalf("inbox deliveries --json: %v", err)
	}
	assertNoTableOutput(t, stdout)
	doc := unmarshalOneJSON(t, stdout)
	jobs, ok := doc["jobs"].([]any)
	if !ok || len(jobs) != deliveryJobsPageSize {
		t.Fatalf("stdout jobs = %T len %v, want %d entries", doc["jobs"], len(jobs), deliveryJobsPageSize)
	}
	if _, present := doc["total"]; present {
		t.Errorf("structured stdout grew a total key the server never sent:\n%s", stdout)
	}
	if !strings.Contains(stderr, "⚠ Delivery queue may be truncated: this listing serves only the newest 50 jobs") {
		t.Errorf("json stderr missing truncation advisory:\n%s", stderr)
	}
}

// TestInboxDeliveriesTotalFooterAndNotice pins the forward-compatible half of
// #352: once the SaaS route answers total, the CLI prints the sibling
// (N shown, M total) footer and keys the notice off total > shown — present
// only when rows were actually cut.
func TestInboxDeliveriesTotalFooterAndNotice(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var captured capturedRequest
	server := inboxServer(t, http.StatusOK, `{"jobs":[`+deliveryJobsArray(2)+`], "total": 57}`, &captured)
	defer server.Close()

	out, err := runRoot(t, server.URL, "inbox", "deliveries", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("inbox deliveries (total 57): %v", err)
	}
	if !strings.Contains(out, "(2 shown, 57 total)") {
		t.Errorf("output missing sibling footer:\n%s", out)
	}
	if !strings.Contains(out, "⚠ Delivery queue is truncated: showing the 2 newest of 57 jobs; older jobs (including failed or dead ones) are not retrievable through this listing") {
		t.Errorf("output missing truncation notice:\n%s", out)
	}

	server2 := inboxServer(t, http.StatusOK, `{"jobs":[`+deliveryJobsArray(2)+`], "total": 2}`, &captured)
	defer server2.Close()
	out, err = runRoot(t, server2.URL, "inbox", "deliveries", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("inbox deliveries (total 2): %v", err)
	}
	if !strings.Contains(out, "(2 shown, 2 total)") {
		t.Errorf("output missing sibling footer:\n%s", out)
	}
	if strings.Contains(out, "⚠") {
		t.Errorf("complete queue produced a truncation notice:\n%s", out)
	}
}

// ─── inbox ID resolution (issue #418) ────────────────────────────────────────

// truncatedSubmissionFullID mirrors the issue #418 table shape: the inbox
// list and deliveries tables print the first 12 characters of this id, and
// that truncated form is what an agent copies into the triage commands.
const (
	truncatedSubmissionFullID = "sub_1abcdef1234fffffffffffffffff"
	truncatedSubmissionPrefix = "sub_1abcdef1234"
)

// submissionJSON is one full-shaped record with the given id.
func submissionJSON(id string) string {
	return `{"submissionId":"` + id + `","title":"Crash on launch","platform":"ios","priority":"!!","status":"received","triageStatus":"open","createdAt":"2026-10-01T00:00:00Z","updatedAt":"2026-10-01T00:00:00Z"}`
}

// inboxResolutionServer routes the resolution protocol (issue #418): a detail
// GET answers a SubmissionDetail envelope only for a known full id and 404s
// otherwise (the server matches exact ids only), the listing walk serves
// pages keyed by offset, and every other request — the mutations — answers
// 200 with a submission record while being recorded in *requests as
// "METHOD path?query".
func inboxResolutionServer(t *testing.T, pages map[string]string, knownIDs []string, requests *[]string, bodies *[]string) *httptest.Server {
	t.Helper()
	known := make(map[string]bool, len(knownIDs))
	for _, id := range knownIDs {
		known[id] = true
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		*requests = append(*requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		if bodies != nil && r.Method != http.MethodGet {
			body, _ := io.ReadAll(r.Body)
			*bodies = append(*bodies, string(body))
		}
		if r.Method == http.MethodGet {
			ref := strings.TrimPrefix(r.URL.Path, "/api/v1/console/workspaces/ws_1/submissions/")
			if ref == "" || strings.Contains(ref, "/") {
				// The listing walk.
				if page, ok := pages[r.URL.Query().Get("offset")]; ok {
					_, _ = w.Write([]byte(page))
					return
				}
				_, _ = w.Write([]byte(`{"submissions":[],"total":0}`))
				return
			}
			// The detail fast path.
			if !known[ref] {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":"Submission not found in this workspace"}`))
				return
			}
			_, _ = w.Write([]byte(`{"submission":` + submissionJSON(ref) + `}`))
			return
		}
		// The server matches exact ids only: a mutation at a truncated
		// reference 404s without touching a row. The bulk route takes an id
		// list, not one id, and always answers.
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/console/workspaces/ws_1/submissions/"), "/")
		if parts[0] != "bulk-triage" && !known[parts[0]] {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Submission not found in this workspace"}`))
			return
		}
		_, _ = w.Write([]byte(`{"submissionId":"resolved","title":"Crash on launch","platform":"ios","priority":"!!","status":"received","triageStatus":"open"}`))
	}))
}

// requestsWithMethod filters the recorded requests by method.
func requestsWithMethod(requests []string, method string) []string {
	var out []string
	for _, r := range requests {
		if strings.HasPrefix(r, method+" ") {
			out = append(out, r)
		}
	}
	return out
}

// TestInboxTriageResolvesTruncatedSubmissionID covers the core #418 case:
// `inbox list` prints sub_1abcdef1234, triage takes that truncated form, the
// probe PATCH 404s, resolution walks the listing at the served page size, and
// the refire PATCH hits the full resolved id while the ✓ line carries it too.
func TestInboxTriageResolvesTruncatedSubmissionID(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var requests []string
	server := inboxResolutionServer(t, map[string]string{
		"0": `{"submissions":[` + submissionJSON(truncatedSubmissionFullID) + `],"total":1}`,
	}, []string{truncatedSubmissionFullID}, &requests, nil)
	defer server.Close()

	out, err := runRoot(t, server.URL, "inbox", "triage", truncatedSubmissionPrefix, "in_progress", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("inbox triage %s: %v", truncatedSubmissionPrefix, err)
	}
	patches := requestsWithMethod(requests, "PATCH")
	want := []string{
		"PATCH /api/v1/console/workspaces/ws_1/submissions/" + truncatedSubmissionPrefix + "/triage?",
		"PATCH /api/v1/console/workspaces/ws_1/submissions/" + truncatedSubmissionFullID + "/triage?",
	}
	if !reflect.DeepEqual(patches, want) {
		t.Errorf("triage PATCHes = %v, want %v", patches, want)
	}
	if !strings.Contains(out, "✓ Triage status of "+truncatedSubmissionFullID+" set to in_progress") {
		t.Errorf("success line must carry the resolved full id:\n%s", out)
	}
	var walkPage bool
	for _, r := range requestsWithMethod(requests, "GET") {
		if strings.HasSuffix(r, "/submissions?limit=200&offset=0") {
			walkPage = true
		}
	}
	if !walkPage {
		t.Errorf("resolution must walk the listing at the served page size: %v", requestsWithMethod(requests, "GET"))
	}
}

// TestInboxGetResolvesTruncatedSubmissionID covers the read path: a truncated
// reference renders the full detail of the resolved submission.
func TestInboxGetResolvesTruncatedSubmissionID(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var requests []string
	server := inboxResolutionServer(t, map[string]string{
		"0": `{"submissions":[` + submissionJSON(truncatedSubmissionFullID) + `],"total":1}`,
	}, []string{truncatedSubmissionFullID}, &requests, nil)
	defer server.Close()

	out, err := runRoot(t, server.URL, "inbox", "get", truncatedSubmissionPrefix, "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("inbox get %s: %v", truncatedSubmissionPrefix, err)
	}
	gets := requestsWithMethod(requests, "GET")
	want := []string{
		"GET /api/v1/console/workspaces/ws_1/submissions/" + truncatedSubmissionPrefix + "?",
		"GET /api/v1/console/workspaces/ws_1/submissions?limit=200&offset=0",
		"GET /api/v1/console/workspaces/ws_1/submissions/" + truncatedSubmissionFullID + "?",
	}
	if !reflect.DeepEqual(gets, want) {
		t.Errorf("gets = %v, want %v", gets, want)
	}
	if !strings.Contains(out, "Crash on launch") || !strings.Contains(out, truncatedSubmissionFullID) {
		t.Errorf("detail render must describe the resolved submission:\n%s", out)
	}
}

// TestInboxAbsentFullIDWalksThenNamesScope pins the truly-absent case: a full
// id that 404s falls back to the listing walk, and the not-found error names
// the reference and the workspace the scan covered — with zero mutations.
func TestInboxAbsentFullIDWalksThenNamesScope(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var requests []string
	server := inboxResolutionServer(t, map[string]string{
		"0": `{"submissions":[` + submissionJSON(truncatedSubmissionFullID) + `],"total":1}`,
	}, []string{truncatedSubmissionFullID}, &requests, nil)
	defer server.Close()

	_, err := runRoot(t, server.URL, "inbox", "retry", "sub_missing000000000000000", "--workspace", "ws_1")
	if err == nil {
		t.Fatal("absent id must fail")
	}
	if !strings.Contains(err.Error(), `submission "sub_missing000000000000000" not found (scanned 1 submissions in workspace ws_1)`) {
		t.Errorf("not-found error missing the scoped wording:\n%v", err)
	}
	if len(requestsWithMethod(requests, "POST")) != 1 {
		t.Errorf("only the 404 probe retry may fire: %v", requestsWithMethod(requests, "POST"))
	}
}

// TestInboxAmbiguousPrefixFailsWithoutMutation pins the ambiguity decision:
// two submissions sharing the table's truncated form fail locally with the
// shared wording and no mutation succeeds.
func TestInboxAmbiguousPrefixFailsWithoutMutation(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	twins := `{"submissions":[` +
		submissionJSON("sub_1abcdef1234twin_a00000000000") + "," +
		submissionJSON("sub_1abcdef1234twin_b00000000000") +
		`],"total":2}`
	var requests []string
	server := inboxResolutionServer(t, map[string]string{"0": twins}, nil, &requests, nil)
	defer server.Close()

	_, err := runRoot(t, server.URL, "inbox", "assign", truncatedSubmissionPrefix, "user_1", "--workspace", "ws_1")
	if err == nil || !strings.Contains(err.Error(), `ambiguous submission prefix "`+truncatedSubmissionPrefix+`" matches 2 submissions; use a longer prefix`) {
		t.Fatalf("error = %v, want the ambiguity error", err)
	}
	if len(requestsWithMethod(requests, "PUT")) != 1 {
		t.Errorf("only the 404 probe assign may fire: %v", requestsWithMethod(requests, "PUT"))
	}
}

// TestInboxBulkTriageResolvesTruncatedIDs pins the up-front resolution
// contract: the bulk endpoint skips unknown ids instead of 404ing, so every
// positional reference resolves before the body is built and the wire body
// carries only full ids.
func TestInboxBulkTriageResolvesTruncatedIDs(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	full := "sub_9abcdef1234fffffffffffffffff"
	var requests, bodies []string
	server := inboxResolutionServer(t, map[string]string{
		"0": `{"submissions":[` + submissionJSON(full) + `],"total":1}`,
	}, []string{full}, &requests, &bodies)
	defer server.Close()

	if _, err := runRoot(t, server.URL, "inbox", "bulk-triage", "sub_9abcdef1234", "--triage-status", "resolved", "--workspace", "ws_1"); err != nil {
		t.Fatalf("bulk-triage with a prefix: %v", err)
	}
	// The wire body carries the resolved FULL id — a truncated reference must
	// never reach the bulk endpoint verbatim, where it would be silently
	// skipped instead of 404ing.
	wantBody := `{"submissionIds":["` + full + `"],"triageStatus":"resolved"}`
	if len(bodies) != 1 || bodies[0] != wantBody {
		t.Errorf("bulk body = %q, want %q", bodies, wantBody)
	}
}

// TestInboxBulkTriageAmbiguousPrefixNamesArgument pins the positional
// diagnosis: when the second reference is ambiguous the error names argument
// 2 and no bulk POST fires.
func TestInboxBulkTriageAmbiguousPrefixNamesArgument(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	full := "sub_9abcdef1234fffffffffffffffff"
	// Page 0 is a full 200-entry page of non-matches (otherwise the walk
	// stops there: 1 < the 200-entry page cap reads as exhausted), so the
	// twins live on the offset-200 page and the ambiguity decision spans
	// both.
	entries := make([]string, 0, inboxPageSize)
	for i := 0; i < inboxPageSize-1; i++ {
		entries = append(entries, submissionJSON(fmt.Sprintf("sub_page%04dffffffffffffffffffff", i)))
	}
	// Argument 1's prefix resolves against this page: `full` rides on it.
	entries = append(entries, submissionJSON(full))
	twins := `{"submissions":[` +
		submissionJSON("sub_1abcdef1234twin_a00000000000") + "," +
		submissionJSON("sub_1abcdef1234twin_b00000000000") +
		`],"total":202}`
	pages := map[string]string{
		"0":   `{"submissions":[` + strings.Join(entries, ",") + `],"total":202}`,
		"200": twins,
	}
	var requests []string
	server := inboxResolutionServer(t, pages, []string{full}, &requests, nil)
	defer server.Close()

	_, err := runRoot(t, server.URL, "inbox", "bulk-triage", "sub_9abcdef1234", truncatedSubmissionPrefix,
		"--triage-status", "resolved", "--workspace", "ws_1")
	if err == nil || !strings.Contains(err.Error(), "argument 2:") ||
		!strings.Contains(err.Error(), `ambiguous submission prefix "sub_1abcdef1234" matches 2 submissions`) {
		t.Fatalf("error = %v, want argument 2 naming the ambiguity error", err)
	}
	if len(requestsWithMethod(requests, "POST")) != 0 {
		t.Errorf("bulk POST must not fire when a reference is ambiguous: %v", requestsWithMethod(requests, "POST"))
	}
}

// TestInboxPrefixScanCoversTheOffsetCapPage pins the +1 page math for the
// submissions walk, mirroring TestChangelogPrefixScanCoversTheOffsetCapPage:
// the server still serves the page at offset 10,000 (the clamp's boundary),
// so resolution must page one past the naive 50-page window — a submission
// living only on that final page resolves instead of a same-prefix submission
// inside the scanned window silently winning.
func TestInboxPrefixScanCoversTheOffsetCapPage(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	// 10,000 submissions fill offsets 0..9,900 and the target sits alone on
	// the offset-10,000 page, so the authoritative total is 10,001: pages
	// 0..49 report more, and only the cap page exhausts the listing.
	const totalEntries = inboxPageSize*50 + 1
	fullPage := func() string {
		entries := make([]string, 0, inboxPageSize)
		for i := 0; i < inboxPageSize; i++ {
			entries = append(entries, submissionJSON(fmt.Sprintf("sub_page%04dffffffffffffffffffff", i)))
		}
		return `{"submissions":[` + strings.Join(entries, ",") + `],"total":` + fmt.Sprint(totalEntries) + `}`
	}
	capPage := `{"submissions":[` + submissionJSON("sub_target000123ffffffffffffff") + `],"total":` + fmt.Sprint(totalEntries) + `}`

	var offsets []string
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		requests = append(requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		if r.Method == http.MethodGet {
			offsets = append(offsets, r.URL.Query().Get("offset"))
			if r.URL.Query().Get("offset") == "10000" {
				_, _ = w.Write([]byte(capPage))
				return
			}
			_, _ = w.Write([]byte(fullPage()))
			return
		}
		// The server matches exact ids only: the truncated probe 404s
		// without touching a row; the resolved refire succeeds.
		if !strings.HasSuffix(r.URL.Path, "/sub_target000123ffffffffffffff/priority") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Submission not found in this workspace"}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	out, err := runRoot(t, server.URL, "inbox", "priority", "sub_target000123", "!!!", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("inbox priority on the offset-cap page: %v", err)
	}
	if got := offsets[len(offsets)-1]; got != "10000" {
		t.Errorf("last scanned offset = %q, want the 10000 clamp page", got)
	}
	if len(offsets) != maxInboxResolvePages {
		t.Errorf("scanned %d pages, want %d (the +1 window)", len(offsets), maxInboxResolvePages)
	}
	PUTs := requestsWithMethod(requests, "PUT")
	if len(PUTs) != 2 {
		t.Fatalf("priority PUTs = %v, want probe + refire", PUTs)
	}
	if !strings.HasPrefix(PUTs[1], "PUT /api/v1/console/workspaces/ws_1/submissions/sub_target000123ffffffffffffff/priority") {
		t.Errorf("refire = %s, want the full resolved id", PUTs[1])
	}
	if !strings.Contains(out, "✓ Priority of sub_target000123ffffffffffffff set to !!!") {
		t.Errorf("success line must carry the resolved full id:\n%s", out)
	}
}
