package forwarding

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/hallelujah-shih/ssh-multihop/internal/connection"
	"github.com/hallelujah-shih/ssh-multihop/internal/db"
	"github.com/hallelujah-shih/ssh-multihop/internal/tunnel"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

// RemoteListenToRemote implements remote-to-remote bridging without local port
//
// Example: vmr.u24:11434 → dc4:11434 (no local port)
//
// Bridges two SSH clients without binding a local port.
//
// Simplified design:
// - Only handles connection and health checking
// - On health check failure: sets database status to error and cleans up resources
// - No retry/rebuild logic (managed by ForwardService)
// - Uses connection pool for SSH connection reuse
type RemoteListenToRemote struct {
	// Source (first hop) - connections come FROM here
	sourceAddr string // Full listen address (e.g., "127.0.0.1:11434")
	sourceHost string // SSH hostname for source
	sourcePort int
	sourceHop  []*tunnel.HopConfig

	// Target (second hop) - connections go TO here
	targetAddr string // Full service address (e.g., "127.0.0.1:11434")
	targetHost string // SSH hostname for target
	targetPort int
	targetHop  []*tunnel.HopConfig

	// Database for status updates
	forwardID string
	db        *db.Database

	// Internal state
	status      ForwardStatus
	statusMu    sync.RWMutex
	cancelFunc  context.CancelFunc
	wg          sync.WaitGroup
	ctx         context.Context
	cleanupOnce sync.Once // Ensure resources are only cleaned up once

	// Connection management - uses connection pool
	pool             *connection.ConnectionManager // Connection pool for SSH connections
	sourcePooledConn *connection.PooledConnection  // Active pooled connection for source (listener)
	targetPooledConn *connection.PooledConnection  // Active pooled connection for target (service)
	listener         net.Listener                  // Listener on source host for accepting connections
	connMap          map[net.Conn]struct{}         // Track active connections for clean shutdown
	connMu           sync.RWMutex                  // Protects connMap
	passphraseSocket interface{}                   // For SSH key passphrase retrieval

	// Configuration
	maxConns int

	// Health monitoring
	healthCheckInterval time.Duration
	healthCheckTicker   *time.Ticker
	healthCheckDone     chan struct{}
}

// NewRemoteListenToRemote creates a new inline forward
//
// sourceAddr: full listen address (e.g., "127.0.0.1:11434")
// sourceHost: source SSH hostname
// sourcePort: source port number
// sourceHop: SSH hops to reach source
// targetAddr: full service address (e.g., "127.0.0.1:11434")
// targetHost: target SSH hostname
// targetPort: target port number
// targetHop: SSH hops to reach target
// forwardID: unique identifier for database status updates
// db: database for status updates
// pool: connection pool for SSH connections
// maxConns: maximum concurrent connections (0 = unlimited)
func NewRemoteListenToRemote(sourceAddr, sourceHost string, sourcePort int, sourceHop []*tunnel.HopConfig,
	targetAddr, targetHost string, targetPort int, targetHop []*tunnel.HopConfig,
	forwardID string, db *db.Database, pool *connection.ConnectionManager, maxConns int) *RemoteListenToRemote {

	return &RemoteListenToRemote{
		sourceAddr:          sourceAddr,
		sourceHost:          sourceHost,
		sourcePort:          sourcePort,
		sourceHop:           sourceHop,
		targetAddr:          targetAddr,
		targetHost:          targetHost,
		targetPort:          targetPort,
		targetHop:           targetHop,
		forwardID:           forwardID,
		db:                  db,
		pool:                pool,
		status:              StatusStopped,
		maxConns:            maxConns,
		connMap:             make(map[net.Conn]struct{}),
		healthCheckInterval: RandomHealthCheckInterval(),
		healthCheckDone:     make(chan struct{}),
	}
}

// String returns a string representation
func (inf *RemoteListenToRemote) String() string {
	return fmt.Sprintf("RemoteListenToRemote[%s:%d → %s:%d]",
		inf.sourceHost, inf.sourcePort, inf.targetHost, inf.targetPort)
}

