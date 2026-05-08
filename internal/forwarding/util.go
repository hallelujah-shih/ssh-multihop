package forwarding

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net"
	"time"
)

// CleanupFunc is a function that cleans up resources
type CleanupFunc func()

// RandomHealthCheckInterval generates a random health check interval between 15-30 seconds
// This prevents "thundering herd" problem where all forwards check health simultaneously
func RandomHealthCheckInterval() time.Duration {
	// Random interval between 15 and 30 seconds
	minSeconds := 15
	maxSeconds := 30
	randomSeconds := minSeconds + rand.Intn(maxSeconds-minSeconds+1)
	return time.Duration(randomSeconds) * time.Second
}

// acceptConnections accepts incoming connections on a listener
//
// This is a common utility for LocalForward to accept connections
// and send them through a channel for handling.
//
// The context can be used to cancel the accept loop.
// Supports both TCP and Unix Domain Socket listeners.
// NOTE: Each Forward type now has its own inline acceptConnections implementation.

// bidirectionalCopy copies data bidirectionally between two connections
//
// This is a common utility for all Forward types to perform data forwarding.
//
// When either copy direction completes (success, error, or EOF), both connections
// are closed and the function returns immediately. This prevents hangs on
// half-close scenarios where one side closes but the other doesn't detect it.
func bidirectionalCopy(conn1, conn2 net.Conn) error {
	defer func() { _ = conn1.Close() }()
	defer func() { _ = conn2.Close() }()

	errCh := make(chan error, 2)

	// Copy conn1 -> conn2
	go func() {
		_, err := io.Copy(conn2, conn1)
		_ = conn2.Close() // Explicitly close on completion
		errCh <- err
	}()

	// Copy conn2 -> conn1
	go func() {
		_, err := io.Copy(conn1, conn2)
		_ = conn1.Close() // Explicitly close on completion
		errCh <- err
	}()

	// Wait for first direction to complete
	// The deferred Close() calls ensure cleanup happens
	return <-errCh
}

// waitForHealthCheck waits for a forward to become healthy
//
// This is used by StartAndWait to poll the status of a forward
// until it reaches Running state or times out.
func waitForHealthCheck(ctx context.Context, forward Forward, timeout time.Duration) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	consecutiveRunningChecks := 0

	for {
		select {
		case <-timeoutCtx.Done():
			return fmt.Errorf("timeout waiting for forward %s to establish", forward.String())

		case <-ticker.C:
			status := forward.Status()
			if status == StatusRunning {
				consecutiveRunningChecks++
				// Require 3 consecutive running checks (300ms) to ensure stability
				if consecutiveRunningChecks >= 3 {
					return nil
				}
			} else {
				// StatusStopped or StatusError - reset counter
				consecutiveRunningChecks = 0
			}
		}
	}
}

// isNormalCloseError checks if an error represents a normal connection closure.
// Returns true for EOF, closed network connections, and broken pipe errors
// which indicate the client or server closed the connection normally.
func isNormalCloseError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	normalClosePatterns := []string{
		"use of closed network connection",
		"broken pipe",
		"connection reset by peer",
	}

	// Check for EOF
	if err == io.EOF {
		return true
	}

	// Check for common normal close patterns
	for _, pattern := range normalClosePatterns {
		if contains(errStr, pattern) {
			return true
		}
	}

	return false
}

// contains checks if a string contains a substring (case-sensitive)
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
