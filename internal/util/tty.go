//go:build !windows

package util

import (
	"os"

	"golang.org/x/term"
)

// IsDaemonMode returns true if running in daemon mode (stdin is not a terminal).
// This is used to detect when the application is running as a daemon or in a
// non-interactive environment where interactive authentication prompts should be skipped.
func IsDaemonMode() bool {
	return !term.IsTerminal(int(os.Stdin.Fd()))
}
