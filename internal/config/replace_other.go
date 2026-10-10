//go:build !windows

package config

import (
	"fmt"
	"os"
)

// replaceConfig atomically moves tmpName over path. POSIX rename replaces the
// destination unconditionally — a concurrent reader holds no lock that blocks
// it, and concurrent writers' renames serialize in the kernel — so the move
// needs no retry.
func replaceConfig(tmpName, path string) error {
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}
