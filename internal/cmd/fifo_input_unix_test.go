//go:build unix

package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Unix-only tests for the shared flag-input FIFO rejection (issue #269): every
// flag path reading a file through readInputFile must fail fast on a FIFO —
// the O_NONBLOCK open and the descriptor's own kind check of the --icon
// discipline (issue #192) — before any HTTP request, instead of parking in
// open(2) past SIGTERM and surviving every orchestrator's timeout.

// TestReadInputFileWriterlessFIFORejectedFast is the unit-level hang detector:
// a writer-less FIFO must come back with NotRegularFileError within the
// deadline, or the O_NONBLOCK open or the regular-file check has regressed and
// the command is parked in open(2) again.
func TestReadInputFileWriterlessFIFORejectedFast(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "input.fifo")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := readInputFile(fifo, 1024)
		done <- err
	}()
	select {
	case err := <-done:
		var nrf *NotRegularFileError
		if !errors.As(err, &nrf) {
			t.Fatalf("readInputFile(fifo) err = %v, want NotRegularFileError", err)
		}
		if nrf.Path != fifo {
			t.Errorf("path = %q, want %q", nrf.Path, fifo)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("readInputFile blocked on the writer-less FIFO — the O_NONBLOCK open or the regular-file check is missing")
	}
}

// TestReadInputFileAttachedFIFOIsRejected pins the post-fix semantics for the
// friendly case: even with a writer attached, a FIFO is not a regular file and
// is rejected. A streaming read could cap its bytes, but it cannot tell a
// producer that will ever write from one that never will, so the deterministic
// local error beats both the old writer-less hang and a silent empty read.
func TestReadInputFileAttachedFIFOIsRejected(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "input.fifo")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}

	// Hold a read end open first — an O_NONBLOCK read open succeeds without a
	// writer — so the writer's blocking O_WRONLY open attaches immediately
	// instead of parking on readInputFile to unblock it. readInputFile is
	// gated on the writer's confirmed attachment so the test exercises the
	// attached-writer state it is named for, and both ends are closed after
	// the assertion.
	r, err := os.OpenFile(fifo, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatalf("open FIFO read end: %v", err)
	}
	defer r.Close()

	attached := make(chan *os.File, 1)
	go func() {
		w, err := os.OpenFile(fifo, os.O_WRONLY, 0)
		if err != nil {
			attached <- nil
			return
		}
		attached <- w
	}()
	var w *os.File
	select {
	case w = <-attached:
	case <-time.After(2 * time.Second):
		t.Fatal("writer never attached to the FIFO — the read end is not open")
	}
	if w == nil {
		t.Fatal("writer failed to open the FIFO write end")
	}
	defer w.Close()

	done := make(chan error, 1)
	go func() {
		_, err := readInputFile(fifo, 1024)
		done <- err
	}()
	select {
	case err := <-done:
		var nrf *NotRegularFileError
		if !errors.As(err, &nrf) {
			t.Fatalf("readInputFile(attached fifo) err = %v, want NotRegularFileError", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("readInputFile blocked on a writer-attached FIFO — the regular-file check is missing")
	}
}

// TestIssue269FIFORejectedBeforeAnyRequest covers the command paths: every
// flag that reads a file through readInputFile fails on a FIFO before any
// HTTP request, with the flag named in the error (the --icon wording
// precedent) and stdout left parse-clean in --json mode.
func TestIssue269FIFORejectedBeforeAnyRequest(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "body.fifo")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}

	for _, tc := range []struct {
		name     string
		args     []string
		wantFlag string
	}{
		{"api request --input @fifo", []string{"api", "request", "GET", "/api/v1/console/me",
			"--input", "@" + fifo, "--json"}, "invalid --input"},
		{"api sign-user-attrs --input @fifo", []string{"api", "sign-user-attrs", "--app-key", "app_1",
			"--input", "@" + fifo, "--secret", "s", "--json"}, "invalid --input"},
		{"changelog create --body-file", []string{"changelog", "create", "--title", "t",
			"--body-file", fifo, "--json"}, "invalid --body-file"},
		{"imports create --options", []string{"imports", "create", "--workspace", "ws_1",
			"--app", "app_1", "--source", "github_issues", "--options", fifo, "--json"}, "invalid --options"},
		{"apps settings set --input", []string{"apps", "settings", "set", "app_1",
			"--workspace", "ws_1", "--input", fifo, "--json"}, "invalid --input"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, sent := countingServer(t)

			out, err := runWithDeadline(t, 10*time.Second, func() (string, error) {
				return runRoot(t, server.URL, tc.args...)
			})
			if err == nil {
				t.Fatal("command succeeded with a FIFO input, want a local failure")
			}
			if *sent != 0 {
				t.Errorf("server observed %d requests, want 0 — the FIFO must be rejected before any request", *sent)
			}
			if !strings.Contains(err.Error(), "not a regular file") {
				t.Errorf("error = %q, want it to name the non-regular file", err.Error())
			}
			if !strings.Contains(err.Error(), tc.wantFlag) {
				t.Errorf("error = %q, want the %s wording naming the flag", err.Error(), tc.wantFlag)
			}
			if out != "" {
				t.Errorf("structured stdout = %q, want it empty (only input_too_large renders a document)", out)
			}
		})
	}
}
