package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Tests for the admin write pre-flight validators (issue #341): every
// flag value the shared zod intake bounds can never accept fails locally
// with the offending flag named and its count in the message, and no HTTP
// request — not even an ID-resolution GET — is issued; in-range values
// still build byte-identical requests.

// writeProbe records every request a test server receives so validation
// tests can assert a rejected write issued none, and capture the wire
// request of an accepted one.
type writeProbe struct {
	requests int
	method   string
	path     string
	body     []byte
}

// serveWriteProbe answers GETs with getBody (pass nil when the command
// under test never GETs) and non-GETs with a minimal JSON object.
func serveWriteProbe(t *testing.T, probe *writeProbe, getBody []byte) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probe.requests++
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write(getBody)
			return
		}
		probe.method = r.Method
		probe.path = r.URL.Path
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		probe.body = b
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	return server
}

// runLocalRejectCases runs each case against a probe server and pins the
// issue #341 contract: the error names the offending flag and no HTTP
// request is issued.
func runLocalRejectCases(t *testing.T, cases []struct {
	name    string
	args    []string
	wantErr string
}) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
			var probe writeProbe
			server := serveWriteProbe(t, &probe, nil)
			_, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
			if probe.requests != 0 {
				t.Errorf("issued %d HTTP request(s), want 0 — must fail before any round trip", probe.requests)
			}
		})
	}
}

func TestFeaturesCreateValidatesBoundsLocally(t *testing.T) {
	runLocalRejectCases(t, []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"short title", []string{"features", "create", "--title", "ab", "--description", "long enough"}, "invalid --title: must be 3-160 characters, got 2"},
		{"long title", []string{"features", "create", "--title", strings.Repeat("x", 161), "--description", "long enough"}, "invalid --title: must be 3-160 characters, got 161"},
		{"long CJK title counts runes", []string{"features", "create", "--title", strings.Repeat("汉", 161), "--description", "long enough"}, "invalid --title: must be 3-160 characters, got 161"},
		{"short description", []string{"features", "create", "--title", "Valid title", "--description", "abcd"}, "invalid --description: must be 5-5000 characters, got 4"},
		{"long description", []string{"features", "create", "--title", "Valid title", "--description", strings.Repeat("x", 5001)}, "invalid --description: must be 5-5000 characters, got 5001"},
		{"long column slug", []string{"features", "create", "--title", "Valid title", "--description", "long enough", "--column-slug", strings.Repeat("s", 61)}, "invalid --column-slug: must be 1-60 characters, got 61"},
	})
}

func TestFeaturesCreateInRangeStillPosts(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	var probe writeProbe
	server := serveWriteProbe(t, &probe, nil)
	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig,
		"features", "create", "--title", strings.Repeat("汉", 160), "--description", "long enough", "--column-slug", "ideas"); err != nil {
		t.Fatalf("features create: %v", err)
	}
	if probe.requests != 1 || probe.method != http.MethodPost || !strings.HasSuffix(probe.path, "/feature-requests") {
		t.Fatalf("wire = %d request(s), %s %s, want one POST to /feature-requests", probe.requests, probe.method, probe.path)
	}
	var got map[string]any
	if err := json.Unmarshal(probe.body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got["appId"] != "app_a" || got["columnSlug"] != "ideas" {
		t.Errorf("body appId/columnSlug = %v/%v, want app_a/ideas", got["appId"], got["columnSlug"])
	}
	if n, _ := got["title"].(string); len([]rune(n)) != 160 {
		t.Errorf("title = %d runes, want the full 160-rune CJK title (rune, not byte, counting)", len([]rune(n)))
	}
	if _, ok := got["versionId"]; ok {
		t.Errorf("body carries versionId %v, want the key omitted", got["versionId"])
	}
}

func TestFeaturesUpdateValidatesBoundsLocally(t *testing.T) {
	runLocalRejectCases(t, []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"empty title", []string{"features", "update", "fr_a_1", "--title", ""}, "invalid --title: must be 3-160 characters, got 0"},
		{"empty description", []string{"features", "update", "fr_a_1", "--description", ""}, "invalid --description: must be 5-5000 characters, got 0"},
		{"long title", []string{"features", "update", "fr_a_1", "--title", strings.Repeat("x", 161)}, "invalid --title: must be 3-160 characters, got 161"},
		// columnSlug is not nullable in the schema, so the empty string has
		// no satisfiable wire form and is rejected with that explanation.
		{"empty column slug cannot clear", []string{"features", "update", "fr_a_1", "--column-slug", ""}, "no clear-the-column form"},
		{"long column slug", []string{"features", "update", "fr_a_1", "--column-slug", strings.Repeat("s", 61)}, "invalid --column-slug: must be 1-60 characters, got 61"},
	})
}

func TestFeaturesUpdateInRangeStillPuts(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	var probe writeProbe
	server := serveWriteProbe(t, &probe, filteredFixture(t, "app_a"))
	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig,
		"features", "update", "fr_a_1", "--title", "Renamed request"); err != nil {
		t.Fatalf("features update: %v", err)
	}
	if probe.requests != 2 || probe.method != http.MethodPut {
		t.Fatalf("wire = %d request(s), last %s, want resolution GET then PUT", probe.requests, probe.method)
	}
	if string(probe.body) != `{"title":"Renamed request"}` {
		t.Errorf("PUT body = %s, want the title-only body unchanged", probe.body)
	}
}

func TestChangelogCreateValidatesBoundsLocally(t *testing.T) {
	runLocalRejectCases(t, []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"short title", []string{"changelog", "create", "--title", "ab"}, "invalid --title: must be 3-160 characters, got 2"},
		{"long title", []string{"changelog", "create", "--title", strings.Repeat("x", 161)}, "invalid --title: must be 3-160 characters, got 161"},
		{"long version label", []string{"changelog", "create", "--title", "Valid title", "--version-label", strings.Repeat("v", 65)}, "invalid --version-label: must be 1-64 characters, got 65"},
		{"body over the server bound", []string{"changelog", "create", "--title", "Valid title", "--body", strings.Repeat("a", changelogBodyLimit+1)}, "the changelog body limit is 20000 characters, got 20001"},
		{"too many linked requests", []string{"changelog", "create", "--title", "Valid title", "--link-request-ids", strings.TrimSuffix(strings.Repeat("fr_,", 51), ",")}, "invalid --link-request-ids: at most 50 request IDs, got 51"},
		{"stray comma in linked requests", []string{"changelog", "create", "--title", "Valid title", "--link-request-ids", "fr_1,,fr_2"}, "request IDs cannot be empty"},
	})
}

