package entities

import "encoding/json"

// Eval types (PLAN.md E.1/E.2). A case replays one incident or question
// against recorded snapshots; the grader compares what the agent cited
// against gold by re-running the cited evidence, so grading is
// deterministic. Only the prose summary may be LLM-graded, elsewhere.

// EvalCase is one replayable case.
type EvalCase struct {
	ID        string `json:"id"`
	NetworkID string `json:"network_id"`
	// SnapshotBefore is the primary snapshot; SnapshotAfter is set for
	// "what changed" cases.
	SnapshotBefore string    `json:"snapshot_before"`
	SnapshotAfter  string    `json:"snapshot_after,omitempty"`
	Input          EvalInput `json:"input"`
	// Gold lists the evidence a correct answer must be grounded in. Every
	// gold item must be matched by a cited evidence item that, when re-run,
	// yields the expected result.
	Gold []GoldEvidence `json:"gold"`
	// ExpectedVerdict is "cause", "no_network_issue" or
	// "insufficient_evidence" (M2 output contract).
	ExpectedVerdict string `json:"expected_verdict"`
	Notes           string `json:"notes,omitempty"`
}

// EvalInput is the question or alert the agent receives.
type EvalInput struct {
	Question string          `json:"question,omitempty"`
	Alert    json.RawMessage `json:"alert,omitempty"`
}

// GoldEvidence is one expected piece of evidence.
type GoldEvidence struct {
	// Kind is "nqe" or "path_search" (same vocabulary as Evidence.Kind).
	Kind string `json:"kind"`
	// Ref is the evidence ref the agent is expected to cite
	// ("certified:<id>@<v>", "nqe:FQ_...", "path:<src>-><dst>").
	Ref string `json:"ref"`
	// SnapshotID the evidence must come from.
	SnapshotID string `json:"snapshot_id"`
	// Params for a query ref (bound parameters), if any.
	Params map[string]any `json:"params,omitempty"`
	// ExpectedRows for nqe kinds: rows the re-run must contain (set
	// semantics after normalisation; extra rows in the result are allowed
	// when Exact is false).
	ExpectedRows []map[string]any `json:"expected_rows,omitempty"`
	// Exact requires the re-run rows to equal ExpectedRows exactly.
	Exact bool `json:"exact,omitempty"`
	// KeyColumns restrict row comparison to these columns (default: all).
	KeyColumns []string `json:"key_columns,omitempty"`
	// ExpectedOutcome for path_search kinds: "DELIVERED", "DROPPED",
	// "BLACKHOLE", "LOOP", "UNREACHABLE"...
	ExpectedOutcome string `json:"expected_outcome,omitempty"`
}

// AgentAnswer is the M2 output contract as the grader consumes it.
type AgentAnswer struct {
	Summary    string     `json:"summary"`
	Evidence   []Evidence `json:"evidence"`
	Confidence float64    `json:"confidence"`
	NextStep   string     `json:"next_step,omitempty"`
	Verdict    string     `json:"verdict"`
	// Telemetry reported by the runner, passed through to the report.
	ToolCalls int   `json:"tool_calls,omitempty"`
	Tokens    int   `json:"tokens,omitempty"`
	LatencyMS int64 `json:"latency_ms,omitempty"`
	// Facts are the agent's atomic factual claims, each with the indexes
	// of Evidence entries that support it. Optional: when absent, the
	// grader extracts candidate facts from Summary heuristically.
	Facts []Fact `json:"facts,omitempty"`
}

// Fact is one factual claim and its citations (indexes into Evidence).
type Fact struct {
	Text     string `json:"text"`
	Evidence []int  `json:"evidence"`
}

// EvidenceCheck is the grader's result for one gold item.
type EvidenceCheck struct {
	Gold        GoldEvidence `json:"gold"`
	Cited       bool         `json:"cited"`
	CitedIndex  int          `json:"cited_index"`
	Rerun       bool         `json:"rerun"`
	Matched     bool         `json:"matched"`
	Explanation string       `json:"explanation"`
}

// GradeReport is the per-case report (PLAN.md E.2).
type GradeReport struct {
	CaseID          string          `json:"case_id"`
	VerdictExpected string          `json:"verdict_expected"`
	VerdictGot      string          `json:"verdict_got"`
	VerdictCorrect  bool            `json:"verdict_correct"`
	EvidenceChecks  []EvidenceCheck `json:"evidence_checks"`
	EvidenceMatched int             `json:"evidence_matched"`
	EvidenceTotal   int             `json:"evidence_total"`
	// Accurate is true when the verdict is correct and every gold item
	// matched. This is the number reported as "accuracy" across cases.
	Accurate bool `json:"accurate"`
	// UncitedFacts are factual claims with no supporting evidence.
	UncitedFacts []string `json:"uncited_facts"`
	// UncitedEvidence are cited items that did not reproduce on re-run
	// (a citation that does not hold is worse than none).
	UnreproducedCitations []string `json:"unreproduced_citations"`
	ToolCalls             int      `json:"tool_calls"`
	Tokens                int      `json:"tokens"`
	LatencyMS             int64    `json:"latency_ms"`
}
