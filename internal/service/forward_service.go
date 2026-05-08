package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/hallelujah-shih/ssh-multihop/internal/config"
	"github.com/hallelujah-shih/ssh-multihop/internal/connection"
	"github.com/hallelujah-shih/ssh-multihop/internal/db"
	"github.com/hallelujah-shih/ssh-multihop/internal/forwarding"
	"github.com/hallelujah-shih/ssh-multihop/internal/tunnel"
	"github.com/hallelujah-shih/ssh-multihop/internal/util"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ForwardService manages port forwards
type ForwardService struct {
	db *db.Database

	// Connection pool for SSH connection reuse
	pool *connection.ConnectionManager

	// ctx is the root context for all operations
	// When this context is cancelled, all forwards should stop
	ctx    context.Context
	cancel context.CancelFunc

	// Active forwards
	forwards map[string]ForwardWrapper
	mu       sync.RWMutex

	// Sync loop control
	syncCancel   context.CancelFunc
	syncDone     chan struct{} // syncLoop completion signal
	syncInterval time.Duration

	// Rebuild backoff control (exponential backoff with max 120s)
	consecutiveFailures map[string]int       // forwardID -> consecutive failure count
	lastRebuildTime     map[string]time.Time // forwardID -> last rebuild time
	baseBackoff         time.Duration        // base backoff (1 second)
	maxBackoff          time.Duration        // max backoff (120 seconds)

	// Pending starts tracking (prevents duplicate starts during slow SSH handshake)
	pendingStarts map[string]bool // Track forward IDs currently being started
	pendingMu     sync.Mutex      // Protect pendingStarts map

	// Passphrase socket for SSH key passphrase retrieval
	passphraseSocket interface{} // *connection.PassphraseSocket

	// SSH config caching to avoid repeated file I/O and parsing
	sshConfigCache map[string]*config.SSHConfig // configPath -> parsed SSH config
	cacheMu        sync.RWMutex                 // Protect sshConfigCache
}

// ForwardWrapper wraps different forward implementations
type ForwardWrapper struct {
	Type                 db.ForwardType
	LocalListenToRemote  *forwarding.LocalListenToRemote
	RemoteListenToLocal  *forwarding.RemoteListenToLocal
	RemoteListenToRemote *forwarding.RemoteListenToRemote
	ctx                  context.Context
	cancel               context.CancelFunc
}

// New creates a new ForwardService
//
// Deprecated: Use NewWithContext() instead for better context control.
// This method creates an independent context that cannot be controlled externally.
func New(database *db.Database) (*ForwardService, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Create service first (incomplete)
	s := &ForwardService{
		db:                  database,
		ctx:                 ctx,
		cancel:              cancel,
		forwards:            make(map[string]ForwardWrapper),
		syncInterval:        10 * time.Second, // default 10s sync interval
		consecutiveFailures: make(map[string]int),
		lastRebuildTime:     make(map[string]time.Time),
		baseBackoff:         1 * time.Second,   // base backoff 1s
		maxBackoff:          120 * time.Second, // max backoff 120s
		pendingStarts:       make(map[string]bool),
		sshConfigCache:      make(map[string]*config.SSHConfig),
	}

	// Create connection manager with hop config provider (closure captures s)
	pool := connection.NewConnectionManager(
		connection.DefaultConfig(),
		s.newHopConfigProvider(),
	)
	s.pool = pool

	return s, nil
}

// NewWithContext creates a new ForwardService with external context control.
// The provided context will be the root context for all operations.
func NewWithContext(ctx context.Context, database *db.Database) (*ForwardService, error) {
	childCtx, cancel := context.WithCancel(ctx)

	// Create service first (incomplete)
	s := &ForwardService{
		db:                  database,
		ctx:                 childCtx,
		cancel:              cancel,
		forwards:            make(map[string]ForwardWrapper),
		syncInterval:        10 * time.Second, // default 10s sync interval
		consecutiveFailures: make(map[string]int),
		lastRebuildTime:     make(map[string]time.Time),
		baseBackoff:         1 * time.Second,   // base backoff 1s
		maxBackoff:          120 * time.Second, // max backoff 120s
		pendingStarts:       make(map[string]bool),
		sshConfigCache:      make(map[string]*config.SSHConfig),
	}

	// Create connection manager with hop config provider (closure captures s)
	pool := connection.NewConnectionManager(
		connection.DefaultConfig(),
		s.newHopConfigProvider(),
	)
	s.pool = pool

	return s, nil
}

