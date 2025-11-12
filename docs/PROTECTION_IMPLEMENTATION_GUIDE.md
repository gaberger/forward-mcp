# Protection Mechanism Implementation Guide

**Document Version**: 1.0
**Date**: 2025-10-17
**Companion to**: PROTECTION_ARCHITECTURE.md

---

## Overview

This document provides detailed implementation guidance for the multi-server protection mechanism, including pseudocode, platform-specific implementations, and comprehensive testing strategies.

---

## Table of Contents

1. [Implementation Roadmap](#implementation-roadmap)
2. [Platform-Specific Implementations](#platform-specific-implementations)
3. [Enhanced Features](#enhanced-features)
4. [Testing Strategy](#testing-strategy)
5. [Deployment Guide](#deployment-guide)
6. [Troubleshooting Playbook](#troubleshooting-playbook)

---

## Implementation Roadmap

### Phase 1: Windows Support (High Priority)

#### Step 1.1: Create Platform-Specific Files

```bash
# Create Windows-specific implementation
touch internal/instancelock/instancelock_windows.go
touch internal/instancelock/instancelock_unix.go

# Move existing code to Unix-specific file
# Keep platform-agnostic code in instancelock.go
```

#### Step 1.2: Refactor Core Logic

**File: internal/instancelock/instancelock.go** (Platform-agnostic)

```go
package instancelock

import (
    "fmt"
    "os"
    "path/filepath"
    "strconv"
    "time"
)

const LockFileName = "forward-mcp.lock"

// InstanceLock manages server instance locking
type InstanceLock struct {
    lockFilePath string
    lockFile     *os.File
    acquired     bool
}

// NewInstanceLock creates a new instance lock manager
func NewInstanceLock(lockDir string) *InstanceLock {
    if lockDir == "" {
        lockDir = DefaultLockDir  // Platform-specific constant
    }
    return &InstanceLock{
        lockFilePath: filepath.Join(lockDir, LockFileName),
        acquired:     false,
    }
}

// TryAcquire attempts to acquire the instance lock
func (il *InstanceLock) TryAcquire() (bool, error) {
    // Check if lock file exists and validate
    if info, err := os.Stat(il.lockFilePath); err == nil {
        // Lock file exists, check if process is still running
        data, readErr := os.ReadFile(il.lockFilePath)
        if readErr == nil {
            // Try to parse PID from lock file
            if pid, parseErr := strconv.Atoi(string(data)); parseErr == nil {
                // Platform-specific process check
                if isProcessRunning(pid) {
                    // Process is still running
                    return false, fmt.Errorf("another instance is already running (PID: %d)", pid)
                }
            }
        }

        // Stale lock file, remove it
        os.Remove(il.lockFilePath)

        // Safety check: file modification time
        if time.Since(info.ModTime()) < 5*time.Minute {
            // Lock file is recent, be cautious
            return false, fmt.Errorf("recent lock file exists, possible race condition")
        }
    }

    // Try to create the lock file exclusively
    file, err := os.OpenFile(il.lockFilePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
    if err != nil {
        if os.IsExist(err) {
            return false, fmt.Errorf("lock file exists, another instance may be starting")
        }
        return false, fmt.Errorf("failed to create lock file: %w", err)
    }

    // Write current process PID to lock file
    pid := os.Getpid()
    if _, err := file.WriteString(fmt.Sprintf("%d", pid)); err != nil {
        file.Close()
        os.Remove(il.lockFilePath)
        return false, fmt.Errorf("failed to write PID to lock file: %w", err)
    }

    // Ensure data is written to disk
    if err := file.Sync(); err != nil {
        file.Close()
        os.Remove(il.lockFilePath)
        return false, fmt.Errorf("failed to sync lock file: %w", err)
    }

    il.lockFile = file
    il.acquired = true
    return true, nil
}

// Acquire attempts to acquire the lock with retry logic
func (il *InstanceLock) Acquire(maxRetries int, retryDelay time.Duration) error {
    for i := 0; i < maxRetries; i++ {
        acquired, err := il.TryAcquire()
        if acquired {
            return nil
        }

        if i < maxRetries-1 {
            time.Sleep(retryDelay)
        } else {
            return err
        }
    }
    return fmt.Errorf("failed to acquire lock after %d retries", maxRetries)
}

// Release releases the instance lock
func (il *InstanceLock) Release() error {
    if !il.acquired {
        return nil
    }

    var errs []error

    // Close the file
    if il.lockFile != nil {
        if err := il.lockFile.Close(); err != nil {
            errs = append(errs, fmt.Errorf("failed to close lock file: %w", err))
        }
    }

    // Remove the lock file
    if err := os.Remove(il.lockFilePath); err != nil && !os.IsNotExist(err) {
        errs = append(errs, fmt.Errorf("failed to remove lock file: %w", err))
    }

    il.acquired = false

    if len(errs) > 0 {
        return fmt.Errorf("errors during lock release: %v", errs)
    }
    return nil
}

// IsAcquired returns whether the lock is currently acquired
func (il *InstanceLock) IsAcquired() bool {
    return il.acquired
}

// GetLockFilePath returns the path to the lock file
func (il *InstanceLock) GetLockFilePath() string {
    return il.lockFilePath
}

// CheckRunningInstance checks if another instance is running without acquiring lock
func CheckRunningInstance(lockDir string) (bool, int, error) {
    if lockDir == "" {
        lockDir = DefaultLockDir
    }
    lockFilePath := filepath.Join(lockDir, LockFileName)

    // Check if lock file exists
    if _, err := os.Stat(lockFilePath); os.IsNotExist(err) {
        return false, 0, nil
    }

    // Read PID from lock file
    data, err := os.ReadFile(lockFilePath)
    if err != nil {
        return false, 0, fmt.Errorf("failed to read lock file: %w", err)
    }

    // Parse PID
    pid, err := strconv.Atoi(string(data))
    if err != nil {
        return false, 0, fmt.Errorf("invalid PID in lock file: %w", err)
    }

    // Check if process is running (platform-specific)
    if !isProcessRunning(pid) {
        return false, 0, nil
    }

    return true, pid, nil
}
```

#### Step 1.3: Unix-Specific Implementation

**File: internal/instancelock/instancelock_unix.go**

```go
//go:build !windows
// +build !windows

package instancelock

import (
    "os"
    "syscall"
)

const DefaultLockDir = "/tmp"

// isProcessRunning checks if a process with the given PID is running (Unix)
func isProcessRunning(pid int) bool {
    process, err := os.FindProcess(pid)
    if err != nil {
        return false
    }

    // On Unix, FindProcess always succeeds, so we need to send signal 0
    // to check if process actually exists
    err = process.Signal(syscall.Signal(0))
    return err == nil
}
```

#### Step 1.4: Windows-Specific Implementation

**File: internal/instancelock/instancelock_windows.go**

```go
//go:build windows
// +build windows

package instancelock

import (
    "syscall"
    "unsafe"
)

const DefaultLockDir = "C:\\ProgramData\\forward-mcp"

const (
    PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
    STILL_ACTIVE                       = 259
)

// isProcessRunning checks if a process with the given PID is running (Windows)
func isProcessRunning(pid int) bool {
    // Open process handle with query access
    handle, err := syscall.OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
    if err != nil {
        return false
    }
    defer syscall.CloseHandle(handle)

    // Get exit code
    var exitCode uint32
    err = syscall.GetExitCodeProcess(handle, &exitCode)
    if err != nil {
        return false
    }

    // If exit code is STILL_ACTIVE, process is running
    return exitCode == STILL_ACTIVE
}
```

#### Step 1.5: Windows-Specific Tests

**File: internal/instancelock/instancelock_windows_test.go**

```go
//go:build windows
// +build windows

package instancelock

import (
    "os"
    "testing"
)

func TestWindowsDefaultLockDir(t *testing.T) {
    if DefaultLockDir != "C:\\ProgramData\\forward-mcp" {
        t.Fatalf("Expected Windows default lock dir, got %s", DefaultLockDir)
    }
}

func TestWindowsProcessCheck(t *testing.T) {
    // Test with current process (should exist)
    if !isProcessRunning(os.Getpid()) {
        t.Fatal("Current process should be running")
    }

    // Test with non-existent PID
    if isProcessRunning(999999) {
        t.Fatal("Non-existent process should not be running")
    }
}

func TestWindowsLockDirectory(t *testing.T) {
    // Test lock directory creation
    lockDir := "C:\\Temp\\forward-mcp-test"
    os.MkdirAll(lockDir, 0755)
    defer os.RemoveAll(lockDir)

    lock := NewInstanceLock(lockDir)
    acquired, err := lock.TryAcquire()
    if err != nil {
        t.Fatalf("Failed to acquire lock: %v", err)
    }
    if !acquired {
        t.Fatal("Should have acquired lock")
    }

    lock.Release()
}
```

---

### Phase 2: Enhanced Error Handling

#### Step 2.1: Define Error Types

**File: internal/instancelock/errors.go**

```go
package instancelock

import (
    "fmt"
    "strings"
)

// ErrorType represents the type of lock error
type ErrorType string

const (
    ErrInstanceRunning    ErrorType = "INSTANCE_RUNNING"
    ErrLockFileExists     ErrorType = "LOCK_FILE_EXISTS"
    ErrPermissionDenied   ErrorType = "PERMISSION_DENIED"
    ErrInvalidPID         ErrorType = "INVALID_PID"
    ErrStaleLock          ErrorType = "STALE_LOCK"
    ErrRaceCondition      ErrorType = "RACE_CONDITION"
    ErrMaxRetriesExceeded ErrorType = "MAX_RETRIES_EXCEEDED"
    ErrReleaseFailed      ErrorType = "RELEASE_FAILED"
)

// LockError provides detailed error information
type LockError struct {
    Type     ErrorType
    PID      int
    LockPath string
    Message  string
    Help     []string
    Cause    error
}

func (e *LockError) Error() string {
    var sb strings.Builder

    // Main error message
    sb.WriteString(fmt.Sprintf("%s: %s", e.Type, e.Message))

    // Include PID if relevant
    if e.PID > 0 {
        sb.WriteString(fmt.Sprintf(" (PID: %d)", e.PID))
    }

    // Include lock path
    if e.LockPath != "" {
        sb.WriteString(fmt.Sprintf("\nLock file: %s", e.LockPath))
    }

    // Include help text
    if len(e.Help) > 0 {
        sb.WriteString("\n\nPossible solutions:")
        for i, help := range e.Help {
            sb.WriteString(fmt.Sprintf("\n  %d. %s", i+1, help))
        }
    }

    // Include underlying cause
    if e.Cause != nil {
        sb.WriteString(fmt.Sprintf("\n\nUnderlying error: %v", e.Cause))
    }

    return sb.String()
}

func (e *LockError) Unwrap() error {
    return e.Cause
}

// Helper functions to create specific errors

func errInstanceRunning(pid int, lockPath string) error {
    return &LockError{
        Type:     ErrInstanceRunning,
        PID:      pid,
        LockPath: lockPath,
        Message:  "Another instance is already running",
        Help: []string{
            fmt.Sprintf("Stop the existing instance: kill %d", pid),
            "Or use a custom lock directory: export FORWARD_LOCK_DIR=/custom/path",
            fmt.Sprintf("Or remove the lock file manually (if instance is actually stopped): rm %s", lockPath),
        },
    }
}

func errPermissionDenied(lockPath string, cause error) error {
    return &LockError{
        Type:     ErrPermissionDenied,
        LockPath: lockPath,
        Message:  "Permission denied when creating lock file",
        Help: []string{
            "Check directory permissions: ls -ld " + filepath.Dir(lockPath),
            "Ensure you have write access to the lock directory",
            "Use a directory you own: export FORWARD_LOCK_DIR=$HOME/.forward-mcp",
        },
        Cause: cause,
    }
}

func errRaceCondition(lockPath string) error {
    return &LockError{
        Type:     ErrRaceCondition,
        LockPath: lockPath,
        Message:  "Recent lock file exists, possible race condition",
        Help: []string{
            "Wait a few seconds and try again",
            "Check if another instance is starting: ps aux | grep forward-mcp",
            "If no instance is running, remove the lock file: rm " + lockPath,
        },
    }
}

func errMaxRetriesExceeded(retries int, lastError error) error {
    return &LockError{
        Type:    ErrMaxRetriesExceeded,
        Message: fmt.Sprintf("Failed to acquire lock after %d retries", retries),
        Help: []string{
            "Check system logs for errors",
            "Verify filesystem is healthy",
            "Ensure lock directory exists and is writable",
        },
        Cause: lastError,
    }
}
```

#### Step 2.2: Update TryAcquire to Use Enhanced Errors

```go
func (il *InstanceLock) TryAcquire() (bool, error) {
    // Check if lock file exists
    if info, err := os.Stat(il.lockFilePath); err == nil {
        data, readErr := os.ReadFile(il.lockFilePath)
        if readErr == nil {
            if pid, parseErr := strconv.Atoi(string(data)); parseErr == nil {
                if isProcessRunning(pid) {
                    return false, errInstanceRunning(pid, il.lockFilePath)
                }
            }
        }

        os.Remove(il.lockFilePath)

        if time.Since(info.ModTime()) < 5*time.Minute {
            return false, errRaceCondition(il.lockFilePath)
        }
    }

    // Try to create the lock file exclusively
    file, err := os.OpenFile(il.lockFilePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
    if err != nil {
        if os.IsPermission(err) {
            return false, errPermissionDenied(il.lockFilePath, err)
        }
        if os.IsExist(err) {
            return false, &LockError{
                Type:     ErrLockFileExists,
                LockPath: il.lockFilePath,
                Message:  "Lock file exists, another instance may be starting",
            }
        }
        return false, fmt.Errorf("failed to create lock file: %w", err)
    }

    // ... rest of the implementation ...
}
```

---

### Phase 3: Optional Heartbeat Mechanism

#### Step 3.1: Add Heartbeat Configuration

**File: internal/instancelock/config.go**

```go
package instancelock

import (
    "os"
    "strconv"
    "time"
)

// Config holds instance lock configuration
type Config struct {
    LockDir           string
    HeartbeatEnabled  bool
    HeartbeatInterval time.Duration
    StaleTimeout      time.Duration
}

// DefaultConfig returns default configuration
func DefaultConfig() *Config {
    return &Config{
        LockDir:           getEnvOrDefault("FORWARD_LOCK_DIR", DefaultLockDir),
        HeartbeatEnabled:  getEnvBool("FORWARD_LOCK_HEARTBEAT_ENABLED", false),
        HeartbeatInterval: getEnvDuration("FORWARD_LOCK_HEARTBEAT_INTERVAL", 30*time.Second),
        StaleTimeout:      getEnvDuration("FORWARD_LOCK_STALE_TIMEOUT", 60*time.Second),
    }
}

func getEnvOrDefault(key, defaultValue string) string {
    if value := os.Getenv(key); value != "" {
        return value
    }
    return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
    if value := os.Getenv(key); value != "" {
        b, err := strconv.ParseBool(value)
        if err == nil {
            return b
        }
    }
    return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
    if value := os.Getenv(key); value != "" {
        d, err := time.ParseDuration(value)
        if err == nil {
            return d
        }
    }
    return defaultValue
}
```

#### Step 3.2: Implement Heartbeat

**File: internal/instancelock/heartbeat.go**

```go
package instancelock

import (
    "os"
    "time"
)

// startHeartbeat starts the heartbeat goroutine
func (il *InstanceLock) startHeartbeat(interval time.Duration) {
    if il.stopHeartbeat != nil {
        return // Already running
    }

    il.stopHeartbeat = make(chan struct{})
    ticker := time.NewTicker(interval)

    go func() {
        defer ticker.Stop()

        for {
            select {
            case <-ticker.C:
                // Update lock file modification time
                now := time.Now()
                if err := os.Chtimes(il.lockFilePath, now, now); err != nil {
                    // Log error but continue
                    // In production, use proper logger
                }

            case <-il.stopHeartbeat:
                return
            }
        }
    }()
}

// stopHeartbeat stops the heartbeat goroutine
func (il *InstanceLock) stopHeartbeatFunc() {
    if il.stopHeartbeat != nil {
        close(il.stopHeartbeat)
        il.stopHeartbeat = nil
    }
}

// isLockStale checks if a lock file is stale based on heartbeat
func isLockStale(lockPath string, timeout time.Duration) bool {
    info, err := os.Stat(lockPath)
    if err != nil {
        return true // If we can't stat it, consider it stale
    }

    // Check if modification time is older than timeout
    return time.Since(info.ModTime()) > timeout
}
```

#### Step 3.3: Update InstanceLock Structure

```go
type InstanceLock struct {
    lockFilePath  string
    lockFile      *os.File
    acquired      bool
    config        *Config
    stopHeartbeat chan struct{}
}

func NewInstanceLockWithConfig(config *Config) *InstanceLock {
    return &InstanceLock{
        lockFilePath: filepath.Join(config.LockDir, LockFileName),
        acquired:     false,
        config:       config,
    }
}

func (il *InstanceLock) TryAcquire() (bool, error) {
    // ... existing logic ...

    // Check for stale lock with heartbeat
    if il.config != nil && il.config.HeartbeatEnabled {
        if isLockStale(il.lockFilePath, il.config.StaleTimeout) {
            os.Remove(il.lockFilePath)
            // Continue with acquisition
        }
    }

    // ... rest of acquisition logic ...

    // Start heartbeat if enabled
    if il.config != nil && il.config.HeartbeatEnabled {
        il.startHeartbeat(il.config.HeartbeatInterval)
    }

    return true, nil
}

func (il *InstanceLock) Release() error {
    // Stop heartbeat
    il.stopHeartbeatFunc()

    // ... existing release logic ...
}
```

---

## Platform-Specific Implementations

### Linux-Specific Enhancements

#### Process Name Validation

**File: internal/instancelock/instancelock_linux.go**

```go
//go:build linux
// +build linux

package instancelock

import (
    "os"
    "strings"
)

// getProcessName returns the process name for a given PID
func getProcessName(pid int) (string, error) {
    comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
    if err != nil {
        return "", err
    }
    return strings.TrimSpace(string(comm)), nil
}

// isProcessRunningEnhanced checks both existence and process name
func isProcessRunningEnhanced(pid int) bool {
    if !isProcessRunning(pid) {
        return false
    }

    // Additional check: verify process name
    name, err := getProcessName(pid)
    if err != nil {
        return false
    }

    // Check if it's actually forward-mcp
    // Handle both "forward-mcp" and "forward-mcp-server"
    return strings.HasPrefix(name, "forward-mcp")
}
```

### macOS-Specific Enhancements

#### Application Support Directory

**File: internal/instancelock/instancelock_darwin.go**

```go
//go:build darwin
// +build darwin

package instancelock

import (
    "os"
    "path/filepath"
)

// getDefaultLockDir returns macOS-specific default lock directory
func getDefaultLockDir() string {
    // Check for user-specific location first
    if home := os.Getenv("HOME"); home != "" {
        appSupport := filepath.Join(home, "Library", "Application Support", "forward-mcp")
        if err := os.MkdirAll(appSupport, 0755); err == nil {
            return appSupport
        }
    }

    // Fall back to /tmp
    return "/tmp"
}

// Override DefaultLockDir
const DefaultLockDir = "/tmp" // Compile-time constant
```

---

## Testing Strategy

### Unit Tests

#### Test Matrix

```go
// File: internal/instancelock/instancelock_test_matrix.go

package instancelock

import (
    "runtime"
    "testing"
)

// TestMatrix runs tests across different configurations
func TestMatrix(t *testing.T) {
    testCases := []struct {
        name   string
        config *Config
    }{
        {
            name:   "Default config",
            config: DefaultConfig(),
        },
        {
            name: "Heartbeat enabled",
            config: &Config{
                LockDir:           t.TempDir(),
                HeartbeatEnabled:  true,
                HeartbeatInterval: 100 * time.Millisecond,
                StaleTimeout:      200 * time.Millisecond,
            },
        },
        {
            name: "Heartbeat disabled",
            config: &Config{
                LockDir:          t.TempDir(),
                HeartbeatEnabled: false,
            },
        },
    }

    for _, tc := range testCases {
        t.Run(tc.name, func(t *testing.T) {
            testBasicAcquisition(t, tc.config)
            testDuplicatePrevention(t, tc.config)
            testStaleLockRemoval(t, tc.config)
        })
    }
}

func testBasicAcquisition(t *testing.T, config *Config) {
    lock := NewInstanceLockWithConfig(config)
    acquired, err := lock.TryAcquire()
    if err != nil {
        t.Fatalf("Failed to acquire lock: %v", err)
    }
    if !acquired {
        t.Fatal("Expected to acquire lock")
    }
    defer lock.Release()
}

func testDuplicatePrevention(t *testing.T, config *Config) {
    lock1 := NewInstanceLockWithConfig(config)
    acquired, err := lock1.TryAcquire()
    if err != nil {
        t.Fatalf("Failed to acquire first lock: %v", err)
    }
    if !acquired {
        t.Fatal("Expected to acquire first lock")
    }
    defer lock1.Release()

    lock2 := NewInstanceLockWithConfig(config)
    acquired, err = lock2.TryAcquire()
    if err == nil {
        t.Fatal("Expected error when trying to acquire second lock")
    }
    if acquired {
        t.Fatal("Should not have acquired second lock")
    }
}

func testStaleLockRemoval(t *testing.T, config *Config) {
    // Implementation similar to existing test
}
```

### Concurrency Tests

```go
// File: internal/instancelock/instancelock_concurrent_test.go

package instancelock

import (
    "sync"
    "testing"
    "time"
)

func TestConcurrentAcquisition(t *testing.T) {
    tmpDir := t.TempDir()
    const goroutines = 50

    acquired := make(chan int, goroutines)
    var wg sync.WaitGroup

    // Start many goroutines trying to acquire lock
    for i := 0; i < goroutines; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()

            lock := NewInstanceLock(tmpDir)
            if ok, _ := lock.TryAcquire(); ok {
                acquired <- id
                time.Sleep(50 * time.Millisecond) // Hold lock briefly
                lock.Release()
            }
        }(i)
    }

    wg.Wait()
    close(acquired)

    // Verify only one goroutine acquired the lock
    var winners []int
    for winner := range acquired {
        winners = append(winners, winner)
    }

    if len(winners) != 1 {
        t.Fatalf("Expected 1 acquisition, got %d: %v", len(winners), winners)
    }
}

func TestRapidAcquireRelease(t *testing.T) {
    tmpDir := t.TempDir()
    iterations := 100

    for i := 0; i < iterations; i++ {
        lock := NewInstanceLock(tmpDir)

        acquired, err := lock.TryAcquire()
        if err != nil {
            t.Fatalf("Iteration %d: failed to acquire: %v", i, err)
        }
        if !acquired {
            t.Fatalf("Iteration %d: expected to acquire lock", i)
        }

        if err := lock.Release(); err != nil {
            t.Fatalf("Iteration %d: failed to release: %v", i, err)
        }

        // Small delay to allow filesystem sync
        time.Sleep(1 * time.Millisecond)
    }
}

func TestRaceConditionSimulation(t *testing.T) {
    tmpDir := t.TempDir()
    const goroutines = 10

    var wg sync.WaitGroup
    start := make(chan struct{})

    for i := 0; i < goroutines; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()

            <-start // Wait for start signal

            lock := NewInstanceLock(tmpDir)
            lock.TryAcquire()
            time.Sleep(10 * time.Millisecond)
            lock.Release()
        }(i)
    }

    close(start) // Start all goroutines simultaneously
    wg.Wait()

    // Verify no lock file remains
    lockPath := filepath.Join(tmpDir, LockFileName)
    if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
        t.Fatal("Lock file should not exist after all releases")
    }
}
```

### Integration Tests

```go
// File: internal/instancelock/instancelock_integration_test.go

package instancelock

import (
    "os"
    "os/exec"
    "testing"
    "time"
)

func TestServerIntegration(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping integration test in short mode")
    }

    // Build test server binary
    cmd := exec.Command("go", "build", "-o", "testserver", "../../cmd/server")
    if err := cmd.Run(); err != nil {
        t.Fatalf("Failed to build server: %v", err)
    }
    defer os.Remove("testserver")

    // Start first server
    server1 := exec.Command("./testserver")
    if err := server1.Start(); err != nil {
        t.Fatalf("Failed to start server 1: %v", err)
    }
    defer server1.Process.Kill()

    // Wait for server to start
    time.Sleep(500 * time.Millisecond)

    // Try to start second server (should fail)
    server2 := exec.Command("./testserver")
    output, err := server2.CombinedOutput()
    if err == nil {
        t.Fatal("Second server should have failed to start")
    }

    if !strings.Contains(string(output), "already running") {
        t.Fatalf("Expected 'already running' error, got: %s", output)
    }

    // Stop first server
    if err := server1.Process.Signal(os.Interrupt); err != nil {
        t.Fatalf("Failed to stop server 1: %v", err)
    }
    server1.Wait()

    // Wait for cleanup
    time.Sleep(500 * time.Millisecond)

    // Try to start third server (should succeed)
    server3 := exec.Command("./testserver")
    if err := server3.Start(); err != nil {
        t.Fatalf("Failed to start server 3: %v", err)
    }
    defer server3.Process.Kill()

    time.Sleep(500 * time.Millisecond)
    server3.Process.Signal(os.Interrupt)
    server3.Wait()
}
```

### Benchmark Tests

```go
// File: internal/instancelock/instancelock_bench_test.go

package instancelock

import (
    "testing"
)

func BenchmarkLockAcquisition(b *testing.B) {
    tmpDir := b.TempDir()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        lock := NewInstanceLock(tmpDir)
        lock.TryAcquire()
        lock.Release()
    }
}

func BenchmarkLockAcquisitionParallel(b *testing.B) {
    tmpDir := b.TempDir()

    b.RunParallel(func(pb *testing.PB) {
        for pb.Next() {
            lock := NewInstanceLock(tmpDir)
            lock.TryAcquire()
            lock.Release()
        }
    })
}

func BenchmarkProcessCheck(b *testing.B) {
    pid := os.Getpid()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        isProcessRunning(pid)
    }
}

func BenchmarkHeartbeat(b *testing.B) {
    tmpDir := b.TempDir()
    config := &Config{
        LockDir:           tmpDir,
        HeartbeatEnabled:  true,
        HeartbeatInterval: 100 * time.Millisecond,
    }

    lock := NewInstanceLockWithConfig(config)
    lock.TryAcquire()
    defer lock.Release()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        time.Sleep(100 * time.Millisecond)
    }
}
```

---

## Deployment Guide

### Production Deployment Checklist

```bash
# 1. Create lock directory with proper permissions
sudo mkdir -p /var/run/forward-mcp
sudo chown forward-mcp:forward-mcp /var/run/forward-mcp
sudo chmod 755 /var/run/forward-mcp

# 2. Configure environment
cat >> /etc/forward-mcp/environment <<EOF
FORWARD_LOCK_DIR=/var/run/forward-mcp
FORWARD_LOCK_HEARTBEAT_ENABLED=true
FORWARD_LOCK_HEARTBEAT_INTERVAL=30s
FORWARD_LOCK_STALE_TIMEOUT=60s
EOF

# 3. Create systemd service
cat > /etc/systemd/system/forward-mcp.service <<EOF
[Unit]
Description=Forward MCP Server
After=network.target

[Service]
Type=simple
User=forward-mcp
Group=forward-mcp
EnvironmentFile=/etc/forward-mcp/environment
ExecStart=/usr/local/bin/forward-mcp
Restart=on-failure
RestartSec=5s
KillMode=mixed
KillSignal=SIGTERM
TimeoutStopSec=30s

[Install]
WantedBy=multi-user.target
EOF

# 4. Enable and start service
sudo systemctl daemon-reload
sudo systemctl enable forward-mcp
sudo systemctl start forward-mcp

# 5. Verify lock file
ls -l /var/run/forward-mcp/forward-mcp.lock

# 6. Test duplicate prevention
sudo systemctl start forward-mcp  # Should fail
```

### Container Deployment

**Dockerfile**:
```dockerfile
FROM golang:1.21 AS builder

WORKDIR /app
COPY . .
RUN go build -o forward-mcp ./cmd/server

FROM debian:bookworm-slim

RUN groupadd -r forward && useradd -r -g forward forward
RUN mkdir -p /var/run/forward-mcp && chown forward:forward /var/run/forward-mcp

COPY --from=builder /app/forward-mcp /usr/local/bin/
USER forward

ENV FORWARD_LOCK_DIR=/var/run/forward-mcp

CMD ["/usr/local/bin/forward-mcp"]
```

**docker-compose.yml**:
```yaml
version: '3.8'

services:
  forward-mcp:
    build: .
    volumes:
      - lock-data:/var/run/forward-mcp
    environment:
      - FORWARD_LOCK_DIR=/var/run/forward-mcp
      - FORWARD_LOCK_HEARTBEAT_ENABLED=true
    deploy:
      replicas: 1  # Ensure single instance
      restart_policy:
        condition: on-failure

volumes:
  lock-data:
```

### Kubernetes Deployment

**deployment.yaml**:
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: forward-mcp
spec:
  replicas: 1  # Single replica
  selector:
    matchLabels:
      app: forward-mcp
  template:
    metadata:
      labels:
        app: forward-mcp
    spec:
      containers:
      - name: forward-mcp
        image: forward-mcp:latest
        env:
        - name: FORWARD_LOCK_DIR
          value: /var/run/forward-mcp
        - name: FORWARD_LOCK_HEARTBEAT_ENABLED
          value: "true"
        volumeMounts:
        - name: lock-data
          mountPath: /var/run/forward-mcp
      volumes:
      - name: lock-data
        emptyDir: {}
```

---

## Troubleshooting Playbook

### Issue 1: Lock Acquisition Fails

**Symptoms**:
```
Failed to acquire instance lock: another instance is already running (PID: 12345)
```

**Diagnosis**:
```bash
# Check if process is actually running
ps -p 12345

# Check lock file
cat /tmp/forward-mcp.lock

# Check lock file age
stat /tmp/forward-mcp.lock
```

**Solutions**:
1. **If process is running**: Stop it first
   ```bash
   kill 12345
   # Wait for graceful shutdown
   sleep 2
   # Restart
   ./forward-mcp
   ```

2. **If process is not running**: Remove stale lock
   ```bash
   rm /tmp/forward-mcp.lock
   ./forward-mcp
   ```

### Issue 2: Permission Denied

**Symptoms**:
```
Failed to acquire instance lock: Permission denied
```

**Diagnosis**:
```bash
# Check directory permissions
ls -ld /tmp

# Check SELinux context
ls -Z /tmp

# Try to create test file
touch /tmp/test-lock
```

**Solutions**:
1. **Fix permissions**:
   ```bash
   sudo chmod 1777 /tmp
   ```

2. **Use custom directory**:
   ```bash
   mkdir -p $HOME/.forward-mcp
   export FORWARD_LOCK_DIR=$HOME/.forward-mcp
   ./forward-mcp
   ```

3. **SELinux**:
   ```bash
   sudo semanage fcontext -a -t tmp_t "/var/run/forward-mcp(/.*)?"
   sudo restorecon -Rv /var/run/forward-mcp
   ```

### Issue 3: Lock Not Released on Crash

**Symptoms**:
Lock file remains after server crash

**Diagnosis**:
```bash
# Check lock file age
stat -f %m /tmp/forward-mcp.lock

# Check PID in lock
PID=$(cat /tmp/forward-mcp.lock)
ps -p $PID
```

**Solutions**:
1. **Automatic cleanup** (next start will remove it)
   ```bash
   ./forward-mcp  # Should succeed
   ```

2. **Manual cleanup**:
   ```bash
   rm /tmp/forward-mcp.lock
   ./forward-mcp
   ```

---

**Document End**

*This implementation guide provides detailed code examples, testing strategies, and deployment patterns for the multi-server protection mechanism. Use in conjunction with PROTECTION_ARCHITECTURE.md for complete understanding.*
