package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// scopedProfileFixture is a PRIV-06-shaped public profile body: the requested
// app-scoped id is echoed back, publicApps carries the owner-scoped workspace
// fields, and comment attribution uses app slugs (no raw Clerk ids).
const scopedProfileFixture = `{
	"profile": {
		"clerkUserId": "u_9f2ca1b3d4e5f60718293a4b5c6d7e8f",
		"displayName": "Ada Lovelace",
		"avatarUrl": null,
		"bio": null,
		"websiteUrl": null,
		"createdAt": "2026-01-15T10:00:00.000Z"
	},
	"publicApps": [
		{
			"id": "app_1",
			"workspaceSlug": "acme",
			"workspaceName": "Acme Studio",
			"appSlug": "ios",
			"name": "Acme iOS",
			"description": null,
			"iconUrl": null
		}
	],
	"recentComments": [
		{
			"id": "c_1",
			"body": "Would love dark mode.",
			"createdAt": "2026-02-01T08:30:00.000Z",
			"featureRequestId": "fr_1",
			"featureRequestTitle": "Dark mode",
			"workspaceSlug": "acme",
			"appSlug": "ios",
			"appName": "Acme iOS"
		}
	]
}`

// TestUsersProfileResolvesScopedIDWithAppKey covers issue #17: app-scoped
// u_* ids from board/comment payloads must be resolved through the appKey
// query parameter, and the response's publicApps (not the old apps key)
// drives the rendered tables.
func TestUsersProfileResolvesScopedIDWithAppKey(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var gotPath, gotAppKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAppKey = r.URL.Query().Get("appKey")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(scopedProfileFixture))
	}))
	defer server.Close()

	out, err := runRoot(t, server.URL, "users", "profile", "u_9f2ca1b3d4e5f60718293a4b5c6d7e8f", "--app-key", "key_live_1")
	if err != nil {
		t.Fatalf("users profile: %v", err)
	}
	if gotPath != "/api/v1/users/u_9f2ca1b3d4e5f60718293a4b5c6d7e8f/profile" {
		t.Errorf("request path = %s", gotPath)
	}
	if gotAppKey != "key_live_1" {
		t.Errorf("appKey query param = %q, want key_live_1", gotAppKey)
	}
	for _, want := range []string{"Ada Lovelace", "Acme Studio", "Acme iOS", "Dark mode", "Would love dark mode."} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestUsersProfileScopedIDRequiresAppKey guards the local pre-validation:
// a well-formed u_* id without --app-key always 404s server-side, so the CLI
// should fail fast with actionable guidance instead of issuing a doomed
// request. Ids that merely start with u_ but don't match the API's
// u_<32 hex> format are passed through untouched, mirroring the server.
func TestUsersProfileScopedIDRequiresAppKey(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	_, err := runRoot(t, server.URL, "users", "profile", "u_9f2ca1b3d4e5f60718293a4b5c6d7e8f")
	if err == nil {
		t.Fatal("expected an error for a u_* id without --app-key")
	}
	if !strings.Contains(err.Error(), "--app-key") {
		t.Errorf("error = %v, want it to mention --app-key", err)
	}
	if hits != 0 {
		t.Errorf("server hit %d time(s), want a local rejection with no request", hits)
	}

	hits = 0
	if _, err := runRoot(t, server.URL, "users", "profile", "u_too_short"); err != nil {
		t.Fatalf("malformed u_ id should be sent as-is: %v", err)
	}
	if hits != 1 {
		t.Errorf("server hit %d time(s), want the malformed id to pass through", hits)
	}
}

