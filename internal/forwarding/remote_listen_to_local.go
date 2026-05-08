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
	"github.com/hallelujah-shih/ssh-multihop/internal/util"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

// RemoteListenToLocal implements SSH -R: local service → remote listen
//
// Example: ssh -R 127.0.0.1:8888:127.0.0.1:8888 vmr.u24
//
// Listens on remote port via SSH server and forwards connections to local service.
//
// Simplified design:
// - Only handles connection and health checking
// - On health check failure: sets database status to error and cleans up resources
// - No retry/rebuild logic (managed by ForwardService)
type RemoteListenToLocal struct {
	// Remote bind address (address to listen on SSH server)
	remoteBindAddr string
	// Local target
	localAddr string // Full local service address (e.g., "127.0.0.1:11434")
	forwardID string // For database status updates

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
	pooledConn       *connection.PooledConnection // Active pooled connection for this forward
	activeConns      map[net.Conn]struct{}        // Track active connections for clean shutdown
	connMu           sync.RWMutex                 // Protects activeConns
	passphraseSocket interface{}                  // For SSH key passphrase retrieval

	// UDS support - when set, dial UDS instead of TCP
	udsPath string

	// Health monitoring
	healthCheckInterval time.Duration
	healthCheckTicker   *time.Ticker
	healthCheckDone     chan struct{}
}

