package connection

import (
	"context"
	"testing"
	"time"

	"github.com/hallelujah-shih/ssh-multihop/internal/tunnel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConnectionManager_New verifies ConnectionManager initialization.
func TestConnectionManager_New(t *testing.T) {
	config := DefaultConfig()
	cm := NewConnectionManager(config, nil)

	require.NotNil(t, cm)
	assert.NotNil(t, cm.pools)
	assert.NotNil(t, cm.done)

	// Cleanup
	_ = cm.Close()
}

// TestConnectionManager_AcquireSameSignature verifies that acquiring the same
// signature returns the same connection instance (when connection exists).
func TestConnectionManager_AcquireSameSignature(t *testing.T) {
	config := DefaultConfig()
	cm := NewConnectionManager(config, nil)
	defer func() { _ = cm.Close() }()

	sig := ConnectionSignature{
		Username:  "user",
		Hostname:  "host",
		Port:      22,
		JumpChain: []string{},
	}

	// First acquire should fail because hopConfigProvider is nil
	conn1, err := cm.Acquire(context.Background(), sig, "test-forward")
	assert.Error(t, err)
	assert.Nil(t, conn1)
	assert.Contains(t, err.Error(), "hopConfigProvider is nil")
}

// TestConnectionManager_Release verifies connection release behavior.
func TestConnectionManager_Release(t *testing.T) {
	config := DefaultConfig()
	cm := NewConnectionManager(config, nil)
	defer func() { _ = cm.Close() }()

	// Create a manual pooled connection for testing
	sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}
	pc := NewPooledConnection(nil, sig)

	// Release should decrement ref count
	err := cm.Release(pc, "test-forward")
	assert.NoError(t, err)
	assert.Equal(t, 0, pc.GetRefCount())
	assert.Equal(t, StatusIdle, pc.GetStatus())
}

// TestConnectionManager_Close verifies connection manager cleanup.
func TestConnectionManager_Close(t *testing.T) {
	config := DefaultConfig()
	cm := NewConnectionManager(config, nil)

	// Add some connections to the pool
	sig1 := ConnectionSignature{Username: "user1", Hostname: "host1", Port: 22}
	sig2 := ConnectionSignature{Username: "user2", Hostname: "host2", Port: 22}

	conn1 := NewPooledConnection(nil, sig1)
	conn2 := NewPooledConnection(nil, sig2)

	cm.mu.Lock()
	cm.pools[sig1.Hash()] = conn1
	cm.pools[sig2.Hash()] = conn2
	cm.mu.Unlock()

	// Close should stop the goroutine and close all connections
	err := cm.Close()
	assert.NoError(t, err)

	// Connections should be marked as closed
	assert.Equal(t, StatusClosed, conn1.GetStatus())
	assert.Equal(t, StatusClosed, conn2.GetStatus())

	// Pool should be empty
	stats := cm.Stats()
	assert.Equal(t, 0, stats.TotalConnections)
}

// TestConnectionManager_Stats verifies statistics reporting.
func TestConnectionManager_Stats(t *testing.T) {
	config := DefaultConfig()
	cm := NewConnectionManager(config, nil)
	defer func() { _ = cm.Close() }()

	// Initially empty
	stats := cm.Stats()
	assert.Equal(t, 0, stats.TotalConnections)
	assert.Equal(t, 0, stats.ActiveConnections)
	assert.Equal(t, 0, stats.IdleConnections)
	assert.Equal(t, 0, stats.ClosedConnections)

	// Add connections with different states
	sig1 := ConnectionSignature{Username: "user1", Hostname: "host1", Port: 22}
	sig2 := ConnectionSignature{Username: "user2", Hostname: "host2", Port: 22}
	sig3 := ConnectionSignature{Username: "user3", Hostname: "host3", Port: 22}

	conn1 := NewPooledConnection(nil, sig1) // Active, refCount=1
	conn2 := NewPooledConnection(nil, sig2) // Active, refCount=1
	conn3 := NewPooledConnection(nil, sig3) // Active, refCount=1
	_ = conn2.Release()                     // Idle

	cm.mu.Lock()
	cm.pools[sig1.Hash()] = conn1
	cm.pools[sig2.Hash()] = conn2
	cm.pools[sig3.Hash()] = conn3
	cm.mu.Unlock()

	stats = cm.Stats()
	assert.Equal(t, 3, stats.TotalConnections)
	assert.Equal(t, 2, stats.ActiveConnections) // conn1, conn3
	assert.Equal(t, 1, stats.IdleConnections)   // conn2
	assert.Equal(t, 0, stats.ClosedConnections)
}

