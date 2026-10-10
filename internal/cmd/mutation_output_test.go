package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// The tests in this file pin the issue #194 mutation-output contract: every
// command that mutates server state prints exactly one machine-readable
// document on stdout in --json/-o yaml mode. For mutations the server answers
// without a body that document is the minimal {action, id, success} record
// emitted by emitMutationResult — with id carrying the RESOLVED resource ID,
// so a prefix-taking command (features delete fr_a) discloses which of the
// prefix matches it acted on. Commands the server answers with a record keep
// printing that richer document. Human (table) output stays byte-identical.

// workspaceOnlyConfig seeds a default workspace without a default app, so
// fetchOneFeatureRequest scans the whole workspace listing — the two-record
// fixture — and prefix resolution has a real ambiguity to rule out.
const workspaceOnlyConfig = `{"defaultWorkspace":"ws_1"}`

// mutationResultServer answers the shapes the mutation tests need: the
// two-record feature-request listing for ID resolution, the apps listing for
// lookupApp, and a generic success body for every mutation.
func mutationResultServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/feature-requests"):
			_, _ = w.Write(filteredFixture(t, r.URL.Query().Get("appId")))
		case r.URL.Path == "/api/v1/console/workspaces/ws_1/apps":
			_, _ = w.Write([]byte(appListFixture))
		default:
			_, _ = w.Write([]byte(`{"success":true}`))
		}
	}))
}

// TestMutationCommandsEmitStructuredResult runs every formerly silent
// mutation site with --json and pins the exact mutation-result document: one
// decodable JSON object, success true, the expected action verb, and the
// resolved resource id (absent for the whole-context commands that have none).
func TestMutationCommandsEmitStructuredResult(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	cases := []struct {
		name string
		args []string
		cfg  string // config seed; defaultAppConfig unless the case needs otherwise
		act  string // expected "action" verb
		id   string // expected "id" ("" = the key must be absent)
	}{
		{"features update", []string{"features", "update", "fr_a_1", "--title", "New title"}, defaultAppConfig, "updated", "fr_a_1"},
		{"features approve", []string{"features", "approve", "fr_a_1"}, defaultAppConfig, "approved", "fr_a_1"},
		{
			"features delete resolves the prefix",
			[]string{"features", "delete", "fr_a", "--yes"},
			// Workspace-wide listing: two requests (fr_a_1, fr_b_1) are
			// visible and only fr_a_1 matches the prefix — the JSON must name
			// the full ID the resolution picked, the exact disclosure gap of
			// issue #194.
			workspaceOnlyConfig, "deleted", "fr_a_1",
		},
		{"columns update", []string{"columns", "update", "col_1", "--name", "In Review"}, defaultAppConfig, "updated", "col_1"},
		{"columns delete", []string{"columns", "delete", "col_1", "--yes"}, defaultAppConfig, "deleted", "col_1"},
		{"versions update", []string{"versions", "update", "ver_1", "--label", "1.0.1"}, defaultAppConfig, "updated", "ver_1"},
		{"versions delete", []string{"versions", "delete", "ver_1", "--yes"}, defaultAppConfig, "deleted", "ver_1"},
		{"changelog delete", []string{"changelog", "delete", "entry_1", "--yes"}, defaultAppConfig, "deleted", "entry_1"},
		{"changelog unpublish", []string{"changelog", "unpublish", "entry_1"}, defaultAppConfig, "unpublished", "entry_1"},
		{"workspaces members set-role", []string{"workspaces", "members", "set-role", "mem_1", "--role", "admin"}, defaultAppConfig, "role_set", "mem_1"},
		{"workspaces members remove", []string{"workspaces", "members", "remove", "mem_1", "--yes"}, defaultAppConfig, "removed", "mem_1"},
		{"workspaces invitations revoke", []string{"workspaces", "invitations", "revoke", "inv_1"}, defaultAppConfig, "revoked", "inv_1"},
		{"notifications read", []string{"notifications", "read", "notif_1"}, defaultAppConfig, "read", "notif_1"},
		{"notifications read-all has no resource id", []string{"notifications", "read-all"}, defaultAppConfig, "read_all", ""},
		{"imports cancel", []string{"imports", "cancel", "job_1", "--yes"}, defaultAppConfig, "canceled", "job_1"},
		{"integrations github disconnect", []string{"integrations", "github", "disconnect"}, defaultAppConfig, "disconnected", "github"},
		{"integrations provider disconnect", []string{"integrations", "linear", "disconnect"}, defaultAppConfig, "disconnected", "linear"},
		{
			"integrations github config names the resolved app",
			[]string{"integrations", "github", "config", "ios", "--owner", "acme"},
			// The app argument is a slug; lookupApp resolves it to app_1 and
			// the document must carry that resolved ID, not the slug.
			defaultAppConfig, "config_updated", "app_1",
		},
	}

	server := mutationResultServer(t)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(cfgPath, []byte(tc.cfg), 0o600); err != nil {
				t.Fatalf("seed config: %v", err)
			}
			out, _, err := runRootCapture(t, cfgPath, server.URL, append(tc.args, "--json")...)
			if err != nil {
				t.Fatalf("%v --json: %v\n%s", tc.args, err, out)
			}
			doc := unmarshalOneJSON(t, out)
			if doc["success"] != true {
				t.Errorf("success = %v, want true:\n%s", doc["success"], out)
			}
			if doc["action"] != tc.act {
				t.Errorf("action = %v, want %q:\n%s", doc["action"], tc.act, out)
			}
			if tc.id == "" {
				if _, present := doc["id"]; present {
					t.Errorf("id = %v, want the key absent for a whole-context action:\n%s", doc["id"], out)
				}
			} else if doc["id"] != tc.id {
				t.Errorf("id = %v, want the resolved %q:\n%s", doc["id"], tc.id, out)
			}
			// The document must decode into the published shape.
			var rec mutationResult
			if err := json.Unmarshal([]byte(out), &rec); err != nil {
				t.Fatalf("decode into mutationResult: %v\n%s", err, out)
			}
		})
	}
}

