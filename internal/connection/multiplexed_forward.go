package connection

import (
	"context"
	"fmt"
	"net"
	"time"
)

// MultiplexedForward represents a forward that uses connection pooling.
//
// It enables Forward implementations to use SSH channels from the connection pool
// instead of creating new SSH connections for each forward.
//
// Key features:
// - Acquires connections from the pool on demand
// - Creates SSH channels over the pooled connection
// - Releases connections back to the pool when done
// - Manages connection lifecycle through Acquire/Release pattern
//
// Usage pattern:
//
//	mf := NewMultiplexedForward(pool, sig, forwardID)
//	conn, err := mf.NewChannel("direct-tcpip", "127.0.0.1:8888")
//	if err != nil { ... }
//	// Use conn (a net.Conn)
//	conn.Close() // Releases connection back to pool
type MultiplexedForward struct {
	// pool manages the connection lifecycle
	pool *ConnectionManager

	// signature uniquely identifies the SSH connection
	signature ConnectionSignature

	// forwardID is used for health check registration
	forwardID string

	// ctx is used for cancellation
	ctx context.Context

	// cancel cancels the context
	cancel context.CancelFunc
}

// NewMultiplexedForward creates a new MultiplexedForward.
//
// Parameters:
//   - pool: The connection manager to acquire connections from
//   - sig: The connection signature identifying which SSH connection to use
//   - forwardID: Unique identifier for this forward (used for health check registration)
func NewMultiplexedForward(pool *ConnectionManager, sig ConnectionSignature, forwardID string) *MultiplexedForward {
	ctx, cancel := context.WithCancel(context.Background())

	return &MultiplexedForward{
		pool:      pool,
		signature: sig,
		forwardID: forwardID,
		ctx:       ctx,
		cancel:    cancel,
	}
}

// Connect acquires a connection from the pool without creating an SSH channel.
//
// This method provides a two-phase connection pattern:
// 1. Call Connect() to get a connection wrapper
// 2. Create SSH channels as needed using the connection
//
// This allows forward implementations to create multiple channels over
// the same pooled connection if needed.
//
// The returned net.Conn wrapper must have Close() called to release
// the pooled connection back to the pool.
//
// Returns an error if:
//   - Context is cancelled
//   - Connection acquisition fails
func (mf *MultiplexedForward) Connect() (*SSHChannelConn, error) {
	// Acquire connection from pool
	conn, err := mf.pool.Acquire(mf.ctx, mf.signature, mf.forwardID)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}

	return &SSHChannelConn{
		pool:       mf.pool,
		pooledConn: conn,
		forwardID:  mf.forwardID,
	}, nil
}

// NewChannel creates a new SSH channel over a pooled connection.
//
// This is the primary method for creating SSH channels. It:
// 1. Acquires a connection from the pool (or creates a new one)
// 2. Creates a new SSH channel using conn.Client.Dial("tcp", target)
// 3. Returns a net.Conn wrapper that releases the connection when closed
//
// Parameters:
//   - target: The target address to connect to (e.g., "127.0.0.1:8888")
//
// Returns a net.Conn that wraps the SSH channel and releases the pooled
// connection when closed.
//
// Returns an error if:
//   - Context is cancelled
//   - Connection acquisition fails
//   - SSH channel creation fails
func (mf *MultiplexedForward) NewChannel(target string) (net.Conn, error) {
	// Acquire connection from pool
	conn, err := mf.pool.Acquire(mf.ctx, mf.signature, mf.forwardID)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}

	// Create SSH channel using the client's Dial method
	// This is the standard way to create TCP connections over SSH
	sshConn, err := conn.Client.Dial("tcp", target)
	if err != nil {
		// Release the connection if channel creation fails
		_ = mf.pool.Release(conn, mf.forwardID)
		return nil, fmt.Errorf("failed to create SSH channel: %w", err)
	}

	// Return wrapped connection that manages lifecycle
	return &SSHChannelConn{
		conn:       sshConn,
		pool:       mf.pool,
		pooledConn: conn,
		forwardID:  mf.forwardID,
	}, nil
}

// Close releases resources and cancels the context.
//
// Any pending operations will be cancelled.
func (mf *MultiplexedForward) Close() error {
	mf.cancel()
	return nil
}

// SSHChannelConn wraps an SSH channel connection as net.Conn.
//
// It implements the net.Conn interface and automatically manages the
// pooled connection lifecycle. When the SSH channel is closed, the
// pooled connection is released back to the pool.
type SSHChannelConn struct {
	// conn is the underlying SSH connection (implements net.Conn)
	conn net.Conn

	// pool manages connection lifecycle
	pool *ConnectionManager

	// pooledConn is the pooled connection being used
	pooledConn *PooledConnection

	// forwardID is used for releasing the connection
	forwardID string

	// closed ensures Close() is idempotent
	closed bool
}

// Read reads data from the connection.
func (c *SSHChannelConn) Read(b []byte) (n int, err error) {
	if c.conn == nil {
		return 0, fmt.Errorf("connection not established")
	}
	return c.conn.Read(b)
}

// Write writes data to the connection.
func (c *SSHChannelConn) Write(b []byte) (n int, err error) {
	if c.conn == nil {
		return 0, fmt.Errorf("connection not established")
	}
	return c.conn.Write(b)
}

// Close closes the SSH channel and releases the pooled connection.
//
// This method is idempotent - multiple calls are safe.
func (c *SSHChannelConn) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true

	// Close the SSH channel
	var err error
	if c.conn != nil {
		err = c.conn.Close()
	}

	// Release the pooled connection back to the pool
	if c.pooledConn != nil {
		_ = c.pool.Release(c.pooledConn, c.forwardID)
	}

	return err
}

// LocalAddr returns the local network address.
//
// If the underlying SSH channel is not yet established (c.conn == nil),
// returns nil to clearly indicate no connection. Callers should check
// for nil before using the address for logging or other purposes.
func (c *SSHChannelConn) LocalAddr() net.Addr {
	if c.conn == nil {
		return nil
	}
	return c.conn.LocalAddr()
}

// RemoteAddr returns the remote network address.
//
// If the underlying SSH channel is not yet established (c.conn == nil),
// returns nil to clearly indicate no connection. Callers should check
// for nil before using the address for logging or other purposes.
func (c *SSHChannelConn) RemoteAddr() net.Addr {
	if c.conn == nil {
		return nil
	}
	return c.conn.RemoteAddr()
}

// SetDeadline sets the read and write deadlines.
func (c *SSHChannelConn) SetDeadline(t time.Time) error {
	if c.conn == nil {
		return fmt.Errorf("connection not established")
	}
	return c.conn.SetDeadline(t)
}

// SetReadDeadline sets the deadline for future Read calls.
func (c *SSHChannelConn) SetReadDeadline(t time.Time) error {
	if c.conn == nil {
		return fmt.Errorf("connection not established")
	}
	return c.conn.SetReadDeadline(t)
}

// SetWriteDeadline sets the deadline for future Write calls.
func (c *SSHChannelConn) SetWriteDeadline(t time.Time) error {
	if c.conn == nil {
		return fmt.Errorf("connection not established")
	}
	return c.conn.SetWriteDeadline(t)
}
