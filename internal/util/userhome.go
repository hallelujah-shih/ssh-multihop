// Package util provides utility functions for address parsing, SSH helpers, and user home directory resolution.
//
// The package handles setuid/setgid scenarios correctly for home directory lookup
// using direct system database queries instead of environment variables.
package util

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
)

// UserHomeDir returns the current user's home directory based on the
// real UID of the process, not the HOME environment variable.
//
// This is more reliable than os.UserHomeDir() when the process is
// started with setuid/setgid or similar mechanisms, because it queries
// the system user database directly using the real UID instead of
// the effective UID.
//
// Falls back to os.UserHomeDir() if lookup fails.
func UserHomeDir() (string, error) {
	// Get real UID (not effective UID)
	uid := os.Getuid()

	// Look up user by real UID
	currentUser, err := user.LookupId(fmt.Sprintf("%d", uid))
	if err != nil {
		// Fallback to os.UserHomeDir() if we can't look up user
		return os.UserHomeDir()
	}

	homeDir := currentUser.HomeDir
	if homeDir == "" {
		return os.UserHomeDir()
	}

	return filepath.Clean(homeDir), nil
}
