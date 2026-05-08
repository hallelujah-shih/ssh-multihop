# Unused Functions Analysis

**Date:** 2026-03-16
**Total Unused:** 8 functions
**Analysis Scope:** All golangci-lint reported unused functions

---

## 1. Test Helper Functions (3 functions)

### Location: `internal/api/testutil.go`

#### 1.1 `createTestService(t *testing.T) (*service.ForwardService, *db.Database, func())`

**Purpose:** Creates a ForwardService with temporary database for testing

**Status:** ✅ **ACTUALLY USED** (False Positive)

**Evidence:**
```bash
$ grep -r "setupIntegrationTest" internal/api/integration_test.go
# Found 6 usages across different test functions
```

**Used By:**
- `integration_test.go` - All integration tests use `setupIntegrationTest`
- `setupIntegrationTest` internally calls `createTestService`

**Conclusion:** False positive from linter. Keep this function.

---

#### 1.2 `createTestServer(t *testing.T, svc *service.ForwardService) *gin.Engine`

**Purpose:** Creates a test HTTP server with Gin router and API handlers

**Status:** ✅ **ACTUALLY USED** (False Positive)

**Used By:**
- `setupIntegrationTest()` → `createTestServer()`

**Conclusion:** False positive. Keep this function.

---

#### 1.3 `setupIntegrationTest(t *testing.T) (*service.ForwardService, *gin.Engine, string, func())`

**Purpose:** Creates complete test environment with service and server

**Status:** ✅ **ACTUALLY USED** (False Positive)

**Used By:** `internal/api/integration_test.go` - Used in 6 test functions:
- TestCreateForward
- TestListForwards
- TestGetForward
- TestDeleteForward
- TestListStatuses
- TestHealthCheck

**Conclusion:** Critical test infrastructure. Keep.

---

## 2. Test Utility Functions (1 function)

### Location: `internal/forwarding/testutil.go`

#### 2.1 `waitForStatus(forward Forward, expectedStatus ForwardStatus, timeout time.Duration) error`

**Purpose:** Waits for a forward to reach a specific status with timeout

**Status:** ⚠️ **UNUSED** (Potential Duplicate)

**Similar Implementation:**
```go
// MockForwardStatus.WaitForStatus (line 92-101)
func (m *MockForwardStatus) WaitForStatus(forwardID, expectedStatus string, timeout time.Duration) error {
    deadline := time.Now().Add(timeout)
    for time.Now().Before(deadline) {
        if m.GetStatus(forwardID) == expectedStatus {
            return nil
        }
        time.Sleep(50 * time.Millisecond)
    }
    return fmt.Errorf("timeout waiting for status...")
}
```

**Comparison:**

| Aspect | waitForStatus (standalone) | MockForwardStatus.WaitForStatus |
|--------|----------------------------|--------------------------------|
| Target | Forward interface (real) | MockForwardStatus (mock) |
| Status Check | Calls `forward.Status()` | Checks internal map |
| Use Case | Real forwards in tests | Mock objects in tests |
| Usage | **0** | **0** (both unused) |

**Analysis:**
- Two similar functions for different use cases
- Both are currently unused
- The standalone version is more flexible (works with any Forward)
- The mock version is tied to MockForwardStatus

**Recommendation:** Keep standalone `waitForStatus`, remove mock version if unused.

---

## 3. Connection Accept Utilities (2 functions)

### Location: `internal/forwarding/util.go`

#### 3.1 `acceptConnections(ctx context.Context, listener net.Listener, connCh chan<- net.Conn) error`

**Purpose:** Accepts connections from TCP listeners with context support

**Status:** ⚠️ **UNUSED** (Replaced by Inline Implementation)

**Current Implementation:** Accept loop with TCP deadline approach
```go
// Uses SetDeadline(1s) to allow context checking
tcpListener.SetDeadline(time.Now().Add(1 * time.Second))
```

**Why Unused:**
- All Forward types now have inline `acceptConnections` implementations
- Each Forward type implements its own loop for custom error handling

---

#### 3.2 `acceptConnectionsGeneric(ctx context.Context, listener net.Listener, connCh chan<- net.Conn) error`

**Purpose:** Accepts connections for non-TCP listeners (Unix sockets) using goroutine

