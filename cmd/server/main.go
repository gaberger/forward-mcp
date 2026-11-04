package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/forward-mcp/internal/adapters/auditlog"
	"github.com/forward-mcp/internal/adapters/httpserver"
	"github.com/forward-mcp/internal/adapters/identity/basic"
	"github.com/forward-mcp/internal/adapters/identity/jwks"
	jwtverifier "github.com/forward-mcp/internal/adapters/identity/jwt"
	"github.com/forward-mcp/internal/adapters/identity/multi"
	"github.com/forward-mcp/internal/adapters/identity/static"
	mcpadapter "github.com/forward-mcp/internal/adapters/mcp"
	"github.com/forward-mcp/internal/config"
	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/domain/services/ratelimit"
	"github.com/forward-mcp/internal/instancelock"
	"github.com/forward-mcp/internal/logger"
	"github.com/forward-mcp/internal/ports"
	"github.com/forward-mcp/internal/service"
	"github.com/forward-mcp/internal/usecases"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverVersion is reported to MCP clients during the initialize handshake.
const serverVersion = "3.0.0"

const serverInstructions = "MCP server for Forward Networks: network discovery, NQE queries, " +
	"path searches, configuration search/diff, snapshots, locations, and a knowledge-graph memory. " +
	"Start with list_networks to find a network ID (or set_default_network to pin one). " +
	"Use search_nqe_queries to discover queries by natural language, then run_nqe_query_by_id to execute. " +
	"Prefer search_paths_bulk for path analysis."

