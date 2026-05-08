#!/bin/bash
# Connection Pool Performance Benchmark Comparison
#
# This script compares the performance of creating multiple forwards
# with and without connection pooling.

set -e

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Configuration
DAEMON_PORT=18081
DB_PATH="/tmp/ssh-multihop-benchmark.db"
TEST_HOST="vmr.u24"
ITERATIONS=10

# Results storage
declare -a POOL_TIMES
declare -a NO_POOL_TIMES

log() {
    echo -e "${BLUE}[$(date '+%H:%M:%S')]${NC} $1"
}

success() {
    echo -e "${GREEN}✓${NC} $1"
}

failure() {
    echo -e "${RED}✗${NC} $1"
}

# Cleanup function
cleanup() {
    log "Cleaning up..."
    pkill -f "ssh-multihop daemon.*--port ${DAEMON_PORT}" 2>/dev/null || true
    rm -f "$DB_PATH"
    sleep 1
}

# Wait for daemon to be ready
wait_for_daemon() {
    local max_wait=10
    local waited=0

    while [ $waited -lt $max_wait ]; do
        if curl -s "http://localhost:${DAEMON_PORT}/health" > /dev/null 2>&1; then
            return 0
        fi
        sleep 1
        waited=$((waited + 1))
    done
    return 1
}

# Create a forward
create_forward() {
    local listen_addr=$1
    local service_addr=$2

    curl -s -X POST "http://localhost:${DAEMON_PORT}/api/v1/forwards" \
        -H "Content-Type: application/json" \
        -d "{
            \"type\": \"local_listen_to_remote\",
            \"listen_host\": \"local\",
            \"listen_addr\": \"$listen_addr\",
            \"service_host\": \"$TEST_HOST\",
            \"service_addr\": \"$service_addr\",
            \"description\": \"Benchmark forward\"
        }" > /dev/null 2>&1
}

# Wait for forward to be running
wait_for_forward() {
    local forward_id=$1
    local max_wait=30
    local waited=0

    while [ $waited -lt $max_wait ]; do
        local status
        status=$(curl -s "http://localhost:${DAEMON_PORT}/api/v1/status/$forward_id" 2>/dev/null | jq -r '.status' 2>/dev/null || echo "")

        if [ "$status" = "running" ]; then
            return 0
        fi

        sleep 1
        waited=$((waited + 1))
    done
    return 1
}

# Benchmark with connection pooling (same destination)
benchmark_with_pool() {
    log "Benchmarking WITH connection pooling (same destination)..."

    local start_time end_time elapsed

    start_time=$(date +%s.%N)

    # Create multiple forwards to the same destination (shares connection)
    for i in $(seq 1 $ITERATIONS); do
        local listen_addr="127.0.0.1:$((19000 + i))"
        create_forward "$listen_addr" "127.0.0.1:22"
    done

    # Wait for all forwards to start
    for i in $(seq 1 $ITERATIONS); do
        local listen_addr="127.0.0.1:$((19000 + i))"
        local forward_id="local_listen_to_remote-local-${listen_addr}-${TEST_HOST}-127.0.0.1:22"
        wait_for_forward "$forward_id"
    done

    end_time=$(date +%s.%N)
    elapsed=$(echo "$end_time - $start_time" | bc)

    success "With pooling: ${elapsed}s for $ITERATIONS forwards"
    echo "$elapsed"
}

# Benchmark without connection pooling (different destinations)
benchmark_without_pool() {
    log "Benchmarking WITHOUT connection pooling (different destinations)..."

    local start_time end_time elapsed

    start_time=$(date +%s.%N)

    # Create forwards to different service ports (simulates different destinations)
    for i in $(seq 1 $ITERATIONS); do
        local listen_addr="127.0.0.1:$((19100 + i))"
        local service_addr="127.0.0.1:$((22 + i))"
        create_forward "$listen_addr" "$service_addr"
    done

    # Wait for all forwards to start
    for i in $(seq 1 $ITERATIONS); do
        local listen_addr="127.0.0.1:$((19100 + i))"
        local service_addr="127.0.0.1:$((22 + i))"
        local forward_id="local_listen_to_remote-local-${listen_addr}-${TEST_HOST}-${service_addr}"
        wait_for_forward "$forward_id"
    done

    end_time=$(date +%s.%N)
    elapsed=$(echo "$end_time - $start_time" | bc)

    success "Without pooling: ${elapsed}s for $ITERATIONS forwards"
    echo "$elapsed"
}

