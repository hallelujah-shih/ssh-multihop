package db

import (
	"fmt"
	"testing"
	"time"
)

// NewTestDB creates a unique in-memory SQLite database for a test.
// The shared-cache name keeps all pool connections on the same database
// and the nano timestamp isolates tests from each other.
func NewTestDB(t *testing.T) *Database {
	t.Helper()
	database, err := New(Config{
		Path: fmt.Sprintf("file:dbtest-%d.db?mode=memory&cache=shared", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}
