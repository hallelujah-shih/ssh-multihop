package agent

import (
	"crypto"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hallelujah-shih/ssh-multihop/internal/daemoncfg"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// LoadSpecs loads exactly the configured keys, strictly: any failure aborts.
// The passphrase env var is read only when the key file actually needs decryption.
func (kl *KeyLoader) LoadSpecs(specs []daemoncfg.KeySpec) ([]*agent.AddedKey, error) {
	var keys []*agent.AddedKey
	seen := make(map[string]bool)
	var errs []error
	for i, spec := range specs {
		if seen[spec.Path] {
			continue
		}
		seen[spec.Path] = true
		added, err := kl.loadSpec(spec)
		if err != nil {
			errs = append(errs, fmt.Errorf("keys[%d] %s: %w", i, spec.Path, err))
			continue
		}
		keys = append(keys, added)
		zap.L().Info("Loaded configured SSH key",
			zap.String("key", spec.Path))
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("failed to load %d of %d configured keys: %w", len(errs), len(specs), errors.Join(errs...))
	}
	return keys, nil
}

func (kl *KeyLoader) loadSpec(spec daemoncfg.KeySpec) (*agent.AddedKey, error) {
	keyBytes, err := os.ReadFile(spec.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to read key file: %w", err)
	}
	privateKey, err := ssh.ParseRawPrivateKey(keyBytes)
	if err == nil {
		return withCert(spec.Path, privateKey)
	}
	var passErr *ssh.PassphraseMissingError
	if !errors.As(err, &passErr) {
		return nil, fmt.Errorf("failed to parse key: %w", err)
	}
	if spec.PassphraseEnv == "" {
		return nil, fmt.Errorf("key is encrypted but no passphrase_env configured")
	}
	passphrase, ok := os.LookupEnv(spec.PassphraseEnv)
	if !ok {
		return nil, fmt.Errorf("key is encrypted and passphrase_env %q is not set in environment", spec.PassphraseEnv)
	}
	privateKey, err = ssh.ParseRawPrivateKeyWithPassphrase(keyBytes, []byte(passphrase))
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt key with passphrase from %q: %w", spec.PassphraseEnv, err)
	}
	return withCert(spec.Path, privateKey)
}

func withCert(keyPath string, privateKey crypto.PrivateKey) (*agent.AddedKey, error) {
	// Certificate load/parse failures are deliberately non-fatal: the key loads
	// without a cert, matching the auto-mode behavior this was extracted from.
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

	return &agent.AddedKey{
		PrivateKey:       privateKey,
		Certificate:      cert,
		Comment:          filepath.Base(keyPath),
		LifetimeSecs:     0,
		ConfirmBeforeUse: false,
	}, nil
}
