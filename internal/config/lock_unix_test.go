//go:build unix

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLockConfigSerializesHolders pins the flock semantics the refresh path
// depends on: a second LockConfig on the same path blocks until the first
// holder releases, so concurrent CLI processes cannot enter the credential
// rotation critical section together.
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
		// expected: the second holder is still blocked on the flock
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
