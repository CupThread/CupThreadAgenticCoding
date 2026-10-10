//go:build unix || windows

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLockConfigSerializesHolders pins the blocking-mutual-exclusion
// semantics the refresh path depends on, on every platform that ships a real
// lock (flock LOCK_EX on unix, LockFileEx with LOCKFILE_EXCLUSIVE_LOCK on
// Windows — issue #193): a second LockConfig on the same path blocks until
// the first holder releases, so concurrent CLI processes cannot enter the
// credential rotation critical section together.
func TestLockConfigSerializesHolders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	first, err := LockConfig(path)
	if err != nil {
		t.Fatalf("first LockConfig: %v", err)
	}
	defer func() { _ = first.Close() }()
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Errorf("lock file not created next to the config: %v", err)
	}

	acquired := make(chan *FileLock, 1)
	go func() {
		second, err := LockConfig(path)
		if err != nil {
			t.Errorf("second LockConfig: %v", err)
			acquired <- nil
			return
		}
		acquired <- second
	}()

	select {
	case lock := <-acquired:
		if lock != nil {
			_ = lock.Close()
		}
		t.Fatal("second LockConfig acquired the lock while the first holder still holds it")
	case <-time.After(150 * time.Millisecond):
		// expected: the second holder is still blocked on the lock
	}

	if err := first.Close(); err != nil {
		t.Fatalf("release first lock: %v", err)
	}
	var second *FileLock
	select {
	case second = <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("second LockConfig never acquired the lock after release")
	}
	if second == nil {
		t.Fatal("second LockConfig returned nil")
	}
	if err := second.Close(); err != nil {
		t.Errorf("close second lock: %v", err)
	}
}

// TestLockConfigCloseIsIdempotent pins the nil-safe Close contract the
// refresh path relies on (deferred Close after an early return): closing a
// nil lock, and closing an already-closed lock, must not panic or error.
func TestLockConfigCloseIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	var nilLock *FileLock
	if err := nilLock.Close(); err != nil {
		t.Fatalf("close nil lock: %v", err)
	}

	lock, err := LockConfig(path)
	if err != nil {
		t.Fatalf("LockConfig: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("close lock: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Errorf("second close: %v", err)
	}

	// A released lock must be immediately re-acquirable by the next
	// holder instead of staying held by the closed handle.
	reacquired, err := LockConfig(path)
	if err != nil {
		t.Fatalf("re-acquire LockConfig after release: %v", err)
	}
	if err := reacquired.Close(); err != nil {
		t.Errorf("close re-acquired lock: %v", err)
	}
}
