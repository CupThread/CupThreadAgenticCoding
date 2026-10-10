//go:build !unix && !windows

package config

// FileLock is the stand-in for the advisory config lock on platforms this
// CLI supports building for but that offer no byte-range locking here
// (js/wasm, plan9); no cross-process mutual exclusion is available, so the
// refresh path keeps its historical single-process behavior there. Windows
// and unix get real locks (lock_windows.go / lock_unix.go).
type FileLock struct{}

// LockSupported reports whether LockConfig provides real cross-process
// mutual exclusion on this platform.
const LockSupported = false

// LockConfig is a no-op stub on platforms without locking support.
func LockConfig(path string) (*FileLock, error) { return &FileLock{}, nil }

// Close is a no-op stub on platforms without locking support.
func (l *FileLock) Close() error { return nil }
