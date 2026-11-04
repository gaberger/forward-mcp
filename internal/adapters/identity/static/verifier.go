// Package static is a development-only identity verifier: a fixed list of
// bearer tokens mapped to subjects, scopes and network ids, read from
// configuration. It exists so remote mode, per-identity isolation (M1c) and
// the S2 concurrency spike can be exercised before the JWT adapter lands.
// It must never be used outside a lab: tokens are compared in constant time
// but they are static shared secrets.
package static

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"

	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/ports"
)

// ErrUnknownToken is returned for any bearer not in the table.
var ErrUnknownToken = errors.New("unknown bearer token")

// Verifier implements ports.IdentityVerifier over a static table.
type Verifier struct {
	entries []entry
}

type entry struct {
	token  string
	caller entities.Caller
}

var _ ports.IdentityVerifier = (*Verifier)(nil)

// Parse builds a verifier from a specification of the form
//
//	token=subject[:scope1+scope2[:network1+network2]],token2=subject2
//
// Whitespace around items is ignored. Subjects must be unique; tokens must
// be at least 16 characters so a typo cannot become a credential.
func Parse(spec string) (*Verifier, error) {
	v := &Verifier{}
	seenSubject := map[string]bool{}
	seenToken := map[string]bool{}
	for _, item := range strings.Split(spec, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		tok, rest, ok := strings.Cut(item, "=")
		tok = strings.TrimSpace(tok)
		if !ok || tok == "" || strings.TrimSpace(rest) == "" {
			return nil, fmt.Errorf("static identity: entry %q must be token=subject[:scopes[:networks]]", item)
		}
		if len(tok) < 16 {
			return nil, fmt.Errorf("static identity: token for %q is shorter than 16 characters", strings.SplitN(rest, ":", 2)[0])
		}
		if seenToken[tok] {
			return nil, fmt.Errorf("static identity: duplicate token")
		}
		seenToken[tok] = true

		parts := strings.SplitN(rest, ":", 3)
		c := entities.Caller{Subject: strings.TrimSpace(parts[0]), Issuer: "static"}
		if c.Subject == "" {
			return nil, fmt.Errorf("static identity: entry %q has an empty subject", item)
		}
		if seenSubject[c.Subject] {
			return nil, fmt.Errorf("static identity: duplicate subject %q", c.Subject)
		}
		seenSubject[c.Subject] = true
		if len(parts) > 1 {
			c.Scopes = splitPlus(parts[1])
		}
		if len(parts) > 2 {
			c.NetworkIDs = splitPlus(parts[2])
		}
		v.entries = append(v.entries, entry{token: tok, caller: c})
	}
	if len(v.entries) == 0 {
		return nil, errors.New("static identity: no tokens configured")
	}
	return v, nil
}

func splitPlus(s string) []string {
	var out []string
	for _, p := range strings.Split(s, "+") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Verify implements ports.IdentityVerifier.
func (v *Verifier) Verify(_ context.Context, bearer string) (entities.Caller, error) {
	for _, e := range v.entries {
		if subtle.ConstantTimeCompare([]byte(e.token), []byte(bearer)) == 1 {
			return cloneCaller(e.caller), nil
		}
	}
	return entities.Caller{}, ErrUnknownToken
}

// Subjects lists configured subjects, for startup logs (never tokens).
func (v *Verifier) Subjects() []string {
	out := make([]string, 0, len(v.entries))
	for _, e := range v.entries {
		out = append(out, e.caller.Subject)
	}
	return out
}

func cloneCaller(c entities.Caller) entities.Caller {
	c.Scopes = append([]string(nil), c.Scopes...)
	c.NetworkIDs = append([]string(nil), c.NetworkIDs...)
	return c
}
