package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// blankServer counts every request it receives, so a test can prove the
// blank-argument refusal happened before any round trip. Every response is a
// minimal success body: if the guard ever fails to fire, the command under
// test completes cleanly and the non-nil-error assertion is what catches it.
func blankServer(t *testing.T, hits *int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/rerun"):
			_, _ = w.Write([]byte(`{"job":{"id":"job_1","source":"linear","mode":"preview","status":"queued","attempts":0,"createdAt":"2026-10-01T00:00:00Z","updatedAt":"2026-10-01T00:00:00Z"}}`))
		case strings.HasSuffix(r.URL.Path, "/publish"):
			_, _ = w.Write([]byte(`{"id":"entry_1","title":"t"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// blankLabel renders a blank argument for a readable subtest name.
func blankLabel(s string) string {
	if s == "" {
		return "empty"
	}
	return "blank"
}

// TestBlankPositionalIDsRejectedBeforeAnyRequest pins the issue #293 guard on
// every single-ID command, the features family included (issue #310): an
// empty or whitespace-only id — the canonical unset-shell-variable accident —
// fails locally with a self-diagnosing error naming the argument, and never
// reaches the wire, not even for the confirm-guarded destructive commands,
// where the refusal must precede the confirmDestructive prompt (so a
// non-interactive run fails with the argument error, not the missing---yes
// refusal). The comments and users rows are the #304 residuals: the three
// ID-taking commands b85b77a missed; the features rows close the family that
// guarded inside fetchOneFeatureRequest only, after the confirm prompt.
func TestBlankPositionalIDsRejectedBeforeAnyRequest(t *testing.T) {
	// yes marks the commands that register --yes; those run both with and
	// without it so the guard demonstrably fires before the confirm refusal.
	// The @ID@ sentinel marks where the blank id is spliced in (it is
	// args[0] everywhere, but multi-arg commands take trailing positionals).
	cases := []struct {
		yes  bool
		args []string
	}{
		{false, []string{"comments", "moderation", "list", "@ID@"}},
		{false, []string{"comments", "list", "@ID@"}},
		{false, []string{"comments", "moderation", "hide", "@ID@"}},
		{false, []string{"comments", "moderation", "unhide", "@ID@"}},
		{true, []string{"comments", "moderation", "delete", "@ID@"}},
		{false, []string{"users", "profile", "@ID@"}},
		{false, []string{"notifications", "read", "@ID@"}},
		{false, []string{"changelog", "update", "@ID@", "--title", "t"}},
		{true, []string{"changelog", "delete", "@ID@"}},
		{false, []string{"changelog", "publish", "@ID@"}},
		{false, []string{"changelog", "unpublish", "@ID@"}},
		{false, []string{"imports", "get", "@ID@"}},
		{true, []string{"imports", "cancel", "@ID@"}},
		{false, []string{"imports", "rerun", "@ID@"}},
		{false, []string{"workspaces", "members", "set-role", "@ID@", "--role", "admin"}},
		{true, []string{"workspaces", "members", "remove", "@ID@"}},
		{false, []string{"workspaces", "invitations", "revoke", "@ID@"}},
		{false, []string{"columns", "update", "@ID@", "--name", "n"}},
		{true, []string{"columns", "delete", "@ID@"}},
		{false, []string{"versions", "update", "@ID@", "--label", "v"}},
		{true, []string{"versions", "delete", "@ID@"}},
		{false, []string{"inbox", "get", "@ID@"}},
		{false, []string{"inbox", "priority", "@ID@", "!!!"}},
		{false, []string{"inbox", "triage", "@ID@", "open"}},
		{false, []string{"inbox", "assign", "@ID@"}},
		{false, []string{"inbox", "retry", "@ID@"}},
		// bulk-triage is the family's one multi-reference command: the blank
		// guard is noBlankArgs (every positional), still in the Args layer
		// (issue #427). The index-naming mixed-blank cases live in
		// inbox_test.go, which the single-blank harness below cannot express.
		{false, []string{"inbox", "bulk-triage", "@ID@"}},
		{false, []string{"features", "get", "@ID@"}},
		{false, []string{"features", "update", "@ID@", "--title", "t"}},
		{false, []string{"features", "approve", "@ID@"}},
		{true, []string{"features", "delete", "@ID@"}},
		{false, []string{"features", "forward", "@ID@", "--target", "issue"}},
		// The id-or-slug / app-key references resolved by list-and-match
		// (lookupWorkspace/lookupApp) plus the unauthenticated public
		// app-key commands (issue #430): the blank value used to reach
		// lookupWorkspace/lookupApp as a wasted authenticated round trip
		// that misreported the unset variable as "not found", and the
		// public commands built // paths, a trailing-slash collection
		// path, and an empty appKey= query before failing. The guard
		// fires in the Args layer, before the lookup or any request.
		{false, []string{"workspaces", "use", "@ID@"}},
		{false, []string{"apps", "get", "@ID@"}},
		{false, []string{"apps", "use", "@ID@"}},
		{false, []string{"apps", "update", "@ID@"}},
		{false, []string{"apps", "settings", "show", "@ID@"}},
		{false, []string{"apps", "settings", "set", "@ID@"}},
		{false, []string{"integrations", "github", "config", "@ID@"}},
		{false, []string{"integrations", "github", "sync", "@ID@"}},
		{false, []string{"apps", "public-changelog", "@ID@"}},
		{false, []string{"apps", "public-feature-requests", "@ID@"}},
		{false, []string{"apps", "public-config", "@ID@"}},
	}

	blanks := []string{"", "   "}
	for _, tc := range cases {
		for _, blank := range blanks {
			for _, yes := range []struct {
				label string
				flag  []string
			}{{"no-yes", nil}, {"yes", []string{"--yes"}}} {
				if !tc.yes && yes.flag != nil {
					continue // the command takes no --yes flag
				}
				full := make([]string, 0, len(tc.args)+1)
				for _, a := range tc.args {
					if a == "@ID@" {
						a = blank
					}
					full = append(full, a)
				}
				full = append(full, yes.flag...)

				name := strings.Join(tc.args, " ") + " " + blankLabel(blank) + " " + yes.label
				t.Run(name, func(t *testing.T) {
					var hits int
					server := blankServer(t, &hits)

					_, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, full...)
					if err == nil {
						t.Fatalf("%v: expected the blank-argument refusal, got success", full)
					}
					if !strings.Contains(err.Error(), "is empty — pass a non-blank") {
						t.Errorf("%v error = %v, want the self-diagnosing blank-argument error", full, err)
					}
					if !strings.Contains(err.Error(), tc.args[0]) {
						t.Errorf("%v error = %v, want it to name the command path", full, err)
					}
					if hits != 0 {
						t.Errorf("%v issued %d HTTP request(s), want 0 (refused before any round trip)", full, hits)
					}
				})
			}
		}
	}
}

// TestBlankArgGuardKeepsNonBlankIDsWorking pins that the guard changed
// nothing for real ids: non-blank ids still reach the server and their
// commands still succeed, and an unknown non-blank id keeps its server
// not-found error instead of a local message (issue #293 requirement 4).
func TestBlankArgGuardKeepsNonBlankIDsWorking(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "sub_missing") || strings.Contains(r.URL.Path, "cmt_missing") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"resource not found"}}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)

	// A real id passes the guard and completes.
	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "notifications", "read", "notif_1"); err != nil {
		t.Fatalf("notifications read notif_1: %v", err)
	}
	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "inbox", "priority", "sub_1", "!!!"); err != nil {
		t.Fatalf("inbox priority sub_1: %v", err)
	}
	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "inbox", "assign", "sub_1", "user_1"); err != nil {
		t.Fatalf("inbox assign sub_1 user_1: %v", err)
	}
	// The optional assignee staying absent (the unassign form) must keep
	// passing the second validator — nonBlankArg(1, …) only fires when the
	// argument is present.
	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "inbox", "assign", "sub_1"); err != nil {
		t.Fatalf("inbox assign sub_1 (unassign): %v", err)
	}
	// Same presence discipline for the #430 optional positionals: bare
	// 'apps settings show' (the whole-workspace listing) keeps working —
	// the new nonBlankArg(0, …) fires only when the optional app
	// id-or-slug is actually supplied.
	if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "apps", "settings", "show"); err != nil {
		t.Fatalf("apps settings show (workspace listing): %v", err)
	}

	// A blank optional assignee is refused locally too (CodeRabbit review of
	// #296): RunE would otherwise send assignedTo: "" as a real assignment.
	_, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "inbox", "assign", "sub_1", "")
	if err == nil || !strings.Contains(err.Error(), "clerk user id is empty") {
		t.Errorf("inbox assign sub_1 \"\" error = %v, want the blank-assignee refusal", err)
	}

	// An unknown non-blank id still fails with a not-found error — the guard
	// must not swallow resolution failures behind a local message. Since
	// issue #418 the 404 also triggers the listing-walk fallback (one extra
	// GET, served empty here), and the scoped not-found wording names the
	// scanned window.
	_, err = runRootWithSeededConfig(t, server.URL, defaultAppConfig, "inbox", "get", "sub_missing")
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "not found") {
		t.Errorf("inbox get sub_missing error = %v, want the not-found error", err)
	}
	_, err = runRootWithSeededConfig(t, server.URL, defaultAppConfig, "comments", "moderation", "hide", "cmt_missing")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("comments moderation hide cmt_missing error = %v, want the not-found error", err)
	}

	if hits != 8 {
		t.Errorf("server saw %d request(s), want 8", hits)
	}
}

// TestChangelogBlankBodyFileFailsLocally pins the resolveBody strictness
// (issue #293 requirement 2): an explicitly passed blank --body-file fails
// before any request instead of falling through to --body's default "" —
// which silently wiped the entry body on update and created empty-bodied
// entries — while every documented file form and the plain --body flag keep
// working byte-for-byte.
func TestChangelogBlankBodyFileFailsLocally(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	var hits int
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if serveChangelogListing(w, r, "entry_1") {
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			bodies = append(bodies, body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"entry_1","title":"t"}`))
	}))
	t.Cleanup(server.Close)

	bodyFile := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(bodyFile, []byte("file body"), 0o600); err != nil {
		t.Fatalf("write body file: %v", err)
	}

	t.Run("update --body-file empty refuses", func(t *testing.T) {
		hits, bodies = 0, nil
		_, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "changelog", "update", "entry_1", "--body-file", "")
		if err == nil || !strings.Contains(err.Error(), "--body-file is empty") {
			t.Fatalf("error = %v, want the blank --body-file refusal", err)
		}
		if hits != 0 {
			t.Errorf("issued %d request(s), want 0", hits)
		}
	})

	// With both body flags passed the #426 mutual-exclusion guard fires
	// before resolveBody, so the pair is refused as contradictory rather
	// than reaching the blank --body-file check.
	t.Run("update --body-file empty with --body refuses as contradictory", func(t *testing.T) {
		hits, bodies = 0, nil
		_, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "changelog", "update", "entry_1", "--body", "text", "--body-file", "")
		if err == nil || !strings.Contains(err.Error(), "--body and --body-file are mutually exclusive") {
			t.Fatalf("error = %v, want the mutual-exclusion refusal to outrank resolveBody", err)
		}
		if hits != 0 {
			t.Errorf("issued %d request(s), want 0", hits)
		}
	})

	t.Run("create --body-file empty refuses", func(t *testing.T) {
		hits, bodies = 0, nil
		_, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "changelog", "create", "--title", "t", "--body-file", "")
		if err == nil || !strings.Contains(err.Error(), "--body-file is empty") {
			t.Fatalf("error = %v, want the blank --body-file refusal", err)
		}
		if hits != 0 {
			t.Errorf("issued %d request(s), want 0", hits)
		}
	})

	t.Run("--body still reaches the wire", func(t *testing.T) {
		hits, bodies = 0, nil
		if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "changelog", "update", "entry_1", "--body", "text body"); err != nil {
			t.Fatalf("update --body: %v", err)
		}
		if hits != 2 || len(bodies) != 1 || bodies[0]["body"] != "text body" {
			t.Errorf("wire body = %v (hits %d), want the resolution GET plus one PUT with body=%q", bodies, hits, "text body")
		}
	})

	t.Run("--body-file path form", func(t *testing.T) {
		hits, bodies = 0, nil
		if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "changelog", "update", "entry_1", "--body-file", bodyFile); err != nil {
			t.Fatalf("update --body-file path: %v", err)
		}
		if hits != 2 || len(bodies) != 1 || bodies[0]["body"] != "file body" {
			t.Errorf("wire body = %v (hits %d), want the resolution GET plus one PUT with the file contents", bodies, hits)
		}
	})

	t.Run("--body-file @path form", func(t *testing.T) {
		hits, bodies = 0, nil
		if _, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig, "changelog", "update", "entry_1", "--body-file", "@"+bodyFile); err != nil {
			t.Fatalf("update --body-file @path: %v", err)
		}
		if hits != 2 || len(bodies) != 1 || bodies[0]["body"] != "file body" {
			t.Errorf("wire body = %v (hits %d), want the resolution GET plus one PUT with the file contents", bodies, hits)
		}
	})

	t.Run("--body-file - reads stdin", func(t *testing.T) {
		hits, bodies = 0, nil
		_, err := runRootCfgWithStdin(t, filepath.Join(t.TempDir(), "config.json"), server.URL, "stdin body",
			"changelog", "update", "entry_1", "--body-file", "-", "--workspace", "ws_1")
		if err != nil {
			t.Fatalf("update --body-file -: %v", err)
		}
		if hits != 2 || len(bodies) != 1 || bodies[0]["body"] != "stdin body" {
			t.Errorf("wire body = %v (hits %d), want the resolution GET plus one PUT with the stdin contents", bodies, hits)
		}
	})
}

