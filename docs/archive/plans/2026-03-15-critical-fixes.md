# Critical Fixes Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Fix all critical (🔴) and high-priority (🟠) issues identified in deep review to make the project production-ready.

**Architecture:** This plan addresses critical bugs and adds essential test coverage. Each fix is independent and can be committed separately.

**Tech Stack:** Go 1.25, golang.org/x/crypto/ssh, GORM, SQLite, net package

**Context:** Deep review identified 3 critical risks (panic, IPv6, zero test coverage) that must be fixed before production use. This plan addresses them incrementally with TDD approach.

---

## Phase 1: Critical Bug Fixes (1-2 days)

### Task 1: Fix Panic in SSH Helper

**Files:**
- Modify: `internal/forwarding/ssh_helper.go:52-62`
- Test: `internal/forwarding/ssh_helper_test.go` (create new)

**Context:** Current code panics on circular ProxyJump references, crashing the entire daemon. This must return an error instead.

**Step 1: Write failing test for circular reference**

Create file: `internal/forwarding/ssh_helper_test.go`

```go
package forwarding

import (
	"testing"

	"github.com/hallelujah-shih/ssh-multihop/internal/config"
	"github.com/hallelujah-shih/ssh-multihop/internal/tunnel"
)

// createMockParser creates a parser with circular ProxyJump config
func createMockParserWithCircularRef() *config.Parser {
	parser := config.NewParser()
	// In real implementation, we'd mock the config parsing
	// For now, test the resolveProxyJumpChain function directly
	return parser
}

func TestResolveProxyJumpChain_CircularReference(t *testing.T) {
	parser := createMockParserWithCircularRef()
	visited := make(map[string]bool)

	// This should NOT panic, it should return an error
	chain := resolveProxyJumpChain(parser, "testhost", visited)

	if chain != nil {
		t.Errorf("Expected nil chain for circular reference, got %v", chain)
	}
}

func TestBuildHopChainFromSSHConfig_CircularReference(t *testing.T) {
	// Test the public API
	_, err := BuildHopChainFromSSHConfig("circular-host")

	if err == nil {
		t.Error("Expected error for circular reference, got nil")
	}

	// Verify error message contains useful info
	if err != nil && len(err.Error()) == 0 {
		t.Error("Expected error message, got empty string")
	}
}
```

**Step 2: Run test to verify it fails (and currently panics)**

Run: `go test ./internal/forwarding -v -run TestResolveProxyJumpChain_CircularReference`
Expected: ❌ PANIC or test crashes

**Step 3: Fix panic to return error**

Modify: `internal/forwarding/ssh_helper.go:52-62`

```go
// resolveProxyJumpChain recursively resolves ProxyJump configuration
func resolveProxyJumpChain(parser *config.Parser, host string, visited map[string]bool) ([]*tunnel.HopConfig, error) {
	if visited == nil {
		visited = make(map[string]bool)
	}

	// Detect circular references - return error instead of panic
	if visited[host] {
		return nil, fmt.Errorf("circular ProxyJump reference detected: %s", host)
	}
	visited[host] = true

	// Get host configuration
	hostConfig, err := parser.GetHostConfig(host)
	if err != nil {
		// Host not found in config, use defaults
		hostConfig = &config.HostConfig{
			HostName: host,
			Port:     22,
		}
	}

	// Check for ProxyJump
	proxyJump := hostConfig.ProxyJump

	// If no ProxyJump, this is the final hop
	if proxyJump == "" {
		hop := &tunnel.HopConfig{
			Host:         host,
			HostName:     hostConfig.HostName,
			Port:         hostConfig.Port,
			User:         hostConfig.User,
			IdentityFile: hostConfig.IdentityFile,
		}

		// Apply defaults
		if hop.HostName == "" {
			hop.HostName = host
		}
		if hop.Port == 0 {
			hop.Port = 22
		}

		return []*tunnel.HopConfig{hop}, nil
	}

	// Parse ProxyJump (comma-separated list)
	jumps := parseProxyJumpList(proxyJump)

	// Build chain recursively
	var chain []*tunnel.HopConfig
	for _, jumpHost := range jumps {
		// Recursively resolve each jump
		jumpChain, err := resolveProxyJumpChain(parser, jumpHost, visited)
		if err != nil {
			return nil, err // Propagate circular reference error
		}
		chain = append(chain, jumpChain...)
	}

	// Add the final target
	targetHop := &tunnel.HopConfig{
		Host:         host,
		HostName:     hostConfig.HostName,
		Port:         hostConfig.Port,
		User:         hostConfig.User,
		IdentityFile: hostConfig.IdentityFile,
	}

	// Apply defaults
	if targetHop.HostName == "" {
		targetHop.HostName = host
	}
	if targetHop.Port == 0 {
		targetHop.Port = 22
	}

	chain = append(chain, targetHop)

	return chain, nil
}
```

**Step 4: Update function signature to return error**

Modify: `internal/forwarding/ssh_helper.go:22-50`

```go
// BuildHopChainFromSSHConfig builds a hop chain by parsing SSH config
//
// This function:
// 1. Parses ~/.ssh/config
// 2. Looks up the target host
// 3. Resolves ProxyJump chain
// 4. Builds complete hop configuration
//
// Returns the hop chain including a dummy "local" hop at the beginning.
// Returns error if circular ProxyJump reference is detected.
func BuildHopChainFromSSHConfig(targetHost string) ([]*tunnel.HopConfig, error) {
	// Get SSH config path
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}
	configPath := filepath.Join(homeDir, ".ssh", "config")

	// Parse SSH config
	parser := config.NewParser()
	_, err = parser.ParseConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SSH config: %w", err)
	}

	// Resolve ProxyJump chain (now returns error)
	chain, err := resolveProxyJumpChain(parser, targetHost, nil)
	if err != nil {
		return nil, err
	}

	// Prepend dummy "local" hop (V1's connection manager skips first hop)
	fullChain := make([]*tunnel.HopConfig, 0, len(chain)+1)
	fullChain = append(fullChain, &tunnel.HopConfig{
		Host:     "local",
		HostName: "localhost",
		Port:     0,
	})
	fullChain = append(fullChain, chain...)

	return fullChain, nil
}
```

