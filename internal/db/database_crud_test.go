package db

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"
)

// TestDatabase_CRUDRoundTrip covers Create→Get→List→Update→Delete in one pass.
// It guards field round-trip (gorm column mapping), the BeforeCreate hook
// (generated ID + timestamps) and the BeforeUpdate hook (UpdatedAt bump).
func TestDatabase_CRUDRoundTrip(t *testing.T) {
	database := NewTestDB(t)

	forward := &Forward{
		Type:        LocalListenToRemote,
		ListenHost:  "local",
		ListenAddr:  "127.0.0.1:8888",
		ServiceHost: "vmr.u24",
		ServiceAddr: "127.0.0.1:8888",
		MaxConns:    3,
		Description: "round trip",
	}

	if err := database.CreateForward(forward); err != nil {
		t.Fatalf("CreateForward failed: %v", err)
	}
	if forward.ID == "" {
		t.Fatal("expected BeforeCreate hook to generate an ID, got empty")
	}

	got, err := database.GetForward(forward.ID)
	if err != nil {
		t.Fatalf("GetForward failed: %v", err)
	}
	if got.Type != LocalListenToRemote ||
		got.ListenHost != "local" ||
		got.ListenAddr != "127.0.0.1:8888" ||
		got.ServiceHost != "vmr.u24" ||
		got.ServiceAddr != "127.0.0.1:8888" ||
		got.MaxConns != 3 ||
		got.Description != "round trip" {
		t.Errorf("field round-trip mismatch, got %+v", got)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Errorf("expected non-zero timestamps from BeforeCreate hook, got created=%v updated=%v", got.CreatedAt, got.UpdatedAt)
	}

	// ListForwards sees the created record
	list, err := database.ListForwards()
	if err != nil {
		t.Fatalf("ListForwards failed: %v", err)
	}
	if len(list) != 1 || list[0].ID != forward.ID {
		t.Errorf("expected 1 forward with ID %s, got %+v", forward.ID, list)
	}

	// UpdateForward persists field changes and bumps UpdatedAt
	time.Sleep(10 * time.Millisecond)
	got.Description = "updated"
	got.MaxConns = 5
	if err := database.UpdateForward(got); err != nil {
		t.Fatalf("UpdateForward failed: %v", err)
	}

	updated, err := database.GetForward(forward.ID)
	if err != nil {
		t.Fatalf("GetForward after update failed: %v", err)
	}
	if updated.Description != "updated" || updated.MaxConns != 5 {
		t.Errorf("update not persisted, got %+v", updated)
	}
	if !updated.UpdatedAt.After(updated.CreatedAt) {
		t.Errorf("expected UpdatedAt %v to be after CreatedAt %v", updated.UpdatedAt, updated.CreatedAt)
	}

	// DeleteForward is a hard delete: record is gone afterwards
	if err := database.DeleteForward(forward.ID); err != nil {
		t.Fatalf("DeleteForward failed: %v", err)
	}
	if _, err := database.GetForward(forward.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("expected gorm.ErrRecordNotFound after delete, got %v", err)
	}
}

// TestDatabase_GetForward_NotFound verifies that a missing forward returns
// gorm.ErrRecordNotFound so callers can distinguish "absent" from other DB errors.
func TestDatabase_GetForward_NotFound(t *testing.T) {
	database := NewTestDB(t)

	_, err := database.GetForward("does-not-exist")
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("expected gorm.ErrRecordNotFound, got %v", err)
	}
}

// TestDatabase_CleanStatuses verifies CleanStatuses wipes only the
// forward_status table and leaves forwards untouched.
func TestDatabase_CleanStatuses(t *testing.T) {
	database := NewTestDB(t)

	for i := 0; i < 2; i++ {
		forward := &Forward{
			ID:          fmt.Sprintf("fwd-clean-statuses-%d", i),
			Type:        LocalListenToRemote,
			ListenHost:  "local",
			ListenAddr:  "127.0.0.1:9000",
			ServiceHost: "remote",
			ServiceAddr: "127.0.0.1:9001",
		}
		if err := database.CreateForward(forward); err != nil {
			t.Fatalf("CreateForward failed: %v", err)
		}
		status := &ForwardStatus{
			ForwardID:     forward.ID,
			Status:        "running",
			LastHeartbeat: time.Now(),
		}
		if err := database.CreateOrUpdateStatus(status); err != nil {
			t.Fatalf("CreateOrUpdateStatus failed: %v", err)
		}
	}

	if err := database.CleanStatuses(); err != nil {
		t.Fatalf("CleanStatuses failed: %v", err)
	}

	statuses, err := database.ListStatuses()
	if err != nil {
		t.Fatalf("ListStatuses failed: %v", err)
	}
	if len(statuses) != 0 {
		t.Errorf("expected 0 statuses after CleanStatuses, got %d", len(statuses))
	}

	forwards, err := database.ListForwards()
	if err != nil {
		t.Fatalf("ListForwards failed: %v", err)
	}
	if len(forwards) != 2 {
		t.Errorf("CleanStatuses must not touch forwards, expected 2, got %d", len(forwards))
	}
}

// TestDatabase_ListStatuses verifies empty-table and multi-record listing.
func TestDatabase_ListStatuses(t *testing.T) {
	database := NewTestDB(t)

	statuses, err := database.ListStatuses()
	if err != nil {
		t.Fatalf("ListStatuses on empty table failed: %v", err)
	}
	if len(statuses) != 0 {
		t.Errorf("expected 0 statuses on empty table, got %d", len(statuses))
	}

	for i := 0; i < 3; i++ {
		status := &ForwardStatus{
			ForwardID:     fmt.Sprintf("fwd-list-statuses-%d", i),
			Status:        "running",
			LastHeartbeat: time.Now(),
		}
		if err := database.CreateOrUpdateStatus(status); err != nil {
			t.Fatalf("CreateOrUpdateStatus failed: %v", err)
		}
	}

	statuses, err = database.ListStatuses()
	if err != nil {
		t.Fatalf("ListStatuses failed: %v", err)
	}
	if len(statuses) != 3 {
		t.Errorf("expected 3 statuses, got %d", len(statuses))
	}
}
