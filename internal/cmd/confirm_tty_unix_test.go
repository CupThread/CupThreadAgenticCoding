//go:build darwin || linux

package cmd

import (
	"os"
	"testing"
)

// TestStdinIsInteractivePipeAndDevNull pins the ioctl-based detection: a
// pipe and /dev/null are both non-terminals even though /dev/null is a
// character device — the Stat-based heuristic this replaced misclassified
// scripts running with stdin from /dev/null as interactive.
func TestStdinIsInteractivePipeAndDevNull(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	if stdinIsInteractive(r.Fd()) {
		t.Error("pipe stdin must not be detected as interactive")
	}

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	t.Cleanup(func() { devNull.Close() })
	if stdinIsInteractive(devNull.Fd()) {
		t.Errorf("%s must not be detected as interactive", os.DevNull)
	}
}
