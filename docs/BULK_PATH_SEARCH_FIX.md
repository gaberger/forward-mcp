# Bulk Path Search Fix

## Issue Description

The `search_paths_bulk` tool was reporting "0 paths found" even when the Forward Networks API was returning valid paths. This was discovered when comparing the curl command results with our MCP implementation.

## Root Cause

The issue was a **response structure mismatch** between what our code expected and what the bulk path search API actually returns.

### Expected Structure (Single Path Search)
```json
{
  "paths": [...],
  "returnPaths": [...],
  "snapshotId": "...",
  "searchTimeMs": 100,
  "numCandidatesFound": 1
}
```

### Actual Structure (Bulk Path Search)
```json
[
  {
    "dstIpLocationType": "INTERNET",
    "info": {
      "paths": [
        {
          "forwardingOutcome": "DROPPED",
          "securityOutcome": "PERMITTED",
          "hops": [
            {
              "deviceName": "atl-core-pe01",
              "deviceType": "ROUTER",
              "ingressInterface": "self.INTERNET-IN",
              "egressInterface": "ge-0/0/4",
              "behaviors": ["L3"]
            }
          ]
        }
      ],
      "totalHits": {
        "value": 9,
        "type": "EXACT"
      }
    },
    "returnPathInfo": {
      "paths": [],
      "totalHits": {
        "value": 0,
        "type": "EXACT"
      }
    },
    "timedOut": false,
    "queryUrl": "..."
  }
]
```

## Key Differences

1. **Response Format**: Bulk API returns an array of responses, not a single response
2. **Path Location**: Paths are nested under `info.paths`, not directly under `paths`
3. **Hop Structure**: Different field names (`deviceName` vs `device`, `ingressInterface`/`egressInterface` vs `interface`)
4. **Additional Fields**: `dstIpLocationType`, `forwardingOutcome`, `securityOutcome`, `behaviors`, etc.

## Solution Implemented

### 1. New Response Types
Added new structs in `internal/forward/client.go`:

```go
type PathSearchBulkResponse struct {
    DstIpLocationType string         `json:"dstIpLocationType"`
    Info              PathSearchInfo `json:"info"`
    ReturnPathInfo    PathSearchInfo `json:"returnPathInfo"`
    TimedOut          bool           `json:"timedOut"`
    QueryUrl          string         `json:"queryUrl"`
}

type PathSearchInfo struct {
    Paths     []BulkPath `json:"paths"`
    TotalHits TotalHits  `json:"totalHits"`
}

type BulkPath struct {
    ForwardingOutcome string    `json:"forwardingOutcome"`
    SecurityOutcome   string    `json:"securityOutcome"`
    Hops              []BulkHop `json:"hops"`
}

type BulkHop struct {
    DeviceName       string   `json:"deviceName"`
    DeviceType       string   `json:"deviceType"`
    IngressInterface string   `json:"ingressInterface"`
    EgressInterface  string   `json:"egressInterface"`
    Behaviors        []string `json:"behaviors"`
}
```

### 2. Updated Interface
Modified `ClientInterface.SearchPathsBulk` to return `[]PathSearchBulkResponse` instead of `[]PathSearchResponse`.

### 3. Updated Response Processing
Modified `searchPathsBulk` in `internal/service/mcp_service.go` to:
- Access paths via `response.Info.Paths` instead of `response.Paths`
- Handle the new response structure correctly
- Convert bulk responses to legacy format for API tracking

### 4. Updated Mock Client
Updated `MockForwardClient.SearchPathsBulk` to return the correct response type and convert legacy responses to bulk format.

## Testing

The fix has been tested with:
- Unit tests pass ✅
- Mock client updated to handle new response format ✅
- Response processing logic updated ✅

## Impact

- **Fixed**: Bulk path search now correctly reports the number of paths found
- **Backward Compatible**: Single path search continues to work as before
- **Enhanced Logging**: Added debug logging to help diagnose similar issues in the future

## Example

Before the fix:
```
Bulk path search completed. 0/1 queries successful, found 0 total paths
```

After the fix:
```
Bulk path search completed. 1/1 queries successful, found 1 total paths
```

## Lessons Learned

1. **API Response Formats**: Different endpoints can return different response structures even for similar operations
2. **Testing with Real Data**: Always test with actual API responses, not just assumptions
3. **Debug Logging**: Comprehensive logging helps identify structural mismatches quickly
4. **Interface Design**: Consider whether different response types should share the same interface 