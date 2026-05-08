package connection

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"sync"
	"time"

	agentpkg "github.com/hallelujah-shih/ssh-multihop/internal/agent"
	"github.com/hallelujah-shih/ssh-multihop/internal/tunnel"
	"github.com/hallelujah-shih/ssh-multihop/internal/util"
	agent "github.com/xanzy/ssh-agent"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

// SSHClientConfigBuilder builds SSH client configurations from hop configurations.
// SSHClientConfigBuilder builds SSH client configurations from hop configurations.
type SSHClientConfigBuilder struct {
	mu                sync.Mutex
	customAgent       sshAgent
	agentEnabled      bool
	sshDir            string // Custom SSH directory for keys and certificates
	keepaliveInterval time.Duration
	keepaliveTimeout  time.Duration
	isDaemon          bool // Running in daemon mode (no TTY)
	passphraseSocket  *PassphraseSocket
}

// sshAgent interface to allow mocking in tests
type sshAgent interface {
	Signers() ([]ssh.Signer, error)
}

// NewSSHClientConfigBuilder creates a new SSH client config builder.
// It automatically checks for SSH_AUTH_SOCK and logs available agents.
//
// Note: This builder assumes daemon mode (no interactive prompts).
// The application only has a daemon command entry point, so interactive
// passphrase prompts via stdin are never appropriate.
func NewSSHClientConfigBuilder() *SSHClientConfigBuilder {
	builder := &SSHClientConfigBuilder{
		agentEnabled: true, // Enable ssh-agent by default
		isDaemon:     true, // Always daemon mode - no interactive stdin prompts
	}

	// Check for external SSH agent from SSH_AUTH_SOCK
	socketPath, keyCount, err := agentpkg.CheckExternalAgent()
	if err != nil {
		zap.L().Debug("Error checking SSH_AUTH_SOCK", zap.Error(err))
	} else if socketPath != "" {
		zap.L().Debug("SSH agent from SSH_AUTH_SOCK is available",
			zap.String("socket", socketPath),
			zap.Int("keys", keyCount))
	} else {
		zap.L().Debug("No SSH agent detected from SSH_AUTH_SOCK")
	}

	return builder
}

// SetAgent sets a custom SSH agent (useful for testing).
func (b *SSHClientConfigBuilder) SetAgent(customAgent sshAgent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.customAgent = customAgent
}

// DisableAgent disables SSH agent integration.
func (b *SSHClientConfigBuilder) DisableAgent() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.agentEnabled = false
}

// SetSSHDir sets the custom SSH directory for loading keys and certificates.
func (b *SSHClientConfigBuilder) SetSSHDir(dir string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sshDir = dir
}

// GetSSHDir returns the configured SSH directory.
func (b *SSHClientConfigBuilder) GetSSHDir() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sshDir
}

// SetPassphraseSocket sets the passphrase socket for retrieving SSH key passphrases.
func (b *SSHClientConfigBuilder) SetPassphraseSocket(ps *PassphraseSocket) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.passphraseSocket = ps
}

// Build creates an SSH client configuration from a hop configuration.
func (b *SSHClientConfigBuilder) Build(hop *tunnel.HopConfig) (*ssh.ClientConfig, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	config := &ssh.ClientConfig{}

	// Set user
	if hop.User != "" {
		config.User = hop.User
	} else {
		// Default to current system user
		currentUser, err := user.Current()
		if err != nil {
			return nil, fmt.Errorf("failed to get current user: %w", err)
		}
		config.User = currentUser.Username
	}

	// Set host key callback (accept all for now - can be made configurable)
	config.HostKeyCallback = ssh.InsecureIgnoreHostKey()

	// Build auth methods
	authMethods, err := b.buildAuthMethods(hop)
	if err != nil {
		return nil, fmt.Errorf("failed to build auth methods: %w", err)
	}

	if len(authMethods) == 0 {
		return nil, fmt.Errorf("no authentication methods available")
	}

	config.Auth = authMethods

	// Note: Keepalive is configured at connection level, not config level
	// The builder stores these values but they are not part of ssh.ClientConfig

	return config, nil
}