// TestMutationResultYAML pins the same contract for -o yaml: one YAML document
// with the same action/id/success fields.
func TestMutationResultYAML(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	server := mutationResultServer(t)

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(workspaceOnlyConfig), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	out, _, err := runRootCapture(t, cfgPath, server.URL, "features", "delete", "fr_a", "--yes", "-o", "yaml")
	if err != nil {
		t.Fatalf("features delete -o yaml: %v\n%s", err, out)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout is not a single YAML document: %v\n%s", err, out)
	}
	if doc["action"] != "deleted" || doc["id"] != "fr_a_1" || doc["success"] != true {
		t.Errorf("yaml document = %v, want {action: deleted, id: fr_a_1, success: true}", doc)
	}
}

// TestMutationTableOutputByteIdentical pins the human-mode echoes of the
// newly covered commands: table output is unchanged by the fix.
func TestMutationTableOutputByteIdentical(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	server := mutationResultServer(t)

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"features delete", []string{"features", "delete", "fr_a", "--yes"}, "✓ Deleted fr_a_1\n"},
		{"features update", []string{"features", "update", "fr_a_1", "--title", "New title"}, "✓ Updated feature request fr_a_1\n"},
		{"columns delete", []string{"columns", "delete", "col_1", "--yes"}, "✓ Deleted column col_1\n"},
		{"notifications read-all", []string{"notifications", "read-all"}, "✓ Marked all notifications as read\n"},
		{"integrations github config", []string{"integrations", "github", "config", "ios", "--owner", "acme"}, "✓ GitHub config updated for app_1\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(cfgPath, []byte(defaultAppConfig), 0o600); err != nil {
				t.Fatalf("seed config: %v", err)
			}
			out, _, err := runRootCapture(t, cfgPath, server.URL, tc.args...)
			if err != nil {
				t.Fatalf("%v: %v\n%s", tc.args, err, out)
			}
			if out != tc.want {
				t.Errorf("table output = %q, want %q", out, tc.want)
			}
		})
	}
}