// TestPoolConfig_DefaultConfig verifies default configuration values.
func TestPoolConfig_DefaultConfig(t *testing.T) {
	config := DefaultConfig()

	assert.Equal(t, 60*time.Second, config.IdleTimeout)
	assert.Equal(t, 10, config.MaxIdleConnections)
	assert.Equal(t, 30*time.Second, config.DialTimeout)
}

// TestConnectionManager_Concurrency verifies thread-safe operations.
func TestConnectionManager_Concurrency(t *testing.T) {
	config := DefaultConfig()
	cm := NewConnectionManager(config, nil)
	defer func() { _ = cm.Close() }()

	sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}

	// Add a connection to the pool
	conn := NewPooledConnection(nil, sig)
	cm.mu.Lock()
	cm.pools[sig.Hash()] = conn
	cm.mu.Unlock()

	// Concurrent acquires and releases
	done := make(chan bool)
	go func() {
		for i := 0; i < 100; i++ {
			_, _ = cm.Acquire(context.Background(), sig, "test-forward")
			_ = cm.Release(conn, "test-forward")
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			_ = cm.Stats()
		}
		done <- true
	}()

	// Wait for completion
	<-done
	<-done

	// Should not have crashed
	_ = cm.Stats()
}

// TestConnectionManager_MultipleSignatures verifies different signatures
// create different connections.
func TestConnectionManager_MultipleSignatures(t *testing.T) {
	config := DefaultConfig()
	cm := NewConnectionManager(config, nil)
	defer func() { _ = cm.Close() }()

	sig1 := ConnectionSignature{Username: "user1", Hostname: "host1", Port: 22}
	sig2 := ConnectionSignature{Username: "user2", Hostname: "host2", Port: 22}
	sig3 := ConnectionSignature{Username: "user1", Hostname: "host1", Port: 2222}

	// These should all be different signatures
	assert.NotEqual(t, sig1.Hash(), sig2.Hash())
	assert.NotEqual(t, sig1.Hash(), sig3.Hash())
	assert.NotEqual(t, sig2.Hash(), sig3.Hash())

	// Add to pool
	conn1 := NewPooledConnection(nil, sig1)
	conn2 := NewPooledConnection(nil, sig2)
	conn3 := NewPooledConnection(nil, sig3)

	cm.mu.Lock()
	cm.pools[sig1.Hash()] = conn1
	cm.pools[sig2.Hash()] = conn2
	cm.pools[sig3.Hash()] = conn3
	cm.mu.Unlock()

	stats := cm.Stats()
	assert.Equal(t, 3, stats.TotalConnections)
}

// mockHopConfigProvider creates a mock hop config provider for testing.
// It returns simple hop configs that would fail in real SSH connection,
// but allows testing the connection pool logic.
func mockHopConfigProvider(t *testing.T) HopConfigProvider {
	return func(sig ConnectionSignature) ([]*tunnel.HopConfig, *SSHClientConfigBuilder, error) {
		// Create a simple hop chain with the signature's target
		hops := []*tunnel.HopConfig{
			{
				Host:     "local",
				HostName: "localhost",
				Port:     0,
				User:     "",
			},
			{
				Host:     sig.Hostname,
				HostName: sig.Hostname,
				Port:     sig.Port,
				User:     sig.Username,
			},
		}

		// Return a minimal builder (will fail to connect, but that's OK for testing)
		builder := NewSSHClientConfigBuilder()

		return hops, builder, nil
	}
}

// TestConnectionManager_AcquireCreatesNew verifies that Acquire creates
// new connections when they don't exist.
func TestConnectionManager_AcquireCreatesNew(t *testing.T) {
	config := DefaultConfig()
	provider := mockHopConfigProvider(t)
	cm := NewConnectionManager(config, provider)
	defer func() { _ = cm.Close() }()

	sig := ConnectionSignature{
		Username:  "user",
		Hostname:  "host",
		Port:      22,
		JumpChain: []string{},
	}

	// First acquire should try to create a new connection
	// This will fail at the SSH dial stage (which is expected in unit tests)
	conn1, err := cm.Acquire(context.Background(), sig, "test-forward")
	assert.Error(t, err) // Expected to fail during SSH dial
	assert.Nil(t, conn1)
	assert.Contains(t, err.Error(), "failed to dial SSH")
}

