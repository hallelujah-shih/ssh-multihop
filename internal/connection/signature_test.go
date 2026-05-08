package connection

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSignatureHash_Deterministic verifies that calling Hash() multiple times
// on the same signature produces the same result.
func TestSignatureHash_Deterministic(t *testing.T) {
	sig := ConnectionSignature{
		Username:  "root",
		Hostname:  "example.com",
		Port:      22,
		JumpChain: []string{"jump1", "jump2"},
	}

	hash1 := sig.Hash()
	hash2 := sig.Hash()
	hash3 := sig.Hash()

	// All hashes should be identical
	assert.Equal(t, hash1, hash2, "First and second hash should match")
	assert.Equal(t, hash2, hash3, "Second and third hash should match")
	assert.Equal(t, hash1, hash3, "First and third hash should match")

	// Hash should be 64 characters (SHA256 hex)
	assert.Len(t, hash1, 64, "SHA256 hash should be 64 hex characters")
}

// TestSignatureHash_DifferentInputs verifies that different signatures
// produce different hashes.
func TestSignatureHash_DifferentInputs(t *testing.T) {
	tests := []struct {
		name     string
		sig1     ConnectionSignature
		sig2     ConnectionSignature
		expected bool // true if hashes should be different
	}{
		{
			name:     "different usernames",
			sig1:     ConnectionSignature{Username: "user1", Hostname: "host", Port: 22},
			sig2:     ConnectionSignature{Username: "user2", Hostname: "host", Port: 22},
			expected: true,
		},
		{
			name:     "different hostnames",
			sig1:     ConnectionSignature{Username: "user", Hostname: "host1", Port: 22},
			sig2:     ConnectionSignature{Username: "user", Hostname: "host2", Port: 22},
			expected: true,
		},
		{
			name:     "different ports",
			sig1:     ConnectionSignature{Username: "user", Hostname: "host", Port: 22},
			sig2:     ConnectionSignature{Username: "user", Hostname: "host", Port: 2222},
			expected: true,
		},
		{
			name:     "different jump chains",
			sig1:     ConnectionSignature{Username: "user", Hostname: "host", Port: 22, JumpChain: []string{"jump1"}},
			sig2:     ConnectionSignature{Username: "user", Hostname: "host", Port: 22, JumpChain: []string{"jump2"}},
			expected: true,
		},
		{
			name:     "empty vs non-empty jump chain",
			sig1:     ConnectionSignature{Username: "user", Hostname: "host", Port: 22, JumpChain: []string{}},
			sig2:     ConnectionSignature{Username: "user", Hostname: "host", Port: 22, JumpChain: []string{"jump1"}},
			expected: true,
		},
		{
			name:     "different jump chain lengths",
			sig1:     ConnectionSignature{Username: "user", Hostname: "host", Port: 22, JumpChain: []string{"jump1"}},
			sig2:     ConnectionSignature{Username: "user", Hostname: "host", Port: 22, JumpChain: []string{"jump1", "jump2"}},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash1 := tt.sig1.Hash()
			hash2 := tt.sig2.Hash()

			if tt.expected {
				assert.NotEqual(t, hash1, hash2, "Hashes should be different for %s", tt.name)
			} else {
				assert.Equal(t, hash1, hash2, "Hashes should be equal for %s", tt.name)
			}
		})
	}
}

// TestSignatureHash_SameInputs verifies that identical signatures
// produce identical hashes (even if created separately).
func TestSignatureHash_SameInputs(t *testing.T) {
	sig1 := ConnectionSignature{
		Username:  "user",
		Hostname:  "host",
		Port:      22,
		JumpChain: []string{"jump1", "jump2", "jump3"},
	}

	sig2 := ConnectionSignature{
		Username:  "user",
		Hostname:  "host",
		Port:      22,
		JumpChain: []string{"jump1", "jump2", "jump3"},
	}

	assert.Equal(t, sig1.Hash(), sig2.Hash(), "Identical signatures should produce same hash")
}