// NewRemoteListenToLocal creates a new remote forward
//
// remoteBindAddr: remote address to listen on (e.g., "127.0.0.1:8888")
// localAddr: full local service address (e.g., "127.0.0.1:8888" or "192.168.1.100:8888")
// forwardID: unique identifier for database status updates
// db: database for status updates
// pool: connection pool for SSH connections
// hopChain: SSH hops to traverse
func NewRemoteListenToLocal(remoteBindAddr, localAddr string, forwardID string, db *db.Database, pool *connection.ConnectionManager, hopChain []*tunnel.HopConfig) *RemoteListenToLocal {
	return &RemoteListenToLocal{
		remoteBindAddr:      remoteBindAddr,
		localAddr:           localAddr,
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
func (rf *RemoteListenToLocal) String() string {
	// Parse localAddr to extract host and port
	host, port, _ := util.ParseAddress(rf.localAddr)
	return fmt.Sprintf("RemoteListenToLocal[%s → %s:%d]", rf.remoteBindAddr, host, port)
}

// Type returns RemoteListenToLocal
func (rf *RemoteListenToLocal) Type() string {
	return "remote_listen_to_local"
}

// Status returns the current status
func (rf *RemoteListenToLocal) Status() ForwardStatus {
	rf.statusMu.RLock()
	defer rf.statusMu.RUnlock()
	return rf.status
}

// setStatus updates the status
func (rf *RemoteListenToLocal) setStatus(status ForwardStatus) {
	rf.statusMu.Lock()
	defer rf.statusMu.Unlock()
	rf.status = status
}

// setErrorStatus sets database status to error
func (rf *RemoteListenToLocal) setErrorStatus(errorMsg string) {
	rf.setStatus(StatusError)

	if rf.db != nil && rf.forwardID != "" {
		forwardStatus := &db.ForwardStatus{
			ForwardID:     rf.forwardID,
			Status:        "error",
			LastHeartbeat: time.Now(),
			ErrorMessage:  errorMsg,
		}

		if err := rf.db.CreateOrUpdateStatus(forwardStatus); err != nil {
			zap.L().Error("Failed to update error status in database",
				zap.String("forward_id", rf.forwardID),
				zap.Error(err))
		} else {
			zap.L().Info("Set database status to error",
				zap.String("forward_id", rf.forwardID),
				zap.String("error", errorMsg))
		}
	}
}

// SetUDSPath sets the Unix Domain Socket path for the local connection
// When set, the forward will dial UDS instead of TCP
func (rf *RemoteListenToLocal) SetUDSPath(udsPath string) {
	rf.udsPath = udsPath
}

// Start begins listening on remote via SSH and forwarding to local
// This method blocks until the forward is stopped or an error occurs.
// On error, sets database status to error and cleans up resources.
func (rf *RemoteListenToLocal) Start(ctx context.Context) error {
	rf.setStatus(StatusStopped)

	// Create cancellable context
	innerCtx, cancel := context.WithCancel(ctx)
	rf.ctx = innerCtx
	rf.cancelFunc = cancel
	defer func() {
		if rf.Status() != StatusRunning {
			// Failed to start, set database status to error
			rf.setErrorStatus("failed to start")
		}
	}()

	// Validate connection pool
	if rf.pool == nil {
		zap.L().Error("Connection pool is nil",
			zap.String("forward", rf.String()))
		return fmt.Errorf("connection pool is nil")
	}

	// Build connection signature for pooled connection
	sig := rf.buildSignature()

	// Acquire pooled connection
	pooledConn, err := rf.pool.Acquire(innerCtx, sig, rf.forwardID)
	if err != nil {
		zap.L().Error("Failed to acquire connection from pool",
			zap.String("forward", rf.String()),
			zap.Error(err))
		return fmt.Errorf("failed to acquire connection: %w", err)
	}

	// Request remote forwarding via SSH TCP/IP forward
	// This creates a listener on the remote SSH server
	listener, err := pooledConn.Client.Listen("tcp", rf.remoteBindAddr)
	if err != nil {
		// Release connection if listener creation fails
		_ = rf.pool.Release(pooledConn, rf.forwardID)
		zap.L().Error("Failed to listen on remote",
			zap.String("forward", rf.String()),
			zap.String("remote_bind", rf.remoteBindAddr),
			zap.Error(err))
		return fmt.Errorf("failed to create remote listener: %w", err)
	}

	// Success! Store resources and start forward
	rf.pooledConn = pooledConn
	rf.listener = listener

	// CRITICAL: Setup deferred cleanup for partially initialized resources
	// If anything fails after this point (before goroutines start), ensure cleanup
	// Use cleanupOnce to avoid double-cleanup if Stop() is also called
	defer func() {
		if rf.Status() != StatusRunning {
			// Trigger unified cleanup if we haven't reached Running status
			// cleanupOnce ensures cleanup only runs once, even if Stop() was also called
			rf.cleanupOnce.Do(func() {
				zap.L().Warn("Cleaning up partially initialized remote forward",
					zap.String("forward", rf.String()))

				// Stop health monitoring if it was started
				if rf.healthCheckDone != nil {
					close(rf.healthCheckDone)
				}
				if rf.healthCheckTicker != nil {
					rf.healthCheckTicker.Stop()
				}

				// Release pooled connection back to pool
				if rf.pooledConn != nil {
					zap.L().Info("Releasing pooled connection",
						zap.String("forward", rf.String()))
					if err := rf.pool.Release(rf.pooledConn, rf.forwardID); err != nil {
						zap.L().Error("Failed to release pooled connection",
							zap.String("forward", rf.String()),
							zap.Error(err))
					}
					rf.pooledConn = nil
				}

				// Close listener
				if rf.listener != nil {
					_ = rf.listener.Close()
				}

				zap.L().Info("Partial cleanup complete",
					zap.String("forward", rf.String()))
			})
		}
	}()

	zap.L().Info("Remote forward listening",
		zap.String("forward", rf.String()),
		zap.String("remote_bind", rf.remoteBindAddr))

	rf.setStatus(StatusRunning)

	// Start health monitoring
	rf.startHealthMonitoring(innerCtx)

	// Start accept loop
	rf.wg.Add(1) // Add acceptLoop to WaitGroup
	go rf.acceptLoop(innerCtx)

	// Start unified cleanup goroutine
	// This ensures all resources are cleaned up when context is canceled
	rf.wg.Add(1)
	go rf.cleanupMonitor(innerCtx)

	return nil
}

// acceptLoop accepts connections from remote and forwards them to local
func (rf *RemoteListenToLocal) acceptLoop(ctx context.Context) {
	defer rf.wg.Done()

	// Track if acceptLoop exits due to error (not normal shutdown)
	exitedWithError := false
	defer func() {
		// CRITICAL: If acceptLoop exits unexpectedly, set error status
		// This triggers ForwardService to rebuild the forward
		if exitedWithError && rf.Status() == StatusRunning {
			zap.L().Error("Accept loop exited unexpectedly due to error",
				zap.String("forward", rf.String()))
			rf.setErrorStatus("accept loop failed unexpectedly")
		}
	}()

	for {
		// Check context before blocking accept
		select {
		case <-ctx.Done():
			zap.L().Info("Accept loop stopped by context",
				zap.String("forward", rf.String()))
			return
		default:
		}

		// Direct accept (SSH listener may not support deadline)
		conn, err := rf.listener.Accept()
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
				zap.String("forward", rf.String()),
				zap.Error(err))
			return
		}

		// Handle connection
		rf.wg.Add(1)
		go func() {
			defer rf.wg.Done()
			rf.handleConnection(conn)
		}()
	}
}

