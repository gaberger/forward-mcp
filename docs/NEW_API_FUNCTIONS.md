# New API Functions Guide

## Overview

This release adds several new MCP tools that expose additional Forward Networks API functionality for managing snapshots, locations, and device-location mappings.

## New Functions

### 1. Delete Snapshot

**Tool Name**: `delete_snapshot`

**Description**: Delete a network snapshot permanently.

**Use Cases**:
- Cleanup old snapshots to free up storage
- Remove snapshots from failed collections
- Maintain snapshot retention policies

**Arguments**:
```json
{
  "snapshot_id": "string (required)"
}
```

**Example**:
```json
{
  "snapshot_id": "snapshot-20240115-120000"
}
```

**Response**:
```
Snapshot snapshot-20240115-120000 deleted successfully
```

**Important Notes**:
- ⚠️ **WARNING**: This operation is permanent and cannot be undone
- Deletes all associated historical data with the snapshot
- Requires appropriate permissions in Forward Networks
- Use `list_snapshots` to get snapshot IDs before deletion

**Error Handling**:
- Returns error if snapshot doesn't exist
- Returns error if snapshot is in use
- Returns error if insufficient permissions

---

### 2. Update Location

**Tool Name**: `update_location`

**Description**: Update an existing location's properties in a network.

**Use Cases**:
- Correct location names or descriptions
- Update GPS coordinates for physical sites
- Reorganize network topology

**Arguments**:
```json
{
  "network_id": "string (required)",
  "location_id": "string (required)",
  "name": "string (optional)",
  "description": "string (optional)",
  "latitude": "number (optional)",
  "longitude": "number (optional)"
}
```

**Example**:
```json
{
  "network_id": "net-12345",
  "location_id": "loc-67890",
  "name": "New York Data Center",
  "description": "Primary US East Coast facility",
  "latitude": 40.7128,
  "longitude": -74.0060
}
```

**Response**:
```json
Location updated successfully:
{
  "id": "loc-67890",
  "name": "New York Data Center",
  "description": "Primary US East Coast facility",
  "latitude": 40.7128,
  "longitude": -74.0060,
  "networkId": "net-12345"
}
```

