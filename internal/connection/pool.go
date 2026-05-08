package connection

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hallelujah-shih/ssh-multihop/internal/tunnel"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sync/singleflight"
)

// PoolConfig holds configuration for the connection pool.
type PoolConfig struct {
	// IdleTimeout is the delay before recycling an idle connection.
	// Default: 60 seconds
	IdleTimeout time.Duration

	// MaxIdleConnections is the maximum number of idle connections to keep.
	// Default: 10
	MaxIdleConnections int

	// DialTimeout is the timeout for establishing new connections.
	// Default: 30 seconds
	DialTimeout time.Duration
}

// DefaultConfig returns the default pool configuration.
func DefaultConfig() PoolConfig {
	return PoolConfig{
		IdleTimeout:        60 * time.Second,
		MaxIdleConnections: 10,
		DialTimeout:        30 * time.Second,
	}
}

// PoolStats holds statistics about the connection pool.
type PoolStats struct {
	// TotalConnections is the total number of connections in the pool.
	TotalConnections int

	// ActiveConnections is the number of connections with RefCount > 0.
	ActiveConnections int

	// IdleConnections is the number of connections with RefCount == 0.
	IdleConnections int

	// ClosedConnections is the number of connections marked as closed.
	ClosedConnections int
}

// HopConfigProvider is a function that returns the hop configuration for a connection signature.
// This allows the ConnectionManager to establish connections without needing to know
// how to parse SSH config or look up database records.
type HopConfigProvider func(sig ConnectionSignature) ([]*tunnel.HopConfig, *SSHClientConfigBuilder, error)

// ConnectionManager manages a pool of SSH connections keyed by signature.
//
// It provides:
// - Connection reuse across multiple forwards
// - Reference counting for connection lifecycle
// - Thread-safe operations with sync.RWMutex and singleflight
// - Health checking for connection monitoring
// - Connection cleanup and recycling with lingering
type ConnectionManager struct {
	// pools maps signature hash to pooled connection.
	pools map[string]*PooledConnection

	// mu protects concurrent access to pools.
	mu sync.RWMutex

	// config holds pool configuration.
	config PoolConfig

	// dialGroup prevents duplicate connection attempts (singleflight).
	dialGroup singleflight.Group

	// hopConfigProvider provides hop configs for establishing connections.
	// If nil, connections cannot be created and Acquire will return error.
	hopConfigProvider HopConfigProvider

	// healthChecker performs health checks on all active connections.
	healthChecker *HealthChecker

	// done signals the background cleanup goroutine to stop.
	done chan struct{}

	// wg waits for the background goroutine to finish.
	wg sync.WaitGroup

	// closeOnce ensures Close() is idempotent.
	closeOnce sync.Once
}

// NewConnectionManager creates a new ConnectionManager with the given config.
// It starts a background goroutine for cleanup of idle connections and initializes
// the health checker.
//
// The hopConfigProvider is called when creating a new connection. It should return
// the hop configuration and SSH client config builder for the given signature.
// If nil, Acquire will return an error when trying to create new connections.
func NewConnectionManager(config PoolConfig, hopConfigProvider HopConfigProvider) *ConnectionManager {
	// Create health checker
	healthChecker := NewHealthChecker(HealthCheckConfig{
		Interval: 15 * time.Second,
		Timeout:  5 * time.Second,
	})

	cm := &ConnectionManager{
		pools:             make(map[string]*PooledConnection),
		config:            config,
		hopConfigProvider: hopConfigProvider,
		healthChecker:     healthChecker,
		done:              make(chan struct{}),
	}

	// Start background cleanup goroutine
	cm.wg.Add(1)
	go cm.cleanupLoop()

	return cm
}

// cleanupLoop runs in the background, periodically checking for and removing
// idle connections that have exceeded the timeout.
func (cm *ConnectionManager) cleanupLoop() {
	defer cm.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cm.cleanupIdleConnections()
		case <-cm.done:
			return
		}
	}
}

// cleanupIdleConnections removes idle connections that have exceeded the timeout.
func (cm *ConnectionManager) cleanupIdleConnections() {
	// TODO: Implement in Task 2.3 with lingering logic
	// For now, this is a placeholder
}

