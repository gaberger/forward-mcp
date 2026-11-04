package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/forward-mcp/internal/adapters/auditlog"
	"github.com/forward-mcp/internal/adapters/identity/static"
	mcpadapter "github.com/forward-mcp/internal/adapters/mcp"
	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/domain/services/toolpolicy"
	"github.com/forward-mcp/internal/ports"
	"github.com/forward-mcp/internal/usecases"
)

type whoArgs struct{}
type echoArgs struct {
	NetworkID string `json:"network_id,omitempty"`
}

type captureSink struct{ lines []string }

func (c *captureSink) Info(format string, args ...interface{}) {
	c.lines = append(c.lines, fmt.Sprintf(format, args...))
}

// newTestServer registers one read tool ("whoami") and one propose tool
// ("predict_create_change_set") through the remote policy.
func newTestServer(t *testing.T, propose bool) (*httptest.Server, *captureSink) {
	t.Helper()
	catalog := toolpolicy.MustCatalog([]entities.ToolSpec{
		{Name: "whoami", Class: entities.ToolClassRead, Target: entities.ToolTargetLocal},
		{Name: "predict_create_change_set", Class: entities.ToolClassPropose, Target: entities.ToolTargetForward},
		{Name: "delete_snapshot", Class: entities.ToolClassWrite, Target: entities.ToolTargetForward},
	})
	exposure := &usecases.ToolExposure{Registry: catalog, Policy: toolpolicy.Remote(propose)}
	sink := &captureSink{}
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	r := mcpadapter.NewRegistrar(srv, exposure, auditlog.New(sink))
	mcpadapter.AddTool(r, "whoami", "who am I", func(ctx context.Context, _ whoArgs) (*mcp.CallToolResult, error) {
		c := ports.CallerFromContext(ctx)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: c.Subject + "|" + strings.Join(c.Scopes, ",")}}}, nil
	})
	mcpadapter.AddTool(r, "predict_create_change_set", "propose", func(_ context.Context, a echoArgs) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "cs-for-" + a.NetworkID}}}, nil
	})
	mcpadapter.AddTool(r, "delete_snapshot", "write", func(context.Context, echoArgs) (*mcp.CallToolResult, error) {
		t.Fatal("write tool handler must never run in remote mode")
		return nil, nil
	})
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}

	verifier, err := static.Parse("agent-token-0123456789=agent-a:predict,plain-token-0123456789=agent-b")
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(srv, Options{Addr: "127.0.0.1:0", Verifier: verifier})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, sink
}

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (b bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if b.token != "" {
		req.Header.Set("Authorization", "Bearer "+b.token)
	}
	return b.base.RoundTrip(req)
}

func connect(t *testing.T, ts *httptest.Server, token string) *mcp.ClientSession {
	t.Helper()
	hc := &http.Client{Transport: bearerTransport{token: token, base: http.DefaultTransport}}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: hc}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestHealthz(t *testing.T) {
	ts, _ := newTestServer(t, false)
	res, err := http.Get(ts.URL + "/healthz")
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("healthz: %v %v", err, res)
	}
}

func TestUnauthenticatedIsRejected(t *testing.T) {
	ts, _ := newTestServer(t, false)
	hc := &http.Client{}
	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"}, nil)
	if _, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: hc}, nil); err == nil {
		t.Fatal("connect without a bearer token must fail")
	}
	res, err := http.Post(ts.URL+"/mcp", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	if err != nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %v %v", err, res)
	}
	if _, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{token: "wrong-token-0123456789", base: http.DefaultTransport}}}, nil); err == nil {
		t.Fatal("connect with an unknown token must fail")
	}
}

func TestRemoteListsOnlyReadToolsByDefault(t *testing.T) {
	ts, _ := newTestServer(t, false)
	session := connect(t, ts, "plain-token-0123456789")
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range tools.Tools {
		names = append(names, tl.Name)
	}
	if strings.Join(names, ",") != "whoami" {
		t.Errorf("expected only whoami, got %v", names)
	}
}

func TestCallerIdentityReachesHandlers(t *testing.T) {
	ts, _ := newTestServer(t, true)
	session := connect(t, ts, "agent-token-0123456789")
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "whoami", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Content[0].(*mcp.TextContent).Text; got != "agent-a|predict" {
		t.Errorf("handler saw caller %q", got)
	}
}

func TestProposeRequiresScopeAndIsAudited(t *testing.T) {
	ts, sink := newTestServer(t, true)

	// agent-b has no predict scope: the tool is listed (flag on) but denied.
	b := connect(t, ts, "plain-token-0123456789")
	res, err := b.CallTool(context.Background(), &mcp.CallToolParams{Name: "predict_create_change_set", Arguments: map[string]any{"network_id": "162112"}})
	if err == nil && (res == nil || !res.IsError) {
		t.Fatalf("expected denial for agent-b, got %+v", res)
	}

	// agent-a holds predict: allowed and audited with identifiers.
	a := connect(t, ts, "agent-token-0123456789")
	res, err = a.CallTool(context.Background(), &mcp.CallToolParams{Name: "predict_create_change_set", Arguments: map[string]any{"network_id": "162112"}})
	if err != nil || res.IsError {
		t.Fatalf("expected success for agent-a: %v %+v", err, res)
	}
	joined := strings.Join(sink.lines, "\n")
	for _, want := range []string{`"subject":"agent-b"`, `"outcome":"denied"`, `"subject":"agent-a"`, `"outcome":"ok"`, `"network_id":"162112"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("audit log missing %s:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "0123456789") {
		t.Errorf("audit log leaked a token:\n%s", joined)
	}
}

func TestNewRequiresVerifierUnlessAnonymous(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	if _, err := New(srv, Options{Addr: "127.0.0.1:0"}); err == nil {
		t.Error("expected error without verifier")
	}
	if _, err := New(srv, Options{Addr: "127.0.0.1:0", AllowAnonymous: true}); err != nil {
		t.Errorf("anonymous lab mode should construct: %v", err)
	}
}
