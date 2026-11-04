package jwt

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"strings"
	"testing"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"

	"github.com/forward-mcp/internal/adapters/identity/jwks"
)

func mint(t *testing.T, key any, method gojwt.SigningMethod, mut func(gojwt.MapClaims)) string {
	t.Helper()
	c := gojwt.MapClaims{"iss": "https://tokend.netorch", "aud": "forward-mcp", "sub": "agent-troubleshoot", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "scope": "predict read", "network_ids": []string{"162112"}}
	if mut != nil {
		mut(c)
	}
	tok := gojwt.NewWithClaims(method, c)
	tok.Header["kid"] = "k1"
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestVerifyMatrix(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	v, err := New(Options{Issuer: "https://tokend.netorch", Audience: "forward-mcp", Keys: jwks.Static{Public: &priv.PublicKey}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	c, err := v.Verify(ctx, mint(t, priv, gojwt.SigningMethodRS256, nil))
	if err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	if c.Subject != "agent-troubleshoot" || c.Issuer != "https://tokend.netorch" || !c.HasScope("predict") || !c.HasScope("read") || !c.MayAccessNetwork("162112") || c.MayAccessNetwork("1") {
		t.Errorf("claims mapped wrong: %+v", c)
	}

	bad := map[string]string{
		"expired":   mint(t, priv, gojwt.SigningMethodRS256, func(c gojwt.MapClaims) { c["exp"] = time.Now().Add(-2 * time.Minute).Unix() }),
		"no exp":    mint(t, priv, gojwt.SigningMethodRS256, func(c gojwt.MapClaims) { delete(c, "exp") }),
		"wrong aud": mint(t, priv, gojwt.SigningMethodRS256, func(c gojwt.MapClaims) { c["aud"] = "other" }),
		"wrong iss": mint(t, priv, gojwt.SigningMethodRS256, func(c gojwt.MapClaims) { c["iss"] = "https://evil" }),
		"no sub":    mint(t, priv, gojwt.SigningMethodRS256, func(c gojwt.MapClaims) { delete(c, "sub") }),
		"wrong key": mint(t, other, gojwt.SigningMethodRS256, nil),
		"hs256":     mint(t, []byte("secret-secret-secret"), gojwt.SigningMethodHS256, nil),
		"garbage":   "not.a.jwt",
		"empty":     "",
	}
	// alg=none: header forged by hand.
	none := gojwt.NewWithClaims(gojwt.SigningMethodNone, gojwt.MapClaims{"iss": "https://tokend.netorch", "aud": "forward-mcp", "sub": "x", "exp": time.Now().Add(time.Hour).Unix()})
	s, _ := none.SignedString(gojwt.UnsafeAllowNoneSignatureType)
	bad["alg none"] = s
	for name, tok := range bad {
		if _, err := v.Verify(ctx, tok); err == nil {
			t.Errorf("%s: must be rejected", name)
		}
	}
}

func TestHS256OnlyWhenAllowed(t *testing.T) {
	secret := []byte("lab-secret-at-least-32-bytes-long!!")
	v, _ := New(Options{Issuer: "i", Audience: "a", Keys: jwks.Static{Secret: secret}, AllowHS256: true})
	tok := mint(t, secret, gojwt.SigningMethodHS256, func(c gojwt.MapClaims) {
		c["iss"], c["aud"] = "i", "a"
		delete(c, "scope")
		c["scopes"] = []string{"predict"}
	})
	c, err := v.Verify(context.Background(), tok)
	if err != nil || !c.HasScope("predict") {
		t.Fatalf("HS256 lab token: %v %+v", err, c)
	}
	if _, err := New(Options{Issuer: "i", Audience: "a"}); err == nil || !strings.Contains(err.Error(), "key source") {
		t.Error("options validation")
	}
}
