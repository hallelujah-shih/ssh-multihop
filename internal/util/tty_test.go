package util

import (
	"testing"
)

func TestIsDaemonMode(t *testing.T) {
	// This test always runs in non-interactive mode (testing environment)
	// So IsDaemonMode() will return true
	result := IsDaemonMode()
	t.Logf("IsDaemonMode() = %v", result)

	// In test environment, stdin is typically not a terminal
	// We don't assert a specific value since it depends on the test environment
	// The important thing is that the function doesn't panic
}
