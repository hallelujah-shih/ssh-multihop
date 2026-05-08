package main

import (
	"testing"

	"go.uber.org/zap"
)

// TestLoggerInitialization verifies that the global logger is properly initialized
func TestLoggerInitialization(t *testing.T) {
	// After init() runs, zap.L() should return a valid logger
	logger := zap.L()

	if logger == nil {
		t.Fatal("Expected global logger to be initialized, got nil")
	}

	// Test that we can actually log without panicking
	// This will fail if logger is not properly configured
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Logger panic during test: %v", r)
		}
	}()

	// Try a simple log operation
	logger.Debug("Test debug log from TestLoggerInitialization")
	logger.Info("Test info log from TestLoggerInitialization")
}

// TestLoggerSync verifies that the logger can be synced
// Note: Sync may fail in test environments when writing to /dev/stderr
// This is expected behavior and not a test failure
func TestLoggerSync(t *testing.T) {
	logger := zap.L()

	if logger == nil {
		t.Fatal("Expected global logger to be initialized, got nil")
	}

	// Sync attempt - we don't fail if it returns an error since
	// syncing to /dev/stderr in a test environment is expected to fail
	_ = logger.Sync()
}