func TestChangelogCreateBodyBoundaryAccepted(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	var probe writeProbe
	server := serveWriteProbe(t, &probe, nil)
	atLimit := strings.Repeat("a", changelogBodyLimit)
	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig,
		"changelog", "create", "--title", "Release 1.2.0", "--body", atLimit, "--version-label", "1.2.0"); err != nil {
		t.Fatalf("changelog create at the body limit: %v", err)
	}
	if probe.requests != 1 || probe.method != http.MethodPost {
		t.Fatalf("wire = %d request(s), %s, want one POST", probe.requests, probe.method)
	}
	var got map[string]any
	if err := json.Unmarshal(probe.body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body, _ := got["body"].(string); len(body) != changelogBodyLimit {
		t.Errorf("body length = %d, want the at-limit %d sent through", len(body), changelogBodyLimit)
	}
	if got["versionLabel"] != "1.2.0" {
		t.Errorf("versionLabel = %v, want 1.2.0", got["versionLabel"])
	}
}

func TestChangelogUpdateValidatesBoundsLocally(t *testing.T) {
	runLocalRejectCases(t, []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"short title", []string{"changelog", "update", "cl_1", "--title", "ab"}, "invalid --title: must be 3-160 characters, got 2"},
		{"body over the server bound", []string{"changelog", "update", "cl_1", "--body", strings.Repeat("a", changelogBodyLimit+1)}, "the changelog body limit is 20000 characters, got 20001"},
		{"long version label", []string{"changelog", "update", "cl_1", "--version-label", strings.Repeat("v", 65)}, "invalid --version-label: must be 1-64 characters, got 65"},
	})
}

func TestChangelogUpdateInRangeStillPuts(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	var probe writeProbe
	server := serveWriteProbe(t, &probe, []byte(changelogListingJSON("cl_1")))
	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig,
		"changelog", "update", "cl_1", "--title", "Renamed entry", "--version-label", ""); err != nil {
		t.Fatalf("changelog update: %v", err)
	}
	if probe.requests != 2 || probe.method != http.MethodPut {
		t.Fatalf("wire = %d request(s), %s, want the resolution GET plus one PUT", probe.requests, probe.method)
	}
	var got map[string]any
	if err := json.Unmarshal(probe.body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got["title"] != "Renamed entry" {
		t.Errorf("title = %v, want Renamed entry", got["title"])
	}
	// "" on update remains the documented clear form (sent as null).
	label, ok := got["versionLabel"]
	if !ok || label != nil {
		t.Errorf("versionLabel = %v (present: %v), want an explicit null clear", label, ok)
	}
}

func TestColumnsCreateValidatesBoundsLocally(t *testing.T) {
	runLocalRejectCases(t, []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"long name", []string{"columns", "create", "--name", strings.Repeat("n", 61)}, "invalid --name: must be 1-60 characters, got 61"},
		{"long slug", []string{"columns", "create", "--name", "Valid name", "--slug", strings.Repeat("s", 61)}, "invalid --slug: must be 1-60 characters, got 61"},
		{"slug with bad characters", []string{"columns", "create", "--name", "Valid name", "--slug", "Bad Slug"}, "may only contain a-z, 0-9, _ and -"},
		{"unknown kind", []string{"columns", "create", "--name", "Valid name", "--kind", "bogus"}, `invalid --kind "bogus": must be one of pending_review, normal, done`},
		{"empty kind", []string{"columns", "create", "--name", "Valid name", "--kind", ""}, `invalid --kind "": must be one of pending_review, normal, done`},
		{"3-digit color", []string{"columns", "create", "--name", "Valid name", "--color", "#fff"}, `invalid --color "#fff": must be a #rrggbb hex color`},
		{"5-digit color", []string{"columns", "create", "--name", "Valid name", "--color", "#4f46e"}, `invalid --color "#4f46e": must be a #rrggbb hex color`},
	})
}

func TestColumnsCreateInRangeStillPosts(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	var probe writeProbe
	server := serveWriteProbe(t, &probe, nil)
	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig,
		"columns", "create", "--name", "In review", "--kind", "pending_review", "--color", "#4f46e5", "--slug", "in_review"); err != nil {
		t.Fatalf("columns create: %v", err)
	}
	if probe.requests != 1 || probe.method != http.MethodPost || !strings.HasSuffix(probe.path, "/columns") {
		t.Fatalf("wire = %d request(s), %s %s, want one POST to /columns", probe.requests, probe.method, probe.path)
	}
	var got map[string]any
	if err := json.Unmarshal(probe.body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got["kind"] != "pending_review" || got["color"] != "#4f46e5" || got["slug"] != "in_review" || got["isVisible"] != true {
		t.Errorf("body = %v, want kind/color/slug/isVisible passed through", got)
	}
}

func TestColumnsUpdateValidatesBoundsLocally(t *testing.T) {
	runLocalRejectCases(t, []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"empty name", []string{"columns", "update", "col_1", "--name", ""}, "invalid --name: must be 1-60 characters, got 0"},
		{"negative position", []string{"columns", "update", "col_1", "--position", "-1"}, "invalid --position: must be 0 or greater, got -1"},
		{"short color", []string{"columns", "update", "col_1", "--color", "#fff"}, `invalid --color "#fff": must be a #rrggbb hex color`},
		{"empty color", []string{"columns", "update", "col_1", "--color", ""}, `invalid --color "": must be a #rrggbb hex color`},
	})
}