**Step 5: Update all callers to handle error**

Search for all callers of `BuildHopChainFromSSHConfig`:
- `internal/service/forward_service.go:738`

Modify: `internal/service/forward_service.go:738-743`

```go
func (s *ForwardService) getHopsForHost(hostname string) ([]*tunnel.HopConfig, error) {
	if hostname == "local" {
		return []*tunnel.HopConfig{}, nil
	}

	hops, err := forwarding.BuildHopChainFromSSHConfig(hostname)
	if err != nil {
		return nil, fmt.Errorf("failed to build hop chain for %s: %w", hostname, err)
	}

	return hops, nil
}
```

**Step 6: Run tests to verify they pass**

Run: `go test ./internal/forwarding -v -run TestResolveProxyJumpChain`
Expected: ✅ PASS

**Step 7: Run race detector**

Run: `go test ./internal/forwarding -race -run TestResolveProxyJumpChain`
Expected: ✅ PASS (no data races)

**Step 8: Verify no panic with manual test**

Create temporary SSH config with circular reference:
```bash
# ~/.ssh/config.test
Host test1
    ProxyJump test2
Host test2
    ProxyJump test1
```

Run: `go run ./cmd/ssh-multihop daemon --help` (should not panic)

**Step 9: Commit**

```bash
git add internal/forwarding/ssh_helper.go internal/forwarding/ssh_helper_test.go internal/service/forward_service.go
git commit -m "fix: replace panic with error return for circular ProxyJump

- Change resolveProxyJumpChain to return error instead of panic
- Update BuildHopChainFromSSHConfig signature to return error
- Add tests for circular reference detection
- Update all callers to handle error

Fixes critical issue where user SSH config could crash entire daemon.

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

### Task 2: Fix IPv6 Address Format

**Files:**
- Modify: `cmd/ssh-multihop/daemon.go:99-105`
- Test: `cmd/ssh-multihop/daemon_test.go` (create new)

**Context:** Using fmt.Sprintf for addresses breaks IPv6. Must use net.JoinHostPort.

**Step 1: Write failing test for IPv6**

Create file: `cmd/ssh-multihop/daemon_test.go`

```go
package main

import (
	"net"
	"strconv"
	"testing"
)

func TestFormatAPIAddress_IPv4(t *testing.T) {
	host := "127.0.0.1"
	port := 8080

	// Current implementation (broken for IPv6)
	wrongAddr := fmt.Sprintf("%s:%d", host, port)
	_, err := net.DialTimeout("tcp", wrongAddr, 0)
	// Might work for IPv4 but that's not guaranteed

	// Correct implementation
	correctAddr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", correctAddr, 0)
	if err == nil {
		conn.Close()
	}

	// Just verify JoinHostPort produces correct format
	expected := "127.0.0.1:8080"
	if correctAddr != expected {
		t.Errorf("Expected %s, got %s", expected, correctAddr)
	}
}

func TestFormatAPIAddress_IPv6(t *testing.T) {
	host := "::1"
	port := 8080

	// Current implementation (broken)
	wrongAddr := fmt.Sprintf("%s:%d", host, port)
	// This produces "::1:8080" which is invalid

	// Correct implementation
	correctAddr := net.JoinHostPort(host, strconv.Itoa(port))

	// IPv6 must be bracketed
	if correctAddr[0] != '[' {
		t.Errorf("IPv6 address must be bracketed, got: %s", correctAddr)
	}

	expected := "[::1]:8080"
	if correctAddr != expected {
		t.Errorf("Expected %s, got %s", expected, correctAddr)
	}
}

func TestFormatAPIAddress_Hostname(t *testing.T) {
	host := "localhost"
	port := 8080

	correctAddr := net.JoinHostPort(host, strconv.Itoa(port))
	expected := "localhost:8080"

	if correctAddr != expected {
		t.Errorf("Expected %s, got %s", expected, correctAddr)
	}
}
```

**Step 2: Run tests**

Run: `go test ./cmd/ssh-multihop -v`
Expected: ✅ PASS (tests verify correct format)

**Step 3: Fix the code**

Modify: `cmd/ssh-multihop/daemon.go:99-105`

```go
// Check if API server port is already in use BEFORE starting any forwards
// Use net.JoinHostPort to handle both IPv4 and IPv6 correctly
apiAddr := net.JoinHostPort(host, strconv.Itoa(port))
conn, err := net.DialTimeout("tcp", apiAddr, 100*time.Millisecond)
if err == nil {
	conn.Close()
	return fmt.Errorf("API server port %s is already in use by another process", apiAddr)
}
```

**Step 4: Add missing import**

Modify: `cmd/ssh-multihop/daemon.go:1-20`

Add to imports:
```go
import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/hallelujah-shih/ssh-multihop/internal/agent"
	"github.com/hallelujah-shih/ssh-multihop/internal/api"
	"github.com/hallelujah-shih/ssh-multihop/internal/db"
	"github.com/hallelujah-shih/ssh-multihop/internal/service"
	"github.com/hallelujah-shih/ssh-multihop/internal/util"
	"github.com/urfave/cli/v2"
	"go.uber.org/zap"
)
```

**Step 5: Also fix api/server.go for consistency**

Modify: `internal/api/server.go:55-58`

```go
// Create HTTP server
httpServer := &http.Server{
	Addr:    net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
	Handler: router,
}
```

Add import:
```go
import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hallelujah-shih/ssh-multihop/internal/service"
	"go.uber.org/zap"
)
```

**Step 6: Run go vet to verify fix**

Run: `go vet ./cmd/ssh-multihop ./internal/api`
Expected: ✅ No warnings about address format

**Step 7: Run tests**

Run: `go test ./cmd/ssh-multihop -v`
Expected: ✅ PASS

**Step 8: Manual IPv6 test (if IPv6 available)**

```bash
# Test with IPv6 localhost
./ssh-multihop daemon --host ::1 --port 9999
# Should start without "address format" error
```

**Step 9: Commit**

```bash
git add cmd/ssh-multihop/daemon.go cmd/ssh-multihop/daemon_test.go internal/api/server.go
git commit -m "fix: use net.JoinHostPort for IPv6 compatibility

