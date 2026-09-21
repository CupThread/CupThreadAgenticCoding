package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// moderationCommentsFixture exercises the Console Moderation list payload,
// including a hidden comment that must surface in the output.
const moderationCommentsFixture = `{
	"comments": [
		{
			"id": "cmt_visible_1234",
			"featureRequestId": "fr_1",
			"authorName": "Ada",
			"body": "Please add dark mode",
			"isHidden": false,
			"createdAt": "2026-09-18T01:02:03Z"
		},
		{
			"id": "cmt_hidden_5678",
			"featureRequestId": "fr_1",
			"authorName": "Bob",
			"body": "spam message",
			"isHidden": true,
			"createdAt": "2026-09-18T04:05:06Z"
		}
	]
}`

// runModerationRoot executes the CLI with a test bearer token, since the
// Console Moderation endpoints require authentication.
func runModerationRoot(t *testing.T, serverURL string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	return runRoot(t, serverURL, args...)
}

// TestCommentsModerationList covers the workspace-scoped moderation list
// endpoint and asserts the path id is authoritative (no X-Workspace-Id).
func TestCommentsModerationList(t *testing.T) {
	var gotMethod, gotPath, gotWorkspace string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotWorkspace = r.Method, r.URL.Path, r.Header.Get("X-Workspace-Id")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(moderationCommentsFixture))
	}))
	defer server.Close()

	out, err := runModerationRoot(t, server.URL, "comments", "moderation", "list", "fr_1", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("moderation list: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/api/v1/console/workspaces/ws_1/feature-requests/fr_1/comments" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	if gotWorkspace != "" {
		t.Errorf("X-Workspace-Id = %q, want no header (path id is authoritative)", gotWorkspace)
	}
	for _, want := range []string{"Ada", "Please add dark mode", "Bob", "spam", "yes", "(2 comments)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestCommentsModerationListNotFound verifies the 404 response (feature
// request missing from the workspace) becomes an explicit error.
func TestCommentsModerationListNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Feature request not found in this workspace"}`))
	}))
	defer server.Close()

	_, err := runModerationRoot(t, server.URL, "comments", "moderation", "list", "fr_missing", "--workspace", "ws_1")
	if err == nil || !strings.Contains(err.Error(), `feature request "fr_missing" not found in workspace ws_1`) {
		t.Fatalf("error = %v, want a not-found message naming the workspace", err)
	}
}

// TestCommentsModerationHideUnhide covers both variants of the hide endpoint,
// including the isHidden request body.
func TestCommentsModerationHideUnhide(t *testing.T) {
	cases := []struct {
		name         string
		args         []string
		wantBody     string
		wantResponse string
	}{
		{name: "hide", args: []string{"hide", "cmt_1"}, wantBody: `{"isHidden":true}`, wantResponse: "hidden"},
		{name: "unhide", args: []string{"unhide", "cmt_1"}, wantBody: `{"isHidden":false}`, wantResponse: "unhidden"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotMethod, gotPath, gotBody string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath = r.Method, r.URL.Path
				body, _ := io.ReadAll(r.Body)
				gotBody = string(body)
				_, _ = w.Write([]byte(`{"success":true}`))
			}))
			defer server.Close()

			args := append([]string{"comments", "moderation"}, append(tc.args, "--workspace", "ws_1")...)
			out, err := runModerationRoot(t, server.URL, args...)
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if gotMethod != http.MethodPatch || gotPath != "/api/v1/console/workspaces/ws_1/comments/cmt_1/hide" {
				t.Errorf("request = %s %s", gotMethod, gotPath)
			}
			if gotBody != tc.wantBody {
				t.Errorf("body = %s, want %s", gotBody, tc.wantBody)
			}
			if !strings.Contains(out, tc.wantResponse) {
				t.Errorf("output missing %q:\n%s", tc.wantResponse, out)
			}
		})
	}
}

// TestCommentsModerationHideNotFound verifies a stale comment id on the hide
// endpoint surfaces as 404, not a success:false body.
func TestCommentsModerationHideNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Comment not found in this workspace"}`))
	}))
	defer server.Close()

	_, err := runModerationRoot(t, server.URL, "comments", "moderation", "hide", "cmt_gone", "--workspace", "ws_1")
	if err == nil || !strings.Contains(err.Error(), `comment "cmt_gone" not found in workspace ws_1`) {
		t.Fatalf("error = %v, want a not-found message naming the workspace", err)
	}
}

