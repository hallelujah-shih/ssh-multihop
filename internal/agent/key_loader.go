package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hallelujah-shih/ssh-multihop/internal/config"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// KeyLoader loads SSH keys from the filesystem.
type KeyLoader struct {
	userCtx *UserContext
}

// NewKeyLoader creates a new key loader.
func NewKeyLoader(userCtx *UserContext) *KeyLoader {
	return &KeyLoader{
		userCtx: userCtx,
	}
}

// NoUsableKeysError is returned when no usable keys are found.
type NoUsableKeysError struct {
	CheckedPaths []string
}

func (e *NoUsableKeysError) Error() string {
	return fmt.Sprintf("no usable SSH keys found (checked %d paths)", len(e.CheckedPaths))
}

// LoadKeys loads keys from a list of paths.
// Only loads keys without passphrases.
func (kl *KeyLoader) LoadKeys(keyPaths []string) ([]*agent.AddedKey, error) {
	var keys []*agent.AddedKey

	zap.L().Debug("Loading SSH keys",
		zap.Int("total_paths", len(keyPaths)))

	for _, keyPath := range keyPaths {
		addedKey := kl.tryLoadKey(keyPath)
		if addedKey != nil {
			keys = append(keys, addedKey)
			zap.L().Debug("Successfully loaded key",
				zap.String("key", keyPath),
				zap.String("comment", addedKey.Comment))
		}
	}

	if len(keys) == 0 {
		return nil, &NoUsableKeysError{
			CheckedPaths: keyPaths,
		}
	}

	zap.L().Info("Loaded SSH keys",
		zap.Int("loaded", len(keys)),
		zap.Int("total_checked", len(keyPaths)))

	return keys, nil
}

// tryLoadKey attempts to load a single key file.
// Returns nil if the key cannot be loaded (encrypted, missing, etc.).
func (kl *KeyLoader) tryLoadKey(keyPath string) *agent.AddedKey {
	// Check if file exists
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		return nil
	}

	// Read private key
	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		zap.L().Debug("Failed to read key",
			zap.String("key", keyPath),
			zap.Error(err))
		return nil
	}

	// Try to parse without passphrase
	// IMPORTANT: Use ParseRawPrivateKey (not ParsePrivateKey) to get the raw
	// private key object, not a Signer interface. agent.AddedKey.PrivateKey requires
	// the actual private key type (*rsa.PrivateKey, *ed25519.PrivateKey, etc.)
	// not a ssh.Signer wrapper.
	privateKey, err := ssh.ParseRawPrivateKey(keyBytes)
	if err != nil {
		// Check if it's a passphrase error
		if _, ok := err.(*ssh.PassphraseMissingError); ok {
			zap.L().Debug("Skipping encrypted key (daemon mode)",
				zap.String("key", keyPath),
				zap.String("hint", "Use ssh-add to add this key manually"))
			return nil
		}

		zap.L().Debug("Failed to parse key",
			zap.String("key", keyPath),
			zap.Error(err))
		return nil
	}

	// Try to load corresponding certificate file
	// Certificate file naming convention: <key>-cert.pub
	certPath := keyPath + "-cert.pub"
	var cert *ssh.Certificate

	if certBytes, err := os.ReadFile(certPath); err == nil {
		pubKey, err := ssh.ParsePublicKey(certBytes)
		if err == nil {
			if c, ok := pubKey.(*ssh.Certificate); ok {
				cert = c
				zap.L().Debug("Loaded certificate for key",
					zap.String("key", keyPath),
					zap.String("certificate", certPath))
			}
		}
	}

	addedKey := &agent.AddedKey{
		PrivateKey:       privateKey, // Raw private key (*rsa.PrivateKey, *ed25519.PrivateKey, etc.)
		Certificate:      cert,
		Comment:          filepath.Base(keyPath),
		LifetimeSecs:     0,
		ConfirmBeforeUse: false,
	}

	return addedKey
}

// CollectKeyPathsFromHosts collects all key paths from host configurations.
func (kl *KeyLoader) CollectKeyPathsFromHosts(hosts []*config.HostInfo) []string {
	seen := make(map[string]bool)
	var paths []string
	sshDir := kl.userCtx.GetSSHDir()

	// Collect IdentityFile from all hosts
	for _, host := range hosts {
		// Case 1: IdentityFile is configured
		if host.IdentityFile != "" && !seen[host.IdentityFile] {
			paths = append(paths, host.IdentityFile)
			seen[host.IdentityFile] = true

			zap.L().Debug("Found configured IdentityFile",
				zap.String("host", host.Name),
				zap.String("identity_file", host.IdentityFile))
		}

		// Case 2: Only CertificateFile is configured (no IdentityFile)
		// Derive private key path from certificate file name
		// Example: ~/.ssh/dc-cert.pub → ~/.ssh/dc
		if host.CertificateFile != "" && host.IdentityFile == "" {
			privKey := strings.TrimSuffix(host.CertificateFile, "-cert.pub")
			if privKey != host.CertificateFile && !seen[privKey] {
				paths = append(paths, privKey)
				seen[privKey] = true

				zap.L().Debug("Derived private key from certificate",
					zap.String("host", host.Name),
					zap.String("cert_file", host.CertificateFile),
					zap.String("derived_key", privKey))
			}
		}
	}

	// Add default SSH keys (OpenSSH standard)
	defaultKeys := []string{
		filepath.Join(sshDir, "id_ed25519"),
		filepath.Join(sshDir, "id_rsa"),
		filepath.Join(sshDir, "id_ecdsa"),
		filepath.Join(sshDir, "id_dsa"),
	}

	for _, key := range defaultKeys {
		if !seen[key] {
			paths = append(paths, key)
			seen[key] = true
		}
	}

	configuredCount := 0
	certOnlyCount := 0
	for _, h := range hosts {
		if h.IdentityFile != "" {
			configuredCount++
		}
		if h.CertificateFile != "" && h.IdentityFile == "" {
			certOnlyCount++
		}
	}

	zap.L().Info("Collected SSH key paths",
		zap.Int("total_paths", len(paths)),
		zap.Int("configured_identity_files", configuredCount),
		zap.Int("cert_only_hosts", certOnlyCount),
		zap.Int("default_keys", 4))

	return paths
}

// CollectDefaultKeyPaths returns only the default key paths.
func (kl *KeyLoader) CollectDefaultKeyPaths() []string {
	sshDir := kl.userCtx.GetSSHDir()
	return []string{
		filepath.Join(sshDir, "id_ed25519"),
		filepath.Join(sshDir, "id_rsa"),
		filepath.Join(sshDir, "id_ecdsa"),
		filepath.Join(sshDir, "id_dsa"),
	}
}
