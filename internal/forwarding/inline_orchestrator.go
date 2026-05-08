package forwarding

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/hallelujah-shih/ssh-multihop/internal/connection"
	"github.com/hallelujah-shih/ssh-multihop/internal/tunnel"
	"go.uber.org/zap"
)

// InlineForwardOrchestrator implements inline forwarding by composing LocalForward + RemoteForward
//
// Architecture:
// - LocalForward: Listens on UDS, forwards to service via SSH
// - RemoteForward: Listens on expose address via SSH, forwards to UDS
// - UDS bridge: Connects the two forwards without TCP port conflicts
//
// Example: vmr.u24:11434 → dc4:11434
// - Service runs on vmr.u24:11434 (serviceHost:servicePort)
// - Exposed on dc4:11434 address (exposeHost:exposePort)
// - Accessing dc4:11434 → SSH → UDS → SSH → vmr.u24:11434
//
// This approach leverages the verified self-healing of LocalForward and RemoteForward.
type InlineForwardOrchestrator struct {
	// Configuration
	serviceHost string // Hostname where service actually runs
	servicePort int    // Port where service listens
	exposeHost  string // Hostname where service is exposed
	exposePort  int    // Port where service is exposed

	// Composed forwards
	localFwd  *LocalListenToRemote // UDS → service
	remoteFwd *RemoteListenToLocal // expose address → UDS

	// Connection pool for composed forwards
	pool *connection.ConnectionManager

	// UDS bridge
	udsPath string // Path to Unix Domain Socket

	// Internal state
	status     ForwardStatus
	statusMu   sync.RWMutex
	cancelFunc context.CancelFunc
	wg         sync.WaitGroup

	// Original hop chains for rebuilding
	serviceHop []*tunnel.HopConfig // SSH hops to reach service
	exposeHop  []*tunnel.HopConfig // SSH hops to reach expose address
}

// NewInlineForwardOrchestrator creates a new inline forward orchestrator
//
// serviceHost: hostname where service runs
// servicePort: port number where service listens
// serviceHop: SSH hops to reach service
// exposeHost: hostname where service is exposed
// exposePort: port number where service is exposed
// exposeHop: SSH hops to reach expose address
func NewInlineForwardOrchestrator(serviceHost string, servicePort int, serviceHop []*tunnel.HopConfig,
	exposeHost string, exposePort int, exposeHop []*tunnel.HopConfig) *InlineForwardOrchestrator {

	// Generate unique UDS path
	udsPath := filepath.Join(os.TempDir(), fmt.Sprintf("inline-forward-%s-%d-%s-%d.sock",
		serviceHost, servicePort, exposeHost, exposePort))

	// Create connection pool for composed forwards
	// Note: Inline orchestrator doesn't use database or HopConfigProvider,
	// so the pool won't be able to create new connections. This is OK for
	// testing scenarios where connections are manually managed.
	pool := connection.NewConnectionManager(connection.DefaultConfig(), nil)

	return &InlineForwardOrchestrator{
		serviceHost: serviceHost,
		servicePort: servicePort,
		exposeHost:  exposeHost,
		exposePort:  exposePort,
		serviceHop:  serviceHop,
		exposeHop:   exposeHop,
		udsPath:     udsPath,
		pool:        pool,
		status:      StatusStopped,
	}
}

// String returns a string representation
func (ifo *InlineForwardOrchestrator) String() string {
	return fmt.Sprintf("InlineForwardOrchestrator[%s:%d → %s:%d]",
		ifo.serviceHost, ifo.servicePort, ifo.exposeHost, ifo.exposePort)
}

// Type returns InlineForward
func (ifo *InlineForwardOrchestrator) Type() string {
	return "inline"
}

// Status returns the current status
func (ifo *InlineForwardOrchestrator) Status() ForwardStatus {
	ifo.statusMu.RLock()
	defer ifo.statusMu.RUnlock()
	return ifo.status
}

// setStatus updates the status
func (ifo *InlineForwardOrchestrator) setStatus(status ForwardStatus) {
	ifo.statusMu.Lock()
	defer ifo.statusMu.Unlock()
	ifo.status = status
}

