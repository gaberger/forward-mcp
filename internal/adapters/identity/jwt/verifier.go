// Package jwt is the production ports.IdentityVerifier (PLAN.md 1c.1):
// bearer JWTs are verified for signature (RS256/ES256 via a key source;
// HS256 only when explicitly allowed for the lab), issuer, audience and
// expiry, then mapped to a Caller.
//
// Claims contract (issued by netorch tokend, or any issuer you configure):
//
//	sub          subject (agent or human id)      required
//	iss, aud     must match configuration         required
//	exp          required; iat/nbf honoured
//	scope        "predict read" (space-separated) or scopes: ["predict"]
//	network_ids  ["162112", ...]                  optional allow-list
package jwt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"

	"github.com/forward-mcp/internal/adapters/identity/jwks"
	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/ports"
)

// Options configure the verifier.
type Options struct {
	Issuer   string
	Audience string
	Keys     jwks.KeySource
	// AllowHS256 permits HMAC tokens (lab only; a shared secret is not an
	// identity).
	AllowHS256 bool
	// Leeway tolerates small clock skew (default 30s).
	Leeway time.Duration
}

// Verifier implements ports.IdentityVerifier.
type Verifier struct {
	opts    Options
	methods []string
}

var _ ports.IdentityVerifier = (*Verifier)(nil)

// New validates options and builds the verifier.
func New(opts Options) (*Verifier, error) {
	if opts.Issuer == "" || opts.Audience == "" || opts.Keys == nil {
		return nil, errors.New("jwt: issuer, audience and a key source are required")
	}
	if opts.Leeway <= 0 {
		opts.Leeway = 30 * time.Second
	}
	methods := []string{"RS256", "RS384", "RS512", "PS256", "ES256", "ES384", "ES512"}
	if opts.AllowHS256 {
		methods = append(methods, "HS256")
	}
	return &Verifier{opts: opts, methods: methods}, nil
}

type claims struct {
	gojwt.RegisteredClaims
	Scope      string   `json:"scope,omitempty"`
	Scopes     []string `json:"scopes,omitempty"`
	NetworkIDs []string `json:"network_ids,omitempty"`
}

// Verify implements ports.IdentityVerifier.
func (v *Verifier) Verify(ctx context.Context, bearer string) (entities.Caller, error) {
	bearer = strings.TrimSpace(bearer)
	if bearer == "" {
		return entities.Caller{}, errors.New("empty token")
	}
	var c claims
	_, err := gojwt.ParseWithClaims(bearer, &c, func(t *gojwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		return v.opts.Keys.Key(ctx, t.Method.Alg(), kid)
	},
		gojwt.WithValidMethods(v.methods),
		gojwt.WithIssuer(v.opts.Issuer),
		gojwt.WithAudience(v.opts.Audience),
		gojwt.WithExpirationRequired(),
		gojwt.WithLeeway(v.opts.Leeway),
	)
	if err != nil {
		return entities.Caller{}, fmt.Errorf("invalid token: %w", err)
	}
	if c.Subject == "" {
		return entities.Caller{}, errors.New("invalid token: sub is required")
	}
	caller := entities.Caller{Subject: c.Subject, Issuer: c.Issuer, NetworkIDs: append([]string(nil), c.NetworkIDs...)}
	for _, s := range strings.Fields(c.Scope) {
		caller.Scopes = append(caller.Scopes, s)
	}
	caller.Scopes = append(caller.Scopes, c.Scopes...)
	return caller, nil
}
