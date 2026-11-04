// Package eval is the deterministic grader (PLAN.md E.2). It decides
// whether an agent answer is grounded: every gold evidence item must be
// cited, and every citation must reproduce when re-run. Prose quality is
// not judged here.
package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/ports"
)

// Grader compares answers with cases using a rerun port.
type Grader struct {
	rerun ports.EvidenceRerun
}

// New builds a grader.
func New(rerun ports.EvidenceRerun) *Grader { return &Grader{rerun: rerun} }

// Grade produces the report for one case.
func (g *Grader) Grade(ctx context.Context, c entities.EvalCase, a entities.AgentAnswer) entities.GradeReport {
	r := entities.GradeReport{
		CaseID:                c.ID,
		VerdictExpected:       c.ExpectedVerdict,
		VerdictGot:            a.Verdict,
		VerdictCorrect:        strings.EqualFold(strings.TrimSpace(c.ExpectedVerdict), strings.TrimSpace(a.Verdict)),
		EvidenceTotal:         len(c.Gold),
		UncitedFacts:          []string{},
		UnreproducedCitations: []string{},
		ToolCalls:             a.ToolCalls,
		Tokens:                a.Tokens,
		LatencyMS:             a.LatencyMS,
	}

	for _, gold := range c.Gold {
		check := entities.EvidenceCheck{Gold: gold, CitedIndex: -1}
		idx := findCitation(a.Evidence, gold)
		if idx < 0 {
			check.Explanation = fmt.Sprintf("no cited evidence matches %s %s @ %s", gold.Kind, gold.Ref, gold.SnapshotID)
			r.EvidenceChecks = append(r.EvidenceChecks, check)
			continue
		}
		check.Cited, check.CitedIndex = true, idx
		matched, explanation, reran := g.verify(ctx, c, gold)
		check.Rerun, check.Matched, check.Explanation = reran, matched, explanation
		if matched {
			r.EvidenceMatched++
		} else {
			r.UnreproducedCitations = append(r.UnreproducedCitations, fmt.Sprintf("%s @ %s: %s", gold.Ref, gold.SnapshotID, explanation))
		}
		r.EvidenceChecks = append(r.EvidenceChecks, check)
	}

	r.UncitedFacts = UncitedFacts(a)
	r.Accurate = r.VerdictCorrect && r.EvidenceMatched == r.EvidenceTotal
	return r
}

// findCitation returns the index of the cited evidence that names the same
// kind, ref and snapshot as gold, or -1.
func findCitation(cited []entities.Evidence, gold entities.GoldEvidence) int {
	for i, e := range cited {
		if e.Kind == gold.Kind && normalizeRef(e.Ref) == normalizeRef(gold.Ref) && e.SnapshotID == gold.SnapshotID {
			return i
		}
	}
	return -1
}

func normalizeRef(ref string) string {
	return strings.ToLower(strings.TrimSpace(ref))
}

// verify re-runs the gold evidence and compares with its expectation.
func (g *Grader) verify(ctx context.Context, c entities.EvalCase, gold entities.GoldEvidence) (matched bool, explanation string, reran bool) {
	if g.rerun == nil {
		return false, "no rerun backend configured", false
	}
	switch gold.Kind {
	case entities.EvidenceKindNQE:
		rows, err := g.rerun.RerunQuery(ctx, gold.Ref, gold.SnapshotID, c.NetworkID, gold.Params)
		if err != nil {
			return false, "rerun failed: " + err.Error(), true
		}
		ok, why := CompareRows(gold.ExpectedRows, rows, gold.KeyColumns, gold.Exact)
		return ok, why, true
	case entities.EvidenceKindPathSearch:
		outcome, err := g.rerun.RerunPathSearch(ctx, gold.Ref, gold.SnapshotID, c.NetworkID)
		if err != nil {
			return false, "rerun failed: " + err.Error(), true
		}
		if strings.EqualFold(outcome, gold.ExpectedOutcome) {
			return true, "outcome " + outcome, true
		}
		return false, fmt.Sprintf("outcome %s, expected %s", outcome, gold.ExpectedOutcome), true
	default:
		return false, "unknown evidence kind " + gold.Kind, false
	}
}