**Important Notes**:
- All update fields are optional - only specify what you want to change
- Location ID cannot be changed (it's the identifier)
- Network ID is required to scope the operation

---

### 3. Delete Location

**Tool Name**: `delete_location`

**Description**: Delete a location from a network.

**Use Cases**:
- Remove decommissioned sites
- Clean up test locations
- Reorganize network topology

**Arguments**:
```json
{
  "network_id": "string (required)",
  "location_id": "string (required)"
}
```

**Example**:
```json
{
  "network_id": "net-12345",
  "location_id": "loc-67890"
}
```

**Response**:
```json
Location deleted successfully:
{
  "id": "loc-67890",
  "name": "Old Location",
  "networkId": "net-12345"
}
```

**Important Notes**:
- Deletes the location permanently
- Devices assigned to this location may need to be reassigned
- Use `list_locations` to find location IDs

---

### 4. Update Device Locations

**Tool Name**: `update_device_locations`

**Description**: Update device location assignments in bulk.

**Use Cases**:
- Assign multiple devices to locations after device discovery
- Reorganize devices during site migrations
- Batch update device locations for network topology changes

**Arguments**:
```json
{
  "network_id": "string (required)",
  "locations": "object (required) - map of device IDs to location IDs"
}
```

**Example**:
```json
{
  "network_id": "net-12345",
  "locations": {
    "device-001": "loc-nyc-dc1",
    "device-002": "loc-nyc-dc1",
    "device-003": "loc-sfo-dc1",
    "device-004": "loc-lon-dc1"
  }
}
```

**Response**:
```
Updated locations for 4 devices
```

**Important Notes**:
- Updates are applied in bulk for efficiency
- Device IDs must exist in the network
- Location IDs must exist in the network
- Invalid device or location IDs will cause the entire operation to fail
- Use `list_devices` to get device IDs
- Use `list_locations` to get location IDs

**Best Practices**:
- Validate device and location IDs before bulk update
- Start with small batches to verify mappings
- Use `get_device_locations` to verify updates

---

## Common Workflows

### Workflow 1: Clean Up Old Snapshots

```json
// Step 1: List snapshots to find old ones
{
  "tool": "list_snapshots",
  "arguments": {
    "network_id": "net-12345"
  }
}

// Step 2: Delete old snapshot
{
  "tool": "delete_snapshot",
  "arguments": {
    "snapshot_id": "snapshot-20230101-120000"
  }
}
```

### Workflow 2: Reorganize Network Locations

```json
// Step 1: Create new location
{
  "tool": "create_location",
  "arguments": {
    "network_id": "net-12345",
    "name": "New York DC2",
    "description": "Backup data center",
    "latitude": 40.7580,
    "longitude": -73.9855
  }
}

// Step 2: Update existing location details
{
  "tool": "update_location",
  "arguments": {
    "network_id": "net-12345",
    "location_id": "loc-nyc-dc1",
    "description": "Primary data center (updated)"
  }
}

// Step 3: Move devices to new location
{
  "tool": "update_device_locations",
  "arguments": {
    "network_id": "net-12345",
    "locations": {
      "device-005": "loc-nyc-dc2",
      "device-006": "loc-nyc-dc2"
    }
  }
}

// Step 4: Delete old unused location
{
  "tool": "delete_location",
  "arguments": {
    "network_id": "net-12345",
    "location_id": "loc-old-site"
  }
}
```

### Workflow 3: Bulk Device Location Setup

```json
// Step 1: List all devices
{
  "tool": "list_devices",
  "arguments": {
    "network_id": "net-12345"
  }
}

// Step 2: List available locations
{
  "tool": "list_locations",
  "arguments": {
    "network_id": "net-12345"
  }
}

// Step 3: Assign devices to locations in bulk
{
  "tool": "update_device_locations",
  "arguments": {
    "network_id": "net-12345",
    "locations": {
      "router-nyc-001": "loc-nyc-dc1",
      "router-nyc-002": "loc-nyc-dc1",
      "switch-nyc-001": "loc-nyc-dc1",
      "router-sfo-001": "loc-sfo-dc1",
      "router-sfo-002": "loc-sfo-dc1"
    }
  }
}

// Step 4: Verify assignments
{
  "tool": "get_device_locations",
  "arguments": {
    "network_id": "net-12345"
  }
}
```

## API Reference

### Complete Function List

| Function | Category | Description |
|----------|----------|-------------|
| `delete_snapshot` | Snapshot Management | Delete a network snapshot |
| `update_location` | Location Management | Update location properties |
| `delete_location` | Location Management | Delete a location |
| `update_device_locations` | Device Management | Bulk update device locations |

### Related Existing Functions

| Function | Description |
|----------|-------------|
| `list_snapshots` | List all snapshots for a network |
| `get_latest_snapshot` | Get the most recent snapshot |
| `list_locations` | List all locations in a network |
| `create_location` | Create a new location |
| `list_devices` | List all devices in a network |
| `get_device_locations` | Get current device-location mappings |

## Error Handling

### Common Errors

#### Invalid Network ID
```
Error: Network not found: net-invalid
Solution: Verify network ID using list_networks
```

#### Invalid Snapshot ID
```
Error: Snapshot not found: snapshot-invalid
Solution: Use list_snapshots to find valid snapshot IDs
```

#### Invalid Location ID
```
Error: Location not found: loc-invalid
Solution: Use list_locations to find valid location IDs
```

#### Invalid Device ID
```
Error: Device not found in network
Solution: Use list_devices to verify device IDs
```

#### Permission Denied
```
Error: Insufficient permissions to delete snapshot
Solution: Verify API credentials have appropriate permissions
```

#### Snapshot In Use
```
Error: Cannot delete snapshot: currently in use by queries
Solution: Wait for queries to complete or use a different snapshot
```

## Testing

### Unit Tests

Test the new handler functions:
```bash
go test ./internal/service/... -run TestDelete -v
go test ./internal/service/... -run TestUpdate -v
```

### Integration Tests

Test complete workflows:
```bash
# Test snapshot deletion
go test ./internal/service/... -run TestDeleteSnapshot -v

# Test location management
go test ./internal/service/... -run TestLocationManagement -v

# Test device location updates
go test ./internal/service/... -run TestDeviceLocationUpdate -v
```

## Security Considerations

1. **Snapshot Deletion**: Requires DELETE permissions in Forward Networks
2. **Location Management**: Requires WRITE permissions for network configuration
3. **Device Location Updates**: Requires WRITE permissions for device management
4. **Audit Trail**: All operations are logged for audit purposes

## Performance Considerations

### Bulk Operations

For `update_device_locations`:
- Recommended batch size: 100-500 devices
- For larger updates, split into multiple batches
- Monitor API rate limits

### Snapshot Deletion

- Snapshot deletion may take several seconds
- Does not affect other operations
- Safe to perform during normal operations

## Migration Guide

### From Previous Versions

If you were using the Forward API directly for these operations, you can now use the MCP tools:

**Before** (Direct API):
```bash
curl -X DELETE https://api.forward.com/api/snapshots/snapshot-123 \
  -H "Authorization: Bearer $TOKEN"
```

**After** (MCP Tool):
```json
{
  "tool": "delete_snapshot",
  "arguments": {
    "snapshot_id": "snapshot-123"
  }
}
```

## Changelog

### Version 2.2.0

**New Tools**:
- `delete_snapshot`: Delete network snapshots
- `update_location`: Update location properties
- `delete_location`: Delete locations
- `update_device_locations`: Bulk update device-location mappings

**Total Tool Count**: 55 tools (was 51)

## Support

For issues or questions:
1. Check the error message for specific details
2. Verify all IDs using list functions
3. Check API permissions in Forward Networks
4. Review logs for detailed error information
5. Consult Forward Networks API documentation for underlying API behavior
