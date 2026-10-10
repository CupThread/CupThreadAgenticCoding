package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// versionsServer responds with status/respBody on the version DELETE route
// and captures the single request the CLI sent.
func versionsServer(t *testing.T, status int, respBody string, captured *capturedRequest) *httptest.Server {
	t.Helper()
	return inboxServer(t, status, respBody, captured)
}

// TestVersionsDelete409NamesFlagRemediation covers issue #199: a version
// with linked feature requests is refused with 409 version_has_feature_requests
// (no `code` field, so the identifier lands in Message) and the CLI must turn
// the dead-end 409 into an actionable error quoting the server's count and
// both flag escapes.
func TestVersionsDelete409NamesFlagRemediation(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")
	withConfirmHooks(t, strings.NewReader(""), false)

	var captured capturedRequest
	server := versionsServer(t, http.StatusConflict,
		`{"error":"version_has_feature_requests","count":3}`, &captured)
	defer server.Close()

	_, err := runRoot(t, server.URL, "versions", "delete", "ver_1", "--workspace", "ws_1", "--yes")
	if err == nil {
		t.Fatal("versions delete on a linked version must fail")
	}
	for _, want := range []string{
		"--confirm-label",
		"--reassign-to",
		"3 linked feature request(s)",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
	if captured.Method != http.MethodDelete || captured.Path != "/api/v1/console/workspaces/ws_1/versions/ver_1" {
		t.Errorf("request = %s %s", captured.Method, captured.Path)
	}
}

// TestVersionsDelete409WithoutCountStillGuides pins the degraded rendering:
// a 409 whose body carries no parseable count must keep the flag remediation
// instead of quoting a fabricated 0.
func TestVersionsDelete409WithoutCountStillGuides(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")
	withConfirmHooks(t, strings.NewReader(""), false)

	var captured capturedRequest
	server := versionsServer(t, http.StatusConflict,
		`{"error":"version_has_feature_requests"}`, &captured)
	defer server.Close()

	_, err := runRoot(t, server.URL, "versions", "delete", "ver_1", "--workspace", "ws_1", "--yes")
	if err == nil {
		t.Fatal("versions delete on a linked version must fail")
	}
	if !strings.Contains(err.Error(), "an unknown number of linked feature request(s)") {
		t.Errorf("error missing the unknown-count fallback:\n%v", err)
	}
	if !strings.Contains(err.Error(), "--confirm-label") {
		t.Errorf("error missing --confirm-label remediation:\n%v", err)
	}
}

// TestVersionsDeleteConfirmLabelBody covers the acknowledged delete: the
// captured DELETE body is exactly the confirmation the server contract
// wants, and a 200 renders the success line.
func TestVersionsDeleteConfirmLabelBody(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")
	withConfirmHooks(t, strings.NewReader(""), false)

	var captured capturedRequest
	server := versionsServer(t, http.StatusOK, `{"ok":true,"unlinkedRequests":3}`, &captured)
	defer server.Close()

	out, err := runRoot(t, server.URL, "versions", "delete", "ver_1", "--workspace", "ws_1", "--yes", "--confirm-label", "1.2.0")
	if err != nil {
		t.Fatalf("versions delete --confirm-label: %v", err)
	}
	if captured.Body != `{"confirmLabel":"1.2.0"}` {
		t.Errorf("request body = %s, want {\"confirmLabel\":\"1.2.0\"}", captured.Body)
	}
	if !strings.Contains(out, "✓ Deleted version ver_1") {
		t.Errorf("output missing confirmation line:\n%s", out)
	}
}

// TestVersionsDeleteReassignBody covers the reassign escape: the flag lands
// on the wire as reassignToVersionId alongside (or without) confirmLabel.
func TestVersionsDeleteReassignBody(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")
	withConfirmHooks(t, strings.NewReader(""), false)

	var captured capturedRequest
	server := versionsServer(t, http.StatusOK, `{"ok":true}`, &captured)
	defer server.Close()

	if _, err := runRoot(t, server.URL, "versions", "delete", "ver_1", "--workspace", "ws_1", "--yes", "--reassign-to", "ver_2"); err != nil {
		t.Fatalf("versions delete --reassign-to: %v", err)
	}
	if captured.Body != `{"reassignToVersionId":"ver_2"}` {
		t.Errorf("request body = %s, want {\"reassignToVersionId\":\"ver_2\"}", captured.Body)
	}
}

// TestVersionsDeleteReassignTargetNotInAppGuides pins the mapping of the
// server's 400 reassign_target_not_in_app onto the same-app target rule
// instead of a bare 400.
func TestVersionsDeleteReassignTargetNotInAppGuides(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")
	withConfirmHooks(t, strings.NewReader(""), false)

	var captured capturedRequest
	server := versionsServer(t, http.StatusBadRequest,
		`{"error":"Reassign target must be a version of the same app","code":"reassign_target_not_in_app"}`, &captured)
	defer server.Close()

	_, err := runRoot(t, server.URL, "versions", "delete", "ver_1", "--workspace", "ws_1", "--yes", "--reassign-to", "ver_foreign")
	if err == nil {
		t.Fatal("a foreign-app reassign target must fail")
	}
	for _, want := range []string{
		"same app",
		"not the version being deleted",
		"cupthread versions list",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
}

// TestVersionsDeleteNoFlagsSendsNoBody pins the unguarded path: deleting a
// version without linked requests keeps sending an empty body — the flags
// must not leak an empty-string confirmLabel onto the wire.
func TestVersionsDeleteNoFlagsSendsNoBody(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")
	withConfirmHooks(t, strings.NewReader(""), false)

	var captured capturedRequest
	server := versionsServer(t, http.StatusOK, `{"ok":true}`, &captured)
	defer server.Close()

	out, err := runRoot(t, server.URL, "versions", "delete", "ver_1", "--workspace", "ws_1", "--yes")
	if err != nil {
		t.Fatalf("versions delete: %v", err)
	}
	if captured.Body != "" {
		t.Errorf("request body = %q, want empty", captured.Body)
	}
	if !strings.Contains(out, "✓ Deleted version ver_1") {
		t.Errorf("output missing confirmation line:\n%s", out)
	}
}

// TestIssue199CommandHelp pins the cobra flag help agents read for the
// versions delete guard: --confirm-label states the current-label
// acknowledgment and --reassign-to states the same-app sibling rule.
func TestIssue199CommandHelp(t *testing.T) {
	root := newRootCmd()
	versions := findSub(t, root, "versions")
	del := findSub(t, versions, "delete")

	confirmLabel := del.Flags().Lookup("confirm-label")
	if confirmLabel == nil || !strings.Contains(confirmLabel.Usage, "Current label") || !strings.Contains(confirmLabel.Usage, "feature requests are still linked") {
		t.Errorf("confirm-label flag usage = %#v, want the linked-requests acknowledgment rule", confirmLabel)
	}
	reassignTo := del.Flags().Lookup("reassign-to")
	if reassignTo == nil || !strings.Contains(reassignTo.Usage, "same app") || !strings.Contains(reassignTo.Usage, "not the version being deleted") {
		t.Errorf("reassign-to flag usage = %#v, want the same-app target rule", reassignTo)
	}
	if !strings.Contains(del.Long, "version_has_feature_requests") {
		t.Errorf("delete Long help missing the 409 guard contract:\n%s", del.Long)
	}
}

// TestCLISkillIssue199 pins the cupthread-cli skill's versions-delete guard
// documentation: the 409 contract, both flag escapes, the unlink-vs-reassign
// semantics, and the reassign target rule.
func TestCLISkillIssue199(t *testing.T) {
	doc := readSkill(t, "cupthread-cli")
	for _, marker := range []string{
		"`409 version_has_feature_requests`",
		"`--confirm-label <current label>`",
		"`--reassign-to <version-id>`",
		"`400 reassign_target_not_in_app`",
		"A version with no linked requests needs neither flag",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-cli/SKILL.md is missing required issue-#199 marker %q", marker)
		}
	}
}
