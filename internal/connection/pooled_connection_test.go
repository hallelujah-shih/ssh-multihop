package connection

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

// mockSSHClient creates a mock SSH client for testing.
// In real usage, this would be an actual SSH connection.
func mockSSHClient(t *testing.T) *ssh.Client {
	// For testing purposes, we can use nil since we're not actually
	// making SSH connections in unit tests.
	// The PooledConnection doesn't directly call Client methods.
	return nil
}

// TestNewPooledConnection verifies initial state of a new pooled connection.
func TestNewPooledConnection(t *testing.T) {
	client := mockSSHClient(t)
	sig := ConnectionSignature{
		Username:  "user",
		Hostname:  "host",
		Port:      22,
		JumpChain: []string{"jump1"},
	}

	pc := NewPooledConnection(client, sig)

	require.NotNil(t, pc)
	assert.Equal(t, client, pc.Client)
	assert.True(t, pc.Signature.Equals(sig))
	assert.Equal(t, 1, pc.RefCount, "Initial refCount should be 1")
	assert.Equal(t, StatusActive, pc.Status, "Initial status should be Active")
	assert.False(t, pc.CreatedAt.IsZero())
	assert.False(t, pc.LastUsedAt.IsZero())
	assert.WithinDuration(t, time.Now(), pc.CreatedAt, 1*time.Second)
	assert.WithinDuration(t, time.Now(), pc.LastUsedAt, 1*time.Second)
}

// TestPooledConnection_Acquire verifies reference counting on Acquire.
func TestPooledConnection_Acquire(t *testing.T) {
	client := mockSSHClient(t)
	sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}
	pc := NewPooledConnection(client, sig)

	initialRefCount := pc.GetRefCount()
	assert.Equal(t, 1, initialRefCount, "Initial refCount should be 1")

	// Acquire should increment ref count
	err := pc.Acquire()
	require.NoError(t, err)
	assert.Equal(t, 2, pc.GetRefCount())
	assert.Equal(t, StatusActive, pc.GetStatus())

	// Multiple acquires
	for i := 0; i < 5; i++ {
		_ = pc.Acquire()
	}
	assert.Equal(t, 7, pc.GetRefCount())
}

// TestPooledConnection_Release verifies reference counting on Release.
func TestPooledConnection_Release(t *testing.T) {
	client := mockSSHClient(t)
	sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}
	pc := NewPooledConnection(client, sig)

	// Acquire a few times
	for i := 0; i < 3; i++ {
		_ = pc.Acquire()
	}
	assert.Equal(t, 4, pc.GetRefCount())

	// Release should decrement ref count
	err := pc.Release()
	require.NoError(t, err)
	assert.Equal(t, 3, pc.GetRefCount())
	assert.Equal(t, StatusActive, pc.GetStatus(), "Status should still be Active when refCount > 0")

	// Release all remaining
	for i := 0; i < 2; i++ {
		_ = pc.Release()
	}
	assert.Equal(t, 1, pc.GetRefCount())

	// Final release should set status to Idle
	_ = pc.Release()
	assert.Equal(t, 0, pc.GetRefCount())
	assert.Equal(t, StatusIdle, pc.GetStatus(), "Status should be Idle when refCount reaches 0")
}

// TestPooledConnection_ReleaseTooMany verifies error on releasing more than acquired.
func TestPooledConnection_ReleaseTooMany(t *testing.T) {
	client := mockSSHClient(t)
	sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}
	pc := NewPooledConnection(client, sig)

	// Release the initial reference
	err := pc.Release()
	require.NoError(t, err)

	// Releasing again should error
	err = pc.Release()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "refCount=0")
}

// TestPooledConnection_IsActive verifies status checking.
func TestPooledConnection_IsActive(t *testing.T) {
	client := mockSSHClient(t)
	sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}
	pc := NewPooledConnection(client, sig)

	// Initially active
	assert.True(t, pc.IsActive())

	// Still active with references
	_ = pc.Acquire()
	assert.True(t, pc.IsActive())
	_ = pc.Release()
	assert.True(t, pc.IsActive())

	// Idle but still active (not closed)
	_ = pc.Release()
	assert.Equal(t, StatusIdle, pc.GetStatus())
	assert.True(t, pc.IsActive(), "Idle connections should still be considered active")

	// After close, not active
	pc.Close()
	assert.False(t, pc.IsActive())
}

