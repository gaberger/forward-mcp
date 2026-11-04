package ports

import (
	"context"

	"github.com/forward-mcp/internal/domain/entities"
)

// CertifiedLibraryLoader reads a certified NQE library from wherever it is
// stored (the in-repo spec/certified directory in M1a).
type CertifiedLibraryLoader interface {
	Load(ctx context.Context) (*entities.CertifiedLibrary, error)
}

// FixtureReader returns the recorded result for a certified entry, used by
// unit tests and by the eval grader when no live instance is available.
type FixtureReader interface {
	ReadFixture(ctx context.Context, q entities.CertifiedQuery) (*entities.NQERunResult, error)
}
