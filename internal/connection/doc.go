// Package connection provides SSH connection establishment for multi-hop tunnels.
//
// The connection package provides simple SSH connection establishment with:
//   - Multi-hop SSH tunnel support
//   - Simple connection establishment (no health monitoring, no auto-reconnect)
//   - Cleanup functions for proper resource management
//
// # Architecture
//
// This package takes a simplified approach:
//   - EstablishSSHChain(): Creates SSH connections through hop chain
//   - Returns final SSH client and cleanup function
//   - No state management, no health monitoring, no auto-reconnect
//
// Health monitoring and reconnection are handled by the Forward layer:
//   - Forward objects monitor their own health
//   - ForwardService handles rebuilding failed forwards
//
// # Usage
//
//	// Establish connections through hop chain
//	hopChain := []*tunnel.HopConfig{
//	    {Host: "local", ...},    // First hop (local machine, skipped)
//	    {Host: "jump1", ...},    // First actual SSH connection
//	    {Host: "jump2", ...},    // Second hop (through jump1)
//	    {Host: "target", ...},   // Final destination
//	}
//
//	// Establish chain
//	client, cleanup, err := connection.EstablishSSHChain(hopChain)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Use client for port forwarding
//	listener, err := client.Listen("tcp", "127.0.0.1:8888")
//
//	// When done, cleanup all connections
//	cleanup()
//
// # Connection Lifecycle
//
// Connections are established in order:
//  1. Direct connection to first hop (after local)
//  2. Tunneled connection to second hop (through first)
//  3. Continue until final destination
//
// Cleanup function closes all connections in reverse order.
//
// # Thread Safety
//
// EstablishSSHChain() is not thread-safe. Each call creates independent
// connections. Multiple forwards can establish their own connections
// concurrently without interference.
//
// # Error Handling
//
// EstablishSSHChain() returns errors for:
//   - Empty hop chain
//   - TCP dial failures (network unreachable, timeout)
//   - SSH handshake failures (authentication, protocol errors)
//   - Tunnel establishment failures (intermediate hop failures)
//
// On error, all partially established connections are cleaned up automatically.
package connection