// Type returns RemoteListenToRemote
func (inf *RemoteListenToRemote) Type() string {
	return "remote_listen_to_remote"
}

// Status returns the current status
func (inf *RemoteListenToRemote) Status() ForwardStatus {
	inf.statusMu.RLock()
	defer inf.statusMu.RUnlock()
	return inf.status
}

// setStatus updates the status
func (inf *RemoteListenToRemote) setStatus(status ForwardStatus) {
	inf.statusMu.Lock()
	defer inf.statusMu.Unlock()
	inf.status = status
}

// setErrorStatus sets database status to error
func (inf *RemoteListenToRemote) setErrorStatus(errorMsg string) {
	inf.setStatus(StatusError)

	if inf.db != nil && inf.forwardID != "" {
		forwardStatus := &db.ForwardStatus{
			ForwardID:     inf.forwardID,
			Status:        "error",
			LastHeartbeat: time.Now(),
			ErrorMessage:  errorMsg,
		}

		if err := inf.db.CreateOrUpdateStatus(forwardStatus); err != nil {
			zap.L().Error("Failed to update error status in database",
				zap.String("forward_id", inf.forwardID),
				zap.Error(err))
		} else {
			zap.L().Info("Set database status to error",
				zap.String("forward_id", inf.forwardID),
				zap.String("error", errorMsg))
		}
	}
}

