package service

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

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

// setForwardStatus writes a status row for a forward, as the sync loop would.
func setForwardStatus(t *testing.T, database *db.Database, id, status string) {
	t.Helper()
	require.NoError(t, database.CreateOrUpdateStatus(&db.ForwardStatus{
		ForwardID:     id,
		Status:        status,
		LastHeartbeat: time.Now(),
	}))
}

// TestLeakRecoveryAllowed_SiblingGuard pins the flip-flop guard: a running
// sibling that normalizes to the same listen bind must disable leak
// recovery, so two records sharing one remote bind cannot retract each
// other's listener. ":p" and "0.0.0.0:p" must normalize equal (R1 P2-5).
func TestLeakRecoveryAllowed_SiblingGuard(t *testing.T) {
	database := setupTestDB(t)
	svc, err := New(database)
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Stop() })

	port := freePort(t)
	f1 := &db.Forward{Type: db.RemoteListenToLocal, ListenHost: "local",
		ListenAddr: fmt.Sprintf(":%d", port), ServiceHost: "local", ServiceAddr: "127.0.0.1:1"}
	f2 := &db.Forward{Type: db.RemoteListenToLocal, ListenHost: "local",
		ListenAddr: fmt.Sprintf("0.0.0.0:%d", port), ServiceHost: "local", ServiceAddr: "127.0.0.1:1"}
	f3 := &db.Forward{Type: db.RemoteListenToLocal, ListenHost: "local",
		ListenAddr: fmt.Sprintf("127.0.0.1:%d", freePort(t)), ServiceHost: "local", ServiceAddr: "127.0.0.1:1"}
	require.NoError(t, database.CreateForward(f1))
	require.NoError(t, database.CreateForward(f2))
	require.NoError(t, database.CreateForward(f3))

	// f2 running on the same bind (after normalization) ⇒ f1 must not recover.
	setForwardStatus(t, database, f2.ID, "running")
	assert.False(t, svc.leakRecoveryAllowed(f1), "running sibling on the same bind must disable recovery")

	// f3: no same-bind sibling (its only sibling is on another port) ⇒ allowed.
	setForwardStatus(t, database, f1.ID, "running")
	assert.True(t, svc.leakRecoveryAllowed(f3), "no same-bind sibling must keep recovery enabled")

	// f2 not running anymore ⇒ f1 allowed again.
	setForwardStatus(t, database, f2.ID, "stopped")
	assert.True(t, svc.leakRecoveryAllowed(f1), "non-running sibling must keep recovery enabled")
}

// TestLeakRecoveryAllowed_TypeScope: an llr sibling on the same ListenAddr
// must not disable recovery — llr binds locally and holds no remote listen,
// so there is nothing it could flip-flop against.
func TestLeakRecoveryAllowed_TypeScope(t *testing.T) {
	database := setupTestDB(t)
	svc, err := New(database)
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Stop() })

	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	rll := &db.Forward{Type: db.RemoteListenToLocal, ListenHost: "local",
		ListenAddr: addr, ServiceHost: "local", ServiceAddr: "127.0.0.1:1"}
	llr := &db.Forward{Type: db.LocalListenToRemote, ListenHost: "local",
		ListenAddr: addr, ServiceHost: "local", ServiceAddr: "127.0.0.1:1"}
	require.NoError(t, database.CreateForward(rll))
	require.NoError(t, database.CreateForward(llr))
	setForwardStatus(t, database, llr.ID, "running")

	assert.True(t, svc.leakRecoveryAllowed(rll), "running llr sibling on the same bind must not disable recovery")
}

// TestLeakRecoveryAllowed_DBFailure: a failed forward listing must
// conservatively disable recovery — a flip-flop is worse than no auto-heal.
func TestLeakRecoveryAllowed_DBFailure(t *testing.T) {
	database := setupTestDB(t)
	svc, err := New(database)
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Stop() })
	require.NoError(t, database.Close())

	fwd := &db.Forward{Type: db.RemoteListenToLocal, ListenHost: "local",
		ListenAddr: "127.0.0.1:23458", ServiceHost: "local", ServiceAddr: "127.0.0.1:1"}
	assert.False(t, svc.leakRecoveryAllowed(fwd), "db failure must conservatively disable recovery")
}

// sameBindSibling builds two rll records sharing one listen bind. The
// sibling's ServiceAddr differs only because forward IDs are derived from
// the full tuple and must not collide.
func sameBindSibling(t *testing.T) (self, sib *db.Forward) {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	self = &db.Forward{Type: db.RemoteListenToLocal, ListenHost: "local",
		ListenAddr: addr, ServiceHost: "local", ServiceAddr: "127.0.0.1:1"}
	sib = &db.Forward{Type: db.RemoteListenToLocal, ListenHost: "local",
		ListenAddr: addr, ServiceHost: "local", ServiceAddr: "127.0.0.1:2"}
	return self, sib
}

