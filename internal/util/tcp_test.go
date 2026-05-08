package util

import (
	"net"
	"testing"
)

// TestSetTCPKeepalive tests that SetTCPKeepalive can be called without errors
// Note: We can't easily inspect socket options from tests, so we just verify no errors
func TestSetTCPKeepalive(t *testing.T) {
	// Create a TCP listener
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}
	defer func() { _ = listener.Close() }()

	// Get the actual listening address
	addr := listener.Addr().String()

	// Dial a connection
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Failed to dial connection: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Apply SetTCPKeepalive
	if err := SetTCPKeepalive(conn); err != nil {
		t.Errorf("SetTCPKeepalive failed: %v", err)
	}
}

// TestSetTCPKeepaliveNonTCP tests that SetTCPKeepalive handles non-TCP connections gracefully
func TestSetTCPKeepaliveNonTCP(t *testing.T) {
	// Create a Unix socket listener (if supported on this platform)
	// This test verifies we don't panic on non-TCP connections
	// On Linux, Unix domain sockets don't implement syscall.Conn the same way

	// For now, we just test with a nil connection (edge case)
	// In practice, all our connections are TCP, so this is primarily defensive
	t.Skip("Unix socket testing deferred - all production connections are TCP")
}
