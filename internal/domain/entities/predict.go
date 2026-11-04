package entities

import "encoding/json"

// Forward Predict types (PLAN.md M1b). Field names follow
// docs/predict-endpoints.md; the verdict rules live in
// domain/services/predict.

// ChangeSet is a Predict change set against a base snapshot.
type ChangeSet struct {
	ID             string   `json:"id"`
	NetworkID      string   `json:"network_id"`
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	BaseSnapshotID string   `json:"base_snapshot_id"`
	Tags           []string `json:"tags,omitempty"`
	CreatedAt      string   `json:"created_at,omitempty"`
	CreatedBy      string   `json:"created_by,omitempty"`
	// Owner is the forward-mcp caller that created it (not a Forward field).
	Owner string `json:"owner,omitempty"`
}

// StageEdits is what predict_stage_edits accepts for one device: either
// CLI text or structured firewall rule edits (PAN-OS cannot take CLI).
type StageEdits struct {
	Device        string             `json:"device"`
	CLI           string             `json:"cli,omitempty"`
	FirewallRules []FirewallRuleEdit `json:"firewall_rules,omitempty"`
}

// FirewallRuleEdit adds, updates or deletes one security rule. Definition
// is passed to Forward verbatim (see getSecurityRulesDiff for the shape of
// existing rules). Only scope LOCAL / rulebase PRIMARY accept writes.
type FirewallRuleEdit struct {
	Action     string          `json:"action"` // add|update|delete
	ScopeID    string          `json:"scope_id,omitempty"`
	RulebaseID string          `json:"rulebase_id,omitempty"`
	RuleID     string          `json:"rule_id,omitempty"`
	Definition json.RawMessage `json:"definition,omitempty"`
}

// CommandValidation is the advisory result of validating CLI text.
type CommandValidation struct {
	Valid  bool     `json:"valid"`
	Errors []string `json:"errors,omitempty"`
	// Advisory notes the caveat that an empty result is not proof (PAN-OS
	// returns nothing; EOS misses some valid commands).
	Advisory string `json:"advisory,omitempty"`
}

// PredictedSnapshot is the snapshot a prediction produced.
type PredictedSnapshot struct {
	ID                string `json:"id"`
	Note              string `json:"note,omitempty"`
	CreatedAt         string `json:"created_at,omitempty"`
	ProcessedAt       string `json:"processed_at,omitempty"`
	ProcessingTrigger string `json:"processing_trigger,omitempty"`
	State             string `json:"state,omitempty"`
}

// Snapshot processing states (docs/predict-endpoints.md, Semantics §6).
const (
	SnapshotProcessed = "PROCESSED"
)

// SnapshotFailedStates never resolve to PROCESSED.
var SnapshotFailedStates = map[string]bool{"FAILED": true, "CANCELED": true, "TIMED_OUT": true, "RESTORE_FAILED": true}

// Connectivity is the subnet-connectivity diff between base and predicted.
type Connectivity struct {
	NewlyConnectedSubnetPairs int  `json:"newly_connected_subnet_pairs"`
	NewlyIsolatedSubnetPairs  int  `json:"newly_isolated_subnet_pairs"`
	NewlyIsolatedLocations    int  `json:"newly_isolated_impacted_locations"`
	ModifiedSubnetPairs       int  `json:"modified_subnet_pairs"`
	EvaluatedSubnetPairs      int  `json:"evaluated_subnet_pairs"`
	TotalSubnetPairs          int  `json:"total_subnet_pairs"`
	TotalImpactedLocations    int  `json:"total_impacted_locations"`
	IsPartialResult           bool `json:"is_partial_result"`
}

// AreaCount is a per-area diff count with its completeness flag.
type AreaCount struct {
	Count    int  `json:"count"`
	Complete bool `json:"complete"`
}

// Diff areas forward-mcp collects counts for.
var DiffAreas = []string{"devices", "interfaces", "topology", "l2", "acl", "nat", "arp", "mac", "files"}

// PredictVerdict is three-valued on purpose (PLAN.md D4).
type PredictVerdict string

const (
	PredictPass    PredictVerdict = "PASS"
	PredictFail    PredictVerdict = "FAIL"
	PredictUnknown PredictVerdict = "UNKNOWN"
)

// PredictResult is predict_get_results' structured output.
type PredictResult struct {
	ChangeSetID         string               `json:"change_set_id"`
	NetworkID           string               `json:"network_id"`
	BaseSnapshotID      string               `json:"base_snapshot_id"`
	PredictedSnapshotID string               `json:"predicted_snapshot_id,omitempty"`
	SnapshotState       string               `json:"snapshot_state,omitempty"`
	Connectivity        *Connectivity        `json:"connectivity,omitempty"`
	Areas               map[string]AreaCount `json:"areas,omitempty"`
	RoutingLoops        *AreaCount           `json:"routing_loops,omitempty"`
	Checks              map[string]AreaCount `json:"checks,omitempty"`
	NewCVEs             *int                 `json:"new_cve_count,omitempty"`
	Verdict             PredictVerdict       `json:"verdict"`
	Reasons             []string             `json:"reasons"`
	// Evidence cites the predicted snapshot so the result can be graded.
	Evidence Evidence `json:"evidence"`
}
