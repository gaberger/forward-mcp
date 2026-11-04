package eval

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/forward-mcp/internal/domain/entities"
)

type fakeRerun struct {
	rows     map[string][]map[string]any // key: ref@snapshot
	outcomes map[string]string
	err      error
}

func (f *fakeRerun) RerunQuery(_ context.Context, ref, snap, _ string, _ map[string]any) ([]map[string]any, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.rows[ref+"@"+snap], nil
}

func (f *fakeRerun) RerunPathSearch(_ context.Context, ref, snap, _ string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.outcomes[ref+"@"+snap], nil
}

var bgpCase = entities.EvalCase{
	ID: "bgp-idle-01", NetworkID: "162112", SnapshotBefore: "snap-1",
	Input:           entities.EvalInput{Question: "why can't 10.1.1.10 reach 10.2.2.20?"},
	ExpectedVerdict: "cause",
	Gold: []entities.GoldEvidence{
		{Kind: "nqe", Ref: "certified:bgp-peerings-not-established@1", SnapshotID: "snap-1",
			ExpectedRows: []map[string]any{{"device": "router-1", "neighborAddress": "192.0.2.2", "state": "IDLE"}},
			KeyColumns:   []string{"device", "neighborAddress", "state"}},
		{Kind: "path_search", Ref: "path:10.1.1.10->10.2.2.20", SnapshotID: "snap-1", ExpectedOutcome: "DROPPED"},
	},
}

func goodAnswer() entities.AgentAnswer {
	return entities.AgentAnswer{
		Summary: "The BGP session from router-1 to 192.0.2.2 is IDLE [0]. Traffic from 10.1.1.10 to 10.2.2.20 is dropped at router-1 [1]. Check the neighbor configuration.",
		Evidence: []entities.Evidence{
			{Kind: "nqe", Ref: "certified:bgp-peerings-not-established@1", SnapshotID: "snap-1", Certified: true},
			{Kind: "path_search", Ref: "path:10.1.1.10->10.2.2.20", SnapshotID: "snap-1"},
		},
		Confidence: 0.9, Verdict: "cause", ToolCalls: 3, Tokens: 1200, LatencyMS: 4200,
	}
}

func goodRerun() *fakeRerun {
	return &fakeRerun{
		rows: map[string][]map[string]any{
			"certified:bgp-peerings-not-established@1@snap-1": {
				{"device": "router-1", "vrf": "default", "neighborAddress": "192.0.2.2", "remoteAs": float64(65002), "state": "IDLE"},
				{"device": "router-1", "vrf": "default", "neighborAddress": "192.0.2.6", "remoteAs": float64(65003), "state": "ACTIVE"},
			},
		},
		outcomes: map[string]string{"path:10.1.1.10->10.2.2.20@snap-1": "DROPPED"},
	}
}

func TestGradeAccurateAnswer(t *testing.T) {
	r := New(goodRerun()).Grade(context.Background(), bgpCase, goodAnswer())
	if !r.Accurate || !r.VerdictCorrect || r.EvidenceMatched != 2 || r.EvidenceTotal != 2 {
		t.Fatalf("expected accurate: %+v", r)
	}
	if len(r.UncitedFacts) != 0 || len(r.UnreproducedCitations) != 0 {
		t.Errorf("unexpected uncited/unreproduced: %v %v", r.UncitedFacts, r.UnreproducedCitations)
	}
	if r.ToolCalls != 3 || r.Tokens != 1200 || r.LatencyMS != 4200 {
		t.Errorf("telemetry not passed through: %+v", r)
	}
}

func TestGradeMissingCitation(t *testing.T) {
	a := goodAnswer()
	a.Evidence = a.Evidence[:1] // path search not cited
	a.Summary = "The BGP session from router-1 to 192.0.2.2 is IDLE [0]. Traffic from 10.1.1.10 to 10.2.2.20 is dropped at router-1."
	r := New(goodRerun()).Grade(context.Background(), bgpCase, a)
	if r.Accurate || r.EvidenceMatched != 1 {
		t.Fatalf("expected inaccurate with 1 match: %+v", r)
	}
	if !r.EvidenceChecks[1].Cited == false || r.EvidenceChecks[1].Cited {
		t.Errorf("second gold item must be uncited: %+v", r.EvidenceChecks[1])
	}
	if len(r.UncitedFacts) != 1 || !strings.Contains(r.UncitedFacts[0], "10.2.2.20") {
		t.Errorf("the dropped-traffic sentence has no citation: %v", r.UncitedFacts)
	}
}