// Acquire gets or creates a connection for the given signature.
// If a connection exists in the pool, it increments the ref count and returns it.
// Otherwise, it creates a new connection using the hopConfigProvider.
//
// The forwardID is used to register the forward with the health checker, so that
// if the connection fails, all affected forwards can be notified via context cancellation.
//
// The context is used for cancellation during connection establishment.
//
// Returns an error if:
// - The context is cancelled
// - Connection establishment fails
// - hopConfigProvider is nil and connection doesn't exist
func (cm *ConnectionManager) Acquire(ctx context.Context, sig ConnectionSignature, forwardID string) (*PooledConnection, error) {
	hash := sig.Hash()

	// Fast path: check if connection exists (read lock)
	cm.mu.RLock()
	conn, exists := cm.pools[hash]
	cm.mu.RUnlock()

	if exists {
		// Connection exists, check if it's still alive before returning it
		// This catches connections that have failed (e.g., remote host reboot)
		// but haven't been cleaned up yet.
		if !cm.isConnectionAlive(conn) {
			// Connection is dead, remove it from pool and create a new one
			// We don't hold the lock during removal to avoid blocking other operations
			cm.mu.Lock()
			delete(cm.pools, hash)
			cm.mu.Unlock()

			// Unregister from health checker
			cm.healthChecker.Unregister(conn)

			// Mark as closed
			conn.Close()

			// CRITICAL: Close ALL SSH clients in reverse order (including intermediate hops)
			if len(conn.AllClients) > 0 {
				for i := len(conn.AllClients) - 1; i >= 0; i-- {
					if conn.AllClients[i] != nil {
						_ = conn.AllClients[i].Close()
					}
				}
			} else if conn.Client != nil {
				// Fallback for connections created before AllClients was added
				_ = conn.Client.Close()
			}

			// Continue to create new connection below
		} else {
			// Connection is alive, check if it's idle (lingering)
			if conn.GetStatus() == StatusIdle {
				// Cancel the lingering timer by cancelling the old context
				conn.CancelFunc()

				// Create a new context for this connection
				newCtx, newCancel := context.WithCancel(context.Background())

				// Update the connection's context and cancel function
				// Note: We need to update these atomically, but since we're in the
				// connection manager and have the pool lock, it's safe
				cm.mu.Lock()
				conn.Context = newCtx
				conn.CancelFunc = newCancel
				cm.mu.Unlock()
			}

			// Acquire the connection (increments ref count, sets status to Active)
			if err := conn.Acquire(); err != nil {
				return nil, fmt.Errorf("failed to acquire existing connection: %w", err)
			}

			// Register forward with health checker
			cm.healthChecker.RegisterForward(conn, forwardID)

			return conn, nil
		}
	}

	// Slow path: create new connection using singleflight
	result, err, shared := cm.dialGroup.Do(hash, func() (interface{}, error) {
		// Double-check: connection might have been created while we waited
		cm.mu.RLock()
		if conn, exists := cm.pools[hash]; exists {
			cm.mu.RUnlock()
			return conn, nil
		}
		cm.mu.RUnlock()

		// Create new SSH connection
		// CRITICAL: Save allClients to properly close intermediate hops later
		sshClient, allClients, err := cm.dialSSH(ctx, sig)
		if err != nil {
			return nil, fmt.Errorf("failed to dial SSH: %w", err)
		}

		// Create pooled connection
		connCtx, connCancel := context.WithCancel(context.Background())
		pooledConn := &PooledConnection{
			Client:     sshClient,
			AllClients: allClients, // Store all clients including intermediate hops
			Signature:  sig,
			CreatedAt:  time.Now(),
			LastUsedAt: time.Now(),
			Status:     StatusActive,
			RefCount:   1, // Initial reference for caller
			Context:    connCtx,
			CancelFunc: connCancel,
		}

		// Add to pool
		cm.mu.Lock()
		cm.pools[hash] = pooledConn
		cm.mu.Unlock()

		// Start health checking
		cm.healthChecker.Monitor(pooledConn)

		return pooledConn, nil
	})

	if err != nil {
		// On failure, forget the result to allow retry
		cm.dialGroup.Forget(hash)
		return nil, err
	}

	pooledConn := result.(*PooledConnection)

	// If result was shared (another goroutine created it), increment ref count
	if shared {
		if err := pooledConn.Acquire(); err != nil {
			return nil, fmt.Errorf("failed to acquire shared connection: %w", err)
		}
	}

	// Register forward with health checker
	cm.healthChecker.RegisterForward(pooledConn, forwardID)

	return pooledConn, nil
}

