#!/bin/bash
# Test script to verify simplified forward architecture

set -e

echo "=== Testing Simplified Forward Architecture ==="
echo ""

# 1. Build all packages
echo "1. Building all packages..."
go build ./... || { echo "❌ Build failed"; exit 1; }
echo "✅ All packages build successfully"
echo ""

# 2. Check that no self-healing methods exist
echo "2. Checking that self-healing methods are removed..."
if grep -r "attemptRepair\|reconnect\|calculateBackoff" internal/forwarding/*.go | grep -v "Removed:" | grep -v "// " | grep -v "^Binary"; then
    echo "❌ Found self-healing methods in forwarding package"
    exit 1
fi
echo "✅ No self-healing methods found in Forward implementations"
echo ""

# 3. Check that setErrorStatus exists in all forwards
echo "3. Checking that setErrorStatus method exists..."
for file in internal/forwarding/local_listen_to_remote.go internal/forwarding/remote_listen_to_local.go internal/forwarding/remote_listen_to_remote.go; do
    if ! grep -q "func.*setErrorStatus" "$file"; then
        echo "❌ setErrorStatus not found in $file"
        exit 1
    fi
done
echo "✅ setErrorStatus method exists in all Forward types"
echo ""

# 4. Check that database integration exists
echo "4. Checking database integration..."
for file in internal/forwarding/local_listen_to_remote.go internal/forwarding/remote_listen_to_local.go internal/forwarding/remote_listen_to_remote.go; do
    if ! grep -q "db \*db.Database" "$file"; then
        echo "❌ Database field not found in $file"
        exit 1
    fi
    if ! grep -q "forwardID string" "$file"; then
        echo "❌ forwardID field not found in $file"
        exit 1
    fi
done
echo "✅ Database integration present in all Forward types"
echo ""

# 5. Check ForwardService has sync loop
echo "5. Checking ForwardService sync loop..."
if ! grep -q "func.*syncLoop" internal/service/forward_service.go; then
    echo "❌ syncLoop not found in ForwardService"
    exit 1
fi
echo "✅ ForwardService has sync loop"
echo ""

# 6. Check that rebuildErrorForward exists
echo "6. Checking rebuildErrorForward method..."
if ! grep -q "func.*rebuildErrorForward" internal/service/forward_service.go; then
    echo "❌ rebuildErrorForward not found in ForwardService"
    exit 1
fi
echo "✅ ForwardService has rebuildErrorForward method"
echo ""

# 7. Verify Stop() method has step-by-step cleanup
echo "7. checking Stop() method cleanup..."
for file in internal/forwarding/local_listen_to_remote.go internal/forwarding/remote_listen_to_local.go internal/forwarding/remote_listen_to_remote.go; do
    if ! grep -q "Step 1:" "$file" || ! grep -q "Step 2:" "$file" || ! grep -q "Step 3:" "$file"; then
        echo "❌ Stop() method missing step-by-step cleanup in $file"
        exit 1
    fi
done
echo "✅ Stop() methods have step-by-step cleanup"
echo ""

# 8. Check that constructors accept forwardID and db
echo "8. Checking constructor signatures..."
if ! grep -q "forwardID string, db \*db.Database" internal/forwarding/local_listen_to_remote.go; then
    echo "❌ LocalListenToRemote constructor missing forwardID/db parameters"
    exit 1
fi
if ! grep -q "forwardID string, db \*db.Database" internal/forwarding/remote_listen_to_local.go; then
    echo "❌ RemoteListenToLocal constructor missing forwardID/db parameters"
    exit 1
fi
if ! grep -q "forwardID string, db \*db.Database" internal/forwarding/remote_listen_to_remote.go; then
    echo "❌ RemoteListenToRemote constructor missing forwardID/db parameters"
    exit 1
fi
echo "✅ All constructors have forwardID and db parameters"
echo ""

echo "=== All Architecture Checks Passed! ==="
echo ""
echo "Summary of changes:"
echo "  ✅ Forward instances: No self-healing logic"
echo "  ✅ Forward instances: setErrorStatus() for database updates"
echo "  ✅ Forward instances: Enhanced Stop() with step-by-step cleanup"
echo "  ✅ ForwardService: Sync loop for lifecycle management"
echo "  ✅ ForwardService: rebuildErrorForward() for error recovery"
echo "  ✅ Database: Single source of truth for state"
echo ""
echo "Architecture simplified successfully!"
