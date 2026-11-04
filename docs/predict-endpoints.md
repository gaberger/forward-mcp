# Forward Predict — HTTP contract (reference for forward-mcp M1b)

**Generated** from `forwardnetworks/forward-python-sdk` `spec/unpublished.yaml` at commit `19a97a7d1ae2b660de35b918b919efb60a1509c1` (2026-11-03) by `scripts/gen-predict-endpoints` — do not hand-edit the tables; edit the *Semantics* section only. Regenerate with `python3 scripts/gen-predict-endpoints/gen_predict_endpoints.py <sdk-checkout> docs/predict-endpoints.md` (needs PyYAML).

All paths are relative to the server base `/api` (same base as the published API). These endpoints are marked `x-forward-stability: unpublished` by Forward: they may change without notice. Spec task 1b.5: any live response that disagrees with this document is a **DIVERGENCE** — stop and report, do not adapt silently.

Conventions that differ from the published API (from the SDK's own notes):
- a query-string `action=` selects the operation, so predict and delete share a path;
- Forward rejects unknown JSON body properties with HTTP 400, so bodies carry exactly the documented keys;
- device command / config bodies are `text/plain; charset=utf-8`, not JSON; max 2 MiB.

## Semantics (hand-written; the SDK's `docs/predict.md` and service code are the source)

### Round trip that `predict_run_and_wait` must implement

1. **Base snapshot**: use the latest *processed* snapshot whose `processingTrigger` is not `PREDICT` (a `REPROCESS` snapshot is real collected data and is fine). forward-mcp already has `GetLatestSnapshot`; it must additionally filter out `PREDICT` snapshots (new in M1b).
2. `createChangeSet` → `ChangeSet.id`. Body keys exactly `name`, `snapshotId`, optional `description`, `tags`. Tag every change set forward-mcp creates with `netorch:<subject>:<run-id>` (1b.6 cleanup).
3. `setChangeSetDeviceCommands` per device (`text/plain`, replaces that device's staged text) and/or firewall rule edits via `addSecurityRule` / `updateSecurityRule` / `deleteSecurityRule`. `validateChangeSetDeviceCommands` is optional and advisory (see caveats).
4. `commitChangeSet` with `note` (query param, required). Optional `analysisMode` (`BEHAVIORAL` default, `CONFIG_ONLY`).
5. `predictChangeSet` with `note` (query param, required). **Not idempotent**: never retry after an ambiguous failure; instead call `listPredictedSnapshots` and reuse the newest one. Response is `PredictedSnapshotMeta{id}` — a snapshot that exists but is *not yet processed*.
6. **Wait for processing**: there is no single-snapshot endpoint; poll `GET /networks/{networkId}/snapshots` (published) and look up the predicted id. Terminal states: `PROCESSED` (success), `FAILED`, `CANCELED`, `TIMED_OUT`, `RESTORE_FAILED` (failure), `ARCHIVED`. SDK default poll 15 s, timeout 3600 s.
7. **Results**: every diff endpoint is `GET /diffs/{before}/{after}/...` with `before` = base snapshot id, `after` = predicted snapshot id.
   - `getSubnetConnectivityDiff`: poll (SDK: every 2 s, 300 s timeout) until `isPartialResult == false`. The `SNAPSHOT_READY` webhook fires *before* this computation starts, so a single early read returns zeros.
   - per-area `?count` endpoints (devices, interfaces, topology, l2, acl, nat, arp, mac, files, checks): each returns `{count, complete}`; `complete:false` means a lower bound.
   - `getRoutingLoopDiffCount`, `getVulnerabilityDiffCounts` need no gating; connectivity needs the `LOCATION_CONNECTIVITY_DIFFS` org property; bidirectional views need `PREDICT_DIFF_INSIGHT_ENHANCEMENTS`.
   - NQE-based checks: `computeNqeCheckDiffs` then `getCheckDiffs?type=NQE`; arbitrary NQE queries across the two snapshots go through the published NQE diff endpoint already wrapped by `forward.Client.DiffNQEQuery`.
8. Cleanup: `deleteChangeSet` (or bulk `deleteChangeSets` with body key `changeSetIds`, not `ids`). **Deleting a change set cascades to its predicted snapshots.**

### Verdict rules for `predict_get_results` (PLAN.md D4)

| Observation | Verdict |
|---|---|
| predicted snapshot in a failure state, or wait timed out | `UNKNOWN` (reason: processing) |
| connectivity `isPartialResult == true` at deadline, or any requested area `complete == false` | `UNKNOWN` (reason: partial) |
| connectivity `totalSubnetPairs == 0` | `UNKNOWN` (reason: nothing compared — network has no locations) |
| `newlyIsolatedSubnetPairs > 0`, or any NQE/intent check went pass→fail | `FAIL` |
| otherwise | `PASS` |

`UNKNOWN` is never treated as approval by the M3 workflow.

### Caveats recorded by the SDK authors (verify live; see 1b.5)

- PAN-OS: CLI `set` text fails prediction ("OS pan_os not supported"); use structured rule edits. Actions `RESET_*` stage but break prediction; `DROP` predicts as `DENY`. forward-mcp's `predict_stage_edits` rejects PAN-OS CLI text up front.
- `validateChangeSetDeviceCommands` returns empty for PAN-OS and misses some valid EOS commands — an empty/negative result is not proof.
- A prediction over a stale base can fail with no reason; `reprocessSnapshot` the base and predict again.
- `listPredictAffectedDevices` returned every L3 device on the observed network; use diffs instead (not wrapped).
- The SDK's live test account got 403 listing change sets, so commit, copy and device-config operations were verified from source and recorded tests only.
- Forward reports missing licence, undeployed feature and RBAC denial with the same 401/403/404 + `ErrorInfo` body; the client cannot tell them apart. Surface the `ErrorInfo` text to the agent, redacted.

### What forward-mcp wraps in M1b

| MCP tool (propose) | Endpoints |
|---|---|
| `predict_create_change_set` | latest non-PREDICT processed snapshot (published), `createChangeSet` |
| `predict_stage_edits` | `setChangeSetDeviceCommands`, `validateChangeSetDeviceCommands` (advisory), `addSecurityRule`/`updateSecurityRule`/`deleteSecurityRule`, `getSecurityRulesDiff` |
| `predict_run_and_wait` | `commitChangeSet`, `predictChangeSet`, snapshot list poll, `listPredictedSnapshots` (recovery) |
| `predict_get_results` | `getSubnetConnectivityDiff` (poll), the ten `?count` endpoints, `getRoutingLoopDiffCount`, `getVulnerabilityDiffCounts`, `computeNqeCheckDiffs` + `getCheckDiffs`, per-area detail on request |
| `predict_gc` (maintenance, write/local) | `listChangeSets`, `deleteChangeSets` |

---

## Change-set lifecycle

### `createChangeSet` — `POST /networks/{networkId}/change-sets`

Create a change set against a base snapshot

| Param | In | Required | Type | Notes |
|---|---|---|---|---|
| `dirPath` | query | no | `string` | Directory in the change-set tree to create it in. |

**Request body** (`application/json`, required): `NewChangeSet`

```json
{
  "name": "Core uplink change",
  "description": "Rehearsal",
  "snapshotId": "656",
  "tags": [
    "rehearsal"
  ]
}
```

**Response 200**: The new change set.

`application/json` → `ChangeSet`

```json
{
  "id": "CHG-101",
  "name": "Core uplink change",
  "networkId": "115",
  "snapshotId": "656",
  "tags": [
    "rehearsal"
  ],
  "createdAt": "2026-09-11T10:00:00Z"
}
```

### `getChangeSetSummary` — `GET /networks/{networkId}/change-sets/{changeSetId}?view=summary`

A change set's name, base snapshot and tags

There is no plain GET on a change set; this view is the only read of one by id. It also lists the ServiceNow change requests linked to the change set (serviceNowChangeRequests, omitted when none) and the Predict analysisMode once chosen (source: ChangeSetController.getChangeSetSummary, ChangeSetSummaryResponse).

**Response 200**: The summary.

`application/json` → `ChangeSetSummary`

```json
{
  "name": "Core uplink change",
  "networkId": "115",
  "snapshotId": "656",
  "tags": [],
  "analysisMode": "BEHAVIORAL",
  "serviceNowChangeRequests": [
    {
      "number": "CHG0000001",
      "sysId": "0123456789abcdef0123456789abcdef"
    }
  ]
}
```

### `getChangeSetDraft` — `GET /networks/{networkId}/change-sets/{changeSetId}/draft`

The uncommitted changes on a change set

**Response 200**: The draft, or a null draft when nothing is staged.

`application/json` → `ChangeSetDraftResponse`

```json
{
  "draft": {
    "id": "CHG-101",
    "name": "Core uplink change",
    "networkId": "115",
    "snapshotId": "656",
    "deviceToChanges": {
      "dc-core": {
        "commands": "interface Ethernet1\n shutdown",
        "hasConfig": false,
        "hasSecurityRuleChanges": false
      }
    },
    "savedAt": 1757584800000
  }
}
```

### `setChangeSetDeviceCommands` — `PUT /networks/{networkId}/change-sets/{changeSetId}/draft/devices/{deviceName}/commands`

Stage CLI commands for one device

The body is the command text itself, sent as text/plain, not JSON. For PAN-OS devices this route does not work: predicting it fails with "Device <name> OS pan_os not supported". Firewall changes go through the security-rule operations instead.

**Request body** (`text/plain`, required): `string`

```json
"interface Ethernet1\n shutdown"
```

**Response 204**: Staged.

### `validateChangeSetDeviceCommands` — `POST /networks/{networkId}/change-sets/{changeSetId}/devices/{deviceName}/commands?action=validate`

Check CLI commands for a device without staging them

**Request body** (`application/json`, required): `CommandValidationRequest`

```json
{
  "commands": "interface Ethernet1\n shutdown",
  "cursorLineNum": 1,
  "cursorColumnNum": 1
}
```

**Response 200**: Errors, suggestions and marks for the text.

`application/json` → `CommandValidationResponse`

```json
{
  "commandErrors": [],
  "suggestions": [],
  "textMarks": []
}
```

### `commitChangeSet` — `POST /networks/{networkId}/change-sets/{changeSetId}/commits`

Commit the staged changes

Commits the staged changes (the user's draft) as a new version. note is REQUIRED and must not be blank (400 otherwise). analysisMode (BEHAVIORAL or CONFIG_ONLY) records the change set's one-time Predict mode choice; it is honoured only when the organization property PREDICT_CONFIG_ONLY is on (default off) and is ignored while it is off. Choosing the mode already recorded is a plain commit; choosing a different mode than the recorded one is 409. A commit that records the choice starts its note with 'Chose behavioral Predict' or 'Chose config-only Predict'. With no draft and an existing head commit the call is a no-op that returns the head commit's metadata. Answers the commit metadata with the committer's id, name and time flattened. Requires MANAGE_CHANGE_SETS on the network and the organization property PREDICT_MODELING (source: ChangeSetController.commitChangeSet, ChangeSetService.commit).

| Param | In | Required | Type | Notes |
|---|---|---|---|---|
| `note` | query | yes | `string` | The commit note. Required; not blank. |
| `analysisMode` | query | no | enum[BEHAVIORAL|CONFIG_ONLY] | Chooses the change set's Predict mode. Honoured only when PREDICT_CONFIG_ONLY is on for the organization. |

**Response 200**: The commit.

`application/json` → `ChangeSetCommit`

```json
{
  "commitId": "9d2e1c0c8b3a4f5e6d7c8b9a0f1e2d3c4b5a6978",
  "note": "staged for review",
  "committedById": "11",
  "committedAt": "2026-09-11T10:02:00Z",
  "committedBy": "me@example.com"
}
```

**Response 400**: A missing or blank note.

**Response 409**: analysisMode differs from the mode the change set already uses.

### `getChangeSetHeadCommit` — `GET /networks/{networkId}/change-sets/{changeSetId}/commits/head`

The most recent commit, with its device changes

**Response 200**: The head commit.

`application/json` → `CommittedChangeSet`

```json
{
  "id": "CHG-101",
  "name": "Core uplink change",
  "networkId": "115",
  "snapshotId": "656",
  "commitId": "9d2e1c0c8b3a4f5e6d7c8b9a0f1e2d3c4b5a6978",
  "note": "staged for review"
}
```

**Response 404**: No commit yet.

### `listChangeSetCommits` — `GET /networks/{networkId}/change-sets/{changeSetId}/commits`

The commit history, each with its device changes

**Response 200**: The commits, newest first.

`application/json` → array of `CommittedChangeSet`

```json
[
  {
    "id": "CHG-101",
    "name": "Core uplink change",
    "networkId": "115",
    "snapshotId": "656",
    "commitId": "9d2e1c0c8b3a4f5e6d7c8b9a0f1e2d3c4b5a6978",
    "note": "staged for review",
    "committedAt": "2026-09-11T10:02:00Z",
    "deviceToChanges": {
      "dc-core": {
        "commands": "interface Ethernet1\n shutdown",
        "hasConfig": false,
        "hasSecurityRuleChanges": false
      }
    }
  }
]
```

### `predictChangeSet` — `POST /networks/{networkId}/change-sets/{changeSetId}?action=predict`

Run Predict, producing a snapshot of the network with the change applied

note is a required query parameter. Omitting it returns 400 "Required request parameter 'note' ... is not present". The returned snapshot is created, not yet processed; wait for it with the snapshots service.

| Param | In | Required | Type | Notes |
|---|---|---|---|---|
| `note` | query | yes | `string` | A note recorded on the predicted snapshot. Required. |

**Response 200**: The predicted snapshot, newly created.

`application/json` → `PredictedSnapshotMeta`

```json
{
  "id": "702",
  "note": "rehearsal 1",
  "createdAt": "2026-09-11T10:05:00Z",
  "processingTrigger": "PREDICT"
}
```

### `listPredictedSnapshots` — `GET /networks/{networkId}/change-sets/{changeSetId}/predicted-snapshots`

The snapshots Predict has produced for a change set

**Response 200**: The predicted snapshots, with processing state.

`application/json` → array of `SnapshotInfo`

```json
[
  {
    "id": "702",
    "state": "PROCESSED",
    "processingTrigger": "PREDICT",
    "createdAt": "2026-09-11T10:05:00Z"
  }
]
```

### `deleteChangeSet` — `DELETE /networks/{networkId}/change-sets/{changeSetId}`

Delete one change set

**Response 204**: Deleted.

### `deleteChangeSets` — `POST /networks/{networkId}/change-sets?action=delete`

Delete several change sets

The body key is changeSetIds. Forward rejects any other key, including ids, with "Non-empty 'changeSetIds' is required".

**Request body** (`application/json`, required): `ChangeSetIds`

```json
{
  "changeSetIds": [
    "CHG-101",
    "CHG-102"
  ]
}
```

**Response 204**: Deleted.

## Firewall (PAN-OS) rule edits

### `getSecurityRulesDiff` — `GET /networks/{networkId}/change-sets/{changeSetId}/devices/{deviceName}/security-rules-diff`

A firewall's rulebases and how the change set alters its rules

The definition objects under entries[].a and entries[].b are the shape the write operations take, so a rule can be read here and posted back.

**Response 200**: The rulebases and rule differences. Empty lists are omitted.

`application/json` → `SecurityRulesDiff`

```json
{
  "rulebases": [
    {
      "scopeId": "LOCAL",
      "rulebaseId": "PRIMARY",
      "editable": true,
      "displayName": "Local"
    }
  ],
  "entries": [
    {
      "diffType": "ADDED",
      "b": {
        "scopeId": "LOCAL",
        "rulebaseId": "PRIMARY",
        "rank": 1,
        "definition": {
          "name": "allow-dns",
          "id": "",
          "description": "",
          "enabled": true,
          "action": "PERMIT"
        }
      }
    }
  ]
}
```

### `addSecurityRule` — `POST /networks/{networkId}/change-sets/{changeSetId}/devices/{deviceName}/scopes/{scopeId}/rulebases/{rulebaseId}/security-rules`

Add a security rule to a firewall's rulebase

predecessor is the id of the rule to insert after; omit it to insert at the top. Only scope LOCAL and rulebase PRIMARY accept writes; others refuse with "Rulebase 'X' in scope 'Y' is not editable".

**Request body** (`application/json`, required): `NewSecurityRule`

```json
{
  "definition": {
    "name": "allow-dns",
    "enabled": true,
    "action": "PERMIT",
    "sourceZones": [
      "trust"
    ],
    "destinationZones": [
      "untrust"
    ]
  }
}
```

**Response 204**: Added.

### `updateSecurityRule` — `PATCH /networks/{networkId}/change-sets/{changeSetId}/devices/{deviceName}/scopes/{scopeId}/rulebases/{rulebaseId}/security-rules/{ruleId}`

Replace a rule's definition, or move it

Exactly one of definition or predecessor. A predecessor of null moves the rule to the top. Both or neither is refused.

**Request body** (`application/json`, required): `SecurityRulePatch`

```json
{
  "definition": {
    "name": "allow-dns",
    "enabled": false,
    "action": "PERMIT"
  }
}
```

**Response 204**: Updated.

### `deleteSecurityRule` — `DELETE /networks/{networkId}/change-sets/{changeSetId}/devices/{deviceName}/scopes/{scopeId}/rulebases/{rulebaseId}/security-rules/{ruleId}`

Delete a security rule

**Response 204**: Deleted.

## Results: connectivity and per-area counts

### `getSubnetConnectivityDiff` — `GET /diffs/{beforeSnapshotId}/{afterSnapshotId}/subnet-connectivity`

How site-to-site connectivity changed between two snapshots

**Response 200**: The connectivity comparison.

`application/json` → `SubnetConnectivityDiff`

```json
{
  "newlyConnectedSubnetPairs": 0,
  "newlyConnectedImpactedLocations": 0,
  "newlyConnectedDestinationSubnets": 0,
  "newlyIsolatedSubnetPairs": 14,
  "newlyIsolatedImpactedLocations": 7,
  "newlyIsolatedDestinationSubnets": 1,
  "modifiedSubnetPairs": 0,
  "modifiedImpactedLocations": 0,
  "evaluatedSubnetPairs": 39,
  "totalSubnetPairs": 39,
  "totalImpactedLocations": 7,
  "isPartialResult": false
}
```

### `getVulnerabilityDiffCounts` — `GET /diffs/{beforeSnapshotId}/{afterSnapshotId}/vulnerabilities/counts`

How vulnerability exposure changed between two snapshots

**Response 200**: The vulnerability comparison.

`application/json` → `VulnerabilityDiffCounts`

```json
{
  "newCveCount": 0
}
```

### `getRoutingLoopDiffCount` — `GET /diffs/{beforeSnapshotId}/{afterSnapshotId}/routing-loop/count`

How many routing loops the change introduces

**Response 200**: The routing-loop comparison.

`application/json` → `RoutingLoopDiffCount`

```json
{
  "count": 0,
  "complete": true
}
```

### `getDeviceDiffCount` — `GET /diffs/{beforeSnapshotId}/{afterSnapshotId}/devices?count`

How many devices were added or removed

**Response 200**: The count, and whether it is final.

`application/json` → `DiffCount`

```json
{
  "count": 3,
  "complete": true
}
```

### `getInterfaceDiffCount` — `GET /diffs/{beforeSnapshotId}/{afterSnapshotId}/interfaces?count`

How many devices have interface changes

**Response 200**: The count, and whether it is final.

`application/json` → `DiffCount`

```json
{
  "count": 3,
  "complete": true
}
```

### `getTopologyDiffCount` — `GET /diffs/{beforeSnapshotId}/{afterSnapshotId}/topology?count`

How many links changed

**Response 200**: The count, and whether it is final.

`application/json` → `DiffCount`

```json
{
  "count": 3,
  "complete": true
}
```

### `getL2DiffCount` — `GET /diffs/{beforeSnapshotId}/{afterSnapshotId}/l2?count`

How many VLANs changed

**Response 200**: The count, and whether it is final.

`application/json` → `DiffCount`

```json
{
  "count": 3,
  "complete": true
}
```

### `getAclDiffCount` — `GET /diffs/{beforeSnapshotId}/{afterSnapshotId}/acl?count`

How many devices have ACL changes

**Response 200**: The count, and whether it is final.

`application/json` → `DiffCount`

```json
{
  "count": 3,
  "complete": true
}
```

### `getNatDiffCount` — `GET /diffs/{beforeSnapshotId}/{afterSnapshotId}/nat?count`

How many devices have NAT changes

**Response 200**: The count, and whether it is final.

`application/json` → `DiffCount`

```json
{
  "count": 3,
  "complete": true
}
```

### `getArpDiffCount` — `GET /diffs/{beforeSnapshotId}/{afterSnapshotId}/arp?count`

How many devices have ARP changes

**Response 200**: The count, and whether it is final.

`application/json` → `DiffCount`

```json
{
  "count": 3,
  "complete": true
}
```

### `getMacDiffCount` — `GET /diffs/{beforeSnapshotId}/{afterSnapshotId}/mac?count`

How many devices have MAC table changes

**Response 200**: The count, and whether it is final.

`application/json` → `DiffCount`

```json
{
  "count": 3,
  "complete": true
}
```

### `getFileDiffCount` — `GET /diffs/{beforeSnapshotId}/{afterSnapshotId}/files?count`

How many devices have changed files, by file type

**Response 200**: The result.

`application/json` → `FileDiffCount`

```json
{
  "count": 3,
  "complete": true,
  "types": {
    "CONFIG": 2,
    "STATE": 1
  }
}
```

### `getCheckDiffCounts` — `GET /diffs/{beforeSnapshotId}/{afterSnapshotId}/checks?counts`

How many checks changed, by check type

**Response 200**: The result.

`application/json` → `CheckDiffCounts`

```json
{
  "Predefined": {
    "count": 2,
    "complete": true
  },
  "NQE": {
    "count": 1,
    "complete": false
  }
}
```

## Results: per-area detail

### `getDeviceDiffs` — `GET /diffs/{beforeSnapshotId}/{afterSnapshotId}/devices`

Devices added or removed

**Response 200**: The result.

`application/json` → array of `DeviceDiffEntry`

```json
[
  {
    "diffType": "ADDED",
    "a": null,
    "b": {
      "name": "new-sw",
      "vendor": "ARISTA",
      "deviceType": "SWITCH"
    }
  }
]
```

### `getFileDiffs` — `GET /diffs/{beforeSnapshotId}/{afterSnapshotId}/files`

Which collected files changed, per device

| Param | In | Required | Type | Notes |
|---|---|---|---|---|
| `type` | query | no | enum[CONFIG|STATE|CUSTOM] | Restrict to one file type; absent means all. |

**Response 200**: The result.

`application/json` → `CollectedFilesDiff`

```json
{
  "deviceInfos": [
    {
      "name": "dc-core",
      "deviceName": "dc-core",
      "files": [
        {
          "name": "dc-core,configuration.txt"
        }
      ],
      "hasConfigChange": true
    }
  ]
}
```

### `getDeviceInterfaceDiffs` — `GET /diffs/{beforeSnapshotId}/{afterSnapshotId}/interfaces/devices/{deviceName}`

One device's interface changes

**Response 200**: The result.

`application/json` → `DeviceInterfaceDiff`

```json
{
  "diffs": [
    {
      "diffType": "MODIFIED",
      "a": {
        "name": "Ethernet1",
        "isDown": false
      },
      "b": {
        "name": "Ethernet1",
        "isDown": true
      }
    }
  ],
  "stats": {
    "ADDED": 0,
    "DELETED": 0,
    "MODIFIED": 1,
    "UNCHANGED": 47
  }
}
```

### `getDeviceAclDiffs` — `GET /diffs/{beforeSnapshotId}/{afterSnapshotId}/acl/devices/{deviceName}`

One device's ACL entry changes

**Response 200**: The result.

`application/json` → `DeviceAclDiff`

```json
{
  "diffs": [
    {
      "diffType": "ADDED",
      "a": null,
      "b": {
        "name": "outbound",
        "action": "DENY",
        "matches": []
      }
    }
  ],
  "stats": {
    "ADDED": 1,
    "DELETED": 0,
    "MODIFIED": 0,
    "UNCHANGED": 12
  }
}
```

### `getCheckDiffs` — `GET /diffs/{beforeSnapshotId}/{afterSnapshotId}/checks`

Check results that changed, for the given check types

| Param | In | Required | Type | Notes |
|---|---|---|---|---|
| `type` | query | yes | array of enum[Isolation|Reachability|Existential|QueryStringBased|Predefined|NQE] | One or more check types. Blocks until computed. |

**Response 200**: The result.

`application/json` → `ChecksDiff`

```json
{
  "checks": [
    {
      "diffType": "MODIFIED",
      "a": {
        "id": "c1",
        "status": "PASS"
      },
      "b": {
        "id": "c1",
        "status": "FAIL"
      }
    }
  ]
}
```

### `computeNqeCheckDiffs` — `POST /diffs/{beforeSnapshotId}/{afterSnapshotId}/checks?action=compute&type=NQE`

Start computing diffs for the given NQE checks

Only active NQE checks are accepted. Returns nothing; read the statistics afterwards.

**Request body** (`application/json`, required): `NqeCheckDefinitions`

```json
{
  "definitions": [
    {
      "checkType": "NQE",
      "queryId": "FQ_1"
    }
  ]
}
```

**Response 200**: Accepted.

## Base snapshot hygiene

### `reprocessSnapshot` — `POST /snapshots/{snapshotId}?action=reprocess`

Request processing of a snapshot that was already invalidated

Refused unless the snapshot is invalidated ("Snapshot ID must be invalidated before reprocessing"). Needs view access and the org's minimum role for reprocessing.

**Response 200**: The snapshot's state before and after.

`application/json` → `SnapshotStateChange`

```json
{
  "previousState": "UNPROCESSED",
  "state": "UNPROCESSED"
}
```

## Schemas referenced above

### `NewChangeSet`

The create body. No networkId: it comes from the path, and Forward rejects unknown properties, so sending one fails with 400.

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | `string` | yes |  |
| `description` | `string` | no |  |
| `snapshotId` | `string` | yes | The base snapshot. |
| `tags` | array of `string` | no |  |

### `ChangeSet`

A change set with its device changes. Attribution fields sit flat on the object rather than nested.

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | `string` | no |  |
| `name` | `string` | no |  |
| `description` | `string` | no |  |
| `networkId` | `string` | no |  |
| `snapshotId` | `string` | no |  |
| `tags` | array of `string` | no |  |
| `deviceToChanges` | `object` | no |  |
| `analysisMode` | enum[BEHAVIORAL|CONFIG_ONLY] | no | The Predict mode chosen at a commit. Absent until one is chosen; the change set then runs BEHAVIORAL. |
| `createdById` | `string` | no |  |
| `createdBy` | `string` | no |  |
| `createdAt` | `string` | no |  |
| `updatedById` | `string` | no |  |
| `updatedBy` | `string` | no |  |
| `updatedAt` | `string` | no |  |

### `DeviceChanges`

What a change set does to one device.

| Field | Type | Required | Notes |
|---|---|---|---|
| `commands` | `string` | no | Staged CLI text, when the change is CLI. |
| `hasConfig` | `boolean` | no |  |
| `hasSecurityRuleChanges` | `boolean` | no |  |

### `ChangeSetSummary`

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | `string` | no |  |
| `description` | `string` | no |  |
| `networkId` | `string` | no |  |
| `snapshotId` | `string` | no |  |
| `tags` | array of `string` | no |  |
| `analysisMode` | enum[BEHAVIORAL|CONFIG_ONLY] | no | The Predict mode chosen at a commit. Absent until one is chosen; the change set then runs BEHAVIORAL. |
| `serviceNowChangeRequests` | array of `ServiceNowChangeRequestRef` | no | ServiceNow change requests linked to the change set, by number. Omitted when there are none. |

### `ServiceNowChangeRequestRef`

A change request linked to a change set (source: ServiceNowChangeRequestRef).

| Field | Type | Required | Notes |
|---|---|---|---|
| `number` | `string` | no |  |
| `sysId` | `string` | no |  |

### `ChangeSetDraftResponse`

| Field | Type | Required | Notes |
|---|---|---|---|
| `draft` | `any` | no | Null when nothing is staged. |

### `ChangeSetDraft`

The staged changes. savedAt is epoch milliseconds, unlike every other timestamp.

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | `string` | no |  |
| `name` | `string` | no |  |
| `networkId` | `string` | no |  |
| `snapshotId` | `string` | no |  |
| `deviceToChanges` | `object` | no |  |
| `savedAt` | `integer` | no |  |
| `hiddenDevices` | array of `string` | no |  |

### `CommandValidationRequest`

| Field | Type | Required | Notes |
|---|---|---|---|
| `commands` | `string` | yes | The command text, one command per line. |
| `cursorLineNum` | `integer` | yes | 1-based. |
| `cursorColumnNum` | `integer` | yes | 1-based. |

### `CommandValidationResponse`

| Field | Type | Required | Notes |
|---|---|---|---|
| `commandErrors` | array of `PredictCommandError` | no |  |
| `suggestions` | array of `object` | no |  |
| `textMarks` | array of `object` | no |  |

### `PredictCommandError`

A problem with one command line (source: CommandError). applied is set if and only if errorType is UNSUPPORTED.

| Field | Type | Required | Notes |
|---|---|---|---|
| `lineNumber` | `integer` | no | 1-based. |
| `startColumnNumber` | `integer` | no |  |
| `endColumnNumber` | `integer` | no |  |
| `errorType` | enum[INCOMPLETE|INVALID|UNSUPPORTED] | no | UNSUPPORTED is a valid command that Predict does not model. |
| `errorMsg` | `string` | no |  |
| `expectedArgs` | array of `object` | no |  |
| `applied` | `boolean` | no | Whether the line is kept in the predicted config. Only on UNSUPPORTED errors; INCOMPLETE and INVALID lines are never applied. |

### `ChangeSetCommit`

| Field | Type | Required | Notes |
|---|---|---|---|
| `commitId` | `string` | no |  |
| `note` | `string` | no |  |
| `committedById` | `string` | no |  |
| `committedBy` | `string` | no |  |
| `committedAt` | `string` | no |  |

### `CommittedChangeSet`

A commit with the change set's device changes as of that commit, flattened together.

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | `string` | no |  |
| `name` | `string` | no |  |
| `description` | `string` | no |  |
| `networkId` | `string` | no |  |
| `snapshotId` | `string` | no |  |
| `tags` | array of `string` | no |  |
| `deviceToChanges` | `object` | no |  |
| `commitId` | `string` | no |  |
| `note` | `string` | no |  |
| `committedById` | `string` | no |  |
| `committedBy` | `string` | no |  |
| `committedAt` | `string` | no |  |
| `hiddenDevices` | array of `string` | no |  |
| `createdById` | `string` | no |  |
| `createdAt` | `string` | no |  |
| `updatedById` | `string` | no |  |
| `updatedAt` | `string` | no |  |

### `PredictedSnapshotMeta`

The snapshot Predict created. Created, not yet processed.

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | `string` | no |  |
| `note` | `string` | no |  |
| `createdAt` | `string` | no |  |
| `processedAt` | `string` | no |  |
| `processingTrigger` | `string` | no |  |

### `SnapshotInfo`

| Field | Type | Required | Notes |
|---|---|---|---|
| `advancedReachabilityState` | `AdvancedReachabilityState` | no | State of advanced reachability computation for this Snapshot. |
| `createdAt` | `string` | no |  |
| `favoritedAt` | `string` | no | When this Snapshot was most recently marked as a favorite if it’s currently a favorite. A favorite Snapshot will never be automatically deleted, regardless of its network’s Snapshot retention policy. |
| `favoritedBy` | `string` | no | The username of the user who most recently marked this Snapshot as a favorite if it's currently a favorite and the user account hasn’t been deleted. |
| `favoritedByUserId` | `string` | no | The id of the user who most recently marked this Snapshot as a favorite if it’s currently a favorite. |
| `id` | `string` | no |  |
| `isDraft` | `boolean` | no |  |
| `note` | `string` | no |  |
| `parentSnapshotId` | `string` | no |  |
| `processedAt` | `string` | no |  |
| `processingTrigger` | enum[UNKNOWN|COLLECTION|IMPORT|REPROCESS|FORK] | no |  |
| `restoredAt` | `string` | no |  |
| `state` | `SnapshotState` | no |  |

### `AdvancedReachabilityState`

enum: `UNPROCESSED`, `PROCESSING`, `PROCESSED`, `FAILED`, `CANCELED`, `TIMED_OUT`

### `SnapshotState`

enum: `UNPACKING`, `UNPROCESSED`, `PROCESSING`, `PROCESSED`, `FAILED`, `CANCELED`, `TIMED_OUT`, `ARCHIVED`, `RESTORING`, `RESTORE_FAILED`

### `ChangeSetIds`

| Field | Type | Required | Notes |
|---|---|---|---|
| `changeSetIds` | array of `string` | yes |  |

### `SecurityRulesDiff`

| Field | Type | Required | Notes |
|---|---|---|---|
| `rulebases` | array of `RulebaseDescriptor` | no |  |
| `entries` | array of `SecurityRuleDiffEntry` | no |  |

### `RulebaseDescriptor`

| Field | Type | Required | Notes |
|---|---|---|---|
| `scopeId` | `string` | no |  |
| `rulebaseId` | `string` | no |  |
| `editable` | `boolean` | no |  |
| `displayName` | `string` | no |  |

### `SecurityRuleDiffEntry`

| Field | Type | Required | Notes |
|---|---|---|---|
| `diffType` | enum[ADDED|DELETED|MODIFIED|UNCHANGED] | no |  |
| `a` | `any` | no | The rule before. Absent when ADDED. |
| `b` | `any` | no | The rule after. Absent when DELETED. |

### `ScopedSecurityRule`

| Field | Type | Required | Notes |
|---|---|---|---|
| `scopeId` | `string` | no |  |
| `rulebaseId` | `string` | no |  |
| `rank` | `integer` | no |  |
| `definition` | `SecurityRuleDefinition` | no |  |
| `createdAt` | `string` | no | Rule lifecycle data, flattened. Each is omitted when unknown. |
| `lastModified` | `string` | no |  |
| `lastUsed` | `string` | no |  |
| `hitCount` | `integer` | no | Truncated hit count. |

### `SecurityRuleDefinition`

A firewall rule as Forward models it. name is required and must not be blank. The specification fields sit flat beside name, id and description rather than nested. Read one from the diff and mirror it; the full field list is address, user, application and service specifications plus zones, an action and a schedule.

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | `string` | yes |  |
| `id` | `string` | no |  |
| `description` | `string` | no |  |
| `enabled` | `boolean` | no |  |
| `action` | enum[DENY|DROP|PERMIT|RESET_CLIENT|RESET_CLIENT_AND_SERVER|RESET_SERVER] | no |  |
| `schedule` | `any` | no | Present when the rule is activated on a schedule. |
| `sourceZones` | array of `string` | no |  |
| `destinationZones` | array of `string` | no |  |
| `sourceAddresses` | `SecurityRuleAddressSpecification` | no |  |
| `destinationAddresses` | `SecurityRuleAddressSpecification` | no |  |
| `sourceUsers` | `SecurityRuleUserSpecification` | no |  |
| `applications` | `SecurityRuleApplicationSpecification` | no |  |
| `services` | `SecurityRuleServiceSpecification` | no |  |
| `urlObjects` | array of `SecurityObjectId` | no |  |

### `SecurityObjectId`

| Field | Type | Required | Notes |
|---|---|---|---|
| `scopeId` | `string` | no |  |
| `name` | `string` | no |  |
| `objectId` | `string` | no | The object's id when it differs from its name. Optional on a write; the reference then resolves by name. |

### `SecurityRuleAddressSpecification`

Which addresses a rule matches (source: AddressSpecification). Empty members with negated false match any address. Empty members are omitted.

| Field | Type | Required | Notes |
|---|---|---|---|
| `subnets` | array of `string` | no |  |
| `ipRanges` | array of `IpRange` | no |  |
| `objects` | array of `SecurityObjectId` | no |  |
| `groups` | array of `SecurityObjectId` | no |  |
| `dynamicAddressObjects` | array of `SecurityObjectId` | no |  |
| `regions` | array of `SecurityObjectId` | no |  |
| `negated` | `boolean` | no |  |

### `IpRange`

| Field | Type | Required | Notes |
|---|---|---|---|
| `start` | `string` | no |  |
| `end` | `string` | no |  |

### `SecurityRuleUserSpecification`

Which users a rule matches (source: UserSpecification).

| Field | Type | Required | Notes |
|---|---|---|---|
| `objects` | array of `SecurityObjectId` | no |  |
| `identityReferences` | array of `string` | no | Opaque, vendor-specific references to users or external groups. |

### `SecurityRuleApplicationSpecification`

Which applications a rule matches (source: ApplicationSpecification).

| Field | Type | Required | Notes |
|---|---|---|---|
| `groups` | array of `SecurityObjectId` | no |  |
| `dynamicGroups` | array of `SecurityObjectId` | no |  |
| `objects` | array of `SecurityObjectId` | no |  |

### `SecurityRuleServiceSpecification`

Which services a rule matches (source: ServiceSpecification).

| Field | Type | Required | Notes |
|---|---|---|---|
| `useApplicationDefaults` | `boolean` | no |  |
| `groups` | array of `SecurityObjectId` | no |  |
| `objects` | array of `SecurityObjectId` | no |  |

### `NewSecurityRule`

| Field | Type | Required | Notes |
|---|---|---|---|
| `predecessor` | `string` | no | Insert after this rule id. Omit to insert at the top. |
| `definition` | `SecurityRuleDefinition` | yes |  |

### `SecurityRulePatch`

Exactly one of definition or predecessor.

| Field | Type | Required | Notes |
|---|---|---|---|
| `definition` | `SecurityRuleDefinition` | no |  |
| `predecessor` | `string` | no | Move after this rule id; null moves to the top. |

### `SubnetConnectivityDiff`

How connectivity between subnets changed. Computed asynchronously, so an early request can return counts of zero with `isPartialResult` true; those zeros mean "not finished", not "nothing changed".

| Field | Type | Required | Notes |
|---|---|---|---|
| `newlyConnectedSubnetPairs` | `integer` | no |  |
| `newlyConnectedImpactedLocations` | `integer` | no |  |
| `newlyConnectedDestinationSubnets` | `integer` | no |  |
| `newlyIsolatedSubnetPairs` | `integer` | no |  |
| `newlyIsolatedImpactedLocations` | `integer` | no |  |
| `newlyIsolatedDestinationSubnets` | `integer` | no |  |
| `modifiedSubnetPairs` | `integer` | no |  |
| `modifiedImpactedLocations` | `integer` | no |  |
| `evaluatedSubnetPairs` | `integer` | no | How many pairs have been compared so far. Zero alongside a non-zero `totalSubnetPairs` means the computation has not started. |
| `totalSubnetPairs` | `integer` | no |  |
| `totalImpactedLocations` | `integer` | no |  |
| `isPartialResult` | `boolean` | no | True while the comparison is still being computed. |

### `VulnerabilityDiffCounts`

Vulnerability exposure introduced by the change. Fields beyond `newCveCount` appear only on deployments licensed for vulnerability analysis, so the model keeps them optional.

| Field | Type | Required | Notes |
|---|---|---|---|
| `newCveCount` | `integer` | no |  |
| `newExposedVulnerableDevicesCount` | `integer` | no |  |

### `RoutingLoopDiffCount`

| Field | Type | Required | Notes |
|---|---|---|---|
| `count` | `integer` | no |  |
| `complete` | `boolean` | no | False when the search stopped early, so `count` is a lower bound rather than a total. |

### `DiffCount`

A count, and whether the computation that produced it finished.

| Field | Type | Required | Notes |
|---|---|---|---|
| `count` | `integer` | no |  |
| `complete` | `boolean` | no | False means count is a lower bound so far. |

### `FileDiffCount`

| Field | Type | Required | Notes |
|---|---|---|---|
| `count` | `integer` | no |  |
| `complete` | `boolean` | no |  |
| `types` | `object` | no |  |

### `CheckDiffCounts`

Check type to its count. NQE appears only when non-zero or incomplete.

`object`

### `DeviceDiffEntry`

| Field | Type | Required | Notes |
|---|---|---|---|
| `diffType` | enum[ADDED|DELETED|MODIFIED|UNCHANGED] | no |  |
| `a` | `InventoryDeviceInfo` | no |  |
| `b` | `InventoryDeviceInfo` | no |  |

### `InventoryDeviceInfo`

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | `string` | no |  |
| `displayName` | `string` | no |  |
| `requestedName` | `string` | no |  |
| `vendor` | `string` | no |  |
| `os` | `string` | no |  |
| `osVersion` | `string` | no |  |
| `model` | `string` | no |  |
| `deviceType` | `string` | no |  |
| `tags` | array of `string` | no |  |
| `managementIps` | array of `string` | no |  |
| `configFile` | `string` | no |  |
| `collectedAt` | `string` | no |  |
| `processingError` | `string` | no |  |
| `collectionError` | `string` | no |  |
| `predictAffected` | `boolean` | no |  |

### `CollectedFilesDiff`

| Field | Type | Required | Notes |
|---|---|---|---|
| `deviceInfos` | array of `DeviceFilesDiff` | no |  |

### `DeviceFilesDiff`

The device's inventory fields sit flat beside files.

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | `string` | no |  |
| `deviceName` | `string` | no |  |
| `vendor` | `string` | no |  |
| `os` | `string` | no |  |
| `osVersion` | `string` | no |  |
| `model` | `string` | no |  |
| `deviceType` | `string` | no |  |
| `tags` | array of `string` | no |  |
| `managementIps` | array of `string` | no |  |
| `files` | array of `StoredFileInfo` | no |  |
| `hasConfigChange` | `boolean` | no |  |

### `StoredFileInfo`

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | `string` | no |  |
| `nameA` | `string` | no | Present only when the file's name differs in the before snapshot. |
| `command` | `string` | no | Present only for custom CLI files. |

### `DeviceInterfaceDiff`

| Field | Type | Required | Notes |
|---|---|---|---|
| `diffs` | array of `InterfaceDiffEntry` | no |  |
| `stats` | `DiffStats` | no |  |

### `InterfaceDiffEntry`

| Field | Type | Required | Notes |
|---|---|---|---|
| `diffType` | enum[ADDED|DELETED|MODIFIED|UNCHANGED] | no |  |
| `a` | `InterfaceInfo` | no |  |
| `b` | `InterfaceInfo` | no |  |

### `InterfaceInfo`

Empty and null fields are omitted; booleans are always present.

| Field | Type | Required | Notes |
|---|---|---|---|
| `type` | `string` | no |  |
| `name` | `string` | no |  |
| `description` | `string` | no |  |
| `mtu` | `integer` | no |  |
| `speedMbps` | `integer` | no |  |
| `fullDuplex` | `boolean` | no |  |
| `isDown` | `boolean` | no |  |
| `aliases` | array of `string` | no |  |
| `macAddress` | `string` | no |  |
| `ipAddresses` | array of `string` | no |  |
| `fhrpIpAddresses` | array of `string` | no |  |
| `vrfName` | `string` | no |  |
| `defaultVlan` | `string` | no |  |
| `accessVlan` | `string` | no |  |
| `voiceVlan` | `string` | no |  |
| `nativeVlan` | `string` | no |  |
| `vlan` | `string` | no |  |
| `innerVlan` | `string` | no |  |
| `trunkVlans` | array of `string` | no |  |
| `parentIface` | `string` | no |  |
| `memberPorts` | array of `string` | no |  |
| `configuredMemberPorts` | array of `string` | no |  |
| `inAclNames` | array of `string` | no |  |
| `outAclNames` | array of `string` | no |  |
| `srcAddresses` | array of `string` | no |  |
| `dstAddresses` | array of `string` | no |  |
| `securityZone` | `string` | no |  |
| `fileLines` | `FileLines` | no |  |

### `FileLines`

Stored file name to the line ranges the entry came from.

`object`

### `LineRange`

| Field | Type | Required | Notes |
|---|---|---|---|
| `end` | `integer` | no | The 0-based index of the last line in the range (inclusive). |
| `start` | `integer` | no | The 0-based index of the first line in the range. |

### `DiffStats`

Counts by kind of change. All four keys are always present.

| Field | Type | Required | Notes |
|---|---|---|---|
| `ADDED` | `integer` | no |  |
| `DELETED` | `integer` | no |  |
| `MODIFIED` | `integer` | no |  |
| `UNCHANGED` | `integer` | no |  |

### `DeviceAclDiff`

| Field | Type | Required | Notes |
|---|---|---|---|
| `diffs` | array of `AclDiffEntry` | no |  |
| `stats` | `DiffStats` | no |  |

### `AclDiffEntry`

| Field | Type | Required | Notes |
|---|---|---|---|
| `diffType` | enum[ADDED|DELETED|MODIFIED|UNCHANGED] | no |  |
| `a` | `AclDiffEntryInfo` | no |  |
| `b` | `AclDiffEntryInfo` | no |  |

### `AclDiffEntryInfo`

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | `string` | no |  |
| `action` | `string` | no |  |
| `aclContext` | `string` | no |  |
| `inputInterfaces` | array of `string` | no |  |
| `outputInterfaces` | array of `string` | no |  |
| `matches` | array of `FieldAndValues` | no |  |
| `negatedMatches` | array of `FieldAndValues` | no |  |
| `fileLines` | `FileLines` | no |  |

### `FieldAndValues`

| Field | Type | Required | Notes |
|---|---|---|---|
| `field` | `string` | no |  |
| `index` | `integer` | no |  |
| `values` | array of `any` | no |  |
| `negated` | `boolean` | no |  |

### `ChecksDiff`

| Field | Type | Required | Notes |
|---|---|---|---|
| `checks` | array of `CheckResultDiffEntry` | no |  |

### `CheckResultDiffEntry`

| Field | Type | Required | Notes |
|---|---|---|---|
| `diffType` | enum[ADDED|DELETED|MODIFIED|UNCHANGED|PROCESSING] | no |  |
| `a` | `NetworkCheckResult` | no |  |
| `b` | `NetworkCheckResult` | no |  |

### `NetworkCheckResult`

| Field | Type | Required | Notes |
|---|---|---|---|
| `createdAt` | `string` | no |  |
| `creator` | `string` | no | The username of the user who created the check, or null if unknown or if the account was deleted |
| `creatorId` | `string` | no |  |
| `definedAt` | `string` | no |  |
| `definition` | `CheckDefinition` | no |  |
| `description` | `string` | no |  |
| `editedAt` | `string` | no |  |
| `editor` | `string` | no | The username of the user who last edited the check, or null if unknown, if the account was deleted, or if the check hasn't been edited |
| `editorId` | `string` | no |  |
| `enabled` | `boolean` | no |  |
| `executedAt` | `string` | no |  |
| `executionDurationMillis` | `integer` | no |  |
| `id` | `string` | no |  |
| `name` | `string` | no |  |
| `note` | `string` | no |  |
| `numViolations` | `integer` | no |  |
| `perfMonitoringEnabled` | `boolean` | no | Whether performance monitoring is enabled for this check. Only supported for Existential intent checks. Performance monitoring must be independently enabled for each device along the check’s path(s). |
| `priority` | `CheckPriority` | no |  |
| `status` | `CheckStatus` | no | Status of this check. `REQUIRES_ADDITIONAL_SNAPSHOT_PROCESSING` indicates that this check requires additional Snapshot processing, such as advanced reachability computation, which can be triggered using [Trigger advanced reachability computation](https://docs.fwd.app/latest/api/network-snapshots/compute-advanced-reachability/). |
| `tags` | array of `string` | no |  |

### `CheckDefinition`

| Field | Type | Required | Notes |
|---|---|---|---|
| `checkType` | enum[Existential|Isolation|Reachability|QueryStringBased|Predefined|NQE] | yes |  |

### `ExistsCheck`

| Field | Type | Required | Notes |
|---|---|---|---|
| `checkType` | `string` | yes |  |
| `filters` | `PathQuery` | yes |  |
| `headerFieldsWithDefaults` | array of enum[ip_tos|tp_src|app_id|url|user_id] | no | The header fields for which a default value is assumed if no valid is specified in the `from` clause. Defaults to `["url"]`. |
| `noiseTypes` | array of `NoiseType` | no | The noise types that should be included in the search results. Defaults to `[]`. |
| `returnPath` | enum[ANY|SYMMETRIC] | no |  |

### `PathQuery`

| Field | Type | Required | Notes |
|---|---|---|---|
| `bypass` | array of `BypassFilter` | no |  |
| `chain` | array of `HopFilter` | no |  |
| `flowTypes` | array of enum[VALID|LOOP|POTENTIAL_LOOP|BLACKHOLE|DROPPED|INADMISSIBLE|UNREACHABLE|IGNORED|UNDELIVERED] | no |  |
| `forwardingTypes` | array of enum[L2|L3|DIRECT_L2|FABRIC_PATH|VXLAN|MCAST_IP|DIRECT_IP|MPLS|L2_PBR|L3_PBR] | no |  |
| `from` | `EndpointFilter` | no |  |
| `mode` | `string` | no |  |
| `to` | `EndpointFilter` | no |  |

### `BypassFilter`

| Field | Type | Required | Notes |
|---|---|---|---|
| `type` | enum[DeviceFilter|DeviceAliasFilter] | yes |  |

### `DeviceFilter`

A filter that matches one device by name or all devices (using value "*")

| Field | Type | Required | Notes |
|---|---|---|---|
| `type` | `string` | yes |  |
| `value` | `string` | yes |  |

### `DeviceAliasFilter`

A filter that matches any device in a Device Group

| Field | Type | Required | Notes |
|---|---|---|---|
| `type` | `string` | yes |  |
| `value` | `string` | yes | A Device Group name |

### `HopFilter`

| Field | Type | Required | Notes |
|---|---|---|---|
| `headers` | array of `PacketHeaderFilter` | no |  |
| `location` | `HopLocationFilter` | yes |  |
| `transitType` | enum[through|ingress|egress] | yes |  |

### `PacketHeaderFilter`

| Field | Type | Required | Notes |
|---|---|---|---|
| `type` | enum[PacketFilter|PacketAliasFilter|NotFilter] | yes |  |

### `PacketFilter`

A filter that matches a specific packet header value or range of values

| Field | Type | Required | Notes |
|---|---|---|---|
| `type` | `string` | yes |  |
| `values` | `object` | yes |  |

### `PacketAliasFilter`

A filter that matches any combination of packet header values that satisfy the criteria in a
Header Set

| Field | Type | Required | Notes |
|---|---|---|---|
| `direction` | enum[src|dst] | no |  |
| `type` | `string` | yes |  |
| `value` | `string` | yes | A Packet Header Set name |

### `NotFilter`

A filter that inverts the filter in its `clause`

| Field | Type | Required | Notes |
|---|---|---|---|
| `clause` | `object` | no | A filter that would be permitted in the place of this NotFilter. Cannot be another NotFilter. |
| `type` | `string` | yes |  |

### `HopLocationFilter`

| Field | Type | Required | Notes |
|---|---|---|---|
| `type` | enum[DeviceFilter|InterfaceFilter|TunnelInterfaceFilter|SecurityZoneFilter|DeviceVrfFilter|DeviceAliasFilter|InterfaceAliasFilter|VrfFilter|NotFilter] | yes |  |

### `InterfaceFilter`

A filter that matches one device interface by name

| Field | Type | Required | Notes |
|---|---|---|---|
| `type` | `string` | yes |  |
| `value` | `string` | yes |  |

### `TunnelInterfaceFilter`

A filter that matches one tunnel interface by name and has underlay semantics in the `from` or `to` property
of a `PathQuery`

| Field | Type | Required | Notes |
|---|---|---|---|
| `type` | `string` | yes |  |
| `value` | `string` | yes |  |

### `SecurityZoneFilter`

A filter that matches one security zone on a firewall by name and can be used in the `from` or `to` property
of a `PathQuery`

| Field | Type | Required | Notes |
|---|---|---|---|
| `device` | `string` | yes |  |
| `type` | `string` | yes |  |
| `value` | `string` | yes |  |

### `DeviceVrfFilter`

A filter that matches a device’s interfaces by VRF name

| Field | Type | Required | Notes |
|---|---|---|---|
| `device` | `string` | yes |  |
| `type` | `string` | yes |  |
| `vrf` | `string` | yes |  |

### `InterfaceAliasFilter`

A filter that matches any device interface in an Interface Group

| Field | Type | Required | Notes |
|---|---|---|---|
| `type` | `string` | yes |  |
| `value` | `string` | yes | An Interface Group name |

### `VrfFilter`

A filter that matches device interfaces by VRF name

| Field | Type | Required | Notes |
|---|---|---|---|
| `type` | `string` | yes |  |
| `value` | `string` | yes | A VRF name. Use `DeviceVrfFilter` instead for a (device, VRF) pair. |

### `EndpointFilter`

| Field | Type | Required | Notes |
|---|---|---|---|
| `headers` | array of `PacketHeaderFilter` | no |  |
| `location` | `LocationFilter` | yes |  |
| `logicalNetwork` | `string` | no |  |

### `LocationFilter`

| Field | Type | Required | Notes |
|---|---|---|---|
| `type` | enum[HostFilter|DeviceFilter|InterfaceFilter|TunnelInterfaceFilter|SecurityZoneFilter|DeviceVrfFilter|HostAliasFilter|DeviceAliasFilter|InterfaceAliasFilter|SubnetLocationFilter|VrfFilter|NotFilter] | yes |  |

### `HostFilter`

A filter that matches hosts by hostname, IP subnet address, or MAC address

| Field | Type | Required | Notes |
|---|---|---|---|
| `type` | `string` | yes |  |
| `value` | `string` | yes |  |

### `HostAliasFilter`

A filter that matches any host in a Host Group

| Field | Type | Required | Notes |
|---|---|---|---|
| `type` | `string` | yes |  |
| `value` | `string` | yes | A Host Group name |

### `SubnetLocationFilter`

A filter that matches a specific IP address or IP subnet address at a specific location in the network. If
the IP address is a multicast group address, this filter matches on the locations of the corresponding
multicast Rendezvous Points (RPs) in the network.

| Field | Type | Required | Notes |
|---|---|---|---|
| `device` | `string` | no | The device at which the subnet is located |
| `host` | `string` | no | The hostname that resolves to the IP in `value` (metadata only; not used directly) |
| `type` | `string` | yes |  |
| `value` | `string` | yes |  |

### `NoiseType`

enum: `WRONG_GATEWAY`, `INADMISSIBLE`, `NETWORK_OR_BROADCAST_ADDRESS`, `TO_L2_INTERFACE_MAC`, `GLEAN`

### `IsolationCheck`

| Field | Type | Required | Notes |
|---|---|---|---|
| `checkType` | `string` | yes |  |
| `filters` | `PathQuery` | yes |  |
| `headerFieldsWithDefaults` | array of enum[ip_tos|tp_src|app_id|url|user_id] | no | The header fields for which a default value is assumed if no valid is specified in the `from` clause. Defaults to `["url"]`. |
| `noiseTypes` | array of `NoiseType` | no | The noise types that should be included in the search results. Defaults to `[]`. |

### `ReachabilityCheck`

| Field | Type | Required | Notes |
|---|---|---|---|
| `checkType` | `string` | yes |  |
| `filters` | `PathQuery` | yes |  |
| `headerFieldsWithDefaults` | array of enum[ip_tos|tp_src|app_id|url|user_id] | no | The header fields for which a default value is assumed if no valid is specified in the `from` clause. Defaults to `["url"]`. |
| `noiseTypes` | array of `NoiseType` | no | The noise types that should be included in the search results. Defaults to `[]`. |

### `QueryStringCheck`

| Field | Type | Required | Notes |
|---|---|---|---|
| `checkType` | `string` | yes |  |
| `forRequest` | `string` | no |  |
| `value` | `string` | no |  |

### `PredefinedCheck`

| Field | Type | Required | Notes |
|---|---|---|---|
| `checkType` | `string` | yes |  |
| `params` | `object` | no |  |
| `predefinedCheckType` | `PredefinedCheckType` | no |  |

### `PredefinedCheckType`

enum: `BGP_NEIGHBOR_ADJACENCY`, `BGP_ROUTER_ID`, `BGP_ROUTE_CONSISTENCY`, `BGP_VPC_PARAMETER_CONSISTENCY`, `EBGP_SELECTION_OVER_IBGP`, `BGP_COMMUNITY_LIST`, `NEXT_HOP_REACHABILITY`, `NO_LOOP`, `SHORTEST_PATH`, `IP_UNIQUENESS`, `HOSTNAME_CONSISTENCY`, `HOSTNAME_UNIQUENESS`, `VLAN_CONSISTENCY`, `MTU_CONSISTENCY`, `PORT_CHANNEL_CONSISTENCY`, `DUPLEX_CONSISTENCY`, `LINK_SPEED_CONSISTENCY`, `SOFTWARE_VERSION_CONSISTENCY`, `TRUNK_INTERFACE_WHITELIST`, `VLAN_EXISTENCE`, `LEARNED_MAC_CONSISTENCY`, `FHRP_PEERING`, `VPC_PARAMETER_CONSISTENCY`, `VPC_INTERFACE_PARAMETER_CONSISTENCY`, `VPC_DEDICATED_KEEPALIVE_LINK`, `VPC_ROLE_PRIORITY`, `SSH_RSA_KEY_LENGTH`, `VPC_STP_PRIORITY`, `VPC_MST_REGION_CONSISTENCY`

### `NqeCheck`

| Field | Type | Required | Notes |
|---|---|---|---|
| `checkType` | `string` | yes |  |
| `params` | `object` | no | Values for each parameter declared in the query. The parameter values are specified as a JSON object, with one property per parameter, where the property name matches the parameter name. The property value is the JSON representation of the parameter value. The JSON representation of a parameter value depends on the type of the value. For values of type `String`, `IpAddress`, `IpSubnet`, and `MacAddress`, the JSON representation is a JSON string. For values of type `Number`, the value is an integral JSON number. For values of type `Boolean`, the JSON representation is true or false. For values of type `List<T>`, the JSON representation is an array of JSON values, each of which is a JSON representation for type `T`. For record values, the JSON representation is a JSON object with corresponding properties whose values are JSON representations corresponding to the properties' types. For enumerations, the JSON representation is a JSON string with the enum constant name. For `oneOf` values that contain data, the JSON representation is a JSON object with two properties, `alternative` and `value`, where the `alternative` property contains the alternative's name (a string) and the `value` property is a JSON representation of the data associated with this alternative. |
| `queryId` | `string` | yes |  |

### `CheckPriority`

enum: `NOT_SET`, `LOW`, `MEDIUM`, `HIGH`

### `CheckStatus`

enum: `NONE`, `PASS`, `FAIL`, `ERROR`, `TIMEOUT`, `PROCESSING`, `REQUIRES_ADDITIONAL_SNAPSHOT_PROCESSING`

### `NqeCheckDefinitions`

| Field | Type | Required | Notes |
|---|---|---|---|
| `definitions` | array of `object` | yes |  |

### `SnapshotStateChange`

A snapshot's processing state before and after a request.

| Field | Type | Required | Notes |
|---|---|---|---|
| `previousState` | `SnapshotState` | no |  |
| `state` | `SnapshotState` | no |  |
