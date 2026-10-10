//go:build unix

package cmd

import (
	"errors"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Unix-only tests for the --icon special-file rejection (issue #192): a FIFO
// at --icon must fail fast with "not a regular file" — neither the open(2)
// call nor the read may park the command waiting for a writer — and no
// request may be sent.

// runWithDeadline fails the test if fn does not return within d: a regression
// here is a command hanging forever on a FIFO, and a hung test is the failure
// mode this contract forbids.
func runIconWithDeadline(t *testing.T, d time.Duration, fn func() (string, error)) (string, error) {
	t.Helper()
	type result struct {
		out string
		err error
	}
	ch := make(chan result, 1)
	go func() {
		out, err := fn()
		ch <- result{out, err}
	}()
	select {
	case r := <-ch:
		return r.out, r.err
	case <-time.After(d):
		t.Fatalf("command did not return within %s — it is blocked on the FIFO instead of failing fast", d)
		return "", nil
	}
}

// TestAppsUpdateIconFIFORejectedBeforeAnyRequest covers the command path: the
// advisory Stat in validateAppsUpdateFlags rejects the FIFO before the client
// is even built, so the error names the file and the server sees no request.
func TestAppsUpdateIconFIFORejectedBeforeAnyRequest(t *testing.T) {
	server, sent := countingServer(t)

	fifo := filepath.Join(t.TempDir(), "icon.fifo")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}

	out, err := runIconWithDeadline(t, 10*time.Second, func() (string, error) {
		return runRoot(t, server.URL, "apps", "update", "app_1", "--icon", fifo, "--workspace", "ws_1", "--json")
	})
	if err == nil {
		t.Fatal("apps update succeeded with a FIFO icon, want a local failure")
	}
	if *sent != 0 {
		t.Errorf("server observed %d requests, want 0", *sent)
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("error = %q, want it to name the non-regular file", err.Error())
	}
	if out != "" {
		t.Errorf("structured stdout = %q, want it empty (only input_too_large renders a document)", out)
	}
}

// TestReadIconFileRejectsFIFOAtReadTime is the read-time counterpart (the
// TOCTOU window: the file passed the advisory Stat, then was swapped for a
// FIFO): the O_NONBLOCK open returns immediately, the descriptor's own kind
// is rejected, and nothing blocks.
func TestReadIconFileRejectsFIFOAtReadTime(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "swap.fifo")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := readIconFile(fifo)
		done <- err
	}()
	select {
	case err := <-done:
		var nrf *NotRegularFileError
		if !errors.As(err, &nrf) {
			t.Fatalf("readIconFile(fifo) err = %v, want NotRegularFileError", err)
		}
		if nrf.Path != fifo {
			t.Errorf("path = %q, want %q", nrf.Path, fifo)
		}
		if !strings.Contains(err.Error(), "invalid --icon") {
			t.Errorf("error = %q, want the command-facing invalid --icon wording", err.Error())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("readIconFile blocked on the FIFO — the O_NONBLOCK open or the regular-file check is missing")
	}
}
