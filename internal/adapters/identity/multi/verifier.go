// Package multi routes each bearer to the verifier for its scheme, so
// forward-mcp remote mode can accept JWTs, opaque API tokens and API
// key/secret (HTTP Basic) at the same time. Routing is by the credential's
// shape, never by trial: a JWT that fails JWT verification is not retried
// as an opaque token.
package multi

import (
	"context"
	"errors"
	"strings"

	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/ports"
)

// Options name the verifier per scheme; nil disables that scheme.
type Options struct {
	JWT   ports.IdentityVerifier
	Token ports.IdentityVerifier
	Basic ports.IdentityVerifier
}

// Verifier implements ports.IdentityVerifier.
type Verifier struct{ opts Options }

var _ ports.IdentityVerifier = (*Verifier)(nil)

// ErrSchemeDisabled is returned for a credential whose scheme is off.
var ErrSchemeDisabled = errors.New("authentication scheme not enabled on this server")

// New requires at least one scheme.
func New(o Options) (*Verifier, error) {
	if o.JWT == nil && o.Token == nil && o.Basic == nil {
		return nil, errors.New("multi: enable at least one of jwt, token, basic")
	}
	return &Verifier{opts: o}, nil
}

// Schemes lists enabled schemes in routing order.
func (v *Verifier) Schemes() []string {
	var out []string
	if v.opts.JWT != nil {
		out = append(out, "jwt")
	}
	if v.opts.Token != nil {
		out = append(out, "token")
	}
	if v.opts.Basic != nil {
		out = append(out, "basic")
	}
	return out
}

// Verify routes by shape: a ports.BasicBearer value goes to Basic, a
// three-segment value starting "eyJ" goes to JWT, anything else to Token.
func (v *Verifier) Verify(ctx context.Context, bearer string) (entities.Caller, error) {
	var target ports.IdentityVerifier
	switch {
	case isBasic(bearer):
		target = v.opts.Basic
	case looksLikeJWT(bearer):
		target = v.opts.JWT
	default:
		target = v.opts.Token
	}
	if target == nil {
		return entities.Caller{}, ErrSchemeDisabled
	}
	return target.Verify(ctx, bearer)
}

func isBasic(b string) bool {
	_, _, ok := ports.ParseBasicBearer(b)
	return ok || strings.HasPrefix(b, "fmcp-basic.")
}

func looksLikeJWT(b string) bool {
	return strings.HasPrefix(b, "eyJ") && strings.Count(b, ".") == 2
}
