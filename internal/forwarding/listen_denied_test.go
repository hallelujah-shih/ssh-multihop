package forwarding

import (
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/hallelujah-shih/ssh-multihop/internal/connection"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errDeniedCause mirrors x/crypto/ssh tcpip.go:138 verbatim.
var errDeniedCause = errors.New("ssh: tcpip-forward request denied by peer")

// freePort returns an OS-assigned free TCP port so tests never race on
// fixed port numbers.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// newProbeClient returns a pooled connection to a testdouble configured to
// reject tcpip-forward and answer exec with the given output/rc. The server
// is returned so tests can assert sessionOpens().
func newProbeClient(t *testing.T, out string, rc int) (*connection.PooledConnection, *testSSHServer) {
	server := newTestSSHServer(t)
	pool, hops := newTestConnectionManager(t, server)
	server.setRejectForward(true)
	server.setSessionOutput(out, rc)

	sig := connection.ConnectionSignature{
		Username: hops[0].User,
		Hostname: hops[0].HostName,
		Port:     hops[0].Port,
	}
	pooled, err := pool.Acquire(t.Context(), sig, "probe-test")
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Release(pooled, "probe-test") })
	return pooled, server
}

func TestAnnotateListenDenied_Occupied(t *testing.T) {
	pooled, _ := newProbeClient(t, "OCCUPIED", 0)
	err := annotateListenDenied(pooled.Client, "127.0.0.1:2222", errDeniedCause)
	require.Error(t, err)
	assert.ErrorContains(t, err, "denied by peer")
	assert.ErrorContains(t, err, "remote port 127.0.0.1:2222 is already in LISTEN state")
	assert.ErrorContains(t, err, "stale listener")
}

func TestAnnotateListenDenied_Free(t *testing.T) {
	pooled, _ := newProbeClient(t, "", 0)
	err := annotateListenDenied(pooled.Client, "127.0.0.1:2222", errDeniedCause)
	require.Error(t, err)
	assert.ErrorContains(t, err, "denied by peer")
	assert.ErrorContains(t, err, "no listener")
	assert.ErrorContains(t, err, "AllowTcpForwarding")
	assert.NotContains(t, err.Error(), "already in LISTEN state")
}

func TestAnnotateListenDenied_UnknownMarker(t *testing.T) {
	// macOS/BSD remotes lack /proc: the probe must land on unknown, not free.
	pooled, _ := newProbeClient(t, "UNKNOWN", 0)
	err := annotateListenDenied(pooled.Client, "127.0.0.1:2222", errDeniedCause)
	require.Error(t, err)
	assert.ErrorContains(t, err, "could not verify remote port state")
	assert.ErrorContains(t, err, "denied by peer")
}

func TestAnnotateListenDenied_ProbeError(t *testing.T) {
	pooled, _ := newProbeClient(t, "", 1) // rc=1: remote command exits non-zero (ExitError path)
	err := annotateListenDenied(pooled.Client, "127.0.0.1:2222", errDeniedCause)
	require.Error(t, err)
	assert.ErrorContains(t, err, "could not verify remote port state")
}

func TestAnnotateListenDenied_ProbeTimeout(t *testing.T) {
	server := newTestSSHServer(t)
	pool, hops := newTestConnectionManager(t, server)
	server.setRejectForward(true)
	server.setSessionDelay(200 * time.Millisecond)

	orig := denyProbeTimeout
	denyProbeTimeout = 50 * time.Millisecond
	t.Cleanup(func() { denyProbeTimeout = orig })

	sig := connection.ConnectionSignature{
		Username: hops[0].User,
		Hostname: hops[0].HostName,
		Port:     hops[0].Port,
	}
	pooled, err := pool.Acquire(t.Context(), sig, "probe-test")
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Release(pooled, "probe-test") })

	start := time.Now()
	err = annotateListenDenied(pooled.Client, "127.0.0.1:2222", errDeniedCause)
	require.Error(t, err)
	assert.Less(t, time.Since(start), 2*time.Second, "probe must be bounded by denyProbeTimeout")
	assert.ErrorContains(t, err, "could not verify remote port state")
}

