# Documentation Cleanup and Unification Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Clean up outdated documentation, unify documentation style, standardize file naming, and improve code comments quality.

**Architecture:** This plan organizes project documentation into a clear structure: user-facing docs in root/, developer docs in docs/, historical docs in docs/archive/, and code comments following Go conventions.

**Tech Stack:** Markdown, Go godoc conventions

**Context:** Current documentation has inconsistent naming (SETUID_SUPPORT.md vs README.md), mixed languages (Chinese/English), varying styles, and temporary docs that should be archived. This plan establishes standards and executes cleanup.

---

## Phase 1: Establish Documentation Standards (1 hour)

### Task 1: Create Documentation Standards Guide

**Files:**
- Create: `docs/DOCUMENTATION_STANDARDS.md`

**Step 1: Write the standards document**

Create file: `docs/DOCUMENTATION_STANDARDS.md`

```markdown
# Documentation Standards

## File Naming Conventions

### Root Directory (`/`)
User-facing documentation:
- `README.md` - Project overview and quick start
- `ARCHITECTURE.md` - System architecture (not SIMPLIFIED_ARCHITECTURE.md)
- `SETUID_SUPPORT.md` - Special topic documentation (ALL_CAPS for specific features)
- `CLAUDE.md` - Claude Code project instructions (required file)

### `docs/` Directory
Developer documentation:
- `index.md` - Documentation index (lowercase)
- `api/REFERENCE.md` - API documentation (not NEW-API-REFERENCE.md)
- `guides/<topic>.md` - How-to guides
- `scripts/README.md` - Test scripts documentation

### `docs/archive/` Directory
Historical/temporary documentation:
- `refactor-summary-<date>.md` - Refactor summaries
- `implementation-<feature>-<date>.md` - Implementation summaries
- Naming pattern: `<type>-<description>-YYYY-MM-DD.md`

## Writing Style Guidelines

### Language
- **Primary Language:** English for all documentation
- **Exception:** Technical Chinese terms keep original (e.g., "正向代理")
- **Code comments:** English only

### Markdown Format

#### Headings
```markdown
# Title (1-3 words)
## Section Name
### Subsection Name
```

#### Code Blocks
```markdown
**Usage:**
\`\`\`bash
command_here
\`\`\`

**Example:**
\`\`\`go
func Example() {
    // code
}
\`\`\`
```

#### Lists
```markdown
- Use dashes for unordered lists
- Second level item
  - Third level item

1. Use numbers for ordered steps
2. Second step
   - Detail for step 2
```

### Document Structure Template

```markdown
# [Feature/Topic Name]

## Overview
[2-3 sentences describing what this is]

## Purpose
[Why this exists, 1-2 sentences]

## Usage/Details
[Main content with examples]

## See Also
- [Related link 1]
- [Related link 2]
```

## Length Guidelines

- **README.md:** 50-100 lines (concise overview)
- **ARCHITECTURE.md:** 100-200 lines (system design)
- **API docs:** As needed (comprehensive)
- **Guides:** 50-150 lines (focused)
- **Archive docs:** Keep as-is (historical record)

## Code Comment Standards

### Philosophy: Documentation Through Comments

Code comments are **documentation**, not redundancy. Focus on accuracy and completeness rather than minimalism.

**Core Principles:**
1. **Accuracy First:** Comments must match code behavior. If they differ, fix the comment.
2. **Explain Why:** Comment on non-obvious decisions, algorithms, and trade-offs.
3. **Document Side Effects:** Mention goroutines, database updates, context usage.
4. **Preserve Value:** Keep comments that aid understanding, even if slightly redundant.

### Package Comments

Every package must have a comment explaining its purpose:

```go
// Package forwarding provides port forwarding implementations.
//
// The package supports four forward types:
//   - LocalListenToRemote: SSH -L (local listen to remote service)
//   - RemoteListenToLocal: SSH -R (remote listen to local service)
//   - RemoteListenToRemote: Remote-to-remote bridging
//   - InlineForwardOrchestrator: Composed forwarding using UDS bridge
//
// Architecture:
// All forwards follow the simplified architecture:
//   - Fail fast on errors (no internal retry logic)
//   - Update database status on errors
//   - ForwardService handles rebuild and recovery
//
// Thread Safety:
// Forward instances are not thread-safe. Use external synchronization
// if calling Start/Stop from multiple goroutines.
package forwarding
```

**Required elements:**
- Package purpose (1-2 sentences)
- Main exported types/functions
- Architecture/pattern notes
- Thread-safety considerations
- Usage examples for complex packages

### Function Comments

Exported functions must have comments:

```go
// Start begins the port forwarding.
//
// This method blocks until the forward is stopped or an error occurs.
// On error, the database status is set to "error" and resources are cleaned up.
//
// Parameters:
//   - ctx: Context for cancellation. Canceling triggers graceful shutdown.
//
// Returns:
//   - error: Non-nil if startup fails or forward encounters error
//
// Side effects:
//   - Opens SSH connections to target hosts
//   - Creates listener (local or remote depending on type)
//   - Launches health monitoring goroutine (15s interval)
//   - Updates database status on errors
//
// The forward runs independently until stopped or error occurs.
func (f *Forward) Start(ctx context.Context) error {
```