// TestLoginBlankTokenFailsFast pins the auth dispatch fix (issue #293
// requirement 3): `--token ""` must reach loginWithToken's "empty token"
// validation immediately instead of falling through to the PKCE browser
// flow, which binds a callback listener, launches a browser and blocks for
// up to 120 s — the zero request count and the instant error prove the flow
// never started. Padded-blank values keep their trimmed "empty token" error,
// and a real token still logs in.
func TestLoginBlankTokenFailsFast(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(meFixture))
	}))
	t.Cleanup(server.Close)

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	for _, blank := range []string{"", "   ", "\t\n"} {
		t.Run("blank "+blankLabel(blank), func(t *testing.T) {
			before := hits
			_, err := runRootCfg(t, cfgPath, server.URL, "auth", "login", "--token", blank)
			if err == nil || !strings.Contains(err.Error(), "empty token") {
				t.Fatalf("error = %v, want the immediate empty-token failure", err)
			}
			if hits != before {
				t.Errorf("server saw %d new request(s), want 0 (no PKCE round trip)", hits-before)
			}
		})
	}

	t.Run("real token still logs in", func(t *testing.T) {
		before := hits
		out, err := runRootCfg(t, cfgPath, server.URL, "auth", "login", "--token", "cpt_live_token")
		if err != nil {
			t.Fatalf("login with a real token: %v\n%s", err, out)
		}
		if hits != before+1 {
			t.Errorf("server saw %d new request(s), want 1 (the session probe)", hits-before)
		}
	})
}
