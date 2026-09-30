package forwarding

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// denyProbeTimeout bounds the remote port-state probe. Package var so tests
// can shrink it.
var denyProbeTimeout = 5 * time.Second

// denyProbeCmdTmpl classifies the remote port state with layered tools.
// Output contract: "OCCUPIED" = a LISTEN socket holds the port; empty output
// = free (only authoritative when /proc/net/tcp is readable); "UNKNOWN" =
// cannot tell (no ss/netstat match and no /proc). The single %d is the port,
// guaranteed to be an integer by the caller.
const denyProbeCmdTmpl = `p=%d; h=$(printf '%%04X' "$p"); ` +
	`ss -ltn 2>/dev/null | grep -qF ":$p " && { echo OCCUPIED; exit 0; }; ` +
	`netstat -ltn 2>/dev/null | grep -qF ":$p " && { echo OCCUPIED; exit 0; }; ` +
	`for f in /proc/net/tcp /proc/net/tcp6; do ` +
	`[ -r "$f" ] && grep -hiE ":$h +00000000:0000 +0A " "$f" 2>/dev/null | grep -q . && { echo OCCUPIED; exit 0; }; ` +
	`done; ` +
	`if [ -r /proc/net/tcp ] || [ -r /proc/net/tcp6 ]; then exit 0; fi; ` +
	`echo UNKNOWN`

// listenDeniedMarker mirrors x/crypto/ssh tcpip.go:138 — the only text a
// failed tcpip-forward reply surfaces through (*ssh.Client).Listen.
const listenDeniedMarker = "denied by peer"

// cancelUndeliveredWarn is logged when listener.Close() fails during cleanup:
// delivery of cancel-tcpip-forward could not be confirmed. If the connection
// is dead the remote sshd reclaims the listener on disconnect; if it is not,
// the listener may linger and same-port forward requests may be denied until
// the remote releases it.
const cancelUndeliveredWarn = "cancel-tcpip-forward delivery not confirmed; " +
	"same-port forward requests may be denied until the remote releases the listener"

// listenClassification is the internal outcome of the remote port probe.
type listenClassification int

const (
	listenOccupied listenClassification = iota
	listenPolicyFree
	listenUnknown
)

// annotateListenDenied enriches a denied tcpip-forward failure with a
// best-effort remote port-state probe so the operator can act on the error:
// a stale listener (restarting this daemon may free it) vs an sshd policy
// refusal. Non-denied causes pass through untouched. Must be called while
// the client is still acquired — before pool.Release.
//
// Callers pass one of two prefixes historically: "failed to create remote
// listener" (remote_listen_to_local) and "failed to create listener"
// (remote_listen_to_remote). The prefix travels with the caller's existing
// wording to keep log greps stable; this helper appends only the bracketed
// classification.
func annotateListenDenied(client *ssh.Client, bindAddr string, cause error) error {
	if !strings.Contains(cause.Error(), listenDeniedMarker) {
		return cause
	}

	detail, _ := probeDeniedPort(client, bindAddr)
	return fmt.Errorf("%w (%s)", cause, detail)
}

// probeDeniedPort runs the remote probe and returns the human-readable
// classification appended to the error, plus the machine-readable class the
// deny-recovery branch dispatches on.
func probeDeniedPort(client *ssh.Client, bindAddr string) (string, listenClassification) {
	port, err := parsePort(bindAddr)
	if err != nil {
		return fmt.Sprintf("could not verify remote port state: %v", err), listenUnknown
	}

	out, err := runRemoteProbe(client, fmt.Sprintf(denyProbeCmdTmpl, port))
	switch {
	case err != nil:
		return fmt.Sprintf("could not verify remote port state: %v", err), listenUnknown
	case strings.Contains(out, "OCCUPIED"):
		return fmt.Sprintf("remote port %s is already in LISTEN state — likely a stale listener "+
			"from a previous dead session (restarting this daemon may free it) or another service "+
			"on the remote host; SSH cannot release it", bindAddr), listenOccupied
	case strings.Contains(out, "UNKNOWN"):
		return "could not verify remote port state: remote host has no ss/netstat and no /proc", listenUnknown
	default:
		return fmt.Sprintf("remote port %s has no listener — the remote sshd is refusing "+
			"tcpip-forward, likely AllowTcpForwarding or PermitListen policy", bindAddr), listenPolicyFree
	}
}

// cancelRemoteListener asks the remote sshd to retract the tcpip-forward
// listener for bindAddr on THIS connection's session and reports whether it
// held one. bindAddr must be byte-identical to the string previously passed
// to Client.Listen: x/crypto sends the host unresolved and the sshd matches
// the cancel against the exact recorded address.
func cancelRemoteListener(client *ssh.Client, bindAddr string) (bool, error) {
	_, portStr, err := net.SplitHostPort(bindAddr)
	if err != nil {
		return false, fmt.Errorf("invalid bind address %q: %w", bindAddr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return false, fmt.Errorf("invalid port %q", portStr)
	}
	payload := ssh.Marshal(struct {
		Addr string
		Port uint32
	}{hostOf(bindAddr), uint32(port)})
	ok, _, err := client.SendRequest("cancel-tcpip-forward", true, payload)
	if err != nil {
		return false, fmt.Errorf("cancel-tcpip-forward %s: %w", bindAddr, err)
	}
	return ok, nil
}

// hostOf returns the host part of bindAddr verbatim (no normalization),
// mirroring what x/crypto's Listen puts on the wire.
func hostOf(bindAddr string) string {
	host, _, err := net.SplitHostPort(bindAddr)
	if err != nil {
		return ""
	}
	return host
}

// parsePort extracts and validates the port from a bind address. The value is
// interpolated into a remote shell command, so only clean integers pass.
func parsePort(bindAddr string) (int, error) {
	_, portStr, err := net.SplitHostPort(bindAddr)
	if err != nil {
		return 0, fmt.Errorf("invalid bind address %q: %w", bindAddr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid port %q", portStr)
	}
	return port, nil
}

// runRemoteProbe execs the probe command on the remote and returns its
// combined output, bounded by denyProbeTimeout. On timeout the abandoned
// goroutine stays alive until the connection closes; the buffered channel
// makes that leak-free.
func runRemoteProbe(client *ssh.Client, cmd string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), denyProbeTimeout)
	defer cancel()

	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		session, err := client.NewSession()
		if err != nil {
			done <- result{err: fmt.Errorf("open session: %w", err)}
			return
		}
		defer func() { _ = session.Close() }()
		out, err := session.CombinedOutput(cmd)
		done <- result{out: string(out), err: err}
	}()

	select {
	case r := <-done:
		return r.out, r.err
	case <-ctx.Done():
		return "", errors.New("probe timed out")
	}
}
