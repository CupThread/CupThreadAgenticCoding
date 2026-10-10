//go:build windows

package config

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// FileLock is an exclusive lock held on an open handle. The kernel releases
// byte-range locks when the handle closes or the process exits, so a crashed
// holder can never wedge the lock file shut.
type FileLock struct {
	f *os.File
}

// LockSupported reports whether LockConfig provides real cross-process
// mutual exclusion on this platform.
const LockSupported = true

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

const (
	lockfileExclusiveLock = 0x00000002 // LOCKFILE_EXCLUSIVE_LOCK
	// Both halves of the 64-bit range length set to 0xFFFFFFFF lock offset 0
	// through 2^64-1 — the whole file at any size. The lock file is always
	// empty, but the maximal range keeps the lock independent of its size.
	lockWholeFile = 0xFFFFFFFF
)

// LockConfig takes an exclusive blocking lock (LockFileEx with
// LOCKFILE_EXCLUSIVE_LOCK and without LOCKFILE_FAIL_IMMEDIATELY) on
// <path>.lock, serializing credential rotation across concurrent CLI
// processes — the Windows counterpart of the unix flock LOCK_EX path in
// lock_unix.go (issue #193). The lock file is created next to the config if
// missing and is never removed, so concurrent openers always open the same
// file.
func LockConfig(path string) (*FileLock, error) {
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open config lock %s.lock: %w", path, err)
	}
	var ol syscall.Overlapped // zero value: the locked range starts at offset 0
	r1, _, callErr := procLockFileEx.Call(
		f.Fd(),
		lockfileExclusiveLock,
		0,             // dwReserved, must be zero
		lockWholeFile, // range length, low
		lockWholeFile, // range length, high
		uintptr(unsafe.Pointer(&ol)),
	)
	if r1 == 0 {
		_ = f.Close()
		return nil, fmt.Errorf("lock config %s.lock: %w", path, callErr)
	}
	return &FileLock{f: f}, nil
}

// Close releases the lock and the underlying handle. The kernel drops the
// byte-range lock on handle close regardless, so an UnlockFileEx failure
// never leaves the file permanently locked.
func (l *FileLock) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	var ol syscall.Overlapped
	r1, _, callErr := procUnlockFileEx.Call(
		l.f.Fd(),
		0, // dwReserved, must be zero
		lockWholeFile,
		lockWholeFile,
		uintptr(unsafe.Pointer(&ol)),
	)
	var err error
	if r1 == 0 {
		err = fmt.Errorf("unlock config lock: %w", callErr)
	}
	if cerr := l.f.Close(); err == nil {
		err = cerr
	}
	l.f = nil
	return err
}
