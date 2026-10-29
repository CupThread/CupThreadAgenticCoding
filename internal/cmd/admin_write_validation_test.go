package cmd

import (
	"encoding/json"
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
	server := serveWriteProbe(t, &probe, nil)
	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig,
		"changelog", "update", "cl_1", "--title", "Renamed entry", "--version-label", ""); err != nil {
		t.Fatalf("changelog update: %v", err)
	}
	if probe.requests != 1 || probe.method != http.MethodPut {
		t.Fatalf("wire = %d request(s), %s, want one PUT", probe.requests, probe.method)
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