// handleConnection forwards a single connection from remote to local
func (rf *RemoteListenToLocal) handleConnection(remoteConn net.Conn) {
	defer func() {
		if err := remoteConn.Close(); err != nil {
			zap.L().Debug("Error closing remote connection", zap.Error(err))
		}
	}()

	zap.L().Debug("Remote connection accepted",
		zap.String("remote_addr", remoteConn.RemoteAddr().String()),
		zap.String("remote_local", remoteConn.LocalAddr().String()))

	// Dial local service
	var localConn net.Conn
	var err error
	localDesc := ""

	if rf.udsPath != "" {
		// Use Unix Domain Socket
		localDesc = fmt.Sprintf("unix://%s", rf.udsPath)
		zap.L().Debug("Dialing local UDS",
			zap.String("uds_path", rf.udsPath))
		localConn, err = net.Dial("unix", rf.udsPath)
		if err != nil {
			zap.L().Error("Failed to dial local UDS",
				zap.String("forward", rf.String()),
				zap.String("uds_path", rf.udsPath),
				zap.String("forward_id", rf.forwardID),
				zap.Error(err))
			return
		}
	} else {
		// Use TCP - use localAddr from database instead of assembling from host+port
		localDesc = rf.localAddr
		zap.L().Debug("Dialing local service",
			zap.String("local_addr", localDesc))
		localConn, err = net.Dial("tcp", localDesc)
		if err != nil {
			zap.L().Error("Failed to dial local service",
				zap.String("forward", rf.String()),
				zap.String("local", localDesc),
				zap.String("forward_id", rf.forwardID),
				zap.Error(err))
			return
		}
	}
	defer func() {
		if err := localConn.Close(); err != nil {
			zap.L().Debug("Error closing local connection", zap.Error(err))
		}
	}()

	// Register connections in active pool for clean shutdown
	rf.connMu.Lock()
	if rf.activeConns == nil {
		rf.activeConns = make(map[net.Conn]struct{})
	}
	rf.activeConns[remoteConn] = struct{}{}
	rf.activeConns[localConn] = struct{}{}
	rf.connMu.Unlock()

	// Unregister connections when done
	defer func() {
		rf.connMu.Lock()
		delete(rf.activeConns, remoteConn)
		delete(rf.activeConns, localConn)
		rf.connMu.Unlock()
	}()

	zap.L().Debug("Local connection established",
		zap.String("local", localDesc),
		zap.String("local_remote", localConn.RemoteAddr().String()))

	// Bidirectional forwarding
	zap.L().Debug("Starting bidirectional forwarding",
		zap.String("remote", remoteConn.RemoteAddr().String()),
		zap.String("local", localDesc))

	// Start forwarding using utility
	if err := bidirectionalCopy(remoteConn, localConn); err != nil {
		if isNormalCloseError(err) {
			// Normal connection close (client disconnect, EOF, etc.) - debug level
			zap.L().Debug("Forwarding stopped",
				zap.String("forward", rf.String()),
				zap.String("error", err.Error()))
		} else {
			// Abnormal error (timeout, network unreachable, etc.) - info level
			zap.L().Info("Forwarding stopped",
				zap.String("forward", rf.String()),
				zap.String("error", err.Error()))
		}
	} else {
		zap.L().Debug("Forwarding completed successfully",
			zap.String("forward", rf.String()))
	}
}

// Stop stops the remote forward by triggering unified cleanup
// The actual resource cleanup is performed by cleanupMonitor goroutine
//
// ⚠️ CRITICAL: StatusStopped means "user manually stopped, DO NOT rebuild"
// - Health check failures should NOT call Stop() - they only call cancel()
// - Stop() sets StatusStopped, then triggers cleanup via cancel()
// - All cleanup logic is unified in cleanupMonitor goroutine
func (rf *RemoteListenToLocal) Stop() error {
	zap.L().Info("Stopping remote forward",
		zap.String("forward", rf.String()))

	rf.setStatus(StatusStopped)

	// Trigger unified cleanup by canceling context
	// cleanupMonitor will handle all resource cleanup
	if rf.cancelFunc != nil {
		rf.cancelFunc()
	}

	// Wait for all goroutines to finish cleanup
	rf.wg.Wait()

	zap.L().Info("Remote forward stopped",
		zap.String("forward", rf.String()))

	return nil
}

