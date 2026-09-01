package connection

import (
	"crypto/ed25519"
	"crypto/rand"
	stdpem "encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/hallelujah-shih/ssh-multihop/internal/tunnel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

// TestEstablish_StalledServerTimesOut reproduces the production hang: the
// server accepts the TCP connection but never sends an SSH banner. The
// handshake must fail within defaultTimeout instead of blocking forever
// (which previously wedged pendingStarts and killed all rebuild retries).
func TestEstablish_StalledServerTimesOut(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer func() { _ = c.Close() }() // stall: never write anything
		}
	}()

	old := defaultTimeout
	defaultTimeout = 500 * time.Millisecond
	defer func() { defaultTimeout = old }()

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)

	hop := &tunnel.HopConfig{
		Host:         "stall",
		HostName:     "127.0.0.1",
		Port:         port,
		User:         "test",
		IdentityFile: writeTestKey(t),
	}

	done := make(chan error, 1)
	go func() {
		_, _, err := Establish([]*tunnel.HopConfig{hop}, NewSSHClientConfigBuilder())
		done <- err
	}()

	select {
	case err := <-done:
		assert.ErrorContains(t, err, "handshake")
	case <-time.After(5 * time.Second):
		t.Fatal("Establish hung on stalled server handshake")
	}
}

func writeTestKey(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	pemBlock, err := ssh.MarshalPrivateKey(priv, "")
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "id_ed25519")
	require.NoError(t, os.WriteFile(path, stdpem.EncodeToMemory(pemBlock), 0600))
	return path
}
