package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const createWorkspaceFixture = `{
	"workspace": {
		"id": "ws_new",
		"name": "New Co",
		"slug": "new-co",
		"createdAt": "2026-09-18T00:00:00Z",
		"updatedAt": "2026-09-18T00:00:00Z"
	},
	"membership": {
		"id": "mem_1",
		"workspaceId": "ws_new",
		"clerkUserId": "user_1",
		"role": "owner",
		"displayName": null,
		"email": null,
		"createdAt": "2026-09-18T00:00:00Z",
		"updatedAt": "2026-09-18T00:00:00Z"
	}
}`

// TestWorkspacesCreateSuccess covers the 201 path of
// POST /api/v1/console/workspaces: the confirmation lines must name the
// created workspace and suggest 'workspaces use'.
func TestWorkspacesCreateSuccess(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(createWorkspaceFixture))
	}))
	defer server.Close()

	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	out, err := runRoot(t, server.URL, "workspaces", "create", "--name", "New Co")
	if err != nil {
		t.Fatalf("workspaces create: %v", err)
	}
	if gotPath != "/api/v1/console/workspaces" {
		t.Errorf("request path = %s", gotPath)
	}
	for _, want := range []string{"Created workspace New Co (ws_new)", "workspaces use ws_new"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestWorkspacesCreateWorkspaceLimitReached covers the 402 contract from
// issue #18: a developer who already owns the cap of workspaces gets an
// explicit, actionable error — not the generic tier-limit message — and no
// success output.
func TestWorkspacesCreateWorkspaceLimitReached(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":"Workspace limit reached (maximum 3 workspaces per developer account)","code":"workspace_limit_reached"}`))
	}))
	defer server.Close()

	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	out, err := runRoot(t, server.URL, "workspaces", "create", "--name", "One Too Many")
	if err == nil {
		t.Fatalf("expected an error, got output:\n%s", out)
	}
	for _, want := range []string{
		"Workspace limit reached (maximum 3 workspaces per developer account)",
		"delete a workspace you own or transfer its ownership",
		"do not count toward the cap",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "tier limit") {
		t.Errorf("error %q still uses the generic tier-limit wording for a per-developer ownership cap", err)
	}
	if strings.Contains(out, "Created workspace") {
		t.Errorf("output reports success despite the 402:\n%s", out)
	}
}

// TestWorkspacesCreateOtherErrorsPassThrough guards the helper: only the
// workspace_limit_reached 402 is rewritten; every other API error keeps the
// server message untouched.
func TestWorkspacesCreateOtherErrorsPassThrough(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"Slug already taken"}`))
	}))
	defer server.Close()

	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	_, err := runRoot(t, server.URL, "workspaces", "create", "--name", "New Co")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "Slug already taken") {
		t.Errorf("error = %v, want the server message", err)
	}
	if strings.Contains(err.Error(), "delete a workspace you own") {
		t.Errorf("error = %v, want non-402 errors untouched by the workspace-cap guidance", err)
	}
}