func TestAnnotateListenDenied_BadAddr(t *testing.T) {
	pooled, server := newProbeClient(t, "OCCUPIED", 0)
	err := annotateListenDenied(pooled.Client, "no-port-here", errDeniedCause)
	require.Error(t, err)
	assert.ErrorContains(t, err, "could not verify remote port state")
	assert.ErrorContains(t, err, "denied by peer")
	// Bad addr must not run the probe at all.
	assert.Equal(t, 0, server.sessionOpens(), "parse failure must not open a probe session")
}

func TestAnnotateListenDenied_NotDeniedCauseUntouched(t *testing.T) {
	pooled, server := newProbeClient(t, "OCCUPIED", 0)
	cause := errors.New("dial tcp: connection refused")
	err := annotateListenDenied(pooled.Client, "127.0.0.1:2222", cause)
	assert.Equal(t, cause, err, "non-denied causes must pass through untouched")
	assert.Equal(t, 0, server.sessionOpens(), "non-denied causes must not open a probe session")
}

func TestProbeDeniedPortClassification(t *testing.T) {
	// 直接测 probeDeniedPort 的分类面（occupied/free/unknown/错误四态），
	// 经 annotateListenDenied 的 Detail 路径间接断言会破坏既有测试，
	// 因此本测走内部函数；文案断言沿用既有 7 测，不在此重复。
	pooled, server := newProbeClient(t, "OCCUPIED", 0)
	_, cls := probeDeniedPort(pooled.Client, "127.0.0.1:2222")
	assert.Equal(t, listenOccupied, cls)

	pooled2, _ := newProbeClient(t, "", 0)
	_, cls = probeDeniedPort(pooled2.Client, "127.0.0.1:2222")
	assert.Equal(t, listenPolicyFree, cls)

	pooled3, _ := newProbeClient(t, "UNKNOWN", 0)
	_, cls = probeDeniedPort(pooled3.Client, "127.0.0.1:2222")
	assert.Equal(t, listenUnknown, cls)

	pooled4, _ := newProbeClient(t, "", 1)
	_, cls = probeDeniedPort(pooled4.Client, "127.0.0.1:2222")
	assert.Equal(t, listenUnknown, cls)
	_ = server
}

func TestCancelRemoteListener(t *testing.T) {
	server := newTestSSHServer(t)
	pool, hops := newTestConnectionManager(t, server)

	sig := connection.ConnectionSignature{
		Username: hops[0].User,
		Hostname: hops[0].HostName,
		Port:     hops[0].Port,
	}
	pooled, err := pool.Acquire(t.Context(), sig, "cancel-test")
	require.NoError(t, err)
	defer func() { _ = pool.Release(pooled, "cancel-test") }()

	// Held forward: cancel retracts it and reports released.
	bind := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	ln, err := pooled.Client.Listen("tcp", bind)
	require.NoError(t, err)
	require.Len(t, server.heldForwards(), 1)

	released, err := cancelRemoteListener(pooled.Client, bind)
	require.NoError(t, err)
	assert.True(t, released, "held forward must be released")
	assert.Empty(t, server.heldForwards())
	// R1 P0-2: ln.Close() now re-sends cancel, remote no longer holds it,
	// and x/crypto surfaces the false reply as an error — accept either the
	// specific error or nil, but do NOT require nil.
	_ = ln.Close()

	// Not-held bind answers false without error; request still counted.
	before := server.cancelRequests()
	released, err = cancelRemoteListener(pooled.Client, fmt.Sprintf("127.0.0.1:%d", freePort(t)))
	require.NoError(t, err)
	assert.False(t, released, "unknown bind must answer false, not error")
	assert.Equal(t, before+1, server.cancelRequests())

	// Wildcard form round-trip: Listen(":0") sends Addr="" — cancel must use
	// the SAME string Listen got (R1 P0-3), so we cancel by the original
	// literal, not by ln.Addr().
	ln2, err := pooled.Client.Listen("tcp", ":0")
	require.NoError(t, err)
	require.Len(t, server.heldForwards(), 1)
	wildcardPort := ln2.Addr().(*net.TCPAddr).Port
	released, err = cancelRemoteListener(pooled.Client, fmt.Sprintf(":%d", wildcardPort))
	require.NoError(t, err)
	assert.True(t, released, "wildcard-form bind must match by identical string")
	_ = ln2.Close()
}

func TestCancelRemoteListener_BadAddr(t *testing.T) {
	pooled, _ := newProbeClient(t, "", 0)
	_, err := cancelRemoteListener(pooled.Client, "no-port-here")
	require.Error(t, err, "invalid bind must be rejected before any request")
}
