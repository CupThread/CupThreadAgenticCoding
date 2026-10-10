package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for the --icon bounded-read contract (issue #192, the --icon site of
// the #142 REL-2 discipline): apps update reads the icon through
// readRegularFile/readBounded — from a verified regular file, capped at
// maxIconBytes — before any HTTP request, so oversized input fails locally
// with the input_too_large document instead of buffering toward the server's
// 413, and special files are rejected instead of hanging the command.

// TestAppsUpdateIconOverLimitRegularFileFailsBeforeRequest covers the advisory
// Stat fast-fail: a regular file over the 10 MB cap fails up front with the
// rich human detail naming the file's actual size, renders the same
// input_too_large document in --json mode, and the test server observes no
// request at all.
func TestAppsUpdateIconOverLimitRegularFileFailsBeforeRequest(t *testing.T) {
	server, sent := countingServer(t)

	iconPath := filepath.Join(t.TempDir(), "icon.png")
	if err := os.WriteFile(iconPath, make([]byte, maxIconBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runRoot(t, server.URL, "apps", "update", "app_1", "--icon", iconPath, "--workspace", "ws_1", "--json")
	if err == nil {
		t.Fatal("apps update succeeded with an over-limit icon, want a local failure")
	}
	if *sent != 0 {
		t.Errorf("server observed %d requests, want 0 — the icon read must precede every request", *sent)
	}
	if !strings.Contains(err.Error(), "the app-icon limit is 10 MB") {
		t.Errorf("error = %q, want the human detail naming the 10 MB cap", err.Error())
	}
	var payload struct {
		Code  string `json:"code"`
		Limit int64  `json:"limit"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
		t.Fatalf("stdout %q is not one JSON document: %v", out, err)
	}
	if payload.Code != "input_too_large" || payload.Limit != maxIconBytes {
		t.Errorf("payload = %+v, want code input_too_large with limit %d", payload, maxIconBytes)
	}
}

// TestAppsUpdateIconOverLimitTableKeepsStdoutClean is the table-mode
// counterpart: the human detail goes to stderr and stdout stays parse-clean.
func TestAppsUpdateIconOverLimitTableKeepsStdoutClean(t *testing.T) {
	server, sent := countingServer(t)

	iconPath := filepath.Join(t.TempDir(), "icon.png")
	if err := os.WriteFile(iconPath, make([]byte, maxIconBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runRoot(t, server.URL, "apps", "update", "app_1", "--icon", iconPath, "--workspace", "ws_1")
	if err == nil {
		t.Fatal("apps update succeeded with an over-limit icon, want a local failure")
	}
	if *sent != 0 {
		t.Errorf("server observed %d requests, want 0", *sent)
	}
	if out != "" {
		t.Errorf("table mode stdout = %q, want it empty (detail belongs on stderr)", out)
	}
}

// TestReadIconFileBoundedAtReadTime proves the read, not the Stat, is the
// enforcement point: readIconFile caps the bytes at exactly maxIconBytes, so
// a file one byte under or at the cap passes whole and one byte over fails
// with an InputTooLargeError carrying the icon cap — even though Stat never
// rejected it here (readIconFile does no Stat of its own beyond the
// regular-file check on the opened descriptor).
func TestReadIconFileBoundedAtReadTime(t *testing.T) {
	for _, tc := range []struct {
		size    int64
		wantErr bool
	}{{maxIconBytes - 1, false}, {maxIconBytes, false}, {maxIconBytes + 1, true}} {
		iconPath := filepath.Join(t.TempDir(), "icon.png")
		if err := os.WriteFile(iconPath, make([]byte, tc.size), 0o644); err != nil {
			t.Fatal(err)
		}
		data, err := readIconFile(iconPath)
		if tc.wantErr {
			var tooLarge *InputTooLargeError
			if !errors.As(err, &tooLarge) {
				t.Fatalf("size %d: err = %v, want InputTooLargeError", tc.size, err)
			}
			if tooLarge.Limit != maxIconBytes {
				t.Errorf("size %d: limit = %d, want %d", tc.size, tooLarge.Limit, maxIconBytes)
			}
			continue
		}
		if err != nil {
			t.Fatalf("size %d: readIconFile: %v", tc.size, err)
		}
		if int64(len(data)) != tc.size {
			t.Errorf("size %d: read %d bytes, want the file whole", tc.size, len(data))
		}
	}
}

// TestReadIconFileWrapsNotRegular pins the command-facing wording: a
// non-regular descriptor surfaces as "invalid --icon …: not a regular file".
// (The FIFO itself is exercised in icon_input_unix_test.go; a directory is
// the portable non-regular stand-in that still reaches readIconFile when
// called directly.)
func TestReadIconFileWrapsNotRegular(t *testing.T) {
	dir := t.TempDir()
	_, err := readIconFile(dir)
	var nrf *NotRegularFileError
	if !errors.As(err, &nrf) {
		t.Fatalf("readIconFile(dir) err = %v, want NotRegularFileError", err)
	}
	if nrf.Path != dir {
		t.Errorf("path = %q, want %q", nrf.Path, dir)
	}
}
