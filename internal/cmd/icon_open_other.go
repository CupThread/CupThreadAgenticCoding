//go:build !unix

package cmd

import "os"

// openInputFile is the non-unix counterpart of the unix O_NONBLOCK open:
// a plain open. Windows named pipes do not park open(2) the way unix FIFOs
// do, and readRegularFile still rejects whatever non-regular file does open
// via the descriptor's own kind (issue #192).
func openInputFile(path string) (*os.File, error) {
	return os.Open(path)
}