func TestColumnsUpdateInRangeStillPuts(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	var probe writeProbe
	server := serveWriteProbe(t, &probe, nil)
	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig,
		"columns", "update", "col_1", "--name", "New name", "--position", "3"); err != nil {
		t.Fatalf("columns update: %v", err)
	}
	if probe.requests != 1 || probe.method != http.MethodPut {
		t.Fatalf("wire = %d request(s), %s, want one PUT", probe.requests, probe.method)
	}
	if string(probe.body) != `{"name":"New name","position":3}` {
		t.Errorf("PUT body = %s, want the name/position body unchanged", probe.body)
	}
}

func TestVersionsCreateValidatesBoundsLocally(t *testing.T) {
	runLocalRejectCases(t, []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"long label", []string{"versions", "create", "--label", strings.Repeat("v", 65)}, "invalid --label: must be 1-64 characters, got 65"},
		{"long description", []string{"versions", "create", "--label", "1.2.0", "--description", strings.Repeat("d", 2001)}, "invalid --description: must be at most 2000 characters, got 2001"},
	})
}

func TestVersionsCreateInRangeStillPosts(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	var probe writeProbe
	server := serveWriteProbe(t, &probe, nil)
	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig,
		"versions", "create", "--label", "1.2.0", "--description", "Short notes"); err != nil {
		t.Fatalf("versions create: %v", err)
	}
	if probe.requests != 1 || probe.method != http.MethodPost || !strings.HasSuffix(probe.path, "/versions") {
		t.Fatalf("wire = %d request(s), %s %s, want one POST to /versions", probe.requests, probe.method, probe.path)
	}
	var got map[string]any
	if err := json.Unmarshal(probe.body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got["label"] != "1.2.0" || got["description"] != "Short notes" {
		t.Errorf("body label/description = %v/%v, want 1.2.0/Short notes", got["label"], got["description"])
	}
}

func TestVersionsUpdateValidatesBoundsLocally(t *testing.T) {
	runLocalRejectCases(t, []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"empty label", []string{"versions", "update", "ver_1", "--label", ""}, "invalid --label: must be 1-64 characters, got 0"},
		{"long label", []string{"versions", "update", "ver_1", "--label", strings.Repeat("v", 65)}, "invalid --label: must be 1-64 characters, got 65"},
		{"long description", []string{"versions", "update", "ver_1", "--description", strings.Repeat("d", 2001)}, "invalid --description: must be at most 2000 characters, got 2001"},
		{"negative position", []string{"versions", "update", "ver_1", "--position", "-1"}, "invalid --position: must be 0 or greater, got -1"},
	})
}

func TestVersionsUpdateInRangeStillPuts(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	var probe writeProbe
	server := serveWriteProbe(t, &probe, nil)
	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig,
		"versions", "update", "ver_1", "--label", "2.0.0", "--description", ""); err != nil {
		t.Fatalf("versions update: %v", err)
	}
	if probe.requests != 1 || probe.method != http.MethodPut {
		t.Fatalf("wire = %d request(s), %s, want one PUT", probe.requests, probe.method)
	}
	var got map[string]any
	if err := json.Unmarshal(probe.body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got["label"] != "2.0.0" {
		t.Errorf("label = %v, want 2.0.0", got["label"])
	}
	// description remains nullable: "" still clears it with an explicit null.
	if desc, ok := got["description"]; !ok || desc != nil {
		t.Errorf("description = %v (present: %v), want an explicit null clear", desc, ok)
	}
}

// --- Issue #345: the remaining write surfaces pre-flight the same zod
// intake bounds locally — imports create/rerun, workspaces create, the
// workspaces members family, integrations github config, and features
// forward — with the identical contract: guaranteed-400 flag values fail
// in-process naming the flag, no HTTP request is issued, and accepted
// values keep the wire bodies byte-identical.

func TestImportsCreateValidatesBoundsLocally(t *testing.T) {
	runLocalRejectCases(t, []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"unknown source", []string{"imports", "create", "--source", "gists"}, `invalid --source "gists": must be one of github_issues, github_discussions, linear, notion, slack`},
		{"bad state", []string{"imports", "create", "--source", "github_issues", "--state", "archive"}, `invalid --state "archive": must be open, closed or all`},
		{"limit zero", []string{"imports", "create", "--source", "github_issues", "--limit", "0"}, "invalid --limit: must be 1-200, got 0"},
		{"limit over max", []string{"imports", "create", "--source", "github_issues", "--limit", "201"}, "invalid --limit: must be 1-200, got 201"},
		{"long owner", []string{"imports", "create", "--source", "github_issues", "--owner", strings.Repeat("o", 101)}, "invalid --owner: must be 1-100 characters, got 101"},
		{"long repo", []string{"imports", "create", "--source", "github_issues", "--repo", strings.Repeat("r", 101)}, "invalid --repo: must be 1-100 characters, got 101"},
		{"empty label entry", []string{"imports", "create", "--source", "github_issues", "--labels", "a,,b"}, "invalid --labels: each label must be 1-60 characters, got 0"},
		{"long label", []string{"imports", "create", "--source", "github_issues", "--labels", strings.Repeat("l", 61)}, "invalid --labels: each label must be 1-60 characters, got 61"},
		{"too many labels", []string{"imports", "create", "--source", "github_issues", "--labels", strings.TrimSuffix(strings.Repeat("a,", 11), ",")}, "invalid --labels: at most 10 labels, got 11"},
		{"long category slug", []string{"imports", "create", "--source", "github_discussions", "--category-slug", strings.Repeat("c", 101)}, "invalid --category-slug: must be 1-100 characters, got 101"},
		{"long column slug", []string{"imports", "create", "--source", "github_issues", "--column-slug", strings.Repeat("s", 61)}, "invalid --column-slug: must be 1-60 characters, got 61"},
	})
}