// TestPooledConnection_AcquireAfterClose verifies error on acquiring closed connection.
func TestPooledConnection_AcquireAfterClose(t *testing.T) {
	client := mockSSHClient(t)
	sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}
	pc := NewPooledConnection(client, sig)

	// Close the connection
	pc.Close()

	// Acquire should fail
	err := pc.Acquire()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "closed connection")
}

// TestPooledConnection_Close idempotence of Close.
func TestPooledConnection_Close(t *testing.T) {
	client := mockSSHClient(t)
	sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}
	pc := NewPooledConnection(client, sig)

	// First close
	pc.Close()
	assert.Equal(t, StatusClosed, pc.GetStatus())
	assert.False(t, pc.IsActive())

	// Second close should be idempotent
	pc.Close()
	assert.Equal(t, StatusClosed, pc.GetStatus())
}

// TestPooledConnection_LastUsedAt verifies LastUsedAt timestamp updates.
func TestPooledConnection_LastUsedAt(t *testing.T) {
	client := mockSSHClient(t)
	sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}
	pc := NewPooledConnection(client, sig)

	initialTime := pc.GetLastUsedAt()

	// Sleep a bit to ensure timestamp difference
	time.Sleep(10 * time.Millisecond)

	// Acquire should update LastUsedAt
	_ = pc.Acquire()
	newTime := pc.GetLastUsedAt()
	assert.True(t, newTime.After(initialTime), "LastUsedAt should be updated")

	// Release should also update LastUsedAt
	time.Sleep(10 * time.Millisecond)
	_ = pc.Release()
	finalTime := pc.GetLastUsedAt()
	assert.True(t, finalTime.After(newTime), "LastUsedAt should be updated on release")
}

// TestPooledConnection_RefCountConcurrency verifies thread safety of reference counting.
func TestPooledConnection_RefCountConcurrency(t *testing.T) {
	client := mockSSHClient(t)
	sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}
	pc := NewPooledConnection(client, sig)

	const numGoroutines = 100
	const numOpsPerGoroutine = 10

	var wg sync.WaitGroup

	// Concurrent acquires
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOpsPerGoroutine; j++ {
				_ = pc.Acquire()
			}
		}()
	}

	wg.Wait()

	// Initial refCount (1) + all acquires
	expectedRefCount := 1 + (numGoroutines * numOpsPerGoroutine)
	assert.Equal(t, expectedRefCount, pc.GetRefCount())

	// Concurrent releases
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOpsPerGoroutine; j++ {
				_ = pc.Release()
			}
		}()
	}

	wg.Wait()

	// Should be back to initial refCount
	assert.Equal(t, 1, pc.GetRefCount())
}

// TestPooledConnection_Getters verifies all getter methods.
func TestPooledConnection_Getters(t *testing.T) {
	client := mockSSHClient(t)
	sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}
	pc := NewPooledConnection(client, sig)

	// GetRefCount
	assert.Equal(t, 1, pc.GetRefCount())

	// GetStatus
	assert.Equal(t, StatusActive, pc.GetStatus())

	// GetCreatedAt
	assert.False(t, pc.GetCreatedAt().IsZero())
	assert.WithinDuration(t, time.Now(), pc.GetCreatedAt(), 1*time.Second)

	// GetLastUsedAt
	assert.False(t, pc.GetLastUsedAt().IsZero())
	assert.WithinDuration(t, time.Now(), pc.GetLastUsedAt(), 1*time.Second)
}

// TestConnectionStatus_String verifies status string representation.
func TestConnectionStatus_String(t *testing.T) {
	tests := []struct {
		status   ConnectionStatus
		expected string
	}{
		{StatusActive, "active"},
		{StatusIdle, "idle"},
		{StatusClosed, "closed"},
		{ConnectionStatus(999), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.status.String())
		})
	}
}
