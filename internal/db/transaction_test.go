package db

import (
	"fmt"
	"os"
	"testing"
	"time"

	"gorm.io/gorm"
)

// TestDatabase_CreateForwardWithStatus tests that both Forward and Status are created atomically
func TestDatabase_CreateForwardWithStatus(t *testing.T) {
	// Create temporary database
	dbFile := "test_transaction.db"
	defer func() {
		if err := os.Remove(dbFile); err != nil {
			t.Logf("Failed to remove test database file: %v", err)
		}
	}()

	db, err := New(Config{Path: dbFile})
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Logf("Failed to close database: %v", err)
		}
	}()

	// Test successful transaction
	forward := &Forward{
		ID:          "test-forward-1",
		Type:        LocalListenToRemote,
		ListenHost:  "local",
		ServiceHost: "remote",
		ListenAddr:  "127.0.0.1:8080",
		ServiceAddr: "127.0.0.1:8081",
	}

	status := &ForwardStatus{
		ForwardID:     "test-forward-1",
		Status:        "pending",
		LastHeartbeat: time.Now(),
	}

	// Use transaction to create both
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(forward).Error; err != nil {
			return err
		}
		if err := tx.Create(status).Error; err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		t.Fatalf("Transaction failed: %v", err)
	}

	// Verify both records exist
	retrievedForward, err := db.GetForward("test-forward-1")
	if err != nil {
		t.Fatalf("Failed to retrieve forward: %v", err)
	}
	if retrievedForward.ID != "test-forward-1" {
		t.Errorf("Expected forward ID 'test-forward-1', got '%s'", retrievedForward.ID)
	}

	retrievedStatus, err := db.GetStatus("test-forward-1")
	if err != nil {
		t.Fatalf("Failed to retrieve status: %v", err)
	}
	if retrievedStatus.Status != "pending" {
		t.Errorf("Expected status 'pending', got '%s'", retrievedStatus.Status)
	}
}

// TestDatabase_TransactionRollback tests that transaction rolls back on error
func TestDatabase_TransactionRollback(t *testing.T) {
	// Create temporary database
	dbFile := "test_transaction_rollback.db"
	defer func() {
		if err := os.Remove(dbFile); err != nil {
			t.Logf("Failed to remove test database file: %v", err)
		}
	}()

	db, err := New(Config{Path: dbFile})
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Logf("Failed to close database: %v", err)
		}
	}()

	// Test transaction rollback
	forward := &Forward{
		ID:          "test-forward-2",
		Type:        LocalListenToRemote,
		ListenHost:  "local",
		ServiceHost: "remote",
		ListenAddr:  "127.0.0.1:8080",
		ServiceAddr: "127.0.0.1:8081",
	}

	status := &ForwardStatus{
		ForwardID:     "test-forward-2",
		Status:        "pending",
		LastHeartbeat: time.Now(),
	}

	// Use transaction but fail on purpose
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(forward).Error; err != nil {
			return err
		}
		// Intentionally fail on status creation (invalid status)
		if err := tx.Create(status).Error; err != nil {
			return err
		}
		return nil
	})

	// In this case, both should succeed, so no error
	if err != nil {
		t.Fatalf("Transaction failed unexpectedly: %v", err)
	}

	// Test actual rollback scenario
	db2, err := New(Config{Path: "test_rollback.db"})
	defer func() {
		if err := os.Remove("test_rollback.db"); err != nil {
			t.Logf("Failed to remove test database file: %v", err)
		}
	}()
	if err != nil {
		t.Fatalf("Failed to create second database: %v", err)
	}
	defer func() {
		if err := db2.Close(); err != nil {
			t.Logf("Failed to close database: %v", err)
		}
	}()

	forward3 := &Forward{
		ID:          "test-forward-3",
		Type:        LocalListenToRemote,
		ListenHost:  "local",
		ServiceHost: "remote",
		ListenAddr:  "127.0.0.1:8080",
		ServiceAddr: "127.0.0.1:8081",
	}

	// Transaction that should fail
	err = db2.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(forward3).Error; err != nil {
			return err
		}
		// Return an error to trigger rollback
		return fmt.Errorf("intentional error")
	})

	if err == nil {
		t.Fatal("Expected transaction to fail, but it succeeded")
	}

	// Verify forward was NOT created due to rollback
	_, err = db2.GetForward("test-forward-3")
	if err == nil {
		t.Error("Expected forward to not exist due to rollback, but it was found")
	}
}
