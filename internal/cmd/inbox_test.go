package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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
// triageStatus/assignedTo, assignee vs unassign).
func TestInboxBulkTriage(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var captured capturedRequest
	server := inboxServer(t, http.StatusOK,
		`{"updatedCount": 1, "updatedIds": ["sub_1"]}`, &captured)
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