func TestImportsCreateInRangeStillPosts(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	var probe writeProbe
	server := serveWriteProbe(t, &probe, nil)
	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig,
		"imports", "create", "--source", "github_issues", "--owner", "acme", "--repo", "ios",
		"--labels", "bug,priority high", "--state", "open", "--limit", "200",
		"--category-slug", "requests", "--column-slug", "triage"); err != nil {
		t.Fatalf("imports create: %v", err)
	}
	if probe.requests != 1 || probe.method != http.MethodPost || !strings.HasSuffix(probe.path, "/imports") {
		t.Fatalf("wire = %d request(s), %s %s, want one POST to /imports", probe.requests, probe.method, probe.path)
	}
	var got struct {
		Source  string         `json:"source"`
		Mode    string         `json:"mode"`
		Options map[string]any `json:"options"`
	}
	if err := json.Unmarshal(probe.body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Source != "github_issues" || got.Mode != "preview" {
		t.Errorf("source/mode = %q/%q, want github_issues/preview", got.Source, got.Mode)
	}
	if got.Options["owner"] != "acme" || got.Options["state"] != "open" || got.Options["categorySlug"] != "requests" || got.Options["columnSlug"] != "triage" {
		t.Errorf("options = %v, want owner/state/categorySlug/columnSlug passed through", got.Options)
	}
	if n, _ := got.Options["limit"].(float64); n != 200 {
		t.Errorf("options.limit = %v, want 200", got.Options["limit"])
	}
	labels, _ := got.Options["labels"].([]any)
	if len(labels) != 2 || labels[0] != "bug" || labels[1] != "priority high" {
		t.Errorf("options.labels = %v, want [bug priority high]", got.Options["labels"])
	}
	for _, key := range []string{"teamId", "databaseId", "channelId", "includeDuplicates"} {
		if _, ok := got.Options[key]; ok {
			t.Errorf("options carries %s, want the key omitted", key)
		}
	}
}

func TestImportsRerunValidatesModeLocally(t *testing.T) {
	runLocalRejectCases(t, []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"empty mode", []string{"imports", "rerun", "imp_1", "--mode", ""}, `invalid --mode "": use preview or commit`},
		{"bogus mode", []string{"imports", "rerun", "imp_1", "--mode", "fast"}, `invalid --mode "fast": use preview or commit`},
	})
}

func TestImportsRerunInRangeStillPosts(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	var probe writeProbe
	server := serveWriteProbe(t, &probe, []byte(`{"job":{"id":"imp_1","source":"linear","mode":"preview","status":"queued","attempts":0,"createdAt":"2026-10-01T00:00:00Z","updatedAt":"2026-10-01T00:00:00Z"}}`))
	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig,
		"imports", "rerun", "imp_1", "--mode", "commit"); err != nil {
		t.Fatalf("imports rerun: %v", err)
	}
	if probe.requests != 2 || probe.method != http.MethodPost || !strings.HasSuffix(probe.path, "/imports/imp_1/rerun") {
		t.Fatalf("wire = %d request(s), %s %s, want the resolution GET plus one POST to /imports/imp_1/rerun", probe.requests, probe.method, probe.path)
	}
	if string(probe.body) != `{"mode":"commit"}` {
		t.Errorf("body = %s, want the mode-only body unchanged", probe.body)
	}
}

func TestWorkspacesCreateValidatesBoundsLocally(t *testing.T) {
	runLocalRejectCases(t, []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"short name", []string{"workspaces", "create", "--name", "a"}, "invalid --name: must be 2-120 characters, got 1"},
		{"long name", []string{"workspaces", "create", "--name", strings.Repeat("n", 121)}, "invalid --name: must be 2-120 characters, got 121"},
		{"long CJK name counts runes", []string{"workspaces", "create", "--name", strings.Repeat("汉", 121)}, "invalid --name: must be 2-120 characters, got 121"},
		{"long slug", []string{"workspaces", "create", "--name", "Valid name", "--slug", strings.Repeat("s", 65)}, "must match [a-z0-9-] with 2-64 characters"},
	})
}

func TestWorkspacesCreateInRangeStillPosts(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	var probe writeProbe
	server := serveWriteProbe(t, &probe, nil)
	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig,
		"workspaces", "create", "--name", strings.Repeat("汉", 120), "--slug", "cjk-workspace"); err != nil {
		t.Fatalf("workspaces create: %v", err)
	}
	if probe.requests != 1 || probe.method != http.MethodPost || !strings.HasSuffix(probe.path, "/api/v1/console/workspaces") {
		t.Fatalf("wire = %d request(s), %s %s, want one POST to /api/v1/console/workspaces", probe.requests, probe.method, probe.path)
	}
	var got map[string]any
	if err := json.Unmarshal(probe.body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if n, _ := got["name"].(string); len([]rune(n)) != 120 {
		t.Errorf("name = %d runes, want the full 120-rune CJK name (rune, not byte, counting)", len([]rune(n)))
	}
	if got["slug"] != "cjk-workspace" {
		t.Errorf("slug = %v, want cjk-workspace", got["slug"])
	}
}

func TestMembersInviteValidatesBoundsLocally(t *testing.T) {
	runLocalRejectCases(t, []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"bad email", []string{"workspaces", "members", "invite", "--email", "notanemail"}, `invalid --email "notanemail": must be an email address`},
		{"owner role is reserved", []string{"workspaces", "members", "invite", "--email", "user@example.com", "--role", "owner"}, `invalid --role "owner": must be admin or member`},
		{"unknown role", []string{"workspaces", "members", "invite", "--email", "user@example.com", "--role", "superadmin"}, `invalid --role "superadmin": must be admin or member`},
		{"empty role", []string{"workspaces", "members", "invite", "--email", "user@example.com", "--role", ""}, `invalid --role "": must be admin or member`},
		{"long display name", []string{"workspaces", "members", "invite", "--email", "user@example.com", "--display-name", strings.Repeat("n", 121)}, "invalid --display-name: must be at most 120 characters, got 121"},
	})
}

