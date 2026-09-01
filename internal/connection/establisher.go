package connection

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/hallelujah-shih/ssh-multihop/internal/tunnel"
	"github.com/hallelujah-shih/ssh-multihop/internal/util"
	"golang.org/x/crypto/ssh"
)

// Establish establishes SSH connections through multiple hops.
//
// It connects sequentially through each hop:
// - First hop: direct connection via net.Dial
// - Subsequent hops: connection through previous hop's Dial
//
// Returns:
// - finalClient: the last SSH client in the chain (for port forwarding)
// - allClients: all SSH clients (for connection pooling)
// - error: any error that occurred (includes hop index)
func Establish(hops []*tunnel.HopConfig, builder *SSHClientConfigBuilder) (finalClient *ssh.Client, allClients []*ssh.Client, err error) {
	// Validate inputs
	if len(hops) == 0 {
		return nil, nil, fmt.Errorf("no hops provided")
	}

	if builder == nil {
		return nil, nil, fmt.Errorf("builder is nil")
	}

	// Create slice to track all clients
	allClients = make([]*ssh.Client, 0, len(hops))

	// Establish connections sequentially
	for i, hop := range hops {
		// Build SSH config for this hop
		sshConfig, err := builder.Build(hop)
		if err != nil {
			closeAllClients(allClients)
			return nil, nil, fmt.Errorf("failed to build SSH config for hop %d (%s): %w", i, hop.Host, err)
		}

		// Establish connection
		var conn net.Conn
		// Use net.JoinHostPort to support both IPv4 and IPv6
		addr := net.JoinHostPort(hop.HostName, fmt.Sprintf("%d", hop.Port))

		if i == 0 {
			// First hop: direct connection
			conn, err = net.DialTimeout("tcp", addr, defaultTimeout)
		} else {
			// Subsequent hops: dial through previous hop
			prevClient := allClients[i-1]
			conn, err = dialThroughWithTimeout(prevClient, addr)
		}

		if err != nil {
			closeAllClients(allClients)
			return nil, nil, fmt.Errorf("failed to connect to hop %d (%s at %s): %w", i, hop.Host, addr, err)
		}

		// Configure TCP keepalive to detect connection breaks faster
		// This helps detect remote host reboots much faster than system default (7200s)
		// Only applies to direct TCP connections (first hop), not SSH-tunneled connections
		if i == 0 {
			if err := util.SetTCPKeepalive(conn); err != nil {
				_ = conn.Close()
				closeAllClients(allClients)
				return nil, nil, fmt.Errorf("failed to configure TCP keepalive for hop %d (%s): %w", i, hop.Host, err)
			}
		}

		// Create SSH client connection
		sshConn, chans, reqs, err := newClientConnWithDeadline(conn, addr, sshConfig)
		if err != nil {
			_ = conn.Close()
			closeAllClients(allClients)
			return nil, nil, fmt.Errorf("SSH handshake failed for hop %d (%s): %w", i, hop.Host, err)
		}

		// Create SSH client
		client := ssh.NewClient(sshConn, chans, reqs)
		allClients = append(allClients, client)
	}

	// Return final client and all clients
	finalClient = allClients[len(allClients)-1]
	return finalClient, allClients, nil
}

// closeAllClients closes all SSH clients in reverse order.
// This is used to cleanup partially established connections on error.
func closeAllClients(clients []*ssh.Client) {
	for i := len(clients) - 1; i >= 0; i-- {
		if clients[i] != nil {
			_ = clients[i].Close()
		}
	}
}

// defaultTimeout bounds TCP dial, tunneled channel-open, and the SSH handshake.
// Without it a stalled peer wedges the service sync loop (pendingStarts never cleared).
var defaultTimeout = 30 * time.Second

// newClientConnWithDeadline bounds the SSH handshake to defaultTimeout.
// x/crypto's ClientConfig.Timeout only applies to ssh.Dial, and SetDeadline
// is unsupported on tunneled channel conns (tcpChan), so the handshake runs
// in a goroutine and the conn is closed on timeout to unblock it.
func newClientConnWithDeadline(conn net.Conn, addr string, config *ssh.ClientConfig) (ssh.Conn, <-chan ssh.NewChannel, <-chan *ssh.Request, error) {
	type result struct {
		conn  ssh.Conn
		chans <-chan ssh.NewChannel
		reqs  <-chan *ssh.Request
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		c, chs, rq, err := ssh.NewClientConn(conn, addr, config)
		ch <- result{c, chs, rq, err}
	}()
	select {
	case r := <-ch:
		return r.conn, r.chans, r.reqs, r.err
	case <-time.After(defaultTimeout):
		_ = conn.Close()
		return nil, nil, nil, fmt.Errorf("SSH handshake with %s timed out after %s", addr, defaultTimeout)
	}
}

// dialThroughWithTimeout bounds client.Dial; SSH channel-open has no deadline
// support, so a stalled channel open would otherwise block forever. Late
// arrivals after timeout are closed by a drainer goroutine.
func dialThroughWithTimeout(client *ssh.Client, addr string) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		c, err := client.Dial("tcp", addr)
		ch <- result{c, err}
	}()
	select {
	case r := <-ch:
		return r.conn, r.err
	case <-time.After(defaultTimeout):
		go func() {
			if r := <-ch; r.conn != nil {
				_ = r.conn.Close()
			}
		}()
		return nil, fmt.Errorf("dial %s through previous hop timed out after %s", addr, defaultTimeout)
	}
}

// HopSource indicates how a hop connection was obtained
type HopSource int

