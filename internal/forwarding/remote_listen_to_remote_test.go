package forwarding

import (
	"context"
	"testing"
	"time"

	"github.com/hallelujah-shih/ssh-multihop/internal/connection"
	"github.com/hallelujah-shih/ssh-multihop/internal/db"
	"github.com/hallelujah-shih/ssh-multihop/internal/tunnel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRemoteListenToRemote_String verifies string representation
func TestRemoteListenToRemote_String(t *testing.T) {
	fwd := NewRemoteListenToRemote(
		"127.0.0.1:11434",
		"vmr.u24",
		11434,
		nil,
		"127.0.0.1:11434",
		"dc4",
		11434,
		nil,
		"test-id",
		nil,
		nil,
		0,
	)

	expected := "RemoteListenToRemote[vmr.u24:11434 → dc4:11434]"
	assert.Equal(t, expected, fwd.String())
}

// TestRemoteListenToRemote_Type verifies forward type
func TestRemoteListenToRemote_Type(t *testing.T) {
	fwd := NewRemoteListenToRemote(
		"127.0.0.1:11434",
		"vmr.u24",
		11434,
		nil,
		"127.0.0.1:11434",
		"dc4",
		11434,
		nil,
		"test-id",
		nil,
		nil,
		0,
	)

	assert.Equal(t, "remote_listen_to_remote", fwd.Type())
}

// TestRemoteListenToRemote_StatusTransitions verifies status transitions
func TestRemoteListenToRemote_StatusTransitions(t *testing.T) {
	fwd := NewRemoteListenToRemote(
		"127.0.0.1:11434",
		"vmr.u24",
		11434,
		nil,
		"127.0.0.1:11434",
		"dc4",
		11434,
		nil,
		"test-id",
		nil,
		nil,
		0,
	)

	// Initial status should be Stopped
	assert.Equal(t, StatusStopped, fwd.Status())

	// Set to Running
	fwd.setStatus(StatusRunning)
	assert.Equal(t, StatusRunning, fwd.Status())

	// Set to Error
	fwd.setStatus(StatusError)
	assert.Equal(t, StatusError, fwd.Status())
}

// TestRemoteListenToRemote_HealthCheckWhenStopped verifies health check fails when stopped
func TestRemoteListenToRemote_HealthCheckWhenStopped(t *testing.T) {
	fwd := NewRemoteListenToRemote(
		"127.0.0.1:11434",
		"vmr.u24",
		11434,
		nil,
		"127.0.0.1:11434",
		"dc4",
		11434,
		nil,
		"test-id",
		nil,
		nil,
		0,
	)

	err := fwd.HealthCheck()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not running")
}

// TestRemoteListenToRemote_NilPool verifies Start fails with nil pool
func TestRemoteListenToRemote_NilPool(t *testing.T) {
	fwd := NewRemoteListenToRemote(
		"127.0.0.1:11434",
		"vmr.u24",
		11434,
		nil,
		"127.0.0.1:11434",
		"dc4",
		11434,
		nil,
		"test-id",
		nil,
		nil, // Nil pool
		0,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	err := fwd.Start(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "connection pool is nil")
}

// TestRemoteListenToRemote_buildListenSignature verifies signature building for listener endpoint
func TestRemoteListenToRemote_buildListenSignature(t *testing.T) {
	hops := []*tunnel.HopConfig{
		{
			Host:     "jump1",
			User:     "user1",
			HostName: "jump1.example.com",
			Port:     22,
		},
		{
			Host:     "vmr.u24",
			User:     "user2",
			HostName: "vmr.example.com",
			Port:     22,
		},
	}

	fwd := NewRemoteListenToRemote(
		"127.0.0.1:11434",
		"vmr.u24",
		11434,
		hops,
		"127.0.0.1:11434",
		"dc4",
		11434,
		nil,
		"test-id",
		nil,
		nil,
		0,
	)

	sig := fwd.buildListenSignature()

	assert.Equal(t, "user2", sig.Username)
	assert.Equal(t, "vmr.example.com", sig.Hostname)
	assert.Equal(t, 22, sig.Port)
	assert.Equal(t, []string{"jump1"}, sig.JumpChain)
}

// TestRemoteListenToRemote_buildServiceSignature verifies signature building for service endpoint
func TestRemoteListenToRemote_buildServiceSignature(t *testing.T) {
	hops := []*tunnel.HopConfig{
		{
			Host:     "jump1",
			User:     "user1",
			HostName: "jump1.example.com",
			Port:     22,
		},
		{
			Host:     "dc4",
			User:     "user3",
			HostName: "dc4.example.com",
			Port:     22,
		},
	}

	fwd := NewRemoteListenToRemote(
		"127.0.0.1:11434",
		"vmr.u24",
		11434,
		nil,
		"127.0.0.1:11434",
		"dc4",
		11434,
		hops,
		"test-id",
		nil,
		nil,
		0,
	)

	sig := fwd.buildServiceSignature()

	assert.Equal(t, "user3", sig.Username)
	assert.Equal(t, "dc4.example.com", sig.Hostname)
	assert.Equal(t, 22, sig.Port)
	assert.Equal(t, []string{"jump1"}, sig.JumpChain)
}

// TestRemoteListenToRemote_EmptyHopChain verifies empty hop chain handling
func TestRemoteListenToRemote_EmptyHopChain(t *testing.T) {
	fwd := NewRemoteListenToRemote(
		"127.0.0.1:11434",
		"vmr.u24",
		11434,
		nil, // Empty source hop
		"127.0.0.1:11434",
		"dc4",
		11434,
		nil, // Empty target hop
		"test-id",
		nil,
		nil,
		0,
	)

	// Empty hop chains should produce empty signatures
	sourceSig := fwd.buildListenSignature()
	assert.Equal(t, connection.ConnectionSignature{}, sourceSig)

	targetSig := fwd.buildServiceSignature()
	assert.Equal(t, connection.ConnectionSignature{}, targetSig)
}

// TestRemoteListenToRemote_SingleHop verifies single hop handling
func TestRemoteListenToRemote_SingleHop(t *testing.T) {
	hops := []*tunnel.HopConfig{
		{
			Host:     "vmr.u24",
			User:     "user",
			HostName: "vmr.example.com",
			Port:     22,
		},
	}

	fwd := NewRemoteListenToRemote(
		"127.0.0.1:11434",
		"vmr.u24",
		11434,
		hops,
		"127.0.0.1:11434",
		"dc4",
		11434,
		nil,
		"test-id",
		nil,
		nil,
		0,
	)

	sig := fwd.buildListenSignature()

	assert.Equal(t, "user", sig.Username)
	assert.Equal(t, "vmr.example.com", sig.Hostname)
	assert.Equal(t, 22, sig.Port)
	assert.Empty(t, sig.JumpChain, "Single hop should have empty jump chain")
}

// TestRemoteListenToRemote_SetPassphraseSocket verifies passphrase socket setter
func TestRemoteListenToRemote_SetPassphraseSocket(t *testing.T) {
	fwd := NewRemoteListenToRemote(
		"127.0.0.1:11434",
		"vmr.u24",
		11434,
		nil,
		"127.0.0.1:11434",
		"dc4",
		11434,
		nil,
		"test-id",
		nil,
		nil,
		0,
	)

	ps := "test-socket"
	fwd.SetPassphraseSocket(ps)
	assert.Equal(t, ps, fwd.passphraseSocket)
}

// TestRemoteListenToRemote_WithDatabase verifies database status updates
func TestRemoteListenToRemote_WithDatabase(t *testing.T) {
	// Create in-memory database
	testDB, err := db.New(db.Config{
		Path: ":memory:",
	})
	require.NoError(t, err)
	defer func() { _ = testDB.Close() }()

	fwd := NewRemoteListenToRemote(
		"127.0.0.1:11434",
		"vmr.u24",
		11434,
		nil,
		"127.0.0.1:11434",
		"dc4",
		11434,
		nil,
		"test-forward-id",
		testDB,
		nil,
		0,
	)

	// Set error status
	fwd.setErrorStatus("test error message")

	// Verify status was written to database
	status, err := testDB.GetStatus("test-forward-id")
	require.NoError(t, err, "Should be able to retrieve status from database")
	assert.Equal(t, "error", status.Status)
	assert.Equal(t, "test error message", status.ErrorMessage)
}

// TestRemoteListenToRemote_NilDatabase verifies nil database handling
func TestRemoteListenToRemote_NilDatabase(t *testing.T) {
	fwd := NewRemoteListenToRemote(
		"127.0.0.1:11434",
		"vmr.u24",
		11434,
		nil,
		"127.0.0.1:11434",
		"dc4",
		11434,
		nil,
		"test-id",
		nil, // Nil database
		nil,
		0,
	)

	// Should not panic when database is nil
	assert.NotPanics(t, func() {
		fwd.setErrorStatus("test error")
	})
}

// TestRemoteListenToRemote_TwoDistinctSignatures verifies that listener and service have different signatures
func TestRemoteListenToRemote_TwoDistinctSignatures(t *testing.T) {
	sourceHops := []*tunnel.HopConfig{
		{
			Host:     "vmr.u24",
			User:     "user1",
			HostName: "vmr.example.com",
			Port:     22,
		},
	}

	targetHops := []*tunnel.HopConfig{
		{
			Host:     "dc4",
			User:     "user2",
			HostName: "dc4.example.com",
			Port:     22,
		},
	}

	fwd := NewRemoteListenToRemote(
		"127.0.0.1:11434",
		"vmr.u24",
		11434,
		sourceHops,
		"127.0.0.1:11434",
		"dc4",
		11434,
		targetHops,
		"test-id",
		nil,
		nil,
		0,
	)

	sourceSig := fwd.buildListenSignature()
	targetSig := fwd.buildServiceSignature()

	// Signatures should be different
	assert.NotEqual(t, sourceSig, targetSig, "Listener and service should have different signatures")

	// Verify specific differences
	assert.Equal(t, "user1", sourceSig.Username)
	assert.Equal(t, "vmr.example.com", sourceSig.Hostname)
	assert.Equal(t, "user2", targetSig.Username)
	assert.Equal(t, "dc4.example.com", targetSig.Hostname)
}

// TestRemoteListenToRemote_MultiHopJumpChain verifies multi-hop jump chain building
func TestRemoteListenToRemote_MultiHopJumpChain(t *testing.T) {
	hops := []*tunnel.HopConfig{
		{
			Host:     "jump1",
			User:     "user1",
			HostName: "jump1.example.com",
			Port:     22,
		},
		{
			Host:     "jump2",
			User:     "user2",
			HostName: "jump2.example.com",
			Port:     22,
		},
		{
			Host:     "vmr.u24",
			User:     "user3",
			HostName: "vmr.example.com",
			Port:     22,
		},
	}

	fwd := NewRemoteListenToRemote(
		"127.0.0.1:11434",
		"vmr.u24",
		11434,
		hops,
		"127.0.0.1:11434",
		"dc4",
		11434,
		nil,
		"test-id",
		nil,
		nil,
		0,
	)

	sig := fwd.buildListenSignature()

	// Should have 2 jump hosts (all except the last)
	assert.Equal(t, []string{"jump1", "jump2"}, sig.JumpChain)
	assert.Equal(t, "user3", sig.Username, "Should use final hop user")
	assert.Equal(t, "vmr.example.com", sig.Hostname, "Should use final hop hostname")
}