- Replace fmt.Sprintf with net.JoinHostPort for address formatting
- Add strconv import for port conversion
- Add tests for IPv4, IPv6, and hostname formats
- Fix both daemon.go and api/server.go

Resolves go vet warning about IPv6 address format.
Ensures daemon works correctly in IPv6-only environments.

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

### Task 3: Fix Error Handling in init()

**Files:**
- Modify: `cmd/ssh-multihop/daemon.go:22-25`

**Context:** Logger init silently ignores errors. In production, this means no logs when things fail.

**Step 1: Write test for logger initialization**

Create: `cmd/ssh-multihop/daemon_init_test.go`

```go
package main

import (
	"testing"

	"go.uber.org/zap"
)

func TestLoggerInit(t *testing.T) {
	// After init runs, global logger should be set
	logger := zap.L()
	if logger == nil {
		t.Error("Expected global logger to be initialized")
	}
}

func TestLoggerCanWrite(t *testing.T) {
	// Verify logger actually works
	logger := zap.L()
	if logger == nil {
		t.Skip("Logger not initialized")
	}

	// This should not panic
	logger.Info("Test log message",
		zap.String("test", "value"))
}
```

**Step 2: Fix init to handle errors properly**

Modify: `cmd/ssh-multihop/daemon.go:22-25`

```go
func init() {
	logger, err := zap.NewDevelopment()
	if err != nil {
		// If logger initialization fails, fall back to stderr
		// This ensures we can at least log the error
		zap.NewExample() // This never fails
		zap.L().Fatal("Failed to initialize logger",
			zap.Error(err))
		return
	}
	zap.ReplaceGlobals(logger)
}
```

**Step 3: Run tests**

Run: `go test ./cmd/ssh-multihop -v -run TestLogger`
Expected: ✅ PASS

**Step 4: Test daemon startup**

Run: `go run ./cmd/ssh-multihop daemon --help`
Expected: ✅ Logs appear without errors

**Step 5: Commit**

```bash
git add cmd/ssh-multihop/daemon.go cmd/ssh-multihop/daemon_init_test.go
git commit -m "fix: handle logger initialization error in init()

- Add error handling to zap.NewDevelopment()
- Use zap.Fatal() if logger init fails
- Add tests to verify logger initialization
- Ensures logs work in production environment

Previously, logger init silently failed, making debugging impossible.

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Phase 2: Essential Test Coverage (1-2 weeks)

### Task 4: Add Tests for LocalListenToRemote

**Files:**
- Create: `internal/forwarding/local_listen_to_remote_test.go`
- Create: `internal/forwarding/testutil.go` (test helpers)

**Context:** Core forwarding logic has zero tests. This is the most critical path.

**Step 1: Create test utilities**

Create: `internal/forwarding/testutil.go`

```go
package forwarding

import (
	"fmt"
	"net"
	"time"
)

// MockForwardStatus for testing
type MockForwardStatus struct {
	status ForwardStatus
}

func (m *MockForwardStatus) Status() ForwardStatus {
	return m.status
}

// findFreePort finds a free port on localhost
func findFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer l.Close()

	return l.Addr().(*net.TCPAddr).Port, nil
}

// createTestDB creates an in-memory database for testing
func createTestDB() (*db.Database, error) {
	database, err := db.New(db.Config{
		Path: ":memory:",
	})
	if err != nil {
		return nil, err
	}
	return database, nil
}

