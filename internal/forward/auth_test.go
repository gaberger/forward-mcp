package forward

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/forward-mcp/internal/config"
)

func capturingForward(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func fakeJWT(exp time.Time) string {
	seg := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	return seg(map[string]string{"alg": "RS256"}) + "." + seg(map[string]any{"sub": "forward-mcp", "exp": exp.Unix()}) + ".c2ln"
}

func clientFor(cfg config.ForwardConfig, url string) *Client {
	cfg.APIBaseURL, cfg.Timeout = url, 5
	return NewClient(&cfg).(*Client)
}

// Both request paths (makeRequest and makeRequestWithRetry) must send the
// configured credential; before this change each built Basic auth itself.
func bothPaths(t *testing.T, c *Client) {
	t.Helper()
	if _, err := c.GetNetworks(); err != nil {
		t.Fatalf("makeRequest path: %v", err)
	}
	res, err := c.makeRequestWithRetry(context.Background(), "GET", "/api/networks", nil, 0)
	if err != nil {
		t.Fatalf("retry path: %v", err)
	}
	res.Body.Close()
}

func TestOutboundBasic(t *testing.T) {
	srv, seen := capturingForward(t)
	bothPaths(t, clientFor(config.ForwardConfig{APIKey: "fwd-key", APISecret: "fwd-secret"}, srv.URL))
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("fwd-key:fwd-secret"))
	if len(*seen) != 2 || (*seen)[0] != want || (*seen)[1] != want {
		t.Fatalf("headers %v", *seen)
	}
}

func TestOutboundToken(t *testing.T) {
	srv, seen := capturingForward(t)
	bothPaths(t, clientFor(config.ForwardConfig{AuthType: "token", APIToken: "fwd-api-token-0123"}, srv.URL))
	for _, h := range *seen {
		if h != "Bearer fwd-api-token-0123" {
			t.Fatalf("token header %q", h)
		}
	}
	srv2, seen2 := capturingForward(t)
	bothPaths(t, clientFor(config.ForwardConfig{AuthType: "token", APIToken: "abc", APITokenScheme: "Token"}, srv2.URL))
	if (*seen2)[0] != "Token abc" {
		t.Fatalf("custom scheme header %q", (*seen2)[0])
	}
}

func TestOutboundJWTFromRotatingFile(t *testing.T) {
	srv, seen := capturingForward(t)
	p := filepath.Join(t.TempDir(), "jwt")
	first := fakeJWT(time.Now().Add(time.Hour))
	_ = os.WriteFile(p, []byte(first+"\n"), 0o600)
	c := clientFor(config.ForwardConfig{AuthType: "jwt", APIJWTFile: p}, srv.URL)
	if _, err := c.GetNetworks(); err != nil {
		t.Fatal(err)
	}
	second := fakeJWT(time.Now().Add(2 * time.Hour))
	_ = os.WriteFile(p, []byte(second), 0o600)
	later := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(p, later, later)
	if _, err := c.GetNetworks(); err != nil {
		t.Fatal(err)
	}
	if (*seen)[0] != "Bearer "+first || (*seen)[1] != "Bearer "+second {
		t.Fatalf("rotation not picked up: %v", *seen)
	}
}

func TestOutboundExpiredJWTFailsBeforeSending(t *testing.T) {
	srv, seen := capturingForward(t)
	c := clientFor(config.ForwardConfig{AuthType: "jwt", APIJWT: fakeJWT(time.Now().Add(-time.Minute))}, srv.URL)
	_, err := c.GetNetworks()
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("want an expiry error, got %v", err)
	}
	if len(*seen) != 0 {
		t.Fatal("an expired JWT must not be sent")
	}
	if strings.Contains(err.Error(), "eyJ") {
		t.Fatal("error must not include the token")
	}
}
