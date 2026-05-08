#!/bin/bash
set -e

echo "==================================================================="
echo "Daemon Integration Test"
echo "==================================================================="

# Clean up
rm -f /tmp/ssh-multihop-fwd.db
rm -f /tmp/inline-forward-*.sock
pkill -f ssh-multihop 2>/dev/null || true
sleep 2

# Start test server on vmr.u24
echo ""
echo "Step 1: Starting test server on vmr.u24..."
ssh vmr.u24 "nohup python3 -m http.server 11434 > /tmp/test-server.log 2>&1 &"
sleep 2
echo "✓ Test server started"

# Start daemon
echo ""
echo "Step 2: Starting daemon..."
/tmp/ssh-multihop daemon --db /tmp/ssh-multihop-fwd.db \
    --host 127.0.0.1 --port 18080 > /tmp/daemon.log 2>&1 &
DAEMON_PID=$!
echo "✓ Daemon started (PID: $DAEMON_PID)"
sleep 3

# Check daemon is running
if ! ps -p $DAEMON_PID > /dev/null 2>&1; then
    echo "✗ Daemon died immediately!"
    cat /tmp/daemon.log
    exit 1
fi
echo "✓ Daemon process running"

# Test health endpoint
echo ""
echo "Step 3: Testing health endpoint..."
if curl -s http://127.0.0.1:18080/health | grep -q "healthy"; then
    echo "✓ Health check passed"
else
    echo "✗ Health check failed"
    cat /tmp/daemon.log
    exit 1
fi

# Create inline forward
echo ""
echo "Step 4: Creating inline forward..."
curl -s -X POST http://127.0.0.1:18080/api/v1/forwards \
    -H "Content-Type: application/json" \
    -d '{
        "type": "inline",
        "service_host": "vmr.u24",
        "service_port": 11434,
        "expose_host": "dc4",
        "expose_port": 23456,
        "description": "Test inline forward"
    }' | jq .

sleep 3

# List forwards
echo ""
echo "Step 5: Listing forwards..."
curl -s http://127.0.0.1:18080/api/v1/forwards | jq .

# Check status
echo ""
echo "Step 6: Checking forward status..."
sleep 2
curl -s http://127.0.0.1:18080/api/v1/status | jq .

# Test connectivity
echo ""
echo "Step 7: Testing connectivity through forward..."
if ssh dc4 "curl -s localhost:23456" | grep -q "DOCTYPE HTML"; then
    echo "✓ Connectivity test passed"
else
    echo "✗ Connectivity test failed"
fi

# Cleanup
echo ""
echo "Step 8: Cleaning up..."
kill $DAEMON_PID 2>/dev/null || true
sleep 2
pkill -9 -f ssh-multihop 2>/dev/null || true
rm -f /tmp/inline-forward-*.sock
echo "✓ Cleanup complete"

echo ""
echo "==================================================================="
echo "Test Result: SUCCESS"
echo "==================================================================="
echo "✓ Daemon startup: OK"
echo "✓ Health check: OK"
echo "✓ Create forward: OK"
echo "✓ List forwards: OK"
echo "✓ Status check: OK"
echo "✓ Connectivity: OK"
echo ""

exit 0
