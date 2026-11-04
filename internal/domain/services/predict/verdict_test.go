package predict

import (
	"strings"
	"testing"

	"github.com/forward-mcp/internal/domain/entities"
)

func base() *entities.PredictResult {
	return &entities.PredictResult{
		PredictedSnapshotID: "702", SnapshotState: "PROCESSED",
		Connectivity: &entities.Connectivity{TotalSubnetPairs: 39, EvaluatedSubnetPairs: 39},
		Areas:        map[string]entities.AreaCount{"acl": {Count: 1, Complete: true}},
		RoutingLoops: &entities.AreaCount{Count: 0, Complete: true},
		Checks:       map[string]entities.AreaCount{"Predefined": {Count: 0, Complete: true}},
	}
}

func TestVerdictTable(t *testing.T) {
	zero := 0
	two := 2
	cases := []struct {
		name   string
		mutate func(*entities.PredictResult)
		want   entities.PredictVerdict
		reason string
	}{
		{"pass", func(*entities.PredictResult) {}, entities.PredictPass, "PASS"},
		{"no snapshot", func(r *entities.PredictResult) { r.PredictedSnapshotID = "" }, entities.PredictUnknown, "no predicted snapshot"},
		{"failed snapshot", func(r *entities.PredictResult) { r.SnapshotState = "FAILED" }, entities.PredictUnknown, "state FAILED"},
		{"still processing", func(r *entities.PredictResult) { r.SnapshotState = "PROCESSING" }, entities.PredictUnknown, "still PROCESSING"},
		{"partial connectivity", func(r *entities.PredictResult) { r.Connectivity.IsPartialResult = true }, entities.PredictUnknown, "still partial"},
		{"nothing compared", func(r *entities.PredictResult) { r.Connectivity.TotalSubnetPairs = 0 }, entities.PredictUnknown, "compared nothing"},
		{"no connectivity", func(r *entities.PredictResult) { r.Connectivity = nil }, entities.PredictUnknown, "not available"},
		{"isolated pairs", func(r *entities.PredictResult) {
			r.Connectivity.NewlyIsolatedSubnetPairs = 14
			r.Connectivity.NewlyIsolatedLocations = 7
		}, entities.PredictFail, "14 subnet pair(s) newly isolated"},
		{"incomplete area", func(r *entities.PredictResult) { r.Areas["acl"] = entities.AreaCount{Count: 3, Complete: false} }, entities.PredictUnknown, "acl diff incomplete"},
		{"routing loop", func(r *entities.PredictResult) { r.RoutingLoops.Count = 1 }, entities.PredictFail, "routing loop"},
		{"check changed", func(r *entities.PredictResult) { r.Checks["NQE"] = entities.AreaCount{Count: 2, Complete: true} }, entities.PredictFail, "2 NQE check(s)"},
		{"cve", func(r *entities.PredictResult) { r.NewCVEs = &two }, entities.PredictFail, "2 newly exposed CVE"},
		{"cve zero", func(r *entities.PredictResult) { r.NewCVEs = &zero }, entities.PredictPass, "PASS"},
		// UNKNOWN beats FAIL: an incomplete picture is never a confident fail either.
		{"partial and isolated", func(r *entities.PredictResult) {
			r.Connectivity.IsPartialResult = true
			r.RoutingLoops.Count = 1
		}, entities.PredictUnknown, "still partial"},
	}
	for _, c := range cases {
		r := base()
		c.mutate(r)
		v, reasons := Verdict(r)
		if v != c.want {
			t.Errorf("%s: want %s got %s (%v)", c.name, c.want, v, reasons)
		}
		if !strings.Contains(strings.Join(reasons, "\n"), c.reason) {
			t.Errorf("%s: reasons %v should mention %q", c.name, reasons, c.reason)
		}
	}
}

func TestValidateStageEdits(t *testing.T) {
	ok := entities.StageEdits{Device: "r1", CLI: "interface Ethernet1\n shutdown"}
	if err := ValidateStageEdits("EOS", ok); err != nil {
		t.Fatal(err)
	}
	bad := []struct {
		name     string
		platform string
		e        entities.StageEdits
		want     string
	}{
		{"no device", "EOS", entities.StageEdits{CLI: "x"}, "device is required"},
		{"nothing", "EOS", entities.StageEdits{Device: "r1"}, "provide cli text or firewall_rules"},
		{"both", "EOS", entities.StageEdits{Device: "r1", CLI: "x", FirewallRules: []entities.FirewallRuleEdit{{Action: "delete", RuleID: "1"}}}, "not both"},
		{"panos cli", "PAN_OS", entities.StageEdits{Device: "fw1", CLI: "set x"}, "PAN-OS"},
		{"palo cli", "Palo Alto", entities.StageEdits{Device: "fw1", CLI: "set x"}, "PAN-OS"},
		{"bad action", "PAN_OS", entities.StageEdits{Device: "fw1", FirewallRules: []entities.FirewallRuleEdit{{Action: "replace"}}}, "must be add, update or delete"},
		{"add no def", "PAN_OS", entities.StageEdits{Device: "fw1", FirewallRules: []entities.FirewallRuleEdit{{Action: "add"}}}, "needs a definition"},
		{"update no id", "PAN_OS", entities.StageEdits{Device: "fw1", FirewallRules: []entities.FirewallRuleEdit{{Action: "update", Definition: []byte(`{}`)}}}, "needs rule_id"},
		{"reset action", "PAN_OS", entities.StageEdits{Device: "fw1", FirewallRules: []entities.FirewallRuleEdit{{Action: "add", Definition: []byte(`{"name":"r","action":"RESET_CLIENT"}`)}}}, "breaks prediction"},
	}
	for _, c := range bad {
		err := ValidateStageEdits(c.platform, c.e)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
	}
	good := entities.StageEdits{Device: "fw1", FirewallRules: []entities.FirewallRuleEdit{{Action: "add", Definition: []byte(`{"name":"r","action":"DENY"}`)}}}
	if err := ValidateStageEdits("PAN_OS", good); err != nil {
		t.Errorf("DENY rule should pass: %v", err)
	}
}
