//go:build unix

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestUnsafePermissions pins the load-time exposure check (issue #186): a
// file readable by group or other is flagged with its actual mode, an
// owner-only file is not, and a missing path is silently reported safe —
// there is nothing on disk to expose, and a first run must not trip on the
// config it has not created yet.
func TestUnsafePermissions(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, mode os.FileMode) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("{}"), mode); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatalf("chmod %s: %v", name, err)
		}
		return path
	}

	cases := []struct {
		name   string
		path   string
		mode   os.FileMode
		unsafe bool
	}{
		{name: "world-readable", path: write("world-readable.json", 0o644), mode: 0o644, unsafe: true},
		{name: "group-readable", path: write("group-readable.json", 0o640), mode: 0o640, unsafe: true},
		{name: "owner-only", path: write("owner-only.json", 0o600), mode: 0o600, unsafe: false},
		{name: "owner-read-only", path: write("owner-read-only.json", 0o400), mode: 0o400, unsafe: false},
		{name: "missing path", path: filepath.Join(dir, "absent.json"), mode: 0, unsafe: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, unsafe := UnsafePermissions(tc.path)
			if unsafe != tc.unsafe {
				t.Fatalf("UnsafePermissions(%s) unsafe = %v, want %v", tc.path, unsafe, tc.unsafe)
			}
			if mode != tc.mode {
				t.Errorf("UnsafePermissions(%s) mode = %04o, want %04o", tc.path, mode, tc.mode)
			}
		})
	}
}

// TestRepairPermissions verifies the automatic tightening restores the 0600
// hygiene Save itself writes.
func TestRepairPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := RepairPermissions(path); err != nil {
		t.Fatalf("RepairPermissions: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("perms after repair = %04o, want 600", perm)
	}
}