// Start begins bridging two remote endpoints
// This method blocks until the forward is stopped or an error occurs.
// On error, sets database status to error and cleans up resources.
func (inf *RemoteListenToRemote) Start(ctx context.Context) error {
	inf.setStatus(StatusStopped)

	// Create cancellable context
	innerCtx, cancel := context.WithCancel(ctx)
	inf.ctx = innerCtx
	inf.cancelFunc = cancel
	defer func() {
		if inf.Status() != StatusRunning {
			// Failed to start, set database status to error
			inf.setErrorStatus("failed to start")
		}
	}()

	// Validate connection pool
	if inf.pool == nil {
		zap.L().Error("Connection pool is nil",
			zap.String("forward", inf.String()))
		return fmt.Errorf("connection pool is nil")
	}

	// Acquire source connection from pool (for listener)
	zap.L().Debug("Acquiring source connection from pool for inline forward",
		zap.String("source", fmt.Sprintf("%s:%d", inf.sourceHost, inf.sourcePort)))

	sourceSig := inf.buildListenSignature()
	sourcePooledConn, err := inf.pool.Acquire(innerCtx, sourceSig, inf.forwardID)
	if err != nil {
		zap.L().Error("Failed to acquire source connection from pool",
			zap.String("forward", inf.String()),
			zap.Error(err))
		return fmt.Errorf("failed to acquire source connection: %w", err)
	}

	// Acquire target connection from pool (for service)
	zap.L().Debug("Acquiring target connection from pool for inline forward",
		zap.String("target", fmt.Sprintf("%s:%d", inf.targetHost, inf.targetPort)))

	targetSig := inf.buildServiceSignature()
	targetPooledConn, err := inf.pool.Acquire(innerCtx, targetSig, inf.forwardID)
	if err != nil {
		// Release source connection if target acquisition fails
		_ = inf.pool.Release(sourcePooledConn, inf.forwardID)
		zap.L().Error("Failed to acquire target connection from pool",
			zap.String("forward", inf.String()),
			zap.Error(err))
		return fmt.Errorf("failed to acquire target connection: %w", err)
	}

	// Create listener on source host
	// Listen on sourceAddr (e.g., "127.0.0.1:11434") through SSH connection
	listener, err := sourcePooledConn.Client.Listen("tcp", inf.sourceAddr)
	if err != nil {
		// Release both connections if listener creation fails
		_ = inf.pool.Release(sourcePooledConn, inf.forwardID)
		_ = inf.pool.Release(targetPooledConn, inf.forwardID)
		zap.L().Error("Failed to listen on source",
			zap.String("forward", inf.String()),
			zap.String("source_listen", inf.sourceAddr),
			zap.Error(err))
		return fmt.Errorf("failed to create listener: %w", err)
	}

	// Success! Store resources and start forward
	inf.sourcePooledConn = sourcePooledConn
	inf.targetPooledConn = targetPooledConn
	inf.listener = listener

	// CRITICAL: Setup deferred cleanup for partially initialized resources
	// If anything fails after this point (before goroutines start), ensure cleanup
	// Use cleanupOnce to avoid double-cleanup if Stop() is also called
	defer func() {
		if inf.Status() != StatusRunning {
			// Trigger unified cleanup if we haven't reached Running status
			// cleanupOnce ensures cleanup only runs once, even if Stop() was also called
			inf.cleanupOnce.Do(func() {
				zap.L().Warn("Cleaning up partially initialized forward",
					zap.String("forward", inf.String()))

				// Stop health monitoring if it was started
				if inf.healthCheckDone != nil {
					close(inf.healthCheckDone)
				}
				if inf.healthCheckTicker != nil {
					inf.healthCheckTicker.Stop()
				}

				// Release pooled connections back to pool
				if inf.sourcePooledConn != nil {
					zap.L().Info("Releasing source pooled connection",
						zap.String("forward", inf.String()))
					if err := inf.pool.Release(inf.sourcePooledConn, inf.forwardID); err != nil {
						zap.L().Error("Failed to release source pooled connection",
							zap.String("forward", inf.String()),
							zap.Error(err))
					}
					inf.sourcePooledConn = nil
				}
				if inf.targetPooledConn != nil {
					zap.L().Debug("Releasing target pooled connection",
						zap.String("forward", inf.String()))
					if err := inf.pool.Release(inf.targetPooledConn, inf.forwardID); err != nil {
						zap.L().Error("Failed to release target pooled connection",
							zap.String("forward", inf.String()),
							zap.Error(err))
					}
					inf.targetPooledConn = nil
				}

				// Close listener object (no-op if SSH already closed it)
				if inf.listener != nil {
					_ = inf.listener.Close()
				}

				zap.L().Info("Partial cleanup complete",
					zap.String("forward", inf.String()))
			})
		}
	}()

	zap.L().Info("Inline forward started",
		zap.String("forward", inf.String()),
		zap.String("source_listen", inf.sourceAddr))

	inf.setStatus(StatusRunning)

	// Start health monitoring
	inf.startHealthMonitoring(innerCtx)

	// Start accept loop
	inf.wg.Add(1) // Add acceptLoop to WaitGroup
	go inf.acceptLoop(innerCtx)

	// Start unified cleanup goroutine
	// This ensures all resources are cleaned up when context is canceled
	inf.wg.Add(1)
	go inf.cleanupMonitor(innerCtx)

	return nil
}

