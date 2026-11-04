// Package predict holds the pure rules for Forward Predict results: the
// three-valued verdict (PLAN.md D4, docs/predict-endpoints.md) and input
// guards that stop known-bad requests before they reach Forward.
package predict

import (
	"fmt"
	"strings"

	"github.com/forward-mcp/internal/domain/entities"
)

// Verdict derives PASS / FAIL / UNKNOWN and the reasons from a result's
// observations. UNKNOWN is never a pass: the M3 workflow treats it as not
// approved.
func Verdict(r *entities.PredictResult) (entities.PredictVerdict, []string) {
	var reasons []string
	unknown := func(f string, a ...any) { reasons = append(reasons, "UNKNOWN: "+fmt.Sprintf(f, a...)) }
	fail := func(f string, a ...any) { reasons = append(reasons, "FAIL: "+fmt.Sprintf(f, a...)) }

	switch {
	case r.PredictedSnapshotID == "":
		unknown("no predicted snapshot; run predict_run_and_wait first")
	case entities.SnapshotFailedStates[r.SnapshotState]:
		unknown("predicted snapshot %s ended in state %s", r.PredictedSnapshotID, r.SnapshotState)
	case r.SnapshotState != entities.SnapshotProcessed:
		unknown("predicted snapshot %s is still %s", r.PredictedSnapshotID, orUnprocessed(r.SnapshotState))
	}

	if c := r.Connectivity; c != nil {
		if c.IsPartialResult {
			unknown("connectivity comparison still partial (%d of %d subnet pairs evaluated)", c.EvaluatedSubnetPairs, c.TotalSubnetPairs)
		} else if c.TotalSubnetPairs == 0 {
			unknown("connectivity compared nothing (total_subnet_pairs = 0): the network has no locations defined, so this is not an all-clear")
		} else if c.NewlyIsolatedSubnetPairs > 0 {
			fail("%d subnet pair(s) newly isolated across %d location(s)", c.NewlyIsolatedSubnetPairs, c.NewlyIsolatedLocations)
		}
	} else if r.PredictedSnapshotID != "" {
		unknown("connectivity comparison not available")
	}

	for area, ac := range r.Areas {
		if !ac.Complete {
			unknown("%s diff incomplete (count so far %d)", area, ac.Count)
		}
	}
	if r.RoutingLoops != nil {
		if !r.RoutingLoops.Complete {
			unknown("routing-loop search incomplete")
		} else if r.RoutingLoops.Count > 0 {
			fail("%d new routing loop(s)", r.RoutingLoops.Count)
		}
	}
	for kind, ac := range r.Checks {
		if !ac.Complete {
			unknown("%s check diff incomplete", kind)
		} else if ac.Count > 0 {
			fail("%d %s check(s) changed", ac.Count, kind)
		}
	}
	if r.NewCVEs != nil && *r.NewCVEs > 0 {
		fail("%d newly exposed CVE(s)", *r.NewCVEs)
	}

	switch {
	case hasPrefix(reasons, "UNKNOWN: "):
		return entities.PredictUnknown, reasons
	case hasPrefix(reasons, "FAIL: "):
		return entities.PredictFail, reasons
	default:
		return entities.PredictPass, []string{"PASS: no newly isolated subnet pairs, no new routing loops, no changed checks; all comparisons complete"}
	}
}

func hasPrefix(list []string, p string) bool {
	for _, s := range list {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func orUnprocessed(s string) string {
	if s == "" {
		return "unprocessed"
	}
	return s
}

// ValidateStageEdits rejects edits Forward is known to mishandle, before
// anything is staged (docs/predict-endpoints.md, caveats).
func ValidateStageEdits(platform string, e entities.StageEdits) error {
	if strings.TrimSpace(e.Device) == "" {
		return fmt.Errorf("device is required")
	}
	if e.CLI == "" && len(e.FirewallRules) == 0 {
		return fmt.Errorf("provide cli text or firewall_rules for %s", e.Device)
	}
	if e.CLI != "" && len(e.FirewallRules) > 0 {
		return fmt.Errorf("provide either cli or firewall_rules for %s, not both", e.Device)
	}
	if e.CLI != "" && isPanOS(platform) {
		return fmt.Errorf("device %s runs PAN-OS: CLI 'set' text fails prediction; use firewall_rules", e.Device)
	}
	if len(e.CLI) > 2*1024*1024 {
		return fmt.Errorf("cli text for %s exceeds Forward's 2 MiB limit", e.Device)
	}
	for i, r := range e.FirewallRules {
		switch strings.ToLower(r.Action) {
		case "add":
			if len(r.Definition) == 0 {
				return fmt.Errorf("firewall_rules[%d]: add needs a definition", i)
			}
		case "update":
			if r.RuleID == "" || len(r.Definition) == 0 {
				return fmt.Errorf("firewall_rules[%d]: update needs rule_id and definition", i)
			}
		case "delete":
			if r.RuleID == "" {
				return fmt.Errorf("firewall_rules[%d]: delete needs rule_id", i)
			}
		default:
			return fmt.Errorf("firewall_rules[%d]: action must be add, update or delete", i)
		}
		if a := strings.ToUpper(extractAction(r.Definition)); a == "RESET_CLIENT" || a == "RESET_SERVER" || a == "RESET_CLIENT_AND_SERVER" {
			return fmt.Errorf("firewall_rules[%d]: action %s stages but breaks prediction (empty rulebase); use DENY", i, a)
		}
	}
	return nil
}

func isPanOS(platform string) bool {
	p := strings.ToLower(strings.ReplaceAll(platform, "-", "_"))
	return strings.Contains(p, "pan_os") || strings.Contains(p, "panos") || strings.Contains(p, "palo")
}

// extractAction pulls "action" from a rule definition without a schema.
func extractAction(def []byte) string {
	s := string(def)
	i := strings.Index(s, `"action"`)
	if i < 0 {
		return ""
	}
	rest := s[i+len(`"action"`):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	rest = rest[j+1:]
	k := strings.Index(rest, `"`)
	if k < 0 {
		return ""
	}
	return rest[:k]
}