// Start begins the inline forward using composition
func (ifo *InlineForwardOrchestrator) Start(ctx context.Context) error {
	ifo.setStatus(StatusStopped)

	// Ensure UDS file doesn't exist
	if _, err := os.Stat(ifo.udsPath); err == nil {
		zap.L().Info("Removing existing UDS file",
			zap.String("uds_path", ifo.udsPath))
		if err := os.Remove(ifo.udsPath); err != nil {
			ifo.setStatus(StatusStopped)
			return fmt.Errorf("failed to remove existing UDS file: %w", err)
		}
	}

	// Create cancellable context
	innerCtx, cancel := context.WithCancel(ctx)
	ifo.cancelFunc = cancel

	// Create LocalForward: UDS → service
	// Listens on UDS, forwards traffic to service via SSH
	udsAddr := fmt.Sprintf("unix://%s", ifo.udsPath)
	// Note: Use localhost/127.0.0.1 instead of serviceHost because
	// the SSH client will dial from the last hop in the chain, and
	// the service is listening on localhost at that hop
	serviceAddr := fmt.Sprintf("127.0.0.1:%d", ifo.servicePort)
	ifo.localFwd = NewLocalListenToRemote(
		udsAddr,
		serviceAddr, // Use full address string
		"",          // No forwardID for inline orchestrator
		nil,         // No database for inline orchestrator
		ifo.pool,    // Connection pool
		ifo.serviceHop,
	)

	zap.L().Info("Starting LocalForward (UDS → service)",
		zap.String("uds", udsAddr),
		zap.String("service", fmt.Sprintf("%s:%d (via %s)", "127.0.0.1", ifo.servicePort, ifo.serviceHost)))

	if err := ifo.localFwd.Start(innerCtx); err != nil {
		cancel()
		ifo.setStatus(StatusStopped)
		return fmt.Errorf("failed to start LocalForward: %w", err)
	}

	// Create RemoteForward: expose address → UDS
	// Listens on expose via SSH, forwards traffic to UDS
	exposeListenAddr := fmt.Sprintf("127.0.0.1:%d", ifo.exposePort)
	ifo.remoteFwd = NewRemoteListenToLocal(
		exposeListenAddr,
		"localhost", // Localhost for UDS (will be overridden by SetUDSPath)
		"",          // No forwardID for inline orchestrator
		nil,         // No database for inline orchestrator
		ifo.pool,    // Connection pool
		ifo.exposeHop,
	)

	// Set UDS path for remote forward to use
	ifo.remoteFwd.SetUDSPath(ifo.udsPath)

	zap.L().Info("Starting RemoteForward (expose → UDS)",
		zap.String("expose_listen", exposeListenAddr),
		zap.String("uds", ifo.udsPath))

	if err := ifo.remoteFwd.Start(innerCtx); err != nil {
		_ = ifo.localFwd.Stop()
		cancel()
		ifo.setStatus(StatusStopped)
		return fmt.Errorf("failed to start RemoteForward: %w", err)
	}

	ifo.setStatus(StatusRunning)

	zap.L().Info("InlineForwardOrchestrator started",
		zap.String("forward", ifo.String()),
		zap.String("uds", ifo.udsPath))

	return nil
}

// Stop stops the inline forward
func (ifo *InlineForwardOrchestrator) Stop() error {
	zap.L().Info("Stopping InlineForwardOrchestrator",
		zap.String("forward", ifo.String()))

	ifo.setStatus(StatusStopped)

	// Stop composed forwards
	if ifo.remoteFwd != nil {
		if err := ifo.remoteFwd.Stop(); err != nil {
			zap.L().Warn("Failed to stop RemoteForward",
				zap.String("forward", ifo.String()),
				zap.Error(err))
		}
	}

	if ifo.localFwd != nil {
		if err := ifo.localFwd.Stop(); err != nil {
			zap.L().Warn("Failed to stop LocalForward",
				zap.String("forward", ifo.String()),
				zap.Error(err))
		}
	}

	// Cancel context
	if ifo.cancelFunc != nil {
		ifo.cancelFunc()
	}

	// Clean up UDS file
	if _, err := os.Stat(ifo.udsPath); err == nil {
		if err := os.Remove(ifo.udsPath); err != nil {
			zap.L().Warn("Failed to remove UDS file",
				zap.String("uds_path", ifo.udsPath),
				zap.Error(err))
		}
	}

	// Wait for all goroutines
	ifo.wg.Wait()

	// Close connection pool
	if ifo.pool != nil {
		_ = ifo.pool.Close()
	}

	return nil
}

// HealthCheck checks if both composed forwards are healthy
func (ifo *InlineForwardOrchestrator) HealthCheck() error {
	if ifo.Status() != StatusRunning {
		return fmt.Errorf("forward not running (status: %s)", ifo.Status())
	}

	// Check if forwards are initialized
	if ifo.localFwd == nil {
		return fmt.Errorf("LocalForward is nil")
	}
	if ifo.remoteFwd == nil {
		return fmt.Errorf("RemoteForward is nil")
	}

	// Check LocalForward health
	if err := ifo.localFwd.HealthCheck(); err != nil {
		return fmt.Errorf("LocalForward health check failed: %w", err)
	}

	// Check RemoteForward health
	if err := ifo.remoteFwd.HealthCheck(); err != nil {
		return fmt.Errorf("RemoteForward health check failed: %w", err)
	}

	// Check UDS bridge
	if _, err := os.Stat(ifo.udsPath); err != nil {
		return fmt.Errorf("UDS bridge not available: %w", err)
	}

	return nil
}

// GetUDSPath returns the UDS path for debugging/monitoring
func (ifo *InlineForwardOrchestrator) GetUDSPath() string {
	return ifo.udsPath
}