// TestLeakRecoveryAllowed_SiblingPendingBlocked: a pending sibling on the
// same bind occupies — the sync loop can launch both records in the same
// pass, and two concurrent starts are exactly the flip-flop window.
func TestLeakRecoveryAllowed_SiblingPendingBlocked(t *testing.T) {
	database := setupTestDB(t)
	svc, err := New(database)
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Stop() })

	self, sib := sameBindSibling(t)
	require.NoError(t, database.CreateForward(self))
	require.NoError(t, database.CreateForward(sib))
	setForwardStatus(t, database, sib.ID, "pending")

	assert.False(t, svc.leakRecoveryAllowed(self), "pending sibling on the same bind must occupy")
}

// TestLeakRecoveryAllowed_SiblingErrorBlocked: an error sibling is one
// backoff tick away from a rebuild attempt — treat it as occupying.
func TestLeakRecoveryAllowed_SiblingErrorBlocked(t *testing.T) {
	database := setupTestDB(t)
	svc, err := New(database)
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Stop() })

	self, sib := sameBindSibling(t)
	require.NoError(t, database.CreateForward(self))
	require.NoError(t, database.CreateForward(sib))
	setForwardStatus(t, database, sib.ID, "error")

	assert.False(t, svc.leakRecoveryAllowed(self), "error sibling on the same bind must occupy")
}

// TestLeakRecoveryAllowed_SiblingStoppedAllowed: only a fully stopped
// sibling frees the bind for leak recovery.
func TestLeakRecoveryAllowed_SiblingStoppedAllowed(t *testing.T) {
	database := setupTestDB(t)
	svc, err := New(database)
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Stop() })

	self, sib := sameBindSibling(t)
	require.NoError(t, database.CreateForward(self))
	require.NoError(t, database.CreateForward(sib))
	setForwardStatus(t, database, sib.ID, "stopped")

	assert.True(t, svc.leakRecoveryAllowed(self), "stopped sibling must keep recovery enabled")
}

// TestLeakRecoveryAllowed_SiblingInFlightStartBlocked: an in-flight start
// has no meaningful DB status yet — pendingStarts is the only witness of
// the concurrent-start window, and it must occupy even for a stopped row.
func TestLeakRecoveryAllowed_SiblingInFlightStartBlocked(t *testing.T) {
	database := setupTestDB(t)
	svc, err := New(database)
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Stop() })

	self, sib := sameBindSibling(t)
	require.NoError(t, database.CreateForward(self))
	require.NoError(t, database.CreateForward(sib))
	setForwardStatus(t, database, sib.ID, "stopped")

	svc.pendingMu.Lock()
	svc.pendingStarts[sib.ID] = true
	svc.pendingMu.Unlock()
	defer func() {
		svc.pendingMu.Lock()
		delete(svc.pendingStarts, sib.ID)
		svc.pendingMu.Unlock()
	}()

	assert.False(t, svc.leakRecoveryAllowed(self), "in-flight sibling start must occupy")
}

// TestLeakRecoveryAllowed_UndecidableBindBlocked: a same-type sibling whose
// bind cannot be normalized (ParseAddress rejects hostnames) makes the
// comparison undecidable — conservatively occupy, with no raw-string
// fallback ("localhost:p" vs "127.0.0.1:p" must not slip through as unequal).
func TestLeakRecoveryAllowed_UndecidableBindBlocked(t *testing.T) {
	database := setupTestDB(t)
	svc, err := New(database)
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Stop() })

	port := freePort(t)
	self := &db.Forward{Type: db.RemoteListenToLocal, ListenHost: "local",
		ListenAddr: fmt.Sprintf("127.0.0.1:%d", port), ServiceHost: "local", ServiceAddr: "127.0.0.1:1"}
	sib := &db.Forward{Type: db.RemoteListenToLocal, ListenHost: "local",
		ListenAddr: fmt.Sprintf("localhost:%d", port), ServiceHost: "local", ServiceAddr: "127.0.0.1:1"}
	require.NoError(t, database.CreateForward(self))
	require.NoError(t, database.CreateForward(sib))
	setForwardStatus(t, database, sib.ID, "running")

	assert.False(t, svc.leakRecoveryAllowed(self),
		"undecidable sibling bind must occupy conservatively")
}

