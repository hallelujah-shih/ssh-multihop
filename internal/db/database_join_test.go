package db

import (
	"testing"
	"time"
)

// TestListForwardsWithStatus tests the LEFT JOIN query that loads forwards with statuses
func TestListForwardsWithStatus(t *testing.T) {
	// Create in-memory database for testing
	database, err := New(Config{Path: ":memory:"})
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Logf("Failed to close database: %v", err)
		}
	}()

	// Create test forwards
	forward1 := &Forward{
		Type:        LocalListenToRemote,
		ListenHost:  "local",
		ListenAddr:  "127.0.0.1:9001",
		ServiceHost: "example.com",
		ServiceAddr: "127.0.0.1:80",
		Description: "Test forward 1",
	}

	forward2 := &Forward{
		Type:        RemoteListenToLocal,
		ListenHost:  "example.com",
		ListenAddr:  "127.0.0.1:9002",
		ServiceHost: "local",
		ServiceAddr: "127.0.0.1:8080",
		Description: "Test forward 2",
	}

	forward3 := &Forward{
		Type:        RemoteListenToRemote,
		ListenHost:  "host1.example.com",
		ListenAddr:  "127.0.0.1:9003",
		ServiceHost: "host2.example.com",
		ServiceAddr: "127.0.0.1:3306",
		Description: "Test forward 3 (no status)",
	}

	// Create forwards in database
	if err := database.CreateForward(forward1); err != nil {
		t.Fatalf("Failed to create forward1: %v", err)
	}
	if err := database.CreateForward(forward2); err != nil {
		t.Fatalf("Failed to create forward2: %v", err)
	}
	if err := database.CreateForward(forward3); err != nil {
		t.Fatalf("Failed to create forward3: %v", err)
	}

	// Create status records for forward1 and forward2 (forward3 has no status)
	status1 := &ForwardStatus{
		ForwardID:     forward1.ID,
		Status:        "running",
		LastHeartbeat: time.Now(),
		ErrorMessage:  "",
	}

	status2 := &ForwardStatus{
		ForwardID:     forward2.ID,
		Status:        "error",
		LastHeartbeat: time.Now().Add(-1 * time.Hour),
		ErrorMessage:  "Connection refused",
	}

	if err := database.CreateOrUpdateStatus(status1); err != nil {
		t.Fatalf("Failed to create status1: %v", err)
	}
	if err := database.CreateOrUpdateStatus(status2); err != nil {
		t.Fatalf("Failed to create status2: %v", err)
	}

	// Test ListForwardsWithStatus
	results, err := database.ListForwardsWithStatus()
	if err != nil {
		t.Fatalf("ListForwardsWithStatus failed: %v", err)
	}

	// Verify all forwards are returned
	if len(results) != 3 {
		t.Errorf("Expected 3 forwards, got %d", len(results))
	}

	// Verify forward1 has running status
	found1 := false
	for _, result := range results {
		if result.ForwardID == forward1.ID {
			found1 = true
			if result.Status == nil {
				t.Errorf("Forward1 expected to have status, got nil")
			} else if *result.Status != "running" {
				t.Errorf("Forward1 expected status 'running', got '%s'", *result.Status)
			}
		}
	}
	if !found1 {
		t.Errorf("Forward1 not found in results")
	}

	// Verify forward2 has error status
	found2 := false
	for _, result := range results {
		if result.ForwardID == forward2.ID {
			found2 = true
			if result.Status == nil {
				t.Errorf("Forward2 expected to have status, got nil")
			} else if *result.Status != "error" {
				t.Errorf("Forward2 expected status 'error', got '%s'", *result.Status)
			} else if result.ErrorMessage == nil || *result.ErrorMessage != "Connection refused" {
				t.Errorf("Forward2 expected error message 'Connection refused', got '%v'", result.ErrorMessage)
			}
		}
	}
	if !found2 {
		t.Errorf("Forward2 not found in results")
	}

	// Verify forward3 has no status (LEFT JOIN should still return the forward)
	found3 := false
	for _, result := range results {
		if result.ForwardID == forward3.ID {
			found3 = true
			if result.Status != nil {
				t.Errorf("Forward3 expected to have no status (nil), got %+v", result.Status)
			}
		}
	}
	if !found3 {
		t.Errorf("Forward3 not found in results")
	}
}

// TestListForwardsWithStatusEmpty tests the method with empty database
func TestListForwardsWithStatusEmpty(t *testing.T) {
	database, err := New(Config{Path: ":memory:"})
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Logf("Failed to close database: %v", err)
		}
	}()

	results, err := database.ListForwardsWithStatus()
	if err != nil {
		t.Fatalf("ListForwardsWithStatus failed: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("Expected 0 forwards, got %d", len(results))
	}
}

// TestListForwardsWithStatusOnlyForwards tests with forwards but no status records
func TestListForwardsWithStatusOnlyForwards(t *testing.T) {
	database, err := New(Config{Path: ":memory:"})
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Logf("Failed to close database: %v", err)
		}
	}()

	// Create a forward without status
	forward := &Forward{
		Type:        LocalListenToRemote,
		ListenHost:  "local",
		ListenAddr:  "127.0.0.1:9001",
		ServiceHost: "example.com",
		ServiceAddr: "127.0.0.1:80",
	}

	if err := database.CreateForward(forward); err != nil {
		t.Fatalf("Failed to create forward: %v", err)
	}

	results, err := database.ListForwardsWithStatus()
	if err != nil {
		t.Fatalf("ListForwardsWithStatus failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 forward, got %d", len(results))
	}

	if results[0].Status != nil {
		t.Errorf("Expected nil status for forward without status record, got '%s'", *results[0].Status)
	}
}