// buildAuthMethods builds authentication methods for the hop.
func (b *SSHClientConfigBuilder) buildAuthMethods(hop *tunnel.HopConfig) ([]ssh.AuthMethod, error) {
	var authMethods []ssh.AuthMethod

	// Try IdentityFile from SSH config first if specified
	// This ensures host-specific keys are tried before generic agent keys
	if hop.IdentityFile != "" {
		var certFile string
		if hop.CertificateFile != "" {
			certFile = hop.CertificateFile
		}
		keyAuth, err := b.tryPublicKeyFileWithCert(hop.IdentityFile, certFile)
		if err == nil && keyAuth != nil {
			authMethods = append(authMethods, keyAuth)
			zap.L().Debug("Using IdentityFile from SSH config",
				zap.String("hop", hop.Host),
				zap.String("identity_file", hop.IdentityFile))
		} else {
			// Log if IdentityFile couldn't be loaded
			zap.L().Debug("Failed to load IdentityFile, will try SSH agent",
				zap.String("hop", hop.Host),
				zap.String("identity_file", hop.IdentityFile),
				zap.Error(err))
		}
	}

	// Try SSH agent if enabled (for additional keys and certificates)
	if b.agentEnabled {
		agentAuth, err := b.trySSHAgent()
		if err == nil && agentAuth != nil {
			authMethods = append(authMethods, agentAuth)
		} else {
			// If agent is not available, we'll try default keys below
			zap.L().Debug("SSH agent authentication failed or unavailable, will try default keys",
				zap.String("hop", hop.Host),
				zap.Error(err))
		}
	}

	// If still no auth methods, try default SSH key files
	// Note: Certificates require SSH agent and won't be loaded here
	// Important: Try ALL available keys since different hosts may require different keys
	if len(authMethods) == 0 {
		// Determine SSH directory to use
		sshDir := b.sshDir
		if sshDir == "" {
			// Use util.UserHomeDir() for setuidgid compatibility
			// This queries the passwd database based on effective UID
			homeDir, err := util.UserHomeDir()
			if err != nil {
				// Fallback to environment variable (may be wrong in setuidgid)
				homeDir = os.Getenv("HOME")
			}
			sshDir = fmt.Sprintf("%s/.ssh", homeDir)
		}

		zap.L().Debug("No auth methods from IdentityFile, trying default keys",
			zap.String("hop", hop.Host),
			zap.String("ssh_dir", sshDir))

		// Try default SSH keys (matching OpenSSH client behavior)
		// Based on ssh(1) man page, the default identity files are:
		// ~/.ssh/id_rsa, ~/.ssh/id_ecdsa, ~/.ssh/id_ecdsa_sk,
		// ~/.ssh/id_ed25519, ~/.ssh/id_ed25519_sk
		// Additional keys should be specified via IdentityFile in SSH config
		commonKeys := []string{
			"id_ed25519",    // Preferred in modern OpenSSH
			"id_ed25519_sk", // Security key (U2F/FIDO2)
			"id_rsa",        // RSA keys (most common)
			"id_ecdsa",      // ECDSA keys
			"id_ecdsa_sk",   // ECDSA security key
			"id_dsa",        // DSA keys (deprecated but still supported)
		}

		for _, keyName := range commonKeys {
			keyPath := fmt.Sprintf("%s/%s", sshDir, keyName)
			keyAuth, err := b.tryPublicKeyFile(keyPath)
			if err == nil && keyAuth != nil {
				authMethods = append(authMethods, keyAuth)
				zap.L().Debug("Successfully loaded SSH key",
					zap.String("hop", hop.Host),
					zap.String("key", keyPath))
			}
			// Don't break - try all keys since different hosts need different keys
		}
	}

	// Add keyboard-interactive auth as fallback
	authMethods = append(authMethods, ssh.PasswordCallback(func() (string, error) {
		return "", fmt.Errorf("password authentication not supported")
	}))

	return authMethods, nil
}

