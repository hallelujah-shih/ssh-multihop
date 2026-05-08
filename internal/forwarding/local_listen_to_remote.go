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

// LocalListenToRemote implements SSH -L: listen on local, forward to remote
//
// Example: ssh -L 127.0.0.1:8888:vmr.u24:8888
//
// Listens on local port and forwards connections to remote service via SSH tunnel.
//
// Simplified design:
// - Only handles connection and health checking
// - On health check failure: sets database status to error and cleans up resources
// - No retry/rebuild logic (managed by ForwardService)
// - Uses connection pool for SSH connection reuse
type LocalListenToRemote struct {
	bindAddr   string
	targetAddr string // Full target service address (e.g., "127.0.0.1:8888" or "192.168.1.100:8888")
	forwardID  string // For database status updates

	// Database for status updates
	db *db.Database

	// Internal state
	status      ForwardStatus
	statusMu    sync.RWMutex
	listener    net.Listener
	cancelFunc  context.CancelFunc
	wg          sync.WaitGroup
	ctx         context.Context
	cleanupOnce sync.Once // Ensure resources are only cleaned up once

	// Connection management - uses connection pool
	pool             *connection.ConnectionManager // Connection pool for SSH connections
	hopChain         []*tunnel.HopConfig
	activeConns      map[net.Conn]struct{} // Track active connections for clean shutdown
	connMu           sync.RWMutex          // Protects activeConns
	passphraseSocket interface{}           // For SSH key passphrase retrieval

	// Health monitoring
	healthCheckInterval time.Duration
	healthCheckTicker   *time.Ticker
	healthCheckDone     chan struct{}
}

// NewLocalListenToRemote creates a new local forward
//
// bindAddr: local address to listen on (e.g., "127.0.0.1:8888")
// targetAddr: target service address (e.g., "127.0.0.1:8888" or "192.168.1.100:8888")
// forwardID: unique identifier for database status updates
// db: database for status updates
// pool: connection pool for SSH connections
// hopChain: SSH hops to traverse
func NewLocalListenToRemote(bindAddr, targetAddr string, forwardID string, db *db.Database, pool *connection.ConnectionManager, hopChain []*tunnel.HopConfig) *LocalListenToRemote {
	return &LocalListenToRemote{
		bindAddr:            bindAddr,
		targetAddr:          targetAddr,
		forwardID:           forwardID,
		db:                  db,
		pool:                pool,
		hopChain:            hopChain,
		status:              StatusStopped,
		activeConns:         make(map[net.Conn]struct{}),
		healthCheckInterval: RandomHealthCheckInterval(),
		healthCheckDone:     make(chan struct{}),
	}
}

// String returns a string representation
func (lf *LocalListenToRemote) String() string {
	return fmt.Sprintf("LocalListenToRemote[%s → %s]", lf.bindAddr, lf.targetAddr)
}

// Type returns LocalListenToRemote
func (lf *LocalListenToRemote) Type() string {
	return "local_listen_to_remote"
}

// Status returns the current status
func (lf *LocalListenToRemote) Status() ForwardStatus {
	lf.statusMu.RLock()
	defer lf.statusMu.RUnlock()
	return lf.status
}

// setStatus updates the status
func (lf *LocalListenToRemote) setStatus(status ForwardStatus) {
	lf.statusMu.Lock()
	defer lf.statusMu.Unlock()
	lf.status = status
}

// setErrorStatus sets database status to error
func (lf *LocalListenToRemote) setErrorStatus(errorMsg string) {
	lf.setStatus(StatusError)

	if lf.db != nil && lf.forwardID != "" {
		forwardStatus := &db.ForwardStatus{
			ForwardID:     lf.forwardID,
			Status:        "error",
			LastHeartbeat: time.Now(),
			ErrorMessage:  errorMsg,
		}

		if err := lf.db.CreateOrUpdateStatus(forwardStatus); err != nil {
			zap.L().Error("Failed to update error status in database",
				zap.String("forward_id", lf.forwardID),
				zap.Error(err))
		} else {
			zap.L().Info("Set database status to error",
				zap.String("forward_id", lf.forwardID),
				zap.String("error", errorMsg))
		}
	}
}

