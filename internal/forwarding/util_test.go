package forwarding

import (
	"io"
	"net"
	"testing"
	"time"
)

// TestRandomHealthCheckInterval verifies that the random interval is within expected range
func TestRandomHealthCheckInterval(t *testing.T) {
	// Generate multiple intervals to test randomness
	minDuration := 15 * time.Second
	maxDuration := 30 * time.Second

	// Test 1000 random intervals
	for i := 0; i < 1000; i++ {
		interval := RandomHealthCheckInterval()

		// Verify interval is within expected range
		if interval < minDuration || interval > maxDuration {
			t.Errorf("Interval %d out of range: got %v, want [%v, %v]",
				i, interval, minDuration, maxDuration)
		}
	}

	t.Log("All 1000 random intervals are within [15s, 30s] range")
}

// TestRandomHealthCheckIntervalDistribution verifies distribution roughly covers the range
func TestRandomHealthCheckIntervalDistribution(t *testing.T) {
	// Collect 1000 samples
	samples := make([]time.Duration, 1000)
	for i := 0; i < 1000; i++ {
		samples[i] = RandomHealthCheckInterval()
	}

	// Count how many different values we got
	uniqueValues := make(map[time.Duration]bool)
	for _, s := range samples {
		uniqueValues[s] = true
	}

	// We expect at least 10 different values (15, 16, ..., 30 seconds)
	if len(uniqueValues) < 10 {
		t.Errorf("Expected at least 10 unique values, got %d", len(uniqueValues))
	}

	t.Logf("Found %d unique values out of 1000 samples", len(uniqueValues))
}

// TestBidirectionalCopyHandlesHalfClose verifies that bidirectionalCopy doesn't hang
// when one side closes (half-close scenario)
func TestBidirectionalCopyHandlesHalfClose(t *testing.T) {
	// Create two connected pipes
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()

	// Start bidirectional copy
	errCh := make(chan error, 2)
	go func() {
		errCh <- bidirectionalCopy(server, client)
	}()

	// Close one side after a short delay
	time.Sleep(100 * time.Millisecond)
	_ = client.Close()

	// Should return quickly, not hang
	select {
	case err := <-errCh:
		if err != nil && err != io.EOF {
			t.Logf("Copy ended with: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bidirectionalCopy hung on half-close")
	}
}
