package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// expectedServerNotificationTypes is the snapshot of the server's
// NotificationTypeSchema enum (SaaS packages/shared/src/schemas/notifications.ts)
// in enum order. It is pinned so a server-side enum change fails here until
// the CLI slice and the skill doc are synced in the same change (issue #200).
var expectedServerNotificationTypes = []string{
	"feedback.received",
	"feature_request.submitted",
	"feature_request.approved",
	"feature_request.shipped",
	"comment.received",
	"vote.milestone",
	"changelog.published",
	"weekly.digest",
	"delivery.success",
	"delivery.failed",
	"import.completed",
	"import.failed",
	"subscription.updated",
	"system",
}

const notificationPrefFixture = `{
	"workspaceId": "ws_1",
	"channel": "inbox",
	"eventMask": ["feedback.received"],
	"enabled": true,
	"updatedAt": "2026-10-10T00:00:00.000Z"
}`

// TestPrefsSetAllEventsSendsFullServerEnum pins issue #200: --all-events must
// put every server notification type — including comment.received — on the
// wire, in enum order. The written eventMask replaces the channel's mask
// server-side, so a missing member silently suppresses that notification for
// the whole channel.
func TestPrefsSetAllEventsSendsFullServerEnum(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var captured capturedRequest
	server := inboxServer(t, http.StatusOK, notificationPrefFixture, &captured)
	defer server.Close()

	out, err := runRoot(t, server.URL, "notifications", "prefs", "set",
		"--channel", "inbox", "--all-events", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("prefs set --all-events: %v", err)
	}
	if captured.Method != "PUT" {
		t.Errorf("method = %s, want PUT", captured.Method)
	}
	if captured.Path != "/api/v1/console/workspaces/ws_1/notification-prefs" {
		t.Errorf("path = %s", captured.Path)
	}
	var body struct {
		Channel   string   `json:"channel"`
		EventMask []string `json:"eventMask"`
	}
	if err := json.Unmarshal([]byte(captured.Body), &body); err != nil {
		t.Fatalf("decode request body %q: %v", captured.Body, err)
	}
	if body.Channel != "inbox" {
		t.Errorf("channel = %q, want inbox", body.Channel)
	}
	if !reflect.DeepEqual(body.EventMask, expectedServerNotificationTypes) {
		t.Errorf("eventMask = %v, want the full 14-type server enum %v", body.EventMask, expectedServerNotificationTypes)
	}
	if !strings.Contains(out, "✓ inbox notifications: yes") {
		t.Errorf("output missing confirmation:\n%s", out)
	}
}

// TestPrefsSetEventsValidatedLocally pins the local --events validation:
// known types (including the previously-suppressed comment.received) are sent
// verbatim, while an unknown value fails locally with a self-diagnosing error
// listing the accepted types — nothing reaches the API.
func TestPrefsSetEventsValidatedLocally(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var captured capturedRequest
	server := inboxServer(t, http.StatusOK, notificationPrefFixture, &captured)
	defer server.Close()

	if _, err := runRoot(t, server.URL, "notifications", "prefs", "set",
		"--channel", "inbox", "--events", "delivery.failed,comment.received", "--workspace", "ws_1"); err != nil {
		t.Fatalf("prefs set --events with known types: %v", err)
	}
	var body struct {
		EventMask []string `json:"eventMask"`
	}
	if err := json.Unmarshal([]byte(captured.Body), &body); err != nil {
		t.Fatalf("decode request body %q: %v", captured.Body, err)
	}
	if !reflect.DeepEqual(body.EventMask, []string{"delivery.failed", "comment.received"}) {
		t.Errorf("eventMask = %v, want the two requested types in order", body.EventMask)
	}

	var rejected capturedRequest
	rejectServer := inboxServer(t, http.StatusOK, notificationPrefFixture, &rejected)
	defer rejectServer.Close()
	_, err := runRoot(t, rejectServer.URL, "notifications", "prefs", "set",
		"--channel", "inbox", "--events", "comment.received,bogus.type", "--workspace", "ws_1")
	if err == nil || !strings.Contains(err.Error(), `invalid --events "bogus.type"`) || !strings.Contains(err.Error(), "comment.received") {
		t.Fatalf("error = %v, want the self-diagnosing invalid --events message listing the accepted types", err)
	}
	if rejected.Method != "" || rejected.Body != "" {
		t.Errorf("invalid --events reached the API: %s %s", rejected.Method, rejected.Body)
	}
}

