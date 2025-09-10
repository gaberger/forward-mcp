# Log Issues and Fixes

## Issues Identified

### 1. NQE Query Error: Invalid Filter/Sort Column "deviceName"

**Problem:**
```
{"timestamp":"2025-07-21T17:48:55-04:00","level":"error","message":"Tool run_nqe_query_by_id failed (duration: 0s): unexpected status code: 400, response: {\"apiUrl\":\"/api/nqe?networkId=162112\",\"httpMethod\":\"POST\",\"message\":\"Invalid filter and sort columns: deviceName.\"}"}
```

**Root Cause:**
The example queries in `examples/test_mcp.go` were using `deviceName` as a column alias, but the Forward Networks API expects proper column names that match the actual data structure.

**Fix Applied:**
Updated example queries to use proper column names:
- `deviceName` → `Name` or `DeviceName`
- `platform` → `Platform`
- `interfaceName` → `InterfaceName`
- `ipAddress` → `IPAddress`
- `prefix` → `Prefix`
- `nextHop` → `NextHop`
- `metric` → `Metric`

**Files Modified:**
- `examples/test_mcp.go`

### 2. Device Resolution Issue: Multiple Devices Resolving to Same IP

**Problem:**
```
{"timestamp":"2025-07-21T17:50:12-04:00","level":"info","message":"Using management IP for device atl-core-pe01: 147.75.201.115"}
{"timestamp":"2025-07-21T17:50:12-04:00","level":"info","message":"Using management IP for device atl-core-pe02: 147.75.201.115"}
{"timestamp":"2025-07-21T17:50:12-04:00","level":"info","message":"Using management IP for device atl-ce01: 147.75.201.115"}
```

Multiple devices (`atl-core-pe01`, `atl-core-pe02`, `atl-ce01`) were resolving to the same IP address `147.75.201.115`.

**Root Cause:**
This could indicate:
1. **Shared Management Network**: Devices share the same management subnet
2. **Load Balancer/Proxy**: Devices behind a load balancer or proxy
3. **Data Quality Issue**: Incorrect device data in the Forward Networks platform
4. **Network Architecture**: Intentional design where multiple devices share management IPs

**Fix Applied:**
Enhanced the `resolveDeviceToIP` function to:
1. **Detect IP Conflicts**: Check if management or interface IPs are shared between devices
2. **Warn About Conflicts**: Log warnings when IPs are shared
3. **Provide Context**: Show which devices share the same IP
4. **Continue Operation**: Still return the IP for path search purposes

**Files Modified:**
- `internal/service/mcp_service.go` (enhanced `resolveDeviceToIP` function)

### 3. Log Level Configuration

**Current State:**
The logs show `info` and `error` levels, but not `debug` level messages.

**Recommendation:**
To see debug messages, ensure the logger is configured with debug level:
```go
logger.SetLevel(logger.DebugLevel)
```

## Enhanced Device Resolution Logic

### New Features Added:

1. **IP Conflict Detection**:
   - Checks if management IPs are shared between devices
   - Checks if interface IPs are shared between devices
   - Logs warnings when conflicts are detected

2. **Detailed Logging**:
   - Shows which devices share the same IP
   - Provides context about the conflict
   - Helps identify network architecture patterns

3. **Conflict Summary**:
   - Reports all IP conflicts at the end of resolution
   - Helps identify systematic issues in the network data

### Example Enhanced Logs:

```
[INFO] Found device: atl-core-pe01
[WARN] Device atl-core-pe01 management IP 147.75.201.115 is shared with other devices: [atl-core-pe02 atl-ce01]
[INFO] Using management IP for device atl-core-pe01: 147.75.201.115
[WARN] IP conflict detected: 147.75.201.115 is used by multiple devices: [atl-core-pe01 atl-core-pe02 atl-ce01]
```

## Best Practices for Path Search

### When Using Device Names:

1. **Be Aware of IP Conflicts**: Multiple devices might resolve to the same IP
2. **Use Specific Device Names**: Prefer unique device identifiers
3. **Consider Network Architecture**: Understand if shared IPs are intentional
4. **Monitor Logs**: Watch for IP conflict warnings

### When Using IP Addresses:

1. **Use Direct IPs**: Bypass device resolution entirely
2. **Use CIDR Notation**: For subnet-based path searches
3. **Validate IPs**: Ensure IPs are valid and reachable

## Troubleshooting

### If You See IP Conflicts:

1. **Check Network Architecture**: Verify if shared IPs are intentional
2. **Review Device Data**: Ensure device information is accurate in Forward Networks
3. **Use Specific IPs**: Instead of device names, use specific IP addresses
4. **Contact Network Team**: Verify the intended network design

### If NQE Queries Fail:

1. **Check Column Names**: Ensure column names match the API schema
2. **Use Proper Aliases**: Follow the corrected examples in `examples/test_mcp.go`
3. **Validate Query Syntax**: Ensure NQE query syntax is correct
4. **Check API Documentation**: Refer to Forward Networks API documentation

## Summary

These fixes address:
- ✅ **NQE Query Errors**: Fixed invalid column names in examples
- ✅ **Device Resolution**: Enhanced to detect and report IP conflicts
- ✅ **Logging**: Improved visibility into device resolution issues
- ✅ **Path Search Reliability**: Better handling of shared IP scenarios

The system now provides better visibility into network architecture and helps identify potential issues while still allowing path searches to proceed. 