// guardMutations drives every command that can issue a non-GET API request to
// its mutation against the guard's fixture server (the minimum args and flags
// to reach the HTTP call). The tree guard fails for any leaf missing from this
// table or guardReadonly, so a new mutating command cannot land without
// declaring itself — and then the run below fails while its stdout is empty
// in --json mode, which is exactly the regression issue #194 fixed.
var guardMutations = map[string][]string{
	"apps create":                    {"--name", "Guard App"},
	"apps settings set":              {"app_1", "--anon-vote=false"},
	"apps update":                    {"app_1", "--name", "Guard Rename"},
	"billing addons":                 {},
	"billing checkout":               {},
	"changelog create":               {"--title", "t", "--body", "b"},
	"changelog delete":               {"entry_1", "--yes"},
	"changelog publish":              {"entry_1"},
	"changelog unpublish":            {"entry_1"},
	"changelog update":               {"entry_1", "--title", "t"},
	"columns create":                 {"--name", "Guard Col"},
	"columns delete":                 {"col_1", "--yes"},
	"columns update":                 {"col_1", "--name", "Guard Col"},
	"comments create":                {"fr_1", "--body", "b"},
	"comments moderation delete":     {"com_1"},
	"comments moderation hide":       {"com_1"},
	"comments moderation unhide":     {"com_1"},
	"features approve":               {"fr_1"},
	"features create":                {"--title", "t", "--description", "d"},
	"features delete":                {"fr_1", "--yes"},
	"features forward":               {"fr_1"},
	"features update":                {"fr_1", "--title", "t"},
	"imports cancel":                 {"job_1", "--yes"},
	"imports create":                 {"--source", "github_issues"},
	"imports rerun":                  {"job_1"},
	"inbox assign":                   {"sub_1", "clerk_1"},
	"inbox bulk-triage":              {"sub_1", "--triage-status", "resolved"},
	"inbox priority":                 {"sub_1", "!!!"},
	"inbox retry":                    {"sub_1"},
	"inbox triage":                   {"sub_1", "resolved"},
	"integrations github config":     {"app_1", "--owner", "acme"},
	"integrations github connect":    {"--token", "ghp_guard"},
	"integrations github disconnect": {},
	"integrations github sync":       {"app_1"},
	"integrations linear connect":    {"--token", "lin_guard"},
	"integrations linear disconnect": {},
	"integrations notion connect":    {"--token", "not_guard"},
	"integrations notion disconnect": {},
	"integrations slack connect":     {"--token", "slk_guard"},
	"integrations slack disconnect":  {},
	"notifications prefs set":        {"--channel", "inbox", "--enable"},
	"notifications read":             {"notif_1"},
	"notifications read-all":         {},
	"versions create":                {"--label", "1.0.0"},
	"versions delete":                {"ver_1", "--yes"},
	"versions update":                {"ver_1", "--label", "1.0.1"},
	"workspaces create":              {"--name", "Guard Co"},
	"workspaces invitations revoke":  {"inv_1"},
	"workspaces members add":         {"--clerk-user-id", "clerk_1"},
	"workspaces members invite":      {"--email", "guard@example.com"},
	"workspaces members remove":      {"mem_1", "--yes"},
	"workspaces members set-role":    {"mem_1", "--role", "admin"},
}

// guardReadonly lists every leaf command that cannot mutate server state, so
// the tree guard's completeness check has a documented classification for it.
// API-reading commands are grouped as one entry shape; local-only commands
// name the test that covers their own output contract.
var guardReadonly = map[string]string{
	"api request":                    "passthrough echoes the server response verbatim in --json mode (api_test.go)",
	"api sign-user-attrs":            "local signing helper, sends nothing (api_signing_test.go)",
	"apps get":                       "GET",
	"apps list":                      "GET",
	"apps public-changelog":          "public GET",
	"apps public-config":             "public GET",
	"apps public-feature-requests":   "public GET",
	"apps settings show":             "GET",
	"apps use":                       "GET plus a local config save (#68 setup contract)",
	"auth login":                     "interactive browser/device/token flows (#68 setup contract, auth_test.go)",
	"auth logout":                    "local config only (#68 setup contract)",
	"auth status":                    "GET probe plus local state (auth_status_test.go)",
	"billing portal":                 "GET",
	"billing show":                   "GET",
	"changelog list":                 "GET",
	"columns list":                   "GET",
	"comments list":                  "public GET",
	"comments moderation list":       "GET",
	"features get":                   "GET (resolution paging)",
	"features list":                  "GET",
	"imports get":                    "GET",
	"imports history":                "GET",
	"imports list":                   "GET",
	"inbox deliveries":               "GET",
	"inbox get":                      "GET",
	"inbox list":                     "GET",
	"integrations github auth-url":   "GET",
	"integrations github categories": "GET",
	"integrations github repos":      "GET",
	"integrations linear auth-url":   "GET",
	"integrations linear status":     "GET",
	"integrations notion auth-url":   "GET",
	"integrations notion status":     "GET",
	"integrations slack auth-url":    "GET",
	"integrations slack status":      "GET",
	"integrations status":            "GET per provider",
	"me":                             "GET",
	"notifications list":             "GET",
	"notifications prefs show":       "GET",
	"search":                         "GET",
	"skills link":                    "local filesystem only (#68 setup contract)",
	"skills list":                    "local",
	"status":                         "local git inspection",
	"users profile":                  "GET",
	"versions list":                  "GET",
	"workspaces invitations list":    "GET",
	"workspaces list":                "GET",
	"workspaces members list":        "GET",
	"workspaces use":                 "GET plus a local config save (#68 setup contract)",
}

