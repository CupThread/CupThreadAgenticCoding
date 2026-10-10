//go:build unix

package config

import "os"

// UnsafePermissions stats the config at path and reports its permission bits
// when the file is readable by group or other, i.e. when the credentials
// stored in it are exposed to every local account. Save always writes 0600,
// but nothing restored that hygiene when a backup restore, a permissive-umask
// copy or a dotfiles sync loosened the mode after the fact (issue #186). A
// missing path is not an error — there is nothing on disk to expose — so it
// reports (0, false).
func UnsafePermissions(path string) (os.FileMode, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	mode := fi.Mode().Perm()
	if mode&0o044 != 0 {
		return mode, true
	}
	return mode, false
}

// RepairPermissions tightens the config at path back to 0600. It follows the
// best effort of warnUnsafeConfigPermissions' caller: a failure (read-only
// directory, foreign ownership) degrades to the warning-only path and never
// blocks the command that triggered the check.
func RepairPermissions(path string) error {
	return os.Chmod(path, 0o600)
}
