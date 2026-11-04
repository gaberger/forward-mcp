// Package forwardclient adapts the existing forward.ClientInterface to the
// narrow ports the use cases need. It is the seam through which the legacy
// client is migrated behind ports (PLAN.md hex rule).
package forwardclient

import (
	"context"

	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/forward"
	"github.com/forward-mcp/internal/ports"
)

// NQERunner implements ports.NQERunner over forward.ClientInterface.
type NQERunner struct {
	client forward.ClientInterface
}

var _ ports.NQERunner = (*NQERunner)(nil)

// NewNQERunner wraps client.
func NewNQERunner(client forward.ClientInterface) *NQERunner {
	return &NQERunner{client: client}
}

// RunQueryByID implements ports.NQERunner. The legacy client has no context
// parameter yet; ctx is honoured only for early cancellation.
func (r *NQERunner) RunQueryByID(ctx context.Context, p entities.NQEQueryParams) (*entities.NQERunResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fp := &forward.NQEQueryParams{
		NetworkID:  p.NetworkID,
		SnapshotID: p.SnapshotID,
		QueryID:    p.QueryID,
		Parameters: p.Parameters,
	}
	if p.Options != nil && p.Options.MaxResults > 0 {
		fp.Options = &forward.NQEQueryOptions{Limit: p.Options.MaxResults}
	}
	res, err := r.client.RunNQEQueryByID(fp)
	if err != nil {
		return nil, err
	}
	return &entities.NQERunResult{SnapshotID: res.SnapshotID, Items: res.Items, TotalNumItems: res.TotalNumItems}, nil
}
