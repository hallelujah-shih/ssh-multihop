package forwarding

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/hallelujah-shih/ssh-multihop/internal/connection"
	"github.com/hallelujah-shih/ssh-multihop/internal/tunnel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

// Wire payloads per RFC4254 §7.2 (mirrors x/crypto/ssh tcpip.go structs).
type tcpipForwardReqMsg struct {
	Addr string
	Port uint32
}

type tcpipForwardReplyMsg struct {
	Port uint32
}

type forwardedTCPDataMsg struct {
	Addr       string
	Port       uint32
	OriginAddr string
	OriginPort uint32
}

// testSSHServer is an in-memory sshd testdouble: accepts any publickey,
// handles "tcpip-forward"/"cancel-tcpip-forward" global requests, and relays
// accepted connections back as "forwarded-tcpip" channels.
type testSSHServer struct {
	t        *testing.T
	listener net.Listener
	hostKey  ssh.Signer

	mu            sync.Mutex
	listeners     map[string]net.Listener
	rejectForward bool
	cancelCount   int

	sessionOutput string
	sessionRC     int
	sessionDelay  time.Duration
	sessionCount  int

	directTCPDialFail         bool
	directTCPRejectProhibited bool
	sessionRealExec           bool

	wg sync.WaitGroup
}

func newTestSSHServer(t *testing.T) *testSSHServer {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	// sessionOutput defaults to the healthy-world E2E probe verdict so every
	// forward started against this testdouble lives in a healthy probe world.
	s := &testSSHServer{
		t:             t,
		listener:      l,
		hostKey:       signer,
		listeners:     make(map[string]net.Listener),
		sessionOutput: "E2E_OPEN",
	}

	s.wg.Add(1)
	go s.acceptLoop()
	t.Cleanup(s.Close)
	return s
}

func (s *testSSHServer) port() int {
	return s.listener.Addr().(*net.TCPAddr).Port
}

func (s *testSSHServer) setRejectForward(reject bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejectForward = reject
}

func (s *testSSHServer) setSessionOutput(out string, rc int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionOutput, s.sessionRC = out, rc
}

func (s *testSSHServer) setSessionDelay(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionDelay = d
}

func (s *testSSHServer) setDirectTCPDialFail(b bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.directTCPDialFail = b
}

func (s *testSSHServer) setDirectTCPRejectProhibited(b bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.directTCPRejectProhibited = b
}

func (s *testSSHServer) setSessionRealExec(b bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionRealExec = b
}

func (s *testSSHServer) sessionOpens() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionCount
}

// heldForwards returns the keys of the remote listeners this server currently
// holds, sorted for stable comparison.
func (s *testSSHServer) heldForwards() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.listeners))
	for k := range s.listeners {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// cancelRequests counts cancel-tcpip-forward requests received, regardless
// of whether the reply was true or false.
func (s *testSSHServer) cancelRequests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancelCount
}

// forwardKey is the single key form shared by tcpip-forward accounting and
// cancel lookups: both must derive identical keys or cancels miss.
func forwardKey(addr string, port uint32) string {
	return fmt.Sprintf("%s:%d", addr, port)
}

func (s *testSSHServer) Close() {
	_ = s.listener.Close()

	s.mu.Lock()
	ls := make([]net.Listener, 0, len(s.listeners))
	for k, l := range s.listeners {
		ls = append(ls, l)
		delete(s.listeners, k)
	}
	s.mu.Unlock()
	for _, l := range ls {
		_ = l.Close()
	}

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		s.t.Log("testSSHServer: timed out waiting for accept loops")
	}
}

func (s *testSSHServer) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *testSSHServer) handleConn(conn net.Conn) {
	config := &ssh.ServerConfig{
		PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
			return &ssh.Permissions{}, nil
		},
	}
	config.AddHostKey(s.hostKey)

	srvConn, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		_ = conn.Close()
		return
	}
	defer func() { _ = srvConn.Close() }()

	go s.handleRequests(srvConn, reqs)
	for ch := range chans {
		switch ch.ChannelType() {
		case "session":
			s.handleSession(ch)
		case "direct-tcpip":
			s.handleDirectTCPIP(ch)
		default:
			// Unknown channel types are rejected, matching real sshd behavior.
			_ = ch.Reject(ssh.UnknownChannelType, "unexpected client channel")
		}
	}
}