// mutationGuardServer answers enough of the API for every guardMutations
// entry to reach its mutation: the me and apps fixtures for context
// resolution, a one-record feature-request listing for fetchOneFeatureRequest,
// an empty object for the remaining reads, and a generic success body for
// every mutation. It counts the non-GET requests so the guard can prove each
// registered command still reaches one.
func mutationGuardServer(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	mutations := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			switch {
			case r.URL.Path == "/api/v1/console/me":
				_, _ = w.Write([]byte(meFixture))
			case r.URL.Path == "/api/v1/console/workspaces/ws_1/apps":
				_, _ = w.Write([]byte(appListFixture))
			case strings.HasSuffix(r.URL.Path, "/feature-requests"):
				_, _ = w.Write([]byte(`{"requests":[{"id":"fr_1","title":"Guard request","status":"open","voteCount":1,"createdAt":"2026-09-01T12:00:00.000Z","updatedAt":"2026-09-01T12:00:00.000Z"}],"total":1}`))
			default:
				_, _ = w.Write([]byte(`{}`))
			}
			return
		}
		mutations++
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	t.Cleanup(server.Close)
	return server, &mutations
}

// TestCommandTreeMutationsEmitStructuredOutput enumerates the command tree
// and enforces the issue #194 regression guard:
//
//  1. Every leaf command is classified in guardMutations or guardReadonly —
//     a new command cannot land unclassified.
//  2. Every classified mutating command succeeds against the fixture server,
//     actually issues its non-GET request, and prints exactly one
//     machine-readable document on stdout in --json mode — never nothing.
func TestCommandTreeMutationsEmitStructuredOutput(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	var leaves []string
	var walk func(path string, c *cobra.Command)
	walk = func(path string, c *cobra.Command) {
		subs := c.Commands()
		if len(subs) == 0 {
			if c.RunE != nil {
				leaves = append(leaves, strings.TrimSpace(path))
			}
			return
		}
		for _, s := range subs {
			walk(path+" "+s.Name(), s)
		}
	}
	walk("", newRootCmd())
	if len(leaves) == 0 {
		t.Fatal("command tree walk found no runnable leaves")
	}

	for _, path := range leaves {
		_, mutating := guardMutations[path]
		_, readonly := guardReadonly[path]
		if !mutating && !readonly {
			t.Errorf("leaf command %q is classified in neither guardMutations nor guardReadonly; declare it (with args that reach its HTTP call) so the mutation-output guard keeps covering it", path)
		}
	}

	for path, args := range guardMutations {
		t.Run(path, func(t *testing.T) {
			cfgPath := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(cfgPath, []byte(defaultAppConfig), 0o600); err != nil {
				t.Fatalf("seed config: %v", err)
			}
			// A fresh server per case keeps the mutation counter per-case, so
			// the reachability assert below cannot be satisfied by an earlier
			// subtest's request.
			server, mutations := mutationGuardServer(t)
			full := append(append(append([]string{}, strings.Fields(path)...), args...), "--json")
			out, _, err := runRootCapture(t, cfgPath, server.URL, full...)
			if err != nil {
				t.Fatalf("%v: command failed against the fixtures — update the guard's args: %v\n%s", full, err, out)
			}
			if *mutations == 0 {
				t.Fatalf("%v: no mutation request reached the server — the guard's args have rotted", full)
			}
			if strings.TrimSpace(out) == "" {
				t.Errorf("mutating command %q printed NOTHING on stdout in --json mode (issue #194)", path)
			}
			unmarshalOneJSON(t, out)
		})
	}
}
