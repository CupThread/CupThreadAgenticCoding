//go:build windows

package config

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// replaceRetryBudget bounds how long a contended replace keeps retrying before
// the last error is reported: the contention below is transient, but an
// indexer or antivirus scan can hold a freshly replaced file for a while.
const replaceRetryBudget = 2 * time.Second

// replaceConfig atomically moves tmpName over path. Concurrent replaces of one
// destination transiently fail on Windows: MoveFileEx's REPLACE_EXISTING needs
// delete access to the target, and another writer's in-flight rename (or a
// scanner holding the freshly replaced file) keeps it busy for milliseconds,
// surfacing as ERROR_ACCESS_DENIED, ERROR_SHARING_VIOLATION or
// ERROR_LOCK_VIOLATION. Those are retried with a ramping backoff inside the
// budget — the loser of a race succeeds a moment later — while every other
// error surfaces immediately.
func replaceConfig(tmpName, path string) error {
	deadline := time.Now().Add(replaceRetryBudget)
	backoff := time.Millisecond
	for {
		err := os.Rename(tmpName, path)
		if err == nil {
			return nil
		}
		if !retryableRenameErr(err) || !time.Now().Before(deadline) {
			return fmt.Errorf("replace config: %w", err)
		}
		time.Sleep(backoff)
		if backoff < 20*time.Millisecond {
			backoff *= 2
		}
	}
}

// Win32 error codes the standard library syscall package does not carry (they
// live in golang.org/x/sys/windows, which this module does not depend on).
// The values are part of the stable Win32 error space.
const (
	ERROR_SHARING_VIOLATION syscall.Errno = 32
	ERROR_LOCK_VIOLATION    syscall.Errno = 33
)

// retryableRenameErr reports whether err is one of the Windows rename failures
// a competing writer or file scanner makes transient. os.ErrPermission matches
// ERROR_ACCESS_DENIED through syscall.Errno.Is; the sharing and lock
// violations are compared by their own Errno values.
func retryableRenameErr(err error) bool {
	return errors.Is(err, os.ErrPermission) ||
		errors.Is(err, ERROR_SHARING_VIOLATION) ||
		errors.Is(err, ERROR_LOCK_VIOLATION)
}