// TestSignatureHash_JumpChainOrder verifies that the order of jump hosts
// matters in the hash (since jump chain order affects connection path).
// jump1->jump2 is different from jump2->jump1.
func TestSignatureHash_JumpChainOrder(t *testing.T) {
	sig1 := ConnectionSignature{
		Username:  "user",
		Hostname:  "host",
		Port:      22,
		JumpChain: []string{"jump1", "jump2"},
	}

	sig2 := ConnectionSignature{
		Username:  "user",
		Hostname:  "host",
		Port:      22,
		JumpChain: []string{"jump2", "jump1"},
	}

	assert.NotEqual(t, sig1.Hash(), sig2.Hash(), "Jump chain order should affect hash")
}

// TestSignatureHash_SuitableAsMapKey verifies that hashes can be used
// as map keys without issues.
func TestSignatureHash_SuitableAsMapKey(t *testing.T) {
	signatures := []ConnectionSignature{
		{Username: "user1", Hostname: "host1", Port: 22},
		{Username: "user2", Hostname: "host2", Port: 2222},
		{Username: "user3", Hostname: "host3", Port: 22, JumpChain: []string{"jump1"}},
	}

	// Create a map using hashes as keys
	m := make(map[string]string)
	for _, sig := range signatures {
		m[sig.Hash()] = sig.String()
	}

	// Verify we can retrieve values
	assert.Len(t, m, 3, "Map should have 3 entries")

	for _, sig := range signatures {
		val, ok := m[sig.Hash()]
		assert.True(t, ok, "Hash should exist in map")
		assert.Equal(t, sig.String(), val, "Retrieved value should match original")
	}
}

// TestSignatureString verifies human-readable string representation.
func TestSignatureString(t *testing.T) {
	tests := []struct {
		name     string
		sig      ConnectionSignature
		expected string
	}{
		{
			name:     "direct connection",
			sig:      ConnectionSignature{Username: "user", Hostname: "host", Port: 22},
			expected: "user@host:22",
		},
		{
			name:     "single jump host",
			sig:      ConnectionSignature{Username: "user", Hostname: "host", Port: 22, JumpChain: []string{"jump1"}},
			expected: "user@host:22 via jump1",
		},
		{
			name:     "multiple jump hosts",
			sig:      ConnectionSignature{Username: "user", Hostname: "host", Port: 22, JumpChain: []string{"jump1", "jump2", "jump3"}},
			expected: "user@host:22 via jump1,jump2,jump3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.sig.String())
		})
	}
}

// TestSignatureEquals verifies the Equals method.
func TestSignatureEquals(t *testing.T) {
	sig1 := ConnectionSignature{
		Username:  "user",
		Hostname:  "host",
		Port:      22,
		JumpChain: []string{"jump1", "jump2"},
	}

	sig2 := ConnectionSignature{
		Username:  "user",
		Hostname:  "host",
		Port:      22,
		JumpChain: []string{"jump1", "jump2"},
	}

	sig3 := ConnectionSignature{
		Username:  "different",
		Hostname:  "host",
		Port:      22,
		JumpChain: []string{"jump1", "jump2"},
	}

	require.True(t, sig1.Equals(sig2), "Identical signatures should be equal")
	require.False(t, sig1.Equals(sig3), "Different signatures should not be equal")
}

// TestSignatureHash_EmptyJumpChain verifies behavior with empty jump chain.
func TestSignatureHash_EmptyJumpChain(t *testing.T) {
	sig1 := ConnectionSignature{
		Username:  "user",
		Hostname:  "host",
		Port:      22,
		JumpChain: []string{},
	}

	sig2 := ConnectionSignature{
		Username:  "user",
		Hostname:  "host",
		Port:      22,
		JumpChain: nil,
	}

	// Both should produce the same hash (empty vs nil)
	assert.Equal(t, sig1.Hash(), sig2.Hash(), "Empty and nil jump chains should produce same hash")
}