// trySSHAgent attempts to use ssh-agent for authentication.
func (b *SSHClientConfigBuilder) trySSHAgent() (ssh.AuthMethod, error) {
	// Use custom agent if set (for testing)
	if b.customAgent != nil {
		zap.L().Info("Using custom SSH agent (for testing)")
		return ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
			return b.customAgent.Signers()
		}), nil
	}

	// Debug: Log SSH_AUTH_SOCK at trySSHAgent() call time
	sshAuthSock := os.Getenv("SSH_AUTH_SOCK")
	zap.L().Debug("trySSHAgent: Checking SSH_AUTH_SOCK",
		zap.String("SSH_AUTH_SOCK", sshAuthSock),
		zap.Bool("is_empty", sshAuthSock == ""))

	// Try external SSH agent via SSH_AUTH_SOCK first (higher priority)
	// This ensures user's configured SSH agent (GNOME Keyring, etc.) is preferred
	if sshAuthSock != "" {
		zap.L().Info("Using external SSH agent via socket",
			zap.String("socket", sshAuthSock))

		return ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
			sshAgent, _, err := agent.New()
			if err != nil {
				zap.L().Debug("Failed to connect to external ssh-agent",
					zap.Error(err))
				return nil, fmt.Errorf("failed to connect to ssh-agent: %w", err)
			}
			// Note: Don't close the connection - the signers manage it internally

			signers, err := sshAgent.Signers()
			if err != nil {
				zap.L().Debug("Failed to get signers from external ssh-agent",
					zap.Error(err))
				return nil, fmt.Errorf("failed to get signers from ssh-agent: %w", err)
			}

			if len(signers) == 0 {
				zap.L().Debug("No signers available in external ssh-agent")
				return nil, errors.New("no keys available in ssh-agent")
			}

			zap.L().Debug("Successfully got signers from external ssh-agent",
				zap.Int("count", len(signers)))
			return signers, nil
		}), nil
	}

	// Fallback to built-in agent (pure in-memory agent)
	// Only used if no external SSH agent is available
	if builtInAgent, err := agentpkg.GetBuiltInAgent(); err == nil && builtInAgent.IsAvailable() {
		zap.L().Info("Using built-in in-memory SSH agent (fallback, no SSH_AUTH_SOCK)",
			zap.Int("keys", builtInAgent.GetKeyCount()))

		return ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
			signers, err := builtInAgent.GetSigners()
			if err != nil {
				zap.L().Debug("Failed to get signers from built-in agent",
					zap.Error(err))
				return nil, fmt.Errorf("failed to get signers from built-in agent: %w", err)
			}

			if len(signers) == 0 {
				zap.L().Debug("No signers available in built-in agent")
				return nil, errors.New("no keys available in built-in agent")
			}

			zap.L().Debug("Successfully got signers from built-in agent",
				zap.Int("count", len(signers)))
			return signers, nil
		}), nil
	}

	// No SSH agent available
	zap.L().Debug("No SSH agent available (no SSH_AUTH_SOCK and no built-in agent)")
	return nil, nil
}

// tryPublicKeyFile attempts to use a private key file for authentication.
// It also attempts to load a corresponding certificate file (-cert.pub) if it exists.
func (b *SSHClientConfigBuilder) tryPublicKeyFile(path string) (ssh.AuthMethod, error) {
	return b.tryPublicKeyFileWithCert(path, "")
}

// tryPublicKeyFileWithCert attempts to use a private key file with an optional certificate.
// If certPath is provided, it will be used; otherwise, it will try to find a corresponding
// certificate file by appending "-cert.pub" to the private key path.
func (b *SSHClientConfigBuilder) tryPublicKeyFileWithCert(path, certPath string) (ssh.AuthMethod, error) {
	signer, err := b.parsePrivateKeyWithPassphrase(path)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	// Determine which certificate path to use
	// If certPath is empty, try the default "-cert.pub" suffix
	actualCertPath := certPath
	if actualCertPath == "" {
		actualCertPath = path + "-cert.pub"
	}

	// Try to load the certificate file
	certBytes, certErr := os.ReadFile(actualCertPath)
	if certErr == nil {
		// Certificate file exists, try to parse and add it to the signer
		pubKey, err := ssh.ParsePublicKey(certBytes)
		if err != nil {
			// Certificate file exists but failed to parse - log warning but continue with key only
			// This is consistent with OpenSSH behavior
		} else {
			// Check if the parsed key is a certificate
			if cert, ok := pubKey.(*ssh.Certificate); ok {
				// Create a signer with the certificate
				// Note: NewCertSigner takes (cert, signer) in this order
				certSigner, err := ssh.NewCertSigner(cert, signer)
				if err == nil {
					signer = certSigner
				}
				// If cert signer creation fails, use the original signer
			}
		}
	}
	// If cert file doesn't exist, that's ok - just use the key without certificate

	// Use PublicKeysCallback for better compatibility with OpenSSH servers
	// This allows the server to choose which key to use
	return ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
		return []ssh.Signer{signer}, nil
	}), nil
}