func TestMembersInviteInRangeStillPosts(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	var probe writeProbe
	server := serveWriteProbe(t, &probe, nil)
	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig,
		"workspaces", "members", "invite", "--email", "user@example.com", "--role", "admin", "--display-name", "Us Er"); err != nil {
		t.Fatalf("members invite: %v", err)
	}
	if probe.requests != 1 || probe.method != http.MethodPost || !strings.HasSuffix(probe.path, "/workspaces/ws_1/members") {
		t.Fatalf("wire = %d request(s), %s %s, want one POST to /workspaces/ws_1/members", probe.requests, probe.method, probe.path)
	}
	var got map[string]any
	if err := json.Unmarshal(probe.body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got["email"] != "user@example.com" || got["role"] != "admin" || got["displayName"] != "Us Er" {
		t.Errorf("body = %v, want email/role/displayName passed through", got)
	}
}

func TestMembersAddValidatesBoundsLocally(t *testing.T) {
	runLocalRejectCases(t, []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"long clerk user id", []string{"workspaces", "members", "add", "--clerk-user-id", strings.Repeat("u", 256)}, "invalid --clerk-user-id: must be at most 255 characters, got 256"},
		{"owner role is reserved", []string{"workspaces", "members", "add", "--clerk-user-id", "user_1", "--role", "owner"}, `invalid --role "owner": must be admin or member`},
	})
}

func TestMembersSetRoleValidatesRoleLocally(t *testing.T) {
	runLocalRejectCases(t, []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"empty role passes required", []string{"workspaces", "members", "set-role", "m_1", "--role", ""}, `invalid --role "": must be admin or member`},
		{"owner role is reserved", []string{"workspaces", "members", "set-role", "m_1", "--role", "owner"}, `invalid --role "owner": must be admin or member`},
		{"unknown role", []string{"workspaces", "members", "set-role", "m_1", "--role", "contributor"}, `invalid --role "contributor": must be admin or member`},
	})
}

func TestMembersSetRoleInRangeStillPuts(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	var probe writeProbe
	server := serveWriteProbe(t, &probe, nil)
	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig,
		"workspaces", "members", "set-role", "m_1", "--role", "admin"); err != nil {
		t.Fatalf("members set-role: %v", err)
	}
	if probe.requests != 1 || probe.method != http.MethodPut || !strings.HasSuffix(probe.path, "/workspaces/ws_1/members/m_1") {
		t.Fatalf("wire = %d request(s), %s %s, want one PUT to /workspaces/ws_1/members/m_1", probe.requests, probe.method, probe.path)
	}
	if string(probe.body) != `{"role":"admin"}` {
		t.Errorf("body = %s, want the role-only body unchanged", probe.body)
	}
}

func TestGitHubConfigValidatesOwnerRepoLocally(t *testing.T) {
	runLocalRejectCases(t, []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"long owner", []string{"integrations", "github", "config", "app_1", "--owner", strings.Repeat("o", 101)}, "invalid --owner: must be 1-100 characters, got 101"},
		{"long repo", []string{"integrations", "github", "config", "app_1", "--repo", strings.Repeat("r", 101)}, "invalid --repo: must be 1-100 characters, got 101"},
	})
}

func TestGitHubConfigEmptyOwnerStillClears(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	var probe writeProbe
	server := serveWriteProbe(t, &probe, []byte(appListFixture))
	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig,
		"integrations", "github", "config", "app_1", "--owner", ""); err != nil {
		t.Fatalf("integrations github config: %v", err)
	}
	if probe.requests != 2 || probe.method != http.MethodPatch {
		t.Fatalf("wire = %d request(s), last %s, want app lookup GET then PATCH", probe.requests, probe.method)
	}
	if !strings.HasSuffix(probe.path, "/apps/app_1/github") {
		t.Errorf("PATCH path = %s, want /apps/app_1/github", probe.path)
	}
	if string(probe.body) != `{"githubOwner":null}` {
		t.Errorf("body = %s, want the explicit null clear preserved", probe.body)
	}
}

func TestFeaturesForwardValidatesLabelsLocally(t *testing.T) {
	runLocalRejectCases(t, []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"empty label entry", []string{"features", "forward", "fr_a_1", "--labels", "a,,b"}, "invalid --labels: each label must be 1-60 characters, got 0"},
		{"long label", []string{"features", "forward", "fr_a_1", "--labels", strings.Repeat("l", 61)}, "invalid --labels: each label must be 1-60 characters, got 61"},
	})
}

func TestFeaturesForwardInRangeStillPosts(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	var forwardBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/github/forward") {
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read forward body: %v", err)
			}
			if err := json.Unmarshal(raw, &forwardBody); err != nil {
				t.Errorf("decode forward body %q: %v", raw, err)
			}
			_, _ = w.Write([]byte(`{"success":true,"targetType":"discussion","url":"https://github.com/acme/ios/discussions/9"}`))
			return
		}
		_, _ = w.Write(filteredFixture(t, r.URL.Query().Get("appId")))
	}))
	t.Cleanup(server.Close)
	// The 60-rune boundary is accepted and passes through rune-intact; the
	// reject table above pins 61 as a local error.
	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig,
		"features", "forward", "fr_a_1", "--labels", strings.Repeat("汉", 60)); err != nil {
		t.Fatalf("features forward: %v", err)
	}
	labels, _ := forwardBody["labels"].([]any)
	if len(labels) != 1 {
		t.Fatalf("labels = %v, want the single boundary label", forwardBody["labels"])
	}
	if n, _ := labels[0].(string); len([]rune(n)) != 60 {
		t.Errorf("label = %d runes, want the full 60-rune boundary label", len([]rune(n)))
	}
}

// --- Issue #349: --schedule-at is pre-flighted against zod's default
// strict-UTC datetime shape on both changelog create and update. Date-only,
// free-text, and numeric-offset values — plus impossible calendar datetimes
// the shape regex alone would accept — fail locally naming the flag with
// zero HTTP requests, while whole- and fractional-second UTC values keep
// the wire bodies byte-identical and the "" clear still sends the explicit
// null.

