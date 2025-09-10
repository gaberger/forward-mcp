# IP Address Validation Fix

## Issue Description

The path search tools were not properly validating that `dst_ip` parameters were valid IP addresses before sending requests to the Forward Networks API. This led to API errors like:

```
"Could not parse as IP subnet or address: usa-core-p02"
```

## Root Cause

The original implementation had two problems:

1. **Insufficient IP Validation**: The code was too permissive and would pass invalid strings to the API
2. **Mock Data Issue**: The mock client was returning fake data instead of real API results

## Solution Implemented

### 1. Strict IP Address Validation

Added comprehensive validation in both `searchPaths` and `searchPathsBulk` functions:

```go
// Validate and resolve dstIP - must be a valid IP address or CIDR
dstIP := args.DstIP
if net.ParseIP(dstIP) != nil {
    s.logger.Debug("dstIP '%s' is a valid IP address", dstIP)
} else if strings.Contains(dstIP, "/") {
    // Check if it's a valid CIDR
    if _, _, err := net.ParseCIDR(dstIP); err != nil {
        return nil, fmt.Errorf("dstIP '%s' is not a valid IP address or CIDR: %w", dstIP, err)
    }
    s.logger.Debug("dstIP '%s' is a valid CIDR", dstIP)
} else {
    // Not an IP or CIDR, try to resolve as device name
    s.logger.Debug("Attempting to resolve device name: %s", dstIP)
    resolvedIP, err := s.resolveDeviceToIP(networkID, dstIP)
    if err != nil {
        return nil, fmt.Errorf("dstIP '%s' is not a valid IP address, CIDR, or resolvable device name: %w", dstIP, err)
    }
    dstIP = resolvedIP
    s.logger.Info("Successfully resolved dstIP from device name '%s' to IP '%s'", args.DstIP, dstIP)
}
```

### 2. Validation Logic

The validation follows this hierarchy:

1. **IP Address Check**: Use `net.ParseIP()` to validate IPv4/IPv6 addresses
2. **CIDR Check**: Use `net.ParseCIDR()` to validate subnet notation (e.g., `192.168.1.0/24`)
3. **Device Name Resolution**: If not an IP/CIDR, attempt to resolve as device name
4. **Error on Failure**: If all validation fails, return a clear error message

### 3. Updated Tool Descriptions

Updated both `search_paths` and `search_paths_bulk` tool descriptions to clearly state the requirements:

- **REQUIRED**: `dst_ip` must be a valid IP address, CIDR, or resolvable device name
- **Device names** are automatically resolved to management IPs
- **If device name resolution fails**, the request will be rejected

### 4. Error Messages

Improved error messages to be more specific:

- `"dstIP 'usa-core-p02' is not a valid IP address, CIDR, or resolvable device name"`
- `"query 1: dst_ip 'invalid-ip' is not a valid IP address or CIDR"`

## Testing

The fix has been tested with:

- **Valid IP addresses**: `192.168.1.1`, `10.0.0.0/24`, `2001:db8::1`
- **Valid device names**: `atl-core-pe01` (resolved to IP)
- **Invalid inputs**: `usa-core-p02`, `not-an-ip`, `invalid-format`
- **Unit tests**: All tests pass ✅

## Impact

### Before the Fix:
```
API Error: "Could not parse as IP subnet or address: usa-core-p02"
```

### After the Fix:
```
Error: dstIP 'usa-core-p02' is not a valid IP address, CIDR, or resolvable device name: device not found
```

## Benefits

1. **Prevents API Errors**: Invalid requests are caught before reaching the API
2. **Clear Error Messages**: Users get specific feedback about what's wrong
3. **Device Name Support**: Still allows device names with automatic resolution
4. **Flexible Input**: Accepts IP addresses, CIDR notation, and device names
5. **Consistent Validation**: Same logic applied to both single and bulk path searches

## Usage Examples

### Valid Inputs:
```json
{
  "dst_ip": "192.168.1.1"           // Valid IP
}
{
  "dst_ip": "10.0.0.0/24"           // Valid CIDR
}
{
  "dst_ip": "atl-core-pe01"         // Valid device name (resolved)
}
```

### Invalid Inputs (now rejected):
```json
{
  "dst_ip": "usa-core-p02"          // Invalid device name
}
{
  "dst_ip": "not-an-ip"             // Invalid string
}
{
  "dst_ip": "192.168.1.1/33"        // Invalid CIDR
}
```

## Lessons Learned

1. **API Validation**: Always validate inputs before sending to external APIs
2. **Error Handling**: Provide clear, actionable error messages
3. **Flexibility**: Support multiple input formats (IP, CIDR, device names)
4. **Consistency**: Apply the same validation logic across related functions
5. **Documentation**: Update tool descriptions to reflect requirements 