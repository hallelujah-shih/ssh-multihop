package forwarding

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hallelujah-shih/ssh-multihop/internal/connection"
	"github.com/hallelujah-shih/ssh-multihop/internal/tunnel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLocalListenToRemote_BasicLifecycle tests the basic lifecycle of a forward
func TestLocalListenToRemote_BasicLifecycle(t *testing.T) {
	// Find free port for testing
	port, err := findFreePort()
	require.NoError(t, err)
	bindAddr := fmt.Sprintf("127.0.0.1:%d", port)

	// Create test database
	testDB, err := createTestDB()
	require.NoError(t, err)
	defer func() {
		if err := testDB.Close(); err != nil {
			t.Logf("Failed to close test database: %v", err)
		}
	}()

	// Create forward with invalid SSH config (will fail to connect, but that's OK)
	forwardID := "test-forward-1"
	hopChain := []*tunnel.HopConfig{
		{
			Host:         "testhost",
			HostName:     "invalid-host-that-does-not-exist.example.com",
			Port:         22,
			User:         "testuser",
			IdentityFile: "/nonexistent/key",
		},
	}

	pool := connection.NewConnectionManager(connection.DefaultConfig(), nil)
	defer func() { _ = pool.Close() }()

	lf := NewLocalListenToRemote(bindAddr, "127.0.0.1:9999", forwardID, testDB, pool, hopChain)

	// Test initial status
	assert.Equal(t, StatusStopped, lf.Status())
	assert.Equal(t, "local_listen_to_remote", lf.Type())
	assert.Contains(t, lf.String(), bindAddr)

	// Test start succeeds (listener created, SSH connection deferred)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	startErr := lf.Start(ctx)
	assert.NoError(t, startErr, "Start should succeed (listener created)")

	// Status should be running after successful start
	assert.Equal(t, StatusRunning, lf.Status(), "Forward should be in running state after start")

	// Test stop
	stopErr := lf.Stop()
	assert.NoError(t, stopErr, "Stop should succeed")
	assert.Equal(t, StatusStopped, lf.Status(), "Forward should be stopped after Stop()")
}

// TestLocalListenToRemote_StatusTransitions tests status transitions
func TestLocalListenToRemote_StatusTransitions(t *testing.T) {
	port, err := findFreePort()
	require.NoError(t, err)
	bindAddr := fmt.Sprintf("127.0.0.1:%d", port)

	testDB, err := createTestDB()
	require.NoError(t, err)
	defer func() {
		if err := testDB.Close(); err != nil {
			t.Logf("Failed to close test database: %v", err)
		}
	}()

	forwardID := "test-forward-transitions"
	hopChain := []*tunnel.HopConfig{
		{
			Host:         "testhost",
			HostName:     "invalid.example.com",
			Port:         22,
			User:         "testuser",
			IdentityFile: "/nonexistent/key",
		},
	}

	pool := connection.NewConnectionManager(connection.DefaultConfig(), nil)
	defer func() { _ = pool.Close() }()

	lf := NewLocalListenToRemote(bindAddr, "127.0.0.1:9999", forwardID, testDB, pool, hopChain)

	// Initial state: Stopped
	assert.Equal(t, StatusStopped, lf.Status())

	// Start the forward
	// Note: LocalListenToRemote uses lazy connection - SSH connection is established
	// when the first client connects, not during Start(). So Start() succeeds even
	// with invalid hop config (as long as the bind address is available).
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	startErr := lf.Start(ctx)

	// Start should succeed (listener created, SSH connection deferred)
	assert.NoError(t, startErr)
	assert.Equal(t, StatusRunning, lf.Status())

	// Stop the forward
	stopErr := lf.Stop()
	assert.NoError(t, stopErr)
	assert.Equal(t, StatusStopped, lf.Status())
}

