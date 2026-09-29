package forwarding

import (
	"errors"
	"testing"
	"time"

	"github.com/hallelujah-shih/ssh-multihop/internal/connection"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errDeniedCause mirrors x/crypto/ssh tcpip.go:138 verbatim.
var errDeniedCause = errors.New("ssh: tcpip-forward request denied by peer")

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
