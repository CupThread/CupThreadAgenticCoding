package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// meFixture carries two workspace entries (one owned, one member-only) and
// the additive maxWorkspaces field from issue #18.
const meFixture = `{
	"clerkUserId": "user_1",
	"email": "dev@example.com",
	"maxWorkspaces": 3,
	"workspaces": [
		{
			"workspace": {
				"id": "ws_1",
				"name": "Owned Co",
				"slug": "owned-co",
				"createdAt": "2026-09-01T00:00:00Z",
				"updatedAt": "2026-09-01T00:00:00Z"
			},
			"membership": {
				"id": "mem_1",
				"workspaceId": "ws_1",
				"clerkUserId": "user_1",
				"role": "owner",
				"displayName": null,
				"email": null,
				"createdAt": "2026-09-01T00:00:00Z",
				"updatedAt": "2026-09-01T00:00:00Z"
			},
			"subscription": null
		},
		{
			"workspace": {
				"id": "ws_2",
				"name": "Guest Co",
				"slug": "guest-co",
				"createdAt": "2026-09-02T00:00:00Z",
				"updatedAt": "2026-09-02T00:00:00Z"
			},
			"membership": {
				"id": "mem_2",
				"workspaceId": "ws_2",
				"clerkUserId": "user_1",
				"role": "member",
				"displayName": null,
				"email": null,
				"createdAt": "2026-09-02T00:00:00Z",
				"updatedAt": "2026-09-02T00:00:00Z"
			},
			"subscription": null
		}
	]
}`

// meWithoutMaxWorkspacesFixture keeps the payload shape the API served
// before BILL-02 so the quota line stays opt-in.
const meWithoutMaxWorkspacesFixture = `{
	"clerkUserId": "user_1",
	"email": "dev@example.com",
	"workspaces": []
}`

// TestMeSurfacesWorkspaceQuota covers the human-readable quota line from
// issue #18: only owner-role memberships count toward maxWorkspaces.
func TestMeSurfacesWorkspaceQuota(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(meFixture))
	}))
	defer server.Close()

	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	out, err := runRoot(t, server.URL, "me")
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	for _, want := range []string{"User: dev@example.com (user_1)", "Owned workspaces: 1 of 3"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestMeJSONIncludesMaxWorkspaces verifies machine-readable output keeps the
// raw field name from the API contract.
func TestMeJSONIncludesMaxWorkspaces(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(meFixture))
	}))
	defer server.Close()

	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	out, err := runRoot(t, server.URL, "me", "--json")
	if err != nil {
		t.Fatalf("me --json: %v", err)
	}
	if !strings.Contains(out, `"maxWorkspaces": 3`) {
		t.Errorf("JSON output missing \"maxWorkspaces\": 3:\n%s", out)
	}
}

// TestMeOmitsQuotaWithoutMaxWorkspaces guards backward compatibility: when
// the API omits maxWorkspaces the CLI must not render a quota line.
func TestMeOmitsQuotaWithoutMaxWorkspaces(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(meWithoutMaxWorkspacesFixture))
	}))
	defer server.Close()

	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	out, err := runRoot(t, server.URL, "me")
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	if strings.Contains(out, "Owned workspaces") {
		t.Errorf("output renders a quota line without maxWorkspaces:\n%s", out)
	}
	if !strings.Contains(out, "User: dev@example.com (user_1)") {
		t.Errorf("output missing the user line:\n%s", out)
	}
}
