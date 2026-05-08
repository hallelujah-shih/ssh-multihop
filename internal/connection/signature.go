package connection

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// ConnectionSignature represents a unique identifier for an SSH connection.
// It combines the connection target (user@host:port) with the jump chain path.
//
// The signature is used as a key in the connection pool to identify connections
// that can be reused across multiple forwards.
//
// Example:
//
//	Direct connection: user@host:port
//	Via jump hosts: user@host:port via jump1,jump2
type ConnectionSignature struct {
	// Username is the SSH username (e.g., "root")
	Username string

	// Hostname is the target hostname or IP (e.g., "example.com")
	Hostname string

	// Port is the SSH port (typically 22)
	Port int

	// JumpChain is the list of jump hostnames in the connection path.
	// For a direct connection, this is empty.
	// For multi-hop: local -> jump1 -> jump2 -> target would be ["jump1", "jump2"]
	JumpChain []string
}

// String returns a human-readable representation of the signature.
// Format: "user@host:port" or "user@host:port via jump1,jump2"
func (s ConnectionSignature) String() string {
	jumpStr := ""
	if len(s.JumpChain) > 0 {
		jumpStr = " via " + strings.Join(s.JumpChain, ",")
	}
	return fmt.Sprintf("%s@%s:%d%s", s.Username, s.Hostname, s.Port, jumpStr)
}

// Hash generates a deterministic unique identifier for this connection signature.
//
// The hash is computed from all signature fields and is guaranteed to be:
// - Deterministic: same input always produces same output
// - Unique: different inputs produce different outputs (collision-resistant)
// - Safe as map key: uses SHA256 for uniformity
// - Order-sensitive: jump chain order matters (jump1->jump2 != jump2->jump1)
//
// The hash format is: SHA256(username|hostname|port|jump1,jump2)
// Returns a 64-character hex string (256 bits).
func (s ConnectionSignature) Hash() string {
	// Build canonical string: "username|hostname|port|jump1,jump2"
	// Jump chain order matters (don't sort!)
	var jumpStr string
	if len(s.JumpChain) > 0 {
		jumpStr = strings.Join(s.JumpChain, ",")
	}
	canonical := fmt.Sprintf("%s|%s|%d|%s", s.Username, s.Hostname, s.Port, jumpStr)

	// Compute SHA256 hash
	hash := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(hash[:])
}

// Equals checks if two signatures are equal.
// This is more efficient than comparing Hash() results.
func (s ConnectionSignature) Equals(other ConnectionSignature) bool {
	if s.Username != other.Username ||
		s.Hostname != other.Hostname ||
		s.Port != other.Port ||
		len(s.JumpChain) != len(other.JumpChain) {
		return false
	}

	// Compare jump chains (order matters for connection path)
	for i := range s.JumpChain {
		if s.JumpChain[i] != other.JumpChain[i] {
			return false
		}
	}

	return true
}