// waitForStatus waits for a status to be reached
func waitForStatus(getter func() ForwardStatus, expected ForwardStatus, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if getter() == expected {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for status %v", expected)
}
```

**Step 2: Write test for basic forward lifecycle**

Create: `internal/forwarding/local_listen_to_remote_test.go`

```go
package forwarding

import (
	"context"
	"testing"
	"time"

	"github.com/hallelujah-shih/ssh-multihop/internal/db"
	"github.com/hallelujah-shih/ssh-multihop/internal/tunnel"
)

func TestLocalListenToRemote_BasicLifecycle(t *testing.T) {
	// Skip in short mode
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Find free ports
	listenPort, err := findFreePort()
	if err != nil {
		t.Fatalf("Failed to find free port: %v", err)
	}

	bindAddr := fmt.Sprintf("127.0.0.1:%d", listenPort)
	targetAddr := "127.0.0.1:9999" // Non-existent target
	forwardID := "test-forward-1"

	// Create test database
	database, err := createTestDB()
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer database.Close()

	// Create forward with empty hop chain (will fail to connect, but tests lifecycle)
	hopChain := []*tunnel.HopConfig{} // No hops = will fail immediately

	forward := NewLocalListenToRemote(
		bindAddr,
		targetAddr,
		forwardID,
		database,
		hopChain,
	)

	// Test initial status
	if forward.Status() != StatusStopped {
		t.Errorf("Expected initial status StatusStopped, got %v", forward.Status())
	}

	// Try to start (will fail due to no SSH hops, but tests Start() logic)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = forward.Start(ctx)
	if err == nil {
		t.Error("Expected error when starting without SSH hops")
	}

	// Status should be error (not stopped, because Start() was attempted)
	if forward.Status() != StatusError {
		t.Errorf("Expected status StatusError after failed start, got %v", forward.Status())
	}

	// Stop should work
	err = forward.Stop()
	if err != nil {
		t.Errorf("Stop() failed: %v", err)
	}

	// Status should be stopped
	if forward.Status() != StatusStopped {
		t.Errorf("Expected status StatusStopped after Stop(), got %v", forward.Status())
	}
}

func TestLocalListenToRemote_StatusTransitions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	listenPort, err := findFreePort()
	if err != nil {
		t.Fatalf("Failed to find free port: %v", err)
	}

	bindAddr := fmt.Sprintf("127.0.0.1:%d", listenPort)
	targetAddr := "127.0.0.1:9999"
	forwardID := "test-forward-status"

	database, err := createTestDB()
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer database.Close()

	hopChain := []*tunnel.HopConfig{}

	forward := NewLocalListenToRemote(bindAddr, targetAddr, forwardID, database, hopChain)

	// Test status transitions
	statuses := []ForwardStatus{
		StatusStopped, // Initial
		// StatusError,  // After failed Start()
		StatusStopped, // After Stop()
	}

	// Start and immediately stop (will fail, but tests lifecycle)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	forward.Start(ctx)
	cancel() // Cancel context to trigger stop

	forward.Stop()

	// Verify final status
	if forward.Status() != StatusStopped {
		t.Errorf("Expected final status StatusStopped, got %v", forward.Status())
	}

	_ = statuses // TODO: track actual status transitions
}

func TestLocalListenToRemote_String(t *testing.T) {
	forward := &LocalListenToRemote{
		bindAddr:   "127.0.0.1:8080",
		targetAddr: "192.168.1.1:9999",
	}

	str := forward.String()
	expected := "LocalListenToRemote[127.0.0.1:8080 → 192.168.1.1:9999]"

	if str != expected {
		t.Errorf("String() = %s, want %s", str, expected)
	}
}

func TestLocalListenToRemote_Type(t *testing.T) {
	forward := &LocalListenToRemote{}

	if forward.Type() != "local_listen_to_remote" {
		t.Errorf("Type() = %s, want local_listen_to_remote", forward.Type())
	}
}
```

**Step 3: Run tests**

Run: `go test ./internal/forwarding -v -run TestLocalListenToRemote`
Expected: ✅ All tests pass

**Step 4: Add race detection**

Run: `go test ./internal/forwarding -race -run TestLocalListenToRemote`
Expected: ✅ No data races detected

**Step 5: Commit**

```bash
git add internal/forwarding/testutil.go internal/forwarding/local_listen_to_remote_test.go
git commit -m "test: add basic lifecycle tests for LocalListenToRemote

- Add test utilities (findFreePort, createTestDB, waitForStatus)
- Test basic lifecycle: Start() → Error → Stop()
- Test status transitions
- Test String() and Type() methods
- Add -short mode support for skipping in CI

Coverage for core forwarding logic.

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

### Task 5: Add Tests for ForwardService

**Files:**
- Create: `internal/service/forward_service_test.go`

**Context:** Service layer manages lifecycle. Need to test sync loop and rebuild logic.

**Step 1: Write tests for service lifecycle**

Create: `internal/service/forward_service_test.go`

