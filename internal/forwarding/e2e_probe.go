package forwarding

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// e2eProbeTimeout bounds one E2E liveness probe. Package var so tests can
// shrink it. Must stay above the remote `read -t 2` (2s) plus session
// overhead, or silent targets regress to unknown (WARN noise) instead of
// alive.
var e2eProbeTimeout = 5 * time.Second

// errE2EUnknown marks an inconclusive probe (exec failure, timeout,
// unreadable output): the monitor only warns and leaves state alone.
// E2E_SLOW is not unknown: a successful connect already proved the chain.
var errE2EUnknown = errors.New("e2e probe inconclusive")

// e2eProbeCmdTmpl reads one byte from the probed address through bash's
// /dev/tcp. Exit-code contract of `read` (measured): data → 0, EOF → 1,
// timeout → 142, redirect failure (connect refused) → 1. Single-segment form:
// no exec wrapper, so no shell-version-dependent exec-failure semantics; the
// redirect failure is what surfaces a refused listener as E2E_FAIL. %s are
// the probe host and port (port validated as an integer by parsePort).
const e2eProbeCmdTmpl = `bash -c 'read -t 2 -n1 c <> /dev/tcp/%s/%s 2>/dev/null && echo E2E_OPEN || { rc=$?; [ $rc -gt 128 ] && echo E2E_SLOW || echo E2E_FAIL; }'`

// RandomE2EProbeInterval generates a random E2E probe interval between 45-90
// seconds, preventing all forwards from probing simultaneously.
func RandomE2EProbeInterval() time.Duration {
	minSeconds := 45
	maxSeconds := 90
	randomSeconds := minSeconds + rand.Intn(maxSeconds-minSeconds+1)
	return time.Duration(randomSeconds) * time.Second
}

// probeHost returns the host to embed in a probe address: an empty host breaks
// the bash /dev/tcp//port form, and production binds are loopback-reachable.
// Applies to probe address construction only, never to the stored config.
func probeHost(addr string) (string, error) {
	port, err := parsePort(addr)
	if err != nil {
		return "", err
	}
	host := hostOf(addr)
	if host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("%s:%d", host, port), nil
}

// probeThroughTunnel execs the /dev/tcp probe on the remote end of client and
// classifies the verdict: nil = path alive, "e2e probe failed" error = path
// definitively dead, errE2EUnknown = inconclusive.
func probeThroughTunnel(client *ssh.Client, listenAddr string, timeout time.Duration) error {
	probeAddr, err := probeHost(listenAddr)
	if err != nil {
		return fmt.Errorf("%w: %v", errE2EUnknown, err)
	}
	host, port, _ := net.SplitHostPort(probeAddr)
	cmd := fmt.Sprintf(e2eProbeCmdTmpl, host, port)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	type probeResult struct {
		out string
		err error
	}
	done := make(chan probeResult, 1)
	go func() {
		session, err := client.NewSession()
		if err != nil {
			done <- probeResult{err: fmt.Errorf("open session: %w", err)}
			return
		}
		defer func() { _ = session.Close() }()
		out, err := session.CombinedOutput(cmd)
		done <- probeResult{out: string(out), err: err}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			return fmt.Errorf("%w: %v", errE2EUnknown, r.err)
		}
		out := strings.TrimSpace(r.out)
		switch {
		case strings.Contains(out, "E2E_OPEN"):
			return nil
		case strings.Contains(out, "E2E_FAIL"):
			return fmt.Errorf("e2e probe failed: %s", out)
		case strings.Contains(out, "E2E_SLOW"):
			// Connect succeeded but no unsolicited byte within 2s: silent
			// protocol (HTTP, mongo) waiting for the client to speak first.
			// The connect already proved the full chain - classify alive.
			return nil
		default:
			return fmt.Errorf("%w: unexpected probe output %q", errE2EUnknown, out)
		}
	case <-ctx.Done():
		return fmt.Errorf("%w: probe timed out", errE2EUnknown)
	}
}

// probeTargetThroughClient dials targetAddr through client (direct-tcpip) and
// classifies: nil = path alive, "e2e probe failed" error = path definitively
// dead, errE2EUnknown = inconclusive (policy refusal or timeout). DialContext
// owns the deadline-vs-connect race, so no hand-rolled goroutine here.
func probeTargetThroughClient(client *ssh.Client, targetAddr string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	conn, err := client.DialContext(ctx, "tcp", targetAddr)
	if err == nil {
		_ = conn.Close()
		return nil
	}
	if strings.Contains(err.Error(), "prohibited") || ctx.Err() != nil {
		return fmt.Errorf("%w: %v", errE2EUnknown, err)
	}
	return fmt.Errorf("e2e probe failed: %w", err)
}
