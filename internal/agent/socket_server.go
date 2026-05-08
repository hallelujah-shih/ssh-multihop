package agent

import (
	"context"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"

	"go.uber.org/zap"
	sshagent "golang.org/x/crypto/ssh/agent"
)

// SocketServer manages the Unix domain socket for the SSH agent.
type SocketServer struct {
	agent      sshagent.Agent
	socketPath string
	listener   net.Listener
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
}

// NewSocketServer creates a new socket server.
func NewSocketServer(ag sshagent.Agent, socketPath string) *SocketServer {
	return &SocketServer{
		agent:      ag,
		socketPath: socketPath,
		done:       make(chan struct{}),
	}
}

// Start starts the socket server.
func (s *SocketServer) Start() error {
	// Remove existing socket file if it exists
	if err := os.Remove(s.socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove existing socket: %w", err)
	}

	// Create Unix domain socket listener
	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on socket: %w", err)
	}

	// Set socket permissions to user-only
	if err := os.Chmod(s.socketPath, 0600); err != nil {
		_ = listener.Close()
		return fmt.Errorf("failed to set socket permissions: %w", err)
	}

	s.listener = listener
	s.ctx, s.cancel = context.WithCancel(context.Background())

	// Start accepting connections in background
	go s.acceptConnections()

	zap.L().Info("SSH agent socket server started",
		zap.String("socket", s.socketPath))

	return nil
}

// Stop stops the socket server.
func (s *SocketServer) Stop() error {
	if s.cancel != nil {
		s.cancel()
	}

	if s.listener != nil {
		if err := s.listener.Close(); err != nil {
			return fmt.Errorf("failed to close listener: %w", err)
		}
	}

	// Wait for accept loop to finish
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		zap.L().Warn("Timeout waiting for socket server to stop")
	}

	// Remove socket file
	if err := os.Remove(s.socketPath); err != nil && !os.IsNotExist(err) {
		zap.L().Warn("Failed to remove socket file",
			zap.String("socket", s.socketPath),
			zap.Error(err))
	}

	zap.L().Info("SSH agent socket server stopped")
	return nil
}

// acceptConnections accepts connections from clients.
func (s *SocketServer) acceptConnections() {
	defer close(s.done)

	for {
		select {
		case <-s.ctx.Done():
			zap.L().Debug("Socket server accept loop stopped")
			return
		default:
			// Set accept deadline to allow checking context periodically
			conn, err := s.listener.Accept()
			if err != nil {
				// Check if we're shutting down
				select {
				case <-s.ctx.Done():
					return
				default:
					if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
						continue
					}
					if isConnectionRefused(err) {
						// Connection refused is normal during shutdown
						continue
					}
					zap.L().Warn("Failed to accept connection",
						zap.Error(err))
					continue
				}
			}

			// Handle connection in background
			go s.handleConnection(conn)
		}
	}
}

// handleConnection handles a single client connection.
func (s *SocketServer) handleConnection(conn net.Conn) {
	defer func() {
		if err := conn.Close(); err != nil {
			zap.L().Debug("Error closing agent connection", zap.Error(err))
		}
	}()

	// Serve the agent protocol on this connection
	// The connection will be closed when the client disconnects
	// or an error occurs
	if err := Serve(s.agent, conn); err != nil {
		if err.Error() != "EOF" {
			zap.L().Debug("Agent connection error",
				zap.String("remote_addr", conn.RemoteAddr().String()),
				zap.Error(err))
		}
	}
}

// isConnectionRefused checks if an error is a connection refused error.
func isConnectionRefused(err error) bool {
	if err == nil {
		return false
	}

	if sysErr, ok := err.(*os.SyscallError); ok {
		if sysErr.Err == syscall.ECONNREFUSED {
			return true
		}
	}

	return false
}

// GetSocketPath returns the socket path.
func (s *SocketServer) GetSocketPath() string {
	return s.socketPath
}