func TestChangelogCreateValidatesScheduleAtLocally(t *testing.T) {
	runLocalRejectCases(t, []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"free text", []string{"changelog", "create", "--title", "Release 2.0", "--schedule-at", "tomorrow"}, `invalid --schedule-at "tomorrow": must be a UTC ISO 8601 datetime (e.g. 2026-11-05T09:00:00Z)`},
		{"date only", []string{"changelog", "create", "--title", "Release 2.0", "--schedule-at", "2026-11-05"}, `invalid --schedule-at "2026-11-05": must be a UTC ISO 8601 datetime (e.g. 2026-11-05T09:00:00Z)`},
		{"numeric offset", []string{"changelog", "create", "--title", "Release 2.0", "--schedule-at", "2026-11-05T09:00:00+02:00"}, `invalid --schedule-at "2026-11-05T09:00:00+02:00": must be a UTC ISO 8601 datetime (e.g. 2026-11-05T09:00:00Z)`},
		{"prose datetime", []string{"changelog", "create", "--title", "Release 2.0", "--schedule-at", "Nov 5, 2026 9am"}, `invalid --schedule-at "Nov 5, 2026 9am": must be a UTC ISO 8601 datetime (e.g. 2026-11-05T09:00:00Z)`},
		{"no timezone marker", []string{"changelog", "create", "--title", "Release 2.0", "--schedule-at", "2026-11-05T09:00:00"}, `invalid --schedule-at "2026-11-05T09:00:00": must be a UTC ISO 8601 datetime (e.g. 2026-11-05T09:00:00Z)`},
		{"space separator", []string{"changelog", "create", "--title", "Release 2.0", "--schedule-at", "2026-11-05 09:00:00Z"}, `invalid --schedule-at "2026-11-05 09:00:00Z": must be a UTC ISO 8601 datetime (e.g. 2026-11-05T09:00:00Z)`},
		{"lowercase t and z", []string{"changelog", "create", "--title", "Release 2.0", "--schedule-at", "2026-11-05t09:00:00z"}, `invalid --schedule-at "2026-11-05t09:00:00z": must be a UTC ISO 8601 datetime (e.g. 2026-11-05T09:00:00Z)`},
		{"impossible month passes the shape", []string{"changelog", "create", "--title", "Release 2.0", "--schedule-at", "2026-13-05T09:00:00Z"}, `invalid --schedule-at "2026-13-05T09:00:00Z": must be a real calendar datetime`},
		{"impossible day passes the shape", []string{"changelog", "create", "--title", "Release 2.0", "--schedule-at", "2026-02-30T09:00:00Z"}, `invalid --schedule-at "2026-02-30T09:00:00Z": must be a real calendar datetime`},
		{"leap second passes the shape", []string{"changelog", "create", "--title", "Release 2.0", "--schedule-at", "2016-12-31T23:59:60Z"}, `invalid --schedule-at "2016-12-31T23:59:60Z": must be a real calendar datetime`},
	})
}

func TestChangelogCreateScheduleAtInRangeStillPosts(t *testing.T) {
	for name, value := range map[string]string{
		"whole seconds":      "2026-11-05T09:00:00Z",
		"fractional seconds": "2026-11-05T09:00:00.123Z",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
			var probe writeProbe
			server := serveWriteProbe(t, &probe, nil)
			if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig,
				"changelog", "create", "--title", "Release 2.0", "--schedule-at", value); err != nil {
				t.Fatalf("changelog create: %v", err)
			}
			if probe.requests != 1 || probe.method != http.MethodPost || !strings.HasSuffix(probe.path, "/changelog") {
				t.Fatalf("wire = %d request(s), %s %s, want one POST to /changelog", probe.requests, probe.method, probe.path)
			}
			var got map[string]any
			if err := json.Unmarshal(probe.body, &got); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if got["scheduledAt"] != value {
				t.Errorf("scheduledAt = %v, want the verbatim %q", got["scheduledAt"], value)
			}
		})
	}
}

func TestChangelogUpdateValidatesScheduleAtLocally(t *testing.T) {
	runLocalRejectCases(t, []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"date only", []string{"changelog", "update", "cl_1", "--schedule-at", "2026-11-05"}, `invalid --schedule-at "2026-11-05": must be a UTC ISO 8601 datetime (e.g. 2026-11-05T09:00:00Z)`},
		{"numeric offset", []string{"changelog", "update", "cl_1", "--schedule-at", "2026-11-05T09:00:00+02:00"}, `invalid --schedule-at "2026-11-05T09:00:00+02:00": must be a UTC ISO 8601 datetime (e.g. 2026-11-05T09:00:00Z)`},
		{"prose datetime", []string{"changelog", "update", "cl_1", "--schedule-at", "Nov 5, 2026 9am"}, `invalid --schedule-at "Nov 5, 2026 9am": must be a UTC ISO 8601 datetime (e.g. 2026-11-05T09:00:00Z)`},
	})
}

func TestChangelogUpdateScheduleAtInRangeStillPuts(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	var probe writeProbe
	server := serveWriteProbe(t, &probe, []byte(changelogListingJSON("cl_1")))
	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig,
		"changelog", "update", "cl_1", "--schedule-at", "2026-11-05T09:00:00.123Z"); err != nil {
		t.Fatalf("changelog update: %v", err)
	}
	if probe.requests != 2 || probe.method != http.MethodPut {
		t.Fatalf("wire = %d request(s), %s, want the resolution GET plus one PUT", probe.requests, probe.method)
	}
	var got map[string]any
	if err := json.Unmarshal(probe.body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got["scheduledAt"] != "2026-11-05T09:00:00.123Z" {
		t.Errorf("scheduledAt = %v, want the fractional-seconds value verbatim", got["scheduledAt"])
	}
}

func TestChangelogUpdateScheduleAtEmptyStillClears(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	var probe writeProbe
	server := serveWriteProbe(t, &probe, []byte(changelogListingJSON("cl_1")))
	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig,
		"changelog", "update", "cl_1", "--schedule-at", ""); err != nil {
		t.Fatalf("changelog update: %v", err)
	}
	if probe.requests != 2 || probe.method != http.MethodPut {
		t.Fatalf("wire = %d request(s), %s, want the resolution GET plus one PUT", probe.requests, probe.method)
	}
	var got map[string]any
	if err := json.Unmarshal(probe.body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if v, ok := got["scheduledAt"]; !ok || v != nil {
		t.Errorf("scheduledAt = %v (present: %v), want an explicit null clear", v, ok)
	}
}

