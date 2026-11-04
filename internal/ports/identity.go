package ports

import (
	"context"
	"encoding/base64"
	"strings"

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

// basicBearerPrefix marks an HTTP Basic credential (API key and secret)
// carried through the bearer-token path. The remote transport rewrites an
// incoming "Authorization: Basic ..." header into this form so one
// verifier chain handles JWT, opaque-token and key/secret callers. Sending
// it directly as a bearer is equivalent to sending Basic: the same key and
// secret are checked.
const basicBearerPrefix = "fmcp-basic."

// BasicBearer encodes an API key and secret as a bearer value.
func BasicBearer(key, secret string) string {
	return basicBearerPrefix + base64.RawURLEncoding.EncodeToString([]byte(key+":"+secret))
}

// ParseBasicBearer decodes a value made by BasicBearer.
func ParseBasicBearer(bearer string) (key, secret string, ok bool) {
	rest, found := strings.CutPrefix(bearer, basicBearerPrefix)
	if !found || rest == "" {
		return "", "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(rest)
	if err != nil {
		return "", "", false
	}
	key, secret, ok = strings.Cut(string(raw), ":")
	if !ok || key == "" {
		return "", "", false
	}
	return key, secret, true
}
