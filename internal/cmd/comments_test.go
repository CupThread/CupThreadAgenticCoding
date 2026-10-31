package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/CupThread/CupThreadAgenticCoding/internal/config"
	"github.com/CupThread/CupThreadAgenticCoding/internal/output"
	"github.com/spf13/cobra"
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
// endpoint end to end: the reference resolves through the features listing
// first (issue #417), then the thread walk hits the resolved id, the path id
// stays authoritative (no X-Workspace-Id), and the output shape is
// unchanged from the pre-resolution command.
func TestCommentsModerationList(t *testing.T) {
	server, seen := serveModerationResolution(t, featureListingJSON("fr_1"), []string{moderationCommentsFixture})
	defer server.Close()

	out, err := runModerationRoot(t, server.URL, "comments", "moderation", "list", "fr_1", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("moderation list: %v", err)
	}
	if len(*seen) != 2 {
		t.Fatalf("requests = %d, want 2 (resolution listing + thread page)", len(*seen))
	}
	resolution, thread := (*seen)[0], (*seen)[1]
	if resolution.method != http.MethodGet || resolution.path != "/api/v1/console/workspaces/ws_1/feature-requests" {
		t.Errorf("resolution request = %s %s", resolution.method, resolution.path)
	}
	if thread.method != http.MethodGet || thread.path != "/api/v1/console/workspaces/ws_1/feature-requests/fr_1/comments" {
		t.Errorf("thread request = %s %s", thread.method, thread.path)
	}
	if resolution.wsHeader != "" || thread.wsHeader != "" {
		t.Errorf("X-Workspace-Id = %q/%q, want no header (path id is authoritative)", resolution.wsHeader, thread.wsHeader)
	}
	for _, want := range []string{"Ada", "Please add dark mode", "Bob", "spam", "yes", "(2 comments)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestCommentsModerationListNotFound pins the resolution contract for a
// reference that matches nothing (issue #417): a full id or a truncated
// prefix absent from the workspace listing errors locally with the #187
// scoped message naming the scanned workspace, instead of forwarding a
// guaranteed 404 — and each failed invocation costs exactly one listing GET,
// with no thread walk.
func TestCommentsModerationListNotFound(t *testing.T) {
	server, seen := serveModerationResolution(t, featureListingJSON("fr_other_1", "fr_other_2"), []string{moderationCommentsFixture})
	defer server.Close()

	for _, ref := range []string{"fr_missing", "fr_miss_123456"} {
		_, err := runModerationRoot(t, server.URL, "comments", "moderation", "list", ref, "--workspace", "ws_1")
		if err == nil || !strings.Contains(err.Error(), `feature request "`+ref+`" not found (scanned 2 requests in workspace ws_1)`) {
			t.Fatalf("error = %v, want the scoped not-found message naming the workspace", err)
		}
	}
	if n := len(*seen); n != 2 {
		t.Fatalf("requests = %d, want one listing GET per failed resolution and no thread walk", n)
	}
}

// TestCommentsModerationListResolvesTruncatedID covers the issue #417 copy
// workflow end to end: `features list` prints the request's first 12
// characters, and `comments moderation list` now accepts that truncated form
// through the same features-listing walk `--thread` uses — the thread GET
// carries the full resolved id and the human output is unchanged.
func TestCommentsModerationListResolvesTruncatedID(t *testing.T) {
	const fullID = "frtrunc_1234_abcdef"
	server, seen := serveModerationResolution(t, featureListingJSON(fullID), []string{moderationCommentsFixture})
	defer server.Close()

	out, err := runModerationRoot(t, server.URL, "comments", "moderation", "list", fullID[:12], "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("moderation list with 12-character prefix: %v", err)
	}
	if len(*seen) != 2 {
		t.Fatalf("requests = %d, want resolution listing + thread page", len(*seen))
	}
	if q := (*seen)[0].query; !strings.Contains(q, "limit=200") || !strings.Contains(q, "offset=0") {
		t.Errorf("resolution query = %q, want the listing paging parameters", q)
	}
	if (*seen)[1].path != "/api/v1/console/workspaces/ws_1/feature-requests/"+fullID+"/comments" {
		t.Errorf("thread path = %s, want the full resolved id in the path", (*seen)[1].path)
	}
	for _, want := range []string{"Ada", "Please add dark mode", "Bob", "spam", "(2 comments)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestCommentsModerationListAmbiguousPrefixErrors pins the ambiguity
// decision at the family's entry point (issue #417): a prefix matching two
// feature requests errors locally naming the match count, and no thread GET
// is ever sent.
func TestCommentsModerationListAmbiguousPrefixErrors(t *testing.T) {
	server, seen := serveModerationResolution(t, featureListingJSON("fr_twinaaaa1111", "fr_twinbbbb2222"), []string{moderationCommentsFixture})
	defer server.Close()

	_, err := runModerationRoot(t, server.URL, "comments", "moderation", "list", "fr_twin", "--workspace", "ws_1")
	if err == nil || !strings.Contains(err.Error(), `ambiguous feature request prefix "fr_twin" matches 2 requests; use a longer prefix`) {
		t.Fatalf("error = %v, want the ambiguity error", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("requests = %d, want only the resolution listing, no thread walk", len(*seen))
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
// 404 path. Both invocations pass --yes: under `go test` stdin is
// non-interactive, so the issue #265 confirmDestructive guard refuses
// without it before any request is sent (TestCommentsModerationDeleteRefusesWithoutYes
// pins that path).
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

	out, err := runModerationRoot(t, server.URL, "comments", "moderation", "delete", "cmt_1", "--workspace", "ws_1", "--yes")
	if err != nil {
		t.Fatalf("moderation delete: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/api/v1/console/workspaces/ws_1/comments/cmt_1" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(out, "deleted") {
		t.Errorf("output missing confirmation:\n%s", out)
	}

	_, err = runModerationRoot(t, server.URL, "comments", "moderation", "delete", "cmt_gone", "--workspace", "ws_1", "--yes")
	if err == nil || !strings.Contains(err.Error(), `comment "cmt_gone" not found in workspace ws_1`) {
		t.Fatalf("error = %v, want a not-found message naming the workspace", err)
	}
}

// TestCommentsModerationDeleteRefusesWithoutYes is the issue #265 guard,
// mirrored from TestFeaturesDeleteRefusesWithoutYes: a non-interactive
// invocation without --yes fails before ANY request and the error names the
// command and the --yes escape hatch — the last hard delete now honors the
// #80 contract its six siblings already followed.
func TestCommentsModerationDeleteRefusesWithoutYes(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")
	withConfirmHooks(t, strings.NewReader(""), false)

	server, reqs := destructiveServer(t)
	_, err := runModerationRoot(t, server.URL, "comments", "moderation", "delete", "cmt_1", "--workspace", "ws_1")
	if err == nil {
		t.Fatal("comments moderation delete without --yes must fail on non-interactive stdin")
	}
	for _, want := range []string{"--yes", "comments moderation delete"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must contain %q, got: %v", want, err)
		}
	}
	if n := len(*reqs); n != 0 {
		t.Errorf("refusal must cost zero requests, saw %d", n)
	}
}

// TestCommentsModerationNotFoundSurvivesWrappedError is the issue #360 pin:
// the client already wraps selected statuses (402 tier limit, 429 rate
// limited, hinted 401/403), so a future wrap of the 404 path must not strip
// the moderation commands' workspace-naming not-found messages down to the
// raw APIError text. The client's per-request Token provider is the one seam
// that can hand Do's caller a pre-wrapped error — a Token failure is returned
// verbatim, before any wire attempt — so it delivers
// fmt.Errorf("moderation: %w", …404…) here, exactly the shape a future client
// wrap would produce. Against the former direct err.(*api.APIError)
// assertions every case fails with the wrapped raw error instead.
//
// The list command's 404 conversion now sits behind the #417 resolution, so
// its seam lets the resolution listing succeed through a real server (an
// exact match on fr_full_1) and fails only the second authenticated request —
// the thread GET — with the wrapped 404; the expected message names the
// resolved full id, not the verbatim argument.
func TestCommentsModerationNotFoundSurvivesWrappedError(t *testing.T) {
	wrapped404 := fmt.Errorf("moderation: %w", &api.APIError{
		Status:  http.StatusNotFound,
		Message: "Feature request not found in this workspace",
	})
	// failingTokenSeam fails every authenticated request with the wrapped
	// 404 — the hide/unhide/delete seam, whose first request is the mutation.
	failingTokenSeam := func(t *testing.T, a *app) {
		t.Helper()
		a.client = api.New("http://moderation.invalid")
		a.client.Token = func(context.Context) (string, error) { return "", wrapped404 }
	}
	// resolveThenFailSeam lets the first authenticated request (the #417
	// resolution listing, served by a real server) succeed and fails every
	// later one — the thread walk — with the wrapped 404.
	resolveThenFailSeam := func(t *testing.T, a *app) {
		t.Helper()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(featureListingJSON("fr_full_1")))
		}))
		t.Cleanup(server.Close)
		a.client = api.New(server.URL)
		calls := 0
		a.client.Token = func(context.Context) (string, error) {
			calls++
			if calls == 1 {
				return "cpt_test", nil
			}
			return "", wrapped404
		}
	}
	cases := []struct {
		name    string
		cmd     func() *cobra.Command
		args    []string
		wantErr string
		wire    func(t *testing.T, a *app)
	}{
		{
			name:    "moderation list",
			cmd:     newCommentsModerationListCmd,
			args:    []string{"fr_full_1"},
			wantErr: notFoundErr("feature request", "fr_full_1", "ws_1").Error(),
			wire:    resolveThenFailSeam,
		},
		{
			name:    "moderation hide",
			cmd:     func() *cobra.Command { return newCommentsModerationHideCmd(true) },
			args:    []string{"cmt_gone"},
			wantErr: notFoundErr("comment", "cmt_gone", "ws_1").Error() + commentModerationNotFoundHint,
			wire:    failingTokenSeam,
		},
		{
			name:    "moderation unhide",
			cmd:     func() *cobra.Command { return newCommentsModerationHideCmd(false) },
			args:    []string{"cmt_gone"},
			wantErr: notFoundErr("comment", "cmt_gone", "ws_1").Error() + commentModerationNotFoundHint,
			wire:    failingTokenSeam,
		},
		{
			name:    "moderation delete",
			cmd:     newCommentsModerationDeleteCmd,
			args:    []string{"cmt_gone"},
			wantErr: notFoundErr("comment", "cmt_gone", "ws_1").Error() + commentModerationNotFoundHint,
			wire:    failingTokenSeam,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldA := A
			defer func() { A = oldA }()
			oldWS := flagWorkspace
			flagWorkspace = "ws_1"
			defer func() { flagWorkspace = oldWS }()

			A = &app{
				out:     output.New(io.Discard, output.FormatTable),
				cfg:     &config.Config{},
				cfgPath: filepath.Join(t.TempDir(), "config.json"),
			}
			tc.wire(t, A)

			cmd := tc.cmd()
			// The list case's resolution listing is a real wire request, so
			// the bare command needs the Context RunE would normally receive
			// from cobra; the pre-wire Token failures never needed one.
			cmd.SetContext(context.Background())
			if cmd.Name() == "delete" {
				if err := cmd.Flags().Set("yes", "true"); err != nil {
					t.Fatalf("set --yes: %v", err)
				}
			}
			err := cmd.RunE(cmd, tc.args)
			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("error = %v, want exactly %q", err, tc.wantErr)
			}
		})
	}
}

// TestAPINotFoundToleratesWrapping pins the errors.As contract of the shared
// moderation 404 check (issue #360): a 404 delivered wrapped — today's
// 402/429/hinted-401/403 client wraps show the pattern — must still be
// recognized, while other statuses and non-API errors must not match.
func TestAPINotFoundToleratesWrapping(t *testing.T) {
	notFound := &api.APIError{Status: http.StatusNotFound, Message: "Feature request not found in this workspace"}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "unwrapped 404", err: notFound, want: true},
		{name: "wrapped once", err: fmt.Errorf("moderation: %w", notFound), want: true},
		{name: "wrapped twice", err: fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", notFound)), want: true},
		{name: "wrapped tier limit", err: fmt.Errorf("tier limit: %w", &api.APIError{Status: http.StatusPaymentRequired}), want: false},
		{name: "wrapped forbidden", err: fmt.Errorf("forbidden: %w", &api.APIError{Status: http.StatusForbidden}), want: false},
		{name: "unwrapped 500", err: &api.APIError{Status: http.StatusInternalServerError}, want: false},
		{name: "non-API error", err: errors.New("dial tcp: connection refused"), want: false},
		{name: "nil", err: nil, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := apiNotFound(tc.err); got != tc.want {
				t.Errorf("apiNotFound(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// successRecordingServer answers every request with 200 {"success":true} and
// records them — the moderation-delete counterpart of confirm_test.go's
// destructiveServer, whose bare-200 body starves the SuccessResponse decode.
func successRecordingServer(t *testing.T) (*httptest.Server, *[]*http.Request) {
	t.Helper()
	var reqs []*http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs = append(reqs, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	t.Cleanup(server.Close)
	return server, &reqs
}

// TestCommentsModerationDeleteYesSendsExactlyOneDelete mirrors
// TestChangelogDeleteYesSendsExactlyOneDelete: the moderation delete
// resolves nothing first, so --yes goes straight to a single DELETE.
func TestCommentsModerationDeleteYesSendsExactlyOneDelete(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")
	withConfirmHooks(t, strings.NewReader(""), false)

	server, reqs := successRecordingServer(t)
	out, err := runModerationRoot(t, server.URL, "comments", "moderation", "delete", "cmt_1", "--workspace", "ws_1", "--yes")
	if err != nil {
		t.Fatalf("comments moderation delete --yes: %v", err)
	}
	if !strings.Contains(out, "✓ Comment cmt_1 deleted") {
		t.Errorf("output missing confirmation line:\n%s", out)
	}
	if n := len(*reqs); n != 1 {
		t.Fatalf("want exactly one request, saw %d", n)
	}
	r := (*reqs)[0]
	if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/console/workspaces/ws_1/comments/cmt_1" {
		t.Errorf("request = %s %s", r.Method, r.URL.Path)
	}
}

// TestCommentsModerationDeleteInteractivePrompt drives the TTY branch end to
// end: the prompt names the raw comment id verbatim (nothing has been sent
// or resolved yet), "y" proceeds to exactly one DELETE, and any other answer
// aborts with zero requests.
func TestCommentsModerationDeleteInteractivePrompt(t *testing.T) {
	t.Run("y proceeds", func(t *testing.T) {
		t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")
		withConfirmHooks(t, strings.NewReader("y\n"), true)

		server, reqs := successRecordingServer(t)
		if _, err := runModerationRoot(t, server.URL, "comments", "moderation", "delete", "cmt_1", "--workspace", "ws_1"); err != nil {
			t.Fatalf("confirm y: %v", err)
		}
		if n := len(*reqs); n != 1 {
			t.Fatalf("want exactly one DELETE, saw %d", n)
		}
	})
	t.Run("n aborts", func(t *testing.T) {
		t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")
		prompt := withConfirmHooks(t, strings.NewReader("n\n"), true)

		server, reqs := destructiveServer(t)
		_, err := runModerationRoot(t, server.URL, "comments", "moderation", "delete", "cmt_1", "--workspace", "ws_1")
		if err == nil || !strings.Contains(err.Error(), "aborted") {
			t.Fatalf("want abort error, got: %v", err)
		}
		if !strings.Contains(prompt.String(), `About to permanently delete comment "cmt_1"`) ||
			!strings.Contains(prompt.String(), "Continue? [yN]") {
			t.Errorf("prompt missing or wrong on captured stderr:\n%s", prompt.String())
		}
		if n := len(*reqs); n != 0 {
			t.Errorf("abort must cost zero requests, saw %d", n)
		}
	})
}

// TestCommentsModerationDeleteJSONRefusalKeepsStdoutClean mirrors
// TestJSONRefusalKeepsStdoutPromptFree for the #265 command: the structured
// refusal is an ordinary error, so machine callers get an empty stdout and
// the full message on the error channel.
func TestCommentsModerationDeleteJSONRefusalKeepsStdoutClean(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")
	withConfirmHooks(t, strings.NewReader(""), false)

	server, reqs := destructiveServer(t)
	out, err := runModerationRoot(t, server.URL, "comments", "moderation", "delete", "cmt_1", "--workspace", "ws_1", "--json")
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("want --yes refusal, got: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("structured stdout must stay empty on refusal, got:\n%s", out)
	}
	if n := len(*reqs); n != 0 {
		t.Errorf("refusal must cost zero requests, saw %d", n)
	}
}

// TestCommentsModerationListJSON keeps machine-readable output on the raw API
// contract field names.
func TestCommentsModerationListJSON(t *testing.T) {
	server, _ := serveModerationResolution(t, featureListingJSON("fr_1"), []string{moderationCommentsFixture})
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

// servedCommentKeys is the exact per-row key set the server emits on both
// comment listings (SaaS mapCommentRow, verified against origin/main) — the
// contract the structured writer must reproduce for issue #276.
var servedCommentKeys = []string{
	"appId", "authorAvatarUrl", "authorClerkId", "authorName", "body",
	"createdAt", "featureRequestId", "id", "isHidden", "parentId",
	"replyToAuthorName", "replyToClerkId", "updatedAt",
}

// servedCommentsPage carries one anonymous row (authorClerkId null) and one
// signed-in row (the u_* public pseudonym) with every served key present.
const servedCommentsPage = `{"id": "cmt_anon_01", "featureRequestId": "fr_1", "appId": "app_1",
		"authorClerkId": null, "authorName": "Lex", "authorAvatarUrl": null,
		"body": "anonymous row", "parentId": null, "replyToClerkId": null,
		"replyToAuthorName": null, "isHidden": false,
		"createdAt": "2026-09-18T01:02:03Z", "updatedAt": "2026-09-18T01:02:03Z"}` + "," + `{"id": "cmt_user_02", "featureRequestId": "fr_1", "appId": "app_1",
		"authorClerkId": "u_4f9a2b", "authorName": "Kim", "authorAvatarUrl": null,
		"body": "signed-in row", "parentId": "cmt_anon_01", "replyToClerkId": null,
		"replyToAuthorName": "Lex", "isHidden": false,
		"createdAt": "2026-09-18T02:02:03Z", "updatedAt": "2026-09-18T02:05:03Z"}`

// TestCommentsListJSONKeepsNestedServedKeys is the issue #276 command-level
// contract: the server names appId, authorClerkId (null for an anonymous
// author, the u_* pseudonym for a signed-in one) and updatedAt on every
// comment row, and both structured listings must reproduce exactly the
// served set — nothing dropped, nothing invented — while the human table
// stays on its six columns.
func TestCommentsListJSONKeepsNestedServedKeys(t *testing.T) {
	page := commentPage(servedCommentsPage, 2, "")

	assertServedRows := func(t *testing.T, out string) {
		t.Helper()
		var resp struct {
			Comments []map[string]json.RawMessage `json:"comments"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
			t.Fatalf("unmarshal structured output: %v\n%s", err, out)
		}
		if len(resp.Comments) != 2 {
			t.Fatalf("comments = %d rows, want 2:\n%s", len(resp.Comments), out)
		}
		for i, row := range resp.Comments {
			got := make([]string, 0, len(row))
			for key := range row {
				got = append(got, key)
			}
			sort.Strings(got)
			if !reflect.DeepEqual(got, servedCommentKeys) {
				t.Errorf("comment %d key set = %v, want the served %v", i, got, servedCommentKeys)
			}
		}
		if string(resp.Comments[0]["authorClerkId"]) != "null" {
			t.Errorf("anonymous authorClerkId = %s, want the served null", resp.Comments[0]["authorClerkId"])
		}
		if string(resp.Comments[1]["authorClerkId"]) != `"u_4f9a2b"` {
			t.Errorf("signed-in authorClerkId = %s, want the pseudonym", resp.Comments[1]["authorClerkId"])
		}
		for i, row := range resp.Comments {
			if string(row["appId"]) != `"app_1"` {
				t.Errorf("comment %d appId = %s, want app_1", i, row["appId"])
			}
			if string(row["updatedAt"]) == "" || string(row["updatedAt"]) == "null" {
				t.Errorf("comment %d updatedAt = %s, want the served timestamp", i, row["updatedAt"])
			}
		}
	}

	t.Run("public listing", func(t *testing.T) {
		server, _ := serveCommentPages(t, []string{page})
		defer server.Close()

		out, err := runPublicRoot(t, server.URL, "comments", "list", "fr_1", "--json")
		if err != nil {
			t.Fatalf("comments list --json: %v", err)
		}
		assertServedRows(t, out)
	})

	t.Run("moderation listing", func(t *testing.T) {
		server, _ := serveModerationResolution(t, featureListingJSON("fr_1"), []string{page})
		defer server.Close()

		out, err := runModerationRoot(t, server.URL, "comments", "moderation", "list", "fr_1", "--workspace", "ws_1", "--json")
		if err != nil {
			t.Fatalf("comments moderation list --json: %v", err)
		}
		assertServedRows(t, out)
	})

	t.Run("human table unchanged", func(t *testing.T) {
		server, _ := serveCommentPages(t, []string{page})
		defer server.Close()

		out, err := runPublicRoot(t, server.URL, "comments", "list", "fr_1")
		if err != nil {
			t.Fatalf("comments list: %v", err)
		}
		for _, want := range []string{"Lex", "anonymous row", "Kim", "signed-in row", "(2 comments)"} {
			if !strings.Contains(out, want) {
				t.Errorf("human output missing %q:\n%s", want, out)
			}
		}
		for _, leaked := range []string{"appId", "authorClerkId", "updatedAt", "u_4f9a2b"} {
			if strings.Contains(out, leaked) {
				t.Errorf("human output leaks machine key %q:\n%s", leaked, out)
			}
		}
	})
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
	for _, want := range []string{
		"not available",
		"may not exist",
		"may not be approved yet",
		// Issue #417: the public board table prints the truncated form, and
		// this credential-free path cannot resolve prefixes client-side, so
		// the 404 must name the ID shape as a likely cause.
		"must be the full form",
		"truncate to 12 characters",
		"--json",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "never existed") {
		t.Errorf("error = %q, must not imply the request never existed", err)
	}
}

// TestCommentsListNotFoundSurvivesWrappedError extends the issue #360 pin to
// the public listing: its 404 branch carries the PRIV-12 "not available"
// message, and a future client wrap of the 404 path must not strip that down
// to the raw APIError text either. The command builds its client through the
// publicClient factory seam, so the test swaps it for one whose Token
// provider returns fmt.Errorf("portal: %w", …404…) — DoWithHeaders hands a
// Token failure back verbatim, before any wire attempt, exactly the shape a
// future client wrap would produce. Against the former direct
// err.(*api.APIError) assertion this case fails with the wrapped raw error
// instead of the not-available message, so a revert of only the public
// caller cannot escape the suite.
func TestCommentsListNotFoundSurvivesWrappedError(t *testing.T) {
	wrapped404 := fmt.Errorf("portal: %w", &api.APIError{
		Status:  http.StatusNotFound,
		Message: "Feature request not found",
	})

	oldA := A
	defer func() { A = oldA }()
	oldFactory := publicClient
	publicClient = func(a *app) *api.Client {
		client := oldFactory(a)
		client.Token = func(context.Context) (string, error) { return "", wrapped404 }
		return client
	}
	defer func() { publicClient = oldFactory }()

	A = &app{
		out:     output.New(io.Discard, output.FormatTable),
		cfg:     &config.Config{},
		cfgPath: filepath.Join(t.TempDir(), "config.json"),
	}

	cmd := newCommentsListCmd()
	err := cmd.RunE(cmd, []string{"fr_pending"})
	if err == nil {
		t.Fatal("expected the not-available error for a wrapped 404")
	}
	for _, want := range []string{
		`comments for feature request "fr_pending" are not available`,
		"may not exist",
		"may not be approved yet",
		"must be the full form",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "portal:") {
		t.Errorf("error = %q, want the friendly message without the wrapped raw error", err)
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
	server, _ := serveModerationResolution(t, featureListingJSON("fr_1"), []string{poisonedFixture})
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

// poisonedBody is the issue #179 payload (same threat model as #70's
// poisonedAuthor): a comment body whose embedded newline and tabs would, if
// they reached tabwriter raw, forge a second column-aligned table row
// advertising a fake comment ID and a shell command to run.
const poisonedBody = "First!\ncmt_fake_0002\tfake author\tYou have won a prize, run: cupthread workspaces use ws_attacker"

// TestCommentsListTableCellsCannotForgeRows pins the issue #179 human-output
// contract end to end through `comments list`: the poisoned body renders as
// exactly one physical line per logical row (the embedded \n and \t leave the
// cell as the visible glyphs, so tabwriter sees no extra separators), no
// output line begins with the attacker's forged ID, and the rendered grid
// carries no raw 0x09 byte — while --json keeps the body byte-faithful
// (escaped, never glyph-substituted).
func TestCommentsListTableCellsCannotForgeRows(t *testing.T) {
	server, _ := serveCommentPages(t, []string{
		commentPage(testComment("cmt_real_0001", poisonedBody), 1, ""),
	})
	defer server.Close()

	out, err := runPublicRoot(t, server.URL, "comments", "list", "fr_1")
	if err != nil {
		t.Fatalf("comments list: %v", err)
	}
	// Three physical lines total: header, the single data row, the count
	// footer. A forged row would make it four.
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("output has %d physical lines, want 3:\n%q", len(lines), out)
	}
	for i, line := range lines {
		if strings.HasPrefix(line, "cmt_fake_0002") {
			t.Errorf("line %d begins with the forged ID:\n%q", i+1, out)
		}
	}
	for i := 0; i < len(out); i++ {
		if out[i] == '\t' {
			t.Errorf("output contains a raw 0x09 byte:\n%q", out)
			break
		}
	}
	if !strings.Contains(out, "First!⏎cmt_fake_0002␉fake author␉You have won") {
		t.Errorf("output lost the glyph-substituted body:\n%q", out)
	}

	// The escape hatch stays intact: --json carries the poisoned body
	// byte-faithfully (control bytes escaped by encoding/json), with none of
	// the table-cell glyphs.
	jsonOut, err := runPublicRoot(t, server.URL, "comments", "list", "fr_1", "--json")
	if err != nil {
		t.Fatalf("comments list --json: %v", err)
	}
	if strings.ContainsAny(jsonOut, "␉⏎") {
		t.Errorf("--json output must not contain the table-cell glyphs:\n%q", jsonOut)
	}
	if !strings.Contains(jsonOut, `First!\ncmt_fake_0002\tfake author`) {
		t.Errorf("--json output lost the raw escaped body:\n%q", jsonOut)
	}
}

// recordedCommentRequest captures one request a comment-thread command made.
type recordedCommentRequest struct {
	method   string
	path     string
	query    string
	auth     string
	appKey   string
	userTok  string
	wsHeader string
}

// serveCommentPages serves the given JSON bodies one per request in order
// (the last one repeats forever) and records every request.
func serveCommentPages(t *testing.T, pages []string) (*httptest.Server, *[]recordedCommentRequest) {
	t.Helper()
	var seen []recordedCommentRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, recordedCommentRequest{
			method:   r.Method,
			path:     r.URL.Path,
			query:    r.URL.RawQuery,
			auth:     r.Header.Get("Authorization"),
			appKey:   r.Header.Get("X-App-Key"),
			userTok:  r.Header.Get("X-User-Token"),
			wsHeader: r.Header.Get("X-Workspace-Id"),
		})
		body := pages[len(pages)-1]
		if n := len(seen); n <= len(pages) {
			body = pages[n-1]
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	return server, &seen
}

// featureListingJSON builds one feature-request listing page for ID
// resolution — the GET .../feature-requests walk fetchOneFeatureRequest (and
// commentThreadRef on top of it) pages through — carrying exactly the given
// ids, which are also the full universe a single small page ends.
func featureListingJSON(ids ...string) string {
	reqs := make([]string, 0, len(ids))
	for _, id := range ids {
		reqs = append(reqs, fmt.Sprintf(`{"id":%q,"appId":"app_a","title":"Request %s","status":"open","voteCount":0,"createdAt":"2026-09-01T12:00:00.000Z","updatedAt":"2026-09-01T12:00:00.000Z"}`, id, id))
	}
	return fmt.Sprintf(`{"requests":[%s],"total":%d}`, strings.Join(reqs, ","), len(reqs))
}

// serveModerationResolution serves the two hops `comments moderation list`
// now walks (issue #417): the feature-request listing that resolves the
// reference first, then the thread pages the command always walked.
// listingJSON answers every listing GET; pages are served to the thread walk
// one per request in order, the last repeating. Every request is recorded,
// listing first, so tests can pin the exact wire sequence.
func serveModerationResolution(t *testing.T, listingJSON string, pages []string) (*httptest.Server, *[]recordedCommentRequest) {
	t.Helper()
	var seen []recordedCommentRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, recordedCommentRequest{
			method:   r.Method,
			path:     r.URL.Path,
			query:    r.URL.RawQuery,
			auth:     r.Header.Get("Authorization"),
			appKey:   r.Header.Get("X-App-Key"),
			userTok:  r.Header.Get("X-User-Token"),
			wsHeader: r.Header.Get("X-Workspace-Id"),
		})
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/feature-requests") {
			_, _ = w.Write([]byte(listingJSON))
			return
		}
		idx := len(seen) - 2 // thread request 1 = seen[1] → pages[0]
		if idx >= len(pages) {
			idx = len(pages) - 1
		}
		_, _ = w.Write([]byte(pages[idx]))
	}))
	t.Cleanup(server.Close)
	return server, &seen
}

// runPublicRoot also executes the CLI with a test bearer token: the public
// thread command must ignore credentials entirely (issue #182) — the token
// proves no caller identity leaks onto the public GET.
func runPublicRoot(t *testing.T, serverURL string, args ...string) (string, error) {
	t.Helper()
	return runModerationRoot(t, serverURL, args...)
}

func testComment(id, body string) string {
	return fmt.Sprintf(`{"id": %q, "featureRequestId": "fr_1", "authorName": "A %s", "body": %q, "isHidden": false, "createdAt": "2026-09-18T01:02:03Z"}`,
		id, id, body)
}

// commentPage builds one keyset-paginated thread page (PROD-31); an empty
// nextCursor means the last page.
func commentPage(commentsJSON string, total int, nextCursor string) string {
	hasMore := "false"
	cursor := "null"
	if nextCursor != "" {
		hasMore = "true"
		cursor = `"` + nextCursor + `"`
	}
	return fmt.Sprintf(`{"comments": [%s], "total": %d, "hasMore": %s, "nextCursor": %s}`,
		commentsJSON, total, hasMore, cursor)
}

// TestCommentsListWalksAllPages covers the public thread listing following
// nextCursor to the end: the first request keeps the historic empty query,
// the second echoes the cursor back, and the identity headers ride on every
// page. The trailing count is the server's authoritative total.
func TestCommentsListWalksAllPages(t *testing.T) {
	server, seen := serveCommentPages(t, []string{
		commentPage(testComment("cmt_a1", "first page one"), 3, "cur_2"),
		commentPage(testComment("cmt_b2", "second page two")+","+testComment("cmt_c3", "second page three"), 3, ""),
	})
	defer server.Close()

	out, err := runPublicRoot(t, server.URL, "comments", "list", "fr_1", "--app-key", "key_live_x", "--user-token", "usr_1")
	if err != nil {
		t.Fatalf("comments list: %v", err)
	}
	if len(*seen) != 2 {
		t.Fatalf("requests = %d, want 2 pages", len(*seen))
	}
	first, second := (*seen)[0], (*seen)[1]
	if first.query != "" {
		t.Errorf("page 1 query = %q, want empty (no cursor on the first page)", first.query)
	}
	if !strings.Contains(second.query, "cursor=cur_2") {
		t.Errorf("page 2 query = %q, want the echoed cursor", second.query)
	}
	for i, req := range *seen {
		if req.appKey != "key_live_x" || req.userTok != "usr_1" {
			t.Errorf("page %d headers X-App-Key=%q X-User-Token=%q, want both identity headers forwarded", i+1, req.appKey, req.userTok)
		}
	}
	for _, want := range []string{"first page one", "second page two", "second page three", "(3 comments)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestCommentsListWorksLoggedOut covers issue #182: with no stored credential
// and no $CUPTHREAD_TOKEN, the public thread GET must go through — the
// command previously failed client-side with "not logged in" before any
// request left the process — and the request must carry no Authorization
// header.
func TestCommentsListWorksLoggedOut(t *testing.T) {
	server, seen := serveCommentPages(t, []string{
		commentPage(testComment("cmt_a1", "logged out one"), 1, ""),
	})
	defer server.Close()

	out, err := runRoot(t, server.URL, "comments", "list", "fr_1")
	if err != nil {
		t.Fatalf("comments list logged out: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("requests = %d, want 1", len(*seen))
	}
	if (*seen)[0].auth != "" {
		t.Errorf("Authorization header = %q, want none on the public thread GET", (*seen)[0].auth)
	}
	if !strings.Contains(out, "logged out one") {
		t.Errorf("output missing comment body:\n%s", out)
	}
}

// TestCommentsListLoggedOutForwardsIdentityHeaders pins that the optional
// SDK-style caller identity (--app-key/--user-token) still rides on the
// credential-free client while the console bearer stays off the wire.
func TestCommentsListLoggedOutForwardsIdentityHeaders(t *testing.T) {
	server, seen := serveCommentPages(t, []string{
		commentPage(testComment("cmt_a1", "one"), 1, ""),
	})
	defer server.Close()

	if _, err := runRoot(t, server.URL, "comments", "list", "fr_1",
		"--app-key", "key_live_x", "--user-token", "usr_1"); err != nil {
		t.Fatalf("comments list logged out with identity headers: %v", err)
	}
	req := (*seen)[0]
	if req.appKey != "key_live_x" || req.userTok != "usr_1" {
		t.Errorf("X-App-Key=%q X-User-Token=%q, want both forwarded", req.appKey, req.userTok)
	}
	if req.auth != "" {
		t.Errorf("Authorization header = %q, want none", req.auth)
	}
}

// TestCommentsListNeverSendsBearerWhenLoggedIn is the issue #182 guard: the
// public thread GET rides a client with no Token provider, so even a fully
// logged-in invocation must not put the cpt_ bearer on the wire (on
// anonymous boards it buys nothing, and it cannot satisfy a sign-in-only
// board either). A refactor that re-attaches the authenticated client sends
// the env token and fails here.
func TestCommentsListNeverSendsBearerWhenLoggedIn(t *testing.T) {
	server, seen := serveCommentPages(t, []string{
		commentPage(testComment("cmt_a1", "one"), 1, ""),
	})
	defer server.Close()

	if _, err := runModerationRoot(t, server.URL, "comments", "list", "fr_1"); err != nil {
		t.Fatalf("comments list with a credential: %v", err)
	}
	if (*seen)[0].auth != "" {
		t.Errorf("Authorization header = %q, want the public GET to stay bearer-free even when logged in", (*seen)[0].auth)
	}
}

// TestCommentsListCountsByTotalNotPageLength pins the issue's core fix: the
// human count line reports the server's total (a comment posted between page
// fetches makes total exceed the aggregated rows), not len(comments).
func TestCommentsListCountsByTotalNotPageLength(t *testing.T) {
	server, seen := serveCommentPages(t, []string{
		commentPage(testComment("cmt_a1", "one"), 4, "cur_2"),
		commentPage(testComment("cmt_b2", "two")+","+testComment("cmt_c3", "three"), 4, ""),
	})
	defer server.Close()

	out, err := runPublicRoot(t, server.URL, "comments", "list", "fr_1")
	if err != nil {
		t.Fatalf("comments list: %v", err)
	}
	if len(*seen) != 2 {
		t.Fatalf("requests = %d, want 2 pages", len(*seen))
	}
	if !strings.Contains(out, "(4 comments)") {
		t.Errorf("count line must show the server total 4, got:\n%s", out)
	}
}

// TestCommentsListOldShapeSinglePage verifies a pre-pagination response
// (comments array only, no total/nextCursor) still renders: one request, and
// the count falls back to len(comments).
func TestCommentsListOldShapeSinglePage(t *testing.T) {
	server, seen := serveCommentPages(t, []string{
		`{"comments": [` + testComment("cmt_a1", "legacy one") + `,` + testComment("cmt_b2", "legacy two") + `]}`,
	})
	defer server.Close()

	out, err := runPublicRoot(t, server.URL, "comments", "list", "fr_1")
	if err != nil {
		t.Fatalf("comments list: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("requests = %d, want a single page for an old-shape response", len(*seen))
	}
	if !strings.Contains(out, "(2 comments)") {
		t.Errorf("output missing legacy count:\n%s", out)
	}
}

// TestCommentsListJSONAggregatesPages checks that --json emits the whole
// walked thread with the final page's paging metadata (hasMore false,
// nextCursor null) rather than just the first wire page.
func TestCommentsListJSONAggregatesPages(t *testing.T) {
	server, _ := serveCommentPages(t, []string{
		commentPage(testComment("cmt_a1", "json one"), 3, "cur_2"),
		commentPage(testComment("cmt_b2", "json two")+","+testComment("cmt_c3", "json three"), 3, ""),
	})
	defer server.Close()

	out, err := runPublicRoot(t, server.URL, "comments", "list", "fr_1", "--json")
	if err != nil {
		t.Fatalf("comments list --json: %v", err)
	}
	for _, want := range []string{`"id": "cmt_a1"`, `"id": "cmt_b2"`, `"id": "cmt_c3"`, `"total": 3`, `"hasMore": false`, `"nextCursor": null`} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON output missing %q:\n%s", want, out)
		}
	}
}

// TestCommentsModerationListWalksPages covers the Console moderation listing
// walking the same keyset pagination, with a hidden comment on the second
// page still rendered and the resolution listing riding ahead of the walk.
func TestCommentsModerationListWalksPages(t *testing.T) {
	hidden := `{"id": "cmt_hidden_9", "featureRequestId": "fr_1", "authorName": "Eve", "body": "spam message", "isHidden": true, "createdAt": "2026-09-18T09:09:09Z"}`
	server, seen := serveModerationResolution(t, featureListingJSON("fr_1"), []string{
		commentPage(testComment("cmt_a1", "visible one"), 3, "cur_2"),
		commentPage(testComment("cmt_b2", "visible two")+","+hidden, 3, ""),
	})
	defer server.Close()

	out, err := runModerationRoot(t, server.URL, "comments", "moderation", "list", "fr_1", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("moderation list: %v", err)
	}
	if len(*seen) != 3 {
		t.Fatalf("requests = %d, want resolution listing + 2 thread pages", len(*seen))
	}
	for i, req := range (*seen)[1:] {
		if req.path != "/api/v1/console/workspaces/ws_1/feature-requests/fr_1/comments" {
			t.Errorf("thread page %d path = %s", i+1, req.path)
		}
	}
	if !strings.Contains((*seen)[2].query, "cursor=cur_2") {
		t.Errorf("page 2 query = %q, want the echoed cursor", (*seen)[2].query)
	}
	for _, want := range []string{"visible one", "visible two", "spam message", "yes", "(3 comments)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestCommentsListPageCapGuard verifies the runaway-paging guard: a server
// that never reports a last page stops the CLI after maxCommentPages pages
// with an error naming the cap, instead of paging forever.
func TestCommentsListPageCapGuard(t *testing.T) {
	var n int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		body := fmt.Sprintf(`{"comments": [%s], "total": 100000, "hasMore": true, "nextCursor": "cur_%d"}`,
			testComment(fmt.Sprintf("cmt_%d", n), "endless"), n)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	_, err := runPublicRoot(t, server.URL, "comments", "list", "fr_1")
	if err == nil || !strings.Contains(err.Error(), "did not end within 50 pages") {
		t.Fatalf("error = %v, want the page-cap message", err)
	}
	if n != maxCommentPages {
		t.Errorf("requests = %d, want exactly maxCommentPages (%d)", n, maxCommentPages)
	}
}

// TestCommentsCreateFailsFastLocally pins the issue #181 core contract: the
// comment POST requires a Clerk end-user session no CLI credential can
// supply, so `comments create` must fail locally with the web-portal
// guidance and ZERO HTTP requests — with no credential, with a stored cpt_
// credential, and with an env token alike, in both output modes, and with
// empty stdout (the failure goes to the error channel, never the
// machine-readable stream).
func TestCommentsCreateFailsFastLocally(t *testing.T) {
	storedTokenCfg := `{"auth":{"method":"token","accessToken":"cpt_pat1234567890","tokenPrefix":"cpt_pat123"}}`
	cases := []struct {
		name string
		env  bool   // set $CUPTHREAD_TOKEN instead of a stored credential
		cfg  string // stored-credential config seed; "" = no credential at all
		json bool
	}{
		{name: "no credential"},
		{name: "stored cpt_ credential", cfg: storedTokenCfg},
		{name: "env token", env: true},
		{name: "stored credential, --json", cfg: storedTokenCfg, json: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env {
				t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
			}
			var requests int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()

			args := []string{"comments", "create", "fr_1", "--body", "hello"}
			if tc.json {
				args = append(args, "--json")
			}
			var out string
			var err error
			if tc.cfg == "" {
				out, err = runRoot(t, server.URL, args...)
			} else {
				out, err = runRootWithSeededConfig(t, server.URL, tc.cfg, args...)
			}
			if err == nil {
				t.Fatal("expected the web-portal error, got success")
			}
			for _, want := range []string{
				"posting comments requires a signed-in end-user session",
				"CupThread web portal",
				"no CLI credential",
				"nothing was sent",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to contain %q", err, want)
				}
			}
			if requests != 0 {
				t.Errorf("requests = %d, want 0 (the command must fail before any round-trip)", requests)
			}
			if strings.TrimSpace(out) != "" {
				t.Errorf("stdout = %q, want empty (a failed command prints nothing to the output stream)", out)
			}
		})
	}
}

// TestCommentsCreateDeadFlagsRejected pins the issue #181 flag cleanup: the
// author-identity and reply flags the server schema strips (PRIV-04, SEC-50)
// and the identity headers the route never reads no longer exist, so cobra
// rejects them as unknown flags before any credential is even consulted.
func TestCommentsCreateDeadFlagsRejected(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	for _, flag := range []string{"author-name", "author-email", "author-avatar-url", "reply-to", "app-key", "user-token"} {
		t.Run(flag, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()

			_, err := runPublicRoot(t, server.URL, "comments", "create", "fr_1", "--body", "hi", "--"+flag, "x")
			if err == nil || !strings.Contains(err.Error(), "unknown flag: --"+flag) {
				t.Errorf("error = %v, want unknown-flag rejection of --%s", err, flag)
			}
		})
	}
}

// threadResolutionServer wires the three hops the --thread resolution walks
// (issue #413): the feature-request listing (resolving the --thread
// reference, itself prefix-capable), the thread's comment pages, and the
// moderation mutation, whose method and path land in *gotMethod/*gotPath.
func threadResolutionServer(t *testing.T, commentsJSON string, gotMethod, gotPath *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/feature-requests"):
			_, _ = w.Write([]byte(`{"requests":[{"id":"fr_full_1","appId":"app_a","title":"Thread request","status":"open","voteCount":0,"createdAt":"2026-09-01T12:00:00.000Z","updatedAt":"2026-09-01T12:00:00.000Z"}],"total":1}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/comments"):
			_, _ = w.Write([]byte(commentsJSON))
		default:
			if gotMethod != nil {
				*gotMethod = r.Method
			}
			if gotPath != nil {
				*gotPath = r.URL.Path
			}
			_, _ = w.Write([]byte(`{"success":true}`))
		}
	}))
}

// TestCommentsModerationThreadResolvesPrefix covers the issue #413 copy
// workflow end to end: the moderation table shows cmt_visible's first 12
// characters, `comments moderation list` ran under fr_full_1, and hide
// accepts both truncated forms — the --thread flag resolving through the
// features listing (itself prefix-capable) and the thread walk resolving the
// comment — so the PATCH carries the full resolved comment id.
func TestCommentsModerationThreadResolvesPrefix(t *testing.T) {
	var gotMethod, gotPath string
	server := threadResolutionServer(t, moderationCommentsFixture, &gotMethod, &gotPath)
	defer server.Close()

	out, err := runModerationRoot(t, server.URL,
		"comments", "moderation", "hide", "cmt_visible_", "--thread", "fr_full_1", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("moderation hide with --thread prefix: %v", err)
	}
	if gotMethod != http.MethodPatch || gotPath != "/api/v1/console/workspaces/ws_1/comments/cmt_visible_1234/hide" {
		t.Errorf("request = %s %s, want the PATCH on the full resolved comment id", gotMethod, gotPath)
	}
	if !strings.Contains(out, "✓ Comment cmt_visible_1234 hidden") {
		t.Errorf("success line must carry the resolved full id:\n%s", out)
	}

	// The --thread reference itself accepts the features table's truncated
	// form: the same workflow with a 12-character request prefix resolves.
	gotMethod, gotPath = "", ""
	out, err = runModerationRoot(t, server.URL,
		"comments", "moderation", "unhide", "cmt_hidden_5", "--thread", "fr_full_1", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("moderation unhide with --thread: %v", err)
	}
	if gotPath != "/api/v1/console/workspaces/ws_1/comments/cmt_hidden_5678/hide" {
		t.Errorf("request = %s %s, want the unhide PATCH on the full resolved id", gotMethod, gotPath)
	}
	if !strings.Contains(out, "✓ Comment cmt_hidden_5678 unhidden") {
		t.Errorf("success line must carry the resolved full id:\n%s", out)
	}
}

// TestCommentsModerationAmbiguousPrefixInThreadErrors pins the ambiguity
// decision inside one thread: a prefix matching two comments errors locally
// naming both matches' count, and no PATCH is ever sent.
func TestCommentsModerationAmbiguousPrefixInThreadErrors(t *testing.T) {
	ambiguous := `{"comments":[
		{"id":"cmt_twinaaa9999","featureRequestId":"fr_full_1","authorName":"A","body":"first","isHidden":false,"createdAt":"2026-09-18T01:02:03Z"},
		{"id":"cmt_twinbbbb0000","featureRequestId":"fr_full_1","authorName":"B","body":"second","isHidden":false,"createdAt":"2026-09-18T04:05:06Z"}
	]}`
	var gotMethod, gotPath string
	server := threadResolutionServer(t, ambiguous, &gotMethod, &gotPath)
	defer server.Close()

	_, err := runModerationRoot(t, server.URL,
		"comments", "moderation", "hide", "cmt_twin", "--thread", "fr_full_1", "--workspace", "ws_1")
	if err == nil || !strings.Contains(err.Error(), `ambiguous comment prefix "cmt_twin" matches 2 comments; use a longer prefix`) {
		t.Fatalf("error = %v, want the ambiguity error", err)
	}
	if gotMethod != "" {
		t.Errorf("mutation %s %s fired on an ambiguous prefix, want none", gotMethod, gotPath)
	}
}

// TestCommentsModerationTruncatedRefDirect404NamesRemedy pins the #413
// self-diagnosis: without --thread a truncated table id still 404s (bare
// prefixes are unresolvable workspace-wide), but the error names both
// remedies — the full id from the --json listing, or --thread — instead of
// reading as a missing comment.
func TestCommentsModerationTruncatedRefDirect404NamesRemedy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Comment not found in this workspace"}`))
	}))
	defer server.Close()

	_, err := runModerationRoot(t, server.URL, "comments", "moderation", "hide", "cmt_visible_1", "--workspace", "ws_1")
	if err == nil {
		t.Fatal("truncated ref without --thread must fail")
	}
	for _, want := range []string{
		`comment "cmt_visible_1" not found in workspace ws_1`,
		"full comment id",
		"comments moderation list <feature-request-id> --json",
		"--thread",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("404 hint missing %q:\n%v", want, err)
		}
	}
}