// Start starts all forwards from database
func (s *ForwardService) Start() error {
	zap.L().Info("Starting ForwardService")

	// Load forwards from database
	forwards, err := s.db.ListForwards()
	if err != nil {
		return fmt.Errorf("failed to load forwards: %w", err)
	}

	zap.L().Info("Loaded forwards from database", zap.Int("count", len(forwards)))

	// Clean up old status records
	if err := s.db.CleanStatuses(); err != nil {
		zap.L().Warn("Failed to clean old status records", zap.Error(err))
	}

	// Start each forward
	for _, fwd := range forwards {
		if err := s.startForward(&fwd); err != nil {
			zap.L().Error("Failed to start forward",
				zap.String("id", fwd.ID),
				zap.Error(err))
			// Update status to error
			s.updateStatus(fwd.ID, "error", err.Error())
		}
	}

	// Start sync loop goroutine
	syncCtx, syncCancel := context.WithCancel(s.ctx)
	s.syncCancel = syncCancel
	s.syncDone = make(chan struct{})
	go s.syncLoop(syncCtx)

	zap.L().Info("ForwardService started",
		zap.Duration("sync_interval", s.syncInterval),
		zap.Int("active_forwards", len(s.forwards)))

	return nil
}

// Stop stops all forwards
func (s *ForwardService) Stop() error {
	zap.L().Info("Stopping ForwardService")

	// Stop sync loop
	if s.syncCancel != nil {
		zap.L().Info("Stopping sync loop")
		s.syncCancel()
	}

	// Wait for syncLoop to exit
	if s.syncDone != nil {
		select {
		case <-s.syncDone:
			zap.L().Info("Sync loop exited gracefully")
		case <-time.After(2 * time.Second):
			zap.L().Warn("Sync loop did not exit gracefully after 2s")
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Stop all forwards
	for id, wrapper := range s.forwards {
		zap.L().Info("Stopping forward", zap.String("id", id))
		s.stopWrapper(wrapper)
		delete(s.forwards, id)
	}

	// Close connection pool
	if s.pool != nil {
		if err := s.pool.Close(); err != nil {
			zap.L().Warn("Error closing connection pool", zap.Error(err))
		}
	}

	// Cancel context
	s.cancel()

	return nil
}

// StopWithContext stops all forwards with timeout control
func (s *ForwardService) StopWithContext(ctx context.Context) error {
	zap.L().Info("Stopping ForwardService with timeout")

	// Stop sync loop
	if s.syncCancel != nil {
		zap.L().Info("Stopping sync loop")
		s.syncCancel()
	}

	// Wait for syncLoop to exit or timeout
	if s.syncDone != nil {
		select {
		case <-s.syncDone:
			zap.L().Info("Sync loop exited gracefully")
		case <-ctx.Done():
			return fmt.Errorf("sync loop shutdown timeout: %w", ctx.Err())
		case <-time.After(2 * time.Second):
			zap.L().Warn("Sync loop did not exit gracefully after 2s")
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Stop all forwards concurrently with timeout
	type stopResult struct {
		id  string
		err error
	}

	resultCh := make(chan stopResult, len(s.forwards))
	var wg sync.WaitGroup

	for id, wrapper := range s.forwards {
		wg.Add(1)
		go func(forwardID string, w ForwardWrapper) {
			defer wg.Done()
			zap.L().Info("Stopping forward", zap.String("id", forwardID))
			if err := s.stopWrapperWithContext(w, ctx); err != nil {
				resultCh <- stopResult{id: forwardID, err: err}
			}
		}(id, wrapper)
	}

	// Wait for all stops to complete or timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		close(resultCh)
		zap.L().Info("All forwards stopped")
	case <-ctx.Done():
		// Context cancelled - some forwards may still be running
		zap.L().Warn("Stop cancelled due to timeout", zap.Error(ctx.Err()))
		return fmt.Errorf("stop timeout: %w", ctx.Err())
	}

	// Collect errors (non-blocking since we closed resultCh)
	var errs []error
	for result := range resultCh {
		errs = append(errs, fmt.Errorf("forward %s: %w", result.id, result.err))
	}

	// Clear forwards map
	s.forwards = make(map[string]ForwardWrapper)

	// Close connection pool
	if s.pool != nil {
		if err := s.pool.Close(); err != nil {
			zap.L().Warn("Error closing connection pool", zap.Error(err))
		}
	}

	// Cancel context
	s.cancel()

	if len(errs) > 0 {
		return fmt.Errorf("stopped with %d errors: %w", len(errs), errors.Join(errs...))
	}

	return nil
}

// syncLoop runs the periodic sync loop
func (s *ForwardService) syncLoop(ctx context.Context) {
	ticker := time.NewTicker(s.syncInterval)
	defer ticker.Stop()
	defer close(s.syncDone) // Signal completion when exiting

	zap.L().Info("Sync loop started", zap.Duration("interval", s.syncInterval))

	for {
		select {
		case <-ctx.Done():
			zap.L().Warn("Sync loop context cancelled - exiting", zap.Error(ctx.Err()))
			return
		case <-ticker.C:
			s.sync()
		}
	}
}

// sync synchronizes database and in-memory state
func (s *ForwardService) sync() {
	startTime := time.Now()

	// Get all forwards with statuses from database (single LEFT JOIN query)
	dbForwardsWithStatus, err := s.db.ListForwardsWithStatus()
	if err != nil {
		zap.L().Error("Sync: Failed to list forwards from database", zap.Error(err))
		return
	}

	// Build database ID set and convert to Forward slice
	dbIDs := make(map[string]bool)
	dbForwards := make([]db.Forward, len(dbForwardsWithStatus))
	for i, fwdWithStatus := range dbForwardsWithStatus {
		dbForwards[i] = fwdWithStatus.ToForward()
		dbIDs[fwdWithStatus.ForwardID] = true
	}

	// Find forwards to start (in DB, not in memory)
	for _, fwdWithStatus := range dbForwardsWithStatus {
		fwd := fwdWithStatus.ToForward()

		s.mu.RLock()
		_, exists := s.forwards[fwd.ID]
		s.mu.RUnlock()

		if !exists {
			// Check if this forward is already being started (prevents duplicate starts)
			s.pendingMu.Lock()
			if s.pendingStarts[fwd.ID] {
				s.pendingMu.Unlock()
				zap.L().Debug("Forward already starting, skipping",
					zap.String("id", fwd.ID))
				continue
			}
			s.pendingStarts[fwd.ID] = true
			s.pendingMu.Unlock()

			// Check if this forward has an error status from previous attempt
			shouldStart := true

			if fwdWithStatus.Status != nil && *fwdWithStatus.Status == "error" {
				// Forward has error status - apply exponential backoff
				shouldRebuild, nextRebuildIn := s.shouldRebuild(fwd.ID)

				if !shouldRebuild {
					zap.L().Debug("Skipping start due to exponential backoff",
						zap.String("id", fwd.ID),
						zap.Duration("next_rebuild_in", nextRebuildIn),
						zap.Int("consecutive_failures", s.consecutiveFailures[fwd.ID]))
					shouldStart = false

					// Release pending lock since we're not starting
					s.pendingMu.Lock()
					delete(s.pendingStarts, fwd.ID)
					s.pendingMu.Unlock()
				} else {
					zap.L().Info("Starting error forward after backoff",
						zap.String("id", fwd.ID),
						zap.Int("consecutive_failures", s.consecutiveFailures[fwd.ID]))
				}
			}

			if !shouldStart {
				continue
			}

			zap.L().Info("Starting new forward from database",
				zap.String("id", fwd.ID),
				zap.String("type", string(fwd.Type)))

			// Start in goroutine (don't block sync loop)
			go func(f db.Forward) {
				defer func() {
					// Release pending lock when start completes (success or failure)
					s.pendingMu.Lock()
					delete(s.pendingStarts, f.ID)
					s.pendingMu.Unlock()
				}()

				if err := s.startForward(&f); err != nil {
					zap.L().Error("Failed to start forward",
						zap.String("id", f.ID),
						zap.Error(err))
					s.updateStatus(f.ID, "error", err.Error())
					// Record failure - will trigger exponential backoff for next attempt
					s.recordRebuildFailure(f.ID)
				} else {
					// Start successful - reset failure counter
					s.recordRebuildSuccess(f.ID)
				}
			}(fwd)
		}
	}

	// Find forwards to stop (in memory, not in DB)
	s.mu.Lock()
	for id, wrapper := range s.forwards {
		if !dbIDs[id] {
			zap.L().Info("Stopping deleted forward",
				zap.String("id", id))

			delete(s.forwards, id)

			// Stop asynchronously (don't block sync loop)
			go func(w ForwardWrapper, forwardID string) {
				s.stopWrapper(w)
				zap.L().Info("Forward stopped and cleaned up",
					zap.String("id", forwardID))
			}(wrapper, id)
		}
	}
	s.mu.Unlock()

	// Find forwards to rebuild (status is StatusError)
	// NOTE: Collect forwards to rebuild, release lock before calling shouldRebuild() to avoid deadlock
	var errorForwards []string
	var errorForwardWrappers []ForwardWrapper

	s.mu.RLock()
	for id, wrapper := range s.forwards {
		var status forwarding.ForwardStatus
		switch wrapper.Type {
		case db.LocalListenToRemote:
			if wrapper.LocalListenToRemote != nil {
				status = wrapper.LocalListenToRemote.Status()
			}
		case db.RemoteListenToLocal:
			if wrapper.RemoteListenToLocal != nil {
				status = wrapper.RemoteListenToLocal.Status()
			}
		case db.RemoteListenToRemote:
			if wrapper.RemoteListenToRemote != nil {
				status = wrapper.RemoteListenToRemote.Status()
			}
		}

		if status == forwarding.StatusError {
			// Collect forwards to rebuild, process later
			errorForwards = append(errorForwards, id)
			errorForwardWrappers = append(errorForwardWrappers, wrapper)
		}
	}
	s.mu.RUnlock()

	// Now safe to call shouldRebuild() (no lock held)
	for i := range errorForwards {
		id := errorForwards[i]
		wrapper := errorForwardWrappers[i]

		zap.L().Info("Detected StatusError, checking rebuild",
			zap.String("id", id))

		// Check if we should rebuild based on exponential backoff
		shouldRebuild, nextRebuildIn := s.shouldRebuild(id)

		if !shouldRebuild {
			zap.L().Debug("Skipping rebuild due to exponential backoff",
				zap.String("id", id),
				zap.Duration("next_rebuild_in", nextRebuildIn),
				zap.Int("consecutive_failures", s.consecutiveFailures[id]))
			continue
		}

		zap.L().Info("Detected StatusError, triggering rebuild",
			zap.String("id", id),
			zap.Int("consecutive_failures", s.consecutiveFailures[id]))

		// Rebuild synchronously
		s.rebuildErrorForward(id, wrapper)
	}

	zap.L().Debug("Sync: Completed sync",
		zap.Duration("duration", time.Since(startTime)),
		zap.Int("total_db_forwards", len(dbForwards)),
		zap.Int("active_forwards", len(s.forwards)))
}

// CreateForward creates a new forward (DB only, sync loop will start it)
func (s *ForwardService) CreateForward(fwd *db.Forward) error {
	// Validate addresses
	_, _, err := util.ParseAddress(fwd.ListenAddr)
	if err != nil {
		return fmt.Errorf("invalid listen_addr: %w", err)
	}

	_, _, err = util.ParseAddress(fwd.ServiceAddr)
	if err != nil {
		return fmt.Errorf("invalid service_addr: %w", err)
	}

	// Validate
	if fwd.Type == db.RemoteListenToRemote && fwd.MaxConns != 0 {
		// InlineForwardOrchestrator doesn't support maxConns
		return fmt.Errorf("inline forward does not support maxConns parameter")
	}

	// Use transaction to create both Forward and Status atomically
	err = s.db.Transaction(func(tx *gorm.DB) error {
		// Create forward record
		if err := tx.Create(fwd).Error; err != nil {
			return fmt.Errorf("failed to create forward: %w", err)
		}

		// Create initial status record
		forwardStatus := &db.ForwardStatus{
			ForwardID:     fwd.ID,
			Status:        "pending",
			LastHeartbeat: time.Now(),
		}
		if err := tx.Create(forwardStatus).Error; err != nil {
			return fmt.Errorf("failed to create forward status: %w", err)
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to create forward: %w", err)
	}

	zap.L().Info("Forward and status created in database",
		zap.String("id", fwd.ID),
		zap.String("type", string(fwd.Type)),
		zap.String("listen_host", fwd.ListenHost),
		zap.String("listen_addr", fwd.ListenAddr),
		zap.String("service_host", fwd.ServiceHost),
		zap.String("service_addr", fwd.ServiceAddr))

	// Sync loop will detect and start within 5 seconds
	return nil
}

// DeleteForward deletes a forward (DB only, sync loop will stop it)
func (s *ForwardService) DeleteForward(id string) error {
	// Delete from database (快速操作，~5ms)
	if err := s.db.DeleteForward(id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrForwardNotFound
		}
		return fmt.Errorf("failed to delete forward: %w", err)
	}

	// Delete status
	if err := s.db.DeleteStatus(id); err != nil {
		zap.L().Warn("Failed to delete status", zap.String("id", id), zap.Error(err))
	}

	zap.L().Info("Forward deleted from database", zap.String("id", id))

	// Sync loop will detect and stop within 5 seconds
	return nil
}

// GetForward retrieves a forward
func (s *ForwardService) GetForward(id string) (*db.Forward, error) {
	forward, err := s.db.GetForward(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrForwardNotFound
		}
		return nil, err
	}
	return forward, nil
}

// ListForwards lists all forwards
func (s *ForwardService) ListForwards() ([]db.Forward, error) {
	return s.db.ListForwards()
}

// GetStatus retrieves the status of a forward
func (s *ForwardService) GetStatus(id string) (*db.ForwardStatus, error) {
	status, err := s.db.GetStatus(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrStatusNotFound
		}
		return nil, err
	}
	return status, nil
}

// ListStatuses lists all forward statuses
func (s *ForwardService) ListStatuses() ([]db.ForwardStatus, error) {
	return s.db.ListStatuses()
}

// GetPoolStats returns current connection pool statistics
func (s *ForwardService) GetPoolStats() (map[string]interface{}, error) {
	if s.pool == nil {
		return map[string]interface{}{
			"error": "connection pool not initialized",
		}, nil
	}

	stats := s.pool.Stats()

	return map[string]interface{}{
		"total_connections":  stats.TotalConnections,
		"active_connections": stats.ActiveConnections,
		"idle_connections":   stats.IdleConnections,
		"closed_connections": stats.ClosedConnections,
	}, nil
}

// SetPassphraseSocket sets the passphrase socket for retrieving SSH key passphrases
func (s *ForwardService) SetPassphraseSocket(ps interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.passphraseSocket = ps
}

// startForward starts a single forward
func (s *ForwardService) startForward(fwd *db.Forward) error {
	zap.L().Debug("Starting forward",
		zap.String("type", string(fwd.Type)),
		zap.String("listen_host", fwd.ListenHost),
		zap.String("listen_addr", fwd.ListenAddr),
		zap.String("service_host", fwd.ServiceHost),
		zap.String("service_addr", fwd.ServiceAddr))

	// Parse listen address
	listenIP, listenPort, err := util.ParseAddress(fwd.ListenAddr)
	if err != nil {
		return fmt.Errorf("invalid listen_addr: %w", err)
	}

	// Parse service address (servicePort used by RemoteListenToRemote)
	_, servicePort, err := util.ParseAddress(fwd.ServiceAddr)
	if err != nil {
		return fmt.Errorf("invalid service_addr: %w", err)
	}

	// Resolve listen_host to get SSH hops
	listenHops, err := s.getHopsForHost(fwd.ListenHost)
	if err != nil {
		return fmt.Errorf("failed to resolve listen_host: %w", err)
	}

	// For local_listen_to_remote and remote_listen_to_remote, also need service_host hops
	var serviceHops []*tunnel.HopConfig
	if fwd.Type == db.LocalListenToRemote || fwd.Type == db.RemoteListenToRemote {
		serviceHops, err = s.getHopsForHost(fwd.ServiceHost)
		if err != nil {
			return fmt.Errorf("failed to resolve service_host: %w", err)
		}
	}

	// Build bind address string
	bindAddr := fmt.Sprintf("%s:%d", listenIP, listenPort)

	var wrapper ForwardWrapper
	var forwardImpl forwarding.Forward

	switch fwd.Type {
	case db.LocalListenToRemote:
		// For SSH -L forwarding, use the full service address from database
		localFwd := forwarding.NewLocalListenToRemote(
			bindAddr,        // Full bind address (e.g., "127.0.0.1:8888")
			fwd.ServiceAddr, // Full service address (e.g., "127.0.0.1:8888" or "192.168.1.100:8888")
			fwd.ID,
			s.db,
			s.pool, // Connection pool for SSH connection reuse
			serviceHops,
		)
		// Set passphrase socket if available
		if s.passphraseSocket != nil {
			localFwd.SetPassphraseSocket(s.passphraseSocket)
		}
		forwardImpl = localFwd
		wrapper = ForwardWrapper{
			Type:                db.LocalListenToRemote,
			LocalListenToRemote: localFwd,
		}

	case db.RemoteListenToLocal:
		remoteFwd := forwarding.NewRemoteListenToLocal(
			bindAddr,        // Full bind address (e.g., "127.0.0.1:8888")
			fwd.ServiceAddr, // Full service address (e.g., "127.0.0.1:8888" or "192.168.1.100:8888")
			fwd.ID,
			s.db,
			s.pool, // Connection pool for SSH connection reuse
			listenHops,
		)
		// Set passphrase socket if available
		if s.passphraseSocket != nil {
			remoteFwd.SetPassphraseSocket(s.passphraseSocket)
		}
		forwardImpl = remoteFwd
		wrapper = ForwardWrapper{
			Type:                db.RemoteListenToLocal,
			RemoteListenToLocal: remoteFwd,
		}

	case db.RemoteListenToRemote:
		relayFwd := forwarding.NewRemoteListenToRemote(
			fwd.ListenAddr, // Full listen address (e.g., "127.0.0.1:11434")
			fwd.ListenHost, // SSH hostname (e.g., "vmr.u24")
			listenPort,     // Port number
			listenHops,
			fwd.ServiceAddr, // Full service address (e.g., "127.0.0.1:11434")
			fwd.ServiceHost, // SSH hostname (e.g., "dc4")
			servicePort,     // Port number
			serviceHops,
			fwd.ID,
			s.db,
			s.pool, // Connection pool for SSH connection reuse
			fwd.MaxConns,
		)
		// Set passphrase socket if available
		if s.passphraseSocket != nil {
			relayFwd.SetPassphraseSocket(s.passphraseSocket)
		}
		forwardImpl = relayFwd
		wrapper = ForwardWrapper{
			Type:                 db.RemoteListenToRemote,
			RemoteListenToRemote: relayFwd,
		}

	default:
		return fmt.Errorf("unknown forward type: %s", fwd.Type)
	}

	// Create context for this forward
	ctx, cancel := context.WithCancel(s.ctx)
	wrapper.ctx = ctx
	wrapper.cancel = cancel

	// Start forward
	if err := forwardImpl.Start(ctx); err != nil {
		zap.L().Error("Failed to start forward, cleaning up resources",
			zap.String("id", fwd.ID),
			zap.String("type", string(fwd.Type)),
			zap.Error(err))

		// CRITICAL: Must call Stop() to cleanup resources properly
		// cancel() alone is not enough - it doesn't wait for goroutines to exit
		// Stop() will:
		// 1. Cancel context to trigger cleanup
		// 2. Wait for all goroutines (acceptLoop, healthCheck, cleanupMonitor) to exit
		// 3. Ensure all resources are released
		if stopErr := forwardImpl.Stop(); stopErr != nil {
			zap.L().Error("Error cleaning up failed forward",
				zap.String("id", fwd.ID),
				zap.Error(stopErr))
		}

		return fmt.Errorf("failed to start forward: %w", err)
	}

	// Store in map
	s.mu.Lock()
	s.forwards[fwd.ID] = wrapper
	s.mu.Unlock()

	// Update status
	s.updateStatus(fwd.ID, "running", "")

	zap.L().Info("Forward started",
		zap.String("id", fwd.ID),
		zap.String("type", string(fwd.Type)))

	return nil
}

// stopWrapper stops a forward wrapper
func (s *ForwardService) stopWrapper(wrapper ForwardWrapper) {
	// Cancel context
	if wrapper.cancel != nil {
		wrapper.cancel()
	}

	// Stop forward
	var err error
	switch wrapper.Type {
	case db.RemoteListenToRemote:
		if wrapper.RemoteListenToRemote != nil {
			err = wrapper.RemoteListenToRemote.Stop()
		}
	case db.LocalListenToRemote:
		if wrapper.LocalListenToRemote != nil {
			err = wrapper.LocalListenToRemote.Stop()
		}
	case db.RemoteListenToLocal:
		if wrapper.RemoteListenToLocal != nil {
			err = wrapper.RemoteListenToLocal.Stop()
		}
	}

	if err != nil {
		zap.L().Warn("Error stopping forward", zap.Error(err))
	}
}

// stopWrapperWithContext stops a forward wrapper with timeout control
func (s *ForwardService) stopWrapperWithContext(wrapper ForwardWrapper, ctx context.Context) error {
	// Cancel context first
	if wrapper.cancel != nil {
		wrapper.cancel()
	}

	// Stop forward with timeout
	done := make(chan error, 1)
	go func() {
		var err error
		switch wrapper.Type {
		case db.RemoteListenToRemote:
			if wrapper.RemoteListenToRemote != nil {
				err = wrapper.RemoteListenToRemote.Stop()
			}
		case db.LocalListenToRemote:
			if wrapper.LocalListenToRemote != nil {
				err = wrapper.LocalListenToRemote.Stop()
			}
		case db.RemoteListenToLocal:
			if wrapper.RemoteListenToLocal != nil {
				err = wrapper.RemoteListenToLocal.Stop()
			}
		}
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			zap.L().Warn("Error stopping forward", zap.Error(err))
		}
		return err
	case <-ctx.Done():
		return fmt.Errorf("stop timeout: %w", ctx.Err())
	}
}

// shouldRebuild checks if a forward should be rebuilt based on exponential backoff
//
// Returns:
// - shouldRebuild: true if rebuild should proceed now
// - nextRebuildIn: duration until next rebuild is allowed (0 if shouldRebuild is true)
//
// Exponential backoff formula:
//
//	interval = min(base * 2^failures, maxBackoff)
//
// Examples:
//
//	Failure 0: 1s   (1 * 2^0 = 1s)
//	Failure 1: 2s   (1 * 2^1 = 2s)
//	Failure 2: 4s   (1 * 2^2 = 4s)
//	Failure 3: 8s   (1 * 2^3 = 8s)
//	Failure 4: 16s  (1 * 2^4 = 16s)
//	Failure 5: 32s  (1 * 2^5 = 32s)
//	Failure 6: 64s  (1 * 2^6 = 64s)
//	Failure 7+: 120s (max)
func (s *ForwardService) shouldRebuild(forwardID string) (shouldRebuild bool, nextRebuildIn time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	failures := s.consecutiveFailures[forwardID]
	lastTime, hasLastTime := s.lastRebuildTime[forwardID]

	// Calculate backoff interval using exponential formula
	backoffInterval := s.calculateBackoff(failures)

	// If never rebuilt before, allow immediately and mark rebuild time
	if !hasLastTime {
		s.lastRebuildTime[forwardID] = time.Now()
		return true, 0
	}

	// Check if enough time has passed since last rebuild
	elapsed := time.Since(lastTime)
	if elapsed >= backoffInterval {
		// Update last rebuild time to now
		s.lastRebuildTime[forwardID] = time.Now()
		return true, 0
	}

	// Not enough time passed, calculate remaining wait time
	nextRebuildIn = backoffInterval - elapsed
	return false, nextRebuildIn
}

// calculateBackoff calculates exponential backoff interval
func (s *ForwardService) calculateBackoff(failures int) time.Duration {
	if failures == 0 {
		return s.baseBackoff
	}

	// Exponential backoff: base * 2^failures
	interval := s.baseBackoff * time.Duration(1<<uint(failures))

	// Cap at max backoff
	if interval > s.maxBackoff {
		interval = s.maxBackoff
	}

	return interval
}

// recordRebuildFailure records a rebuild failure (increments consecutive failure counter)
func (s *ForwardService) recordRebuildFailure(forwardID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.consecutiveFailures[forwardID]++
	s.lastRebuildTime[forwardID] = time.Now()

	failures := s.consecutiveFailures[forwardID]
	backoff := s.calculateBackoff(failures)

	zap.L().Warn("Recorded rebuild failure",
		zap.String("id", forwardID),
		zap.Int("consecutive_failures", failures),
		zap.Duration("next_backoff", backoff))
}

// recordRebuildSuccess records a rebuild success (resets consecutive failure counter)
func (s *ForwardService) recordRebuildSuccess(forwardID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.consecutiveFailures, forwardID)
	delete(s.lastRebuildTime, forwardID)

	zap.L().Info("Rebuild successful, reset failure counter",
		zap.String("id", forwardID))
}