// TestLeakRecoveryAllowed_UndecidableSelfNoSameTypeSibling: with no
// same-type sibling at all, recovery stays enabled even if the self bind is
// unparseable — there is nothing the comparison could flip-flop against.
func TestLeakRecoveryAllowed_UndecidableSelfNoSameTypeSibling(t *testing.T) {
	database := setupTestDB(t)
	svc, err := New(database)
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Stop() })

	self := &db.Forward{Type: db.RemoteListenToLocal, ListenHost: "local",
		ListenAddr: fmt.Sprintf("localhost:%d", freePort(t)), ServiceHost: "local", ServiceAddr: "127.0.0.1:1"}
	llr := &db.Forward{Type: db.LocalListenToRemote, ListenHost: "local",
		ListenAddr: self.ListenAddr, ServiceHost: "local", ServiceAddr: "127.0.0.1:1"}
	require.NoError(t, database.CreateForward(self))
	require.NoError(t, database.CreateForward(llr))
	setForwardStatus(t, database, llr.ID, "running")

	assert.True(t, svc.leakRecoveryAllowed(self),
		"no same-type sibling must keep recovery enabled even with an unparseable self bind")
}

// allowLeakRecoveryOf reads the leak-recovery opt-in off a concrete forward.
// llr holds no remote listen and has no such field by design, so it reads
// as constant false.
func allowLeakRecoveryOf(t *testing.T, f forwarding.Forward) bool {
	t.Helper()
	switch v := f.(type) {
	case *forwarding.RemoteListenToLocal:
		return v.AllowLeakRecovery
	case *forwarding.RemoteListenToRemote:
		return v.AllowLeakRecovery
	case *forwarding.LocalListenToRemote:
		return false
	default:
		t.Fatalf("unexpected forward type %T", f)
		return false
	}
}

// TestBuildForward_LeakRecoveryWiring: buildForward is the single wiring
// point — rll/rlr get the sibling guard verdict, llr never opts in, with or
// without a same-bind sibling.
func TestBuildForward_LeakRecoveryWiring(t *testing.T) {
	database := setupTestDB(t)
	svc, err := New(database)
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Stop() })

	mk := func(typ db.ForwardType) *db.Forward {
		return &db.Forward{Type: typ, ListenHost: "local",
			ListenAddr:  fmt.Sprintf("127.0.0.1:%d", freePort(t)),
			ServiceHost: "local", ServiceAddr: "127.0.0.1:1"}
	}

	rll := mk(db.RemoteListenToLocal)
	rlr := mk(db.RemoteListenToRemote)
	llr := mk(db.LocalListenToRemote)
	sibRll := mk(db.RemoteListenToLocal)
	sibRll.ListenAddr = rll.ListenAddr
	sibRll.ServiceAddr = "127.0.0.1:2" // distinct ID; ServiceAddr is not part of the bind comparison
	sibRlr := mk(db.RemoteListenToRemote)
	sibRlr.ListenAddr = rlr.ListenAddr
	sibRlr.ServiceAddr = "127.0.0.1:2"
	for _, f := range []*db.Forward{rll, rlr, llr, sibRll, sibRlr} {
		require.NoError(t, database.CreateForward(f))
	}
	setForwardStatus(t, database, sibRll.ID, "running")
	setForwardStatus(t, database, sibRlr.ID, "running")

	// With a running same-bind sibling: rll/rlr must be opted out; llr N/A.
	f, err := svc.buildForward(rll)
	require.NoError(t, err)
	assert.False(t, allowLeakRecoveryOf(t, f), "rll with running same-bind sibling must not recover")
	f, err = svc.buildForward(rlr)
	require.NoError(t, err)
	assert.False(t, allowLeakRecoveryOf(t, f), "rlr with running same-bind sibling must not recover")
	f, err = svc.buildForward(llr)
	require.NoError(t, err)
	assert.False(t, allowLeakRecoveryOf(t, f), "llr must never opt into leak recovery")

	// Without a same-bind sibling: rll/rlr keep recovery enabled, llr stays out.
	for _, typ := range []db.ForwardType{db.RemoteListenToLocal, db.RemoteListenToRemote} {
		f, err := svc.buildForward(mk(typ))
		require.NoError(t, err, string(typ))
		assert.True(t, allowLeakRecoveryOf(t, f), "%s without same-bind sibling must keep recovery enabled", typ)
	}
	f, err = svc.buildForward(mk(db.LocalListenToRemote))
	require.NoError(t, err)
	assert.False(t, allowLeakRecoveryOf(t, f), "llr must never opt into leak recovery")
}