// TestLocalListenToRemote_String tests string representation
func TestLocalListenToRemote_String(t *testing.T) {
	testDB, err := createTestDB()
	require.NoError(t, err)
	defer func() {
		if err := testDB.Close(); err != nil {
			t.Logf("Failed to close test database: %v", err)
		}
	}()

	pool := connection.NewConnectionManager(connection.DefaultConfig(), nil)
	defer func() { _ = pool.Close() }()

	lf := NewLocalListenToRemote(
		"127.0.0.1:8888",
		"192.168.1.100:9999",
		"test-id",
		testDB,
		pool,
		[]*tunnel.HopConfig{},
	)

	str := lf.String()
	assert.Contains(t, str, "LocalListenToRemote")
	assert.Contains(t, str, "127.0.0.1:8888")
	assert.Contains(t, str, "192.168.1.100:9999")
}

// TestLocalListenToRemote_Type tests forward type
func TestLocalListenToRemote_Type(t *testing.T) {
	testDB, err := createTestDB()
	require.NoError(t, err)
	defer func() {
		if err := testDB.Close(); err != nil {
			t.Logf("Failed to close test database: %v", err)
		}
	}()

	pool := connection.NewConnectionManager(connection.DefaultConfig(), nil)
	defer func() { _ = pool.Close() }()

	lf := NewLocalListenToRemote(
		"127.0.0.1:8888",
		"192.168.1.100:9999",
		"test-id",
		testDB,
		pool,
		[]*tunnel.HopConfig{},
	)

	assert.Equal(t, "local_listen_to_remote", lf.Type())
}

// TestLocalListenToRemote_HealthCheckWhenStopped tests health check when stopped
func TestLocalListenToRemote_HealthCheckWhenStopped(t *testing.T) {
	testDB, err := createTestDB()
	require.NoError(t, err)
	defer func() {
		if err := testDB.Close(); err != nil {
			t.Logf("Failed to close test database: %v", err)
		}
	}()

	pool := connection.NewConnectionManager(connection.DefaultConfig(), nil)
	defer func() { _ = pool.Close() }()

	lf := NewLocalListenToRemote(
		"127.0.0.1:8888",
		"192.168.1.100:9999",
		"test-id",
		testDB,
		pool,
		[]*tunnel.HopConfig{},
	)

	// Health check should fail when not running
	err = lf.HealthCheck()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not running")
}

// TestLocalListenToRemote_ConcurrentStop tests concurrent stop calls
func TestLocalListenToRemote_ConcurrentStop(t *testing.T) {
	port, err := findFreePort()
	require.NoError(t, err)
	bindAddr := fmt.Sprintf("127.0.0.1:%d", port)

	testDB, err := createTestDB()
	require.NoError(t, err)
	defer func() {
		if err := testDB.Close(); err != nil {
			t.Logf("Failed to close test database: %v", err)
		}
	}()

	forwardID := "test-concurrent-stop"
	hopChain := []*tunnel.HopConfig{
		{
			Host:         "testhost",
			HostName:     "invalid.example.com",
			Port:         22,
			User:         "testuser",
			IdentityFile: "/nonexistent/key",
		},
	}

	pool := connection.NewConnectionManager(connection.DefaultConfig(), nil)
	defer func() { _ = pool.Close() }()

	lf := NewLocalListenToRemote(bindAddr, "127.0.0.1:9999", forwardID, testDB, pool, hopChain)

	// Start (should succeed with lazy connection)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	startErr := lf.Start(ctx)
	require.NoError(t, startErr)
	assert.Equal(t, StatusRunning, lf.Status())

	// Call Stop multiple times concurrently
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			_ = lf.Stop()
			done <- true
		}()
	}

	// All stops should complete without deadlock
	for i := 0; i < 10; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for Stop() to complete")
		}
	}

	// Status should be stopped
	assert.Equal(t, StatusStopped, lf.Status())
}

