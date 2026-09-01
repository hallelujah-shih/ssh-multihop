package forwarding

import (
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// waitFor polls cond until it returns true or the timeout expires (no naked sleeps).
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

// startEchoService starts a local echo server to act as service_addr.
func startEchoService(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func TestRemoteListenToLocal_Lifecycle(t *testing.T) {
	zap.ReplaceGlobals(zap.NewNop())

	server := newTestSSHServer(t)
	pool, hops := newTestConnectionManager(t, server)
	testDB, err := createTestDB()
	require.NoError(t, err)
	defer func() { _ = testDB.Close() }()

	echoL := startEchoService(t)

	forwardID := "rltl-lifecycle"
	fwd := NewRemoteListenToLocal("127.0.0.1:0", echoL.Addr().String(), forwardID, testDB, pool, hops)
	assert.Equal(t, StatusStopped, fwd.Status())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, fwd.Start(ctx))
	assert.Equal(t, StatusRunning, fwd.Status())

	remotePort := fwd.listener.Addr().(*net.TCPAddr).Port
	require.NotZero(t, remotePort)

	// Connect through the remote listener and verify the echo round-trip.
	payload := "hello-remote-forward"
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", remotePort), 2*time.Second)
	require.NoError(t, err)
	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))
	_, err = conn.Write([]byte(payload))
	require.NoError(t, err)

	buf := make([]byte, len(payload))
	_, err = io.ReadFull(conn, buf)
	require.NoError(t, err)
	assert.Equal(t, payload, string(buf))
	_ = conn.Close()

	require.NoError(t, fwd.Stop())
	assert.Equal(t, StatusStopped, fwd.Status())

	// Remote listener must be closed after Stop (client sent cancel-tcpip-forward).
	waitFor(t, 2*time.Second, func() bool {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", remotePort), 500*time.Millisecond)
		if err != nil {
			return true
		}
		_ = c.Close()
		return false
	})
}

func TestRemoteListenToLocal_StartForwardRejected(t *testing.T) {
	zap.ReplaceGlobals(zap.NewNop())

	server := newTestSSHServer(t)
	server.setRejectForward(true)
	pool, hops := newTestConnectionManager(t, server)
	testDB, err := createTestDB()
	require.NoError(t, err)
	defer func() { _ = testDB.Close() }()

	forwardID := "rltl-rejected"
	fwd := NewRemoteListenToLocal("127.0.0.1:23456", "127.0.0.1:59999", forwardID, testDB, pool, hops)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = fwd.Start(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create remote listener")
	assert.Equal(t, StatusError, fwd.Status())

	status, err := testDB.GetStatus(forwardID)
	require.NoError(t, err)
	assert.Equal(t, "error", status.Status)
}
