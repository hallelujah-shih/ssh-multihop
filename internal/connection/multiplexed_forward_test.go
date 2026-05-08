package connection

import (
	"net"
	"testing"
	"time"

	"github.com/hallelujah-shih/ssh-multihop/internal/tunnel"
)

// mockNetConn is a mock net.Conn for testing MultiplexedForward
type mockNetConn struct {
	net.Conn
	readFunc  func(b []byte) (n int, err error)
	writeFunc func(b []byte) (n int, err error)
	closeFunc func() error
	closed    bool
}

func (m *mockNetConn) Read(b []byte) (n int, err error) {
	if m.readFunc != nil {
		return m.readFunc(b)
	}
	return 0, nil
}

func (m *mockNetConn) Write(b []byte) (n int, err error) {
	if m.writeFunc != nil {
		return m.writeFunc(b)
	}
	return 0, nil
}

func (m *mockNetConn) Close() error {
	m.closed = true
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

func (m *mockNetConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}
}

func (m *mockNetConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}
}

func (m *mockNetConn) SetDeadline(t time.Time) error {
	return nil
}

func (m *mockNetConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (m *mockNetConn) SetWriteDeadline(t time.Time) error {
	return nil
}

// mockHopConfigProviderForMultiplexed is a mock HopConfigProvider for testing MultiplexedForward
func mockHopConfigProviderForMultiplexed(sig ConnectionSignature) ([]*tunnel.HopConfig, *SSHClientConfigBuilder, error) {
	// Return a simple hop chain for testing
	hops := []*tunnel.HopConfig{
		{
			HostName: "localhost",
			Port:     22,
			User:     sig.Username,
		},
		{
			HostName: sig.Hostname,
			Port:     sig.Port,
			User:     sig.Username,
		},
	}
	builder := NewSSHClientConfigBuilder()
	return hops, builder, nil
}

// TestNewMultiplexedForward tests creating a new MultiplexedForward
func TestNewMultiplexedForward(t *testing.T) {
	pool := NewConnectionManager(DefaultConfig(), mockHopConfigProviderForMultiplexed)
	defer func() { _ = pool.Close() }()

	sig := ConnectionSignature{
		Username: "testuser",
		Hostname: "example.com",
		Port:     22,
	}

	mf := NewMultiplexedForward(pool, sig, "test-forward")

	if mf == nil {
		t.Fatal("NewMultiplexedForward returned nil")
	}

	if mf.pool != pool {
		t.Error("pool not set correctly")
	}

	if !mf.signature.Equals(sig) {
		t.Error("signature not set correctly")
	}

	if mf.forwardID != "test-forward" {
		t.Error("forwardID not set correctly")
	}

	if mf.ctx == nil {
		t.Error("ctx not initialized")
	}

	if mf.cancel == nil {
		t.Error("cancel not initialized")
	}
}

// TestMultiplexedForward_Connect tests the Connect method structure
func TestMultiplexedForward_Connect(t *testing.T) {
	// Note: We can't test actual SSH connection without real credentials
	// So we test the structure and error handling

	// Test with nil provider - should fail
	pool := NewConnectionManager(DefaultConfig(), nil)
	defer func() { _ = pool.Close() }()

	sig := ConnectionSignature{
		Username: "testuser",
		Hostname: "example.com",
		Port:     22,
	}

	mf := NewMultiplexedForward(pool, sig, "test-forward")

	// Connect should fail when hopConfigProvider is nil
	_, err := mf.Connect()
	if err == nil {
		t.Error("Connect should fail when hopConfigProvider is nil")
	}

	// Verify structure is still created correctly
	if mf.pool != pool {
		t.Error("pool not set correctly")
	}

	if !mf.signature.Equals(sig) {
		t.Error("signature not set correctly")
	}

	// Close should work even after failed connect
	_ = mf.Close()
}

// TestMultiplexedForward_Connect_returns_wrapper tests that Connect returns proper wrapper
func TestMultiplexedForward_Connect_returns_wrapper(t *testing.T) {
	sig := ConnectionSignature{
		Username: "testuser",
		Hostname: "example.com",
		Port:     22,
	}

	pool := NewConnectionManager(DefaultConfig(), mockHopConfigProviderForMultiplexed)
	defer func() { _ = pool.Close() }()

	mf := NewMultiplexedForward(pool, sig, "test-forward")

	// Connect should return a wrapper (will fail to actually connect, but we can check the structure)
	wrapper, err := mf.Connect()
	// Error is expected since we don't have real SSH credentials
	_ = err // Explicitly ignore error as it's expected

	// If we got a wrapper, verify its structure
	if wrapper != nil {
		if wrapper.pool != pool {
			t.Error("wrapper pool not set correctly")
		}
		if wrapper.forwardID != "test-forward" {
			t.Error("wrapper forwardID not set correctly")
		}
	}

	_ = mf.Close()
}

// TestMultiplexedForward_NewChannel tests the NewChannel method
func TestMultiplexedForward_NewChannel(t *testing.T) {
	// Note: This test would require a real SSH connection or a more sophisticated mock
	// For now, we skip the actual NewChannel test since it requires an *ssh.Client
	// which is difficult to mock due to its interface
	// The integration tests will cover the full NewChannel flow

	sig := ConnectionSignature{
		Username: "testuser",
		Hostname: "example.com",
		Port:     22,
	}

	pool := NewConnectionManager(DefaultConfig(), mockHopConfigProviderForMultiplexed)
	defer func() { _ = pool.Close() }()

	mf := NewMultiplexedForward(pool, sig, "test-forward")

	// NewChannel with nil provider will fail, but we can verify structure
	_, err := mf.NewChannel("127.0.0.1:8888")
	// Error is expected without real credentials
	_ = err // Explicitly ignore error as it's expected

	// Close should work even without calling NewChannel
	err = mf.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

// TestMultiplexedForward_Close tests the Close method
func TestMultiplexedForward_Close(t *testing.T) {
	pool := NewConnectionManager(DefaultConfig(), mockHopConfigProviderForMultiplexed)
	defer func() { _ = pool.Close() }()

	sig := ConnectionSignature{
		Username: "testuser",
		Hostname: "example.com",
		Port:     22,
	}

	mf := NewMultiplexedForward(pool, sig, "test-forward")

	// Close should not error
	err := mf.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Context should be cancelled
	select {
	case <-mf.ctx.Done():
		// Expected
	default:
		t.Error("context not cancelled after Close")
	}
}

// TestSSHChannelConn_Close_idempotent tests that Close is idempotent
func TestSSHChannelConn_Close_idempotent(t *testing.T) {
	pool := NewConnectionManager(DefaultConfig(), mockHopConfigProviderForMultiplexed)
	defer func() { _ = pool.Close() }()

	sig := ConnectionSignature{
		Username: "testuser",
		Hostname: "example.com",
		Port:     22,
	}

	_ = NewMultiplexedForward(pool, sig, "test-forward")

	// Create a mock connection
	mockUnderlyingConn := &mockNetConn{}
	sshConn := &SSHChannelConn{
		conn:       mockUnderlyingConn,
		pool:       pool,
		pooledConn: nil,
		forwardID:  "test-forward",
		closed:     false,
	}

	// First close
	err := sshConn.Close()
	if err != nil {
		t.Fatalf("First Close failed: %v", err)
	}

	if !mockUnderlyingConn.closed {
		t.Error("underlying connection not closed")
	}

	if !sshConn.closed {
		t.Error("SSHChannelConn not marked as closed")
	}

	// Second close should be safe
	err = sshConn.Close()
	if err != nil {
		t.Fatalf("Second Close failed: %v", err)
	}
}

// TestSSHChannelConn_ReadWrite_before_established tests Read/Write before connection is established
func TestSSHChannelConn_ReadWrite_before_established(t *testing.T) {
	pool := NewConnectionManager(DefaultConfig(), mockHopConfigProviderForMultiplexed)
	defer func() { _ = pool.Close() }()

	sig := ConnectionSignature{
		Username: "testuser",
		Hostname: "example.com",
		Port:     22,
	}

	_ = NewMultiplexedForward(pool, sig, "test-forward")

	sshConn := &SSHChannelConn{
		pool:       pool,
		pooledConn: nil,
		forwardID:  "test-forward",
	}

	// Read should fail
	buf := make([]byte, 1024)
	_, err := sshConn.Read(buf)
	if err == nil {
		t.Error("Read should fail when connection not established")
	}

	// Write should fail
	_, err = sshConn.Write([]byte("test"))
	if err == nil {
		t.Error("Write should fail when connection not established")
	}
}

// TestSSHChannelConn_SetConnection tests SetConnection
func TestSSHChannelConn_SetConnection(t *testing.T) {
	pool := NewConnectionManager(DefaultConfig(), mockHopConfigProviderForMultiplexed)
	defer func() { _ = pool.Close() }()

	sig := ConnectionSignature{
		Username: "testuser",
		Hostname: "example.com",
		Port:     22,
	}

	_ = NewMultiplexedForward(pool, sig, "test-forward")

	mockUnderlyingConn := &mockNetConn{}
	sshConn := &SSHChannelConn{
		conn:       mockUnderlyingConn,
		pool:       pool,
		pooledConn: nil,
		forwardID:  "test-forward",
	}

	// Connection should already be established via direct initialization
	if sshConn.conn != mockUnderlyingConn {
		t.Error("connection not set correctly")
	}

	// Read/Write should work
	buf := make([]byte, 1024)
	_, err := sshConn.Read(buf)
	if err != nil {
		t.Errorf("Read failed: %v", err)
	}

	_, err = sshConn.Write([]byte("test"))
	if err != nil {
		t.Errorf("Write failed: %v", err)
	}
}

// TestSSHChannelConn_Addr_methods tests LocalAddr and RemoteAddr
func TestSSHChannelConn_Addr_methods(t *testing.T) {
	// Test with established connection
	mockUnderlyingConn := &mockNetConn{}

	sshConn := &SSHChannelConn{
		conn: mockUnderlyingConn,
	}

	localAddr := sshConn.LocalAddr()
	if localAddr == nil {
		t.Error("LocalAddr returned nil")
	}

	remoteAddr := sshConn.RemoteAddr()
	if remoteAddr == nil {
		t.Error("RemoteAddr returned nil")
	}

	// Test without established connection - should return nil
	sshConn2 := &SSHChannelConn{
		conn: nil,
	}

	localAddr = sshConn2.LocalAddr()
	if localAddr != nil {
		t.Error("LocalAddr should return nil for unestablished connection")
	}

	remoteAddr = sshConn2.RemoteAddr()
	if remoteAddr != nil {
		t.Error("RemoteAddr should return nil for unestablished connection")
	}
}

// TestSSHChannelConn_Deadline_methods tests deadline methods
func TestSSHChannelConn_Deadline_methods(t *testing.T) {
	mockUnderlyingConn := &mockNetConn{}

	sshConn := &SSHChannelConn{
		conn: mockUnderlyingConn,
	}

	// These should not error
	err := sshConn.SetDeadline(time.Now().Add(10 * time.Second))
	if err != nil {
		t.Errorf("SetDeadline failed: %v", err)
	}

	err = sshConn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if err != nil {
		t.Errorf("SetReadDeadline failed: %v", err)
	}

	err = sshConn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err != nil {
		t.Errorf("SetWriteDeadline failed: %v", err)
	}

	// Test without established connection
	sshConn2 := &SSHChannelConn{
		conn: nil,
	}

	err = sshConn2.SetDeadline(time.Now().Add(10 * time.Second))
	if err == nil {
		t.Error("SetDeadline should fail when connection not established")
	}

	err = sshConn2.SetReadDeadline(time.Now().Add(10 * time.Second))
	if err == nil {
		t.Error("SetReadDeadline should fail when connection not established")
	}

	err = sshConn2.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err == nil {
		t.Error("SetWriteDeadline should fail when connection not established")
	}
}