// --- Issue #348: apps create --store-url and the apps update URL flags are
// pre-flighted against the server's https-only display-URL policy
// (isAllowedHttpsUrl). http, ftp, javascript, scheme-relative, userinfo,
// and non-URL values — plus, on create, URLs over the schema's 2048
// characters — fail locally naming the flag with zero HTTP requests, while
// accepted https URLs keep the wire bodies byte-identical and the update
// "" clear still sends the explicit null.

func TestAppsCreateValidatesStoreURLLocally(t *testing.T) {
	runLocalRejectCases(t, []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"http scheme", []string{"apps", "create", "--name", "My App", "--store-url", "http://example.com/app"}, `invalid --store-url "http://example.com/app": must be an absolute https URL (e.g. https://apps.apple.com/app/id123)`},
		{"javascript scheme", []string{"apps", "create", "--name", "My App", "--store-url", "javascript://evil.com/x"}, `invalid --store-url "javascript://evil.com/x": must be an absolute https URL (e.g. https://apps.apple.com/app/id123)`},
		{"ftp scheme", []string{"apps", "create", "--name", "My App", "--store-url", "ftp://files.example.com/app"}, `invalid --store-url "ftp://files.example.com/app": must be an absolute https URL (e.g. https://apps.apple.com/app/id123)`},
		{"scheme relative", []string{"apps", "create", "--name", "My App", "--store-url", "//example.com/app"}, `invalid --store-url "//example.com/app": must be an absolute https URL (e.g. https://apps.apple.com/app/id123)`},
		{"not a url", []string{"apps", "create", "--name", "My App", "--store-url", "not a url"}, `invalid --store-url "not a url": must be an absolute https URL (e.g. https://apps.apple.com/app/id123)`},
		{"empty host", []string{"apps", "create", "--name", "My App", "--store-url", "https://"}, `invalid --store-url "https://": must be an absolute https URL (e.g. https://apps.apple.com/app/id123)`},
		{"port without host", []string{"apps", "create", "--name", "My App", "--store-url", "https://:443/"}, `invalid --store-url "https://:443/": must be an absolute https URL (e.g. https://apps.apple.com/app/id123)`},
		{"port above 16 bits", []string{"apps", "create", "--name", "My App", "--store-url", "https://example.com:70000/"}, `invalid --store-url "https://example.com:70000/": must be an absolute https URL (e.g. https://apps.apple.com/app/id123)`},
		{"userinfo", []string{"apps", "create", "--name", "My App", "--store-url", "https://user:pass@example.com/app"}, `invalid --store-url "https://user:pass@example.com/app": must be an absolute https URL (e.g. https://apps.apple.com/app/id123)`},
		{"password without username", []string{"apps", "create", "--name", "My App", "--store-url", "https://:secret@example.com/app"}, `invalid --store-url "https://:secret@example.com/app": must be an absolute https URL (e.g. https://apps.apple.com/app/id123)`},
		{"over the 2048-character cap", []string{"apps", "create", "--name", "My App", "--store-url", "https://example.com/" + strings.Repeat("a", maxDisplayURLLen)}, fmt.Sprintf("invalid --store-url: must be at most 2048 characters, got %d", len("https://example.com/")+maxDisplayURLLen)},
	})
}

func TestAppsCreateStoreURLInRangeStillPosts(t *testing.T) {
	for name, value := range map[string]string{
		"plain store url":           "https://apps.apple.com/app/id123",
		"uppercase scheme is https": "HTTPS://Example.com/App",
		"at the 16-bit port cap":    "https://example.com:65535/app",
		"at the 2048-char boundary": "https://example.com/" + strings.Repeat("a", maxDisplayURLLen-len("https://example.com/")),
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
			var probe writeProbe
			server := serveWriteProbe(t, &probe, nil)
			if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig,
				"apps", "create", "--name", "My App", "--store-url", value); err != nil {
				t.Fatalf("apps create: %v", err)
			}
			if probe.requests != 1 || probe.method != http.MethodPost || !strings.HasSuffix(probe.path, "/apps") {
				t.Fatalf("wire = %d request(s), %s %s, want one POST to /apps", probe.requests, probe.method, probe.path)
			}
			var got map[string]any
			if err := json.Unmarshal(probe.body, &got); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if got["storeUrl"] != value {
				t.Errorf("storeUrl = %v, want the verbatim %q", got["storeUrl"], value)
			}
		})
	}
}

func TestAppsUpdateValidatesURLFlagsLocally(t *testing.T) {
	runLocalRejectCases(t, []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"http store url", []string{"apps", "update", "app_1", "--store-url", "http://example.com/app"}, `invalid --store-url "http://example.com/app": must be an absolute https URL (e.g. https://apps.apple.com/app/id123)`},
		{"ftp app store url", []string{"apps", "update", "app_1", "--app-store-url", "ftp://files.example.com/app"}, `invalid --app-store-url "ftp://files.example.com/app": must be an absolute https URL (e.g. https://apps.apple.com/app/id123)`},
		{"javascript google play url", []string{"apps", "update", "app_1", "--google-play-url", "javascript://evil.com/x"}, `invalid --google-play-url "javascript://evil.com/x": must be an absolute https URL (e.g. https://apps.apple.com/app/id123)`},
		{"userinfo store url", []string{"apps", "update", "app_1", "--store-url", "https://user:pass@example.com/app"}, `invalid --store-url "https://user:pass@example.com/app": must be an absolute https URL (e.g. https://apps.apple.com/app/id123)`},
		{"not a url", []string{"apps", "update", "app_1", "--store-url", "not a url"}, `invalid --store-url "not a url": must be an absolute https URL (e.g. https://apps.apple.com/app/id123)`},
		{"port above 16 bits", []string{"apps", "update", "app_1", "--app-store-url", "https://apps.apple.com:70000/app"}, `invalid --app-store-url "https://apps.apple.com:70000/app": must be an absolute https URL (e.g. https://apps.apple.com/app/id123)`},
	})
}

