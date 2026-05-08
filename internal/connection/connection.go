package connection

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hallelujah-shih/ssh-multihop/internal/tunnel"
	"golang.org/x/crypto/ssh"
)

// CleanupFunc is a function that cleans up resources
type CleanupFunc func()

// EstablishSSHChain establishes SSH connections through a hop chain
// Returns the final SSH client and a cleanup function.
//
// This is a simplified connection establishment that:
// - Creates SSH connections through multi-hop chain
// - Returns only the final client (for Forward to use)
// - Provides cleanup function to close all connections
// - No health monitoring, no auto-reconnect, no state management
//
// Forward manages its own health checking and rebuild logic.
//
// The passphraseSocket parameter is used for retrieving SSH key passphrases.
func EstablishSSHChain(hopChain []*tunnel.HopConfig, passphraseSocket *PassphraseSocket) (*ssh.Client, CleanupFunc, error) {
	if len(hopChain) == 0 {
		return nil, nil, fmt.Errorf("empty hop chain")
	}

	// Skip the first hop (local machine)
	if len(hopChain) < 2 {
		return nil, nil, fmt.Errorf("hop chain must have at least 2 hops (local + destination)")
	}

	var clients []*ssh.Client
	var cleanups []CleanupFunc

	// Establish connections for each hop after local
	for i := 1; i < len(hopChain); i++ {
		hop := hopChain[i]

		// Determine previous client (nil for first connection after local)
		var prevClient *ssh.Client
		if len(clients) > 0 {
			prevClient = clients[len(clients)-1]
		}

		// Establish connection to this hop
		client, cleanup, err := establishSingleHop(hop, prevClient, passphraseSocket)
		if err != nil {
			// Cleanup all previous connections on error
			for j := len(cleanups) - 1; j >= 0; j-- {
				cleanups[j]()
			}
			return nil, nil, fmt.Errorf("failed to establish hop %d (%s): %w", i, hop.Host, err)
		}

		clients = append(clients, client)
		cleanups = append(cleanups, cleanup)
	}

	// Create unified cleanup function that closes all connections in reverse order
	unifiedCleanup := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}

	// Return the final client
	return clients[len(clients)-1], unifiedCleanup, nil
}

// establishSingleHop establishes a single SSH connection
// If prevClient is nil, establishes direct connection
// If prevClient is provided, establishes tunneled connection through it
func establishSingleHop(hop *tunnel.HopConfig, prevClient *ssh.Client, passphraseSocket *PassphraseSocket) (*ssh.Client, CleanupFunc, error) {
	// Build SSH config for this hop
	builder := NewSSHClientConfigBuilder()
	if passphraseSocket != nil {
		builder.SetPassphraseSocket(passphraseSocket)
	}
	config, err := builder.Build(hop)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build SSH config: %w", err)
	}

	// Build address string in "hostname:port" format
	address := fmt.Sprintf("%s:%d", hop.HostName, hop.Port)

	// Establish connection
	var client *ssh.Client
	if prevClient == nil {
		// Direct connection
		client, err = dialDirect(address, config)
	} else {
		// Tunneled connection through previous hop
		client, err = dialTunneled(prevClient, address, config)
	}

	if err != nil {
		return nil, nil, err
	}

	// Create cleanup function
	cleanup := func() {
		_ = client.Close()
	}

	return client, cleanup, nil
}

// dialDirect establishes a direct SSH connection
func dialDirect(hostname string, config *ssh.ClientConfig) (*ssh.Client, error) {
	conn, err := ssh.Dial("tcp", hostname, config)
	if err != nil {
		return nil, fmt.Errorf("direct dial failed: %w", err)
	}
	return conn, nil
}

// dialTunneled establishes a tunneled SSH connection through a previous client
func dialTunneled(prevClient *ssh.Client, hostname string, config *ssh.ClientConfig) (*ssh.Client, error) {
	// Dial through the previous SSH connection
	conn, err := prevClient.Dial("tcp", hostname)
	if err != nil {
		return nil, fmt.Errorf("tunneled dial failed: %w", err)
	}

	// Create SSH connection over the tunneled connection
	ncc, chans, reqs, err := ssh.NewClientConn(conn, hostname, config)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("new client conn failed: %w", err)
	}

	client := ssh.NewClient(ncc, chans, reqs)
	return client, nil
}

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