func (s *testSSHServer) handleSession(ch ssh.NewChannel) {
	s.mu.Lock()
	s.sessionCount++
	out, rc, delay, realExec := s.sessionOutput, s.sessionRC, s.sessionDelay, s.sessionRealExec
	s.mu.Unlock()

	channel, reqs, err := ch.Accept()
	if err != nil {
		return
	}
	defer func() { _ = channel.Close() }()

	for req := range reqs {
		if req.Type != "exec" {
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
			continue
		}
		var p struct{ Command string }
		if err := ssh.Unmarshal(req.Payload, &p); err != nil {
			_ = req.Reply(false, nil)
			continue
		}
		if realExec {
			_ = req.Reply(true, nil)
			s.runRealExec(channel, p.Command)
			return
		}
		if delay > 0 {
			time.Sleep(delay)
		}
		_ = req.Reply(true, nil)
		if out != "" {
			_, _ = channel.Write([]byte(out))
		}
		_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(rc)}))
		return
	}
}

// runRealExec executes the command with the local shell and pipes output back
// over the channel, so tests can drive the real probe command end to end
// instead of a canned output that has no discriminating power over the
// command string.
func (s *testSSHServer) runRealExec(channel ssh.Channel, command string) {
	cmd := exec.Command("bash", "-c", command)
	cmd.Stdout = channel
	cmd.Stderr = channel
	rc := 0
	if err := cmd.Run(); err != nil {
		rc = 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() > 0 {
			rc = exitErr.ExitCode()
		}
	}
	_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(rc)}))
}

// directTCPIPMsg mirrors x/crypto/ssh's wire payload for direct-tcpip opens.
type directTCPIPMsg struct {
	Addr       string
	Port       uint32
	OriginAddr string
	OriginPort uint32
}

// handleDirectTCPIP accepts direct-tcpip opens, dials the requested address
// and relays both ways (healthy world). The fault switches emulate sshd-side
// outcomes: dial failure (ConnectionFailed reject) and PermitOpen policy
// refusal (Prohibited reject).
func (s *testSSHServer) handleDirectTCPIP(ch ssh.NewChannel) {
	s.mu.Lock()
	dialFail := s.directTCPDialFail
	prohibited := s.directTCPRejectProhibited
	s.mu.Unlock()

	if prohibited {
		_ = ch.Reject(ssh.Prohibited, "administratively prohibited")
		return
	}
	if dialFail {
		_ = ch.Reject(ssh.ConnectionFailed, "connect failed")
		return
	}

	var p directTCPIPMsg
	if err := ssh.Unmarshal(ch.ExtraData(), &p); err != nil {
		_ = ch.Reject(ssh.ConnectionFailed, "bad payload")
		return
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", p.Addr, p.Port), 2*time.Second)
	if err != nil {
		_ = ch.Reject(ssh.ConnectionFailed, "connect failed")
		return
	}
	channel, reqs, err := ch.Accept()
	if err != nil {
		_ = conn.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	go s.relay(channel, conn)
}

func (s *testSSHServer) handleRequests(srvConn *ssh.ServerConn, reqs <-chan *ssh.Request) {
	for req := range reqs {
		switch req.Type {
		case "tcpip-forward":
			s.handleTCPForward(srvConn, req)
		case "cancel-tcpip-forward":
			s.mu.Lock()
			s.cancelCount++
			s.mu.Unlock()
			var p tcpipForwardReqMsg
			if err := ssh.Unmarshal(req.Payload, &p); err != nil {
				if req.WantReply {
					_ = req.Reply(false, nil)
				}
				continue
			}
			// Real sshd semantics: a cancel only succeeds for a listener this
			// session actually holds; anything else answers false.
			key := forwardKey(p.Addr, p.Port)
			s.mu.Lock()
			l, ok := s.listeners[key]
			if ok {
				delete(s.listeners, key)
			}
			s.mu.Unlock()
			if ok {
				_ = l.Close()
			}
			if req.WantReply {
				_ = req.Reply(ok, nil)
			}
		default:
			// Covers keepalive@openssh.com (sent by the connection pool) etc.
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
		}
	}
}

func (s *testSSHServer) handleTCPForward(srvConn *ssh.ServerConn, req *ssh.Request) {
	var p tcpipForwardReqMsg
	if err := ssh.Unmarshal(req.Payload, &p); err != nil {
		if req.WantReply {
			_ = req.Reply(false, nil)
		}
		return
	}

	s.mu.Lock()
	rejected := s.rejectForward
	s.mu.Unlock()
	if rejected {
		if req.WantReply {
			_ = req.Reply(false, nil)
		}
		return
	}

	// Real sshd semantics: an explicit port (p.Port != 0) must bind the
	// requested address, and a bind failure (port occupied) is the deny
	// path. Port 0 keeps the old behavior: the kernel picks a port and the
	// allocated port is reported in the reply (x/crypto adopts the reply
	// port client-side only when it asked for 0).
	listenAddr := fmt.Sprintf("%s:%d", p.Addr, p.Port)
	l, err := net.Listen("tcp", listenAddr)
	if err != nil {
		if req.WantReply {
			_ = req.Reply(false, nil)
		}
		return
	}

	boundPort := uint32(l.Addr().(*net.TCPAddr).Port)
	// Record under the same key form cancel lookups use: for explicit ports
	// the bound port IS the requested port; for port 0 it is the allocated
	// port the reply reports (matching x/crypto's adopted entry).
	s.mu.Lock()
	s.listeners[forwardKey(p.Addr, boundPort)] = l
	s.mu.Unlock()

	if req.WantReply {
		_ = req.Reply(true, ssh.Marshal(tcpipForwardReplyMsg{Port: boundPort}))
	}

	s.wg.Add(1)
	go s.remoteAcceptLoop(srvConn, l, p.Addr, boundPort)
}

func (s *testSSHServer) remoteAcceptLoop(srvConn *ssh.ServerConn, l net.Listener, bindAddr string, boundPort uint32) {
	defer s.wg.Done()
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}

		originAddr, originPort := "127.0.0.1", uint32(0)
		if tcpAddr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
			originAddr, originPort = tcpAddr.IP.String(), uint32(tcpAddr.Port)
		}

		ch, chReqs, err := srvConn.OpenChannel("forwarded-tcpip", ssh.Marshal(forwardedTCPDataMsg{
			Addr:       bindAddr,
			Port:       boundPort,
			OriginAddr: originAddr,
			OriginPort: originPort,
		}))
		if err != nil {
			_ = conn.Close()
			continue
		}
		go ssh.DiscardRequests(chReqs)
		go s.relay(ch, conn)
	}
}

