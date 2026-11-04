package multi

import (
	"context"
	"errors"
	"testing"

	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/ports"
)

type fake struct {
	name string
	got  []string
}

func (f *fake) Verify(_ context.Context, bearer string) (entities.Caller, error) {
	f.got = append(f.got, bearer)
	if bearer == "bad" || bearer == "eyJbad.e30.sig" {
		return entities.Caller{}, errors.New("rejected by " + f.name)
	}
	return entities.Caller{Subject: f.name}, nil
}

func TestRoutesEachSchemeToItsVerifier(t *testing.T) {
	jwt, tok, basic := &fake{name: "jwt"}, &fake{name: "token"}, &fake{name: "basic"}
	m, err := New(Options{JWT: jwt, Token: tok, Basic: basic})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cases := map[string]string{
		"eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ4In0.c2ln": "jwt",
		"opaque-token-0123456789":                   "token",
		ports.BasicBearer("key", "secret"):          "basic",
	}
	for bearer, want := range cases {
		c, err := m.Verify(ctx, bearer)
		if err != nil || c.Subject != want {
			t.Errorf("%q -> %+v %v, want %s", bearer, c, err, want)
		}
	}
	// A JWT that fails JWT verification never falls through to the token table.
	if _, err := m.Verify(ctx, "eyJbad.e30.sig"); err == nil {
		t.Error("a rejected JWT must not be retried as an opaque token")
	}
	if len(tok.got) != 1 {
		t.Errorf("token verifier saw %v", tok.got)
	}
}

func TestDisabledSchemesAreRefused(t *testing.T) {
	m, _ := New(Options{JWT: &fake{name: "jwt"}})
	ctx := context.Background()
	if _, err := m.Verify(ctx, ports.BasicBearer("k", "s")); err == nil {
		t.Error("basic not configured: must be refused")
	}
	if _, err := m.Verify(ctx, "opaque-token-0123456789"); err == nil {
		t.Error("token not configured: must be refused (not handed to the JWT verifier as a fallback)")
	}
	if _, err := New(Options{}); err == nil {
		t.Error("at least one scheme is required")
	}
	if got := (&Verifier{opts: Options{JWT: &fake{}, Basic: &fake{}}}).Schemes(); len(got) != 2 || got[0] != "jwt" || got[1] != "basic" {
		t.Errorf("schemes %v", got)
	}
}