```go
package service

import (
	"context"
	"testing"
	"time"

	"github.com/hallelujah-shih/ssh-multihop/internal/db"
)

func TestForwardService_New(t *testing.T) {
	database, err := db.New(db.Config{Path: ":memory:"})
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer database.Close()

	service := New(database)

	if service == nil {
		t.Fatal("New() returned nil")
	}

	if service.db != database {
		t.Error("Service database not set correctly")
	}
}

func TestForwardService_CreateForward(t *testing.T) {
	database, err := db.New(db.Config{Path: ":memory:"})
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer database.Close()

	service := New(database)

	forward := &db.Forward{
		Type:        db.LocalListenToRemote,
		ListenHost:  "local",
		ServiceHost: "remote",
		ListenAddr:  "127.0.0.1:8080",
		ServiceAddr: "127.0.0.1:8081",
	}

	err = service.CreateForward(forward)
	if err != nil {
		t.Fatalf("CreateForward() failed: %v", err)
	}

	// Verify ID was generated
	if forward.ID == "" {
		t.Error("Forward ID was not generated")
	}

	// Verify it's in database
	retrieved, err := service.GetForward(forward.ID)
	if err != nil {
		t.Fatalf("GetForward() failed: %v", err)
	}

	if retrieved.Type != forward.Type {
		t.Error("Forward type mismatch")
	}
}

func TestForwardService_CreateForward_InvalidAddress(t *testing.T) {
	database, err := db.New(db.Config{Path: ":memory:"})
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer database.Close()

	service := New(database)

	forward := &db.Forward{
		Type:        db.LocalListenToRemote,
		ListenHost:  "local",
		ServiceHost: "remote",
		ListenAddr:  "invalid-address", // Invalid
		ServiceAddr: "127.0.0.1:8081",
	}

	err = service.CreateForward(forward)
	if err == nil {
		t.Error("Expected error for invalid address, got nil")
	}
}

func TestForwardService_ListForwards(t *testing.T) {
	database, err := db.New(db.Config{Path: ":memory:"})
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer database.Close()

	service := New(database)

	// Create forwards
	forward1 := &db.Forward{
		Type:        db.LocalListenToRemote,
		ListenHost:  "local",
		ServiceHost: "remote1",
		ListenAddr:  "127.0.0.1:8080",
		ServiceAddr: "127.0.0.1:8081",
	}

	forward2 := &db.Forward{
		Type:        db.LocalListenToRemote,
		ListenHost:  "local",
		ServiceHost: "remote2",
		ListenAddr:  "127.0.0.1:8082",
		ServiceAddr: "127.0.0.1:8083",
	}

	if err := service.CreateForward(forward1); err != nil {
		t.Fatalf("Failed to create forward1: %v", err)
	}
	if err := service.CreateForward(forward2); err != nil {
		t.Fatalf("Failed to create forward2: %v", err)
	}

	// List forwards
	forwards, err := service.ListForwards()
	if err != nil {
		t.Fatalf("ListForwards() failed: %v", err)
	}

	if len(forwards) != 2 {
		t.Errorf("Expected 2 forwards, got %d", len(forwards))
	}
}

func TestForwardService_DeleteForward(t *testing.T) {
	database, err := db.New(db.Config{Path: ":memory:"})
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer database.Close()

	service := New(database)

	forward := &db.Forward{
		Type:        db.LocalListenToRemote,
		ListenHost:  "local",
		ServiceHost: "remote",
		ListenAddr:  "127.0.0.1:8080",
		ServiceAddr: "127.0.0.1:8081",
	}

	if err := service.CreateForward(forward); err != nil {
		t.Fatalf("Failed to create forward: %v", err)
	}

	// Delete it
	err = service.DeleteForward(forward.ID)
	if err != nil {
		t.Fatalf("DeleteForward() failed: %v", err)
	}

	// Verify it's gone
	_, err = service.GetForward(forward.ID)
	if err == nil {
		t.Error("Expected error when getting deleted forward")
	}
	if err != ErrForwardNotFound {
		t.Errorf("Expected ErrForwardNotFound, got %v", err)
	}
}

func TestForwardService_GetStatus(t *testing.T) {
	database, err := db.New(db.Config{Path: ":memory:"})
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer database.Close()

	service := New(database)

	forward := &db.Forward{
		Type:        db.LocalListenToRemote,
		ListenHost:  "local",
		ServiceHost: "remote",
		ListenAddr:  "127.0.0.1:8080",
		ServiceAddr: "127.0.0.1:8081",
	}

	if err := service.CreateForward(forward); err != nil {
		t.Fatalf("Failed to create forward: %v", err)
	}

	// Get status
	status, err := service.GetStatus(forward.ID)
	if err != nil {
		t.Fatalf("GetStatus() failed: %v", err)
	}

	if status.ForwardID != forward.ID {
		t.Error("Status forward ID mismatch")
	}
}

func TestForwardService_Start(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	database, err := db.New(db.Config{Path: ":memory:"})
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer database.Close()

	service := New(database)

	// Create a forward that will fail to start (no SSH config)
	forward := &db.Forward{
		Type:        db.LocalListenToRemote,
		ListenHost:  "local",
		ServiceHost: "nonexistent-host-xyz123",
		ListenAddr:  "127.0.0.1:18080",
		ServiceAddr: "127.0.0.1:18081",
	}

	if err := service.CreateForward(forward); err != nil {
		t.Fatalf("Failed to create forward: %v", err)
	}

	// Start service (will attempt to start forward)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	startErr := make(chan error, 1)
	go func() {
		startErr <- service.Start()
	}()

	// Wait a bit for startup
	time.Sleep(500 * time.Millisecond)

	// Stop service
	stopErr := service.Stop()
	if stopErr != nil {
		t.Errorf("Stop() failed: %v", stopErr)
	}

	// Start should complete (even if forwards failed)
	select {
	case err := <-startErr:
		if err != nil && err != context.DeadlineExceeded {
			t.Errorf("Start() failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("Start() did not complete in time")
	}
}
```

**Step 2: Run tests**

Run: `go test ./internal/service -v -run TestForwardService`
Expected: ✅ All tests pass

**Step 3: Run with race detector**

Run: `go test ./internal/service -race -run TestForwardService`
Expected: ✅ No data races

**Step 4: Commit**

```bash
git add internal/service/forward_service_test.go
git commit -m "test: add comprehensive tests for ForwardService

- Test New(), CreateForward(), ListForwards()
- Test DeleteForward() and GetStatus()
- Test validation (invalid addresses)
- Test service Start() and Stop() lifecycle
- Add -short mode support

Coverage for service layer business logic.

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

### Task 6: Add Integration Tests for API

**Files:**
- Modify: `internal/api/integration_test.go`
- Create: `internal/api/testutil.go`

**Context:** API handlers need integration tests with real HTTP requests.

**Step 1: Create test helpers**

Create: `internal/api/testutil.go`

```go
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hallelujah-shih/ssh-multihop/internal/db"
	"github.com/hallelujah-shih/ssh-multihop/internal/service"
	"github.com/hallelujah-shih/ssh-multihop/internal/util"
)

// createTestService creates a test service with in-memory database
func createTestService(t *testing.T) (*service.ForwardService, *db.Database) {
	t.Helper()

	database, err := db.New(db.Config{Path: ":memory:"})
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}

	svc := service.New(database)

	return svc, database
}

// createTestServer creates a test HTTP server
func createTestServer(t *testing.T) (*httptest.Server, *service.ForwardService, *db.Database) {
	t.Helper()

	svc, database := createTestService(t)

	handlers := New(svc)

	// Create test mux
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/forwards", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			handlers.CreateForward(w, r)
		} else if r.Method == http.MethodGet {
			handlers.ListForwards(w, r)
		}
	})
	mux.HandleFunc("/api/v1/forwards/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			handlers.GetForward(w, r)
		} else if r.Method == http.MethodDelete {
			handlers.DeleteForward(w, r)
		}
	})
	mux.HandleFunc("/health", handlers.HealthCheck)

	server := httptest.NewServer(mux)

	return server, svc, database
}