func main() {
	// Subcommands run in their own process, outside the MCP server lifecycle,
	// so a client respawn of the stdio server cannot interrupt them.
	if len(os.Args) > 1 && os.Args[1] == "hydrate" {
		runHydrateCommand(os.Args[2:])
		return
	}
	// Remote mode: Streamable HTTP with per-caller identity and the
	// read-only-by-default tool policy. Stdio (below) is unchanged.
	if len(os.Args) > 1 && os.Args[1] == "serve-http" {
		runServeHTTP(os.Args[2:])
		return
	}

	// Initialize logger
	logger := logger.New()

	// Load configuration
	cfg := config.LoadConfig()

	// Create logger
	logger.Info("Forward MCP Server starting...")

	// Best-effort instance lock. Clients like Claude Desktop may briefly spawn
	// overlapping server processes (config reload, reconnect); exiting fatally
	// here makes every such respawn look like a crash and drops in-flight tool
	// calls. SQLite (WAL + busy_timeout) arbitrates shared data access, so
	// concurrent instances are safe - we only warn for visibility.
	lockDir := os.Getenv("FORWARD_LOCK_DIR")
	if lockDir == "" {
		lockDir = "/tmp"
	}
	instanceLock := instancelock.NewInstanceLock(lockDir)
	lockAcquired := false

	if running, pid, err := instancelock.CheckRunningInstance(lockDir); running {
		logger.Warn("Another Forward MCP Server instance appears to be running (PID: %d) - continuing anyway", pid)
	} else if err != nil {
		logger.Error("Warning: Could not check for running instances: %v", err)
	}

	logger.Debug("Acquiring instance lock at: %s", instanceLock.GetLockFilePath())
	if err := instanceLock.Acquire(3, 500*time.Millisecond); err != nil {
		logger.Warn("Could not acquire instance lock (%v) - continuing without it", err)
	} else {
		lockAcquired = true
		logger.Debug("Instance lock acquired successfully")
	}

	// Ensure lock is released on exit
	defer func() {
		if !lockAcquired {
			return
		}
		logger.Debug("Releasing instance lock...")
		if err := instanceLock.Release(); err != nil {
			logger.Error("Failed to release instance lock: %v", err)
		} else {
			logger.Debug("Instance lock released successfully")
		}
	}()

	// Log essential environment configuration at INFO level
	logger.Info("Environment initialized - API: %s", cfg.Forward.APIBaseURL)
	if err := cfg.Forward.ValidateAuth(); err == nil {
		logger.Info("Environment initialized - API credentials: configured (%s)", cfg.Forward.EffectiveAuthType())
	} else {
		logger.Info("Environment initialized - API credentials: %v", err)
	}

	if cfg.Forward.DefaultNetworkID != "" {
		logger.Info("Environment initialized - Default network: %s", cfg.Forward.DefaultNetworkID)
	} else {
		logger.Info("Environment initialized - Default network: not set")
	}

	// SECURITY: TLS 1.3 is now enforced, InsecureSkipVerify has been removed
	logger.Info("Environment initialized - TLS verification: enabled (TLS 1.3 minimum)")

	// Security: Do not log sensitive configuration details even in debug mode
	// Use INFO level logging above for configuration visibility

	// Create Forward MCP service
	logger.Debug("Creating Forward MCP service...")
	forwardService := service.NewForwardMCPService(cfg, logger)

	// Create MCP server (official go-sdk); stdio transport is attached in Run below.
	logger.Debug("Creating MCP server...")
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "forward-mcp",
		Title:   "Forward Networks MCP Server",
		Version: serverVersion,
	}, &mcp.ServerOptions{
		Instructions: serverInstructions,
	})

	// Register all Forward Networks tools
	logger.Debug("Registering Forward Networks tools...")
	if err := forwardService.RegisterTools(server); err != nil {
		logger.Fatalf("Failed to register tools: %v", err)
	}
	logger.Debug("Tools registered successfully!")

	// Register prompt workflows following MCP best practices
	logger.Debug("Registering prompt workflows...")
	if err := forwardService.RegisterPrompts(server); err != nil {
		logger.Fatalf("Failed to register prompts: %v", err)
	}
	logger.Debug("Prompt workflows registered successfully!")

	// Register contextual resources following MCP best practices
	logger.Debug("Registering contextual resources...")
	if err := forwardService.RegisterResources(server); err != nil {
		logger.Fatalf("Failed to register resources: %v", err)
	}
	logger.Debug("Contextual resources registered successfully!")

	// Check if we're in a TTY (interactive mode) or pipe mode
	if fileInfo, _ := os.Stdin.Stat(); (fileInfo.Mode() & os.ModeCharDevice) != 0 {
		logger.Debug("Running in interactive mode (TTY detected)")
		logger.Debug("Server is ready and waiting for MCP protocol messages on stdin...")
		logger.Debug("Send MCP messages as JSON to interact with the server")
	} else {
		logger.Debug("Running in pipe mode (stdin redirected)")
	}

	// Setup graceful shutdown
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run the server over stdio; Run blocks until the client disconnects
	// (stdin EOF) or the context is cancelled.
	logger.Debug("Starting Forward Networks MCP server...")
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Run(ctx, &mcp.StdioTransport{})
	}()

	logger.Debug("MCP server is now running and waiting for connections...")

	// Wait for client disconnect, server error, or shutdown signal.
	var runErr error
	select {
	case runErr = <-serverErr:
		if runErr != nil {
			logger.Error("Server error: %v", runErr)
		} else {
			logger.Info("Client disconnected, shutting down...")
		}
	case sig := <-shutdown:
		logger.Info("Received signal %v, shutting down gracefully...", sig)
		cancel()
	}

	// Shutdown the ForwardMCPService to stop background goroutines and close databases.
	if err := forwardService.Shutdown(30 * time.Second); err != nil {
		logger.Error("Error during service shutdown: %v", err)
	}

	// Close logger file if it exists
	if err := logger.Close(); err != nil {
		logger.Error("Error closing logger: %v", err)
	}

	logger.Info("Server shutdown complete")
	if runErr != nil {
		os.Exit(1)
	}
}

// runHydrateCommand implements `forward-mcp-server hydrate`: a synchronous,
// out-of-process database hydration. Heavy sync work lives here instead of in
// the MCP serving process, which clients kill and respawn at will.
func runHydrateCommand(args []string) {
	flags := flag.NewFlagSet("hydrate", flag.ExitOnError)
	enhanced := flags.Bool("enhanced", false, "fetch full metadata (intent/description/source); one API call per changed query, slower")
	embeddings := flags.Bool("embeddings", false, "regenerate the embedding cache after syncing")
	force := flags.Bool("force", false, "ignore stored commit IDs and refetch everything")
	timeout := flags.Duration("timeout", 30*time.Minute, "overall budget for the API fetch")
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "Usage: %s hydrate [flags]\n\nSyncs the NQE query database from the Forward Networks API.\nRun a basic sync first; add --enhanced for rich metadata and --embeddings to rebuild semantic search.\n\nFlags:\n", os.Args[0])
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		os.Exit(2)
	}

	log := logger.New()
	cfg := config.LoadConfig()

	err := service.RunHydrateCLI(cfg, log, service.HydrateCLIOptions{
		Enhanced:   *enhanced,
		Embeddings: *embeddings,
		Force:      *force,
		Timeout:    *timeout,
	})
	if closeErr := log.Close(); closeErr != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to close logger: %v\n", closeErr)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "hydrate failed: %v\n", err)
		os.Exit(1)
	}
}