**Required elements:**
- What the function does
- Blocking behavior (blocks until X)
- Parameters (especially context usage)
- Return values (especially error conditions)
- Side effects (goroutines, database updates, state changes)

### Type Comments

Structs and interfaces must document purpose and usage:

```go
// ForwardService manages the lifecycle of all port forwards.
//
// The service runs a sync loop every 5 seconds to:
//   - Start new forwards from database
//   - Stop deleted forwards
//   - Rebuild forwards in error state
//
// Thread Safety:
// All methods are thread-safe and can be called concurrently.
//
// Lifecycle:
//   - Created with New()
//   - Started with Start()
//   - Stopped with Stop() or StopWithContext()
type ForwardService struct {
    // Database for persisting forward configurations
    db *db.Database

    // Active forwards indexed by forward ID
    // Protected by forwardsMu
    forwards map[string]*ForwardWrapper
    forwardsMu sync.RWMutex

    // Context for canceling all operations
    ctx    context.Context
    cancel context.CancelFunc

    // Notification channels
    syncDone   chan struct{} // Closed when syncLoop exits
    shutdownCh chan struct{} // Closed when Stop() is called
}
```

**Required elements:**
- Purpose and responsibilities
- Key operations
- Thread-safety guarantees
- Lifecycle management
- Important field comments

### Comment Quality Checklist

When reviewing comments, verify:

✅ **Accuracy:** Comment matches actual code behavior
✅ **Completeness:** Explains parameters, returns, side effects
✅ **Context:** Explains why, not just what
✅ **Non-obvious:** Comments add value beyond reading code

❌ **Avoid:** Comments that just repeat the code
❌ **Avoid:** Outdated comments that don't match implementation
❌ **Avoid:** Vague comments like "do the needful"

### Comment Maintenance Workflow

1. **Code Change First:** When modifying code, update comments immediately
2. **Verification:** After changing code, re-read comments to ensure they still match
3. **Peer Review:** Review comments alongside code in PRs
4. **Periodic Audit:** Quarterly review of comment accuracy

## Review Checklist

Before committing documentation:
- [ ] File name follows naming conventions
- [ ] Language is English (except technical terms)
- [ ] Markdown format follows template
- [ ] Code examples are tested
- [ ] Links are valid
- [ ] Spelling is correct
```

**Step 2: Commit standards document**

```bash
git add docs/DOCUMENTATION_STANDARDS.md
git commit -m "docs: establish documentation standards guide"
```

---

## Phase 2: Archive Historical Documentation (30 minutes)

### Task 2: Create Archive Directory and Move Old Docs

**Files:**
- Create: `docs/archive/`
- Move: `docs/refactor-summary.md` → `docs/archive/refactor-summary-2026-03-15.md`
- Move: `docs/refactor-verification.md` → `docs/archive/refactor-verification-2026-03-15.md`
- Move: `docs/PROJECT_STRUCTURE_REVIEW.md` → `docs/archive/project-structure-review-2026-03-15.md`
- Move: `docs/unified-context-shutdown.md` → `docs/archive/unified-context-shutdown-implementation-2026-03-16.md`

**Step 1: Create archive directory**

```bash
mkdir -p docs/archive
```

**Step 2: Move refactor summary**

```bash
mv docs/refactor-summary.md docs/archive/refactor-summary-2026-03-15.md
```

**Step 3: Move refactor verification**

```bash
mv docs/refactor-verification.md docs/archive/refactor-verification-2026-03-15.md
```

**Step 4: Move project structure review**

```bash
mv docs/PROJECT_STRUCTURE_REVIEW.md docs/archive/project-structure-review-2026-03-15.md
```

**Step 5: Move unified context shutdown doc**

```bash
mv docs/unified-context-shutdown.md docs/archive/unified-context-shutdown-implementation-2026-03-16.md
```

**Step 6: Create archive index**

Create file: `docs/archive/README.md`

```markdown
# Documentation Archive

This directory contains historical and temporary documentation preserved for reference.

## Contents

### Refactor Documentation
- `refactor-summary-2026-03-15.md` - Summary of internal structure refactor
- `refactor-verification-2026-03-15.md` - Post-refactor verification results
- `project-structure-review-2026-03-15.md` - Pre-refactor structure analysis

### Implementation Summaries
- `unified-context-shutdown-implementation-2026-03-16.md` - Graceful shutdown implementation details

## Note

