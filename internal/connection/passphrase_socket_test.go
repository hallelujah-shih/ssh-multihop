package connection

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPassphraseSocket_Basic(t *testing.T) {
	// Create temporary socket path
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	// Create and start passphrase socket
	ps := NewPassphraseSocket(socketPath)
	if err := ps.Start(); err != nil {
		t.Fatalf("Failed to start passphrase socket: %v", err)
	}
	defer ps.Stop()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Test 1: Send passphrase
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to connect to socket: %v", err)
	}

	testFingerprint := "SHA256:abc123"
	testPassphrase := "mysecret"

	// Send: <fingerprint> <passphrase>
	_, _ = fmt.Fprintf(conn, "%s %s\n", testFingerprint, testPassphrase)

	// Read response
	response, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}
	defer func() { _ = conn.Close() }()

	response = strings.TrimSpace(response)
	if response != "OK" {
		t.Errorf("Expected OK, got %s", response)
	}

	// Test 2: Verify passphrase was stored
	passphrase, ok := ps.GetPassphrase(testFingerprint)
	if !ok {
		t.Error("Passphrase not found")
	}
	if passphrase != testPassphrase {
		t.Errorf("Expected passphrase %s, got %s", testPassphrase, passphrase)
	}
}

func TestPassphraseSocket_Multiple(t *testing.T) {
	// Create temporary socket path
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	// Create and start passphrase socket
	ps := NewPassphraseSocket(socketPath)
	if err := ps.Start(); err != nil {
		t.Fatalf("Failed to start passphrase socket: %v", err)
	}
	defer ps.Stop()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Send multiple passphrases
	testCases := []struct {
		fingerprint string
		passphrase  string
	}{
		{"SHA256:abc123", "pass1"},
		{"SHA256:def456", "pass2"},
		{"SHA256:ghi789", "pass3"},
	}

	for _, tc := range testCases {
		conn, err := net.Dial("unix", socketPath)
		if err != nil {
			t.Fatalf("Failed to connect to socket: %v", err)
		}

		_, _ = fmt.Fprintf(conn, "%s %s\n", tc.fingerprint, tc.passphrase)

		response, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			t.Fatalf("Failed to read response: %v", err)
		}
		_ = conn.Close()

		response = strings.TrimSpace(response)
		if response != "OK" {
			t.Errorf("Expected OK, got %s", response)
		}
	}

	// Verify all passphrases were stored
	for _, tc := range testCases {
		passphrase, ok := ps.GetPassphrase(tc.fingerprint)
		if !ok {
			t.Errorf("Passphrase not found for %s", tc.fingerprint)
			continue
		}
		if passphrase != tc.passphrase {
			t.Errorf("Expected passphrase %s, got %s", tc.passphrase, passphrase)
		}
	}
}

func TestPassphraseSocket_InvalidFormat(t *testing.T) {
	// Create temporary socket path
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	// Create and start passphrase socket
	ps := NewPassphraseSocket(socketPath)
	if err := ps.Start(); err != nil {
		t.Fatalf("Failed to start passphrase socket: %v", err)
	}
	defer ps.Stop()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Test invalid format (missing passphrase)
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to connect to socket: %v", err)
	}

	_, _ = fmt.Fprintf(conn, "SHA256:abc123\n")

	response, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}
	_ = conn.Close()

	response = strings.TrimSpace(response)
	if !strings.HasPrefix(response, "ERROR") {
		t.Errorf("Expected ERROR response, got %s", response)
	}
}

func TestPassphraseSocket_SocketPermissions(t *testing.T) {
	// Create temporary socket path
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	// Create and start passphrase socket
	ps := NewPassphraseSocket(socketPath)
	if err := ps.Start(); err != nil {
		t.Fatalf("Failed to start passphrase socket: %v", err)
	}
	defer ps.Stop()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Check socket permissions
	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("Failed to stat socket: %v", err)
	}

	// Check mode (0600 = owner read/write only)
	mode := info.Mode()
	if mode.Perm() != 0600 {
		t.Errorf("Expected socket permissions 0600, got %04o", mode.Perm())
	}
}

func TestPassphraseSocket_Stop(t *testing.T) {
	// Create temporary socket path
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	// Create and start passphrase socket
	ps := NewPassphraseSocket(socketPath)
	if err := ps.Start(); err != nil {
		t.Fatalf("Failed to start passphrase socket: %v", err)
	}

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Stop socket
	ps.Stop()

	// Verify socket is removed
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Error("Socket file still exists after Stop()")
	}
}
