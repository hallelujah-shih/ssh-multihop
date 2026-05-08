package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hallelujah-shih/ssh-multihop/internal/util"
)

// expandPath expands a file path with tilde (~) and environment variables.
// Supports:
// - ~/path or ~user/path (tilde expansion)
// - $VAR/path or ${VAR}/path (environment variable expansion)
func expandPath(path string) (string, error) {
	if path == "" {
		return "", nil
	}

	// Expand tilde
	expanded, err := expandTilde(path)
	if err != nil {
		return "", err
	}

	// Expand environment variables
	expanded = os.ExpandEnv(expanded)

	return expanded, nil
}

// expandTilde expands a leading tilde (~) to the user's home directory.
// Supports both ~ and ~user syntax.
func expandTilde(path string) (string, error) {
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}

	// Split on first slash to separate user from rest
	parts := strings.SplitN(path, "/", 2)

	var homeDir string
	var err error

	if len(parts[0]) == 1 {
		// Just ~, use current user's home directory
		homeDir, err = util.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %w", err)
		}
	} else {
		// ~user syntax - get user's home directory
		username := parts[0][1:] // Remove ~
		homeDir, err = getUserHomeDir(username)
		if err != nil {
			return "", fmt.Errorf("failed to get home directory for user %q: %w", username, err)
		}
	}

	// Reconstruct path with expanded home directory
	if len(parts) == 1 {
		return homeDir, nil
	}

	return filepath.Join(homeDir, parts[1]), nil
}

// getUserHomeDir gets the home directory for a specific user.
// This is a simplified version that works on most Unix-like systems.
func getUserHomeDir(username string) (string, error) {
	// Try to get from system
	// On Unix, home directories are typically /home/username or /users/username

	// Common home directory locations
	possiblePaths := []string{
		filepath.Join("/home", username),
		filepath.Join("/users", username),
	}

	for _, path := range possiblePaths {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("home directory not found for user %q", username)
}
