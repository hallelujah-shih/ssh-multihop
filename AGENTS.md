# AGENTS.md

This file provides guidance to coding agents working with the SSH Multi-Hop Port Forwarding Tool repository.

## Project Overview

Go-based CLI tool and daemon for managing SSH multi-hop port forwarding with REST API, auto-reconnection, and built-in SSH agent support.

**Key principles:**
- **Fail fast**: Forward implementations don't retry internally
- **Database as source of truth**: All state changes reflected in database
- **Service-layer recovery**: `ForwardService` manages rebuilds with exponential backoff
- **Context-based cancellation**: Goroutines exit cleanly via context
- **Resource cleanup order**: Listeners before connections

## Build and Test Commands

```bash
# Build
make build              # Standard build to ./ssh-multihop
make dev                # Verbose development build

# Testing - Run a single test
go test ./internal/forwarding -v -run TestLocalListenToRemote_BasicLifecycle
go test -v -run "TestName" ./package/path
go test ./internal/api -tags=integration -v -timeout 30s

# All tests
make test               # Run all tests
make test-verbose       # With debug logging
make test-integration   # Integration tests only
make test-api           # API integration tests

# Code quality
make fmt                # go fmt and goimports
make vet                # go vet
make lint               # golangci-lint
make lint-fix           # Auto-fix issues
make check              # All checks (fail fast)

# Utilities
make clean              # Remove build artifacts
make deps               # Download dependencies
make coverage           # Generate coverage report
```

## Running the Application

```bash
./ssh-multihop list-hosts                                    # List SSH config hosts
./ssh-multihop map --forward 127.0.0.1:8888@local --to 127.0.0.1:8888@vmr.u24
./ssh-multihop daemon --port 8080                            # Start REST API
```

## Code Style Guidelines

### Imports Organization
```go
import (
    // Standard library
    "context"
    "fmt"
    "net"
    "sync"
    "time"

    // Third-party packages
    "github.com/gin-gonic/gin"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "go.uber.org/zap"
    "golang.org/x/crypto/ssh"

    // Internal packages
    "github.com/hallelujah-shih/ssh-multihop/internal/connection"
    "github.com/hallelujah-shih/ssh-multihop/internal/db"
    "github.com/hallelujah-shih/ssh-multihop/internal/forwarding"
)
```

### Naming Conventions
- **Packages**: lowercase, single word (`forwarding`, `connection`, `db`)
- **Interfaces**: `-er` suffix (`Forward`, `HealthChecker`)
- **Types**: `PascalCase` (`LocalListenToRemote`, `ForwardStatus`)
- **Variables**: `camelCase` (`forwardID`, `healthCheckInterval`)
- **Constants**: `PascalCase` exported, `camelCase` internal
- **Database fields**: `snake_case` (`forward_id`, `created_at`)

### Error Handling
```go
// Return errors with context
listener, err := net.Listen("tcp", bindAddr)
if err != nil {
    return fmt.Errorf("failed to listen on %s: %w", bindAddr, err)
}

// Database errors
status, err := db.GetStatus(forwardID)
if err != nil {
    if errors.Is(err, gorm.ErrRecordNotFound) {
        // Handle not found
    }
    return fmt.Errorf("failed to get status: %w", err)
}

// Cleanup on error
func setup() error {
    resource, err := acquireResource()
    if err != nil {
        return err
    }
    defer func() {
        if err != nil {
            resource.Close()
        }
    }()
    return nil
}
```

### Forward Implementation Pattern
```go
// Forwards: fail fast, update DB on error, no internal retry
func (f *LocalListenToRemote) Start(ctx context.Context) error {
    f.statusMu.Lock()
    defer f.statusMu.Unlock()
    
    listener, err := net.Listen("tcp", f.bindAddr)
    if err != nil {
        f.setStatus(StatusError)
        f.updateDBStatus("error", err.Error())
        return fmt.Errorf("listen failed: %w", err)
    }
    
    go f.healthCheckLoop()
    f.setStatus(StatusRunning)
    return nil
}

// Health check: simple, update DB on failure
func (f *LocalListenToRemote) HealthCheck() error {
    if f.status != StatusRunning {
        return fmt.Errorf("not running")
    }
    if err := f.checkListener(); err != nil {
        f.setStatus(StatusError)
        f.updateDBStatus("error", err.Error())
        return err
    }
    return nil
}
```

### Testing Patterns
```go
// Use testify assertions
import (
    "testing"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestLocalListenToRemote_BasicLifecycle(t *testing.T) {
    // Arrange
    testDB, err := createTestDB()
    require.NoError(t, err)
    defer testDB.Close()

    // Act
    lf := NewLocalListenToRemote(...)
    err = lf.Start(ctx)

    // Assert
    assert.NoError(t, err)
    assert.Equal(t, StatusRunning, lf.Status())
    assert.NoError(t, lf.Stop())
}
```

### Concurrency and Cleanup
```go
// Use sync.Once for idempotent cleanup
type LocalListenToRemote struct {
    cleanupOnce sync.Once
}

func (f *LocalListenToRemote) Stop() error {
    f.cleanupOnce.Do(func() {
        // Cleanup logic
    })
    return nil
}

// Track active connections
type LocalListenToRemote struct {
    activeConns map[net.Conn]struct{}
    connMu      sync.RWMutex
}

// Context-based shutdown
func (f *LocalListenToRemote) Start(ctx context.Context) error {
    f.ctx, f.cancelFunc = context.WithCancel(ctx)
    return nil
}
```

### Logging
```go
import "go.uber.org/zap"

logger.Info("Starting forward",
    zap.String("forward_id", forwardID),
    zap.String("bind_addr", bindAddr))

logger.Error("Failed to start forward",
    zap.String("forward_id", forwardID),
    zap.Error(err))
```

## Project Structure
```
cmd/ssh-multihop/          # Main entry point
internal/
├── agent/                # SSH agent implementation
├── api/                   # REST API handlers
├── config/                # SSH config parser
├── connection/            # SSH connection management
├── db/                    # SQLite via GORM
├── forwarding/            # Forward implementations
├── service/               # ForwardService (lifecycle)
├── tunnel/               # Tunnel planning
└── util/                 # Helpers
```

## Forward Types
1. **`local_listen_to_remote`** (SSH -L): Local → Remote
2. **`remote_listen_to_local`** (SSH -R): Remote → Local
3. **`remote_listen_to_remote`**: Remote → Remote (no local binding)

## REST API
Base: `http://localhost:8080/api/v1`

- `POST /forwards` - Create forward
- `GET /forwards` - List forwards
- `GET /forwards/:id` - Get details
- `DELETE /forwards/:id` - Delete forward
- `GET /forwards/:id/status` - Get status

## Common Pitfalls
1. **Don't add retry logic in Forwards** - `ForwardService` handles rebuilds
2. **Always update database status** on errors
3. **Use context cancellation** for graceful shutdown
4. **Cleanup order**: listeners → connections → contexts
5. **Use `sync.Once`** for idempotent cleanup

## Additional Resources
- `CLAUDE.md` - Project overview and architecture
- `docs/architecture.md` - System design
- `docs/api/REFERENCE.md` - API documentation
- `docs/scripts/README.md` - Testing guide