// TestPrefsSetEventsCompletionOffersAllTypes covers the --events shell
// completion: every server notification type, including comment.received
// (issue #200), must be discoverable there.
func TestPrefsSetEventsCompletionOffersAllTypes(t *testing.T) {
	out := runComplete(t, "notifications", "prefs", "set", "--events", "")
	for _, want := range expectedServerNotificationTypes {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("completion output missing %q:\n%s", want, out)
		}
	}
}

// backtickToken extracts `…` spans for the skill-doc enum parser below.
var backtickToken = regexp.MustCompile("`([^`]+)`")

// runComplete executes the CLI's hidden __complete command and returns its
// stdout. Unlike runRoot it puts the global flags before the subcommand, so
// cobra's toComplete argument stays the last word after __complete.
func runComplete(t *testing.T, args ...string) string {
	t.Helper()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	root := newRootCmd()
	full := append([]string{"--base-url", "http://127.0.0.1:1", "--config", filepath.Join(t.TempDir(), "config.json"), "__complete"}, args...)
	root.SetArgs(full)
	if execErr := root.Execute(); execErr != nil {
		os.Stdout = oldStdout
		t.Fatalf("__complete %v: %v", args, execErr)
	}

	os.Stdout = oldStdout
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return string(out)
}

// TestNotificationTypeEnumDrift guards the cross-repo contract (issue #200):
// the CLI's notificationTypes must (a) equal the pinned server enum snapshot
// and (b) match, member for member, the full enum enumerated in the CLI
// skill's notifications section. A one-sided change — the server gains a
// type, or the doc is updated without the code — fails here instead of
// silently mutating channels through --all-events.
func TestNotificationTypeEnumDrift(t *testing.T) {
	if !reflect.DeepEqual(notificationTypes, expectedServerNotificationTypes) {
		t.Errorf("notificationTypes = %v, want the server enum snapshot %v (update the code, the skill doc and this snapshot together)",
			notificationTypes, expectedServerNotificationTypes)
	}

	doc := readSkill(t, "cupthread-cli")
	var enumLine string
	for _, line := range strings.Split(doc, "\n") {
		if strings.Contains(line, "`comment.received`") {
			if enumLine != "" {
				t.Fatal("multiple skill-doc lines mention `comment.received`; the enum-line locator in this test needs updating")
			}
			enumLine = line
		}
	}
	if enumLine == "" {
		t.Fatal("cupthread-cli/SKILL.md no longer enumerates the notification enum (no line mentions `comment.received`)")
	}

	docTypes := map[string]bool{}
	for _, m := range backtickToken.FindAllStringSubmatch(enumLine, -1) {
		docTypes[m[1]] = true
	}
	codeTypes := map[string]bool{}
	for _, member := range notificationTypes {
		codeTypes[member] = true
	}
	if len(docTypes) != len(notificationTypes) {
		t.Errorf("skill-doc enum line has %d members, want %d: %v", len(docTypes), len(notificationTypes), enumLine)
	}
	for _, member := range notificationTypes {
		if !docTypes[member] {
			t.Errorf("skill-doc enum line is missing CLI type %q:\n%s", member, enumLine)
		}
	}
	for member := range docTypes {
		if !codeTypes[member] {
			t.Errorf("skill-doc enum line lists %q but notificationTypes does not (update both together)", member)
		}
	}
}
