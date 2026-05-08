# OpenWolf

@.wolf/OPENWOLF.md

This project uses OpenWolf for context management. Read and follow .wolf/OPENWOLF.md every session. Check .wolf/cerebrum.md before generating code. Check .wolf/anatomy.md before reading files.


# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

SSH Multi-Hop Port Forwarding Tool - A Go-based CLI tool and daemon for managing SSH multi-hop port forwarding with REST API, auto-reconnection, and built-in SSH agent support.

## Development Commands

```bash
# Build the CLI tool
make build              # Standard build to ./ssh-multihop
make dev               # Verbose build for development

# Testing
make test              # Run all tests
make test-integration  # Run integration tests (requires -tags=integration)
make test-api          # Run API integration tests
go test ./... -v       # Run tests with verbose output

# Code Quality
make lint              # Run golangci-lint
make lint-fix          # Auto-fix lint issues
make fmt               # Format code with go fmt and goimports
make vet               # Run go vet
make check             # Run all checks (fmt, vet, lint, test)

# Utilities
make clean             # Remove build artifacts
make deps              # Download and tidy dependencies
make coverage          # Generate coverage report
```

## Running the Application

```bash
# CLI mode
./ssh-multihop list-hosts                    # List all hosts from SSH config
./ssh-multihop map --forward 127.0.0.1:8888@local --to 127.0.0.1:8888@vmr.u24

# Daemon mode
./ssh-multihop daemon --port 8080            # Start REST API on port 8080
./ssh-multihop daemon --port 8080 --db /path/to/db  # Custom database path
```

## Documentation

### Primary Documentation
- **[README.md](README.md)** - Project overview and quick start
- **[docs/architecture.md](docs/architecture.md)** - System architecture and design principles
- **[docs/archive/setuid-support-implementation.md](docs/archive/setuid-support-implementation.md)** - Running with elevated privileges

### Developer Documentation
- **[docs/](docs/)** - Comprehensive developer documentation
  - [API Reference](docs/api/REFERENCE.md) - REST API documentation
  - [Documentation Standards](docs/DOCUMENTATION_STANDARDS.md) - Writing guidelines
  - [Test Scripts](docs/scripts/README.md) - Testing guide
  - [Archive](docs/archive/) - Historical documentation

### See Also
- **[CLAUDE.md](CLAUDE.md)** - This file (project instructions for Claude Code)

## Architecture

### Simplified Forward Architecture

The project implements a **simplified architecture** with clear separation of concerns:

- **Forward Instances** (`internal/forwarding/`): Only handle connection establishment and health checking. Fail fast on errors without retry logic.
- **ForwardService** (`internal/service/`): Manages lifecycle (creation, rebuild, deletion) of all forwards via a 5-second sync loop.

Key principles:
1. Forwards update database status to "error" on failure, then stop
2. ForwardService detects error states from database and rebuilds with exponential retry (max 10 attempts, 3s delay)
3. No self-healing logic inside Forward implementations
4. Database is single source of truth for configuration

See `docs/architecture.md` for detailed design rationale.

### Forward Types

The system supports three forward types:

1. **`local_listen_to_remote`** (SSH -L): Local listener forwarding to remote service
2. **`remote_listen_to_local`** (SSH -R): Remote listener forwarding to local service
3. **`remote_listen_to_remote`**: Bridge two remote hosts without binding local port

Each type has a dedicated implementation in `internal/forwarding/`:
- `LocalListenToRemote`
- `RemoteListenToLocal`
- `RemoteListenToRemote`

### Directory Structure

```
cmd/ssh-multihop/          # Main application entry point
internal/
├── agent/                # Built-in SSH agent implementation
├── api/                   # REST API handlers and server
├── config/                # SSH config parser
├── connection/            # SSH connection management
├── db/                    # Database layer (SQLite via GORM)
├── forwarding/            # Forward implementations (3 types)
├── service/              # ForwardService (lifecycle management)
├── tunnel/               # Tunnel planning types
└── util/                 # Address parsing, SSH helpers, and user home directory
docs/                     # Documentation and test scripts
```

## REST API

Base URL: `http://localhost:8080/api/v1`

Key endpoints:
- `POST /api/v1/forwards` - Create forward
- `GET /api/v1/forwards` - List all forwards
- `GET /api/v1/forwards/:id` - Get forward details
- `DELETE /api/v1/forwards/:id` - Delete forward
- `GET /api/v1/forwards/:id/status` - Get forward status

See `docs/api/REFERENCE.md` for complete API documentation.

## Key Implementation Patterns

### Forward Lifecycle

1. Forward.Start() blocks until stopped or error
2. Health check every 15 seconds
3. On error: set DB status to "error", stop forward, return
4. ForwardService sync loop detects "error" status and rebuilds

### Resource Cleanup

Forwards use context cancellation for graceful shutdown:
1. Cancel context (unblocks goroutines)
2. Close listener (stops accepting connections)
3. Close SSH connections (unblocks dial operations)
4. Wait for connection handlers to finish

### Database Integration

Forwards update database status on errors:
```go
f.setStatus(StatusError)
f.db.CreateOrUpdateStatus(&db.ForwardStatus{
    ForwardID: f.forwardID,
    Status: "error",
    ErrorMessage: errorMsg,
})
```

## Testing

Integration tests use build tags:
```bash
go test ./internal/api -tags=integration -v
```

Test scripts in `docs/scripts/`:
- `test-7-scenarios.sh` - Common usage scenarios (comprehensive 7-scenario test)
- `test-api.sh` - API endpoint tests

Historical test scripts are archived in `docs/archive/scripts/`

## SSH Config Integration

The tool parses OpenSSH config with ProxyJump support:
- Uses `github.com/kevinburke/ssh_config` for parsing
- Supports all standard SSH config directives
- Hostnames from config are used in API requests

## setuid/setgid Support

The binary correctly handles elevated privileges. See `docs/archive/setuid-support-implementation.md` for details.

## Common Patterns to Follow

When adding new functionality:
1. **Fail fast**: Forwards should not retry internally
2. **Update database**: Reflect all state changes in database
3. **Let service layer handle recovery**: ForwardService manages rebuilds
4. **Use context for cancellation**: Ensure goroutines exit cleanly
5. **Close resources in dependency order**: Listeners before connections

When modifying forwards:
- Keep health check logic simple
- On error: set DB status, stop, return
- Don't add retry/backoff logic inside Forward
- All recovery belongs in ForwardService
