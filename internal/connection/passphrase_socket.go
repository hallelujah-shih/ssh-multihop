package connection

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// PassphraseSocket manages a Unix domain socket for receiving SSH key passphrases
type PassphraseSocket struct {
	socketPath  string
	listener    net.Listener
	passphrases map[string]string // fingerprint -> passphrase
	mu          sync.RWMutex
	quit        chan struct{}
}

// NewPassphraseSocket creates a new passphrase socket server
func NewPassphraseSocket(socketPath string) *PassphraseSocket {
	return &PassphraseSocket{
		socketPath:  socketPath,
		passphrases: make(map[string]string),
		quit:        make(chan struct{}),
	}
}

// Start starts the passphrase socket server
func (ps *PassphraseSocket) Start() error {
	// Remove existing socket if present
	_ = os.RemoveAll(ps.socketPath)

	listener, err := net.Listen("unix", ps.socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on passphrase socket: %w", err)
	}

	// Set restrictive permissions (0600 - owner read/write only)
	if err := os.Chmod(ps.socketPath, 0600); err != nil {
		_ = listener.Close()
		return fmt.Errorf("failed to set socket permissions: %w", err)
	}

	ps.listener = listener

	go ps.serve()
	return nil
}

// serve handles incoming passphrase connections
func (ps *PassphraseSocket) serve() {
	for {
		select {
		case <-ps.quit:
			return
		default:
			conn, err := ps.listener.Accept()
			if err != nil {
				select {
				case <-ps.quit:
					return
				default:
					zap.L().Warn("Error accepting passphrase connection",
						zap.Error(err))
					continue
				}
			}

			go ps.handleConnection(conn)
		}
	}
}

// handleConnection handles a single passphrase connection
func (ps *PassphraseSocket) handleConnection(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	// Set read timeout (30 seconds)
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	// Read line: "<fingerprint> <passphrase>"
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		zap.L().Debug("Failed to read from passphrase connection")
		return
	}

	line := scanner.Text()

	// Parse fingerprint and passphrase
	parts := strings.SplitN(line, " ", 2)
	if len(parts) != 2 {
		_, _ = fmt.Fprintln(conn, "ERROR invalid format")
		zap.L().Debug("Invalid passphrase format",
			zap.String("line", line))
		return
	}

	fingerprint, passphrase := parts[0], parts[1]

	if fingerprint == "" || passphrase == "" {
		_, _ = fmt.Fprintln(conn, "ERROR empty fingerprint or passphrase")
		return
	}

	// Store passphrase
	ps.mu.Lock()
	ps.passphrases[fingerprint] = passphrase
	ps.mu.Unlock()

	// Log access for audit
	zap.L().Info("Passphrase received via socket",
		zap.String("fingerprint", fingerprint),
		zap.String("remote_addr", conn.RemoteAddr().String()))

	_, _ = fmt.Fprintln(conn, "OK")
}

// GetPassphrase retrieves a passphrase for a key fingerprint
func (ps *PassphraseSocket) GetPassphrase(fingerprint string) (string, bool) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	passphrase, ok := ps.passphrases[fingerprint]
	return passphrase, ok
}

// Stop stops the passphrase socket server
func (ps *PassphraseSocket) Stop() {
	close(ps.quit)
	if ps.listener != nil {
		_ = ps.listener.Close()
	}
	_ = os.RemoveAll(ps.socketPath)
	zap.L().Info("Passphrase socket stopped",
		zap.String("socket", ps.socketPath))
}
