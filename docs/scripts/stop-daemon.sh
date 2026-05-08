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
