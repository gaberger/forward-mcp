package ports

import (
	"context"

	"github.com/forward-mcp/internal/domain/entities"
)

// callerKey is the context key under which the authenticated Caller travels.
type callerKey struct{}

// WithCaller returns a context carrying the caller.
func WithCaller(ctx context.Context, c entities.Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// CallerFromContext returns the caller stored in ctx. When none is present
// (stdio mode, tests) it returns entities.LocalCaller() so that callers
// never have to special-case an empty identity.
func CallerFromContext(ctx context.Context) entities.Caller {
	if ctx == nil {
		return entities.LocalCaller()
	}
	if c, ok := ctx.Value(callerKey{}).(entities.Caller); ok && c.Subject != "" {
		return c
	}
	return entities.LocalCaller()
}

// IdentityVerifier turns a bearer credential into a Caller. The JWT adapter
// (M1c) implements it; the remote transport calls it per request.
type IdentityVerifier interface {
	Verify(ctx context.Context, bearer string) (entities.Caller, error)
}