// CompareRows checks that every expected row appears in got (set
// semantics on the normalised key columns). With exact, got must contain
// nothing else. The explanation names the first missing/extra row.
func CompareRows(expected, got []map[string]any, keyColumns []string, exact bool) (bool, string) {
	want := map[string]int{}
	for _, r := range expected {
		want[RowKey(r, keyColumns)]++
	}
	have := map[string]int{}
	for _, r := range got {
		have[RowKey(r, keyColumns)]++
	}
	var missing, extra []string
	for k, n := range want {
		if have[k] < n {
			missing = append(missing, k)
		}
	}
	if exact {
		for k, n := range have {
			if want[k] < n {
				extra = append(extra, k)
			}
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	switch {
	case len(missing) > 0:
		return false, fmt.Sprintf("%d expected row(s) missing, first: %s", len(missing), missing[0])
	case len(extra) > 0:
		return false, fmt.Sprintf("%d unexpected row(s), first: %s", len(extra), extra[0])
	}
	return true, fmt.Sprintf("%d row(s) matched", len(expected))
}

// RowKey renders a row as a canonical string: selected (or all) columns,
// sorted by name, values JSON-encoded with numbers normalised so 65002
// and 65002.0 compare equal and strings trimmed/lower-cased.
func RowKey(row map[string]any, keyColumns []string) string {
	cols := keyColumns
	if len(cols) == 0 {
		cols = make([]string, 0, len(row))
		for k := range row {
			cols = append(cols, k)
		}
	}
	cols = append([]string(nil), cols...)
	sort.Strings(cols)
	var b strings.Builder
	for i, c := range cols {
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteString(c)
		b.WriteByte('=')
		b.WriteString(normalizeValue(row[c]))
	}
	return b.String()
}

func normalizeValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return strings.ToLower(strings.TrimSpace(x))
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%g", x)
	case int:
		return fmt.Sprintf("%d", x)
	case int64:
		return fmt.Sprintf("%d", x)
	case bool:
		return fmt.Sprintf("%t", x)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

// Fact-detection heuristics for answers without an explicit Facts list.
// A sentence counts as a network fact when it names an IP, a prefix, a
// device-like token, an interface, an AS number, a VLAN or a protocol
// state. Such a sentence must contain an evidence marker [n].
var (
	sentenceSplit = regexp.MustCompile(`(?m)[.!?]\s+|\n+`)
	citationMark  = regexp.MustCompile(`\[(\d+)\]`)
	// Recommendations and questions are not claims about the network.
	notAFact    = regexp.MustCompile(`(?i)^(suggested next step|next step|recommend|i recommend|we recommend|consider|check|verify|try|you should|please|open a ticket|escalate|\?$)`)
	factSignals = []*regexp.Regexp{
		regexp.MustCompile(`\b\d{1,3}(\.\d{1,3}){3}(/\d{1,2})?\b`),                                      // IPv4 / prefix
		regexp.MustCompile(`(?i)\b[0-9a-f]{1,4}(:[0-9a-f]{0,4}){2,7}\b`),                                // IPv6-ish
		regexp.MustCompile(`(?i)\b(as|asn)\s?\d{2,10}\b`),                                               // AS number
		regexp.MustCompile(`(?i)\bvlan\s?\d+\b`),                                                        // VLAN
		regexp.MustCompile(`(?i)\b(gi|te|et|eth|xe|ge|fa|hu|po|ae|lo|vlan|tunnel|bond)\S*\d+(/\d+)*\b`), // interface
		regexp.MustCompile(`(?i)\b(idle|active|established|down|up|flapping|dropped|delivered|blackhole|denied|permitted)\b`),
		regexp.MustCompile(`(?i)\b[a-z]+[-_][a-z0-9-]*\d+\b`), // device-like name (edge-rtr-01)
	}
)

// UncitedFacts returns factual claims that carry no evidence citation. If
// the answer lists Facts explicitly, each fact's evidence indexes must be
// non-empty and in range; otherwise sentences of Summary are scanned.
func UncitedFacts(a entities.AgentAnswer) []string {
	out := []string{}
	if len(a.Facts) > 0 {
		for _, f := range a.Facts {
			if !hasValidCitation(f.Evidence, len(a.Evidence)) {
				out = append(out, strings.TrimSpace(f.Text))
			}
		}
		return out
	}
	for _, s := range sentenceSplit.Split(a.Summary, -1) {
		s = strings.TrimSpace(s)
		if s == "" || !looksLikeFact(s) {
			continue
		}
		var idx []int
		for _, m := range citationMark.FindAllStringSubmatch(s, -1) {
			var n int
			_, _ = fmt.Sscanf(m[1], "%d", &n)
			idx = append(idx, n)
		}
		if !hasValidCitation(idx, len(a.Evidence)) {
			out = append(out, s)
		}
	}
	return out
}

func hasValidCitation(idx []int, n int) bool {
	for _, i := range idx {
		if i >= 0 && i < n {
			return true
		}
	}
	return false
}

func looksLikeFact(s string) bool {
	if notAFact.MatchString(s) || strings.HasSuffix(s, "?") {
		return false
	}
	for _, re := range factSignals {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// Summary aggregates reports across cases (PLAN.md E.2 report).
type Summary struct {
	Cases             int     `json:"cases"`
	Accurate          int     `json:"accurate"`
	Accuracy          float64 `json:"accuracy"`
	VerdictCorrect    int     `json:"verdict_correct"`
	EvidenceMatched   int     `json:"evidence_matched"`
	EvidenceTotal     int     `json:"evidence_total"`
	UncitedFactCount  int     `json:"uncited_fact_count"`
	UnreproducedCount int     `json:"unreproduced_citation_count"`
	ToolCalls         int     `json:"tool_calls"`
	Tokens            int     `json:"tokens"`
	LatencyMSTotal    int64   `json:"latency_ms_total"`
}

// Summarize folds reports into a Summary.
func Summarize(reports []entities.GradeReport) Summary {
	var s Summary
	for _, r := range reports {
		s.Cases++
		if r.Accurate {
			s.Accurate++
		}
		if r.VerdictCorrect {
			s.VerdictCorrect++
		}
		s.EvidenceMatched += r.EvidenceMatched
		s.EvidenceTotal += r.EvidenceTotal
		s.UncitedFactCount += len(r.UncitedFacts)
		s.UnreproducedCount += len(r.UnreproducedCitations)
		s.ToolCalls += r.ToolCalls
		s.Tokens += r.Tokens
		s.LatencyMSTotal += r.LatencyMS
	}
	if s.Cases > 0 {
		s.Accuracy = float64(s.Accurate) / float64(s.Cases)
	}
	return s
}