const (
	// HopCreated means the connection was newly created
	HopCreated HopSource = iota
	// HopReused means the connection was reused from pool
	HopReused
)

// HopInfo contains information about a hop connection
type HopInfo struct {
	Client   *ssh.Client
	Source   HopSource
	PoolHash string // hash in pool if reused, empty if created
}

// EstablishWithReuse establishes SSH connections through multiple hops with reuse support.
//
// Unlike Establish(), this function attempts to reuse intermediate hop connections
// from the connection pool. This is critical for multi-hop (ProxyJump) scenarios
// where multiple forwards share the same intermediate hops.
//
// Parameters:
//   - ctx: context for cancellation
//   - hops: the hop chain to establish
//   - builder: SSH client config builder
//   - pool: connection manager for reuse (can be nil to disable reuse)
//   - forwardID: forward identifier for health check registration
//
// Returns:
//   - finalClient: the last SSH client in the chain (for port forwarding)
//   - hopInfos: information about each hop (for proper cleanup)
//   - error: any error that occurred
//
// Example:
//
//	Forward A: local -> t2 -> vmr.u24
//	  - Creates t2 connection, adds to pool
//	  - Creates vmr connection
//	Forward B: local -> t2 -> dc4
//	  - Reuses t2 connection from pool (HopReused)
//	  - Creates dc4 connection
func EstablishWithReuse(
	ctx context.Context,
	hops []*tunnel.HopConfig,
	builder *SSHClientConfigBuilder,
	pool *ConnectionManager,
	forwardID string,
) (finalClient *ssh.Client, hopInfos []HopInfo, err error) {
	if len(hops) == 0 {
		return nil, nil, fmt.Errorf("no hops provided")
	}

	if builder == nil {
		return nil, nil, fmt.Errorf("builder is nil")
	}

	hopInfos = make([]HopInfo, 0, len(hops))
	var prevClient *ssh.Client

	for i, hop := range hops {
		select {
		case <-ctx.Done():
			cleanupCreatedHops(hopInfos)
			return nil, nil, ctx.Err()
		default:
		}

		partialSig := buildPartialSignature(hops[:i+1])
		var client *ssh.Client
		var source HopSource
		var poolHash string

		if pool != nil {
			pooledConn, acquireErr := pool.acquirePartial(ctx, partialSig, forwardID)
			if acquireErr == nil && pooledConn != nil {
				client = pooledConn.Client
				source = HopReused
				poolHash = partialSig.Hash()
			}
		}

		if client == nil {
			sshConfig, buildErr := builder.Build(hop)
			if buildErr != nil {
				cleanupCreatedHops(hopInfos)
				return nil, nil, fmt.Errorf("failed to build SSH config for hop %d (%s): %w", i, hop.Host, buildErr)
			}

			addr := net.JoinHostPort(hop.HostName, fmt.Sprintf("%d", hop.Port))
			var conn net.Conn

			if i == 0 {
				conn, err = net.DialTimeout("tcp", addr, defaultTimeout)
			} else {
				conn, err = dialThroughWithTimeout(prevClient, addr)
			}

			if err != nil {
				cleanupCreatedHops(hopInfos)
				return nil, nil, fmt.Errorf("failed to connect to hop %d (%s at %s): %w", i, hop.Host, addr, err)
			}

			if i == 0 {
				if keepaliveErr := util.SetTCPKeepalive(conn); keepaliveErr != nil {
					_ = conn.Close()
					cleanupCreatedHops(hopInfos)
					return nil, nil, fmt.Errorf("failed to configure TCP keepalive for hop %d (%s): %w", i, hop.Host, keepaliveErr)
				}
			}

			sshConn, chans, reqs, handshakeErr := newClientConnWithDeadline(conn, addr, sshConfig)
			if handshakeErr != nil {
				_ = conn.Close()
				cleanupCreatedHops(hopInfos)
				return nil, nil, fmt.Errorf("SSH handshake failed for hop %d (%s): %w", i, hop.Host, handshakeErr)
			}

			client = ssh.NewClient(sshConn, chans, reqs)
			source = HopCreated

			if pool != nil && i < len(hops)-1 {
				pool.addPartial(partialSig, client, forwardID)
			}
		}

		hopInfos = append(hopInfos, HopInfo{
			Client:   client,
			Source:   source,
			PoolHash: poolHash,
		})
		prevClient = client
	}

	if len(hopInfos) == 0 {
		return nil, nil, fmt.Errorf("no connections established")
	}
	finalClient = hopInfos[len(hopInfos)-1].Client
	return finalClient, hopInfos, nil
}

// buildPartialSignature creates a ConnectionSignature for a partial hop chain.
// This is used to identify intermediate hops in the pool for reuse.
func buildPartialSignature(hops []*tunnel.HopConfig) ConnectionSignature {
	if len(hops) == 0 {
		return ConnectionSignature{}
	}

	finalHop := hops[len(hops)-1]
	jumpChain := make([]string, 0, len(hops)-1)
	for i := 0; i < len(hops)-1; i++ {
		jumpChain = append(jumpChain, hops[i].Host)
	}

	return ConnectionSignature{
		Username:  finalHop.User,
		Hostname:  finalHop.HostName,
		Port:      finalHop.Port,
		JumpChain: jumpChain,
	}
}

// cleanupCreatedHops closes only the created (non-reused) hops.
// This is called on error to clean up partial connections.
func cleanupCreatedHops(hopInfos []HopInfo) {
	for i := len(hopInfos) - 1; i >= 0; i-- {
		if hopInfos[i].Source == HopCreated && hopInfos[i].Client != nil {
			_ = hopInfos[i].Client.Close()
		}
	}
}
