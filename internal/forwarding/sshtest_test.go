package forwarding

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hallelujah-shih/ssh-multihop/internal/connection"
	"github.com/hallelujah-shih/ssh-multihop/internal/tunnel"
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

	s := &testSSHServer{
		t:         t,
		listener:  l,
		hostKey:   signer,
		listeners: make(map[string]net.Listener),
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
	for range chans {
		// No channel opens are expected from the client side of an -R forward.
	}
}

func (s *testSSHServer) handleRequests(srvConn *ssh.ServerConn, reqs <-chan *ssh.Request) {
	for req := range reqs {
		switch req.Type {
		case "tcpip-forward":
			s.handleTCPForward(srvConn, req)
		case "cancel-tcpip-forward":
			var p tcpipForwardReqMsg
			if err := ssh.Unmarshal(req.Payload, &p); err != nil {
				if req.WantReply {
					_ = req.Reply(false, nil)
				}
				continue
			}
			key := fmt.Sprintf("%s:%d", p.Addr, p.Port)
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
				_ = req.Reply(true, nil)
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

	// Always bind 127.0.0.1:0; let the kernel pick an ephemeral port.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if req.WantReply {
			_ = req.Reply(false, nil)
		}
		return
	}

	boundPort := uint32(l.Addr().(*net.TCPAddr).Port)
	// Key by the bound port: x/crypto sends cancel-tcpip-forward with the
	// allocated port (from the reply), not the requested one (real sshd does the same).
	s.mu.Lock()
	s.listeners[fmt.Sprintf("%s:%d", p.Addr, boundPort)] = l
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
