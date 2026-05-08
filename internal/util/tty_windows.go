//go:build windows

package util

import "os"

// IsDaemonMode returns true if running in daemon mode (stdin is not a terminal).
// On Windows, we check if stdin is a character device (console).
func IsDaemonMode() bool {
	// On Windows, check if stdin is a character device (console)
	fi, err := os.Stdin.Stat()
	if err != nil {
		return true // Assume daemon mode if we can't determine
	}
	return (fi.Mode() & os.ModeCharDevice) == 0
}