// runServeHTTP implements `forward-mcp serve-http`: the remote-mode server
// for agents and humans (PLAN.md M1c.0). Only read-class tools are exposed
// unless FORWARD_ENABLE_PROPOSE_TOOLS=true; write-class tools never are.
func runServeHTTP(args []string) {
	flags := flag.NewFlagSet("serve-http", flag.ExitOnError)
	addr := flags.String("addr", "", "listen address (default SERVER_HOST:SERVER_PORT, 0.0.0.0:8080)")
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "Usage: %s serve-http [flags]\n\nServes MCP over Streamable HTTP at /mcp with bearer authentication.\n"+
			"Identity: FORWARD_REMOTE_AUTH is a comma list of jwt, token, basic:\n"+
			"  jwt   FORWARD_JWT_ISSUER, FORWARD_JWT_AUDIENCE and FORWARD_JWT_JWKS_URL or FORWARD_JWT_PUBLIC_KEY_FILE\n"+
			"  token FORWARD_REMOTE_STATIC_TOKENS=\"token=subject[:scopes[:networks]],...\" (alias: static)\n"+
			"  basic FORWARD_REMOTE_BASIC_CREDENTIALS_FILE (API key + SHA-256 of secret per subject)\n"+
			"or FORWARD_REMOTE_AUTH=none with FORWARD_REMOTE_ALLOW_ANONYMOUS=true (lab only).\n"+
			"Propose tools: FORWARD_ENABLE_PROPOSE_TOOLS=true (callers also need the \"predict\" scope).\n\nFlags:\n", os.Args[0])
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		os.Exit(2)
	}

	log := logger.New()
	cfg := config.LoadConfig()
	if *addr == "" {
		*addr = fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	}

	verifier, err := buildVerifier(cfg.Remote, log)
	if err != nil {
		log.Fatalf("serve-http: %v", err)
	}
	if err := cfg.Forward.ValidateAuth(); err != nil {
		log.Fatalf("serve-http: %v", err)
	}
	log.Info("serve-http: Forward API auth type %s", cfg.Forward.EffectiveAuthType())

	log.Info("Forward MCP remote server starting on %s (API: %s)", *addr, cfg.Forward.APIBaseURL)
	forwardService := service.NewForwardMCPService(cfg, log)

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "forward-mcp",
		Title:   "Forward Networks MCP Server",
		Version: serverVersion,
	}, &mcp.ServerOptions{Instructions: serverInstructions})

	exposure := usecases.NewToolExposure(entities.ServeModeRemote, cfg.Remote.EnableProposeTools)
	registrar := mcpadapter.NewRegistrar(server, exposure, auditlog.New(log)).
		WithRateLimiter(ratelimit.New(cfg.Remote.RateLimitPerMinute, cfg.Remote.RateLimitBurst))
	if err := forwardService.RegisterToolsWith(registrar); err != nil {
		log.Fatalf("Failed to register tools: %v", err)
	}
	log.Info("Tool exposure: %s; withheld: %v", registrar.Summary(), registrar.Skipped())
	if err := forwardService.RegisterPrompts(server); err != nil {
		log.Fatalf("Failed to register prompts: %v", err)
	}
	if err := forwardService.RegisterResources(server); err != nil {
		log.Fatalf("Failed to register resources: %v", err)
	}

	httpSrv, err := httpserver.New(server, httpserver.Options{
		Addr:           *addr,
		Verifier:       verifier,
		AllowAnonymous: cfg.Remote.AllowAnonymous && verifier == nil,
		Stateless:      cfg.Remote.Stateless,
	})
	if err != nil {
		log.Fatalf("serve-http: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-shutdown
		log.Info("Received signal %v, shutting down...", sig)
		cancel()
	}()

	runErr := httpSrv.ListenAndServe(ctx)
	if runErr != nil {
		log.Error("Server error: %v", runErr)
	}
	if err := forwardService.Shutdown(30 * time.Second); err != nil {
		log.Error("Error during service shutdown: %v", err)
	}
	if err := log.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to close logger: %v\n", err)
	}
	if runErr != nil {
		os.Exit(1)
	}
}

