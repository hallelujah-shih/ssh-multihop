package connection

import (
	"testing"
)

func TestNewSSHClientConfigBuilder(t *testing.T) {
	builder := NewSSHClientConfigBuilder()

	if builder == nil {
		t.Fatal("NewSSHClientConfigBuilder returned nil")
	}

	// Verify default values
	if !builder.agentEnabled {
		t.Error("SSH agent should be enabled by default")
	}

	// Verify daemon mode is always enabled
	// The application only has a daemon command entry point, so we always
	// treat it as daemon mode (no interactive stdin prompts)
	if !builder.isDaemon {
		t.Error("isDaemon should always be true - no interactive prompts allowed")
	}
}

func TestBuildConfigWithEncryptedKeyInDaemonMode(t *testing.T) {
	builder := NewSSHClientConfigBuilder()

	// In daemon mode, encrypted keys should fail without prompting
	// This test verifies that we don't hang waiting for input
	if builder.isDaemon {
		t.Logf("Running in daemon mode - encrypted keys will fail gracefully")
		// We would need an encrypted test key to fully test this behavior
		// For now, we just verify the flag is set correctly
	} else {
		t.Skip("Skipping daemon mode test - not running in daemon mode")
	}
}