**Status:** ⚠️ **UNUSED** (Replaced by Inline Implementation)

**Current Implementation:** Goroutine-based approach
```go
// Uses background goroutine + channel for non-TCP listeners
go func() {
    for {
        conn, err := listener.Accept()
        // ...
    }
}()
```

**Why Unused:**
- RemoteListenToLocal (which uses UDS) has inline implementation
- Custom error handling per Forward type

**Architecture Decision:**
Each Forward type now has its own `acceptConnections()` method instead of shared utility:
- `LocalListenToRemote.acceptConnections()` (line 261-295)
- `RemoteListenToLocal.acceptConnections()` (line 261-295)
- `RemoteListenToRemote.acceptConnections()` (line 349-383)

**Reason:**
- Forward-specific error handling
- Different listener types (TCP vs UDS)
- Integration with Forward state management

**Recommendation:** These can be safely removed. They were superseded by inline implementations.

---

## 4. Data Copying Functions (1 function)

### Location: `internal/forwarding/util.go`

#### 4.1 `copyData(dst net.Conn, src net.Conn) (int64, error)`

**Purpose:** Copies data with buffering, returns bytes written

**Status:** ⚠️ **UNUSED** (Superseded by bidirectionalCopy)

**Similar Implementation:** `bidirectionalCopy(conn1, conn2) error`

**Comparison:**

| Aspect | copyData | bidirectionalCopy |
|--------|----------|-------------------|
| Direction | Unidirectional (src → dst) | Bidirectional |
| Buffering | 32KB buffer | io.Copy (default buffer) |
| Return | Bytes written, error | Error only |
| Usage | **0** | **3** (all Forward types) |

**Why bidirectionalCopy is preferred:**
- SSH forwarding always requires bidirectional data flow
- Simpler error handling with `io.Copy`
- Goroutine-based parallel copying

**Recommendation:** Remove `copyData`. It's a legacy function from early development.

---

## 5. Conversion Utility (1 function)

### Location: `internal/service/forward_service.go`

#### 5.1 `convertToHopConfigPtrs(hops []*tunnel.HopConfig) []*tunnel.HopConfig`

**Purpose:** Converts slice of HopConfig pointers to... the same type

**Implementation:**
```go
func convertToHopConfigPtrs(hops []*tunnel.HopConfig) []*tunnel.HopConfig {
    return hops  // No-op! Just returns input
}
```

**Status:** ❌ **USELESS NO-OP FUNCTION**

**Analysis:**
- Input and output types are identical
- Function does nothing
- Not used anywhere in codebase
- Likely leftover from refactoring when type signatures changed

**Recommendation:** **DELETE IMMEDIATELY** - This is dead code with no purpose.

---

## Summary and Recommendations

### Immediate Actions (Safe to Delete)

1. **DELETE** `convertToHopConfigPtrs` - Useless no-op function
2. **DELETE** `copyData` - Superseded by bidirectionalCopy
3. **DELETE** `acceptConnections` - Replaced by inline implementations
4. **DELETE** `acceptConnectionsGeneric` - Replaced by inline implementations

### Keep (False Positives)

5. **KEEP** `createTestService` - Used by integration tests
6. **KEEP** `createTestServer` - Used by integration tests
7. **KEEP** `setupIntegrationTest` - Used by integration tests

### Review (Maybe Keep)

8. **REVIEW** `waitForStatus` - Potentially useful for future tests

**Note:** 3 of the "unused" functions are actually used (false positives from golangci-lint).

---

## Architecture Evolution

The unused functions in `util.go` reveal an architectural evolution:

**Phase 1 (Early):** Shared utility functions
- `acceptConnections()`, `acceptConnectionsGeneric()`
- `copyData()`

**Phase 2 (Current):** Inline implementations per Forward type
- Each Forward has its own `acceptConnections()` method
- Custom error handling per Forward
- Better integration with Forward lifecycle

**Phase 3 (Future):** Potential for interface-based design
```go
type ConnectionAcceptor interface {
    acceptConnections(ctx context.Context) error
}
```

**Rationale:** The move from shared utilities to inline implementations was intentional:
- Clearer error handling per Forward type
- Easier to test individual Forward types
- More flexibility for Forward-specific behavior
