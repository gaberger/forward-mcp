package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/forward-mcp/internal/config"
	"github.com/forward-mcp/internal/instancelock"
	"github.com/forward-mcp/internal/logger"
	"github.com/forward-mcp/internal/service"
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
	if cfg.Forward.APIKey != "" {
		logger.Info("Environment initialized - API credentials: configured")
	} else {
		logger.Info("Environment initialized - API credentials: missing")
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
