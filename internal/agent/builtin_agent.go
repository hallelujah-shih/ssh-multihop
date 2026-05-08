package agent

import (
	"context"
	"fmt"
	"os"
	"sync"

	xagent "github.com/xanzy/ssh-agent"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
	sshagent "golang.org/x/crypto/ssh/agent"
)

// BuiltInAgent manages a pure Go SSH agent for the application.
// It runs entirely in-memory without external processes, making it
// suitable for setuid/setgid scenarios.
type BuiltInAgent struct {
	mu           sync.Mutex
	goAgent      *GoAgent
	initialized  bool
	ctx          context.Context
	cancel       context.CancelFunc
	shutdownOnce sync.Once
}

// globalBuiltInAgent is the singleton instance
var globalBuiltInAgent *BuiltInAgent
var globalBuiltInAgentOnce sync.Once

// GetBuiltInAgent returns the singleton built-in agent instance,
// initializing it on first use.
func GetBuiltInAgent() (*BuiltInAgent, error) {
	var initErr error
	globalBuiltInAgentOnce.Do(func() {
		globalBuiltInAgent, initErr = newBuiltInAgent()
	})
	return globalBuiltInAgent, initErr
}

// newBuiltInAgent creates and initializes a built-in agent.
func newBuiltInAgent() (*BuiltInAgent, error) {
	ctx, cancel := context.WithCancel(context.Background())

	a := &BuiltInAgent{
		ctx:    ctx,
		cancel: cancel,
	}

	if err := a.init(); err != nil {
		return nil, err
	}

	return a, nil
}

// init initializes the built-in agent.
func (a *BuiltInAgent) init() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Create pure Go agent
	goAgent, err := NewGoAgent()
	if err != nil {
		return fmt.Errorf("failed to create Go agent: %w", err)
	}

	// Initialize: load keys and start socket server
	if err := goAgent.Initialize(); err != nil {
		return fmt.Errorf("failed to initialize Go agent: %w", err)
	}

	a.goAgent = goAgent
	a.initialized = true

	userCtx := goAgent.GetUserContext()
	zap.L().Info("In-memory SSH agent initialized",
		zap.String("user", userCtx.String()),
		zap.Int("keys_loaded", goAgent.GetKeyCount()),
		zap.String("type", "in-memory"),
		zap.Bool("setuid_mode", goAgent.IsSetUID()))

	return nil
}

// Stop stops the built-in agent and cleans up resources.
func (a *BuiltInAgent) Stop() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	var err error
	a.shutdownOnce.Do(func() {
		if !a.initialized {
			return
		}

		if a.goAgent != nil {
			if stopErr := a.goAgent.Stop(); stopErr != nil {
				err = stopErr
			}
		}

		a.initialized = false
		zap.L().Info("Stopped pure Go SSH agent")
	})

	return err
}

// GetSigners returns the signers from the agent.
func (a *BuiltInAgent) GetSigners() ([]ssh.Signer, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.initialized {
		return nil, fmt.Errorf("agent not initialized")
	}

	// Get the underlying agent
	ag := a.goAgent.GetAgent()
	return ag.Signers()
}

// IsAvailable returns true if the agent is available and has keys.
func (a *BuiltInAgent) IsAvailable() bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.initialized {
		return false
	}

	return a.goAgent.IsAvailable()
}

// GetSocketPath returns the agent socket path.
func (a *BuiltInAgent) GetSocketPath() string {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.goAgent != nil {
		return a.goAgent.GetSocketPath()
	}
	return ""
}

// GetKeyCount returns the number of keys loaded.
func (a *BuiltInAgent) GetKeyCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.goAgent != nil {
		return a.goAgent.GetKeyCount()
	}
	return 0
}

// GetAgent returns the underlying agent for advanced usage.
func (a *BuiltInAgent) GetAgent() sshagent.Agent {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.goAgent != nil {
		return a.goAgent.GetAgent()
	}
	return nil
}

// CleanupOrphanedAgents is a no-op for pure Go agent.
// The old process-based agent needed this, but the pure Go agent
// doesn't leave orphaned processes.
func CleanupOrphanedAgents() error {
	// No-op for pure Go agent
	return nil
}

// CheckExternalAgent validates SSH_AUTH_SOCK and returns information about it.
// Returns (socketPath, keyCount, nil) if SSH_AUTH_SOCK is valid and has keys.
// Returns ("", 0, nil) if SSH_AUTH_SOCK is not set or invalid (not an error).
// Returns ("", 0, error) if there's an error checking the agent.
func CheckExternalAgent() (string, int, error) {
	socketPath := os.Getenv("SSH_AUTH_SOCK")
	if socketPath == "" {
		return "", 0, nil // No agent configured, not an error
	}

	// Verify socket exists and is accessible
	fi, err := os.Stat(socketPath)
	if err != nil {
		zap.L().Debug("SSH_AUTH_SOCK exists but socket is not accessible",
			zap.String("socket", socketPath),
			zap.Error(err))
		return "", 0, nil
	}

	// Verify it's a socket
	if fi.Mode()&os.ModeSocket == 0 {
		zap.L().Debug("SSH_AUTH_SOCK is not a socket",
			zap.String("socket", socketPath),
			zap.String("mode", fi.Mode().String()))
		return "", 0, nil
	}

	// Connect to the external agent and verify it has keys
	sshAgent, _, err := xagent.New()
	if err != nil {
		zap.L().Warn("Failed to connect to SSH agent from SSH_AUTH_SOCK",
			zap.String("socket", socketPath),
			zap.Error(err))
		return "", 0, nil
	}

	// Check if agent has any keys
	signers, err := sshAgent.Signers()
	if err != nil {
		zap.L().Warn("Failed to get signers from SSH agent",
			zap.String("socket", socketPath),
			zap.Error(err))
		return "", 0, nil
	}

	if len(signers) == 0 {
		zap.L().Debug("SSH agent from SSH_AUTH_SOCK has no keys",
			zap.String("socket", socketPath))
		return "", 0, nil
	}

	zap.L().Debug("Auto-detected SSH agent from SSH_AUTH_SOCK",
		zap.String("socket", socketPath),
		zap.Int("keys_available", len(signers)))

	return socketPath, len(signers), nil
}