// Start begins listening on local address and forwarding to remote
// This method blocks until the forward is stopped or an error occurs.
// On error, sets database status to error and cleans up resources.
func (lf *LocalListenToRemote) Start(ctx context.Context) error {
	lf.setStatus(StatusStopped)

	// Create cancellable context
	innerCtx, cancel := context.WithCancel(ctx)
	lf.ctx = innerCtx
	lf.cancelFunc = cancel
	defer func() {
		if lf.Status() != StatusRunning {
			// Failed to start, set database status to error
			lf.setErrorStatus("failed to start")
		}
	}()

	// Validate connection pool
	if lf.pool == nil {
		zap.L().Error("Connection pool is nil",
			zap.String("forward", lf.String()))
		return fmt.Errorf("connection pool is nil")
	}

	// Create listener (supports both TCP and UDS)
	network := "tcp"
	bindAddr := lf.bindAddr
	if len(lf.bindAddr) > 7 && lf.bindAddr[:7] == "unix://" {
		network = "unix"
		bindAddr = lf.bindAddr[7:] // Strip "unix://" prefix
	}

	listener, err := net.Listen(network, bindAddr)
	if err != nil {
		zap.L().Error("Failed to listen",
			zap.String("forward", lf.String()),
			zap.String("bind", lf.bindAddr),
			zap.Error(err))
		return fmt.Errorf("failed to create listener: %w", err)
	}

	// Success! Store listener and start forward
	lf.listener = listener

	// CRITICAL: Setup deferred cleanup for partially initialized resources
	// If anything fails after this point (before goroutines start), ensure cleanup
	// Use cleanupOnce to avoid double-cleanup if Stop() is also called
	defer func() {
		if lf.Status() != StatusRunning {
			// Trigger unified cleanup if we haven't reached Running status
			// cleanupOnce ensures cleanup only runs once, even if Stop() was also called
			lf.cleanupOnce.Do(func() {
				zap.L().Warn("Cleaning up partially initialized local forward",
					zap.String("forward", lf.String()))

				// Stop health monitoring if it was started
				if lf.healthCheckDone != nil {
					close(lf.healthCheckDone)
				}
				if lf.healthCheckTicker != nil {
					lf.healthCheckTicker.Stop()
				}

				// Close listener
				if lf.listener != nil {
					_ = lf.listener.Close()
				}

				zap.L().Info("Partial cleanup complete",
					zap.String("forward", lf.String()))
			})
		}
	}()

	zap.L().Info("Local forward listening",
		zap.String("forward", lf.String()),
		zap.String("bind", lf.bindAddr))

	lf.setStatus(StatusRunning)

	// Start health monitoring
	lf.startHealthMonitoring(innerCtx)

	// Start accept loop
	lf.wg.Add(1) // Add acceptLoop to WaitGroup
	go lf.acceptLoop(innerCtx)

	// Start unified cleanup goroutine
	// This ensures all resources are cleaned up when context is canceled
	lf.wg.Add(1)
	go lf.cleanupMonitor(innerCtx)

	return nil
}

// acceptLoop accepts connections and forwards them to remote
func (lf *LocalListenToRemote) acceptLoop(ctx context.Context) {
	defer lf.wg.Done()

	// Track if acceptLoop exits due to error (not normal shutdown)
	exitedWithError := false
	defer func() {
		// CRITICAL: If acceptLoop exits unexpectedly, set error status
		// This triggers ForwardService to rebuild the forward
		if exitedWithError && lf.Status() == StatusRunning {
			zap.L().Error("Accept loop exited unexpectedly due to error",
				zap.String("forward", lf.String()))
			lf.setErrorStatus("accept loop failed unexpectedly")
		}
	}()

	for {
		// Check context before blocking accept
		select {
		case <-ctx.Done():
			zap.L().Info("Accept loop stopped by context",
				zap.String("forward", lf.String()))
			return
		default:
		}

		// Set accept deadline to allow checking context
		tcpListener, ok := lf.listener.(*net.TCPListener)
		if ok {
			if err := tcpListener.SetDeadline(time.Now().Add(1 * time.Second)); err != nil {
				zap.L().Error("Failed to set deadline",
					zap.String("forward", lf.String()),
					zap.Error(err))
				exitedWithError = true
				return
			}
		}

		conn, err := lf.listener.Accept()
		if err != nil {
			// Timeout is expected (due to deadline) - not an error
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			// Check if this is a normal shutdown (listener closed)
			if err.Error() == "use of closed network connection" {
				// Normal shutdown - not an error, don't set exitedWithError
				return
			}
			// Real error - mark as error exit
			exitedWithError = true
			zap.L().Error("Accept error",
				zap.String("forward", lf.String()),
				zap.Error(err))
			return
		}

		// Handle connection
		lf.wg.Add(1)
		go func() {
			defer lf.wg.Done()
			lf.handleConnection(conn)
		}()
	}
}