func (s *testSSHServer) relay(ch ssh.Channel, conn net.Conn) {
	defer func() {
		_ = ch.Close()
		_ = conn.Close()
	}()

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(ch, conn)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(conn, ch)
		done <- struct{}{}
	}()
	<-done
}

// newTestConnectionManager returns a pool whose hopConfigProvider dials the
// testdouble SSH server, exercising the production acquire path. The client
// authenticates with a throwaway key referenced via hop.IdentityFile.
func newTestConnectionManager(t *testing.T, server *testSSHServer) (*connection.ConnectionManager, []*tunnel.HopConfig) {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	keyFile := filepath.Join(t.TempDir(), "test_identity")
	pemBlock, err := ssh.MarshalPrivateKey(priv, "")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(pemBlock), 0600))

	builder := connection.NewSSHClientConfigBuilder()

	hops := []*tunnel.HopConfig{
		{
			Host:         "testdouble",
			User:         "test",
			HostName:     "127.0.0.1",
			Port:         server.port(),
			IdentityFile: keyFile,
		},
	}

	pool := connection.NewConnectionManager(connection.PoolConfig{
		IdleTimeout:        time.Second,
		MaxIdleConnections: 2,
		DialTimeout:        5 * time.Second,
	}, func(connection.ConnectionSignature) ([]*tunnel.HopConfig, *connection.SSHClientConfigBuilder, error) {
		return hops, builder, nil
	})
	t.Cleanup(func() { _ = pool.Close() })
	return pool, hops
}

// TestSSHServerSessionExec verifies the session/exec testdouble used by the
// deny-probe tests: exec returns the configured output and exit code.
func TestSSHServerSessionExec(t *testing.T) {
	server := newTestSSHServer(t)
	pool, hops := newTestConnectionManager(t, server)

	server.setSessionOutput("OCCUPIED", 0)

	sig := connection.ConnectionSignature{
		Username: hops[0].User,
		Hostname: hops[0].HostName,
		Port:     hops[0].Port,
	}
	pooled, err := pool.Acquire(context.Background(), sig, "harness-selftest")
	require.NoError(t, err)
	defer func() { _ = pool.Release(pooled, "harness-selftest") }()

	session, err := pooled.Client.NewSession()
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	out, err := session.CombinedOutput("probe-cmd")
	require.NoError(t, err)
	assert.Equal(t, "OCCUPIED", string(out))
	assert.Equal(t, 1, server.sessionOpens())
}
