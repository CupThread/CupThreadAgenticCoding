package cmd

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// withConfirmHooks pins the confirmation TTY answer for one test and returns
// the buffer the prompt was written to. Every cmd-level confirmation test
// needs this: under `go test` the process stdin is /dev/null, which IS a
// character device, so the real stdinIsTerminal() would misreport TTY state.
func withConfirmHooks(t *testing.T, in io.Reader, isTTY bool) *bytes.Buffer {
	t.Helper()
	oldIn, oldOut, oldTTY := confirmIn, confirmOut, confirmIsTTY
	t.Cleanup(func() { confirmIn, confirmOut, confirmIsTTY = oldIn, oldOut, oldTTY })
	buf := &bytes.Buffer{}
	confirmIn = in
	confirmOut = buf
	confirmIsTTY = func() bool { return isTTY }
	return buf
}

// destructiveServer records every request; GETs under /feature-requests are
// answered with the list fixture so features delete can resolve its prefix,
// everything else gets a bare 200 like a hard-delete endpoint.
func destructiveServer(t *testing.T) (*httptest.Server, *[]*http.Request) {
	t.Helper()
	var reqs []*http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs = append(reqs, r)
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/feature-requests") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"requests":[{"id":"fr_123"}],"total":1,"hasMore":false}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	return server, &reqs
}

// TestFeaturesDeleteRefusesWithoutYes covers the issue's core scenario: a
// non-interactive invocation without --yes fails before ANY request —
// including the list call prefix resolution would make — and the error names
// the --yes escape hatch.
func TestFeaturesDeleteRefusesWithoutYes(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")
	withConfirmHooks(t, strings.NewReader(""), false)

	server, reqs := destructiveServer(t)
	_, err := runRoot(t, server.URL, "features", "delete", "fr_1", "--workspace", "ws_1", "--app", "app_1")
	if err == nil {
		t.Fatal("features delete without --yes must fail on non-interactive stdin")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("error must carry the --yes hint, got: %v", err)
	}
	if !strings.Contains(err.Error(), "features delete") {
		t.Errorf("error must name the command, got: %v", err)
	}
	if n := len(*reqs); n != 0 {
		t.Errorf("refusal must cost zero requests, saw %d (%v)", n, *reqs)
	}
}

// TestFeaturesDeleteYesResolvesThenDeletes runs the same invocation with
// --yes: the guard passes, prefix resolution lists once, and exactly one
// DELETE targets the resolved id.
func TestFeaturesDeleteYesResolvesThenDeletes(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")
	withConfirmHooks(t, strings.NewReader(""), false)

	server, reqs := destructiveServer(t)
	out, err := runRoot(t, server.URL, "features", "delete", "fr_1", "--workspace", "ws_1", "--app", "app_1", "--yes")
	if err != nil {
		t.Fatalf("features delete --yes: %v", err)
	}
	if !strings.Contains(out, "✓ Deleted fr_123") {
		t.Errorf("output missing confirmation line:\n%s", out)
	}
	var deletes []*http.Request
	for _, r := range *reqs {
		if r.Method == http.MethodDelete {
			deletes = append(deletes, r)
		}
	}
	if len(deletes) != 1 {
		t.Fatalf("want exactly one DELETE, saw %d of %d requests", len(deletes), len(*reqs))
	}
	if got := deletes[0].URL.Path; got != "/api/v1/console/workspaces/ws_1/feature-requests/fr_123" {
		t.Errorf("DELETE path = %s", got)
	}
}

// TestChangelogDeleteYesSendsExactlyOneDelete pins the simple no-resolution
// delete: --yes goes straight to a single DELETE.
func TestChangelogDeleteYesSendsExactlyOneDelete(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")
	withConfirmHooks(t, strings.NewReader(""), false)

	server, reqs := destructiveServer(t)
	out, err := runRoot(t, server.URL, "changelog", "delete", "cl_1", "--workspace", "ws_1", "--yes")
	if err != nil {
		t.Fatalf("changelog delete --yes: %v", err)
	}
	if !strings.Contains(out, "✓ Deleted changelog entry cl_1") {
		t.Errorf("output missing confirmation line:\n%s", out)
	}
	if n := len(*reqs); n != 1 {
		t.Fatalf("want exactly one request, saw %d", n)
	}
	r := (*reqs)[0]
	if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/console/workspaces/ws_1/changelog/cl_1" {
		t.Errorf("request = %s %s", r.Method, r.URL.Path)
	}
}

