//go:build linux

package cmd

import (
	"syscall"
	"unsafe"
)

// stdinIsInteractive reports whether the file descriptor is a real terminal
// by issuing the TCGETS ioctl. A Stat-based ModeCharDevice check alone is
// not enough: /dev/null is a character device too, so scripts running with
// stdin redirected from it would be misclassified as interactive.
func stdinIsInteractive(fd uintptr) bool {
	var termios [128]byte // >= sizeof(struct termios) on every linux variant
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&termios[0])))
	return errno == 0
}
