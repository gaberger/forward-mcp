# Instance Lock Guide

## Overview

Forward MCP Server now includes an instance locking mechanism to prevent multiple server instances from running simultaneously. This ensures proper resource management and prevents conflicts when multiple processes attempt to access the same resources.

## How It Works

The instance lock uses a file-based locking mechanism:

1. **Lock File Creation**: When the server starts, it creates a lock file at `/tmp/forward-mcp.lock` (by default)
2. **PID Storage**: The lock file contains the Process ID (PID) of the running server
3. **Process Validation**: Before starting, the server checks if another instance is running by:
   - Reading the PID from the lock file
   - Verifying the process is still active
   - Removing stale lock files from terminated processes
4. **Automatic Cleanup**: The lock is automatically released when the server shuts down

## Configuration

### Custom Lock Directory

You can specify a custom directory for the lock file using the `FORWARD_LOCK_DIR` environment variable:

```bash
export FORWARD_LOCK_DIR=/var/run/forward-mcp
./forward-mcp
```

If not specified, the lock file will be created in `/tmp/`.

## Behavior

### Successful Start

When no other instance is running:
```
Forward MCP Server starting...
Acquiring instance lock at: /tmp/forward-mcp.lock
Instance lock acquired successfully
Environment initialized - API: https://api.forwardnetworks.com
...
```

### Blocked Start

When another instance is already running:
```
Forward MCP Server starting...
Acquiring instance lock at: /tmp/forward-mcp.lock
Failed to acquire instance lock: another instance is already running (PID: 12345)
Another instance may be running or starting up.
```

### Stale Lock Recovery

If a previous server process crashed or was killed without releasing the lock:
- The new server will detect the stale lock (process no longer running)
- The stale lock will be automatically removed
- The new server will start successfully

## Lock File Details

### Location
- Default: `/tmp/forward-mcp.lock`
- Configurable via `FORWARD_LOCK_DIR` environment variable

### Contents
The lock file contains a single line with the PID of the running process:
```
12345
```

### Permissions
- File permissions: `0600` (owner read/write only)
- Ensures only the file owner can read or modify the lock

## Error Handling

### Common Error Scenarios

1. **Multiple Instances**: "another instance is already running (PID: XXXX)"
   - **Solution**: Stop the existing instance before starting a new one

2. **Race Condition**: "recent lock file exists, possible race condition"
   - **Solution**: Wait a few seconds and try again
   - **Cause**: Two processes tried to start simultaneously

3. **Permission Denied**: "failed to create lock file"
   - **Solution**: Ensure write permissions on the lock directory
   - **Check**: Directory exists and is writable by the user

### Lock Acquisition Retry

The server will retry lock acquisition up to 3 times with 500ms delays between attempts. This handles transient issues during startup.

## Testing

The instance lock functionality includes comprehensive tests:

```bash
# Run instance lock tests
go test ./internal/instancelock/... -v

# Expected output:
# PASS: TestInstanceLock_BasicAcquisition
# PASS: TestInstanceLock_PreventDuplicate
# PASS: TestInstanceLock_StaleLockRemoval
# PASS: TestInstanceLock_AcquireWithRetry
# PASS: TestInstanceLock_MultipleRelease
# PASS: TestCheckRunningInstance
# PASS: TestInstanceLock_IsAcquired
```

## Manual Testing

### Test 1: Prevent Duplicate Instances

```bash
# Terminal 1
./forward-mcp
# Server starts successfully

# Terminal 2
./forward-mcp
# Server exits with error: another instance is already running
```

### Test 2: Automatic Stale Lock Removal

```bash
# Start server
./forward-mcp &
SERVER_PID=$!

# Kill server abruptly (simulating crash)
kill -9 $SERVER_PID

# Lock file still exists but server is gone
ls -l /tmp/forward-mcp.lock

# Start new server - should succeed by removing stale lock
./forward-mcp
# Server starts successfully
```

### Test 3: Custom Lock Directory

```bash
# Create custom lock directory
mkdir -p /var/run/forward-mcp
export FORWARD_LOCK_DIR=/var/run/forward-mcp

# Start server
./forward-mcp

# Verify lock location
ls -l /var/run/forward-mcp/forward-mcp.lock
```

## Implementation Details

### Code Structure

```
internal/instancelock/
├── instancelock.go       # Main implementation
└── instancelock_test.go  # Comprehensive tests

cmd/server/main.go         # Integration in server startup
```

### Key Functions

- `NewInstanceLock(lockDir)`: Create a new lock manager
- `TryAcquire()`: Attempt to acquire lock once
- `Acquire(retries, delay)`: Acquire lock with retry logic
- `Release()`: Release the lock
- `CheckRunningInstance(lockDir)`: Check if instance is running

## Best Practices

1. **Always Use Environment Variables**: Configure lock directory via environment variables, not hardcoded paths
2. **Monitor Lock Files**: In production, monitor for stale lock files that might indicate server crashes
3. **Graceful Shutdown**: Use SIGTERM for clean shutdown to ensure proper lock release
4. **Log Analysis**: Check logs for "Instance lock acquired" and "Instance lock released" messages

## Troubleshooting

### Problem: Server won't start even after killing previous instance

**Solution:**
```bash
# Check for running processes
ps aux | grep forward-mcp

# If no process found, manually remove lock file
rm /tmp/forward-mcp.lock

# Start server again
./forward-mcp
```

### Problem: Lock file in wrong location

**Solution:**
```bash
# Check current lock directory
echo $FORWARD_LOCK_DIR

# Set correct directory
export FORWARD_LOCK_DIR=/your/preferred/path

# Restart server
./forward-mcp
```

### Problem: Permission denied creating lock file

**Solution:**
```bash
# Check directory permissions
ls -ld /tmp

# Make directory writable (if needed)
chmod 1777 /tmp

# Or use a different directory
export FORWARD_LOCK_DIR=$HOME/.forward-mcp
mkdir -p $HOME/.forward-mcp
./forward-mcp
```

## Platform Compatibility

The instance lock mechanism is compatible with:
- Linux (all distributions)
- macOS
- Unix-like systems

**Note**: Windows compatibility requires separate implementation using different locking primitives.

## Security Considerations

1. **Lock File Permissions**: The lock file is created with 0600 permissions (owner only)
2. **PID Validation**: The system validates PIDs to prevent spoofing
3. **Atomic Operations**: Lock file creation uses exclusive flags to prevent race conditions
4. **No Sensitive Data**: Lock file contains only the PID, no credentials or configuration

## Future Enhancements

Potential improvements under consideration:
- Named locks for multiple server configurations
- Network-based distributed locking
- Lock file health monitoring daemon
- Automatic cleanup of orphaned lock files
