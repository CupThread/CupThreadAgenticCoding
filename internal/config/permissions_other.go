//go:build !unix

package config

import "os"

// UnsafePermissions is a no-op on platforms without meaningful permission
// bits (issue #186): a Windows os.FileMode only carries the read-only bit,
// so no group/world exposure can be detected there — and none is reported.
func UnsafePermissions(path string) (os.FileMode, bool) {
	return 0, false
}

// RepairPermissions is a no-op on platforms without meaningful permission
// bits: Chmod there can only toggle the read-only bit, which is not a
// credential-exposure repair.
func RepairPermissions(path string) error {
	return nil
}
