package cmd

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Tests for the curl-style @path input syntax (issue #188): the help texts,
// the apps settings set error and SKILL.md teach `--input @file`, but
// readInputFile only treated the exact values "-" and "@" as stdin and opened
// everything else verbatim — the documented spelling failed with
// "open @body.json: no such file or directory", and silently read a literal
// @-prefixed file when one happened to exist in its place.

// TestReadInputFileAtSyntax pins the three input forms of readInputFile:
// exactly "-" or "@" reads stdin, a leading "@" followed by more characters
// opens the named file (the sigil is stripped before the open, so the error
// names the real path), and anything else stays a plain path. The bounded
// read behind every form is unchanged (issue #142).
func TestReadInputFileAtSyntax(t *testing.T) {
	dir := t.TempDir()
	const body = `{"a":1}`
	bodyPath := filepath.Join(dir, "body.json")
	if err := os.WriteFile(bodyPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write body file: %v", err)
	}

	const stdinBody = `{"from":"stdin"}`
	stdinFile := filepath.Join(dir, "stdin")
	if err := os.WriteFile(stdinFile, []byte(stdinBody), 0o600); err != nil {
		t.Fatalf("write stdin file: %v", err)
	}
	f, err := os.Open(stdinFile)
	if err != nil {
		t.Fatalf("open stdin file: %v", err)
	}
	oldStdin := os.Stdin
	os.Stdin = f
	t.Cleanup(func() {
		os.Stdin = oldStdin
		if err := f.Close(); err != nil {
			t.Errorf("close stdin file: %v", err)
		}
	})

	for _, tc := range []struct {
		name    string
		arg     string
		want    string
		wantErr bool
	}{
		{name: "@path reads the named file", arg: "@" + bodyPath, want: body},
		{name: "plain path still reads the file", arg: bodyPath, want: body},
		{name: "- reads stdin", arg: "-", want: stdinBody},
		{name: "@ reads stdin", arg: "@", want: stdinBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The stdin forms drain os.Stdin to EOF; rewind so both cases see
			// the full content.
			if _, err := f.Seek(0, 0); err != nil {
				t.Fatalf("rewind stdin file: %v", err)
			}
			out, err := readInputFile(tc.arg, maxConsoleBodyBytes)
			if err != nil {
				t.Fatalf("readInputFile(%q): %v", tc.arg, err)
			}
			if string(out) != tc.want {
				t.Errorf("readInputFile(%q) = %q, want %q", tc.arg, out, tc.want)
			}
		})
	}

	t.Run("@missing names the path without the sigil", func(t *testing.T) {
		missing := filepath.Join(dir, "missing.json")
		_, err := readInputFile("@"+missing, maxConsoleBodyBytes)
		// The not-exist wording is OS-specific ("no such file or directory"
		// vs Windows' "The system cannot find the file specified."), so match
		// on the sentinel, not the text.
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("@missing read err = %v, want a not-exist error", err)
		}
		if !strings.Contains(err.Error(), missing) {
			t.Errorf("error = %q, want it to name %s", err, missing)
		}
		if strings.Contains(err.Error(), "@"+missing) {
			t.Errorf("error = %q, want the @ sigil stripped from the named path", err)
		}
	})

	t.Run("@path stays bounded", func(t *testing.T) {
		huge := filepath.Join(dir, "huge.json")
		if err := os.WriteFile(huge, []byte(strings.Repeat("x", 11)), 0o600); err != nil {
			t.Fatalf("write huge file: %v", err)
		}
		_, err := readInputFile("@"+huge, 10)
		var tooLarge *InputTooLargeError
		if !asInputTooLarge(err, &tooLarge) {
			t.Fatalf("@path over cap err = %v, want InputTooLargeError", err)
		}
		if tooLarge.Limit != 10 {
			t.Errorf("limit = %d, want 10", tooLarge.Limit)
		}
	})
}

// TestAPIRequestInputAtFileSendsBytes covers the command-level contract: the
// documented `api request --input @file` spelling sends the file's contents
// byte-for-byte (issue #75 numeric passthrough included), and the bare "@"
// stdin form still reads the pipe.
func TestAPIRequestInputAtFileSendsBytes(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	const body = `{"a":1,"bigNumber":1699999999999999999}`
	path := writeInputFile(t, body)

	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		gotBody = string(data)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)

	t.Run("@file", func(t *testing.T) {
		gotBody = ""
		if _, err := runRoot(t, server.URL, "api", "request", "POST", "/api/v1/console/me",
			"--input", "@"+path, "--json"); err != nil {
			t.Fatalf("api request --input @file: %v", err)
		}
		if gotBody != body {
			t.Errorf("wire body = %q, want the file's bytes %q unchanged", gotBody, body)
		}
	})

	t.Run("@ reads stdin", func(t *testing.T) {
		gotBody = ""
		if _, err := runRootWithStdin(t, server.URL, body, "api", "request", "POST", "/api/v1/console/me",
			"--input", "@", "--json"); err != nil {
			t.Fatalf("api request --input @: %v", err)
		}
		if gotBody != body {
			t.Errorf("wire body = %q, want the stdin bytes %q unchanged", gotBody, body)
		}
	})
}

// TestIssue188AtFileInputDocs pins the docs sync: every input flag's help and
// the CLI skill must advertise the implemented three-form syntax (plain path,
// "@path", "-" / "@" for stdin) so no text teaches a spelling readInputFile
// does not implement.
func TestIssue188AtFileInputDocs(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  *cobra.Command
		flag string
	}{
		{"api request --input", findSub(t, newAPICmd(), "request"), "input"},
		{"api sign-user-attrs --input", findSub(t, newAPICmd(), "sign-user-attrs"), "input"},
		{"apps settings set --input", findSub(t, findSub(t, newAppsCmd(), "settings"), "set"), "input"},
		{"imports create --options", findSub(t, newImportsCmd(), "create"), "options"},
		{"changelog create --body-file", findSub(t, newChangelogCmd(), "create"), "body-file"},
		{"changelog update --body-file", findSub(t, newChangelogCmd(), "update"), "body-file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.cmd.Flags().Lookup(tc.flag)
			if f == nil {
				t.Fatalf("flag --%s not found", tc.flag)
			}
			if !strings.Contains(f.Usage, `"@path"`) {
				t.Errorf("%s usage = %q, want the \"@path\" form documented", tc.name, f.Usage)
			}
		})
	}

	doc := readSkill(t, "cupthread-cli")
	for _, marker := range []string{
		// The two advertised spellings, both implemented as of #188.
		"`--input` (a file path, a curl-style `\"@path\"`, or `\"-\"`/`\"@\"` for stdin)",
		"a file path, `\"@path\"`, or `\"-\"`/`\"@\"` for stdin",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-cli/SKILL.md is missing required @path marker %s", marker)
		}
	}
	if strings.Contains(doc, "`--input @file`") {
		t.Error("cupthread-cli/SKILL.md still teaches the bare `--input @file` shorthand; state all three implemented forms instead")
	}
}