// cleanupMonitor waits for context cancellation and performs unified resource cleanup
// This ensures ALL exit paths (Stop, health check failure, etc.) use the SAME cleanup logic
func (rf *RemoteListenToLocal) cleanupMonitor(ctx context.Context) {
	defer rf.wg.Done()

	<-ctx.Done()
	zap.L().Info("Context canceled, starting unified resource cleanup",
		zap.String("forward", rf.String()))

	// Use sync.Once to ensure cleanup only happens once
	// This is critical when both Stop() and health check failure trigger cleanup
	rf.cleanupOnce.Do(func() {
		zap.L().Info("Starting unified resource cleanup",
			zap.String("forward", rf.String()))

		// Step 1: Stop health monitoring
		if rf.healthCheckDone != nil {
			close(rf.healthCheckDone)
		}
		if rf.healthCheckTicker != nil {
			rf.healthCheckTicker.Stop()
		}

		// Step 2: Close listener FIRST to stop accepting new connections
		if rf.listener != nil {
			if err := rf.listener.Close(); err != nil {
				zap.L().Debug("Error closing listener", zap.Error(err))
			}
		}

		// Step 3: Close all active connections
		// This causes bidirectionalCopy to fail immediately with "use of closed network connection"
		rf.connMu.Lock()
		activeConnCount := len(rf.activeConns)
		for conn := range rf.activeConns {
			zap.L().Debug("Closing active connection",
				zap.String("remote_addr", conn.RemoteAddr().String()))
			_ = conn.Close()
		}
		rf.activeConns = make(map[net.Conn]struct{})
		rf.connMu.Unlock()

		if activeConnCount > 0 {
			zap.L().Info("Closed active connections",
				zap.Int("count", activeConnCount))
		}

		// Step 4: Release pooled connection back to pool
		if rf.pooledConn != nil {
			zap.L().Info("Releasing pooled connection back to pool",
				zap.String("forward", rf.String()))
			if err := rf.pool.Release(rf.pooledConn, rf.forwardID); err != nil {
				zap.L().Error("Failed to release pooled connection",
					zap.String("forward", rf.String()),
					zap.Error(err))
			}
			rf.pooledConn = nil
		}

		zap.L().Info("Unified resource cleanup complete",
			zap.String("forward", rf.String()))
	})
}

// HealthCheck checks if the forward is healthy by observing the SSH client state.
// It does not actively send keepalives (ConnectionPool already does that),
// but rather observes whether the connection is still alive.
func (rf *RemoteListenToLocal) HealthCheck() error {
	if rf.Status() != StatusRunning {
		return fmt.Errorf("forward not running (status: %s)", rf.Status())
	}

	// Check if listener is still active
	if rf.listener == nil {
		return fmt.Errorf("listener is nil")
	}

	// Observe pooled connection state
	if rf.pooledConn != nil && rf.pooledConn.Client != nil {
		if err := rf.observeConnectionState(rf.pooledConn.Client); err != nil {
			return fmt.Errorf("connection failed: %w", err)
		}
	}

	return nil
}

// observeConnectionState observes the SSH client's connection state.
// It sends a lightweight keepalive request to detect if the connection is still alive.
func (rf *RemoteListenToLocal) observeConnectionState(client *ssh.Client) error {
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
func (rf *RemoteListenToLocal) startHealthMonitoring(ctx context.Context) {
	rf.wg.Add(1)
	rf.healthCheckTicker = time.NewTicker(rf.healthCheckInterval)

	go func() {
		defer rf.wg.Done()
		defer zap.L().Info("Health monitoring stopped",
			zap.String("forward", rf.String()))

		for {
			select {
			case <-ctx.Done():
				return
			case <-rf.healthCheckDone:
				return
			case <-rf.healthCheckTicker.C:
				if err := rf.HealthCheck(); err != nil {
					zap.L().Warn("Health check failed, setting error status",
						zap.String("forward", rf.String()),
						zap.Error(err))

					// Set database status to error
					rf.setErrorStatus(err.Error())

					// Trigger unified cleanup by canceling context
					// cleanupMonitor will handle all resource cleanup including closing listener
					if rf.cancelFunc != nil {
						rf.cancelFunc()
					}
					return
				}
			}
		}
	}()

	zap.L().Debug("Health monitoring started",
		zap.String("forward", rf.String()),
		zap.String("interval", rf.healthCheckInterval.String()))
}

// Removed: attemptRepair, reconnect, and calculateBackoff methods
// Rebuild logic is now handled by ForwardService

// SetPassphraseSocket sets the passphrase socket for retrieving SSH key passphrases
func (rf *RemoteListenToLocal) SetPassphraseSocket(ps interface{}) {
	rf.passphraseSocket = ps
}

// buildSignature creates a ConnectionSignature from the hop chain
func (rf *RemoteListenToLocal) buildSignature() connection.ConnectionSignature {
	if len(rf.hopChain) == 0 {
		return connection.ConnectionSignature{}
	}

	// Get the final destination hop (last in chain)
	finalHop := rf.hopChain[len(rf.hopChain)-1]

	// Build jump chain from all hops except the last
	jumpChain := make([]string, 0, len(rf.hopChain)-1)
	for i := 0; i < len(rf.hopChain)-1; i++ {
		jumpChain = append(jumpChain, rf.hopChain[i].Host)
	}

	return connection.ConnectionSignature{
		Username:  finalHop.User,
		Hostname:  finalHop.HostName,
		Port:      finalHop.Port,
		JumpChain: jumpChain,
	}
}
