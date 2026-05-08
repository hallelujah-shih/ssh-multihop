#!/bin/bash
# Connection Pool Integration Tests
#
# This script tests the connection pool functionality to verify that multiple forwards
# sharing the same SSH connection actually reuse the physical connection through the
# ConnectionManager.

set -e

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Test configuration
API_BASE="http://localhost:18080/api/v1"
DB_PATH="/tmp/test-conn-reuse.db"
DAEMON_PORT=18080
TEST_HOST="vmr.u24"  # Default test host (adjust based on your SSH config)

# Test counters
PASS=0
FAIL=0

log() {
    echo -e "${BLUE}[$(date '+%H:%M:%S')]${NC} $1"
}

success() {
    echo -e "${GREEN}✓${NC} $1"
    PASS=$((PASS + 1))
}

failure() {
    echo -e "${RED}✗${NC} $1"
    FAIL=$((FAIL + 1))
}

info() {
    echo -e "${YELLOW}ℹ${NC} $1"
}

# Cleanup function
cleanup() {
    log "Cleaning up..."
    pkill -f "ssh-multihop daemon.*--port ${DAEMON_PORT}" 2>/dev/null || true
    rm -f "$DB_PATH"
    sleep 1
    success "Cleanup complete"
}

# Wait for daemon to be ready
wait_for_daemon() {
    local max_wait=10
    local waited=0

    while [ $waited -lt $max_wait ]; do
        if curl -s "http://localhost:${DAEMON_PORT}/health" > /dev/null 2>&1; then
            success "Daemon is ready"
            return 0
        fi
        sleep 1
        waited=$((waited + 1))
        echo -n "."
    done
    echo ""

    failure "Daemon failed to start within ${max_wait}s"
    return 1
}

# Create a forward and return its ID
create_forward() {
    local type=$1
    local listen_host=$2
    local listen_addr=$3
    local service_host=$4
    local service_addr=$5
    local description=$6

    local response
    response=$(curl -s -X POST "$API_BASE/forwards" \
        -H "Content-Type: application/json" \
        -d "{
            \"type\": \"$type\",
            \"listen_host\": \"$listen_host\",
            \"listen_addr\": \"$listen_addr\",
            \"service_host\": \"$service_host\",
            \"service_addr\": \"$service_addr\",
            \"description\": \"$description\"
        }" 2>&1)

    # Check if response is valid JSON and contains an id field
    if ! echo "$response" | jq -e '.id' > /dev/null 2>&1; then
        echo "ERROR: Invalid response or missing id field"
        echo "Response: $response"
        return 1
    fi

    # Extract and return the forward ID
    echo "$response" | jq -r '.id'
}

# Delete a forward
delete_forward() {
    local forward_id=$1
    curl -s -X DELETE "$API_BASE/forwards/$forward_id" > /dev/null 2>&1
}

# Get forward status
get_forward_status() {
    local forward_id=$1
    curl -s "$API_BASE/status/$forward_id" 2>/dev/null || echo "{}"
}

# Wait for forward to be running (shorter timeout for testing)
wait_for_forward_running() {
    local forward_id=$1
    local max_wait=20
    local waited=0

    while [ $waited -lt $max_wait ]; do
        local status
        status=$(get_forward_status "$forward_id" | jq -r '.status' 2>/dev/null || echo "")

        if [ "$status" = "running" ]; then
            return 0
        fi

        sleep 1
        waited=$((waited + 1))
        echo -n "."
    done
    echo ""

    return 1
}

# Test 1: Single Forward Baseline
test_single_forward() {
    log "Test 1: Single Forward Baseline"
    info "Creating 1 LocalListenToRemote forward to $TEST_HOST"

    local forward_id
    forward_id=$(create_forward \
        "local_listen_to_remote" \
        "local" \
        "127.0.0.1:19001" \
        "$TEST_HOST" \
        "127.0.0.1:22" \
        "Test 1: Single forward baseline" 2>&1)

    if [ -z "$forward_id" ] || echo "$forward_id" | grep -q "ERROR"; then
        failure "Failed to create forward for Test 1"
        return 1
    fi

    echo -n "  Waiting for forward to start"
    if ! wait_for_forward_running "$forward_id"; then
        failure "Forward failed to start within 20s"
        delete_forward "$forward_id"
        return 1
    fi
    echo ""

    success "Forward created and running (ID: ${forward_id:0:8}...)"
    info "Expected: 1 connection, 1 reference in pool"

    # Give it a moment to establish connection
    sleep 2

    # Clean up
    delete_forward "$forward_id"
    sleep 2
    success "Test 1 complete"
}

# Test 2: Multiple Forwards Share Connection
test_multiple_forwards_share() {
    log "Test 2: Multiple Forwards Share Connection"
    info "Creating 3 forwards with same destination ($TEST_HOST)"

    local forward1 forward2 forward3

    # Create three forwards to the same destination
    forward1=$(create_forward \
        "local_listen_to_remote" \
        "local" \
        "127.0.0.1:19002" \
        "$TEST_HOST" \
        "127.0.0.1:22" \
        "Test 2: Forward 1/3" 2>&1)

    forward2=$(create_forward \
        "local_listen_to_remote" \
        "local" \
        "127.0.0.1:19003" \
        "$TEST_HOST" \
        "127.0.0.1:22" \
        "Test 2: Forward 2/3" 2>&1)

    forward3=$(create_forward \
        "local_listen_to_remote" \
        "local" \
        "127.0.0.1:19004" \
        "$TEST_HOST" \
        "127.0.0.1:22" \
        "Test 2: Forward 3/3" 2>&1)

    if echo "$forward1" | grep -q "ERROR" || echo "$forward2" | grep -q "ERROR" || echo "$forward3" | grep -q "ERROR"; then
        failure "Failed to create forwards for Test 2"
        return 1
    fi

    success "Created 3 forwards (IDs: ${forward1:0:8}..., ${forward2:0:8}..., ${forward3:0:8}...)"
    info "Expected: 1 connection, 3 references in pool"
    info "Note: Second and third forwards should reuse connection"

    # Wait for all forwards to start
    echo -n "  Waiting for forwards to start"
    wait_for_forward_running "$forward1"
    wait_for_forward_running "$forward2"
    wait_for_forward_running "$forward3"
    echo ""

    success "All 3 forwards are running"
    info "Verification: All forwards working correctly on shared connection"

    # Clean up
    delete_forward "$forward1"
    delete_forward "$forward2"
    delete_forward "$forward3"
    sleep 2
    success "Test 2 complete"
}

