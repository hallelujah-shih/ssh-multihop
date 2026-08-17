package service

import (
	"testing"
	"time"

	"github.com/hallelujah-shih/ssh-multihop/internal/db"
	"github.com/hallelujah-shih/ssh-multihop/internal/forwarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// injectStoppedForward inserts an unstarted (StatusStopped) forward into the
// service's in-memory map without touching SSH.
func injectStoppedForward(t *testing.T, svc *ForwardService, id string) {
	t.Helper()
	lf := forwarding.NewLocalListenToRemote("127.0.0.1:19999", "127.0.0.1:19998", id, nil, nil, nil)
	svc.mu.Lock()
	svc.forwards[id] = ForwardWrapper{Type: db.LocalListenToRemote, LocalListenToRemote: lf}
	svc.mu.Unlock()
}

func TestGetStatus_MergesInMemoryState(t *testing.T) {
	setupTestLogger()
	database := setupTestDB(t)

	svc, err := New(database)
	require.NoError(t, err)

	id := "local_listen_to_remote-local-127.0.0.1:1-x-127.0.0.1:2"
	stale := time.Now().Add(-72 * time.Hour)
	require.NoError(t, database.CreateOrUpdateStatus(&db.ForwardStatus{
		ForwardID:     id,
		Status:        "running",
		LastHeartbeat: stale,
	}))

	injectStoppedForward(t, svc, id)

	status, err := svc.GetStatus(id)
	require.NoError(t, err)
	assert.Equal(t, "stopped", status.Status, "in-memory status must override stale DB row")
}

func TestGetStatus_PassesThroughWhenNotLoaded(t *testing.T) {
	setupTestLogger()
	database := setupTestDB(t)

	svc, err := New(database)
	require.NoError(t, err)

	id := "local_listen_to_remote-local-127.0.0.1:3-x-127.0.0.1:4"
	stale := time.Now().Add(-72 * time.Hour)
	require.NoError(t, database.CreateOrUpdateStatus(&db.ForwardStatus{
		ForwardID:     id,
		Status:        "running",
		LastHeartbeat: stale,
	}))

	status, err := svc.GetStatus(id)
	require.NoError(t, err)
	assert.Equal(t, "running", status.Status)
	assert.True(t, stale.Equal(status.LastHeartbeat), "DB row must pass through untouched")
}

func TestListStatuses_MergesInMemoryState(t *testing.T) {
	setupTestLogger()
	database := setupTestDB(t)

	svc, err := New(database)
	require.NoError(t, err)

	liveID := "local_listen_to_remote-local-127.0.0.1:5-x-127.0.0.1:6"
	ghostID := "local_listen_to_remote-local-127.0.0.1:7-x-127.0.0.1:8"
	stale := time.Now().Add(-72 * time.Hour)
	for _, id := range []string{liveID, ghostID} {
		require.NoError(t, database.CreateOrUpdateStatus(&db.ForwardStatus{
			ForwardID:     id,
			Status:        "running",
			LastHeartbeat: stale,
		}))
	}

	injectStoppedForward(t, svc, liveID)

	statuses, err := svc.ListStatuses()
	require.NoError(t, err)

	byID := map[string]db.ForwardStatus{}
	for _, st := range statuses {
		byID[st.ForwardID] = st
	}
	assert.Equal(t, "stopped", byID[liveID].Status)
	assert.Equal(t, "running", byID[ghostID].Status)
	assert.True(t, stale.Equal(byID[ghostID].LastHeartbeat), "DB row must pass through untouched")
}
