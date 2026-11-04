package ports

import (
	"context"

	"github.com/forward-mcp/internal/domain/entities"
)

// NQERunner executes an NQE library query by id against a snapshot. It is
// the narrow slice of the Forward client the certified-query use case needs;
// adapters/forwardclient implements it over forward.ClientInterface.
type NQERunner interface {
	RunQueryByID(ctx context.Context, params entities.NQEQueryParams) (*entities.NQERunResult, error)
}
