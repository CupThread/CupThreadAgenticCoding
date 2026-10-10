//go:build unix

package cmd

import (
	"os"
	"syscall"
)

// openInputFile opens path for reading without honoring unix FIFO blocking
// semantics: O_NONBLOCK makes open(2) return immediately on a named pipe with
// no writer, so readRegularFile can fstat the descriptor and reject it as
// non-regular instead of parking the command forever (issue #192). The flag
// changes nothing about how regular files open or read.
func openInputFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