These documents are kept for historical reference. For current documentation, see the main `docs/` directory.
```

**Step 7: Commit archive changes**

```bash
git add docs/archive/
git commit -m "docs: archive historical documentation to docs/archive/"
```

---

## Phase 3: Rename and Standardize Active Documentation (45 minutes)

### Task 3: Rename API Documentation

**Files:**
- Rename: `docs/api/NEW-API-REFERENCE.md` → `docs/api/REFERENCE.md`
- Modify: Update references in `docs/index.md`, `README.md`

**Step 1: Rename API reference file**

```bash
mv docs/api/NEW-API-REFERENCE.md docs/api/REFERENCE.md
```

**Step 2: Update docs/index.md**

File: `docs/index.md`

Find and replace:
```markdown
Old: - **[New API Reference](api/NEW-API-REFERENCE.md)** - REST API documentation
New: - **[API Reference](api/REFERENCE.md)** - REST API documentation
```

**Step 3: Update README.md**

File: `README.md`

Find and replace:
```markdown
Old: - **[SIMPLIFIED_ARCHITECTURE.md](SIMPLIFIED_ARCHITECTURE.md)** - System architecture
New: - **[ARCHITECTURE.md](ARCHITECTURE.md)** - System architecture and design principles
```

**Step 4: Rename architecture doc**

```bash
mv SIMPLIFIED_ARCHITECTURE.md ARCHITECTURE.md
```

**Step 5: Commit rename changes**

```bash
git add docs/api/REFERENCE.md docs/index.md README.md ARCHITECTURE.md
git commit -m "docs: standardize documentation naming (remove NEW prefix, unify ARCHITECTURE)"
```

---

## Phase 4: Update Documentation Index (20 minutes)

### Task 4: Rewrite docs/index.md Following Standards

**Files:**
- Modify: `docs/index.md`

**Step 1: Read current docs/index.md**

```bash
cat docs/index.md
```

**Step 2: Rewrite with standardized structure**

File: `docs/index.md`

```markdown
# Documentation

## User Guides

- **[README](../README.md)** - Project overview and quick start
- **[Architecture](../ARCHITECTURE.md)** - System architecture and design principles
- **[setuid/setgid Support](../SETUID_SUPPORT.md)** - Running with elevated privileges

## Developer Documentation

### API Reference
- **[API Reference](api/REFERENCE.md)** - REST API documentation
  - Address format and semantics
  - Forward types and validation rules
  - Example configurations for all scenarios

### Guides
- **[Documentation Standards](DOCUMENTATION_STANDARDS.md)** - How to write and format documentation
- **[Test Scripts](scripts/README.md)** - Testing guide and script usage

## Historical Documentation

- **[Archive](archive/)** - Historical implementation notes and refactor summaries

## Quick Links

### Forward Types
- `LocalListenToRemote` - SSH -L (local listen to remote service)
- `RemoteListenToLocal` - SSH -R (remote listen to local service)
- `RemoteListenToRemote` - Remote-to-remote bridging

### Key Components
- `ForwardService` - Lifecycle management and recovery
- `Forward interface` - Unified port forwarding API
- Health checking and graceful shutdown
```

**Step 3: Commit index update**

```bash
git add docs/index.md
git commit -m "docs: reorganize documentation index with standardized structure"
```

---

## Phase 5: Create Test Scripts Documentation (15 minutes)

### Task 5: Document Test Scripts

**Files:**
- Create: `docs/scripts/README.md`

**Step 1: Create scripts documentation**

Create file: `docs/scripts/README.md`

```markdown
# Test Scripts

This directory contains integration test scripts for verifying SSH multi-hop functionality.

## Prerequisites

- SSH multihop daemon running: `./ssh-multihop daemon --port 8080`
- SSH config with test hosts configured
- Required Go build tags for integration tests

## Available Scripts

### Scenario Tests
- **`test-7-scenarios.sh`** - Comprehensive 7-scenario testing
  - Tests all forward types
  - Validates error handling
  - Run time: ~5 minutes

### API Tests
- **`test-api.sh`** - REST API endpoint testing
  - Create, list, delete forwards
  - Status endpoint validation
  - Run time: ~2 minutes

### Daemon Tests
- **`test-daemon.sh`** - Daemon mode functionality
  - Startup and shutdown
  - Signal handling
  - Run time: ~3 minutes

### Architecture Tests
- **`test-simplified-architecture.sh`** - Architecture verification
  - Fail-fast behavior
  - Service-layer recovery
  - Run time: ~4 minutes

### Graceful Shutdown Tests
- **`test-graceful-shutdown.sh`** - Ctrl+C handling
  - Context cancellation
  - Resource cleanup
  - Run time: ~2 minutes

## Running Tests

### Run all tests
```bash
cd docs/scripts
./test-7-scenarios.sh
```

### Run specific test
```bash
cd docs/scripts
./test-api.sh
```

### Run with verbose output
```bash
bash -x ./test-daemon.sh
```

## Writing New Tests

Follow this template:
```bash
#!/bin/bash
# Test: <description>
# Prerequisites: <what needs to be running>
# Expected: <what should happen>

set -e

# Setup
BASE_URL="http://localhost:8080/api/v1"

# Test steps
echo "Test: <step description>"
# ... test code ...

# Verify
if [ condition ]; then
    echo "✓ PASS"
else
    echo "✗ FAIL"
    exit 1
fi
```

## Troubleshooting

### Port already in use

**Step 1: Check if ssh-multihop is running**
```bash
# Check for ssh-multihop processes
ps aux | grep ssh-multihop | grep -v grep
```