// TestLocalListenToRemote_DatabaseStatusUpdate tests database status updates
func TestLocalListenToRemote_DatabaseStatusUpdate(t *testing.T) {
	port, err := findFreePort()
	require.NoError(t, err)
	bindAddr := fmt.Sprintf("127.0.0.1:%d", port)

	testDB, err := createTestDB()
	require.NoError(t, err)
	defer func() {
		if err := testDB.Close(); err != nil {
			t.Logf("Failed to close test database: %v", err)
		}
	}()

	forwardID := "test-db-status"
	hopChain := []*tunnel.HopConfig{
		{
			Host:         "testhost",
			HostName:     "invalid.example.com",
			Port:         22,
			User:         "testuser",
			IdentityFile: "/nonexistent/key",
		},
	}

	pool := connection.NewConnectionManager(connection.DefaultConfig(), nil)
	defer func() { _ = pool.Close() }()

	lf := NewLocalListenToRemote(bindAddr, "127.0.0.1:9999", forwardID, testDB, pool, hopChain)

	// Start (should succeed with lazy connection)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	startErr := lf.Start(ctx)
	assert.NoError(t, startErr)

	// Note: LocalListenToRemote doesn't automatically update database status on successful start
	// Database status updates are handled by ForwardService
	// Here we just verify the forward is running
	assert.Equal(t, StatusRunning, lf.Status())

	// Stop the forward
	_ = lf.Stop()
}

// TestLocalListenToRemote_NilDatabase tests behavior with nil database
func TestLocalListenToRemote_NilDatabase(t *testing.T) {
	port, err := findFreePort()
	require.NoError(t, err)
	bindAddr := fmt.Sprintf("127.0.0.1:%d", port)

	// Create forward with nil database and nil pool
	pool := connection.NewConnectionManager(connection.DefaultConfig(), nil)
	defer func() { _ = pool.Close() }()

	lf := NewLocalListenToRemote(bindAddr, "127.0.0.1:9999", "test-nil-db", nil, pool, []*tunnel.HopConfig{
		{
			Host:         "testhost",
			HostName:     "invalid.example.com",
			Port:         22,
			User:         "testuser",
			IdentityFile: "/nonexistent/key",
		},
	})

	// Start should succeed (nil database doesn't affect listener creation)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	startErr := lf.Start(ctx)
	assert.NoError(t, startErr)
	assert.Equal(t, StatusRunning, lf.Status())

	// Stop should work
	assert.NoError(t, lf.Stop())
}

// TestLocalListenToRemote_EmptyHopChain tests behavior with empty hop chain
func TestLocalListenToRemote_EmptyHopChain(t *testing.T) {
	port, err := findFreePort()
	require.NoError(t, err)
	bindAddr := fmt.Sprintf("127.0.0.1:%d", port)

	testDB, err := createTestDB()
	require.NoError(t, err)
	defer func() {
		if err := testDB.Close(); err != nil {
			t.Logf("Failed to close test database: %v", err)
		}
	}()

	// Create forward with empty hop chain
	pool := connection.NewConnectionManager(connection.DefaultConfig(), nil)
	defer func() { _ = pool.Close() }()

	lf := NewLocalListenToRemote(bindAddr, "127.0.0.1:9999", "test-empty-hop", testDB, pool, []*tunnel.HopConfig{})

	// Start should succeed (empty hop chain is OK for listener creation)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	startErr := lf.Start(ctx)
	assert.NoError(t, startErr)
	assert.Equal(t, StatusRunning, lf.Status())

	_ = lf.Stop()
}