// TestUsersProfileLegacyIDWithoutAppKey covers the legacy path: raw user_*
// ids (old /u/ bookmarks) stay accepted without appKey, and the opt-in
// placeholder response renders as an empty profile with no app section.
func TestUsersProfileLegacyIDWithoutAppKey(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var gotAppKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAppKey = r.URL.Query().Get("appKey")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"profile": {
				"clerkUserId": "user_legacy_1",
				"displayName": null,
				"avatarUrl": null,
				"bio": null,
				"websiteUrl": null,
				"createdAt": "2026-01-15T10:00:00.000Z"
			},
			"publicApps": [],
			"recentComments": []
		}`))
	}))
	defer server.Close()

	out, err := runRoot(t, server.URL, "users", "profile", "user_legacy_1")
	if err != nil {
		t.Fatalf("users profile: %v", err)
	}
	if gotAppKey != "" {
		t.Errorf("appKey query param = %q, want it unset for legacy ids", gotAppKey)
	}
	if strings.Contains(out, "Apps (") {
		t.Errorf("placeholder profile should render no app section:\n%s", out)
	}
	if !strings.Contains(out, "user_legacy_1") {
		t.Errorf("output should echo the requested id:\n%s", out)
	}
}

// TestUsersProfileJSONOutput verifies the structured view exposes the
// publicApps contract (issue #17 renames the response key from apps).
func TestUsersProfileJSONOutput(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(scopedProfileFixture))
	}))
	defer server.Close()

	out, err := runRoot(t, server.URL, "users", "profile", "u_9f2ca1b3d4e5f60718293a4b5c6d7e8f", "--app-key", "key_live_1", "--json")
	if err != nil {
		t.Fatalf("users profile --json: %v", err)
	}
	var payload struct {
		Profile struct {
			ClerkUserID string  `json:"clerkUserId"`
			DisplayName *string `json:"displayName"`
		} `json:"profile"`
		PublicApps []struct {
			ID            string `json:"id"`
			WorkspaceName string `json:"workspaceName"`
			AppSlug       string `json:"appSlug"`
		} `json:"publicApps"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
		t.Fatalf("decode structured output %q: %v", out, err)
	}
	if payload.Profile.ClerkUserID != "u_9f2ca1b3d4e5f60718293a4b5c6d7e8f" {
		t.Errorf("clerkUserId = %q", payload.Profile.ClerkUserID)
	}
	if payload.Profile.DisplayName == nil || *payload.Profile.DisplayName != "Ada Lovelace" {
		t.Errorf("displayName = %v", payload.Profile.DisplayName)
	}
	if len(payload.PublicApps) != 1 || payload.PublicApps[0].WorkspaceName != "Acme Studio" || payload.PublicApps[0].AppSlug != "ios" {
		t.Errorf("publicApps = %+v", payload.PublicApps)
	}
}

// TestUsersProfileRateLimited covers the SEC-34 per-IP rate limit on the
// public profile GET: the CLI must map a 429 to the rate-limited error path
// with the retry-with-backoff hint, not a generic HTTP failure. The env opt
// out keeps this a single-shot request — the retry loop itself is covered in
// internal/api.
func TestUsersProfileRateLimited(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")
	t.Setenv("CUPTHREAD_NO_RETRY", "1")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error": "Too many requests. Please try again shortly."}`))
	}))
	defer server.Close()

	_, err := runRoot(t, server.URL, "users", "profile", "u_9f2ca1b3d4e5f60718293a4b5c6d7e8f", "--app-key", "key_live_1")
	if err == nil {
		t.Fatal("expected a rate-limit error from users profile")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("error = %v, want it reported as rate limited", err)
	}
	if !strings.Contains(err.Error(), "back off exponentially") {
		t.Errorf("error = %v, want the retry-with-backoff hint", err)
	}
}

// TestUsersProfileNotFound covers the SEC-34 unknown-u_* contract: an unmapped
// app-scoped id fails with the server's 404 body instead of decoding into a
// zero-value profile that would render as empty tables.
func TestUsersProfileNotFound(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error": "User profile not found"}`))
	}))
	defer server.Close()

	_, err := runRoot(t, server.URL, "users", "profile", "u_0123456789abcdef0123456789abcdef", "--app-key", "key_live_1")
	if err == nil {
		t.Fatal("expected a not-found error from users profile")
	}
	if !strings.Contains(err.Error(), "User profile not found") {
		t.Errorf("error = %v, want the server's 404 message", err)
	}
}
