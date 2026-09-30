package forwarding

import (
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/hallelujah-shih/ssh-multihop/internal/connection"
	"github.com/hallelujah-shih/ssh-multihop/internal/db"
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

// TestRemoteListenToLocal_DenyDiagnosis: a denied tcpip-forward must surface
// the probe classification in the Start error, and a successful start must
// not open any probe session.
func TestRemoteListenToLocal_DenyDiagnosis(t *testing.T) {
	server := newTestSSHServer(t)
	pool, hops := newTestConnectionManager(t, server)
	server.setRejectForward(true)
	server.setSessionOutput("OCCUPIED", 0)
	testDB := db.NewTestDB(t)

	rf := NewRemoteListenToLocal("127.0.0.1:23456", "127.0.0.1:22",
		"deny-diag-test", testDB, pool, hops)
	err := rf.Start(context.Background())
	require.Error(t, err)
	assert.ErrorContains(t, err, "already in LISTEN state",
		"denied forward must carry the probe classification")
	assert.ErrorContains(t, err, "denied by peer")
	assert.ErrorContains(t, err, "failed to create remote listener",
		"caller prefix must stay verbatim for log greps")

	// Happy path: no probe session may be opened.
	server2 := newTestSSHServer(t)
	pool2, hops2 := newTestConnectionManager(t, server2)
	rf2 := NewRemoteListenToLocal("127.0.0.1:0", "127.0.0.1:22",
		"deny-diag-test-2", db.NewTestDB(t), pool2, hops2)
	require.NoError(t, rf2.Start(context.Background()))
	defer func() { _ = rf2.Stop() }()
	assert.Equal(t, 0, server2.sessionOpens(),
		"successful forward must not run the deny probe")
}

// TestRemoteListenToRemote_DenyPrefix pins the rlr side: the legacy prefix
// "failed to create listener" must survive verbatim alongside the
// classification (R2 N-P2-2 — prefix retention is load-bearing for log
// greps, so it gets its own assertion instead of relying on symmetry).
func TestRemoteListenToRemote_DenyPrefix(t *testing.T) {
	server := newTestSSHServer(t)
	pool, hops := newTestConnectionManager(t, server)
	server.setRejectForward(true)
	server.setSessionOutput("OCCUPIED", 0)

	inf := NewRemoteListenToRemote("127.0.0.1:23457", "testdouble", 23457, hops,
		"127.0.0.1:9999", "testdouble", 9999, hops,
		"deny-diag-rlr", db.NewTestDB(t), pool, 0)
	err := inf.Start(context.Background())
	require.Error(t, err)
	assert.ErrorContains(t, err, "failed to create listener",
		"rlr legacy prefix must stay verbatim")
	assert.ErrorContains(t, err, "already in LISTEN state")
	assert.ErrorContains(t, err, "denied by peer")
}

// TestRemoteListenToLocal_LeakHeal: a leaked remote listener on the same
// pooled session is retracted by cancel and the forward starts within the
// SAME Start attempt (the incident class, healed in one rebuild).
func TestRemoteListenToLocal_LeakHeal(t *testing.T) {
	server := newTestSSHServer(t) // 非 reject 模式；占用仿真生效
	server.setSessionOutput("OCCUPIED", 0)
	pool, hops := newTestConnectionManager(t, server)
	testDB := db.NewTestDB(t)

	sig := connection.ConnectionSignature{
		Username: hops[0].User,
		Hostname: hops[0].HostName,
		Port:     hops[0].Port,
	}
	pooled, err := pool.Acquire(t.Context(), sig, "leak-seed")
	require.NoError(t, err)
	// Seed the leak: bind the explicit port without ever cancelling
	// (lost cancel-tcpip-forward). The entry stays client-side until
	// we Close it below (R2 P0-2 sequencing). Seed and forward bind
	// share one free port — the testdouble denies only explicit binds.
	bind := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	leaked, err := pooled.Client.Listen("tcp", bind)
	require.NoError(t, err)

	rf := NewRemoteListenToLocal(bind, "127.0.0.1:22",
		"leak-heal-test", testDB, pool, hops)
	rf.AllowLeakRecovery = true
	require.NoError(t, rf.Start(t.Context()))

	// Heal assertions: one cancel request was sent; the only held forward
	// left is rf's own (the seed was retracted).
	assert.Equal(t, 1, server.cancelRequests())
	held := server.heldForwards()
	assert.Len(t, held, 1, "leaked listener must be retracted, rf's own remains")

	// R2 P0-2: close the seed's client-side entry BEFORE rf.Stop().
	// x/crypto forwardList.remove deletes the FIRST entry with a matching
	// addr; with the seed still present, rf.Stop()'s Close would remove the
	// seed's entry and orphan rf's accept channel (Stop hangs forever).
	// The cancel inside leaks back false (already retracted) — discard.
	_ = leaked.Close()
	require.NoError(t, rf.Stop())
}

func TestRemoteListenToLocal_LeakRecoveryOffByDefault(t *testing.T) {
	server := newTestSSHServer(t)
	server.setSessionOutput("OCCUPIED", 0)
	pool, hops := newTestConnectionManager(t, server)

	sig := connection.ConnectionSignature{
		Username: hops[0].User,
		Hostname: hops[0].HostName,
		Port:     hops[0].Port,
	}
	pooled, err := pool.Acquire(t.Context(), sig, "off-seed")
	require.NoError(t, err)
	bind := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	leaked, err := pooled.Client.Listen("tcp", bind)
	require.NoError(t, err)
	before := server.cancelRequests()

	rf := NewRemoteListenToLocal(bind, "127.0.0.1:22",
		"leak-off-test", db.NewTestDB(t), pool, hops)
	// 不置 AllowLeakRecovery：deny 自然发生（占用仿真），行为须与批次 1 一致
	err = rf.Start(t.Context())
	require.Error(t, err)
	assert.ErrorContains(t, err, "already in LISTEN state")
	assert.NotContains(t, err.Error(), "cancel-tcpip-forward")
	assert.Equal(t, before, server.cancelRequests(), "default path must not send cancel")

	_ = leaked.Close() // 同 R2 P0-2 收尾
}
