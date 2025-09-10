# 🔄 Device Name Resolution for Path Search

## Overview

The `search_paths_bulk` tool now supports automatic device name resolution for the `dst_ip` field. This allows you to use device names instead of IP addresses when specifying destinations, making path search requests more intuitive and user-friendly.

## 🎯 How It Works

### Automatic Resolution Process

1. **Input Validation**: The tool first checks if the `dst_ip` value looks like an IP address or CIDR
2. **Device Name Detection**: If it's not an IP/CIDR, it's treated as a device name
3. **Device Lookup**: The tool queries the network's device inventory to find the device
4. **IP Resolution**: If found, the device's first management IP is used
5. **Fallback**: If resolution fails, the original value is used as-is

### Resolution Logic

```go
// Pseudo-code of the resolution process
if net.ParseIP(dstIP) == nil && !strings.Contains(dstIP, "/") {
    // Might be a device name, try to resolve it
    resolvedIP, err := resolveDeviceToIP(networkID, dstIP)
    if err != nil {
        // Use original value as-is
        log.Warn("Could not resolve device name, using as-is")
    } else {
        dstIP = resolvedIP
    }
}
```

## 📝 Usage Examples

### Example 1: Device Name to IP Resolution

**Request:**
```json
{
  "network_id": "162112",
  "queries": [
    {
      "from": "atl-core-pe01",
      "dst_ip": "sjc-core-pe01"
    },
    {
      "from": "sjc-core-pe01", 
      "dst_ip": "atl-core-pe01"
    }
  ],
  "intent": "PREFER_DELIVERED",
  "max_results": 2
}
```

**What Happens:**
1. `"dst_ip": "sjc-core-pe01"` is detected as a device name
2. Tool looks up `sjc-core-pe01` in the device inventory
3. Finds the device and gets its management IP (e.g., `10.1.1.1`)
4. Sends request with `"dst_ip": "10.1.1.1"` to the API

### Example 2: Mixed IP and Device Names

**Request:**
```json
{
  "network_id": "162112",
  "queries": [
    {
      "from": "atl-core-pe01",
      "dst_ip": "192.168.1.1"
    },
    {
      "from": "sjc-core-pe01",
      "dst_ip": "atl-core-pe01"
    },
    {
      "from": "atl-edge01",
      "dst_ip": "10.0.0.0/24"
    }
  ]
}
```

**Resolution Results:**
- `"dst_ip": "192.168.1.1"` → Used as-is (valid IP)
- `"dst_ip": "atl-core-pe01"` → Resolved to device's management IP
- `"dst_ip": "10.0.0.0/24"` → Used as-is (valid CIDR)

### Example 3: Fallback Behavior

**Request:**
```json
{
  "network_id": "162112",
  "queries": [
    {
      "from": "atl-core-pe01",
      "dst_ip": "nonexistent-device"
    }
  ]
}
```

**What Happens:**
1. `"dst_ip": "nonexistent-device"` is detected as a device name
2. Tool searches for device but doesn't find it
3. Logs a warning: `"Could not resolve dst_ip 'nonexistent-device' as device name, using as-is"`
4. Sends request with original value: `"dst_ip": "nonexistent-device"`
5. API will return an error about invalid IP/subnet

## 🔧 Technical Details

### Device Lookup Process

1. **API Call**: `GET /api/networks/{networkID}/devices`
2. **Device Matching**: Searches for device with `Name == dst_ip`
3. **IP Extraction**: Uses first management IP from `ManagementIPs[]` array
4. **Error Handling**: Graceful fallback if device not found

### Supported Input Types

| Input Type | Example | Resolution |
|------------|---------|------------|
| **IP Address** | `192.168.1.1` | Used as-is |
| **CIDR Subnet** | `10.0.0.0/24` | Used as-is |
| **Device Name** | `atl-core-pe01` | Resolved to management IP |
| **Invalid Device** | `nonexistent-device` | Used as-is (will cause API error) |

### Error Scenarios

1. **Device Not Found**: Original value used, API returns error
2. **Device Has No Management IP**: Returns error about missing management IP
3. **Network API Error**: Returns error about device lookup failure
4. **Invalid IP Format**: API returns validation error

