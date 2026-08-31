package cmd

import (
	"io"
	"os"
)

// readInputFile reads flag input where "@" or "-" means stdin, otherwise a
// filesystem path.
func readInputFile(path string) ([]byte, error) {
	if path == "@" || path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}