// cleanupMonitor waits for context cancellation and performs unified resource cleanup
// This ensures ALL exit paths (Stop, health check failure, etc.) use the SAME cleanup logic
func (inf *RemoteListenToRemote) cleanupMonitor(ctx context.Context) {
	defer inf.wg.Done()

	<-ctx.Done()
	zap.L().Info("Context canceled, starting unified resource cleanup",
		zap.String("forward", inf.String()))

	// Use sync.Once to ensure cleanup only happens once
	// This is critical when both Stop() and health check failure trigger cleanup
	inf.cleanupOnce.Do(func() {
		zap.L().Info("Starting unified resource cleanup",
			zap.String("forward", inf.String()))

		// Step 1: Stop health monitoring
		if inf.healthCheckDone != nil {
			close(inf.healthCheckDone)
		}
		if inf.healthCheckTicker != nil {
			inf.healthCheckTicker.Stop()
		}

		// Step 2: Close listener FIRST to stop accepting new connections
		// Note: This will be closed again by SSH connection cleanup (safe no-op)
		if inf.listener != nil {
			if err := inf.listener.Close(); err != nil {
				zap.L().Debug("Error closing listener", zap.Error(err))
			}
		}

		// Step 3: Close all active connections
		// This causes bidirectionalCopy to fail immediately with "use of closed network connection"
		inf.connMu.Lock()
		activeConnCount := len(inf.connMap)
		for conn := range inf.connMap {
			zap.L().Debug("Closing active connection",
				zap.String("remote_addr", conn.RemoteAddr().String()))
			_ = conn.Close()
		}
		inf.connMap = make(map[net.Conn]struct{})
		inf.connMu.Unlock()

		if activeConnCount > 0 {
			zap.L().Info("Closed active connections",
				zap.Int("count", activeConnCount))
		}

		// Step 4: Release pooled connections back to pool
		// This is critical for SSH remote listeners!
		// SSH remote listeners are created on the SSH connection.
		// Closing the SSH connection will automatically clean up ALL listeners
		// on that connection, including our remote listener.
		if inf.sourcePooledConn != nil {
			zap.L().Info("Releasing source pooled connection (will auto-cleanup remote listeners)",
				zap.String("forward", inf.String()))
			if err := inf.pool.Release(inf.sourcePooledConn, inf.forwardID); err != nil {
				zap.L().Error("Failed to release source pooled connection",
					zap.String("forward", inf.String()),
					zap.Error(err))
			}
			inf.sourcePooledConn = nil
		}
		if inf.targetPooledConn != nil {
			zap.L().Debug("Releasing target pooled connection",
				zap.String("forward", inf.String()))
			if err := inf.pool.Release(inf.targetPooledConn, inf.forwardID); err != nil {
				zap.L().Error("Failed to release target pooled connection",
					zap.String("forward", inf.String()),
					zap.Error(err))
			}
			inf.targetPooledConn = nil
		}

		zap.L().Info("Unified resource cleanup complete",
			zap.String("forward", inf.String()))
	})
}

// acceptLoop accepts connections from source listener and forwards to target
func (inf *RemoteListenToRemote) acceptLoop(ctx context.Context) {
	defer inf.wg.Done()

	// Track if acceptLoop exits due to error (not normal shutdown)
	exitedWithError := false
	defer func() {
		// CRITICAL: If acceptLoop exits unexpectedly, set error status
		// This triggers ForwardService to rebuild the forward
		if exitedWithError && inf.Status() == StatusRunning {
			zap.L().Error("Accept loop exited unexpectedly due to error",
				zap.String("forward", inf.String()))
			inf.setErrorStatus("accept loop failed unexpectedly")
		}
	}()

	for {
		// Check context before blocking accept
		select {
		case <-ctx.Done():
			zap.L().Info("Accept loop stopped by context",
				zap.String("forward", inf.String()))
			return
		default:
		}

		// Accept connection from target listener
		conn, err := inf.listener.Accept()
		if err != nil {
			// Check if context was canceled
			select {
			case <-ctx.Done():
				return
			default:
			}

			// Check if this is a normal shutdown (listener closed)
			if err.Error() == "use of closed network connection" {
				// Normal shutdown - not an error, don't set exitedWithError
				return
			}

			// Real error - mark as error exit
			exitedWithError = true
			zap.L().Error("Accept error",
				zap.String("forward", inf.String()),
				zap.Error(err))
			return
		}

		// Handle connection
		inf.wg.Add(1)
		go func() {
			defer inf.wg.Done()
			inf.handleConnection(conn)
		}()
	}
}