// parsePrivateKeyWithPassphrase reads and parses a private key file, prompting for passphrase if needed.
func (b *SSHClientConfigBuilder) parsePrivateKeyWithPassphrase(keyPath string) (ssh.Signer, error) {
	// Read the private key file
	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		if os.IsNotExist(err) {
			zap.L().Debug("SSH key file not found",
				zap.String("key", keyPath))
			return nil, fmt.Errorf("cannot find the key file: %s", keyPath)
		}
		zap.L().Warn("Failed to read SSH key file",
			zap.String("key", keyPath),
			zap.Error(err))
		return nil, fmt.Errorf("failed to read key file: %w", err)
	}

	// Try parsing without passphrase first (for unencrypted keys)
	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err == nil {
		// Key was successfully parsed without passphrase
		zap.L().Debug("Successfully loaded unencrypted SSH key",
			zap.String("key", keyPath))
		return signer, nil
	}

	// Check if the error indicates the key is encrypted
	var parseKeyErr *ssh.PassphraseMissingError
	if errors.As(err, &parseKeyErr) {
		// Key is encrypted
		zap.L().Debug("SSH key requires passphrase",
			zap.String("key", keyPath))

		// Try passphrase socket first
		if b.passphraseSocket != nil {
			// Parse the public key to get fingerprint
			pubKey, err := ssh.ParsePublicKey(keyBytes)
			if err != nil {
				zap.L().Debug("Failed to parse public key for fingerprint",
					zap.String("key", keyPath),
					zap.Error(err))
			} else {
				fingerprint := ssh.FingerprintSHA256(pubKey)
				if passphrase, ok := b.passphraseSocket.GetPassphrase(fingerprint); ok {
					signer, err := ssh.ParsePrivateKeyWithPassphrase(keyBytes, []byte(passphrase))
					if err == nil {
						zap.L().Debug("Successfully used passphrase from socket",
							zap.String("key", keyPath),
							zap.String("fingerprint", fingerprint))
						return signer, nil
					}
					zap.L().Warn("Passphrase from socket failed",
						zap.String("key", keyPath),
						zap.Error(err))
				}
			}
		}

		// Skip interactive prompts in daemon mode
		if b.isDaemon {
			zap.L().Warn("Cannot prompt for passphrase in daemon mode",
				zap.String("key", keyPath),
				zap.String("hint", "Use ssh-agent or unencrypted keys"))
			return nil, fmt.Errorf("key requires passphrase but running in daemon mode: %s", keyPath)
		}

		passphrase, err := b.promptForPassphrase(os.Stderr)
		if err != nil {
			return nil, fmt.Errorf("failed to get passphrase: %w", err)
		}

		// Try parsing with passphrase
		signer, err = ssh.ParsePrivateKeyWithPassphrase(keyBytes, passphrase)
		if err != nil {
			return nil, fmt.Errorf("incorrect passphrase or invalid key: %w", err)
		}

		return signer, nil
	}

	// Some other parsing error
	zap.L().Warn("Failed to parse SSH key",
		zap.String("key", keyPath),
		zap.Error(err))
	return nil, fmt.Errorf("failed to parse private key: %w", err)
}

// promptForPassphrase prompts the user for a passphrase via stdin.
func (b *SSHClientConfigBuilder) promptForPassphrase(out interface{ WriteString(string) (int, error) }) ([]byte, error) {
	// Display prompt to the user
	_, err := out.WriteString("Enter passphrase for key: ")
	if err != nil {
		return nil, fmt.Errorf("failed to write prompt: %w", err)
	}

	// Read passphrase from stdin
	var passphrase string
	_, err = fmt.Scanln(&passphrase)
	if err != nil {
		// Handle EOF (e.g., when input is piped)
		if errors.Is(err, io.EOF) {
			return nil, errors.New("no passphrase provided (EOF)")
		}
		return nil, fmt.Errorf("failed to read passphrase: %w", err)
	}

	return []byte(passphrase), nil
}

// Keepalive configuration methods

// SetKeepalive sets the keepalive interval and timeout for SSH connections.
// Interval is how often to send keepalive messages.
// Timeout is how long to wait for a response to a keepalive message.
func (b *SSHClientConfigBuilder) SetKeepalive(interval, timeout time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.keepaliveInterval = interval
	b.keepaliveTimeout = timeout
}

// GetKeepalive returns the configured keepalive interval and timeout.
func (b *SSHClientConfigBuilder) GetKeepalive() (interval, timeout time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.keepaliveInterval, b.keepaliveTimeout
}
