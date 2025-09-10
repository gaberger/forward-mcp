# 🔍 Network Prefix Discovery & Connectivity Analysis

## Overview

The Network Prefix Discovery workflow is a powerful tool for analyzing network connectivity between sites using different aggregation levels. It helps you discover network prefixes, map them to devices, and analyze connectivity patterns across your network infrastructure.

## 🎯 Key Capabilities

### 1. **Prefix Discovery**
- Automatically discover all network prefixes in your network
- Map prefixes to devices and locations
- Identify aggregation opportunities at different levels (/8, /16, /24, etc.)

### 2. **Connectivity Analysis**
- Test connectivity between sites using aggregated prefixes
- Analyze connectivity at different aggregation levels
- Identify connectivity gaps and bottlenecks

### 3. **Topology Mapping**
- Create connectivity matrices for network planning
- Visualize network topology patterns
- Document network architecture

### 4. **Site-to-Site Analysis**
- Analyze connectivity between different physical locations
- Validate routing policies and network segmentation
- Plan network expansions with comprehensive insights

## 🚀 How to Use

### Step 1: Start the Workflow

Use the interactive workflow prompt:

```bash
# In your MCP client
network_prefix_discovery_workflow
```

This will guide you through:
- Understanding the process
- Seeing practical examples
- Getting step-by-step guidance
- Running the actual analysis

### Step 2: Run Network Prefix Analysis

Use the `analyze_network_prefixes` tool with your parameters:

```json
{
  "network_id": "your_network_id",
  "prefix_levels": ["/8", "/16", "/24"],
  "from_devices": ["device1", "device2"],
  "to_devices": ["device3", "device4"],
  "intent": "PREFER_DELIVERED",
  "max_results": 10
}
```

### Step 3: Interpret Results

The analysis provides:

- **CONNECTED**: Full connectivity at this aggregation level
- **PARTIAL**: Some paths exist but not all
- **DISCONNECTED**: No connectivity at this level

## 📊 Example Analysis

### Input
```json
{
  "network_id": "162112",
  "prefix_levels": ["/8", "/16", "/24"],
  "from_devices": ["hq-router", "branch1-router", "branch2-router"],
  "to_devices": ["hq-router", "branch1-router", "branch2-router", "dmz-firewall"],
  "intent": "PREFER_DELIVERED",
  "max_results": 10
}
```

### Expected Results

1. **Prefix Discovery:**
   - 10.0.0.0/8 (aggregated from all sites)
   - 10.1.0.0/16, 10.2.0.0/16, 10.3.0.0/16 (individual sites)
   - 192.168.1.0/24 (DMZ)

2. **Connectivity Matrix:**
   - Site A ↔ Site B: CONNECTED (via 10.0.0.0/8)
   - Site A ↔ Site C: CONNECTED (via 10.0.0.0/8)
   - Site A ↔ DMZ: PARTIAL (via specific routes)
   - All sites ↔ Internet: CONNECTED (via DMZ)

3. **Insights:**
   - All sites have connectivity at /8 level
   - DMZ has restricted access to internal sites
   - Redundant paths exist between major sites
   - Internet access is centralized through DMZ

## 💡 Use Cases

### 1. **Multi-Site Network Planning**
- Analyze connectivity between headquarters and branch offices
- Identify optimal routing paths
- Plan network expansions

### 2. **Network Segmentation Validation**
- Verify that network segments are properly isolated
- Validate security policies
- Ensure compliance requirements are met

### 3. **Route Aggregation Verification**
- Check if route aggregation is working correctly
- Identify routing inefficiencies
- Optimize network routing

### 4. **Connectivity Gap Analysis**
- Find network segments that lack connectivity
- Identify single points of failure
- Plan redundant paths

### 5. **Network Topology Documentation**
- Generate comprehensive network documentation
- Create connectivity matrices
- Document network architecture

## 🔧 Technical Details

### How It Works

1. **Prefix Discovery**
   - Uses NQE queries to discover device IP addresses
   - Extracts network prefixes from device interfaces
   - Maps prefixes to devices and locations