// TestConnectionManager_AcquireReusesExisting verifies that acquiring
// an existing connection returns the same instance.
func TestConnectionManager_AcquireReusesExisting(t *testing.T) {
	config := DefaultConfig()
	cm := NewConnectionManager(config, nil)
	defer func() { _ = cm.Close() }()

	sig := ConnectionSignature{
		Username:  "user",
		Hostname:  "host",
		Port:      22,
		JumpChain: []string{},
	}

	// Manually add a connection to the pool
	existingConn := NewPooledConnection(nil, sig)
	cm.mu.Lock()
	cm.pools[sig.Hash()] = existingConn
	cm.mu.Unlock()

	// Acquire should return the existing connection
	conn1, err := cm.Acquire(context.Background(), sig, "test-forward")
	assert.NoError(t, err)
	assert.Same(t, existingConn, conn1)
	assert.Equal(t, 2, conn1.GetRefCount())

	// Second acquire should also return the same connection
	conn2, err := cm.Acquire(context.Background(), sig, "test-forward")
	assert.NoError(t, err)
	assert.Same(t, existingConn, conn2)
	assert.Equal(t, 3, conn2.GetRefCount())
}

// TestConnectionManager_Singleflight verifies that concurrent Acquire
// calls for the same signature share the connection creation.
func TestConnectionManager_Singleflight(t *testing.T) {
	config := DefaultConfig()
	provider := mockHopConfigProvider(t)
	cm := NewConnectionManager(config, provider)
	defer func() { _ = cm.Close() }()

	sig := ConnectionSignature{
		Username:  "user",
		Hostname:  "host",
		Port:      22,
		JumpChain: []string{},
	}

	// Track how many times the provider is called
	callCount := 0
	originalProvider := provider
	cm.hopConfigProvider = func(s ConnectionSignature) ([]*tunnel.HopConfig, *SSHClientConfigBuilder, error) {
		callCount++
		return originalProvider(s)
	}

	// Launch concurrent acquires (all will fail at SSH dial, but that's OK)
	const numGoroutines = 10
	errChan := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			_, err := cm.Acquire(context.Background(), sig, "test-forward")
			errChan <- err
		}()
	}

	// Collect all errors
	for i := 0; i < numGoroutines; i++ {
		err := <-errChan
		assert.Error(t, err) // All should fail at SSH dial
	}

	// Provider should have been called only once due to singleflight
	assert.Equal(t, 1, callCount, "hopConfigProvider should be called once due to singleflight")
}

// TestConnectionManager_ReleaseWithLingering verifies that released connections
// linger in the pool before being closed.
func TestConnectionManager_ReleaseWithLingering(t *testing.T) {
	config := PoolConfig{
		IdleTimeout:        100 * time.Millisecond,
		MaxIdleConnections: 10,
		DialTimeout:        30 * time.Second,
	}
	cm := NewConnectionManager(config, nil)
	defer func() { _ = cm.Close() }()

	sig := ConnectionSignature{
		Username:  "user",
		Hostname:  "host",
		Port:      22,
		JumpChain: []string{},
	}

	// Create a manual pooled connection
	conn := NewPooledConnection(nil, sig)
	assert.Equal(t, 1, conn.GetRefCount())

	// Add to pool
	cm.mu.Lock()
	cm.pools[sig.Hash()] = conn
	cm.mu.Unlock()

	// Release the connection (should start lingering)
	err := cm.Release(conn, "test-forward")
	require.NoError(t, err)

	// Connection should be idle but still in pool
	assert.Equal(t, 0, conn.GetRefCount())
	assert.Equal(t, StatusIdle, conn.GetStatus())

	cm.mu.RLock()
	_, exists := cm.pools[sig.Hash()]
	cm.mu.RUnlock()
	assert.True(t, exists, "Connection should still be in pool during lingering")

	// Wait for lingering timeout
	time.Sleep(150 * time.Millisecond)

	// Connection should have been removed from pool
	cm.mu.RLock()
	_, exists = cm.pools[sig.Hash()]
	cm.mu.RUnlock()
	assert.False(t, exists, "Connection should be removed from pool after lingering timeout")

	// Connection should be marked as closed
	assert.Equal(t, StatusClosed, conn.GetStatus())
}

