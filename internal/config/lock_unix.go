//go:build unix

package config

import (
	"fmt"
	"os"
	"syscall"
)

// FileLock is an exclusive advisory lock held on an open descriptor. The
// kernel releases it when the descriptor closes or the process exits, so a
// crashed holder can never wedge the lock file shut.
type FileLock struct {
	f *os.File
}

// LockConfig takes an exclusive advisory lock (flock LOCK_EX, blocking) on
// <path>.lock, serializing credential rotation across concurrent CLI
// processes. The lock file is created next to the config if missing and is
// never removed, so concurrent openers always bind to the same inode.
func LockConfig(path string) (*FileLock, error) {
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open config lock %s.lock: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock config %s.lock: %w", path, err)
	}
	return &FileLock{f: f}, nil
}

// Close releases the lock and the underlying descriptor.
func (l *FileLock) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	if cerr := l.f.Close(); err == nil {
		err = cerr
	}
	l.f = nil
	return err
}
