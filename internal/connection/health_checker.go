package connection

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// HealthCheckConfig holds configuration for the health checker.
type HealthCheckConfig struct {
	// Interval is the time between health checks.
	// Default: 15 seconds
	Interval time.Duration

	// Timeout is the timeout for each health check.
	// Default: 5 seconds
	Timeout time.Duration
}

// DefaultHealthCheckConfig returns the default health check configuration.
func DefaultHealthCheckConfig() HealthCheckConfig {
	return HealthCheckConfig{
		Interval: 15 * time.Second,
		Timeout:  5 * time.Second,
	}
}

// HealthChecker monitors the health of pooled connections.
//
// It periodically sends keepalive requests to all monitored connections.
// If a connection fails the health check, the context is cancelled,
// which cascades to all forwards using that connection.
type HealthChecker struct {
	// monitoredConnections holds connections being monitored, keyed by signature hash.
	monitoredConnections map[string]*PooledConnection

	// affectedForwards maps connection signatures to lists of forward IDs.
	// When a connection fails, all these forwards need to be notified.
	affectedForwards map[string][]string

	// mu protects concurrent access to the maps.
	mu sync.RWMutex

	// config holds health check configuration.
	config HealthCheckConfig

	// done signals the check loop to stop.
	done chan struct{}

	// wg waits for the check loop goroutine to finish.
	wg sync.WaitGroup
}

// NewHealthChecker creates a new HealthChecker with the given config.
// It starts a background goroutine that performs periodic health checks.
func NewHealthChecker(config HealthCheckConfig) *HealthChecker {
	hc := &HealthChecker{
		monitoredConnections: make(map[string]*PooledConnection),
		affectedForwards:     make(map[string][]string),
		config:               config,
		done:                 make(chan struct{}),
	}

	// Start health check loop
	hc.wg.Add(1)
	go hc.checkLoop()

	return hc
}

// Monitor starts monitoring a connection.
// The connection will be periodically checked for health.
func (hc *HealthChecker) Monitor(conn *PooledConnection) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	hash := conn.Signature.Hash()
	hc.monitoredConnections[hash] = conn
}

// Unregister stops monitoring a connection.
func (hc *HealthChecker) Unregister(conn *PooledConnection) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	hash := conn.Signature.Hash()
	delete(hc.monitoredConnections, hash)

	// Also clean up affected forwards
	delete(hc.affectedForwards, hash)
}

// RegisterForward registers a forward ID as using the given connection.
// When the connection fails, this forward will be notified via context cancellation.
func (hc *HealthChecker) RegisterForward(conn *PooledConnection, forwardID string) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	hash := conn.Signature.Hash()
	hc.affectedForwards[hash] = append(hc.affectedForwards[hash], forwardID)
}

// UnregisterForward removes a forward ID from the connection's list.
func (hc *HealthChecker) UnregisterForward(conn *PooledConnection, forwardID string) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	hash := conn.Signature.Hash()
	forwards := hc.affectedForwards[hash]

	// Filter out the forward ID
	newForwards := make([]string, 0, len(forwards))
	for _, id := range forwards {
		if id != forwardID {
			newForwards = append(newForwards, id)
		}
	}

	hc.affectedForwards[hash] = newForwards
}

// checkLoop runs in the background, performing periodic health checks.
func (hc *HealthChecker) checkLoop() {
	defer hc.wg.Done()

	ticker := time.NewTicker(hc.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			hc.checkAllConnections()
		case <-hc.done:
			return
		}
	}
}

// checkAllConnections checks the health of all monitored connections.
func (hc *HealthChecker) checkAllConnections() {
	hc.mu.RLock()
	// Create a snapshot of connections to check
	connections := make([]*PooledConnection, 0, len(hc.monitoredConnections))
	for _, conn := range hc.monitoredConnections {
		connections = append(connections, conn)
	}
	hc.mu.RUnlock()

	// Check each connection
	for _, conn := range connections {
		if err := hc.checkConnection(conn); err != nil {
			hc.handleFailure(conn, err)
		}
	}
}

// checkConnection performs a health check on a single connection.
// It sends a keepalive request to verify the connection is still alive.
func (hc *HealthChecker) checkConnection(conn *PooledConnection) error {
	// Don't check closed or idle connections
	if !conn.IsActive() {
		return nil
	}

	// Check ref count - only check active connections with refs
	if conn.GetRefCount() == 0 {
		return nil
	}

	// Don't check connections with nil clients (e.g., in unit tests)
	if conn.Client == nil {
		return nil
	}

	// Create a timeout context for the health check
	ctx, cancel := context.WithTimeout(context.Background(), hc.config.Timeout)
	defer cancel()

	// Try to send a keepalive request
	// The keepalive@openssh.com request is a standard SSH keepalive
	done := make(chan error, 1)

	go func() {
		// Send keepalive request
		// Note: We use the keepalive@openssh.com request type which is widely supported
		// The return values are: bool (ok), bool (reply), error
		_, _, err := conn.Client.SendRequest("keepalive@openssh.com", true, nil)
		done <- err
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("health check timeout")
	}
}

// handleFailure handles a connection failure by cancelling the context
// and notifying all affected forwards.
//
// Note: This does NOT remove the connection from the pool.
// The connection will be removed lazily when ConnectionManager.Acquire()
// validates the connection and finds it dead.
func (hc *HealthChecker) handleFailure(conn *PooledConnection, err error) {
	hash := conn.Signature.Hash()

	hc.mu.RLock()
	forwardIDs := hc.affectedForwards[hash]
	hc.mu.RUnlock()

	// Cancel the connection's context, which cascades to all forwards
	conn.CancelFunc()

	// Log the failure (in production, this would go to a proper logger)
	// For now, we just mark the connection as failed
	// The forwards will detect the context cancellation and handle it

	// Notify affected forwards
	// In the full implementation, this would update the database status
	// For now, the context cancellation is sufficient for forwards to detect failure
	_ = forwardIDs // Will be used in Task 2.5 for database updates
}

// Close stops the health checker.
// It waits for the check loop to finish.
// This method is idempotent and safe to call multiple times.
func (hc *HealthChecker) Close() error {
	// Use select to avoid closing an already-closed channel
	select {
	case <-hc.done:
		// Already closed
		return nil
	default:
		// Not closed yet, close it
		close(hc.done)
	}

	hc.wg.Wait()
	return nil
}
