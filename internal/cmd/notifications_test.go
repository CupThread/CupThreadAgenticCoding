package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// notificationsListFixture is a minimal ListNotificationsResponse covering
// the read/unread split the table renders (issue #201 adds the clamp tests).
const notificationsListFixture = `{
	"notifications": [
		{"id": "ntf_1111111111abcdef", "workspaceId": "ws_1", "type": "feedback.received", "title": "New feedback on Dark mode", "readAt": null, "createdAt": "2026-10-01T10:00:00.000Z"},
		{"id": "ntf_2222222222abcdef", "workspaceId": "ws_1", "type": "changelog.published", "title": "v1.2.0 shipped", "readAt": "2026-10-02T09:00:00.000Z", "createdAt": "2026-10-01T11:00:00.000Z"}
	],
	"total": 2,
	"unreadCount": 1
}`

// notificationsServer replies with respBody and captures the request query.
func notificationsServer(t *testing.T, respBody string) (*httptest.Server, *url.Values) {
	t.Helper()
	var gotQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respBody))
	}))
	t.Cleanup(server.Close)
	return server, &gotQuery
}

// TestNotificationsListRendersReadState covers the table contract: newest
// first rows with the read/unread state and the unread-count trailer.
func TestNotificationsListRendersReadState(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	server, gotQuery := notificationsServer(t, notificationsListFixture)

	out, err := runRoot(t, server.URL, "notifications", "list", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("notifications list: %v", err)
	}
	if got := gotQuery.Get("limit"); got != "50" {
		t.Errorf("default limit = %q, want 50", got)
	}
	for _, want := range []string{"feedback.received", "unread", "changelog.published", "read", "(1 unread)"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
}

// TestNotificationsListClampsLimit pins the local --limit clamp on the
// notifications listing (issue #201): the server clamps a parsed page size to
// its parseListPagination maxLimit (200) silently, so an offset walk stepped
// by a larger requested limit would skip rows without any hint (the command
// prints no more-pages marker at all). The CLI clamps first — over-cap to
// 200, 0/negative to 1 (matching features list) — and announces the rewrite
// through warnf: table mode keeps it on stdout, --json mode moves it to
// stderr so stdout stays a single document. The 200 boundary passes through
// verbatim with no warning.
func TestNotificationsListClampsLimit(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	server, gotQuery := notificationsServer(t, notificationsListFixture)

	for _, tc := range []struct{ requested, sent string }{
		{"500", "200"},
		{"0", "1"},
		{"-5", "1"},
	} {
		out, err := runRoot(t, server.URL, "notifications", "list", "--workspace", "ws_1", "--limit", tc.requested)
		if err != nil {
			t.Fatalf("notifications list --limit %s: %v", tc.requested, err)
		}
		if got := gotQuery.Get("limit"); got != tc.sent {
			t.Errorf("--limit %s sent limit = %q, want %q", tc.requested, got, tc.sent)
		}
		if !strings.Contains(out, "⚠ --limit "+tc.requested+" is outside the server's 1-200 page range") {
			t.Errorf("--limit %s output missing clamp warning:\n%s", tc.requested, out)
		}
	}

	out, err := runRoot(t, server.URL, "notifications", "list", "--workspace", "ws_1", "--limit", "200")
	if err != nil {
		t.Fatalf("notifications list --limit 200: %v", err)
	}
	if got := gotQuery.Get("limit"); got != "200" {
		t.Errorf("boundary --limit 200 sent limit = %q, want 200", got)
	}
	if strings.Contains(out, "⚠") {
		t.Errorf("boundary --limit 200 produced a warning:\n%s", out)
	}

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	stdout, stderr, err := runRootCapture(t, cfgPath, server.URL,
		"notifications", "list", "--workspace", "ws_1", "--limit", "500", "--json")
	if err != nil {
		t.Fatalf("notifications list --json --limit 500: %v", err)
	}
	unmarshalOneJSON(t, stdout)
	assertNoTableOutput(t, stdout)
	if !strings.Contains(stderr, "⚠ --limit 500 is outside the server's 1-200 page range; requesting 200 instead") {
		t.Errorf("json stderr missing clamp warning:\n%s", stderr)
	}
}
