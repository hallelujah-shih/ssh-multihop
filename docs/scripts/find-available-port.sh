#!/bin/bash
# Find an available port starting from a given port
# Usage: find-available-port.sh [start_port]
# Default start port: 8080

START_PORT=${1:-8080}
END_PORT=$((START_PORT + 1000))  # Try up to 1000 ports above start

for port in $(seq $START_PORT $END_PORT); do
    if ! lsof -i:$port > /dev/null 2>&1; then
        echo $port
        exit 0
    fi
done

echo "ERROR: No available port found in range $START_PORT-$END_PORT" >&2
exit 1
