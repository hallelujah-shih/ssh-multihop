package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

// TestCheckExternalAgent_NoEnvVar tests the case where SSH_AUTH_SOCK is not set
func TestCheckExternalAgent_NoEnvVar(t *testing.T) {
	// Ensure SSH_AUTH_SOCK is not set
	unsetEnv(t, "SSH_AUTH_SOCK")

	logger := zaptest.NewLogger(t)
	defer func() {
		_ = logger.Sync()
	}()

	// Override the global logger for this test
	old := zap.L()
	zap.ReplaceGlobals(logger)
	defer func() { zap.ReplaceGlobals(old) }()

	socketPath, keyCount, err := CheckExternalAgent()

	assert.NoError(t, err, "CheckExternalAgent should not return an error when SSH_AUTH_SOCK is not set")
	assert.Empty(t, socketPath, "Socket path should be empty when SSH_AUTH_SOCK is not set")
	assert.Zero(t, keyCount, "Key count should be zero when SSH_AUTH_SOCK is not set")
}

// TestCheckExternalAgent_InvalidSocket tests the case where SSH_AUTH_SOCK points to a non-existent file
func TestCheckExternalAgent_InvalidSocket(t *testing.T) {
	// Set SSH_AUTH_SOCK to a non-existent path
	setEnv(t, "SSH_AUTH_SOCK", "/nonexistent/path/to/agent.sock")

	logger := zaptest.NewLogger(t)
	defer func() {
		_ = logger.Sync()
	}()

	// Override the global logger for this test
	old := zap.L()
	zap.ReplaceGlobals(logger)
	defer func() { zap.ReplaceGlobals(old) }()

	socketPath, keyCount, err := CheckExternalAgent()

	assert.NoError(t, err, "CheckExternalAgent should not return an error for invalid socket")
	assert.Empty(t, socketPath, "Socket path should be empty for invalid socket")
	assert.Zero(t, keyCount, "Key count should be zero for invalid socket")
}

// TestCheckExternalAgent_NotASocket tests the case where SSH_AUTH_SOCK points to a regular file
func TestCheckExternalAgent_NotASocket(t *testing.T) {
	// Create a temporary file (not a socket)
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "not-a-socket")
	err := os.WriteFile(tmpFile, []byte("test"), 0600)
	assert.NoError(t, err, "Failed to create temp file")

	setEnv(t, "SSH_AUTH_SOCK", tmpFile)

	logger := zaptest.NewLogger(t)
	defer func() {
		_ = logger.Sync()
	}()

	// Override the global logger for this test
	old := zap.L()
	zap.ReplaceGlobals(logger)
	defer func() { zap.ReplaceGlobals(old) }()

	socketPath, keyCount, err := CheckExternalAgent()

	assert.NoError(t, err, "CheckExternalAgent should not return an error for non-socket file")
	assert.Empty(t, socketPath, "Socket path should be empty for non-socket file")
	assert.Zero(t, keyCount, "Key count should be zero for non-socket file")
}

// setEnv sets an environment variable for the duration of the test
func setEnv(t *testing.T, key, value string) {
	t.Helper()
	original := os.Getenv(key)
	t.Cleanup(func() {
		if original == "" {
			_ = os.Unsetenv(key)
		} else {
			_ = os.Setenv(key, original)
		}
	})
	err := os.Setenv(key, value)
	assert.NoError(t, err, "Failed to set environment variable")
}

// unsetEnv unsets an environment variable for the duration of the test
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	original := os.Getenv(key)
	t.Cleanup(func() {
		if original != "" {
			_ = os.Setenv(key, original)
		}
	})
	err := os.Unsetenv(key)
	assert.NoError(t, err, "Failed to unset environment variable")
}
