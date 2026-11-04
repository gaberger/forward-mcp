package ports

import "context"

// EvidenceRerun re-executes a cited evidence ref against a snapshot so the
// grader can compare the live answer with gold. The remote-mode adapter
// talks to forward-mcp as the "eval-grader" identity; tests use fixtures.
type EvidenceRerun interface {
	// RerunQuery runs an nqe ref ("certified:<id>@<v>" or "nqe:FQ_...")
	// and returns its rows.
	RerunQuery(ctx context.Context, ref, snapshotID, networkID string, params map[string]any) ([]map[string]any, error)
	// RerunPathSearch runs a path ref ("path:<src>-><dst>") and returns the
	// forwarding outcome of the best path (e.g. "DELIVERED").
	RerunPathSearch(ctx context.Context, ref, snapshotID, networkID string) (string, error)
}