func TestGradeCitationThatDoesNotReproduce(t *testing.T) {
	rr := goodRerun()
	rr.rows["certified:bgp-peerings-not-established@1@snap-1"] = []map[string]any{
		{"device": "router-1", "neighborAddress": "192.0.2.2", "state": "ESTABLISHED"},
	}
	r := New(rr).Grade(context.Background(), bgpCase, goodAnswer())
	if r.Accurate || r.EvidenceMatched != 1 || len(r.UnreproducedCitations) != 1 {
		t.Fatalf("expected one unreproduced citation: %+v", r)
	}
	if !strings.Contains(r.UnreproducedCitations[0], "missing") {
		t.Errorf("explanation should name the missing row: %s", r.UnreproducedCitations[0])
	}
}

func TestGradeWrongVerdict(t *testing.T) {
	a := goodAnswer()
	a.Verdict = "no_network_issue"
	r := New(goodRerun()).Grade(context.Background(), bgpCase, a)
	if r.Accurate || r.VerdictCorrect || r.EvidenceMatched != 2 {
		t.Fatalf("verdict wrong must fail accuracy even with evidence matched: %+v", r)
	}
}

func TestGradeRerunErrorAndNoBackend(t *testing.T) {
	r := New(&fakeRerun{err: errors.New("forward unreachable")}).Grade(context.Background(), bgpCase, goodAnswer())
	if r.EvidenceMatched != 0 || !strings.Contains(r.UnreproducedCitations[0], "rerun failed") {
		t.Errorf("rerun errors must be reported: %+v", r)
	}
	r = New(nil).Grade(context.Background(), bgpCase, goodAnswer())
	if r.EvidenceChecks[0].Rerun || r.EvidenceMatched != 0 {
		t.Errorf("no backend must not match: %+v", r)
	}
}

func TestCompareRows(t *testing.T) {
	exp := []map[string]any{{"name": "R1", "count": float64(3)}}
	got := []map[string]any{{"name": " r1 ", "count": 3, "extra": "x"}, {"name": "r2", "count": float64(1)}}
	if ok, _ := CompareRows(exp, got, []string{"name", "count"}, false); !ok {
		t.Error("subset with normalised values should match")
	}
	if ok, why := CompareRows(exp, got, []string{"name", "count"}, true); ok || !strings.Contains(why, "unexpected") {
		t.Errorf("exact should fail on extra rows: %v %s", ok, why)
	}
	if ok, _ := CompareRows(exp, got, nil, false); ok {
		t.Error("without key columns the extra column makes rows differ")
	}
	if ok, why := CompareRows(nil, got, nil, false); !ok || !strings.Contains(why, "0 row") {
		t.Errorf("empty expectation matches: %v %s", ok, why)
	}
}

func TestRowKeyNormalisation(t *testing.T) {
	a := RowKey(map[string]any{"b": "X", "a": float64(65002)}, nil)
	b := RowKey(map[string]any{"a": 65002, "b": " x"}, nil)
	if a != b {
		t.Errorf("keys differ: %q vs %q", a, b)
	}
	if RowKey(map[string]any{"a": nil}, nil) != "a=null" {
		t.Error("nil renders as null")
	}
}

func TestUncitedFactsHeuristics(t *testing.T) {
	a := entities.AgentAnswer{
		Summary:  "Interface Gi0/0/1 on edge-rtr-01 is down. VLAN 200 is missing on sw-2 [0]. I recommend opening a ticket. AS 65002 flaps every hour [9].",
		Evidence: []entities.Evidence{{Kind: "nqe", Ref: "nqe:FQ_x", SnapshotID: "s"}},
	}
	got := UncitedFacts(a)
	if len(got) != 2 {
		t.Fatalf("expected 2 uncited facts (interface sentence, out-of-range [9]), got %v", got)
	}
	if !strings.Contains(got[0], "Gi0/0/1") || !strings.Contains(got[1], "AS 65002") {
		t.Errorf("wrong sentences flagged: %v", got)
	}

	// Explicit facts take precedence over heuristics.
	b := entities.AgentAnswer{
		Summary:  "ignored",
		Evidence: []entities.Evidence{{Kind: "nqe", Ref: "r", SnapshotID: "s"}},
		Facts:    []entities.Fact{{Text: "cited", Evidence: []int{0}}, {Text: "uncited", Evidence: nil}, {Text: "bad index", Evidence: []int{5}}},
	}
	got = UncitedFacts(b)
	if len(got) != 2 || got[0] != "uncited" || got[1] != "bad index" {
		t.Errorf("explicit facts: %v", got)
	}
}

func TestSummarize(t *testing.T) {
	g := New(goodRerun())
	bad := goodAnswer()
	bad.Verdict = "no_network_issue"
	s := Summarize([]entities.GradeReport{
		g.Grade(context.Background(), bgpCase, goodAnswer()),
		g.Grade(context.Background(), bgpCase, bad),
	})
	if s.Cases != 2 || s.Accurate != 1 || s.Accuracy != 0.5 || s.EvidenceMatched != 4 || s.ToolCalls != 6 {
		t.Errorf("summary: %+v", s)
	}
}
