//go:build !darwin && !linux

package cmd

import "os"

// stdinIsInteractive falls back to a character-device check on platforms
// where the termios ioctl is not wired up. Destructive commands treat a
// false negative safely: they require --yes instead of prompting.
func stdinIsInteractive(fd uintptr) bool {
	fi, err := os.NewFile(fd, "").Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
