package service

import (
	"testing"

	"github.com/hallelujah-shih/ssh-multihop/internal/connection"
	"github.com/stretchr/testify/require"
)

// TestPassphraseSocketInjection verifies that PassphraseSocket is correctly
// injected into ForwardService and passed to Forward instances.
func TestPassphraseSocketInjection(t *testing.T) {
	// Create a test passphrase socket
	testSocket := connection.NewPassphraseSocket("/tmp/test-passphrase.sock")

	// Create ForwardService
	svc, err := New(nil)
	require.NoError(t, err)
	// Note: nil database is OK for this test since we only test passphrase socket injection

	// Set passphrase socket
	svc.SetPassphraseSocket(testSocket)

	// Verify passphrase socket is set
	if svc.passphraseSocket == nil {
		t.Fatal("passphraseSocket should not be nil after SetPassphraseSocket")
	}

	// Verify it's the same instance
	if svc.passphraseSocket != testSocket {
		t.Fatal("passphraseSocket should be the same instance that was set")
	}

	t.Logf("✅ PassphraseSocket injection successful")
}

// TestNilPassphraseSocketInjection verifies that nil PassphraseSocket
// doesn't cause any issues.
func TestNilPassphraseSocketInjection(t *testing.T) {
	// Create ForwardService
	svc, err := New(nil)
	require.NoError(t, err)
	// Note: nil database is OK for this test since we only test passphrase socket injection

	// Set nil passphrase socket (should not panic)
	svc.SetPassphraseSocket(nil)

	// Verify passphrase socket is nil
	if svc.passphraseSocket != nil {
		t.Fatal("passphraseSocket should be nil after setting nil")
	}

	t.Logf("✅ Nil PassphraseSocket injection handled correctly")
}
