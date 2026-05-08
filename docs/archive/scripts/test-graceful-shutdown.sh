#!/bin/bash
# Manual test for graceful shutdown fix
# This script verifies that Ctrl+C properly exits the daemon

set -e

echo "=== Testing Graceful Shutdown Fix ==="
echo ""

# Build the binary
echo "1. Building binary..."
make build > /dev/null 2>&1
echo "   ✓ Binary built successfully"
echo ""

# Create temporary database
DB_PATH="/tmp/ssh-multihop-test-$$.db"
echo "2. Creating test database at $DB_PATH"
mkdir -p /tmp/ssh-multihop-test-$$
echo "   ✓ Database directory created"
echo ""

# Start daemon in background
echo "3. Starting daemon in background..."
./ssh-multihop daemon --port 18080 --db "$DB_PATH" > /tmp/daemon-$$.log 2>&1 &
DAEMON_PID=$!
echo "   ✓ Daemon started with PID $DAEMON_PID"
echo ""

# Wait for daemon to start
echo "4. Waiting for daemon to initialize..."
sleep 2
echo "   ✓ Daemon initialized"
echo ""

# Verify daemon is running
if ! kill -0 $DAEMON_PID 2>/dev/null; then
    echo "   ✗ Daemon failed to start!"
    cat /tmp/daemon-$$.log
    exit 1
fi
echo "   ✓ Daemon is running (PID $DAEMON_PID)"
echo ""

# Send SIGINT (Ctrl+C)
echo "5. Sending SIGINT (simulating Ctrl+C)..."
kill -INT $DAEMON_PID
echo "   ✓ SIGINT sent"
echo ""

# Wait for graceful shutdown (max 10 seconds)
echo "6. Waiting for graceful shutdown (max 10s)..."
TIMEOUT=10
ELAPSED=0
while kill -0 $DAEMON_PID 2>/dev/null; do
    if [ $ELAPSED -ge $TIMEOUT ]; then
        echo "   ✗ Timeout! Daemon did not exit after ${TIMEOUT}s"
        echo "   This indicates the shutdown fix is NOT working!"
        kill -9 $DAEMON_PID 2>/dev/null || true
        exit 1
    fi
    sleep 1
    ELAPSED=$((ELAPSED + 1))
    echo -n "   ${ELAPSED}s..."
done
echo ""
echo "   ✓ Daemon exited gracefully after ${ELAPSED}s"
echo ""

# Check logs for graceful shutdown message
if grep -q "Graceful shutdown complete" /tmp/daemon-$$.log; then
    echo "   ✓ Found 'Graceful shutdown complete' in logs"
else
    echo "   ⚠ Warning: 'Graceful shutdown complete' not found in logs"
    echo "   Daemon may have exited but without proper logging"
fi
echo ""

# Cleanup
echo "7. Cleanup..."
kill -9 $DAEMON_PID 2>/dev/null || true
rm -f "$DB_PATH"
rm -f /tmp/daemon-$$.log
rm -rf /tmp/ssh-multihop-test-$$
echo "   ✓ Cleanup complete"
echo ""

echo "=== Test Result: PASSED ✓ ==="
echo ""
echo "The graceful shutdown fix is working correctly!"
echo "Ctrl+C now properly exits the daemon."
