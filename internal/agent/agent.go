package agent

import (
	"fmt"
	"os"

	"github.com/hallelujah-shih/ssh-multihop/internal/config"
	"github.com/hallelujah-shih/ssh-multihop/internal/daemoncfg"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh/agent"
)

// GoAgent is a pure Go SSH agent implementation.
// It manages SSH keys in memory for SSH authentication.
type GoAgent struct {
	userCtx     *UserContext
	memoryAgent *MemoryAgent
	keyLoader   *KeyLoader
}

// NewGoAgent creates a new pure Go SSH agent.
func NewGoAgent() (*GoAgent, error) {
	// Get user context (based on RUID for setuid/setgid scenarios)
	userCtx, err := NewUserContext()
	if err != nil {
		return nil, fmt.Errorf("failed to get user context: %w", err)
	}

	// Ensure agent directory exists
	if err := userCtx.EnsureAgentDir(); err != nil {
		return nil, fmt.Errorf("failed to create agent directory: %w", err)
	}

	return &GoAgent{
		userCtx:     userCtx,
		memoryAgent: NewMemoryAgent(),
		keyLoader:   NewKeyLoader(userCtx),
	}, nil
}

// LoadOptions configures how Initialize loads key material.
// Keys selects explicit mode (strict loading); empty Keys is auto mode.
type LoadOptions struct {
	Keys          []daemoncfg.KeySpec
	SSHConfigPath string
}

// Initialize loads keys and starts the socket server.
func (a *GoAgent) Initialize(opts LoadOptions) error {
	var keys []*agent.AddedKey
	if len(opts.Keys) > 0 {
		loaded, err := a.keyLoader.LoadSpecs(opts.Keys)
		if err != nil {
			return err
		}
		keys = loaded
	} else {
		keyPaths, err := a.collectKeyPaths(opts.SSHConfigPath)
		if err != nil {
			zap.L().Warn("Failed to collect key paths from SSH config, using defaults only", zap.Error(err))
		}
		// 注意: 不加 "keyPaths == nil -> 默认 key" 回落——现状（agent.go:43-48）解析失败即
		// LoadKeys(nil) -> NoUsableKeysError, auto 模式字节级不变（BCD I2, 盲审 r1 P1-2）。
		loaded, err := a.keyLoader.LoadKeys(keyPaths)
		if err != nil {
			if _, ok := err.(*NoUsableKeysError); ok {
				return fmt.Errorf(`no usable SSH keys found

Please ensure at least one of the following:
1. Add an unencrypted key to ~/.ssh/ (id_ed25519, id_rsa, etc.)
2. Add an unencrypted key in SSH config with IdentityFile
3. Keys with passphrases are not supported in daemon mode

Keys checked: %d`, len(keyPaths))
			}
			return fmt.Errorf("failed to load keys: %w", err)
		}
		keys = loaded
	}

	// Add keys to memory agent
	loadedCount := 0
	for _, key := range keys {
		if err := a.memoryAgent.Add(*key); err != nil {
			zap.L().Warn("Failed to add key to agent",
				zap.String("key", key.Comment),
				zap.Error(err))
		} else {
			loadedCount++
		}
	}

	zap.L().Info("In-memory SSH agent initialized",
		zap.Int("keys_loaded", loadedCount),
		zap.String("user", a.userCtx.String()))

	return nil
}

// collectKeyPaths collects all key paths from SSH config and defaults.
func (a *GoAgent) collectKeyPaths(sshConfigPath string) ([]string, error) {
	// Try to parse SSH config
	path := sshConfigPath
	if path == "" {
		path = a.userCtx.GetSSHConfigPath()
	}
	parser := config.NewParser()
	_, err := parser.ParseConfig(path)
	if err != nil {
		if os.IsNotExist(err) {
			// SSH config doesn't exist, use default keys
			zap.L().Debug("No SSH config found, using default keys")
			return a.keyLoader.CollectDefaultKeyPaths(), nil
		}
		return nil, fmt.Errorf("failed to parse SSH config: %w", err)
	}

	// List all hosts to collect IdentityFile configurations
	allHosts, err := parser.ListHosts()
	if err != nil {
		return nil, fmt.Errorf("failed to list hosts: %w", err)
	}

	// Collect all key paths from hosts
	keyPaths := a.keyLoader.CollectKeyPathsFromHosts(allHosts)

	return keyPaths, nil
}

// Stop stops the agent and clears keys from memory.
func (a *GoAgent) Stop() error {
	// Clear keys from memory
	if err := a.memoryAgent.RemoveAll(); err != nil {
		zap.L().Warn("Failed to clear keys from memory", zap.Error(err))
	}

	return nil
}

// GetKeyCount returns the number of keys loaded in the agent.
func (a *GoAgent) GetKeyCount() int {
	return a.memoryAgent.GetKeyCount()
}

// IsAvailable returns true if the agent has keys loaded.
func (a *GoAgent) IsAvailable() bool {
	return a.GetKeyCount() > 0
}

// GetAgent returns the underlying agent for use with SSH clients.
func (a *GoAgent) GetAgent() agent.Agent {
	return a.memoryAgent
}

// IsSetUID returns true if running in setuid/setgid mode.
func (a *GoAgent) IsSetUID() bool {
	return a.userCtx.IsSetUID()
}

// GetUserContext returns the user context.
func (a *GoAgent) GetUserContext() *UserContext {
	return a.userCtx
}
