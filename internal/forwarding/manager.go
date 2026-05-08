package forwarding

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Manager manages port forwarding forwards
//
// Manager provides CRUD operations for forwards:
// - Add a new forward
// - Remove an existing forward
// - Stop a running forward
// - List all forwards
// - Get a specific forward
//
// Manager is thread-safe and can be used concurrently.
type Manager struct {
	forwards map[string]Forward
	mu       sync.RWMutex
}

// NewManager creates a new forward manager
func NewManager() *Manager {
	return &Manager{
		forwards: make(map[string]Forward),
	}
}

// Add adds a new forward with the given name
//
// If a forward with the same name already exists, it returns an error.
// The forward is started immediately after being added.
func (m *Manager) Add(name string, forward Forward) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.forwards[name]; exists {
		return fmt.Errorf("forward '%s' already exists", name)
	}

	m.forwards[name] = forward

	zap.L().Info("Forward added",
		zap.String("name", name),
		zap.String("type", forward.Type()),
		zap.String("config", forward.String()))

	return nil
}

// Remove removes and stops a forward
//
// If the forward is running, it will be stopped before removal.
// If the forward does not exist, it returns an error.
func (m *Manager) Remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	forward, exists := m.forwards[name]
	if !exists {
		return fmt.Errorf("forward '%s' not found", name)
	}

	// Stop the forward if it's running
	if forward.Status() == StatusRunning {
		if err := forward.Stop(); err != nil {
			zap.L().Error("Failed to stop forward during removal",
				zap.String("name", name),
				zap.Error(err))
		}
	}

	delete(m.forwards, name)

	zap.L().Info("Forward removed",
		zap.String("name", name))

	return nil
}

// Stop stops a running forward
//
// If the forward is not running, it returns an error.
func (m *Manager) Stop(name string) error {
	m.mu.RLock()
	forward, exists := m.forwards[name]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("forward '%s' not found", name)
	}

	if forward.Status() != StatusRunning {
		return fmt.Errorf("forward '%s' is not running (status: %s)",
			name, forward.Status())
	}

	return forward.Stop()
}

// List returns all forwards
//
// The returned slice is a snapshot and is safe to use even after
// the manager is modified.
func (m *Manager) List() []Forward {
	m.mu.RLock()
	defer m.mu.RUnlock()

	forwards := make([]Forward, 0, len(m.forwards))
	for _, forward := range m.forwards {
		forwards = append(forwards, forward)
	}

	return forwards
}

// Get retrieves a forward by name
//
// Returns the forward and true if found, nil and false otherwise.
func (m *Manager) Get(name string) (Forward, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	forward, exists := m.forwards[name]
	return forward, exists
}

// Start starts a forward by name
//
// This is a convenience method that retrieves the forward and starts it.
// The forward runs in a background goroutine.
func (m *Manager) Start(name string, ctx context.Context) error {
	forward, exists := m.Get(name)
	if !exists {
		return fmt.Errorf("forward '%s' not found", name)
	}

	// Start forward in background
	go func() {
		if err := forward.Start(ctx); err != nil {
			zap.L().Error("Forward failed",
				zap.String("name", name),
				zap.Error(err))
		}
	}()

	return nil
}

// StartAndWait starts a forward and waits for it to establish
//
// This is used for serial restoration to ensure each forward is fully
// established before starting the next one.
//
// The timeout specifies how long to wait for the forward to reach
// Running status. Returns nil if the forward is running and stable.
func (m *Manager) StartAndWait(name string, ctx context.Context, timeout time.Duration) error {
	forward, exists := m.Get(name)
	if !exists {
		return fmt.Errorf("forward '%s' not found", name)
	}

	// Start the forward
	if err := forward.Start(ctx); err != nil {
		return fmt.Errorf("failed to start forward '%s': %w", name, err)
	}

	// Wait for it to establish
	if err := waitForHealthCheck(ctx, forward, timeout); err != nil {
		return fmt.Errorf("forward '%s' failed to establish: %w", name, err)
	}

	zap.L().Info("Forward established",
		zap.String("name", name),
		zap.String("config", forward.String()))

	return nil
}