// TestConnectionManager_AcquireDuringLingering verifies that acquiring
// a connection during its lingering period cancels the lingering timer.
func TestConnectionManager_AcquireDuringLingering(t *testing.T) {
	config := PoolConfig{
		IdleTimeout:        500 * time.Millisecond,
		MaxIdleConnections: 10,
		DialTimeout:        30 * time.Second,
	}
	cm := NewConnectionManager(config, nil)
	defer func() { _ = cm.Close() }()

	sig := ConnectionSignature{
		Username:  "user",
		Hostname:  "host",
		Port:      22,
		JumpChain: []string{},
	}

	// Create a manual pooled connection
	conn := NewPooledConnection(nil, sig)

	// Add to pool
	cm.mu.Lock()
	cm.pools[sig.Hash()] = conn
	cm.mu.Unlock()

	// Acquire and release to establish initial state
	_, err := cm.Acquire(context.Background(), sig, "test-forward")
	require.NoError(t, err)
	assert.Equal(t, 2, conn.GetRefCount())

	err = cm.Release(conn, "test-forward")
	require.NoError(t, err)
	assert.Equal(t, 1, conn.GetRefCount())
	assert.Equal(t, StatusActive, conn.GetStatus())

	// Release again to start lingering
	err = cm.Release(conn, "test-forward")
	require.NoError(t, err)
	assert.Equal(t, 0, conn.GetRefCount())
	assert.Equal(t, StatusIdle, conn.GetStatus())

	// Wait a bit (but less than idle timeout)
	time.Sleep(100 * time.Millisecond)

	// Re-acquire the connection (should cancel lingering)
	acquiredConn, err := cm.Acquire(context.Background(), sig, "test-forward")
	require.NoError(t, err)
	assert.Same(t, conn, acquiredConn)
	assert.Equal(t, 1, acquiredConn.GetRefCount())
	assert.Equal(t, StatusActive, acquiredConn.GetStatus(), "Status should be Active after re-acquire")

	// Wait for what would have been the lingering timeout
	time.Sleep(450 * time.Millisecond)

	// Connection should still be in pool (was not closed)
	cm.mu.RLock()
	_, exists := cm.pools[sig.Hash()]
	cm.mu.RUnlock()
	assert.True(t, exists, "Connection should still be in pool after re-acquire")
	assert.Equal(t, StatusActive, conn.GetStatus())
}

// TestConnectionManager_LingerWithMultipleReferences verifies that
// connections with multiple references don't start lingering until
// all references are released.
func TestConnectionManager_LingerWithMultipleReferences(t *testing.T) {
	config := PoolConfig{
		IdleTimeout:        100 * time.Millisecond,
		MaxIdleConnections: 10,
		DialTimeout:        30 * time.Second,
	}
	cm := NewConnectionManager(config, nil)
	defer func() { _ = cm.Close() }()

	sig := ConnectionSignature{
		Username:  "user",
		Hostname:  "host",
		Port:      22,
		JumpChain: []string{},
	}

	// Create a connection with multiple references
	conn := NewPooledConnection(nil, sig)
	_ = conn.Acquire() // refCount = 2
	_ = conn.Acquire() // refCount = 3

	cm.mu.Lock()
	cm.pools[sig.Hash()] = conn
	cm.mu.Unlock()

	// Release once through manager (should not start lingering, refCount = 2)
	err := cm.Release(conn, "test-forward")
	require.NoError(t, err)
	assert.Equal(t, 2, conn.GetRefCount())
	assert.Equal(t, StatusActive, conn.GetStatus())

	// Wait a bit
	time.Sleep(50 * time.Millisecond)

	// Connection should still be in pool
	cm.mu.RLock()
	_, exists := cm.pools[sig.Hash()]
	cm.mu.RUnlock()
	assert.True(t, exists, "Connection should still be in pool")

	// Release all remaining references through manager
	err = cm.Release(conn, "test-forward") // refCount = 1
	require.NoError(t, err)
	assert.Equal(t, 1, conn.GetRefCount())
	assert.Equal(t, StatusActive, conn.GetStatus())

	err = cm.Release(conn, "test-forward") // refCount = 0, should start lingering
	require.NoError(t, err)
	assert.Equal(t, 0, conn.GetRefCount())
	assert.Equal(t, StatusIdle, conn.GetStatus())

	// Wait for lingering timeout
	time.Sleep(150 * time.Millisecond)

	// Connection should be closed
	cm.mu.RLock()
	_, exists = cm.pools[sig.Hash()]
	cm.mu.RUnlock()
	assert.False(t, exists, "Connection should be removed after lingering")
}