// rebuildErrorForward rebuilds a forward that has StatusError
//
// This method performs a single rebuild attempt and relies on the sync loop's
// exponential backoff mechanism for retry control. This maintains consistency
// with the simplified architecture where forwards fail fast and recovery is
// managed at the service layer.
func (s *ForwardService) rebuildErrorForward(forwardID string, wrapper ForwardWrapper) {
	// Record rebuild start time for backoff calculation
	s.mu.Lock()
	s.lastRebuildTime[forwardID] = time.Now()
	s.mu.Unlock()

	zap.L().Info("Rebuilding error forward",
		zap.String("id", forwardID),
		zap.Int("consecutive_failures", s.consecutiveFailures[forwardID]))

	// Step 1: Delete from forwards map
	s.mu.Lock()
	delete(s.forwards, forwardID)
	s.mu.Unlock()

	// Step 2: Stop the old forward (cleanup resources)
	s.stopWrapper(wrapper)
	zap.L().Info("Old forward stopped",
		zap.String("id", forwardID))

	// Step 3: Get forward config from database
	dbForward, err := s.db.GetForward(forwardID)
	if err != nil {
		zap.L().Error("Failed to get forward from database for rebuild",
			zap.String("id", forwardID),
			zap.Error(err))
		// Record failure - will trigger exponential backoff
		s.recordRebuildFailure(forwardID)
		return
	}

	// Step 4: Start new forward (single attempt - sync loop handles retries via exponential backoff)
	if err := s.startForward(dbForward); err != nil {
		zap.L().Error("Failed to rebuild forward",
			zap.String("id", forwardID),
			zap.Error(err))
		s.updateStatus(forwardID, "error", err.Error())

		// Record failure - will trigger exponential backoff for next rebuild attempt
		s.recordRebuildFailure(forwardID)
		return
	}

	// Rebuild successful
	zap.L().Info("Forward rebuilt successfully",
		zap.String("id", forwardID))

	// Record success - resets consecutive failure counter
	s.recordRebuildSuccess(forwardID)
}

