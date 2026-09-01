package connection

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHealthChecker_New verifies health checker initialization.
func TestHealthChecker_New(t *testing.T) {
	config := DefaultHealthCheckConfig()
	hc := NewHealthChecker(config)

	require.NotNil(t, hc)
	assert.NotNil(t, hc.monitoredConnections)

	// Cleanup
	_ = hc.Close()
}

// TestHealthChecker_Monitor verifies monitoring of connections.
func TestHealthChecker_Monitor(t *testing.T) {
	config := HealthCheckConfig{
		Interval: 50 * time.Millisecond,
		Timeout:  1 * time.Second,
	}
	hc := NewHealthChecker(config)
	defer func() { _ = hc.Close() }()

	sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}
	conn := NewPooledConnection(nil, sig)

	// Monitor the connection
	hc.Monitor(conn)

	// Verify it's being monitored
	hc.mu.RLock()
	monitored, exists := hc.monitoredConnections[sig.Hash()]
	hc.mu.RUnlock()

	assert.True(t, exists, "Connection should be monitored")
	assert.Same(t, conn, monitored, "Should monitor the correct connection")
}

// TestHealthChecker_Unregister verifies unregistration of connections.
func TestHealthChecker_Unregister(t *testing.T) {
	config := DefaultHealthCheckConfig()
	hc := NewHealthChecker(config)

	sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}
	conn := NewPooledConnection(nil, sig)

	// Monitor then unregister
	hc.Monitor(conn)
	hc.Unregister(conn)

	// Verify it's no longer being monitored
	hc.mu.RLock()
	_, exists := hc.monitoredConnections[sig.Hash()]
	hc.mu.RUnlock()

	assert.False(t, exists, "Connection should not be monitored after unregister")
}

// TestHealthChecker_CheckLoop verifies that the check loop runs and monitors connections.
func TestHealthChecker_CheckLoop(t *testing.T) {
	config := HealthCheckConfig{
		Interval: 20 * time.Millisecond,
		Timeout:  1 * time.Second,
	}
	hc := NewHealthChecker(config)
	defer func() { _ = hc.Close() }()

	sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}
	conn := NewPooledConnection(nil, sig)

	hc.Monitor(conn)

	// Wait for the check loop to run at least once
	time.Sleep(30 * time.Millisecond)

	// Verify connection is still being monitored
	hc.mu.RLock()
	_, exists := hc.monitoredConnections[sig.Hash()]
	hc.mu.RUnlock()

	assert.True(t, exists, "Connection should still be monitored after check loop runs")
}

// TestHealthChecker_IdleConnectionNotChecked verifies that idle connections
// are handled correctly.
func TestHealthChecker_IdleConnectionNotChecked(t *testing.T) {
	config := HealthCheckConfig{
		Interval: 20 * time.Millisecond,
		Timeout:  1 * time.Second,
	}
	hc := NewHealthChecker(config)
	defer func() { _ = hc.Close() }()

	sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}
	conn := NewPooledConnection(nil, sig)

	// Release to make it idle
	_ = conn.Release()
	assert.Equal(t, StatusIdle, conn.GetStatus())

	hc.Monitor(conn)

	// Wait for potential checks
	time.Sleep(30 * time.Millisecond)

	// Connection should still be monitored (even if idle)
	hc.mu.RLock()
	_, exists := hc.monitoredConnections[sig.Hash()]
	hc.mu.RUnlock()

	assert.True(t, exists, "Idle connection should still be in monitored list")
}

// TestHealthChecker_Close verifies graceful shutdown.
func TestHealthChecker_Close(t *testing.T) {
	config := HealthCheckConfig{
		Interval: 20 * time.Millisecond,
		Timeout:  1 * time.Second,
	}
	hc := NewHealthChecker(config)

	// Close should stop the check loop
	err := hc.Close()
	assert.NoError(t, err)

	// Double-close should be safe
	err = hc.Close()
	assert.NoError(t, err)
}

// TestHealthCheckConfig_DefaultConfig verifies default configuration.
func TestHealthCheckConfig_DefaultConfig(t *testing.T) {
	config := DefaultHealthCheckConfig()

	assert.Equal(t, 15*time.Second, config.Interval)
	assert.Equal(t, 5*time.Second, config.Timeout)
}

// TestHealthChecker_HandleFailure_CancelsConnectionContext verifies that a failed
// health check cancels the connection's context, so forwards on a dead SSH
// connection see cancellation and ForwardService can rebuild them.
func TestHealthChecker_HandleFailure_CancelsConnectionContext(t *testing.T) {
	hc := NewHealthChecker(DefaultHealthCheckConfig())
	defer func() { _ = hc.Close() }()

	sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}
	conn := NewPooledConnection(nil, sig)

	hc.handleFailure(conn, errors.New("keepalive timeout"))

	select {
	case <-conn.Context.Done():
	case <-time.After(time.Second):
		t.Fatal("connection context was not cancelled after handleFailure")
	}
}

// TestHealthChecker_HandleFailure_RepeatedDoesNotPanic verifies that repeated
// failures on the same connection are safe: context cancellation is idempotent,
// so a flapping connection must not panic the health check loop.
func TestHealthChecker_HandleFailure_RepeatedDoesNotPanic(t *testing.T) {
	hc := NewHealthChecker(DefaultHealthCheckConfig())
	defer func() { _ = hc.Close() }()

	sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}
	conn := NewPooledConnection(nil, sig)

	assert.NotPanics(t, func() {
		hc.handleFailure(conn, errors.New("first failure"))
		hc.handleFailure(conn, errors.New("second failure"))
	})

	select {
	case <-conn.Context.Done():
	default:
		t.Fatal("connection context should be cancelled")
	}
}