func TestAppsUpdateURLsInRangeStillPuts(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	var probe writeProbe
	server := serveWriteProbe(t, &probe, []byte(appListFixture))
	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig,
		"apps", "update", "app_1",
		"--store-url", "https://example.com/app",
		"--app-store-url", "https://apps.apple.com/app/id123",
		"--google-play-url", "https://play.google.com/store/apps/details?id=com.example"); err != nil {
		t.Fatalf("apps update: %v", err)
	}
	if probe.requests != 2 || probe.method != http.MethodPut {
		t.Fatalf("wire = %d request(s), last %s, want app lookup GET then PUT", probe.requests, probe.method)
	}
	if !strings.HasSuffix(probe.path, "/apps/app_1") {
		t.Errorf("PUT path = %s, want /apps/app_1", probe.path)
	}
	var got map[string]any
	if err := json.Unmarshal(probe.body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	want := map[string]string{
		"storeUrl":      "https://example.com/app",
		"appStoreUrl":   "https://apps.apple.com/app/id123",
		"googlePlayUrl": "https://play.google.com/store/apps/details?id=com.example",
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %v, want the verbatim %q", key, got[key], value)
		}
	}
}

func TestAppsUpdateURLEmptyStillClears(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	var probe writeProbe
	server := serveWriteProbe(t, &probe, []byte(appListFixture))
	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig,
		"apps", "update", "app_1", "--store-url", ""); err != nil {
		t.Fatalf("apps update: %v", err)
	}
	if probe.requests != 2 || probe.method != http.MethodPut {
		t.Fatalf("wire = %d request(s), last %s, want app lookup GET then PUT", probe.requests, probe.method)
	}
	var got map[string]any
	if err := json.Unmarshal(probe.body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if v, ok := got["storeUrl"]; !ok || v != nil {
		t.Errorf("storeUrl = %v (present: %v), want an explicit null clear", v, ok)
	}
}

// --- Issue #353: billing checkout --extra-apps/--extra-members and billing
// addons --extra-apps/--extra-members are pre-flighted against the billing
// schemas' 0-500 intake bound. Negative and over-cap values fail locally
// naming the flag and count with zero HTTP requests, while the in-range
// boundaries (0 and 500) keep the wire bodies byte-identical.

func TestBillingWriteValidatesAddonBoundsLocally(t *testing.T) {
	runLocalRejectCases(t, []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"checkout negative extra-apps", []string{"billing", "checkout", "--extra-apps", "-1"}, "invalid --extra-apps: must be 0-500, got -1"},
		{"checkout over-cap extra-apps", []string{"billing", "checkout", "--extra-apps", "501"}, "invalid --extra-apps: must be 0-500, got 501"},
		{"checkout negative extra-members", []string{"billing", "checkout", "--extra-members", "-2"}, "invalid --extra-members: must be 0-500, got -2"},
		{"checkout over-cap extra-members", []string{"billing", "checkout", "--extra-members", "999"}, "invalid --extra-members: must be 0-500, got 999"},
		{"addons negative extra-apps", []string{"billing", "addons", "--extra-apps", "-5"}, "invalid --extra-apps: must be 0-500, got -5"},
		{"addons over-cap extra-apps", []string{"billing", "addons", "--extra-apps", "501"}, "invalid --extra-apps: must be 0-500, got 501"},
		{"addons negative extra-members", []string{"billing", "addons", "--extra-members", "-1"}, "invalid --extra-members: must be 0-500, got -1"},
		{"addons over-cap extra-members", []string{"billing", "addons", "--extra-members", "999"}, "invalid --extra-members: must be 0-500, got 999"},
		{"checkout first offending flag wins", []string{"billing", "checkout", "--extra-apps", "-1", "--extra-members", "999"}, "invalid --extra-apps: must be 0-500, got -1"},
	})
}

func TestBillingAddonBoundsInRangeStillWire(t *testing.T) {
	for name, tc := range map[string]struct {
		args     []string
		method   string
		path     string
		commands int
		want     map[string]any
	}{
		"checkout at the 0 default": {
			args:     []string{"billing", "checkout"},
			method:   http.MethodPost,
			path:     "/billing/checkout",
			commands: 1,
			want:     map[string]any{"tier": "pro", "extraApps": float64(0), "extraMembers": float64(0)},
		},
		"checkout at the 500 cap": {
			args:     []string{"billing", "checkout", "--extra-apps", "500", "--extra-members", "500"},
			method:   http.MethodPost,
			path:     "/billing/checkout",
			commands: 1,
			want:     map[string]any{"tier": "pro", "extraApps": float64(500), "extraMembers": float64(500)},
		},
		"addons at the 0 default": {
			args:     []string{"billing", "addons"},
			method:   http.MethodPut,
			path:     "/billing/addons",
			commands: 1,
			want:     map[string]any{"extraApps": float64(0), "extraMembers": float64(0)},
		},
		"addons at the 500 cap": {
			args:     []string{"billing", "addons", "--extra-apps", "500", "--extra-members", "500"},
			method:   http.MethodPut,
			path:     "/billing/addons",
			commands: 1,
			want:     map[string]any{"extraApps": float64(500), "extraMembers": float64(500)},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
			var probe writeProbe
			server := serveWriteProbe(t, &probe, nil)
			// The probe's `{}` reply leaves CheckoutURL nil, so checkout
			// ends in the documented "billing is not configured" path; the
			// wire contract under test is the request, not that tail.
			_, _ = runRootWithSeededConfig(t, server.URL, defaultAppConfig, tc.args...)
			if probe.requests != tc.commands || probe.method != tc.method || !strings.HasSuffix(probe.path, tc.path) {
				t.Fatalf("wire = %d request(s), %s %s, want one %s to %s", probe.requests, probe.method, probe.path, tc.method, tc.path)
			}
			var got map[string]any
			if err := json.Unmarshal(probe.body, &got); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			for key, value := range tc.want {
				if got[key] != value {
					t.Errorf("%s = %v, want the verbatim %v", key, got[key], value)
				}
			}
		})
	}
}
