package cmd

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

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