// handleConnection forwards a single connection
func (lf *LocalListenToRemote) handleConnection(localConn net.Conn) {
	defer func() {
		if err := localConn.Close(); err != nil {
			zap.L().Debug("Error closing local connection", zap.Error(err))
		}
	}()

	// Build connection signature for pooled connection
	sig := lf.buildSignature()

	// Create MultiplexedForward to use connection pool
	mf := connection.NewMultiplexedForward(lf.pool, sig, lf.forwardID)
	defer func() { _ = mf.Close() }() // Release connection back to pool when done

	// Create SSH channel to target service
	remoteConn, err := mf.NewChannel(lf.targetAddr)
	if err != nil {
		zap.L().Error("Failed to create SSH channel",
			zap.String("forward", lf.String()),
			zap.String("target", lf.targetAddr),
			zap.String("forward_id", lf.forwardID),
			zap.Error(err))
		lf.setErrorStatus(fmt.Sprintf("failed to create SSH channel: %v", err))
		return
	}
	defer func() {
		if err := remoteConn.Close(); err != nil {
			zap.L().Debug("Error closing remote connection", zap.Error(err))
		}
	}()

	// Register connections in active pool for clean shutdown
	lf.connMu.Lock()
	if lf.activeConns == nil {
		lf.activeConns = make(map[net.Conn]struct{})
	}
	lf.activeConns[localConn] = struct{}{}
	lf.activeConns[remoteConn] = struct{}{}
	lf.connMu.Unlock()

	// Unregister connections when done
	defer func() {
		lf.connMu.Lock()
		delete(lf.activeConns, localConn)
		delete(lf.activeConns, remoteConn)
		lf.connMu.Unlock()
	}()

	// Bidirectional forwarding
	zap.L().Debug("Forwarding connection",
		zap.String("local", localConn.RemoteAddr().String()),
		zap.String("remote", lf.targetAddr))

	// Start forwarding using utility
	if err := bidirectionalCopy(localConn, remoteConn); err != nil {
		if isNormalCloseError(err) {
			// Normal connection close - debug level
			zap.L().Debug("Forwarding stopped",
				zap.String("error", err.Error()))
		} else {
			// Abnormal error - info level
			zap.L().Info("Forwarding stopped",
				zap.String("forward", lf.String()),
				zap.String("error", err.Error()))
		}
	}
}

// Stop stops the local forward by triggering unified cleanup
// The actual resource cleanup is performed by cleanupMonitor goroutine
//
// ⚠️ CRITICAL: StatusStopped means "user manually stopped, DO NOT rebuild"
// - Health check failures should NOT call Stop() - they only call cancel()
// - Stop() sets StatusStopped, then triggers cleanup via cancel()
// - All cleanup logic is unified in cleanupMonitor goroutine
func (lf *LocalListenToRemote) Stop() error {
	zap.L().Info("Stopping local forward",
		zap.String("forward", lf.String()))

	lf.setStatus(StatusStopped)

	// Trigger unified cleanup by canceling context
	// cleanupMonitor will handle all resource cleanup
	if lf.cancelFunc != nil {
		lf.cancelFunc()
	}

	// Wait for all goroutines to finish cleanup
	lf.wg.Wait()

	zap.L().Info("Local forward stopped",
		zap.String("forward", lf.String()))

	return nil
}

// cleanupMonitor waits for context cancellation and performs unified resource cleanup
// This ensures ALL exit paths (Stop, health check failure, etc.) use the SAME cleanup logic
func (lf *LocalListenToRemote) cleanupMonitor(ctx context.Context) {
	defer lf.wg.Done()

	<-ctx.Done()
	zap.L().Info("Context canceled, starting unified resource cleanup",
		zap.String("forward", lf.String()))

	// Use sync.Once to ensure cleanup only happens once
	// This is critical when both Stop() and health check failure trigger cleanup
	lf.cleanupOnce.Do(func() {
		zap.L().Info("Starting unified resource cleanup",
			zap.String("forward", lf.String()))

		// Step 1: Stop health monitoring
		if lf.healthCheckDone != nil {
			close(lf.healthCheckDone)
		}
		if lf.healthCheckTicker != nil {
			lf.healthCheckTicker.Stop()
		}

		// Step 2: Close listener FIRST to stop accepting new connections
		if lf.listener != nil {
			if err := lf.listener.Close(); err != nil {
				zap.L().Debug("Error closing listener", zap.Error(err))
			}
		}

		// Step 3: Close all active connections
		// This causes bidirectionalCopy to fail immediately with "use of closed network connection"
		lf.connMu.Lock()
		activeConnCount := len(lf.activeConns)
		for conn := range lf.activeConns {
			zap.L().Debug("Closing active connection",
				zap.String("remote_addr", conn.RemoteAddr().String()))
			_ = conn.Close()
		}
		lf.activeConns = make(map[net.Conn]struct{})
		lf.connMu.Unlock()

		if activeConnCount > 0 {
			zap.L().Info("Closed active connections",
				zap.Int("count", activeConnCount))
		}

		// Note: SSH connections are managed by the connection pool
		// They are automatically released when MultiplexedForward.Close() is called
		// in handleConnection defer

		zap.L().Info("Unified resource cleanup complete",
			zap.String("forward", lf.String()))
	})
}