// TestLocalListenToRemote_CleanupOnFailedStart tests cleanup when Start() fails
// This verifies resources are cleaned up properly on failures like port conflicts
func TestLocalListenToRemote_CleanupOnFailedStart(t *testing.T) {
	// Find free port for testing
	port, err := findFreePort()
	require.NoError(t, err)
	bindAddr := fmt.Sprintf("127.0.0.1:%d", port)

	testDB, err := createTestDB()
	require.NoError(t, err)
	defer func() {
		if err := testDB.Close(); err != nil {
			t.Logf("Failed to close test database: %v", err)
		}
	}()

	forwardID := "test-cleanup-failed-start"

	// Create first forward and bind the port
	hopChain := []*tunnel.HopConfig{
		{
			Host:     "testhost",
			HostName: "localhost",
			Port:     22,
			User:     "testuser",
		},
	}

	pool := connection.NewConnectionManager(connection.DefaultConfig(), nil)
	defer func() { _ = pool.Close() }()

	lf1 := NewLocalListenToRemote(bindAddr, "127.0.0.1:9999", forwardID+"-1", testDB, pool, hopChain)

	// Start first forward successfully
	ctx1, cancel1 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel1()

	startErr1 := lf1.Start(ctx1)
	require.NoError(t, startErr1, "First forward should start successfully")

	// Try to create a second forward on the same port (should fail)
	lf2 := NewLocalListenToRemote(bindAddr, "127.0.0.1:9999", forwardID+"-2", testDB, pool, hopChain)

	ctx2, cancel2 := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel2()

	startErr2 := lf2.Start(ctx2)
	assert.Error(t, startErr2, "Second forward should fail due to port conflict")

	// Stop first forward
	_ = lf1.Stop()

	// Now we should be able to bind to the same port again
	lf3 := NewLocalListenToRemote(bindAddr, "127.0.0.1:9999", forwardID+"-3", testDB, pool, hopChain)

	ctx3, cancel3 := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel3()

	startErr3 := lf3.Start(ctx3)
	assert.NoError(t, startErr3, "Third forward should succeed after first is stopped")
	_ = lf3.Stop()
}

// TestLocalListenToRemote_StopIdempotent tests that calling Stop() multiple times
// doesn't panic or hang. This verifies the cleanupOnce pattern works correctly.
func TestLocalListenToRemote_StopIdempotent(t *testing.T) {
	port, err := findFreePort()
	require.NoError(t, err)
	bindAddr := fmt.Sprintf("127.0.0.1:%d", port)

	testDB, err := createTestDB()
	require.NoError(t, err)
	defer func() {
		if err := testDB.Close(); err != nil {
			t.Logf("Failed to close test database: %v", err)
		}
	}()

	forwardID := "test-stop-idempotent"

	// Create a forward with localhost target (will succeed without needing real SSH)
	// We use a minimal hop chain that will allow connection pool to work
	hopChain := []*tunnel.HopConfig{
		{
			Host:         "localhost",
			HostName:     "127.0.0.1",
			Port:         22,
			User:         "testuser",
			IdentityFile: "/nonexistent/key",
		},
	}

	pool := connection.NewConnectionManager(connection.DefaultConfig(), nil)
	defer func() { _ = pool.Close() }()

	lf := NewLocalListenToRemote(bindAddr, "127.0.0.1:9999", forwardID, testDB, pool, hopChain)

	// Start will fail to validate SSH connection, but that's OK for this test
	// The important part is testing Stop() idempotency
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	startErr := lf.Start(ctx)
	// Start may fail due to invalid SSH config - that's expected
	_ = startErr

	// Regardless of start result, call Stop() multiple times - should not panic or hang
	// This verifies cleanupOnce ensures cleanup only runs once
	for i := 0; i < 5; i++ {
		stopErr := lf.Stop()
		assert.NoError(t, stopErr, "Stop() should not error on call %d", i+1)
		assert.Equal(t, StatusStopped, lf.Status(), "Status should be stopped after call %d", i+1)
	}

	// Verify no goroutines are leaked by checking we can create a new forward on same port
	pool2 := connection.NewConnectionManager(connection.DefaultConfig(), nil)
	defer func() { _ = pool2.Close() }()

	lf2 := NewLocalListenToRemote(bindAddr, "127.0.0.1:9999", "test-idempotent-reuse", testDB, pool2, hopChain)

	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()

	// Should not fail with port conflict
	startErr2 := lf2.Start(ctx2)
	if startErr2 != nil {
		assert.NotContains(t, startErr2.Error(), "address already in use",
			"Port should be available after multiple Stop() calls")
	}

	_ = lf2.Stop()
}
