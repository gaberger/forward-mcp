// Package jwks resolves verification keys for JWTs: from a JWKS endpoint
// (netorch tokend, or any OIDC issuer), from a static PEM public key, or
// from an HS256 shared secret for lab use. It is deliberately small so the
// only third-party code in the identity path is the JWT library itself.
package jwks

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"
)

// KeySource returns the key to verify a token signed with alg and kid.
type KeySource interface {
	Key(ctx context.Context, alg, kid string) (any, error)
}

// Static holds one public key (RSA or ECDSA) or one HMAC secret.
type Static struct {
	Public crypto.PublicKey
	Secret []byte
}

// Key implements KeySource.
func (s Static) Key(_ context.Context, alg, _ string) (any, error) {
	switch alg[:2] {
	case "HS":
		if len(s.Secret) == 0 {
			return nil, errors.New("no HMAC secret configured")
		}
		return s.Secret, nil
	default:
		if s.Public == nil {
			return nil, errors.New("no public key configured")
		}
		return s.Public, nil
	}
}

// ParsePEMPublicKey reads an RSA or EC public key (PKIX "PUBLIC KEY" or
// "RSA PUBLIC KEY" blocks).
func ParsePEMPublicKey(b []byte) (crypto.PublicKey, error) {
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, errors.New("no PEM block found")
	}
	switch block.Type {
	case "PUBLIC KEY":
		return x509.ParsePKIXPublicKey(block.Bytes)
	case "RSA PUBLIC KEY":
		return x509.ParsePKCS1PublicKey(block.Bytes)
	}
	return nil, fmt.Errorf("unsupported PEM block %q", block.Type)
}

// JWK is the subset of RFC 7517 we accept.
type JWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use,omitempty"`
	Alg string `json:"alg,omitempty"`
	N   string `json:"n,omitempty"`
	E   string `json:"e,omitempty"`
	Crv string `json:"crv,omitempty"`
	X   string `json:"x,omitempty"`
	Y   string `json:"y,omitempty"`
}

// Set is a JWKS document.
type Set struct {
	Keys []JWK `json:"keys"`
}

// PublicKey converts a JWK to a crypto.PublicKey.
func (k JWK) PublicKey() (crypto.PublicKey, error) {
	switch k.Kty {
	case "RSA":
		n, err := b64(k.N)
		if err != nil {
			return nil, err
		}
		e, err := b64(k.E)
		if err != nil {
			return nil, err
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}, nil
	case "EC":
		var curve elliptic.Curve
		switch k.Crv {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return nil, fmt.Errorf("unsupported curve %q", k.Crv)
		}
		x, err := b64(k.X)
		if err != nil {
			return nil, err
		}
		y, err := b64(k.Y)
		if err != nil {
			return nil, err
		}
		return &ecdsa.PublicKey{Curve: curve, X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}, nil
	}
	return nil, fmt.Errorf("unsupported kty %q", k.Kty)
}

func b64(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }

func (JWK) encode(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// FromRSAPublicKey builds a JWK for publishing.
func FromRSAPublicKey(kid string, pub *rsa.PublicKey) JWK {
	return JWK{Kty: "RSA", Kid: kid, Use: "sig", Alg: "RS256",
		N: base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes())}
}

// Remote fetches and caches a JWKS. Unknown kids trigger one refresh
// (key rotation); refreshes are rate-limited.
type Remote struct {
	URL      string
	HTTP     *http.Client
	TTL      time.Duration // default 10m
	MinRetry time.Duration // default 30s between forced refreshes

	mu        sync.Mutex
	keys      map[string]crypto.PublicKey
	fetched   time.Time
	lastForce time.Time
	now       func() time.Time
}

// NewRemote builds a Remote key source.
func NewRemote(url string) *Remote {
	return &Remote{URL: url, HTTP: &http.Client{Timeout: 10 * time.Second}, TTL: 10 * time.Minute, MinRetry: 30 * time.Second, now: time.Now}
}

// Key implements KeySource.
func (r *Remote) Key(ctx context.Context, alg, kid string) (any, error) {
	if len(alg) < 2 || alg[:2] == "HS" {
		return nil, errors.New("JWKS keys cannot verify HMAC tokens")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.keys == nil || r.now().Sub(r.fetched) > r.TTL {
		if err := r.refresh(ctx); err != nil && r.keys == nil {
			return nil, err
		}
	}
	if k, ok := r.keys[kid]; ok {
		return k, nil
	}
	if r.now().Sub(r.lastForce) >= r.MinRetry {
		r.lastForce = r.now()
		if err := r.refresh(ctx); err != nil {
			return nil, err
		}
		if k, ok := r.keys[kid]; ok {
			return k, nil
		}
	}
	if kid == "" && len(r.keys) == 1 {
		for _, k := range r.keys {
			return k, nil
		}
	}
	return nil, fmt.Errorf("no key %q in JWKS", kid)
}

func (r *Remote) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", r.URL, nil)
	if err != nil {
		return err
	}
	res, err := r.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("jwks fetch: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("jwks fetch: HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	var set Set
	if err := json.Unmarshal(b, &set); err != nil {
		return fmt.Errorf("jwks parse: %w", err)
	}
	keys := map[string]crypto.PublicKey{}
	for _, k := range set.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		pk, err := k.PublicKey()
		if err != nil {
			continue
		}
		keys[k.Kid] = pk
	}
	if len(keys) == 0 {
		return errors.New("jwks: no usable keys")
	}
	r.keys, r.fetched = keys, r.now()
	return nil
}