// HealthCheck checks if the forward is healthy by observing the SSH client state.
// It does not actively send keepalives (ConnectionPool already does that),
// but rather observes whether the connection is still alive.
//
// For LocalListenToRemote, which uses MultiplexedForward (ephemeral connections),
// we temporarily acquire a connection from the pool to observe its state.
func (lf *LocalListenToRemote) HealthCheck() error {
	if lf.Status() != StatusRunning {
		return fmt.Errorf("forward not running (status: %s)", lf.Status())
	}

	// Check if listener is still active
	if lf.listener == nil {
		return fmt.Errorf("listener is nil")
	}

	// Temporarily acquire a connection from the pool to observe its state
	// This ensures we can detect connection failures even when idle
	sig := lf.buildSignature()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	pooledConn, err := lf.pool.Acquire(ctx, sig, lf.forwardID)
	if err != nil {
		return fmt.Errorf("failed to acquire connection for health check: %w", err)
	}
	defer func() {
		_ = lf.pool.Release(pooledConn, lf.forwardID)
	}()

	// Observe the connection state
	if err := lf.observeConnectionState(pooledConn.Client); err != nil {
		return fmt.Errorf("connection observation failed: %w", err)
	}

	return nil
}

// observeConnectionState observes the SSH client's connection state.
// It sends a lightweight keepalive request to detect if the connection is still alive.
func (lf *LocalListenToRemote) observeConnectionState(client *ssh.Client) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("connection not alive: %w", err)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("connection observation timeout")
	}
}

// startHealthMonitoring starts the health monitoring goroutine
func (lf *LocalListenToRemote) startHealthMonitoring(ctx context.Context) {
	lf.wg.Add(1)
	lf.healthCheckTicker = time.NewTicker(lf.healthCheckInterval)

	go func() {
		defer lf.wg.Done()
		defer zap.L().Info("Health monitoring stopped",
			zap.String("forward", lf.String()))

		for {
			select {
			case <-ctx.Done():
				return
			case <-lf.healthCheckDone:
				return
			case <-lf.healthCheckTicker.C:
				if err := lf.HealthCheck(); err != nil {
					zap.L().Warn("Health check failed, setting error status",
						zap.String("forward", lf.String()),
						zap.Error(err))

					// Set database status to error
					lf.setErrorStatus(err.Error())

					// Trigger unified cleanup by canceling context
					// cleanupMonitor will handle all resource cleanup including closing listener
					if lf.cancelFunc != nil {
						lf.cancelFunc()
					}
					return
				}
			}
		}
	}()

	zap.L().Debug("Health monitoring started",
		zap.String("forward", lf.String()),
		zap.String("interval", lf.healthCheckInterval.String()))
}

// Removed: attemptRepair and reconnect methods
// Rebuild logic is now handled by ForwardService

// SetPassphraseSocket sets the passphrase socket for retrieving SSH key passphrases
func (lf *LocalListenToRemote) SetPassphraseSocket(ps interface{}) {
	lf.passphraseSocket = ps
}

// buildSignature creates a ConnectionSignature from the hop chain
func (lf *LocalListenToRemote) buildSignature() connection.ConnectionSignature {
	if len(lf.hopChain) == 0 {
		return connection.ConnectionSignature{}
	}

	// Get the final destination hop (last in chain)
	finalHop := lf.hopChain[len(lf.hopChain)-1]

	// Build jump chain from all hops except the last
	jumpChain := make([]string, 0, len(lf.hopChain)-1)
	for i := 0; i < len(lf.hopChain)-1; i++ {
		jumpChain = append(jumpChain, lf.hopChain[i].Host)
	}

	return connection.ConnectionSignature{
		Username:  finalHop.User,
		Hostname:  finalHop.HostName,
		Port:      finalHop.Port,
		JumpChain: jumpChain,
	}
}