**Step 2: If ssh-multihop is running, stop it safely**
```bash
# Find the process
SSH_MULTIHOP_PID=$(pgrep -f "ssh-multihop daemon")

# If found, stop gracefully
if [ -n "$SSH_MULTIHOP_PID" ]; then
    echo "Stopping ssh-multihop daemon (PID: $SSH_MULTIHOP_PID)"
    kill -TERM $SSH_MULTIHOP_PID  # Graceful shutdown first
    sleep 2

    # If still running, force kill
    if ps -p $SSH_MULTIHOP_PID > /dev/null; then
        echo "Force killing ssh-multihop daemon"
        kill -9 $SSH_MULTIHOP_PID
    fi
fi
```

**Step 3: If port is occupied by other process, choose different port**
```bash
# Check what's using port 8080
lsof -i:8080

# If it's NOT ssh-multihop, DO NOT KILL IT!
# Instead, use a different port:
./ssh-multihop daemon --port 8081
```

**Step 4: Find an available port automatically**
```bash
# Find available port (this script finds one)
./docs/scripts/find-available-port.sh
```

### Database locked
```bash
# Check if another daemon is running first
ps aux | grep ssh-multihop | grep -v grep

# If no daemon running, remove stale lock
rm -f ~/.ssh-multihop/ssh-multihop-fwd.db
```

### SSH connection fails
```bash
# Test SSH config
ssh -G vmr.u24

# Test connection directly
ssh vmr.u24 "echo 'connection works'"

# Check SSH key permissions
ls -la ~/.ssh/id_rsa*
```

## See Also
- [Architecture Documentation](../ARCHITECTURE.md)
- [API Reference](../api/REFERENCE.md)
```

**Step 2: Make scripts executable**

```bash
chmod +x docs/scripts/*.sh
```

**Step 3: Commit scripts documentation**

```bash
git add docs/scripts/README.md
git commit -m "docs: add comprehensive test scripts documentation"
```

---

### Task 5.5: Create Safe Port Helper Script

**Files:**
- Create: `docs/scripts/find-available-port.sh`
- Create: `docs/scripts/stop-daemon.sh`

**Step 1: Create find-available-port.sh helper**

Create file: `docs/scripts/find-available-port.sh`

```bash
#!/bin/bash
# Find an available port starting from a given port
# Usage: find-available-port.sh [start_port]
# Default start port: 8080

START_PORT=${1:-8080}
END_PORT=9080  # Try up to 1000 ports above start

for port in $(seq $START_PORT $END_PORT); do
    if ! lsof -i:$port > /dev/null 2>&1; then
        echo $port
        exit 0
    fi
done

echo "ERROR: No available port found in range $START_PORT-$END_PORT" >&2
exit 1
```

**Step 2: Create safe daemon stop script**

Create file: `docs/scripts/stop-daemon.sh`

```bash
#!/bin/bash
# Safely stop ssh-multihop daemon
# Usage: stop-daemon.sh

echo "Checking for ssh-multihop daemon processes..."

# Find ssh-multihop daemon processes
SSH_MULTIHOP_PIDS=$(pgrep -f "ssh-multihop daemon")

if [ -z "$SSH_MULTIHOP_PIDS" ]; then
    echo "✓ No ssh-multihop daemon running"
    exit 0
fi

echo "Found ssh-multihop daemon process(es):"
echo "$SSH_MULTIHOP_PIDS"

# Confirm before killing
read -p "Stop these daemon(s)? [y/N] " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    for PID in $SSH_MULTIHOP_PIDS; do
        echo "Stopping PID $PID gracefully..."
        kill -TERM $PID

        # Wait up to 5 seconds for graceful shutdown
        for i in {1..5}; do
            if ! ps -p $PID > /dev/null 2>&1; then
                echo "✓ Process $PID stopped gracefully"
                break
            fi
            sleep 1
        done

        # Force kill if still running
        if ps -p $PID > /dev/null 2>&1; then
            echo "⚠ Process $PID did not stop gracefully, force killing..."
            kill -9 $PID
            echo "✓ Process $PID force killed"
        fi
    done

    # Verify no processes left
    REMAINING=$(pgrep -f "ssh-multihop daemon")
    if [ -z "$REMAINING" ]; then
        echo "✓ All ssh-multihop daemons stopped"
    else
        echo "✗ Some processes still running: $REMAINING"
        exit 1
    fi
else
    echo "✗ Cancelled"
    exit 1
fi
```

**Step 3: Create check-port.sh helper**

Create file: `docs/scripts/check-port.sh`

```bash
#!/bin/bash
# Check what process is using a port
# Usage: check-port.sh [port]
# Default port: 8080

PORT=${1:-8080}

echo "Checking what's using port $PORT..."

# Check if anything is using the port
PORT_USER=$(lsof -i:$PORT -t)

if [ -z "$PORT_USER" ]; then
    echo "✓ Port $PORT is available"
    exit 0
fi

echo "⚠ Port $PORT is occupied by:"
lsof -i:$PORT

# Check if it's ssh-multihop
if ps -p $PORT_USER -o command= | grep -q "ssh-multihop"; then
    echo ""
    echo "This is ssh-multihop daemon. Safe to stop with:"
    echo "  ./docs/scripts/stop-daemon.sh"
    exit 2
else
    echo ""
    echo "⚠ WARNING: Port is occupied by a DIFFERENT process!"
    echo "DO NOT kill this process - it may be important!"
    echo ""
    echo "Options:"
    echo "  1. Use a different port: ./ssh-multihop daemon --port 8081"
    echo "  2. Find available port: ./docs/scripts/find-available-port.sh"
    echo "  3. Stop the other process manually if you know what it is"
    exit 3
fi
```

**Step 4: Update README.md to reference helper scripts**

File: `docs/scripts/README.md`

Add to "Running Tests" section:
```markdown
## Running Tests

### Pre-flight Checks

```bash
# Check if default port is available
cd docs/scripts
./check-port.sh 8080

# If port is occupied, find an available one
./find-available-port.sh 8080

# If ssh-multihop daemon is running, stop it safely
./stop-daemon.sh
```

### Start Daemon

```bash
# Using default port 8080
./ssh-multihop daemon --port 8080

# Or use an available port found by helper
AVAILABLE_PORT=$(./docs/scripts/find-available-port.sh)
./ssh-multihop daemon --port $AVAILABLE_PORT
```

## Helper Scripts

- **`check-port.sh`** - Check what's using a port (safe inspection)
- **`stop-daemon.sh`** - Safely stop ssh-multihop daemon with confirmation
- **`find-available-port.sh`** - Find an available port automatically
```

**Step 5: Make helper scripts executable**

```bash
chmod +x docs/scripts/check-port.sh
chmod +x docs/scripts/stop-daemon.sh
chmod +x docs/scripts/find-available-port.sh
```

**Step 6: Test helper scripts**

```bash
# Test 1: Check current port
cd docs/scripts
./check-port.sh 8080

# Test 2: Find available port
./find-available-port.sh 8080

# Test 3: Start daemon on available port
AVAILABLE_PORT=$(./find-available-port.sh)
../../ssh-multihop daemon --port $AVAILABLE_PORT &
DAEMON_PID=$!

# Test 4: Check port again (should show ssh-multihop)
./check-port.sh $AVAILABLE_PORT

# Test 5: Stop daemon safely
./stop-daemon.sh
```

**Step 7: Commit helper scripts**

```bash
git add docs/scripts/*.sh docs/scripts/README.md
git commit -m "docs: add safe port management helper scripts

- check-port.sh: Inspect port usage safely (no kill)
- stop-daemon.sh: Gracefully stop ssh-multihop with confirmation
- find-available-port.sh: Auto-find available ports

These scripts prevent accidental killing of important processes
that happen to be using the same port."
```

---

## Phase 6: Verify and Organize Code Comments (2 hours)

### Task 6: Audit Package Comments

**Objective:** Verify every package has accurate documentation comments.

**Files:**
- Review: `internal/*/doc.go` or package headers in main files
- Modify: Add/improve package documentation where needed

**Step 1: List all packages**

```bash
find internal -type d -mindepth 1 -maxdepth 1 | sort
```

Expected output:
```
internal/agent
internal/api
internal/config
internal/connection
internal/db
internal/forwarding
internal/service
internal/tunnel
internal/util
```

**Step 2: Check for existing doc.go files**

```bash
find internal -name "doc.go"
```

**Step 3: Review existing package comments**

```bash
# Check each package's main Go file for package comments
head -30 internal/forwarding/forward.go
head -30 internal/service/forward_service.go
head -30 internal/api/handlers.go
head -30 internal/connection/builder.go
head -30 internal/config/parser.go
```

**Step 4: Create package comment checklist**

Create temporary checklist: `/tmp/package-comments-audit.md`

```markdown
# Package Comments Audit

