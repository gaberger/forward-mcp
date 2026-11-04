package basic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/forward-mcp/internal/ports"
)

func hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func newV(t *testing.T) *Verifier {
	t.Helper()
	v, err := New([]Credential{
		{Key: "fwd-agent-01", SecretSHA256: hash("agent-secret-value"), Subject: "agent-troubleshoot", Scopes: []string{"read"}, NetworkIDs: []string{"162112"}},
		{Key: "fwd-ops-01", SecretSHA256: "sha256:" + hash("ops-secret-value"), Subject: "gary", Scopes: []string{"read", "predict"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestValidKeySecretYieldsCaller(t *testing.T) {
	v := newV(t)
	c, err := v.Verify(context.Background(), ports.BasicBearer("fwd-agent-01", "agent-secret-value"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Subject != "agent-troubleshoot" || c.Issuer != "basic" || strings.Join(c.Scopes, ",") != "read" || c.NetworkIDs[0] != "162112" {
		t.Fatalf("caller %+v", c)
	}
	if c, err := v.Verify(context.Background(), ports.BasicBearer("fwd-ops-01", "ops-secret-value")); err != nil || c.Subject != "gary" {
		t.Fatalf("sha256: prefix form: %v %+v", err, c)
	}
}

func TestRejections(t *testing.T) {
	v := newV(t)
	for name, bearer := range map[string]string{
		"wrong secret":        ports.BasicBearer("fwd-agent-01", "nope"),
		"unknown key":         ports.BasicBearer("fwd-nobody", "agent-secret-value"),
		"other key's secret":  ports.BasicBearer("fwd-ops-01", "agent-secret-value"),
		"empty secret":        ports.BasicBearer("fwd-agent-01", ""),
		"not a basic bearer":  "agent-secret-value",
		"jwt-looking bearer":  "eyJhbGciOiJSUzI1NiJ9.e30.sig",
		"secret hash as pass": ports.BasicBearer("fwd-agent-01", hash("agent-secret-value")),
	} {
		if c, err := v.Verify(context.Background(), bearer); err == nil {
			t.Errorf("%s: accepted as %+v", name, c)
		}
	}
}

func TestConfigValidation(t *testing.T) {
	for name, creds := range map[string][]Credential{
		"plaintext secret":  {{Key: "k1", SecretSHA256: "agent-secret-value", Subject: "s"}},
		"short hash":        {{Key: "k1", SecretSHA256: "abcd", Subject: "s"}},
		"no subject":        {{Key: "k1", SecretSHA256: hash("x")}},
		"no key":            {{SecretSHA256: hash("x"), Subject: "s"}},
		"duplicate key":     {{Key: "k1", SecretSHA256: hash("x"), Subject: "a"}, {Key: "k1", SecretSHA256: hash("y"), Subject: "b"}},
		"duplicate subject": {{Key: "k1", SecretSHA256: hash("x"), Subject: "a"}, {Key: "k2", SecretSHA256: hash("y"), Subject: "a"}},
		"colon in key":      {{Key: "k:1", SecretSHA256: hash("x"), Subject: "a"}},
	} {
		if _, err := New(creds); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestLoadFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "creds.json")
	body := `{"credentials":[{"key":"fwd-agent-01","secret_sha256":"` + hash("agent-secret-value") + `","subject":"agent-troubleshoot","scopes":["read"]}]}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c, err := v.Verify(context.Background(), ports.BasicBearer("fwd-agent-01", "agent-secret-value")); err != nil || c.Subject != "agent-troubleshoot" {
		t.Fatalf("%v %+v", err, c)
	}
	if subs := v.Subjects(); len(subs) != 1 || subs[0] != "agent-troubleshoot" {
		t.Errorf("subjects %v", subs)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("missing file must fail")
	}
}
