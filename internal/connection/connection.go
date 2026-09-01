package connection

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// ConnectionStatus represents the current state of a pooled connection.
type ConnectionStatus int

const (
	// StatusActive indicates the connection is in use and healthy.
	StatusActive ConnectionStatus = iota
	// StatusIdle indicates the connection is healthy but not currently in use.
	StatusIdle
	// StatusClosed indicates the connection has been closed and should not be used.
	StatusClosed
)

// String returns a string representation of the connection status.
func (s ConnectionStatus) String() string {
	switch s {
	case StatusActive:
		return "active"
	case StatusIdle:
		return "idle"
	case StatusClosed:
		return "closed"
	default:
		return "unknown"
	}
}

// PooledConnection wraps an SSH client with metadata for connection pooling.
//
// It tracks:
// - Connection signature (for identification in the pool)
// - Creation and last-used timestamps
// - Reference count (number of active users)
// - Connection status (active, idle, closed)
// - Context for cancellation
// - All intermediate hop connections (for proper cleanup in multi-hop scenarios)
//
// All operations are thread-safe using sync.RWMutex.
type PooledConnection struct {
	// Client is the underlying SSH client (final destination).
	Client *ssh.Client

	// AllClients contains all SSH clients in the hop chain, including intermediate hops.
	// This is critical for proper resource cleanup in multi-hop (ProxyJump) scenarios.
	// Order: [hop0, hop1, ..., finalClient] where Client == AllClients[len(AllClients)-1]
	//
	// Deprecated: Use HopInfos instead for proper hop reuse cleanup.
	AllClients []*ssh.Client

	// HopInfos contains detailed information about each hop in the chain.
	// This is used for proper cleanup in hop reuse scenarios:
	// - HopCreated hops should be closed when this connection is released
	// - HopReused hops should only have their reference count decremented
	HopInfos []HopInfo

	// Signature uniquely identifies this connection in the pool.
	Signature ConnectionSignature

	// CreatedAt is when this connection was established.
	CreatedAt time.Time

	// LastUsedAt is when this connection was last acquired/released.
	LastUsedAt time.Time

	// Status indicates the current state of the connection.
	Status ConnectionStatus

	// RefCount is the number of active references to this connection.
	RefCount int

	// Context is used for cancellation and lifetime management.
	Context context.Context

	// CancelFunc cancels the context, triggering cleanup.
	CancelFunc context.CancelFunc

	// mu protects all fields for concurrent access.
	mu sync.RWMutex
}

// NewPooledConnection creates a new PooledConnection with the given client and signature.
// The connection starts with status=Active and refCount=1 (caller holds the initial reference).
func NewPooledConnection(client *ssh.Client, sig ConnectionSignature) *PooledConnection {
	ctx, cancel := context.WithCancel(context.Background())

	now := time.Now()
	return &PooledConnection{
		Client:     client,
		Signature:  sig,
		CreatedAt:  now,
		LastUsedAt: now,
		Status:     StatusActive,
		RefCount:   1, // Caller holds initial reference
		Context:    ctx,
		CancelFunc: cancel,
	}
}

// Acquire increments the reference count atomically.
// It also updates the LastUsedAt timestamp and sets status to Active.
//
// Returns error if the connection is closed.
func (pc *PooledConnection) Acquire() error {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	if pc.Status == StatusClosed {
		return fmt.Errorf("cannot acquire closed connection")
	}

	pc.RefCount++
	pc.LastUsedAt = time.Now()
	pc.Status = StatusActive

	return nil
}

// Release decrements the reference count atomically.
// If the reference count reaches zero, the status is set to Idle.
//
// The caller should check if RefCount is zero after calling Release
// and initiate lingering/cleanup if needed.
func (pc *PooledConnection) Release() error {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	if pc.RefCount <= 0 {
		return fmt.Errorf("release called on connection with refCount=%d", pc.RefCount)
	}

	pc.RefCount--
	pc.LastUsedAt = time.Now()

	// If no more references, mark as idle
	if pc.RefCount == 0 {
		pc.Status = StatusIdle
	}

	return nil
}

// IsActive returns true if the connection is not closed.
func (pc *PooledConnection) IsActive() bool {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.Status != StatusClosed
}

// GetRefCount returns the current reference count.
func (pc *PooledConnection) GetRefCount() int {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.RefCount
}

// GetStatus returns the current connection status.
func (pc *PooledConnection) GetStatus() ConnectionStatus {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.Status
}

// Close marks the connection as closed and cancels its context.
// It does NOT close the underlying SSH client - that's the caller's responsibility.
func (pc *PooledConnection) Close() {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	if pc.Status == StatusClosed {
		return
	}

	pc.Status = StatusClosed
	pc.CancelFunc()
}

// GetLastUsedAt returns the last-used timestamp.
func (pc *PooledConnection) GetLastUsedAt() time.Time {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.LastUsedAt
}

// GetCreatedAt returns the creation timestamp.
func (pc *PooledConnection) GetCreatedAt() time.Time {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.CreatedAt
}