## Audit Criteria
For each package, verify:
- [ ] Package has doc comment (either in doc.go or main file)
- [ ] Comment accurately explains package purpose
- [ ] Comment lists key exported types and functions
- [ ] Comment follows Go conventions (starts with "Package X")
- [ ] Comment matches actual implementation

## Packages to Audit

### agent/
- Has doc comment: [ ] Yes [ ] No
- File location: ____________
- Accuracy check: [ ] Matches code [ ] Needs update
- Issues found: ____________

### api/
- Has doc comment: [ ] Yes [ ] No
- File location: ____________
- Accuracy check: [ ] Matches code [ ] Needs update
- Issues found: ____________

### config/
- Has doc comment: [ ] Yes [ ] No
- File location: ____________
- Accuracy check: [ ] Matches code [ ] Needs update
- Issues found: ____________

### connection/
- Has doc comment: [ ] Yes [ ] No
- File location: ____________
- Accuracy check: [ ] Matches code [ ] Needs update
- Issues found: ____________

### db/
- Has doc comment: [ ] Yes [ ] No
- File location: ____________
- Accuracy check: [ ] Matches code [ ] Needs update
- Issues found: ____________

### forwarding/
- Has doc comment: [ ] Yes [ ] No
- File location: ____________
- Accuracy check: [ ] Matches code [ ] Needs update
- Issues found: ____________

### service/
- Has doc comment: [ ] Yes [ ] No
- File location: ____________
- Accuracy check: [ ] Matches code [ ] Needs update
- Issues found: ____________

### tunnel/
- Has doc comment: [ ] Yes [ ] No
- File location: ____________
- Accuracy check: [ ] Matches code [ ] Needs update
- Issues found: ____________