// updateStatus updates the status of a forward in database
func (s *ForwardService) updateStatus(forwardID, status, errorMsg string) {
	forwardStatus := &db.ForwardStatus{
		ForwardID:     forwardID,
		Status:        status,
		LastHeartbeat: time.Now(),
		ErrorMessage:  errorMsg,
	}

	if err := s.db.CreateOrUpdateStatus(forwardStatus); err != nil {
		zap.L().Error("Failed to update status",
			zap.String("forward_id", forwardID),
			zap.Error(err))
	}
}

// getHopsForHost resolves a hostname to SSH hop chain
// Returns empty slice for "local" (no SSH hops needed)
func (s *ForwardService) getHopsForHost(hostname string) ([]*tunnel.HopConfig, error) {
	if hostname == "local" {
		return []*tunnel.HopConfig{}, nil
	}

	hops, err := forwarding.BuildHopChainFromSSHConfig(hostname)
	if err != nil {
		return nil, err
	}

	return hops, nil
}

// newHopConfigProvider creates a HopConfigProvider for the connection pool
//
// This provider is called when the pool needs to establish a new connection.
// It takes a ConnectionSignature and returns the hop configuration and SSH client
// config builder needed to establish the connection.
func (s *ForwardService) newHopConfigProvider() connection.HopConfigProvider {
	return func(sig connection.ConnectionSignature) ([]*tunnel.HopConfig, *connection.SSHClientConfigBuilder, error) {
		// Build hop chain from signature using cached SSH config
		hops, err := s.buildHopsFromSignature(sig)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to build hops from signature: %w", err)
		}

		// Create SSH client config builder
		builder := connection.NewSSHClientConfigBuilder()

		return hops, builder, nil
	}
}