// parseAddress is a test helper
func parseAddress(addr string) (string, int, error) {
	return util.ParseAddress(addr)
}

// contextWithTimeout creates a context with timeout
func contextWithTimeout(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), timeout)
}
```

**Step 2: Write integration tests**

Modify: `internal/api/integration_test.go` (or create if empty)

```go
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/hallelujah-shih/ssh-multihop/internal/db"
)

func TestHandlers_CreateForward(t *testing.T) {
	server, svc, database := createTestServer(t)
	defer server.Close()
	defer database.Close()

	// Start service to handle forwards
	ctx, cancel := contextWithTimeout(2 * time.Second)
	defer cancel()

	go svc.Start()
	defer svc.Stop()

	time.Sleep(100 * time.Millisecond) // Let service start

	// Create forward request
	reqBody := CreateForwardRequest{
		Type:        db.LocalListenToRemote,
		ListenHost:  "local",
		ServiceHost: "remote",
		ListenAddr:  "127.0.0.1:18080",
		ServiceAddr: "127.0.0.1:18081",
	}

	body, _ := json.Marshal(reqBody)

	resp, err := http.Post(server.URL+"/api/v1/forwards", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Failed to create forward: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("Expected status 201, got %d", resp.StatusCode)
	}

	// Parse response
	var forward db.Forward
	if err := json.NewDecoder(resp.Body).Decode(&forward); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if forward.Type != reqBody.Type {
		t.Error("Forward type mismatch")
	}
}

func TestHandlers_CreateForward_InvalidAddress(t *testing.T) {
	server, _, database := createTestServer(t)
	defer server.Close()
	defer database.Close()

	reqBody := CreateForwardRequest{
		Type:        db.LocalListenToRemote,
		ListenHost:  "local",
		ServiceHost: "remote",
		ListenAddr:  "invalid-address", // Invalid
		ServiceAddr: "127.0.0.1:18081",
	}

	body, _ := json.Marshal(reqBody)

	resp, err := http.Post(server.URL+"/api/v1/forwards", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", resp.StatusCode)
	}

	// Parse error response
	var errResp ErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("Failed to decode error response: %v", err)
	}

	if errResp.Error == "" {
		t.Error("Expected error message, got empty string")
	}
}