### util/
- Has doc comment: [ ] Yes [ ] No
- File location: ____________
- Accuracy check: [ ] Matches code [ ] Needs update
- Issues found: ____________
```

**Step 5: Perform systematic audit**

For each package:
1. Read the package comment
2. Read the main exported types and functions
3. Compare comment with actual implementation
4. Document any mismatches

**Step 6: Document audit findings**

Save findings to memory (do not commit yet):
- Packages with accurate comments: _____
- Packages needing updates: _____
- Specific issues per package: ____________

---

### Task 7: Fix Inaccurate Package Comments

**Objective:** Ensure package comments accurately reflect the code.

**Files:**
- Modify: Package comment files or main package files

**Step 1: For each package with inaccurate comments**

Read the current comment and compare with actual exports:
```bash
# Example for forwarding package
grep "^func\|^type" internal/forwarding/*.go | grep -v "_test.go" | grep "^[^/]*func [A-Z]"
grep "^type [A-Z]" internal/forwarding/*.go | grep -v "_test.go"
```

**Step 2: Update comments to match implementation**

**Example update for internal/forwarding/forward.go:**

If comment says "provides three forward types" but code actually has four, update:
```go
// Package forwarding provides port forwarding implementations.
//
// The package supports four forward types:
//   - LocalListenToRemote: SSH -L (local listen to remote)
//   - RemoteListenToLocal: SSH -R (remote listen to local)
//   - RemoteListenToRemote: Remote-to-remote bridging
//   - InlineForwardOrchestrator: Composed forwarding using UDS bridge
//
// All forwards follow the simplified architecture:
//   - Fail fast on errors (no internal retry logic)
//   - Update database status on errors
//   - ForwardService handles rebuild and recovery
//
// Key interfaces:
//   - Forward: Unified port forwarding interface
//   - ForwardManager: Lifecycle management
//
// All forwards support graceful shutdown via context cancellation.
package forwarding
```

**Step 3: Verify comment accuracy**

For each updated comment, verify:
1. All exported types mentioned actually exist
2. Descriptions match actual behavior
3. No mentioned features have been removed
4. No new features are missing from comment

**Step 4: Run tests to ensure no functional changes**

```bash
go test ./...
```

**Step 5: Commit package comment fixes**

```bash
git add internal/
git commit -m "docs: fix inaccurate package comments to match implementation"
```

---

### Task 8: Verify Function Comments Match Implementation

**Objective:** Ensure exported function comments accurately describe behavior.

**Files:**
- Review: All exported functions in `internal/`
- Modify: Comments that don't match implementation

**Step 1: Find all exported functions**

```bash
# List all exported functions
grep -rn "^func [A-Z]" internal --include="*.go" | grep -v "_test.go" | wc -l
```

**Step 2: Sample check for comment accuracy**

Check key functions in each package:
```bash
# forwarding package
grep -A 5 "^func [A-Z].*Start" internal/forwarding/*.go
grep -A 5 "^func [A-Z].*Stop" internal/forwarding/*.go
grep -A 5 "^func [A-Z].*HealthCheck" internal/forwarding/*.go

# service package
grep -A 5 "^func [A-Z]" internal/service/forward_service.go | head -50

# api package
grep -A 5 "^func [A-Z]" internal/api/handlers.go | head -50
```

**Step 3: For each exported function, verify**

1. **Function signature:** Does comment mention parameters?
2. **Return values:** Does comment explain return values?
3. **Error conditions:** Does comment mention when errors occur?
4. **Behavior:** Does comment accurately describe what happens?
5. **Side effects:** Does comment mention database updates, goroutine launches, etc.?

**Step 4: Fix mismatched comments**

Example - if comment says "returns error if forward exists" but code actually returns nil:

Before (inaccurate):
```go
// CreateForward creates a new forward.
// Returns error if forward already exists.
func (s *ForwardService) CreateForward(fwd *db.Forward) error {
```

After (accurate):
```go
// CreateForward creates a new forward and starts it.
//
// If a forward with the same ID already exists in memory, it is replaced.
// The forward configuration is saved to the database before starting.
//
// Returns an error if:
//   - Database save fails
//   - Forward fails to start
//   - Invalid forward configuration
//
// This method is non-blocking: the forward runs in a background goroutine.
func (s *ForwardService) CreateForward(fwd *db.Forward) error {
```

**Step 5: Remove truly redundant comments**

Only remove comments that add zero value:
```go
// REMOVE: Comments that just repeat the function name
// GetID returns the ID
func (f *Forward) GetID() string { return f.id }

// KEEP: Comments that explain non-obvious behavior
// GetID returns the unique identifier for this forward.
// The ID is a UUID generated at creation time and persists across rebuilds.
func (f *Forward) GetID() string { return f.id }
```

**Step 6: Add missing important comments**

Look for complex logic without comments:
```bash
# Find functions >20 lines without comments
find internal -name "*.go" -not -name "*_test.go" -exec awk '/^func [A-Z]/ {p=1; name=$0} /^func [A-Z]/ && p==1 && NR>20 && name==prev {print FILENAME":"NR-20; p=0} {prev=name}' {} \;
```

Add comments for:
- Goroutine spawning (what runs, how it's controlled)
- Context usage (cancellation behavior)
- Database transactions (what's modified atomically)
- Channel operations (blocking vs non-blocking, buffering)
- Error handling (what errors mean, recovery strategy)

**Step 7: Run tests after comment changes**

```bash
go test ./...
go build ./...
```

**Step 8: Commit function comment improvements**

```bash
git add internal/
git commit -m "docs: verify and fix function comments to match implementation"
```

---

### Task 9: Review Struct and Interface Comments

**Objective:** Ensure type comments accurately describe purpose and usage.

**Files:**
- Review: All exported structs and interfaces
- Modify: Inaccurate or missing type comments

**Step 1: List all exported types**

```bash
# Find all exported structs and interfaces
grep -rn "^type [A-Z]" internal --include="*.go" | grep -v "_test.go"
```

**Step 2: Verify type comments**

For each exported type, check:
1. Purpose is clearly explained
2. Key fields are documented
3. Usage context is provided
4. Thread-safety is mentioned if applicable
5. Lifecycle is explained (Start/Stop, Open/Close, etc.)

**Step 3: Fix struct comments**

Example - incomplete comment:

Before:
```go
// Forward represents a port forwarding configuration
type Forward interface {
```

After:
```go
// Forward represents a port forwarding configuration.
//
// Forward is self-contained: it manages its own connections,
// listeners, health checks, and data forwarding. Once started,
// it runs independently until stopped.
//
// Lifecycle:
//   - Start() begins forwarding (blocks until stopped or error)
//   - Stop() gracefully terminates forwarding
//   - HealthCheck() verifies the forward is working
//
// Thread-safety: Forward methods are not thread-safe.
// Call Start/Stop from a single goroutine.
type Forward interface {
```

**Step 4: Document important struct fields**

For structs with complex fields, add field comments:
```go
type ForwardService struct {
    // Database for persisting forward configurations
    db *db.Database

    // Active forwards indexed by forward ID
    forwards map[string]*ForwardWrapper

    // Channel for shutdown notification
    shutdownCh chan struct{}

    // Context for canceling all operations
    ctx    context.Context
    cancel context.CancelFunc
}
```

**Step 5: Verify interface comments mention all methods**

For interfaces, list key methods:
```go
// Forward defines the interface for port forwarding implementations.
//
// All forwards follow a common lifecycle pattern:
//   1. Created with configuration
//   2. Started with Start(context)
//   3. Health checked periodically
//   4. Stopped gracefully
//
// Implementations must handle context cancellation
// for graceful shutdown.
type Forward interface {
    // Start begins the port forwarding.
    // ... (detailed method comments)
    Start(ctx context.Context) error

    // Stop gracefully stops the port forwarding.
    // ... (detailed method comments)
    Stop() error

    // HealthCheck performs an active health check.
    // ... (detailed method comments)
    HealthCheck() error
    // ... other methods
}
```

**Step 6: Commit type comment improvements**

```bash
git add internal/
git commit -m "docs: improve struct and interface documentation"
```

---

## Phase 7: Update CLAUDE.md (15 minutes)

### Task 9: Sync CLAUDE.md with New Documentation Structure

**Files:**
- Modify: `CLAUDE.md`

**Step 1: Review CLAUDE.md documentation section**

Current section around line 43:
```markdown
## Documentation

- **[SIMPLIFIED_ARCHITECTURE.md](SIMPLIFIED_ARCHITECTURE.md)** - System architecture and design principles
- **[docs/](docs/)** - Additional documentation (API reference, testing guides)
```

**Step 2: Update documentation references**

File: `CLAUDE.md`

Replace section with:
```markdown
## Documentation

### Primary Documentation
- **[README.md](README.md)** - Project overview and quick start
- **[ARCHITECTURE.md](ARCHITECTURE.md)** - System architecture and design principles
- **[SETUID_SUPPORT.md](SETUID_SUPPORT.md)** - Running with elevated privileges

### Developer Documentation
- **[docs/](docs/)** - Comprehensive developer documentation
  - [API Reference](docs/api/REFERENCE.md) - REST API documentation
  - [Documentation Standards](docs/DOCUMENTATION_STANDARDS.md) - Writing guidelines
  - [Test Scripts](docs/scripts/README.md) - Testing guide
  - [Archive](docs/archive/) - Historical documentation

### See Also
- **[CLAUDE.md](CLAUDE.md)** - This file (project instructions for Claude Code)
```

**Step 3: Commit CLAUDE.md update**

```bash
git add CLAUDE.md
git commit -m "docs: update CLAUDE.md documentation links"
```

---

## Phase 8: Final Verification (30 minutes)

### Task 10: Verify Documentation Links and Build

**Step 1: Check all markdown links**

```bash
# Find all .md files
find . -name "*.md" -type f | sort

# Verify main documentation exists
ls -la README.md ARCHITECTURE.md SETUID_SUPPORT.md CLAUDE.md
ls -la docs/index.md docs/DOCUMENTATION_STANDARDS.md
ls -la docs/api/REFERENCE.md
ls -la docs/scripts/README.md
ls -la docs/archive/README.md
```

**Step 2: Verify no broken links in docs**

```bash
# Check for references to old file names
grep -r "SIMPLIFIED_ARCHITECTURE" docs/ README.md 2>/dev/null
grep -r "NEW-API-REFERENCE" docs/ README.md 2>/dev/null
grep -r "refactor-summary.md" docs/ 2>/dev/null
```

If any found, update to new names.

**Step 3: Verify code still builds**

```bash
make clean
make build
```

**Step 4: Run tests**

```bash
make test
```

**Step 5: Run documentation linter (optional)**

```bash
# If markdown lint tool available
markdownlint *.md docs/**/*.md
```

**Step 6: Generate documentation coverage report**

Create file: `docs/DOCUMENTATION_COVERAGE.md`

```markdown
# Documentation Coverage Report