// handleConnection forwards a single connection from source listener to target
func (inf *RemoteListenToRemote) handleConnection(sourceConn net.Conn) {
	defer func() {
		if err := sourceConn.Close(); err != nil {
			zap.L().Debug("Error closing source connection", zap.Error(err))
		}
	}()

	zap.L().Debug("Handling inline connection",
		zap.String("source_remote", sourceConn.RemoteAddr().String()))

	// Create SSH channel to target service using target pooled connection
	// targetAddr is the full service address (e.g., "127.0.0.1:11434" or "0.0.0.0:11434")
	targetConn, err := inf.targetPooledConn.Client.Dial("tcp", inf.targetAddr)
	if err != nil {
		zap.L().Error("Failed to dial target service",
			zap.String("forward", inf.String()),
			zap.String("target", inf.targetAddr),
			zap.String("target_host", inf.targetHost),
			zap.String("forward_id", inf.forwardID),
			zap.Error(err))
		return
	}
	defer func() {
		if err := targetConn.Close(); err != nil {
			zap.L().Debug("Error closing target connection", zap.Error(err))
		}
	}()

	// Register connections in active pool for clean shutdown
	inf.connMu.Lock()
	if inf.connMap == nil {
		inf.connMap = make(map[net.Conn]struct{})
	}
	inf.connMap[sourceConn] = struct{}{}
	inf.connMap[targetConn] = struct{}{}
	inf.connMu.Unlock()

	// Unregister connections when done
	defer func() {
		inf.connMu.Lock()
		delete(inf.connMap, sourceConn)
		delete(inf.connMap, targetConn)
		inf.connMu.Unlock()
	}()

	// Bidirectional forwarding
	zap.L().Debug("Bridging inline connection",
		zap.String("target", inf.targetAddr),
		zap.String("source_remote", sourceConn.RemoteAddr().String()))

	if err := bidirectionalCopy(targetConn, sourceConn); err != nil {
		if isNormalCloseError(err) {
			// Normal connection close - debug level
			zap.L().Debug("Inline forwarding stopped",
				zap.String("error", err.Error()))
		} else {
			// Abnormal error - info level
			zap.L().Info("Inline forwarding stopped",
				zap.String("forward", inf.String()),
				zap.String("error", err.Error()))
		}
	}
}

// Stop stops the inline forward by triggering unified cleanup
// The actual resource cleanup is performed by cleanupMonitor goroutine
//
// ⚠️ CRITICAL: StatusStopped means "user manually stopped, DO NOT rebuild"
// - Health check failures should NOT call Stop() - they only call cancel()
// - Stop() sets StatusStopped, then triggers cleanup via cancel()
// - All cleanup logic is unified in cleanupMonitor goroutine
func (inf *RemoteListenToRemote) Stop() error {
	zap.L().Info("Stopping inline forward",
		zap.String("forward", inf.String()))

	inf.setStatus(StatusStopped)

	// Trigger unified cleanup by canceling context
	// cleanupMonitor will handle all resource cleanup
	if inf.cancelFunc != nil {
		inf.cancelFunc()
	}

	// Wait for all goroutines to finish cleanup
	inf.wg.Wait()

	zap.L().Info("Inline forward stopped",
		zap.String("forward", inf.String()))

	return nil
}

// HealthCheck checks the health of the forward.
//
// Health monitoring is primarily handled by the ConnectionManager's
// healthChecker observes the state of the SSH connections used by this forward.
// It does not actively send keepalives (ConnectionPool already does that),
// but rather observes whether the connections are still alive.
//
// If a connection has failed, this method returns an error, which triggers
// the forward to be marked as failed and rebuilt by ForwardService.
func (inf *RemoteListenToRemote) HealthCheck() error {
	if inf.Status() != StatusRunning {
		return fmt.Errorf("forward not running (status: %s)", inf.Status())
	}

	// Check listener exists
	if inf.listener == nil {
		return fmt.Errorf("listener is nil")
	}

	// Observe source connection state
	if inf.sourcePooledConn != nil && inf.sourcePooledConn.Client != nil {
		if err := inf.observeConnectionState(inf.sourcePooledConn.Client); err != nil {
			return fmt.Errorf("source connection failed: %w", err)
		}
	}

	// Observe target connection state
	if inf.targetPooledConn != nil && inf.targetPooledConn.Client != nil {
		if err := inf.observeConnectionState(inf.targetPooledConn.Client); err != nil {
			return fmt.Errorf("target connection failed: %w", err)
		}
	}

	return nil
}