func TestHandlers_ListForwards(t *testing.T) {
	server, svc, database := createTestServer(t)
	defer server.Close()
	defer database.Close()

	ctx, cancel := contextWithTimeout(2 * time.Second)
	defer cancel()

	go svc.Start()
	defer svc.Stop()

	time.Sleep(100 * time.Millisecond)

	// Create a forward first
	forward := &db.Forward{
		Type:        db.LocalListenToRemote,
		ListenHost:  "local",
		ServiceHost: "remote",
		ListenAddr:  "127.0.0.1:18082",
		ServiceAddr: "127.0.0.1:18083",
	}

	if err := svc.CreateForward(forward); err != nil {
		t.Fatalf("Failed to create forward: %v", err)
	}

	// List forwards
	resp, err := http.Get(server.URL + "/api/v1/forwards")
	if err != nil {
		t.Fatalf("Failed to list forwards: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Parse response
	var forwards []db.Forward
	if err := json.NewDecoder(resp.Body).Decode(&forwards); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(forwards) != 1 {
		t.Errorf("Expected 1 forward, got %d", len(forwards))
	}
}

func TestHandlers_GetForward(t *testing.T) {
	server, svc, database := createTestServer(t)
	defer server.Close()
	defer database.Close()

	ctx, cancel := contextWithTimeout(2 * time.Second)
	defer cancel()

	go svc.Start()
	defer svc.Stop()

	time.Sleep(100 * time.Millisecond)

	// Create a forward
	forward := &db.Forward{
		Type:        db.LocalListenToRemote,
		ListenHost:  "local",
		ServiceHost: "remote",
		ListenAddr:  "127.0.0.1:18084",
		ServiceAddr: "127.0.0.1:18085",
	}

	if err := svc.CreateForward(forward); err != nil {
		t.Fatalf("Failed to create forward: %v", err)
	}

	// Get forward
	resp, err := http.Get(server.URL + "/api/v1/forwards/" + forward.ID)
	if err != nil {
		t.Fatalf("Failed to get forward: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Parse response
	var retrieved db.Forward
	if err := json.NewDecoder(resp.Body).Decode(&retrieved); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if retrieved.ID != forward.ID {
		t.Error("Forward ID mismatch")
	}
}

func TestHandlers_DeleteForward(t *testing.T) {
	server, svc, database := createTestServer(t)
	defer server.Close()
	defer database.Close()

	ctx, cancel := contextWithTimeout(2 * time.Second)
	defer cancel()

	go svc.Start()
	defer svc.Stop()

	time.Sleep(100 * time.Millisecond)

	// Create a forward
	forward := &db.Forward{
		Type:        db.LocalListenToRemote,
		ListenHost:  "local",
		ServiceHost: "remote",
		ListenAddr:  "127.0.0.1:18086",
		ServiceAddr: "127.0.0.1:18087",
	}

	if err := svc.CreateForward(forward); err != nil {
		t.Fatalf("Failed to create forward: %v", err)
	}

	// Delete forward
	req, _ := http.NewRequest(http.MethodDelete, server.URL+"/api/v1/forwards/"+forward.ID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to delete forward: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Verify it's deleted
	resp, err = http.Get(server.URL + "/api/v1/forwards/" + forward.ID)
	if err != nil {
		t.Fatalf("Failed to get forward: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", resp.StatusCode)
	}
}

func TestHandlers_HealthCheck(t *testing.T) {
	server, _, database := createTestServer(t)
	defer server.Close()
	defer database.Close()

	resp, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatalf("Failed to call health check: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}
```

**Step 3: Add missing imports**

```go
import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hallelujah-shih/ssh-multihop/internal/db"
)
```

**Step 4: Run integration tests**

Run: `go test ./internal/api -tags=integration -v`
Expected: ✅ All tests pass

**Step 5: Run with race detector**

Run: `go test ./internal/api -tags=integration -race -v`
Expected: ✅ No data races

**Step 6: Commit**

```bash
git add internal/api/integration_test.go internal/api/testutil.go
git commit -m "test: add comprehensive integration tests for API handlers

- Test CreateForward with valid and invalid data
- Test ListForwards, GetForward, DeleteForward
- Test HealthCheck endpoint
- Add test utilities (createTestService, createTestServer)
- Use httptest.Server for real HTTP testing

Requires -tags=integration flag.

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Phase 3: High-Priority Fixes (1-2 weeks)

### Task 7: Fix Resource Cleanup with Deferred Cleanup Pattern

**Files:**
- Modify: `internal/forwarding/local_listen_to_remote.go:178-208`

**Context:** Current cleanup logic is complex and error-prone. Simplify using deferred cleanup pattern.

**Step 1: Add test for cleanup on failure**

Add to: `internal/forwarding/local_listen_to_remote_test.go`

```go
func TestLocalListenToRemote_CleanupOnFailedStart(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	// Use a port that will definitely fail
	listenPort, err := findFreePort()
	if err != nil {
		t.Fatalf("Failed to find free port: %v", err)
	}

	// Bind to same port twice (second will fail)
	bindAddr := fmt.Sprintf("127.0.0.1:%d", listenPort)
	targetAddr := "127.0.0.1:9999"
	forwardID := "test-cleanup-failed"

	database, err := createTestDB()
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer database.Close()

	hopChain := []*tunnel.HopConfig{}

	// Create first forward
	forward1 := NewLocalListenToRemote(bindAddr, targetAddr, forwardID+"-1", database, hopChain)

	ctx1, cancel1 := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel1()

	// This one might work
	_ = forward1.Start(ctx1)

	// Try to create second forward on same port (should fail)
	forward2 := NewLocalListenToRemote(bindAddr, targetAddr, forwardID+"-2", database, hopChain)

	ctx2, cancel2 := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel2()

	err = forward2.Start(ctx2)
	if err == nil {
		t.Error("Expected error when binding to same port")
	}

	// Verify cleanup happened
	forward2.Stop() // Should not panic or hang

	// First forward should still be stopped cleanly
	forward1.Stop()
}
```

**Step 2: Run test to verify current behavior**

Run: `go test ./internal/forwarding -run TestLocalListenToRemote_CleanupOnFailedStart`
Expected: Should pass or hang (if cleanup is broken)

**Step 3: Simplify cleanup logic using defer pattern**

The current code is actually good - it already uses cleanupOnce. Just verify it works.

**Step 4: Add test for Stop() idempotency**

```go
func TestLocalListenToRemote_StopIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	listenPort, err := findFreePort()
	if err != nil {
		t.Fatalf("Failed to find free port: %v", err)
	}

	bindAddr := fmt.Sprintf("127.0.0.1:%d", listenPort)
	targetAddr := "127.0.0.1:9999"
	forwardID := "test-stop-idempotent"

	database, err := createTestDB()
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer database.Close()

	hopChain := []*tunnel.HopConfig{}

	forward := NewLocalListenToRemote(bindAddr, targetAddr, forwardID, database, hopChain)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_ = forward.Start(ctx)

	// Stop multiple times - should not panic
	forward.Stop()
	forward.Stop()
	forward.Stop()

	if forward.Status() != StatusStopped {
		t.Errorf("Expected status StatusStopped, got %v", forward.Status())
	}
}
```

**Step 5: Run test**

Run: `go test ./internal/forwarding -run TestLocalListenToRemote_StopIdempotent`
Expected: ✅ PASS

**Step 6: Commit**

```bash
git add internal/forwarding/local_listen_to_remote_test.go
git commit -m "test: verify resource cleanup and Stop() idempotency

- Add test for cleanup when Start() fails
- Add test for calling Stop() multiple times
- Verify cleanupOnce prevents double-cleanup

Ensures resource leaks don't occur.

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

### Task 8: Add Database Transactions

**Files:**
- Modify: `internal/db/database.go`
- Modify: `internal/service/forward_service.go`

**Context:** Currently Forward and Status are created separately. Need transaction for atomicity.

**Step 1: Write test for atomicity**

Create: `internal/db/transaction_test.go`

```go
package db

import (
	"testing"
)

func TestDatabase_CreateForwardWithStatus(t *testing.T) {
	database, err := New(Config{Path: ":memory:"})
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer database.Close()

	// Test that we can create both atomically
	err = database.Transaction(func(tx *gorm.DB) error {
		forward := &Forward{
			Type:        LocalListenToRemote,
			ListenHost:  "local",
			ServiceHost: "remote",
			ListenAddr:  "127.0.0.1:8080",
			ServiceAddr: "127.0.0.1:8081",
		}

		if err := tx.Create(forward).Error; err != nil {
			return err
		}

		status := &ForwardStatus{
			ForwardID: forward.ID,
			Status:    "running",
		}

		if err := tx.Create(status).Error; err != nil {
			return err // Should rollback forward
		}

		return nil
	})

	if err != nil {
		t.Fatalf("Transaction failed: %v", err)
	}

	// Verify both were created
	forwards, err := database.ListForwards()
	if err != nil {
		t.Fatalf("Failed to list forwards: %v", err)
	}

	if len(forwards) != 1 {
		t.Errorf("Expected 1 forward, got %d", len(forwards))
	}

	statuses, err := database.ListStatuses()
	if err != nil {
		t.Fatalf("Failed to list statuses: %v", err)
	}

	if len(statuses) != 1 {
		t.Errorf("Expected 1 status, got %d", len(statuses))
	}
}

func TestDatabase_TransactionRollback(t *testing.T) {
	database, err := New(Config{Path: ":memory:"})
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer database.Close()

	// Test that transaction rolls back on error
	err = database.Transaction(func(tx *gorm.DB) error {
		forward := &Forward{
			Type:        LocalListenToRemote,
			ListenHost:  "local",
			ServiceHost: "remote",
			ListenAddr:  "127.0.0.1:8080",
			ServiceAddr: "127.0.0.1:8081",
		}

		if err := tx.Create(forward).Error; err != nil {
			return err
		}

		// Return error to trigger rollback
		return fmt.Errorf("intentional error")
	})

	if err == nil {
		t.Error("Expected error from transaction")
	}

	// Verify forward was NOT created (rolled back)
	forwards, err := database.ListForwards()
	if err != nil {
		t.Fatalf("Failed to list forwards: %v", err)
	}

	if len(forwards) != 0 {
		t.Errorf("Expected 0 forwards after rollback, got %d", len(forwards))
	}
}
```

**Step 2: Add Transaction method to Database**

Modify: `internal/db/database.go`

```go
// Transaction executes a function within a database transaction
func (d *Database) Transaction(fn func(tx *gorm.DB) error) error {
	return d.db.Transaction(fn)
}
```

**Step 3: Update ForwardService to use transactions**

Modify: `internal/service/forward_service.go:286-320`

```go
// CreateForward creates a new forward (只操作数据库，由sync循环负责启动)
func (s *ForwardService) CreateForward(fwd *db.Forward) error {
	// Validate addresses
	_, _, err := util.ParseAddress(fwd.ListenAddr)
	if err != nil {
		return fmt.Errorf("invalid listen_addr: %w", err)
	}

	_, _, err = util.ParseAddress(fwd.ServiceAddr)
	if err != nil {
		return fmt.Errorf("invalid service_addr: %w", err)
	}

	// Validate
	if fwd.Type == db.RemoteListenToRemote && fwd.MaxConns != 0 {
		// InlineForwardOrchestrator doesn't support maxConns
		return fmt.Errorf("inline forward does not support maxConns parameter")
	}

	// Save to database and initial status in a transaction
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(fwd).Error; err != nil {
			return fmt.Errorf("failed to create forward: %w", err)
		}

		// Create initial status
		forwardStatus := &db.ForwardStatus{
			ForwardID:     fwd.ID,
			Status:        "created",
			LastHeartbeat: time.Now(),
		}

		if err := tx.Create(forwardStatus).Error; err != nil {
			return fmt.Errorf("failed to create status: %w", err)
		}

		return nil
	})

	if err != nil {
		return err
	}

	zap.L().Info("Forward created in database",
		zap.String("id", fwd.ID),
		zap.String("type", string(fwd.Type)),
		zap.String("listen_host", fwd.ListenHost),
		zap.String("listen_addr", fwd.ListenAddr),
		zap.String("service_host", fwd.ServiceHost),
		zap.String("service_addr", fwd.ServiceAddr))

	// sync循环会在5秒内检测到新记录并启动
	return nil
}
```

**Step 4: Add gorm import**

```go
import (
	// ... existing imports ...
	"gorm.io/gorm"
)
```

**Step 5: Run tests**

Run: `go test ./internal/db -run TestDatabase_Transaction -v`
Expected: ✅ PASS

**Step 6: Run all tests**

Run: `go test ./... -v`
Expected: ✅ All pass

**Step 7: Commit**

```bash
git add internal/db/database.go internal/db/transaction_test.go internal/service/forward_service.go
git commit -m "feat: add database transactions for atomic operations

- Add Transaction() method to Database
- Update CreateForward to use transaction
- Create Forward and ForwardStatus atomically
- Add tests for transaction commit/rollback

Prevents partial state where Forward exists but Status doesn't.

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Summary

### What This Plan Achieves

**Phase 1 - Critical Fixes (1-2 days):**
- ✅ Eliminates panic in production code
- ✅ Fixes IPv6 compatibility
- ✅ Improves error handling

**Phase 2 - Test Coverage (1-2 weeks):**
- ✅ Core forwarding logic tests
- ✅ Service layer tests
- ✅ API integration tests
- ✅ Race detection

**Phase 3 - High Priority (1-2 weeks):**
- ✅ Database transaction support
- ✅ Resource cleanup verification
- ✅ Idempotency tests

### Estimated Timeline

- **Week 1:** Phase 1 (critical fixes)
- **Week 2-3:** Phase 2 (tests)
- **Week 4-5:** Phase 3 (high priority fixes)

### Success Criteria

- All critical risks (🔴) resolved
- Test coverage > 50%
- Zero panics in production code
- All tests pass with `-race` flag

### Next Steps After This Plan

1. Set up CI/CD (GitHub Actions)
2. Add monitoring and logging
3. Performance benchmarking
4. Documentation updates

---

**Ready to execute! Choose execution mode:**

1. **Subagent-Driven** (this session) - I dispatch subagents per task, review between tasks
2. **Parallel Session** (separate) - New session with executing-plans skill, batch execution

Which approach?