// acquirePartial attempts to acquire an existing partial hop connection from the pool.
// This is used for hop reuse in multi-hop (ProxyJump) scenarios.
// Returns error if not found or connection is dead.
func (cm *ConnectionManager) acquirePartial(ctx context.Context, sig ConnectionSignature, forwardID string) (*PooledConnection, error) {
	hash := sig.Hash()

	cm.mu.RLock()
	conn, exists := cm.pools[hash]
	cm.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("partial connection not found")
	}

	if !cm.isConnectionAlive(conn) {
		return nil, fmt.Errorf("partial connection is dead")
	}

	if err := conn.Acquire(); err != nil {
		return nil, fmt.Errorf("failed to acquire partial connection: %w", err)
	}

	cm.healthChecker.RegisterForward(conn, forwardID)

	return conn, nil
}

// addPartial adds a newly created partial hop connection to the pool for reuse.
// Only adds intermediate hops (not the final destination) to avoid polluting the pool.
func (cm *ConnectionManager) addPartial(sig ConnectionSignature, client *ssh.Client, forwardID string) {
	hash := sig.Hash()

	cm.mu.RLock()
	_, exists := cm.pools[hash]
	cm.mu.RUnlock()

	if exists {
		return
	}

	connCtx, connCancel := context.WithCancel(context.Background())
	pooledConn := &PooledConnection{
		Client:     client,
		Signature:  sig,
		CreatedAt:  time.Now(),
		LastUsedAt: time.Now(),
		Status:     StatusActive,
		RefCount:   1,
		Context:    connCtx,
		CancelFunc: connCancel,
	}

	cm.mu.Lock()
	if _, exists := cm.pools[hash]; exists {
		cm.mu.Unlock()
		connCancel()
		return
	}
	cm.pools[hash] = pooledConn
	cm.mu.Unlock()

	cm.healthChecker.Monitor(pooledConn)
}

// dialSSH establishes a new SSH connection for the given signature.
// It uses the hopConfigProvider to get the hop configuration.
// Returns the final client and all intermediate clients for proper cleanup.
func (cm *ConnectionManager) dialSSH(ctx context.Context, sig ConnectionSignature) (*ssh.Client, []*ssh.Client, error) {
	if cm.hopConfigProvider == nil {
		return nil, nil, fmt.Errorf("cannot create connection: hopConfigProvider is nil")
	}

	hops, builder, err := cm.hopConfigProvider(sig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get hop config: %w", err)
	}

	if builder == nil {
		return nil, nil, fmt.Errorf("builder is nil")
	}

	finalClient, hopInfos, err := EstablishWithReuse(ctx, hops, builder, cm, "")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to establish SSH connection: %w", err)
	}

	allClients := make([]*ssh.Client, len(hopInfos))
	for i, info := range hopInfos {
		allClients[i] = info.Client
	}

	return finalClient, allClients, nil
}

// Release releases a connection back to the pool.
// It decrements the ref count and marks the connection as idle if count reaches 0.
// If the reference count reaches zero, a lingering timer is started to recycle
// the connection after the configured idle timeout.
//
// The forwardID is used to unregister the forward from the health checker.
func (cm *ConnectionManager) Release(conn *PooledConnection, forwardID string) error {
	// Unregister forward from health checker
	cm.healthChecker.UnregisterForward(conn, forwardID)

	// Decrement reference count
	if err := conn.Release(); err != nil {
		return err
	}

	// If ref count reached zero, start lingering timer
	if conn.GetRefCount() == 0 {
		go cm.lingerAndClose(conn)
	}

	return nil
}

// lingerAndClose waits for the idle timeout, then closes the connection.
// If the connection is re-acquired during the lingering period, the context
// will be cancelled and this function will return without closing.
func (cm *ConnectionManager) lingerAndClose(conn *PooledConnection) {
	// Wait for idle timeout or context cancellation
	select {
	case <-time.After(cm.config.IdleTimeout):
		// Timeout reached, close the connection
		cm.closeConnection(conn)
	case <-conn.Context.Done():
		// Connection was re-acquired, cancel lingering
		return
	}
}

