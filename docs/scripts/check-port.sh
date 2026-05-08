#!/bin/bash
# Check what process is using a port
# Usage: check-port.sh [port]
# Default port: 8080

PORT=${1:-8080}

echo "Checking what's using port $PORT..."

# Check if anything is using the port
PORT_USER=$(lsof -ti:$PORT 2>/dev/null)

if [ -z "$PORT_USER" ]; then
    echo "✓ Port $PORT is available"
    exit 0
fi

echo "⚠ Port $PORT is occupied by:"
lsof -i:$PORT

# Check if it's ssh-multihop
if ps -p $PORT_USER -o command= 2>/dev/null | grep -q "ssh-multihop"; then
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
