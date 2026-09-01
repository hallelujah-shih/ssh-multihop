package service

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"

	"github.com/hallelujah-shih/ssh-multihop/internal/db"
	"github.com/hallelujah-shih/ssh-multihop/internal/forwarding"
	"github.com/hallelujah-shih/ssh-multihop/internal/tunnel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// freePort returns an OS-assigned free TCP port; util.ParseAddress rejects
// port 0, so the real bind target must be a concrete port.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// fakeForward implements forwarding.Forward with observable state.
// All access is mutex-guarded: the service may call Stop()/Status()
// from its own goroutines.
type fakeForward struct {
	mu     sync.Mutex
	status forwarding.ForwardStatus
	stops  int
}

func newFakeForward(status forwarding.ForwardStatus) *fakeForward {
	return &fakeForward{status: status}
}

func (f *fakeForward) Start(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = forwarding.StatusRunning
	return nil
}

func (f *fakeForward) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
	f.status = forwarding.StatusStopped
	return nil
}

func (f *fakeForward) HealthCheck() error { return nil }

func (f *fakeForward) Type() string { return string(db.LocalListenToRemote) }

func (f *fakeForward) Status() forwarding.ForwardStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *fakeForward) String() string { return "fakeForward" }

func (f *fakeForward) stopCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stops
}

// injectForward places a fake into the service's in-memory map.
func injectForward(t *testing.T, svc *ForwardService, id string, fwd forwarding.Forward) {
	t.Helper()
	svc.mu.Lock()
	svc.forwards[id] = ForwardWrapper{Forward: fwd}
	svc.mu.Unlock()
}

// TestSync_RebuildsErrorForward_Success guards against the "forward errored
// and never recovers" incident: a StatusError forward must be stopped once,
// replaced by a fresh running forward, flipped to running in the database,
// and have its backoff counter cleared.
func TestSync_RebuildsErrorForward_Success(t *testing.T) {
	database := setupTestDB(t)
	svc, err := New(database)
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Stop() })

	// Real startForward path: 127.0.0.1:0 binds without SSH (both hosts
	// are "local" so hopResolver is never called).
	fwd := &db.Forward{
		Type:        db.LocalListenToRemote,
		ListenHost:  "local",
		ListenAddr:  fmt.Sprintf("127.0.0.1:%d", freePort(t)),
		ServiceHost: "local",
		ServiceAddr: "127.0.0.1:1",
	}
	require.NoError(t, database.CreateForward(fwd))

	fake := newFakeForward(forwarding.StatusError)
	injectForward(t, svc, fwd.ID, fake)

	// Prime a failure count to prove a successful rebuild resets it.
	svc.mu.Lock()
	svc.consecutiveFailures[fwd.ID] = 3
	svc.mu.Unlock()

	svc.sync() // rebuild runs synchronously inside sync

	// Old fake stopped exactly once.
	assert.Equal(t, 1, fake.stopCalls(), "old errored forward must be stopped exactly once")

	// Replaced in the map by a fresh running forward.
	svc.mu.RLock()
	wrapper, exists := svc.forwards[fwd.ID]
	svc.mu.RUnlock()
	require.True(t, exists, "rebuilt forward must be back in the map")
	assert.NotEqual(t, forwarding.Forward(fake), wrapper.Forward, "forward must be a new instance")
	require.NotNil(t, wrapper.Forward)
	assert.Equal(t, forwarding.StatusRunning, wrapper.Forward.Status())

	// Database reflects recovery.
	status, err := database.GetStatus(fwd.ID)
	require.NoError(t, err)
	assert.Equal(t, "running", status.Status)
	assert.Empty(t, status.ErrorMessage)

	// Backoff counter cleared.
	svc.mu.RLock()
	_, hasFailures := svc.consecutiveFailures[fwd.ID]
	svc.mu.RUnlock()
	assert.False(t, hasFailures, "consecutiveFailures must be cleared after successful rebuild")
}