// observeConnectionState observes the SSH client's connection state.
// It sends a lightweight keepalive request to detect if the connection is still alive.
// This is not an active health check (ConnectionPool does that), but rather
// an observation of the connection's current state from the Forward's perspective.
func (inf *RemoteListenToRemote) observeConnectionState(client *ssh.Client) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		// SendRequest with keepalive@openssh.com is a standard way to check connection state
		// The second parameter (true) means we want a reply
		_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
		done <- err
	}()

	select {
	case err := <-done:
		// If we get any error, the connection is not alive
		// This includes:
		// - Connection closed
		// - Write error (connection broken)
		// - Timeout waiting for reply
		if err != nil {
			return fmt.Errorf("connection not alive: %w", err)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("connection observation timeout")
	}
}

// startHealthMonitoring starts the health monitoring goroutine
func (inf *RemoteListenToRemote) startHealthMonitoring(ctx context.Context) {
	inf.wg.Add(1)
	inf.healthCheckTicker = time.NewTicker(inf.healthCheckInterval)

	go func() {
		defer inf.wg.Done()
		defer zap.L().Info("Health monitoring stopped",
			zap.String("forward", inf.String()))

		for {
			select {
			case <-ctx.Done():
				return
			case <-inf.healthCheckDone:
				return
			case <-inf.healthCheckTicker.C:
				if err := inf.HealthCheck(); err != nil {
					zap.L().Warn("Health check failed, setting error status",
						zap.String("forward", inf.String()),
						zap.Error(err))

					// Set database status to error
					inf.setErrorStatus(err.Error())

					// Trigger unified cleanup by canceling context
					// cleanupMonitor will handle all resource cleanup including closing listener
					if inf.cancelFunc != nil {
						inf.cancelFunc()
					}
					return
				}
			}
		}
	}()

	zap.L().Debug("Health monitoring started",
		zap.String("forward", inf.String()),
		zap.String("interval", inf.healthCheckInterval.String()))
}

// Removed: attemptRepair, reconnect, monitorListenerHealth, and calculateBackoff methods
// Rebuild logic is now handled by ForwardService

// SetPassphraseSocket sets the passphrase socket for retrieving SSH key passphrases
func (inf *RemoteListenToRemote) SetPassphraseSocket(ps interface{}) {
	inf.passphraseSocket = ps
}

// buildListenSignature creates a ConnectionSignature for the listener endpoint (source)
// The listener is created on the source host, where connections come FROM
func (inf *RemoteListenToRemote) buildListenSignature() connection.ConnectionSignature {
	if len(inf.sourceHop) == 0 {
		return connection.ConnectionSignature{}
	}

	// Get the final destination hop (last in chain)
	finalHop := inf.sourceHop[len(inf.sourceHop)-1]

	// Build jump chain from all hops except the last
	jumpChain := make([]string, 0, len(inf.sourceHop)-1)
	for i := 0; i < len(inf.sourceHop)-1; i++ {
		jumpChain = append(jumpChain, inf.sourceHop[i].Host)
	}

	return connection.ConnectionSignature{
		Username:  finalHop.User,
		Hostname:  finalHop.HostName,
		Port:      finalHop.Port,
		JumpChain: jumpChain,
	}
}

// buildServiceSignature creates a ConnectionSignature for the service endpoint (target)
// The service runs on the target host, where connections go TO
func (inf *RemoteListenToRemote) buildServiceSignature() connection.ConnectionSignature {
	if len(inf.targetHop) == 0 {
		return connection.ConnectionSignature{}
	}

	// Get the final destination hop (last in chain)
	finalHop := inf.targetHop[len(inf.targetHop)-1]

	// Build jump chain from all hops except the last
	jumpChain := make([]string, 0, len(inf.targetHop)-1)
	for i := 0; i < len(inf.targetHop)-1; i++ {
		jumpChain = append(jumpChain, inf.targetHop[i].Host)
	}

	return connection.ConnectionSignature{
		Username:  finalHop.User,
		Hostname:  finalHop.HostName,
		Port:      finalHop.Port,
		JumpChain: jumpChain,
	}
}
