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
	"github.com/hallelujah-shih/ssh-multihop/internal/tunnel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// acquireTestClient pulls one pooled SSH client from the pool for direct
// probe calls (white-box: same package as production code).
func acquireTestClient(t *testing.T, pool *connection.ConnectionManager, hops []*tunnel.HopConfig) *connection.PooledConnection {
	t.Helper()
	sig := connection.ConnectionSignature{
		Username: hops[0].User,
		Hostname: hops[0].HostName,
		Port:     hops[0].Port,
	}
	pooled, err := pool.Acquire(context.Background(), sig, "e2e-probe-test")
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Release(pooled, "e2e-probe-test") })
	return pooled
}

// shrinkE2EProbeTimeout swaps the package probe timeout for the test duration.
func shrinkE2EProbeTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	orig := e2eProbeTimeout
	e2eProbeTimeout = d
	t.Cleanup(func() { e2eProbeTimeout = orig })
}

// startBannerService starts a target that writes a byte first (e.g. an SSH
// server sending its banner) and holds the connection until the peer closes.
func startBannerService(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				_, _ = c.Write([]byte("B"))
				_, _ = io.Copy(io.Discard, c)
			}(conn)
		}
	}()
	t.Cleanup(func() { _ = l.Close() })
	return l
}

// startSilentService starts a target that accepts but never writes (e.g. a
// daemon waiting for a request), so a reader sees neither data nor EOF.
func startSilentService(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(io.Discard, c)
			}(conn)
		}
	}()
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func TestProbeThroughTunnel_OpenCannedOutput(t *testing.T) {
	server := newTestSSHServer(t)
	server.setSessionOutput("E2E_OPEN", 0)
	pool, hops := newTestConnectionManager(t, server)
	pooled := acquireTestClient(t, pool, hops)

	err := probeThroughTunnel(pooled.Client, "127.0.0.1:8888", 500*time.Millisecond)
	require.NoError(t, err)
}

func TestProbeThroughTunnel_FailCannedOutput(t *testing.T) {
	server := newTestSSHServer(t)
	server.setSessionOutput("E2E_FAIL", 0)
	pool, hops := newTestConnectionManager(t, server)
	pooled := acquireTestClient(t, pool, hops)

	err := probeThroughTunnel(pooled.Client, "127.0.0.1:8888", 500*time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "e2e probe failed")
	assert.NotErrorIs(t, err, errE2EUnknown, "E2E_FAIL is a definitive death, not unknown")
}

func TestProbeThroughTunnel_TimeoutYieldsUnknown(t *testing.T) {
	server := newTestSSHServer(t)
	server.setSessionDelay(1 * time.Second)
	pool, hops := newTestConnectionManager(t, server)
	pooled := acquireTestClient(t, pool, hops)

	err := probeThroughTunnel(pooled.Client, "127.0.0.1:8888", 50*time.Millisecond)
	require.ErrorIs(t, err, errE2EUnknown)
}

func TestProbeThroughTunnel_SlowCannedOutputYieldsUnknown(t *testing.T) {
	server := newTestSSHServer(t)
	server.setSessionOutput("E2E_SLOW", 0)
	pool, hops := newTestConnectionManager(t, server)
	pooled := acquireTestClient(t, pool, hops)

	err := probeThroughTunnel(pooled.Client, "127.0.0.1:8888", 500*time.Millisecond)
	require.ErrorIs(t, err, errE2EUnknown, "E2E_SLOW must classify as unknown, never as death")
	assert.NotContains(t, err.Error(), "e2e probe failed")
}

func TestProbeTargetThroughClient_RelayOpen(t *testing.T) {
	server := newTestSSHServer(t)
	pool, hops := newTestConnectionManager(t, server)
	pooled := acquireTestClient(t, pool, hops)

	target := startEchoService(t)
	err := probeTargetThroughClient(pooled.Client, target.Addr().String(), 500*time.Millisecond)
	require.NoError(t, err)
}

func TestProbeTargetThroughClient_DialFailDies(t *testing.T) {
	server := newTestSSHServer(t)
	server.setDirectTCPDialFail(true)
	pool, hops := newTestConnectionManager(t, server)
	pooled := acquireTestClient(t, pool, hops)

	err := probeTargetThroughClient(pooled.Client, "127.0.0.1:9999", 500*time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "e2e probe failed")
	assert.NotErrorIs(t, err, errE2EUnknown, "connect-failed is a definitive death, not unknown")
}

func TestProbeTargetThroughClient_ProhibitedYieldsUnknown(t *testing.T) {
	server := newTestSSHServer(t)
	server.setDirectTCPRejectProhibited(true)
	pool, hops := newTestConnectionManager(t, server)
	pooled := acquireTestClient(t, pool, hops)

	err := probeTargetThroughClient(pooled.Client, "127.0.0.1:9999", 500*time.Millisecond)
	require.ErrorIs(t, err, errE2EUnknown, "policy refusal is unknown, never a death")
}

// TestE2EProbeRealPath drives the REAL probe command through the full chain:
// harness listener → forward accept loop → target, via the real-exec session
// mode. Canned output has zero discriminating power over the command string;
// this is the probe command's only behavioral guardrail.
func TestE2EProbeRealPath(t *testing.T) {
	server := newTestSSHServer(t)
	server.setSessionRealExec(true)
	pool, hops := newTestConnectionManager(t, server)
	testDB := db.NewTestDB(t)

	bannerL := startBannerService(t)
	silentL := startSilentService(t)
	deadPort := freePort(t)

	cases := []struct {
		name   string
		target string
		want   func(*testing.T, error)
	}{
		{"banner target opens", bannerL.Addr().String(), func(t *testing.T, err error) {
			require.NoError(t, err)
		}},
		{"dead target dies", fmt.Sprintf("127.0.0.1:%d", deadPort), func(t *testing.T, err error) {
			require.Error(t, err)
			assert.Contains(t, err.Error(), "e2e probe failed")
			assert.NotErrorIs(t, err, errE2EUnknown)
		}},
		{"silent target unknown", silentL.Addr().String(), func(t *testing.T, err error) {
			require.ErrorIs(t, err, errE2EUnknown)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fwd := NewRemoteListenToLocal("127.0.0.1:0", tc.target, "e2e-real-"+tc.name, testDB, pool, hops)
			require.NoError(t, fwd.Start(t.Context()))
			defer func() { _ = fwd.Stop() }()

			err := probeThroughTunnel(fwd.pooledConn.Client, fwd.listener.Addr().String(), 250*time.Millisecond)
			tc.want(t, err)
		})
	}
}