**Generated:** 2026-03-16
**Status:** ✅ Complete

## Package Documentation

| Package      | Doc Comment | Status |
|--------------|-------------|--------|
| agent        | ✅ Yes      | Complete |
| api          | ✅ Yes      | Complete |
| config       | ✅ Yes      | Complete |
| connection   | ✅ Yes      | Complete |
| db           | ✅ Yes      | Complete |
| forwarding   | ✅ Yes      | Complete |
| service      | ✅ Yes      | Complete |
| tunnel       | ✅ Yes      | Complete |
| util         | ✅ Yes      | Complete |

## User Documentation

| Document            | Status    | Location               |
|---------------------|-----------|------------------------|
| README              | ✅ Current | /README.md             |
| Architecture        | ✅ Current | /ARCHITECTURE.md       |
| SETUID Support      | ✅ Current | /SETUID_SUPPORT.md     |
| CLAUDE.md           | ✅ Current | /CLAUDE.md             |
| API Reference       | ✅ Current | /docs/api/REFERENCE.md |
| Documentation Std   | ✅ Current | /docs/DOCUMENTATION_STANDARDS.md |
| Test Scripts        | ✅ Current | /docs/scripts/README.md|
| Archive Index       | ✅ Current | /docs/archive/README.md|

## Archived Documentation