// buildHopsFromSignature converts a ConnectionSignature to a hop chain
//
// This is the inverse operation of buildSignature() in forwards.
// It reconstructs the hop chain from the signature's jump chain and destination.
// It queries SSH config to get complete hop configurations.
//
// This function now uses the ForwardService's SSH config cache to avoid repeated
// file I/O and parsing overhead when building hop chains for different signatures.
func (s *ForwardService) buildHopsFromSignature(sig connection.ConnectionSignature) ([]*tunnel.HopConfig, error) {
	// If hostname is empty, return empty hop chain
	if sig.Hostname == "" {
		return []*tunnel.HopConfig{}, nil
	}

	// Get SSH config path
	homeDir, err := util.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}
	configPath := filepath.Join(homeDir, ".ssh", "config")

	// Try to get from cache first
	s.cacheMu.RLock()
	sshConfig, exists := s.sshConfigCache[configPath]
	s.cacheMu.RUnlock()

	if !exists {
		// Cache miss - parse SSH config file
		parser := config.NewParser()
		parsedConfig, err := parser.ParseConfig(configPath)
		if err != nil {
			// If SSH config doesn't exist, continue with minimal config
			// This allows the system to work without ~/.ssh/config
			parsedConfig = nil
		}

		// Cache the parsed config (even nil is cached to avoid repeated failed parses)
		s.cacheMu.Lock()
		s.sshConfigCache[configPath] = parsedConfig
		s.cacheMu.Unlock()

		sshConfig = parsedConfig
	}

	// Build hop chain from jump hosts + final destination
	hops := make([]*tunnel.HopConfig, 0, len(sig.JumpChain)+1)

	// Add all jump hosts to the hop chain
	for _, jumpHost := range sig.JumpChain {
		hop, err := s.buildHopFromHost(sshConfig, jumpHost)
		if err != nil {
			return nil, fmt.Errorf("failed to build hop for jump host %s: %w", jumpHost, err)
		}
		hops = append(hops, hop)
	}

	// Reverse-lookup: find the SSH config alias whose HostName matches sig.Hostname
	hostAlias := sig.Hostname
	if sshConfig != nil && sshConfig.Hosts != nil {
		for alias, hostConfig := range sshConfig.Hosts {
			if hostConfig.HostName == sig.Hostname {
				hostAlias = alias
				break
			}
		}
	}

	// Add the final destination hop
	finalHop := &tunnel.HopConfig{
		Host:     hostAlias,
		HostName: sig.Hostname,
		Port:     sig.Port,
		User:     sig.Username,
	}

	// Try to get additional config from SSH config for final destination
	if sshConfig != nil {
		if hostConfig, err := sshConfig.GetHostConfig(hostAlias); err == nil {
			// Merge SSH config with signature values (signature takes precedence)
			if finalHop.HostName == "" || finalHop.HostName == sig.Hostname {
				finalHop.HostName = hostConfig.HostName
			}
			if finalHop.Port == 0 && hostConfig.Port != 0 {
				finalHop.Port = hostConfig.Port
			}
			if finalHop.User == "" && hostConfig.User != "" {
				finalHop.User = hostConfig.User
			}
			if hostConfig.IdentityFile != "" {
				finalHop.IdentityFile = hostConfig.IdentityFile
			}
		}
	}

	// Apply defaults for final hop
	if finalHop.HostName == "" {
		finalHop.HostName = sig.Hostname
	}
	if finalHop.Port == 0 {
		finalHop.Port = 22
	}

	hops = append(hops, finalHop)

	return hops, nil
}

// buildHopFromHost builds a HopConfig for a host by querying SSH config
func (s *ForwardService) buildHopFromHost(sshConfig *config.SSHConfig, host string) (*tunnel.HopConfig, error) {
	hop := &tunnel.HopConfig{
		Host: host,
	}

	if sshConfig != nil {
		// Try to get config from SSH config
		hostConfig, err := sshConfig.GetHostConfig(host)
		if err != nil {
			// SSH config parse failed, log debug and continue with defaults
			zap.L().Debug("SSH config parse failed, using defaults",
				zap.String("host", host),
				zap.Error(err),
			)
		} else {
			// Found in SSH config
			hop.HostName = hostConfig.HostName
			hop.Port = hostConfig.Port
			hop.User = hostConfig.User
			hop.IdentityFile = hostConfig.IdentityFile
			hop.CertificateFile = hostConfig.CertificateFile
		}
	}

	// Apply defaults
	if hop.HostName == "" {
		hop.HostName = host
	}
	if hop.Port == 0 {
		hop.Port = 22
	}

	return hop, nil
}
