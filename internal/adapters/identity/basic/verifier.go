// Package basic verifies API key and secret credentials (HTTP Basic) for
// forward-mcp remote mode. Secrets are configured only as SHA-256 hashes,
// so the credentials file is not itself a credential. Comparison is in
// constant time, and an unknown key costs the same as a wrong secret.
package basic

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/ports"
)

// Credential is one API key.
type Credential struct {
	Key string `json:"key"`
	// SecretSHA256 is hex(sha256(secret)), optionally prefixed "sha256:".
	// Generate with: printf %s "$SECRET" | sha256sum
	SecretSHA256 string   `json:"secret_sha256"`
	Subject      string   `json:"subject"`
	Scopes       []string `json:"scopes,omitempty"`
	NetworkIDs   []string `json:"network_ids,omitempty"`
}

type entry struct {
	hash   [32]byte
	caller entities.Caller
}

// Verifier implements ports.IdentityVerifier for ports.BasicBearer values.
type Verifier struct {
	byKey map[string]entry
}

var _ ports.IdentityVerifier = (*Verifier)(nil)

// ErrInvalid is returned for every failed check (no detail for callers).
var ErrInvalid = errors.New("invalid API key or secret")

// New validates and indexes credentials.
func New(creds []Credential) (*Verifier, error) {
	v := &Verifier{byKey: map[string]entry{}}
	subjects := map[string]bool{}
	for i, c := range creds {
		switch {
		case c.Key == "":
			return nil, fmt.Errorf("basic: credential %d has no key", i)
		case strings.Contains(c.Key, ":"):
			return nil, fmt.Errorf("basic: key %q may not contain ':'", c.Key)
		case c.Subject == "":
			return nil, fmt.Errorf("basic: key %q has no subject", c.Key)
		}
		if _, dup := v.byKey[c.Key]; dup {
			return nil, fmt.Errorf("basic: duplicate key %q", c.Key)
		}
		if subjects[c.Subject] {
			return nil, fmt.Errorf("basic: subject %q is used by two keys", c.Subject)
		}
		raw, err := hex.DecodeString(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(c.SecretSHA256)), "sha256:"))
		if err != nil || len(raw) != sha256.Size {
			return nil, fmt.Errorf("basic: key %q: secret_sha256 must be a 64-hex-digit SHA-256 (never the secret itself)", c.Key)
		}
		var h [32]byte
		copy(h[:], raw)
		subjects[c.Subject] = true
		v.byKey[c.Key] = entry{hash: h, caller: entities.Caller{
			Subject: c.Subject, Issuer: "basic",
			Scopes: append([]string(nil), c.Scopes...), NetworkIDs: append([]string(nil), c.NetworkIDs...),
		}}
	}
	if len(v.byKey) == 0 {
		return nil, errors.New("basic: no credentials configured")
	}
	return v, nil
}

// Load reads {"credentials":[...]} from a JSON file.
func Load(path string) (*Verifier, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("basic: %w", err)
	}
	var f struct {
		Credentials []Credential `json:"credentials"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("basic: %s: %w", path, err)
	}
	return New(f.Credentials)
}

// Subjects lists configured subjects, sorted (for startup logs).
func (v *Verifier) Subjects() []string {
	out := make([]string, 0, len(v.byKey))
	for _, e := range v.byKey {
		out = append(out, e.caller.Subject)
	}
	sort.Strings(out)
	return out
}

// dummy keeps the unknown-key path as costly as the wrong-secret path.
var dummy = sha256.Sum256([]byte("forward-mcp-basic-dummy"))

// Verify implements ports.IdentityVerifier.
func (v *Verifier) Verify(_ context.Context, bearer string) (entities.Caller, error) {
	key, secret, ok := ports.ParseBasicBearer(bearer)
	if !ok || secret == "" {
		return entities.Caller{}, ErrInvalid
	}
	got := sha256.Sum256([]byte(secret))
	e, known := v.byKey[key]
	want := dummy
	if known {
		want = e.hash
	}
	if subtle.ConstantTimeCompare(got[:], want[:]) != 1 || !known {
		return entities.Caller{}, ErrInvalid
	}
	c := e.caller
	c.Scopes = append([]string(nil), c.Scopes...)
	c.NetworkIDs = append([]string(nil), c.NetworkIDs...)
	return c, nil
}