// TestRemainingDestructiveCommandsConfirmAndFire walks the other four
// irreversible commands: without --yes they refuse with zero requests, with
// --yes they send exactly their one wire call.
func TestRemainingDestructiveCommandsConfirmAndFire(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		wantMethod string
		wantPath   string
	}{
		{"columns delete", []string{"columns", "delete", "col_1"}, http.MethodDelete, "/api/v1/console/workspaces/ws_1/columns/col_1"},
		{"versions delete", []string{"versions", "delete", "v_1"}, http.MethodDelete, "/api/v1/console/workspaces/ws_1/versions/v_1"},
		{"members remove", []string{"workspaces", "members", "remove", "m_1"}, http.MethodDelete, "/api/v1/console/workspaces/ws_1/members/m_1"},
		{"imports cancel", []string{"imports", "cancel", "job_1"}, http.MethodPost, "/api/v1/console/workspaces/ws_1/imports/job_1/cancel"},
	}
	for _, tc := range cases {
		t.Run(tc.name+" refuses without --yes", func(t *testing.T) {
			t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")
			withConfirmHooks(t, strings.NewReader(""), false)

			server, reqs := destructiveServer(t)
			noArgs := append(append([]string{}, tc.args...), "--workspace", "ws_1")
			_, err := runRoot(t, server.URL, noArgs...)
			if err == nil || !strings.Contains(err.Error(), "--yes") {
				t.Fatalf("want --yes refusal, got: %v", err)
			}
			if n := len(*reqs); n != 0 {
				t.Errorf("refusal must cost zero requests, saw %d", n)
			}
		})
		t.Run(tc.name+" fires with --yes", func(t *testing.T) {
			t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")
			withConfirmHooks(t, strings.NewReader(""), false)

			server, reqs := destructiveServer(t)
			yesArgs := append(append([]string{}, tc.args...), "--workspace", "ws_1", "--yes")
			if _, err := runRoot(t, server.URL, yesArgs...); err != nil {
				t.Fatalf("%s --yes: %v", tc.name, err)
			}
			if n := len(*reqs); n != 1 {
				t.Fatalf("want exactly one request, saw %d", n)
			}
			r := (*reqs)[0]
			if r.Method != tc.wantMethod || r.URL.Path != tc.wantPath {
				t.Errorf("request = %s %s, want %s %s", r.Method, r.URL.Path, tc.wantMethod, tc.wantPath)
			}
		})
	}
}

// TestJSONRefusalKeepsStdoutPromptFree checks the structured-mode contract:
// the refusal is an ordinary error (nothing on stdout), so machine callers
// never see prompt text mixed into their output.
func TestJSONRefusalKeepsStdoutPromptFree(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")
	withConfirmHooks(t, strings.NewReader(""), false)

	server, reqs := destructiveServer(t)
	out, err := runRoot(t, server.URL, "changelog", "delete", "cl_1", "--workspace", "ws_1", "--json")
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("want --yes refusal, got: %v", err)
	}
	if strings.Contains(out, "About to") || strings.Contains(out, "Continue?") {
		t.Errorf("structured stdout must stay prompt-free, got:\n%s", out)
	}
	if n := len(*reqs); n != 0 {
		t.Errorf("refusal must cost zero requests, saw %d", n)
	}
}

// TestInteractiveAbortSendsNoRequests drives the TTY branch end to end: the
// prompt goes to the captured stderr stand-in and any answer but y/yes
// aborts with zero requests.
func TestInteractiveAbortSendsNoRequests(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")
	prompt := withConfirmHooks(t, strings.NewReader("n\n"), true)

	server, reqs := destructiveServer(t)
	_, err := runRoot(t, server.URL, "changelog", "delete", "cl_1", "--workspace", "ws_1")
	if err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("want abort error, got: %v", err)
	}
	if !strings.Contains(prompt.String(), `About to permanently delete changelog entry "cl_1"`) ||
		!strings.Contains(prompt.String(), "Continue? [yN]") {
		t.Errorf("prompt missing or wrong on captured stderr:\n%s", prompt.String())
	}
	if n := len(*reqs); n != 0 {
		t.Errorf("abort must cost zero requests, saw %d", n)
	}
}

// TestConfirmDestructiveAnswers unit-tests the accepted and rejected answer
// forms, including EOF as an abort.
func TestConfirmDestructiveAnswers(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"y", "y\n", false},
		{"yes", "YES\n", false},
		{"padded yes", "  yes \r\n", false},
		{"capital N", "N\n", true},
		{"empty line", "\n", true},
		{"eof", "", true},
		{"garbage", "delete it\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withConfirmHooks(t, strings.NewReader(tc.input), true)

			c := &cobra.Command{Use: "delete <entry-id>"}
			c.Flags().BoolP("yes", "y", false, "test")
			err := confirmDestructive(c, `permanently delete changelog entry "cl_1"`)
			if tc.wantErr && err == nil {
				t.Errorf("input %q: want error, got nil", tc.input)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("input %q: want nil, got %v", tc.input, err)
			}
		})
	}
}