2. **Aggregation Analysis**
   - Groups prefixes by different levels (/8, /16, /24, etc.)
   - Creates connectivity test matrices
   - Tests paths between aggregated prefixes

3. **Connectivity Testing**
   - Uses bulk path search to test connectivity
   - Analyzes results at different aggregation levels
   - Generates connectivity reports

4. **Report Generation**
   - Creates comprehensive connectivity reports
   - Provides insights and recommendations
   - Documents network topology

### Parameters

| Parameter | Type | Description | Required |
|-----------|------|-------------|----------|
| `network_id` | string | Target network for analysis | Yes |
| `prefix_levels` | array | Aggregation levels to analyze (e.g., ["/8", "/16", "/24"]) | No (default: ["/8", "/16", "/24"]) |
| `from_devices` | array | Source devices to analyze | No |
| `to_devices` | array | Destination devices to analyze | No |
| `intent` | string | Search intent (PREFER_DELIVERED, PREFER_VIOLATIONS, VIOLATIONS_ONLY) | No (default: PREFER_DELIVERED) |
| `max_results` | integer | Maximum results per analysis | No (default: 10) |

## 📈 Best Practices

### 1. **Start with Broader Aggregation**
- Begin with /8 and /16 levels to understand overall connectivity
- Then drill down to /24 for specific site analysis

### 2. **Focus on Key Devices**
- Start with core network devices (routers, switches)
- Then include edge devices and firewalls

### 3. **Use Appropriate Intent**
- `PREFER_DELIVERED`: Normal connectivity testing
- `PREFER_VIOLATIONS`: Find connectivity issues
- `VIOLATIONS_ONLY`: Focus on problems only

### 4. **Set Reasonable Limits**
- Use appropriate `max_results` to avoid timeouts
- Start with smaller numbers and increase as needed

### 5. **Document Results**
- Save analysis reports for future reference
- Use results for network documentation
- Track changes over time

## 🎯 Example Workflows

### Workflow 1: Initial Network Assessment
```json
{
  "network_id": "your_network_id",
  "prefix_levels": ["/8", "/16"],
  "intent": "PREFER_DELIVERED",
  "max_results": 5
}
```

### Workflow 2: Site-to-Site Analysis
```json
{
  "network_id": "your_network_id",
  "prefix_levels": ["/16", "/24"],
  "from_devices": ["hq-router", "branch1-router"],
  "to_devices": ["hq-router", "branch1-router", "internet-gateway"],
  "intent": "PREFER_DELIVERED",
  "max_results": 10
}
```

### Workflow 3: Security Validation
```json
{
  "network_id": "your_network_id",
  "prefix_levels": ["/24"],
  "from_devices": ["dmz-firewall", "internal-router"],
  "to_devices": ["dmz-firewall", "internal-router", "internet-gateway"],
  "intent": "PREFER_VIOLATIONS",
  "max_results": 15
}
```

## 🔍 Troubleshooting

### Common Issues

1. **No Prefixes Found**
   - Check if devices have IP addresses configured
   - Verify network ID is correct
   - Ensure devices are online and accessible

2. **No Connectivity Results**
   - Verify device names are correct
   - Check if devices are in the same network
   - Ensure routing is properly configured

3. **Timeout Errors**
   - Reduce `max_results` parameter
   - Focus on fewer devices initially
   - Use broader aggregation levels

### Getting Help

- Use the interactive workflow for step-by-step guidance
- Check the example scenarios in the workflow
- Review the best practices section
- Contact support if issues persist

## 🚀 Next Steps

1. **Start with the Workflow**: Use `network_prefix_discovery_workflow` for guided experience
2. **Run Initial Analysis**: Test with your network ID and basic parameters
3. **Explore Results**: Review the connectivity reports and insights
4. **Plan Improvements**: Use findings to optimize your network
5. **Document Architecture**: Generate comprehensive network documentation

---

*This workflow provides powerful insights into your network topology and connectivity patterns. Use it to optimize your network infrastructure and ensure reliable connectivity across all sites.* 