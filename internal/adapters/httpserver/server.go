// Package httpserver serves the MCP server over Streamable HTTP for remote
// callers (agents, humans), with bearer authentication in front of it.
// It is the remote-mode entry point described in PLAN.md M1c.0.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpadapter "github.com/forward-mcp/internal/adapters/mcp"
	"github.com/forward-mcp/internal/ports"
)

// Options configure the remote server.
type Options struct {
	// Addr is host:port to listen on.
	Addr string
	// Verifier authenticates bearer tokens. Required unless AllowAnonymous.
	Verifier ports.IdentityVerifier
	// AllowAnonymous serves /mcp without authentication. Lab use only; the
	// server logs a warning and every call runs as the local caller.
	AllowAnonymous bool
	// Path is the MCP endpoint path (default "/mcp").
	Path string
	// SessionTimeout closes idle MCP sessions (default 30m).
	SessionTimeout time.Duration
	// ReadHeaderTimeout for the HTTP server (default 10s).
	ReadHeaderTimeout time.Duration
	// Stateless disables session tracking; useful behind load balancers
	// without sticky sessions. Server-to-client requests are then unavailable.
	Stateless bool
}

// Server is a configured, not yet started, HTTP server.
type Server struct {
	http *http.Server
	opts Options
}

// tokenTTL is the synthetic expiration handed to the SDK middleware; the
// identity verifier is re-consulted on every request regardless.
const tokenTTL = 5 * time.Minute

// New builds the server. mcpServer is the fully registered MCP server.
func New(mcpServer *mcp.Server, opts Options) (*Server, error) {
	if opts.Addr == "" {
		return nil, errors.New("httpserver: Addr is required")
	}
	if opts.Verifier == nil && !opts.AllowAnonymous {
		return nil, errors.New("httpserver: an identity verifier is required (or AllowAnonymous for lab use)")
	}
	if opts.Path == "" {
		opts.Path = "/mcp"
	}
	if opts.SessionTimeout == 0 {
		opts.SessionTimeout = 30 * time.Minute
	}
	if opts.ReadHeaderTimeout == 0 {
		opts.ReadHeaderTimeout = 10 * time.Second
	}

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, &mcp.StreamableHTTPOptions{
		SessionTimeout: opts.SessionTimeout,
		Stateless:      opts.Stateless,
	})

	var mcpHandler http.Handler = handler
	if opts.Verifier != nil {
		mcpHandler = auth.RequireBearerToken(mcpadapter.TokenVerifier(opts.Verifier, tokenTTL), nil)(handler)
	}

	mux := http.NewServeMux()
	mux.Handle(opts.Path, mcpHandler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok\n"))
	})

	return &Server{
		http: &http.Server{
			Addr:              opts.Addr,
			Handler:           mux,
			ReadHeaderTimeout: opts.ReadHeaderTimeout,
		},
		opts: opts,
	}, nil
}

// Handler exposes the mux for in-process tests.
func (s *Server) Handler() http.Handler { return s.http.Handler }

// ListenAndServe blocks until ctx is cancelled or the listener fails. It
// returns nil on a clean shutdown.
func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.opts.Addr)
	if err != nil {
		return fmt.Errorf("httpserver: listen %s: %w", s.opts.Addr, err)
	}
	return s.Serve(ctx, ln)
}

// Serve is ListenAndServe over an existing listener (tests use port 0).
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	errCh := make(chan error, 1)
	go func() { errCh <- s.http.Serve(ln) }()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return s.http.Shutdown(shutdownCtx)
	}
}

// Addr returns the configured address.
func (s *Server) Addr() string { return s.opts.Addr }