// TestCommentsModerationDelete covers the permanent delete endpoint and its
// 404 path.
func TestCommentsModerationDelete(t *testing.T) {
	var gotMethod, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		if strings.HasSuffix(r.URL.Path, "cmt_gone") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Comment not found in this workspace"}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()

	out, err := runModerationRoot(t, server.URL, "comments", "moderation", "delete", "cmt_1", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("moderation delete: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/api/v1/console/workspaces/ws_1/comments/cmt_1" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(out, "deleted") {
		t.Errorf("output missing confirmation:\n%s", out)
	}

	_, err = runModerationRoot(t, server.URL, "comments", "moderation", "delete", "cmt_gone", "--workspace", "ws_1")
	if err == nil || !strings.Contains(err.Error(), `comment "cmt_gone" not found in workspace ws_1`) {
		t.Fatalf("error = %v, want a not-found message naming the workspace", err)
	}
}

// TestCommentsModerationListJSON keeps machine-readable output on the raw API
// contract field names.
func TestCommentsModerationListJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(moderationCommentsFixture))
	}))
	defer server.Close()

	out, err := runModerationRoot(t, server.URL, "comments", "moderation", "list", "fr_1", "--workspace", "ws_1", "--json")
	if err != nil {
		t.Fatalf("moderation list --json: %v", err)
	}
	for _, want := range []string{`"isHidden": true`, `"authorName": "Ada"`} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON output missing %q:\n%s", want, out)
		}
	}
}

// TestCommentsListNotFoundCoversUnapproved covers the PRIV-12 existence
// hiding (issue #77): the public thread endpoint answers 404 both for a
// missing id and for an unapproved request, so the CLI must present that as
// "not available" instead of implying the request never existed.
func TestCommentsListNotFoundCoversUnapproved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/feature-requests/fr_pending/comments" {
			t.Errorf("request path = %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Feature request not found"}`))
	}))
	defer server.Close()

	_, err := runModerationRoot(t, server.URL, "comments", "list", "fr_pending")
	if err == nil {
		t.Fatal("expected a not-available error for the 404 response")
	}
	for _, want := range []string{"not available", "may not exist", "may not be approved yet"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "never existed") {
		t.Errorf("error = %q, must not imply the request never existed", err)
	}
}

// TestCommentsListAuthenticationRequiredHint covers the PRIV-12 401 (issue
// #77): a sign-in-only board rejects anonymous thread reads with
// authentication_required, and the CLI error must surface the actionable
// hint instead of a bare status line.
func TestCommentsListAuthenticationRequiredHint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"Sign in is required to view this roadmap","code":"authentication_required"}`))
	}))
	defer server.Close()

	_, err := runModerationRoot(t, server.URL, "comments", "list", "fr_1")
	if err == nil {
		t.Fatal("expected an authentication-required error")
	}
	for _, want := range []string{"authentication required", "Sign in is required to view this roadmap", "requires a signed-in session", "re-enable anonymous access"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

// poisonedAuthor is the issue #70 injection payload: an OSC 8 hyperlink
// labeled "CupThread Security" pointing at an attacker URL, an SGR color
// sequence, and a CR-forged line mimicking CLI success output.
const poisonedAuthor = "\x1b]8;;https://evil.example/verify\x1b\\CupThread Security\x1b]8;;\x1b\\\x1b[31mACCOUNT COMPROMISED\x1b[0m\r✓ Backup exported to ~/cupthread-backup.tar.gz"

// assertNoTerminalControlBytes fails when out carries ESC, CR, BEL or any
// other C0/DEL byte that lets content act as a terminal command.
func assertNoTerminalControlBytes(t *testing.T, out string) {
	t.Helper()
	for i := 0; i < len(out); i++ {
		b := out[i]
		if (b < 0x20 && b != '\t' && b != '\n') || b == 0x7f {
			t.Errorf("output contains control byte 0x%02x: %q", b, out)
			return
		}
	}
}

// TestCommentsModerationListSanitizesAuthorControlChars covers the issue #70
// human-output contract: an author name carrying terminal escape sequences
// renders with zero control bytes while the visible text survives. The --json
// run of the same payload must stay byte-faithful (ESC escaped, not stripped).
func TestCommentsModerationListSanitizesAuthorControlChars(t *testing.T) {
	nameJSON, err := json.Marshal(poisonedAuthor)
	if err != nil {
		t.Fatalf("marshal author: %v", err)
	}
	poisonedFixture := `{
		"comments": [
			{
				"id": "cmt_evil_0001",
				"featureRequestId": "fr_1",
				"authorName": ` + string(nameJSON) + `,
				"body": "plain body",
				"isHidden": false,
				"createdAt": "2026-09-18T01:02:03Z"
			}
		]
	}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(poisonedFixture))
	}))
	defer server.Close()

	out, err := runModerationRoot(t, server.URL, "comments", "moderation", "list", "fr_1", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("moderation list: %v", err)
	}
	assertNoTerminalControlBytes(t, out)
	for _, want := range []string{"CupThread Security", "ACCOUNT COMPROMISED", "Backup exported to ~/cupthread-backup.tar.gz", "plain body"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing visible text %q:\n%s", want, out)
		}
	}

	jsonOut, err := runModerationRoot(t, server.URL, "comments", "moderation", "list", "fr_1", "--workspace", "ws_1", "--json")
	if err != nil {
		t.Fatalf("moderation list --json: %v", err)
	}
	assertNoTerminalControlBytes(t, jsonOut)
	if !strings.Contains(jsonOut, `\u001b]8;;https://evil.example/verify`) {
		t.Errorf("JSON output lost the escaped payload:\n%s", jsonOut)
	}
}
