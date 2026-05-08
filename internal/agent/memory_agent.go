package agent

import (
	"errors"
	"io"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

var (
	ErrLocked    = errors.New("agent is locked")
	ErrNoSuchKey = errors.New("key not found")
)

// MemoryAgent implements the agent.Agent interface with in-memory key storage.
type MemoryAgent struct {
	mu         sync.Mutex
	locked     bool
	passphrase []byte
	keys       map[string]*agent.AddedKey
}

// NewMemoryAgent creates a new in-memory SSH agent.
func NewMemoryAgent() *MemoryAgent {
	return &MemoryAgent{
		keys: make(map[string]*agent.AddedKey),
	}
}

// List returns the list of identities.
func (m *MemoryAgent) List() ([]*agent.Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.locked {
		return nil, ErrLocked
	}

	var keys []*agent.Key
	for _, addedKey := range m.keys {
		var pubKey ssh.PublicKey

		if addedKey.Certificate != nil {
			pubKey = addedKey.Certificate
		} else {
			// PrivateKey is interface{}, try to get public key
			signer, err := ssh.NewSignerFromKey(addedKey.PrivateKey)
			if err != nil {
				continue
			}
			pubKey = signer.PublicKey()
		}

		key := &agent.Key{
			Format: pubKey.Type(),
			Blob:   pubKey.Marshal(),
		}

		if addedKey.Comment != "" {
			key.Comment = addedKey.Comment
		}

		keys = append(keys, key)
	}

	return keys, nil
}

// Sign returns a signature for the data.
func (m *MemoryAgent) Sign(key ssh.PublicKey, data []byte) (*ssh.Signature, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.locked {
		return nil, ErrLocked
	}

	// Find the key by public key
	addedKey, err := m.getByPublicKey(key)
	if err != nil {
		return nil, err
	}

	signer, err := ssh.NewSignerFromKey(addedKey.PrivateKey)
	if err != nil {
		return nil, err
	}

	if addedKey.Certificate != nil {
		certSigner, err := ssh.NewCertSigner(addedKey.Certificate, signer)
		if err == nil {
			signer = certSigner
		}
	}

	return signer.Sign(nil, data)
}

// Add adds a private key to the agent.
func (m *MemoryAgent) Add(key agent.AddedKey) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.locked {
		return ErrLocked
	}

	var pubKey ssh.PublicKey

	if key.Certificate != nil {
		pubKey = key.Certificate
	} else {
		// PrivateKey is interface{}, get public key via signer
		signer, err := ssh.NewSignerFromKey(key.PrivateKey)
		if err != nil {
			return err
		}
		pubKey = signer.PublicKey()
	}

	keyID := string(pubKey.Marshal())

	// Store a copy to avoid external modifications
	keyCopy := key
	m.keys[keyID] = &keyCopy

	return nil
}

// Remove removes a key from the agent.
func (m *MemoryAgent) Remove(key ssh.PublicKey) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.locked {
		return ErrLocked
	}

	keyID := string(key.Marshal())
	if _, exists := m.keys[keyID]; !exists {
		return ErrNoSuchKey
	}

	delete(m.keys, keyID)
	return nil
}

// RemoveAll removes all keys.
func (m *MemoryAgent) RemoveAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.locked {
		return ErrLocked
	}

	m.keys = make(map[string]*agent.AddedKey)
	return nil
}

// Lock locks the agent with a passphrase.
func (m *MemoryAgent) Lock(passphrase []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.locked {
		return errors.New("already locked")
	}

	m.locked = true
	m.passphrase = passphrase
	return nil
}

// Unlock unlocks the agent.
func (m *MemoryAgent) Unlock(passphrase []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.locked {
		return errors.New("not locked")
	}

	if !stringEquals(m.passphrase, passphrase) {
		return errors.New("wrong passphrase")
	}

	m.locked = false
	m.passphrase = nil
	return nil
}

// SignWithFlags signs with flags (ExtendedAgent interface).
func (m *MemoryAgent) SignWithFlags(key *agent.Key, data []byte, flags agent.SignatureFlags) (*ssh.Signature, error) {
	// For now, just call Sign - flags can be used for future extensions
	return m.Sign(key, data)
}

// Extension handles agent extensions (ExtendedAgent interface).
func (m *MemoryAgent) Extension(extensionType string, contents []byte) ([]byte, error) {
	return nil, agent.ErrExtensionUnsupported
}

// getByPublicKey finds an added key by public key.
func (m *MemoryAgent) getByPublicKey(pubKey ssh.PublicKey) (*agent.AddedKey, error) {
	addedKey, exists := m.keys[string(pubKey.Marshal())]
	if !exists {
		return nil, ErrNoSuchKey
	}
	return addedKey, nil
}

// Signers returns signers for all the known keys.
func (m *MemoryAgent) Signers() ([]ssh.Signer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.locked {
		return nil, ErrLocked
	}

	var signers []ssh.Signer
	for _, addedKey := range m.keys {
		signer, err := ssh.NewSignerFromKey(addedKey.PrivateKey)
		if err != nil {
			continue
		}

		if addedKey.Certificate != nil {
			certSigner, err := ssh.NewCertSigner(addedKey.Certificate, signer)
			if err == nil {
				signer = certSigner
			}
		}

		signers = append(signers, signer)
	}

	return signers, nil
}

// stringEquals safely compares two byte slices in constant time.
func stringEquals(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}

	var result byte
	for i := 0; i < len(a); i++ {
		result |= a[i] ^ b[i]
	}

	return result == 0
}

// GetKeyCount returns the number of keys in the agent.
func (m *MemoryAgent) GetKeyCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.keys)
}

// IsLocked returns whether the agent is locked.
func (m *MemoryAgent) IsLocked() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.locked
}

// Serve serves the agent protocol on the given connection.
// It returns when an I/O error occurs.
func Serve(ag agent.Agent, c io.ReadWriter) error {
	return agent.ServeAgent(ag, c)
}