# Main benchmark execution
main() {
    echo "========================================"
    echo "Connection Pool Performance Benchmark"
    echo "Time: $(date '+%Y-%m-%d %H:%M:%S')"
    echo "========================================"
    echo ""
    echo "Configuration:"
    echo "  - Test host: $TEST_HOST"
    echo "  - Iterations: $ITERATIONS"
    echo "  - Daemon port: $DAEMON_PORT"
    echo ""

    # Check if we're in the project root
    if [ ! -f "go.mod" ]; then
        failure "Error: Not in project root"
        exit 1
    fi

    # Check if test host is available
    log "Checking if test host is available..."
    if ! timeout 2 bash -c "cat < /dev/null > /dev/tcp/$TEST_HOST/22" 2>/dev/null; then
        failure "Test host $TEST_HOST is not available"
        exit 1
    fi
    success "Test host is available"
    echo ""

    # Build the project
    log "Building ssh-multihop..."
    if ! make build > /dev/null 2>&1; then
        failure "Build failed"
        exit 1
    fi
    success "Build complete"
    echo ""

    # Cleanup any existing daemon
    cleanup

    # Start daemon
    log "Starting daemon on port ${DAEMON_PORT}..."
    nohup ./ssh-multihop daemon --port "$DAEMON_PORT" --db "$DB_PATH" > /tmp/daemon-benchmark.log 2>&1 &
    sleep 3

    if ! wait_for_daemon; then
        failure "Daemon failed to start"
        cat /tmp/daemon-benchmark.log
        exit 1
    fi
    success "Daemon is ready"
    echo ""

    # Run benchmarks
    log "Running benchmarks..."
    echo ""

    # Benchmark with pooling
    pool_time=$(benchmark_with_pool)
    POOL_TIMES+=("$pool_time")

    # Clean up forwards
    log "Cleaning up forwards (with pooling)..."
    for i in $(seq 1 $ITERATIONS); do
        local listen_addr="127.0.0.1:$((19000 + i))"
        local forward_id="local_listen_to_remote-local-${listen_addr}-${TEST_HOST}-127.0.0.1:22"
        curl -s -X DELETE "http://localhost:${DAEMON_PORT}/api/v1/forwards/$forward_id" > /dev/null 2>&1
    done
    sleep 2
    echo ""

    # Benchmark without pooling
    no_pool_time=$(benchmark_without_pool)
    NO_POOL_TIMES+=("$no_pool_time")

    # Clean up forwards
    log "Cleaning up forwards (without pooling)..."
    for i in $(seq 1 $ITERATIONS); do
        local listen_addr="127.0.0.1:$((19100 + i))"
        local service_addr="127.0.0.1:$((22 + i))"
        local forward_id="local_listen_to_remote-local-${listen_addr}-${TEST_HOST}-${service_addr}"
        curl -s -X DELETE "http://localhost:${DAEMON_PORT}/api/v1/forwards/$forward_id" > /dev/null 2>&1
    done
    sleep 2
    echo ""

    # Calculate improvement
    improvement=$(echo "scale=2; ($no_pool_time - $pool_time) / $no_pool_time * 100" | bc)
    speedup=$(echo "scale=2; $no_pool_time / $pool_time" | bc)

    # Print results
    echo "========================================"
    echo "Benchmark Results"
    echo "========================================"
    echo ""
    echo "With Connection Pooling:"
    echo "  - Time: ${pool_time}s"
    echo "  - Throughput: $(echo "scale=2; $ITERATIONS / $pool_time" | bc) forwards/second"
    echo ""
    echo "Without Connection Pooling:"
    echo "  - Time: ${no_pool_time}s"
    echo "  - Throughput: $(echo "scale=2; $ITERATIONS / $no_pool_time" | bc) forwards/second"
    echo ""
    echo -e "${GREEN}Performance Improvement:${NC}"
    echo "  - Time saved: $(echo "scale=2; $no_pool_time - $pool_time" | bc)s (${improvement}% faster)"
    echo "  - Speedup: ${speedup}x"
    echo ""

    # Interpret results
    if (( $(echo "$speedup > 1.5" | bc -l) )); then
        success "Significant performance improvement with connection pooling!"
    elif (( $(echo "$speedup > 1.2" | bc -l) )); then
        success "Moderate performance improvement with connection pooling."
    else
        info "Marginal performance improvement (may be due to network latency)"
    fi
    echo ""

    # Cleanup
    cleanup
    success "Benchmark complete"
}

main