// closeConnection removes a connection from the pool and closes the SSH client.
// It is called when a connection's lingering timeout expires.
// CRITICAL: Closes ALL clients in the hop chain (including intermediate hops) to prevent leaks.
func (cm *ConnectionManager) closeConnection(conn *PooledConnection) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	hash := conn.Signature.Hash()

	// Double-check that the connection is still idle and in the pool
	// (It might have been re-acquired and replaced with a new context)
	currentConn, exists := cm.pools[hash]
	if !exists || currentConn != conn {
		// Connection already removed or replaced, nothing to do
		return
	}

	// Check ref count again (might have been re-acquired)
	if conn.GetRefCount() > 0 {
		// Connection was re-acquired, don't close
		return
	}

	// Remove from pool
	delete(cm.pools, hash)

	// Unregister from health checker
	cm.healthChecker.Unregister(conn)

	// Mark as closed
	conn.Close()

	// CRITICAL: Close ALL SSH clients in reverse order (final first, then intermediates)
	// This ensures proper cleanup in multi-hop (ProxyJump) scenarios
	// AllClients order: [hop0, hop1, ..., finalClient]
	// Close order: finalClient, hopN, ..., hop1, hop0
	if len(conn.AllClients) > 0 {
		for i := len(conn.AllClients) - 1; i >= 0; i-- {
			if conn.AllClients[i] != nil {
				_ = conn.AllClients[i].Close()
			}
		}
	} else if conn.Client != nil {
		// Fallback for connections created before AllClients was added
		_ = conn.Client.Close()
	}
}

// isConnectionAlive checks if a pooled connection is still alive
// by sending a lightweight keepalive request.
// This is used in Acquire() to validate cached connections before returning them.
func (cm *ConnectionManager) isConnectionAlive(conn *PooledConnection) bool {
	if conn == nil {
		return false
	}

	// If Client is nil (test scenario), consider connection alive
	// This allows unit tests that mock connections to work
	if conn.Client == nil {
		return true
	}

	// Don't check closed connections
	if conn.GetStatus() == StatusClosed {
		return false
	}

	// Send keepalive request with timeout to detect connection state
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		// keepalive@openssh.com is a standard SSH keepalive request
		_, _, err := conn.Client.SendRequest("keepalive@openssh.com", true, nil)
		done <- err
	}()

	select {
	case err := <-done:
		// If we get any error, the connection is not alive
		return err == nil
	case <-ctx.Done():
		// Timeout means connection is not responding
		return false
	}
}

// Close closes all connections in the pool and stops the background goroutine.
// This method is idempotent - calling it multiple times is safe.
// CRITICAL: Closes ALL clients in hop chains (including intermediate hops) to prevent leaks.
func (cm *ConnectionManager) Close() error {
	var err error
	cm.closeOnce.Do(func() {
		// Signal background goroutine to stop
		close(cm.done)
		cm.wg.Wait()

		// Close all connections
		cm.mu.Lock()
		defer cm.mu.Unlock()

		for hash, conn := range cm.pools {
			// Unregister from health checker
			cm.healthChecker.Unregister(conn)

			conn.Close()
			delete(cm.pools, hash)

			// CRITICAL: Close ALL SSH clients in reverse order
			// This ensures proper cleanup in multi-hop (ProxyJump) scenarios
			if len(conn.AllClients) > 0 {
				for i := len(conn.AllClients) - 1; i >= 0; i-- {
					if conn.AllClients[i] != nil {
						_ = conn.AllClients[i].Close()
					}
				}
			} else if conn.Client != nil {
				// Fallback for connections created before AllClients was added
				_ = conn.Client.Close()
			}
		}

		// Close health checker
		err = cm.healthChecker.Close()
	})
	return err
}

// Stats returns current statistics about the connection pool.
func (cm *ConnectionManager) Stats() PoolStats {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	stats := PoolStats{
		TotalConnections: len(cm.pools),
	}

	for _, conn := range cm.pools {
		status := conn.GetStatus()
		refCount := conn.GetRefCount()

		switch status {
		case StatusActive:
			if refCount > 0 {
				stats.ActiveConnections++
			} else {
				stats.IdleConnections++
			}
		case StatusIdle:
			stats.IdleConnections++
		case StatusClosed:
			stats.ClosedConnections++
		}
	}

	return stats
}