// buildJWTVerifier wires the key source from config: JWKS URL (tokend or
// any issuer), a static PEM public key, or an HS256 secret for the lab.
// buildVerifier turns FORWARD_REMOTE_AUTH into a verifier. It is a comma
// list of jwt, token (alias: static), basic, or the single value none
// (FORWARD_REMOTE_ALLOW_ANONYMOUS=true, lab only). Each credential is routed
// to the verifier for its shape (see identity/multi).
func buildVerifier(rc config.RemoteConfig, log *logger.Logger) (ports.IdentityVerifier, error) {
	modes := map[string]bool{}
	for _, m := range strings.Split(strings.ToLower(rc.Auth), ",") {
		if m = strings.TrimSpace(m); m == "static" {
			m = "token"
		}
		if m != "" {
			modes[m] = true
		}
	}
	if modes["none"] {
		if len(modes) > 1 {
			return nil, fmt.Errorf("FORWARD_REMOTE_AUTH=none cannot be combined with other schemes")
		}
		if !rc.AllowAnonymous {
			return nil, fmt.Errorf("FORWARD_REMOTE_AUTH=none requires FORWARD_REMOTE_ALLOW_ANONYMOUS=true")
		}
		log.Warn("serve-http: ANONYMOUS access enabled; every caller is %q (lab only)", entities.LocalSubject)
		return nil, nil
	}
	var o multi.Options
	for m := range modes {
		switch m {
		case "jwt":
			v, err := buildJWTVerifier(rc)
			if err != nil {
				return nil, err
			}
			o.JWT = v
			log.Info("serve-http: auth jwt: issuer=%s audience=%s jwks=%s", rc.JWTIssuer, rc.JWTAudience, rc.JWTJWKSURL)
		case "token":
			v, err := static.Parse(rc.StaticTokens)
			if err != nil {
				return nil, err
			}
			o.Token = v
			log.Warn("serve-http: auth token: static bearer tokens (FORWARD_REMOTE_STATIC_TOKENS); subjects %v", v.Subjects())
		case "basic":
			if rc.BasicCredentialsFile == "" {
				return nil, fmt.Errorf("FORWARD_REMOTE_AUTH includes basic: set FORWARD_REMOTE_BASIC_CREDENTIALS_FILE")
			}
			v, err := basic.Load(rc.BasicCredentialsFile)
			if err != nil {
				return nil, err
			}
			o.Basic = v
			log.Info("serve-http: auth basic: API key/secret; subjects %v", v.Subjects())
		default:
			return nil, fmt.Errorf("unsupported FORWARD_REMOTE_AUTH scheme %q (jwt|token|basic, or none)", m)
		}
	}
	return multi.New(o)
}

func buildJWTVerifier(rc config.RemoteConfig) (ports.IdentityVerifier, error) {
	if rc.JWTIssuer == "" {
		return nil, fmt.Errorf("FORWARD_JWT_ISSUER is required for FORWARD_REMOTE_AUTH=jwt")
	}
	var keys jwks.KeySource
	allowHS := false
	switch {
	case rc.JWTJWKSURL != "":
		keys = jwks.NewRemote(rc.JWTJWKSURL)
	case rc.JWTPublicKeyFile != "":
		b, err := os.ReadFile(rc.JWTPublicKeyFile)
		if err != nil {
			return nil, fmt.Errorf("FORWARD_JWT_PUBLIC_KEY_FILE: %w", err)
		}
		pub, err := jwks.ParsePEMPublicKey(b)
		if err != nil {
			return nil, fmt.Errorf("FORWARD_JWT_PUBLIC_KEY_FILE: %w", err)
		}
		keys = jwks.Static{Public: pub}
	case rc.JWTHS256Secret != "":
		if len(rc.JWTHS256Secret) < 32 {
			return nil, fmt.Errorf("FORWARD_JWT_HS256_SECRET must be at least 32 bytes")
		}
		keys = jwks.Static{Secret: []byte(rc.JWTHS256Secret)}
		allowHS = true
	default:
		return nil, fmt.Errorf("set FORWARD_JWT_JWKS_URL, FORWARD_JWT_PUBLIC_KEY_FILE or FORWARD_JWT_HS256_SECRET")
	}
	return jwtverifier.New(jwtverifier.Options{Issuer: rc.JWTIssuer, Audience: rc.JWTAudience, Keys: keys, AllowHS256: allowHS})
}
