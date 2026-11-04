package jwks

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestJWKRoundTripAndPEM(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	jwk := FromRSAPublicKey("k1", &priv.PublicKey)
	pub, err := jwk.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	if !pub.(*rsa.PublicKey).Equal(&priv.PublicKey) {
		t.Fatal("RSA JWK round trip mismatch")
	}
	der, _ := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	p, err := ParsePEMPublicKey(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	if err != nil || !p.(*rsa.PublicKey).Equal(&priv.PublicKey) {
		t.Fatalf("PEM: %v", err)
	}
	if _, err := ParsePEMPublicKey([]byte("junk")); err == nil {
		t.Fatal("junk PEM must fail")
	}
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ecjwk := JWK{Kty: "EC", Crv: "P-256", X: b64enc(ec.X.Bytes()), Y: b64enc(ec.Y.Bytes())}
	if _, err := ecjwk.PublicKey(); err != nil {
		t.Fatalf("EC: %v", err)
	}
	if _, err := (JWK{Kty: "oct"}).PublicKey(); err == nil {
		t.Fatal("oct must be rejected")
	}
}

func TestStaticKeySource(t *testing.T) {
	s := Static{Secret: []byte("s")}
	if k, err := s.Key(context.Background(), "HS256", ""); err != nil || string(k.([]byte)) != "s" {
		t.Fatal("HS secret")
	}
	if _, err := s.Key(context.Background(), "RS256", ""); err == nil {
		t.Fatal("no public key must fail")
	}
}

func TestRemoteCachesAndRotates(t *testing.T) {
	k1, _ := rsa.GenerateKey(rand.Reader, 2048)
	k2, _ := rsa.GenerateKey(rand.Reader, 2048)
	set := Set{Keys: []JWK{FromRSAPublicKey("k1", &k1.PublicKey)}}
	fetches := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches++
		_ = json.NewEncoder(w).Encode(set)
	}))
	defer srv.Close()
	r := NewRemote(srv.URL)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return now }
	ctx := context.Background()
	if _, err := r.Key(ctx, "RS256", "k1"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Key(ctx, "RS256", "k1"); err != nil || fetches != 1 {
		t.Fatalf("second lookup must be cached (fetches=%d)", fetches)
	}
	// Unknown kid forces one refresh; still unknown → error; rate-limited.
	if _, err := r.Key(ctx, "RS256", "k2"); err == nil || fetches != 2 {
		t.Fatalf("expected forced refresh then error (fetches=%d, err=%v)", fetches, err)
	}
	if _, err := r.Key(ctx, "RS256", "k2"); err == nil || fetches != 2 {
		t.Fatalf("forced refresh must be rate-limited (fetches=%d)", fetches)
	}
	// Rotation: after MinRetry the new key is picked up.
	set.Keys = append(set.Keys, FromRSAPublicKey("k2", &k2.PublicKey))
	now = now.Add(time.Minute)
	if _, err := r.Key(ctx, "RS256", "k2"); err != nil {
		t.Fatalf("rotated key: %v", err)
	}
	if _, err := r.Key(ctx, "HS256", ""); err == nil {
		t.Fatal("JWKS must not serve HMAC")
	}
}

func b64enc(b []byte) string {
	return JWK{}.encode(b)
}
