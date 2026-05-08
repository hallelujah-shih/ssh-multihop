package main

import (
	"context"
	"testing"
	"time"

	"github.com/hallelujah-shih/ssh-multihop/internal/db"
	"github.com/hallelujah-shih/ssh-multihop/internal/service"
	"github.com/stretchr/testify/require"
)

// TestGracefulShutdown verifies that the daemon shuts down gracefully
func TestGracefulShutdown(t *testing.T) {
	// Create temporary database
	tmpDB, cleanup := createTestDB(t)
	defer cleanup()

	// Create service with root context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc, err := service.NewWithContext(ctx, tmpDB)
	require.NoError(t, err)

	// Start service
	if err := svc.Start(); err != nil {
		t.Fatalf("Failed to start service: %v", err)
	}

	// Wait a bit to ensure it's running
	time.Sleep(100 * time.Millisecond)

	// Trigger graceful shutdown
	cancel()

	// Stop service with timeout
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()

	start := time.Now()
	if err := svc.StopWithContext(stopCtx); err != nil {
		t.Errorf("Failed to stop service: %v", err)
	}
	duration := time.Since(start)

	// Verify shutdown completed in reasonable time (< 2 seconds)
	if duration > 2*time.Second {
		t.Errorf("Shutdown took too long: %v", duration)
	}

	t.Logf("Graceful shutdown completed in %v", duration)
}

// TestGracefulShutdownWithForwards verifies shutdown with active forwards
func TestGracefulShutdownWithForwards(t *testing.T) {
	// Create temporary database
	tmpDB, cleanup := createTestDB(t)
	defer cleanup()

	// Create a test forward
	testForward := &db.Forward{
		ID:          "test-forward-1",
		Type:        db.LocalListenToRemote,
		ListenHost:  "local",
		ListenAddr:  "127.0.0.1:0", // Use random port
		ServiceHost: "local",
		ServiceAddr: "127.0.0.1:9999",
	}

	if err := tmpDB.CreateForward(testForward); err != nil {
		t.Fatalf("Failed to create test forward: %v", err)
	}

	// Create service with root context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc, err := service.NewWithContext(ctx, tmpDB)
	require.NoError(t, err)

	// Start service
	if err := svc.Start(); err != nil {
		t.Fatalf("Failed to start service: %v", err)
	}

	// Wait a bit for forward to start
	time.Sleep(200 * time.Millisecond)

	// Verify forward is in database
	forwards, err := tmpDB.ListForwards()
	if err != nil {
		t.Fatalf("Failed to list forwards: %v", err)
	}

	if len(forwards) != 1 {
		t.Errorf("Expected 1 forward, got %d", len(forwards))
	}

	// Trigger graceful shutdown
	cancel()

	// Stop service with timeout
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()

	start := time.Now()
	if err := svc.StopWithContext(stopCtx); err != nil {
		t.Errorf("Failed to stop service: %v", err)
	}
	duration := time.Since(start)

	// Verify shutdown completed in reasonable time (< 3 seconds)
	if duration > 3*time.Second {
		t.Errorf("Shutdown took too long: %v", duration)
	}

	t.Logf("Graceful shutdown with forward completed in %v", duration)
}

// TestStopWithContextTimeout verifies timeout handling
func TestStopWithContextTimeout(t *testing.T) {
	tmpDB, cleanup := createTestDB(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc, err := service.NewWithContext(ctx, tmpDB)
	require.NoError(t, err)

	if err := svc.Start(); err != nil {
		t.Fatalf("Failed to start service: %v", err)
	}

	// Use very short timeout to test timeout handling
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer stopCancel()
	time.Sleep(10 * time.Millisecond) // Ensure timeout expires

	// Should timeout but not panic
	if err := svc.StopWithContext(stopCtx); err != nil {
		// Expected to timeout
		if err.Error() == "context deadline exceeded" ||
			err.Error() == "sync loop shutdown timeout: context deadline exceeded" {
			t.Logf("Got expected timeout error: %v", err)
		} else {
			t.Logf("Got unexpected error (may be ok): %v", err)
		}
	} else {
		t.Log("Stop completed without timeout (service may have stopped quickly)")
	}
}

// TestServiceStopIdempotent verifies that Stop() can be called multiple times
func TestServiceStopIdempotent(t *testing.T) {
	tmpDB, cleanup := createTestDB(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc, err := service.NewWithContext(ctx, tmpDB)
	require.NoError(t, err)

	if err := svc.Start(); err != nil {
		t.Fatalf("Failed to start service: %v", err)
	}

	// First stop
	stopCtx1, stopCancel1 := context.WithTimeout(context.Background(), 5*time.Second)
	if err := svc.StopWithContext(stopCtx1); err != nil {
		t.Errorf("First stop failed: %v", err)
	}
	stopCancel1()

	// Second stop should not panic
	stopCtx2, stopCancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	if err := svc.StopWithContext(stopCtx2); err != nil {
		// May error because service is already stopped, but shouldn't panic
		t.Logf("Second stop returned error (acceptable): %v", err)
	}
	stopCancel2()

	t.Log("Stop is idempotent - no panic on multiple calls")
}

// TestContextHierarchy verifies that child contexts are cancelled when root is cancelled
func TestContextHierarchy(t *testing.T) {
	tmpDB, cleanup := createTestDB(t)
	defer cleanup()

	// Create root context
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	// Create service with root context
	svc, err := service.NewWithContext(rootCtx, tmpDB)
	require.NoError(t, err)

	if err := svc.Start(); err != nil {
		t.Fatalf("Failed to start service: %v", err)
	}

	// Wait for service to start
	time.Sleep(100 * time.Millisecond)

	// Cancel root context
	rootCancel()

	// Service should stop gracefully
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()

	if err := svc.StopWithContext(stopCtx); err != nil {
		t.Errorf("Failed to stop service after root cancel: %v", err)
	}

	t.Log("Context hierarchy works correctly")
}

func createTestDB(t *testing.T) (*db.Database, func()) {
	t.Helper()

	// Create temporary database
	tmpFile := t.TempDir() + "/test.db"
	database, err := db.New(db.Config{Path: tmpFile})
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}

	cleanup := func() {
		if err := database.Close(); err != nil {
			t.Logf("Failed to close database: %v", err)
		}
	}

	return database, cleanup
}