## 🚀 Benefits

### 1. **User-Friendly Interface**
- Use familiar device names instead of remembering IP addresses
- More intuitive path search requests
- Easier to understand and maintain

### 2. **Flexible Input**
- Supports both IP addresses and device names
- Automatic detection and resolution
- Graceful fallback for edge cases

### 3. **Consistent Behavior**
- Works with existing IP/CIDR inputs
- No breaking changes to current usage
- Backward compatible

### 4. **Better Error Messages**
- Clear logging of resolution attempts
- Helpful warnings when resolution fails
- Debug information for troubleshooting

## 📋 Best Practices

### 1. **Use Device Names for Clarity**
```json
// ✅ Good - Clear and readable
{
  "from": "atl-core-pe01",
  "dst_ip": "sjc-core-pe01"
}

// ❌ Less clear - Requires IP knowledge
{
  "from": "atl-core-pe01", 
  "dst_ip": "10.1.1.1"
}
```

### 2. **Handle Resolution Failures**
- Check logs for resolution warnings
- Verify device names are correct
- Use IP addresses as fallback if needed

### 3. **Monitor Performance**
- Device resolution adds one API call per unique device name
- Consider caching device information for large bulk requests
- Use IP addresses directly for performance-critical scenarios

### 4. **Validate Device Names**
- Ensure device names match exactly (case-sensitive)
- Check that devices have management IPs configured
- Verify devices are in the target network

## 🔍 Troubleshooting

### Common Issues

1. **"Could not resolve device name"**
   - Check if device name is spelled correctly
   - Verify device exists in the network
   - Ensure device has management IP configured

2. **"Device not found in network"**
   - Confirm network ID is correct
   - Check if device is in the specified network
   - Verify device inventory is up to date

3. **"Device found but has no management IP"**
   - Check device configuration
   - Verify management interface is configured
   - Contact network administrator

### Debug Information

The tool provides detailed logging for troubleshooting:

```
DEBUG: Resolving device name to IP: sjc-core-pe01
DEBUG: Resolved device sjc-core-pe01 to IP 10.1.1.1
WARN: Could not resolve dst_ip 'nonexistent-device' as device name, using as-is: device nonexistent-device not found in network 162112
```

## 🎯 Example Workflows

### Workflow 1: Site-to-Site Connectivity
```json
{
  "network_id": "162112",
  "queries": [
    {"from": "atl-core-pe01", "dst_ip": "sjc-core-pe01"},
    {"from": "sjc-core-pe01", "dst_ip": "atl-core-pe01"},
    {"from": "atl-edge01", "dst_ip": "sjc-edge01"},
    {"from": "sjc-edge01", "dst_ip": "atl-edge01"}
  ],
  "intent": "PREFER_DELIVERED",
  "max_results": 5
}
```

### Workflow 2: Hub-and-Spoke Testing
```json
{
  "network_id": "162112", 
  "queries": [
    {"from": "hq-router", "dst_ip": "branch1-router"},
    {"from": "hq-router", "dst_ip": "branch2-router"},
    {"from": "hq-router", "dst_ip": "branch3-router"},
    {"from": "branch1-router", "dst_ip": "hq-router"},
    {"from": "branch2-router", "dst_ip": "hq-router"},
    {"from": "branch3-router", "dst_ip": "hq-router"}
  ],
  "intent": "PREFER_DELIVERED",
  "max_results": 3
}
```

### Workflow 3: Mixed Input Types
```json
{
  "network_id": "162112",
  "queries": [
    {"from": "atl-core-pe01", "dst_ip": "sjc-core-pe01"},
    {"from": "sjc-core-pe01", "dst_ip": "192.168.1.1"},
    {"from": "atl-edge01", "dst_ip": "10.0.0.0/24"},
    {"from": "sjc-edge01", "dst_ip": "internet-gateway"}
  ],
  "intent": "PREFER_DELIVERED",
  "max_results": 2
}
```

---

*This feature makes path search more intuitive by allowing you to use device names instead of IP addresses, while maintaining full backward compatibility with existing IP-based requests.* 