// TestSync_RebuildFailure_RecordsBackoffAndSkipsRetry guards against the
// "rebuild failure is swallowed / retried in a tight loop" incident: a failed
// rebuild must surface the error in the database, bump the backoff counter,
// and a follow-up sync within the backoff window must not attempt again.
func TestSync_RebuildFailure_RecordsBackoffAndSkipsRetry(t *testing.T) {
	database := setupTestDB(t)
	svc, err := New(database)
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Stop() })

	svc.hopResolver = func(string) ([]*tunnel.HopConfig, error) {
		return nil, fmt.Errorf("resolver broken by test")
	}

	// ServiceHost != "local" so startForward routes through hopResolver and fails.
	fwd := &db.Forward{
		Type:        db.LocalListenToRemote,
		ListenHost:  "local",
		ListenAddr:  fmt.Sprintf("127.0.0.1:%d", freePort(t)),
		ServiceHost: "unreachable-test-host",
		ServiceAddr: "127.0.0.1:1",
	}
	require.NoError(t, database.CreateForward(fwd))

	fake := newFakeForward(forwarding.StatusError)
	injectForward(t, svc, fwd.ID, fake)

	svc.sync()

	// Old fake stopped once, nothing re-registered.
	assert.Equal(t, 1, fake.stopCalls())
	svc.mu.RLock()
	_, exists := svc.forwards[fwd.ID]
	failures := svc.consecutiveFailures[fwd.ID]
	svc.mu.RUnlock()
	assert.False(t, exists, "failed rebuild must not leave a forward in the map")
	assert.Equal(t, 1, failures, "consecutiveFailures must increase after failed rebuild")

	// Database carries the error and a non-empty message.
	status, err := database.GetStatus(fwd.ID)
	require.NoError(t, err)
	assert.Equal(t, "error", status.Status)
	assert.NotEmpty(t, status.ErrorMessage)

	// Second sync inside the backoff window must not retry (no double Stop,
	// counter unchanged).
	svc.sync()
	assert.Equal(t, 1, fake.stopCalls(), "backoff must suppress an immediate retry")
	svc.mu.RLock()
	failuresAfter := svc.consecutiveFailures[fwd.ID]
	svc.mu.RUnlock()
	assert.Equal(t, 1, failuresAfter, "suppressed retry must not bump the counter")
}

// TestSync_DoesNotRebuildHealthyForward guards against the "sync kills a
// healthy forward" incident: only StatusError forwards enter the rebuild
// path; a StatusRunning peer must survive untouched.
func TestSync_DoesNotRebuildHealthyForward(t *testing.T) {
	database := setupTestDB(t)
	svc, err := New(database)
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Stop() })

	svc.hopResolver = func(string) ([]*tunnel.HopConfig, error) {
		return nil, fmt.Errorf("resolver broken by test")
	}

	// Both must exist in the database, otherwise sync treats them as deleted.
	healthy := &db.Forward{
		Type:        db.LocalListenToRemote,
		ListenHost:  "local",
		ListenAddr:  fmt.Sprintf("127.0.0.1:%d", freePort(t)),
		ServiceHost: "local",
		ServiceAddr: "127.0.0.1:1",
	}
	errored := &db.Forward{
		Type:        db.LocalListenToRemote,
		ListenHost:  "local",
		ListenAddr:  fmt.Sprintf("127.0.0.1:%d", freePort(t)),
		ServiceHost: "unreachable-test-host",
		ServiceAddr: "127.0.0.1:1",
	}
	require.NoError(t, database.CreateForward(healthy))
	require.NoError(t, database.CreateForward(errored))

	healthyFake := newFakeForward(forwarding.StatusRunning)
	erroredFake := newFakeForward(forwarding.StatusError)
	injectForward(t, svc, healthy.ID, healthyFake)
	injectForward(t, svc, errored.ID, erroredFake)

	svc.sync()

	// Healthy forward: same instance, never stopped.
	svc.mu.RLock()
	wrapper, exists := svc.forwards[healthy.ID]
	svc.mu.RUnlock()
	require.True(t, exists, "healthy forward must stay in the map")
	assert.Same(t, healthyFake, wrapper.Forward, "healthy forward must not be replaced")
	assert.Equal(t, 0, healthyFake.stopCalls(), "healthy forward must not be stopped")
	assert.Equal(t, forwarding.StatusRunning, healthyFake.Status())

	// Errored peer did go through rebuild (sanity check the test exercised it).
	assert.Equal(t, 1, erroredFake.stopCalls())
}
