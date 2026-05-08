package forwarding

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/hallelujah-shih/ssh-multihop/internal/db"
)

// findFreePort finds an available port for testing
func findFreePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("failed to find free port: %w", err)
	}
	defer func() { _ = listener.Close() }()

	addr := listener.Addr().(*net.TCPAddr)
	return addr.Port, nil
}

// createTestDB creates an in-memory database for testing
func createTestDB() (*db.Database, error) {
	testDB, err := db.New(db.Config{
		Path: ":memory:",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create test database: %w", err)
	}
	return testDB, nil
}

// MockForwardStatus is a mock implementation for testing database status updates
type MockForwardStatus struct {
	mu     sync.Mutex
	status map[string]string
	errors map[string]string
}

// NewMockForwardStatus creates a new mock status tracker
func NewMockForwardStatus() *MockForwardStatus {
	return &MockForwardStatus{
		status: make(map[string]string),
		errors: make(map[string]string),
	}
}

// GetStatus returns the current status for a forward ID
func (m *MockForwardStatus) GetStatus(forwardID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status[forwardID]
}

// GetError returns the error message for a forward ID
func (m *MockForwardStatus) GetError(forwardID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.errors[forwardID]
}

// SetStatus sets the status for a forward ID
func (m *MockForwardStatus) SetStatus(forwardID, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status[forwardID] = status
}

// SetError sets the error message for a forward ID
func (m *MockForwardStatus) SetError(forwardID, errorMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors[forwardID] = errorMsg
	m.status[forwardID] = "error"
}

// WaitForStatus waits for a specific status with timeout
func (m *MockForwardStatus) WaitForStatus(forwardID, expectedStatus string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if m.GetStatus(forwardID) == expectedStatus {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for status %s (current: %s)", expectedStatus, m.GetStatus(forwardID))
}