| Document                                      | Status | Location                           |
|-----------------------------------------------|--------|------------------------------------|
| Refactor Summary                              | ✅ Archived | docs/archive/refactor-summary-2026-03-15.md |
| Refactor Verification                         | ✅ Archived | docs/archive/refactor-verification-2026-03-15.md |
| Project Structure Review                      | ✅ Archived | docs/archive/project-structure-review-2026-03-15.md |
| Unified Context Shutdown Implementation       | ✅ Archived | docs/archive/unified-context-shutdown-implementation-2026-03-16.md |

## Standards Compliance

✅ All file names follow naming conventions
✅ All documents use English (except technical terms)
✅ All documents follow Markdown template
✅ All packages have accurate documentation comments
✅ Function comments match implementation (verified)
✅ Struct comments document purpose and thread-safety
✅ Documentation index is complete and accurate

## Code Comment Verification

✅ Package comments audited for accuracy (9 packages)
✅ Exported function comments verified against implementation
✅ Struct/interface comments document purpose and lifecycle
✅ Inaccurate comments corrected (code logic is source of truth)
✅ Missing important comments added (goroutines, context usage, side effects)
✅ Truly redundant comments removed (only when zero value)

## Next Steps

1. ✅ Documentation standards established
2. ✅ Historical documentation archived
3. ✅ File naming standardized
4. ✅ Code comments verified and organized
5. ✅ All links verified
6. ✅ Coverage report generated

**Maintenance:**
- Follow `docs/DOCUMENTATION_STANDARDS.md` for new documentation
- When modifying code, update comments immediately
- Quarterly audit of comment accuracy recommended
```

**Step 7: Final commit**

```bash
git add docs/DOCUMENTATION_COVERAGE.md
git commit -m "docs: add documentation coverage report"
```

---

## Summary

This plan establishes a comprehensive documentation structure with focus on **accuracy and reliability**:

1. **Standards:** DOCUMENTATION_STANDARDS.md defines conventions
2. **Archive:** Historical docs moved to docs/archive/
3. **Naming:** Files renamed to follow conventions (ARCHITECTURE.md, REFERENCE.md)
4. **Index:** docs/index.md reorganized for clarity
5. **Scripts:** docs/scripts/README.md documents testing
6. **Code Comments:** Systematic verification and organization
   - Package comments audited for accuracy
   - Function comments verified against implementation
   - Type comments enhanced with purpose and lifecycle
   - **Code logic is source of truth** - comments corrected to match
   - Important comments added (goroutines, context, side effects)
7. **Verification:** All links checked, coverage report generated

**Key Philosophy:**
- **Accuracy over brevity:** Comments must match code, fixed if different
- **Documentation, not redundancy:** Keep comments that aid understanding
- **Systematic verification:** Check that comments reflect actual behavior
- **Code is truth:** When comment differs from code, fix the comment

**Total Time Estimate:** 5-6 hours
**Total Commits:** ~12 commits (one per phase/task)

**Outcome:** Clean, organized, maintainable documentation with **reliable, accurate code comments** that serve as trustworthy documentation for the codebase.