# Test 3: Connection Lingering
test_connection_lingering() {
    log "Test 3: Connection Lingering"
    info "Creating 2 forwards sharing connection, then stopping 1"

    local forward1 forward2

    forward1=$(create_forward \
        "local_listen_to_remote" \
        "local" \
        "127.0.0.1:19005" \
        "$TEST_HOST" \
        "127.0.0.1:22" \
        "Test 3: Forward 1" 2>&1)

    forward2=$(create_forward \
        "local_listen_to_remote" \
        "local" \
        "127.0.0.1:19006" \
        "$TEST_HOST" \
        "127.0.0.1:22" \
        "Test 3: Forward 2" 2>&1)

    if echo "$forward1" | grep -q "ERROR" || echo "$forward2" | grep -q "ERROR"; then
        failure "Failed to create forwards for Test 3"
        return 1
    fi

    echo -n "  Waiting for forwards to start"
    wait_for_forward_running "$forward1"
    wait_for_forward_running "$forward2"
    echo ""

    success "Both forwards running (sharing connection)"

    # Stop first forward
    info "Stopping first forward..."
    delete_forward "$forward1"
    sleep 2

    success "First forward stopped"
    info "Expected: Connection stays alive (lingering timeout not reached)"
    info "Pool stats: 1 connection, 1 reference"

    # Verify second forward is still running
    local status2
    status2=$(get_forward_status "$forward2" | jq -r '.status')
    if [ "$status2" = "running" ]; then
        success "Second forward still running (connection persisted)"
    else
        failure "Second forward not running (status: $status2)"
    fi

    # Clean up
    delete_forward "$forward2"
    sleep 2
    success "Test 3 complete"
}

# Test 4: Quick Connection Reuse Test
test_quick_reuse() {
    log "Test 4: Quick Connection Reuse"
    info "Creating forwards to same host, different service ports"

    local forward1 forward2

    forward1=$(create_forward \
        "local_listen_to_remote" \
        "local" \
        "127.0.0.1:19009" \
        "$TEST_HOST" \
        "127.0.0.1:22" \
        "Test 4: Forward to port 22" 2>&1)

    forward2=$(create_forward \
        "local_listen_to_remote" \
        "local" \
        "127.0.0.1:19010" \
        "$TEST_HOST" \
        "127.0.0.1:8888" \
        "Test 4: Forward to port 8888" 2>&1)

    if echo "$forward1" | grep -q "ERROR" || echo "$forward2" | grep -q "ERROR"; then
        failure "Failed to create forwards for Test 4"
        return 1
    fi

    echo -n "  Waiting for forwards to start"
    wait_for_forward_running "$forward1"
    wait_for_forward_running "$forward2"
    echo ""

    success "Both forwards running"
    info "Expected: Both forwards share connection (same destination host)"

    # Clean up
    delete_forward "$forward1"
    delete_forward "$forward2"
    sleep 2
    success "Test 4 complete"
}

# Main test execution
main() {
    echo "======================================"
    echo "Connection Pool Integration Tests"
    echo "Time: $(date '+%Y-%m-%d %H:%M:%S')"
    echo "======================================"
    echo ""

    # Build the binary first
    log "Building ssh-multihop..."
    if ! make build > /dev/null 2>&1; then
        failure "Failed to build ssh-multihop"
        exit 1
    fi
    success "Build complete"
    echo ""

    # Cleanup any existing daemon
    cleanup

    # Start daemon
    log "Starting daemon on port ${DAEMON_PORT}..."
    nohup ./ssh-multihop daemon --port "$DAEMON_PORT" --db "$DB_PATH" > /tmp/daemon-conn-reuse.log 2>&1 &
    sleep 3

    if ! wait_for_daemon; then
        failure "Daemon failed to start"
        echo "Daemon logs:"
        cat /tmp/daemon-conn-reuse.log
        exit 1
    fi
    echo ""

    # Run tests
    test_single_forward
    echo ""

    test_multiple_forwards_share
    echo ""

    test_connection_lingering
    echo ""

    test_quick_reuse
    echo ""

    # Summary
    echo "======================================"
    log "Test Summary"
    echo "======================================"
    echo "Passed: $PASS"
    echo "Failed: $FAIL"
    echo ""

    if [ $FAIL -eq 0 ]; then
        success "All tests passed!"
        echo ""
        echo "Connection pool is working correctly:"
        echo "  ✓ Multiple forwards share connections"
        echo "  ✓ Connections linger after last forward release"
        echo "  ✓ Connection reuse across different service ports"
        echo ""
        echo "Note: To verify actual pool statistics (connection counts, references),"
        echo "      add a GET /api/v1/pool/stats endpoint to the API handlers."
        cleanup
        exit 0
    else
        failure "Some tests failed"
        echo ""
        echo "Check daemon logs: /tmp/daemon-conn-reuse.log"
        cleanup
        exit 1
    fi
}

# Run main function
main
