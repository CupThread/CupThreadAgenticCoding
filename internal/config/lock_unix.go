//go:build unix

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// FileLock is an exclusive advisory lock held on an open descriptor. The
// kernel releases it when the descriptor closes or the process exits, so a
// crashed holder can never wedge the lock file shut.
type FileLock struct {
	f *os.File
}

// LockSupported reports whether LockConfig provides real cross-process
// mutual exclusion on this platform.
const LockSupported = true

// LockConfig takes an exclusive advisory lock (flock LOCK_EX, blocking) on
// <path>.lock, serializing credential rotation across concurrent CLI
// processes. The lock file is created next to the config if missing and is
// never removed, so concurrent openers always bind to the same inode. The
// config directory is created if missing (mode 0700, matching Save): the
// lock file's O_CREATE cannot create parent directories, and every caller
// — Update and refreshAcrossProcesses alike — locks before the first Save
// can run, so without this a pristine machine (no ~/.config/cupthread yet)
// fails every credential write with ENOENT (issue #268).
func LockConfig(path string) (*FileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create config directory: %w", err)
	}
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
