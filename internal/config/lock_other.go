//go:build !unix

package config

// FileLock is the non-unix stand-in for the advisory config lock; no
// cross-process mutual exclusion is available, so the refresh path keeps its
// historical single-process behavior there.
type FileLock struct{}

// LockConfig is a no-op stub on platforms without flock support.
func LockConfig(path string) (*FileLock, error) { return &FileLock{}, nil }

// Close is a no-op stub on platforms without flock support.
func (l *FileLock) Close() error { return nil }
