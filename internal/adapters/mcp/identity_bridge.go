package mcpadapter

import (
	"context"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/ports"
)

// The go-sdk carries the verified bearer identity as auth.TokenInfo: the
// HTTP middleware (auth.RequireBearerToken) stores it in the request
// context, and the Streamable HTTP transport copies it onto every JSON-RPC
// request as RequestExtra.TokenInfo. Tool handler contexts are detached
// from the HTTP request, so the Registrar reads it from the request and
// re-attaches it as a ports Caller. These two functions are the only
// translation between the SDK's identity type and the domain's.

const (
	extraIssuer     = "iss"
	extraNetworkIDs = "network_ids"
)

// TokenInfoFromCaller converts a domain Caller into what auth.RequireBearerToken
// expects. exp is required by the middleware (a zero expiration is a 401).
func TokenInfoFromCaller(c entities.Caller, exp time.Time) *auth.TokenInfo {
	return &auth.TokenInfo{
		UserID:     c.Subject,
		Scopes:     append([]string(nil), c.Scopes...),
		Expiration: exp,
		Extra: map[string]any{
			extraIssuer:     c.Issuer,
			extraNetworkIDs: append([]string(nil), c.NetworkIDs...),
		},
	}
}

// CallerFromTokenInfo is the inverse. A nil TokenInfo yields LocalCaller,
// which the remote policy will reject for anything but read tools and the
// stdio policy accepts.
func CallerFromTokenInfo(ti *auth.TokenInfo) entities.Caller {
	if ti == nil || ti.UserID == "" {
		return entities.LocalCaller()
	}
	c := entities.Caller{Subject: ti.UserID, Scopes: append([]string(nil), ti.Scopes...)}
	if iss, ok := ti.Extra[extraIssuer].(string); ok {
		c.Issuer = iss
	}
	switch nets := ti.Extra[extraNetworkIDs].(type) {
	case []string:
		c.NetworkIDs = append([]string(nil), nets...)
	case []any:
		for _, n := range nets {
			if s, ok := n.(string); ok {
				c.NetworkIDs = append(c.NetworkIDs, s)
			}
		}
	}
	return c
}

// TokenVerifier adapts a ports.IdentityVerifier to the SDK's middleware
// hook. Verification failures are reported as auth.ErrInvalidToken so the
// middleware answers 401 rather than 500. Tokens are given a short
// synthetic expiration because ports.IdentityVerifier owns expiry itself.
func TokenVerifier(v ports.IdentityVerifier, ttl time.Duration) auth.TokenVerifier {
	return func(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		c, err := v.Verify(ctx, token)
		if err != nil {
			return nil, auth.ErrInvalidToken
		}
		return TokenInfoFromCaller(c, time.Now().Add(ttl)), nil
	}
}

// callerFor derives the caller for one tool call: the request's TokenInfo
// when present (remote mode), otherwise whatever is already in ctx
// (stdio: LocalCaller).
func callerFor(ctx context.Context, req *mcp.CallToolRequest) (context.Context, entities.Caller) {
	if req != nil {
		if extra := req.GetExtra(); extra != nil && extra.TokenInfo != nil {
			c := CallerFromTokenInfo(extra.TokenInfo)
			return ports.WithCaller(ctx, c), c
		}
	}
	return ctx, ports.CallerFromContext(ctx)
}
