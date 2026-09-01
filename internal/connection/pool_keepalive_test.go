package connection

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// startKeepaliveTestServer starts an in-process SSH server.
// zombie=true: completes handshake then never replies to anything (simulates
// a NAT-dropped half-open connection). zombie=false: replies to global
// requests automatically (x/crypto answers unknown want-reply requests with
// a failure, which still proves liveness).
func startKeepaliveTestServer(t *testing.T, zombie bool) string {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}

	config := &ssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				sconn, chans, reqs, err := ssh.NewServerConn(c, config)
				if err != nil {
					_ = c.Close()
					return
				}
				if zombie {
					// Hold the connection open without serving anything.
					select {}
				}
				go ssh.DiscardRequests(reqs)
				for newCh := range chans {
					_ = newCh.Reject(ssh.UnknownChannelType, "unsupported")
				}
				_ = sconn.Close()
			}(c)
		}
	}()

	return ln.Addr().String()
}

func newKeepalivePoolConn(t *testing.T, cm *ConnectionManager, client *ssh.Client) *PooledConnection {
	t.Helper()
	sig := ConnectionSignature{Username: "test", Hostname: "keepalive-test", Port: 22}
	conn := &PooledConnection{
		Client:     client,
		Signature:  sig,
		CreatedAt:  time.Now(),
		LastUsedAt: time.Now(),
		Status:     StatusActive,
		RefCount:   1,
	}
	ctx, cancel := context.WithCancel(context.Background())
	conn.Context = ctx
	conn.CancelFunc = cancel
	cm.mu.Lock()
	cm.pools[sig.Hash()] = conn
	cm.mu.Unlock()
	t.Cleanup(func() { cancel(); _ = client.Close() })
	return conn
}

func TestKeepalive_EvictsZombieConnection(t *testing.T) {
	addr := startKeepaliveTestServer(t, true)
	client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            "test",
		Auth:            []ssh.AuthMethod{ssh.Password("x")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial test server: %v", err)
	}

	cm := NewConnectionManager(DefaultConfig(), nil)
	cm.keepaliveInterval = 30 * time.Millisecond
	cm.keepaliveTimeout = 100 * time.Millisecond
	conn := newKeepalivePoolConn(t, cm, client)
	cm.startKeepalive(conn)

	// Probe interval 30ms + timeout 100ms; allow generous margin.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if conn.GetStatus() == StatusClosed {
			cm.mu.RLock()
			_, inPool := cm.pools[conn.Signature.Hash()]
			cm.mu.RUnlock()
			if inPool {
				t.Fatal("connection closed but still in pool")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("zombie connection was not evicted within deadline")
}

func TestKeepalive_HealthyConnectionSurvives(t *testing.T) {
	addr := startKeepaliveTestServer(t, false)
	client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            "test",
		Auth:            []ssh.AuthMethod{ssh.Password("x")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial test server: %v", err)
	}

	cm := NewConnectionManager(DefaultConfig(), nil)
	cm.keepaliveInterval = 30 * time.Millisecond
	cm.keepaliveTimeout = 2 * time.Second
	conn := newKeepalivePoolConn(t, cm, client)
	cm.startKeepalive(conn)

	// Run several probe cycles; connection must stay alive and pooled.
	time.Sleep(300 * time.Millisecond)
	if conn.GetStatus() == StatusClosed {
		t.Fatal("healthy connection was evicted")
	}
	cm.mu.RLock()
	_, inPool := cm.pools[conn.Signature.Hash()]
	cm.mu.RUnlock()
	if !inPool {
		t.Fatal("healthy connection was removed from pool")
	}
}