// TestCommentsModerationPrefixRefusedWhenThreadWalkCaps pins the PR #414
// review hardening: the cursor walk has no server-side end, so when the
// maxCommentPages guard stops the scan early a prefix that matches within
// the scanned pages must still be refused — a later page could hold a
// second match, and a hide/delete must never act on an unverifiable choice.
// Exact ids keep their early return, so a full id past the cap still works.
func TestCommentsModerationPrefixRefusedWhenThreadWalkCaps(t *testing.T) {
	filler := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		filler = append(filler, fmt.Sprintf(`{"id":"cmt_other_%04d_extra","featureRequestId":"fr_full_1","authorName":"A","body":"b","isHidden":false,"createdAt":"2026-09-18T01:02:03Z"}`, i))
	}
	oneMatch := append([]string{`{"id":"cmt_prefix_match_0042_extra","featureRequestId":"fr_full_1","authorName":"A","body":"b","isHidden":false,"createdAt":"2026-09-18T01:02:03Z"}`}, filler[1:]...)
	fullPage := func(comments []string) string {
		// Every page is full and carries a nextCursor: the walk always
		// stops at the guard, never at exhaustion.
		return `{"comments":[` + strings.Join(comments, ",") + `],"total":100000,"nextCursor":"cursor-never-dies"}`
	}

	var mutations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/feature-requests"):
			_, _ = w.Write([]byte(`{"requests":[{"id":"fr_full_1","appId":"app_a","title":"Thread request","status":"open","voteCount":0,"createdAt":"2026-09-01T12:00:00.000Z","updatedAt":"2026-09-01T12:00:00.000Z"}],"total":1}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/comments"):
			// The matching comment lives on page 1 only; every later page
			// (cursor set) is filler, so the scan ends holding exactly one
			// prefix match it can never prove unique.
			if r.URL.Query().Get("cursor") == "" {
				_, _ = w.Write([]byte(fullPage(oneMatch)))
				return
			}
			_, _ = w.Write([]byte(fullPage(filler)))
		default:
			mutations = append(mutations, r.Method+" "+r.URL.Path)
			_, _ = w.Write([]byte(`{"success":true}`))
		}
	}))
	defer server.Close()

	if _, err := runModerationRoot(t, server.URL,
		"comments", "moderation", "hide", "cmt_prefix_match_0042", "--thread", "fr_full_1", "--workspace", "ws_1"); err == nil || !strings.Contains(err.Error(), "matches within the first 10000 comments of request fr_full_1") || !strings.Contains(err.Error(), "pass the full comment id") {
		t.Fatalf("error = %v, want the cap-stopped prefix refusal", err)
	}
	if len(mutations) != 0 {
		t.Errorf("mutations = %v, want none on a cap-stopped prefix scan", mutations)
	}
	mutations = nil

	// An exact id is verifiable on the page that holds it: it returns on
	// page 1 without needing the walk to finish, so it still hides.
	out, err := runModerationRoot(t, server.URL,
		"comments", "moderation", "hide", "cmt_prefix_match_0042_extra", "--thread", "fr_full_1", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("moderation hide with exact id: %v", err)
	}
	if !strings.Contains(out, "✓ Comment cmt_prefix_match_0042_extra hidden") {
		t.Errorf("exact id must hide on the page that holds it without walking on:\n%s", out)
	}
	if want := []string{"PATCH /api/v1/console/workspaces/ws_1/comments/cmt_prefix_match_0042_extra/hide"}; !reflect.DeepEqual(mutations, want) {
		t.Errorf("mutations = %v, want exactly %v", mutations, want)
	}
}
