package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/forward-mcp/internal/config"
	"github.com/forward-mcp/internal/forward"
	"github.com/forward-mcp/internal/logger"
	_ "github.com/mattn/go-sqlite3"
	mcp "github.com/metoro-io/mcp-golang"
)

// Arguments for get_nqe_result_chunks tool
// Either entity_id or (query_id, network_id, snapshot_id) must be provided
// Optionally, chunk_index can be used to fetch a single chunk
// If chunk_index is omitted, all chunks are returned
type GetNQEResultChunksArgs struct {
	EntityID   string `json:"entity_id"`
	QueryID    string `json:"query_id"`
	NetworkID  string `json:"network_id"`
	SnapshotID string `json:"snapshot_id"`
	ChunkIndex *int   `json:"chunk_index,omitempty"`
}

// WorkflowState represents the current state of a user workflow
type WorkflowState struct {
	CurrentStep   string                 `json:"current_step"`
	Parameters    map[string]interface{} `json:"parameters"`
	SelectedQuery string                 `json:"selected_query"`
	NetworkID     string                 `json:"network_id"`
	SnapshotID    string                 `json:"snapshot_id"`
}

// WorkflowManager manages user workflow states
type WorkflowManager struct {
	sessions map[string]*WorkflowState
	mutex    sync.RWMutex
}

// NewWorkflowManager creates a new workflow manager
func NewWorkflowManager() *WorkflowManager {
	return &WorkflowManager{
		sessions: make(map[string]*WorkflowState),
	}
}

// GetState gets the workflow state for a session
func (wm *WorkflowManager) GetState(sessionID string) *WorkflowState {
	wm.mutex.RLock()
	defer wm.mutex.RUnlock()
	if state, exists := wm.sessions[sessionID]; exists {
		return state
	}
	return &WorkflowState{
		CurrentStep: "start",
		Parameters:  make(map[string]interface{}),
	}
}

// SetState sets the workflow state for a session
func (wm *WorkflowManager) SetState(sessionID string, state *WorkflowState) {
	wm.mutex.Lock()
	defer wm.mutex.Unlock()
	wm.sessions[sessionID] = state
}

// ForwardMCPService implements Forward Networks MCP tools using mcp-golang
type ForwardMCPService struct {
	forwardClient     forward.ClientInterface
	config            *config.Config
	logger            *logger.Logger
	instanceID        string // Unique identifier for this Forward Networks instance
	defaults          *ServiceDefaults
	workflowManager   *WorkflowManager
	semanticCache     *SemanticCache
	queryIndex        *NQEQueryIndex
	database          *NQEDatabase
	memorySystem      *MemorySystem       // Knowledge graph memory system
	apiTracker        *APIMemoryTracker   // API result tracking using memory system
	bloomManager      *BloomSearchManager // Bloom filter for efficient large result filtering
	bloomIndexManager *BloomIndexManager  // Persistent bloom index for large NQE results
	// Context cancellation for graceful shutdown
	ctx        context.Context
	cancelFunc context.CancelFunc
}

// ServiceDefaults holds default values for the MCP service
type ServiceDefaults struct {
	NetworkID  string
	SnapshotID string
	QueryLimit int
}

// NewForwardMCPService creates a new Forward MCP service
func NewForwardMCPService(cfg *config.Config, logger *logger.Logger) *ForwardMCPService {
	// Generate instance ID for partitioning database and cache by Forward Networks instance
	instanceID := GenerateInstanceID(cfg.Forward.APIBaseURL)
	logger.Info("Using instance ID '%s' for partitioning (based on %s)", instanceID, cfg.Forward.APIBaseURL)

	// Create Forward Networks client
	forwardClient := forward.NewClient(&cfg.Forward)

	// Create embedding service based on config
	var embeddingService EmbeddingService
	if cfg.Forward.SemanticCache.EmbeddingProvider == "openai" {
		if openaiKey := os.Getenv("OPENAI_API_KEY"); openaiKey != "" {
			embeddingService = NewOpenAIEmbeddingService(openaiKey)
		} else {
			embeddingService = NewKeywordEmbeddingService()
			logger.Warn("OpenAI provider selected but OPENAI_API_KEY not set - using keyword embedding service")
		}
	} else {
		embeddingService = NewKeywordEmbeddingService()
	}

	// Create semantic cache with instance partitioning
	semanticCache := NewSemanticCache(embeddingService, logger, instanceID, &cfg.Forward.SemanticCache)

	// Create database with instance partitioning
	database, err := NewNQEDatabase(logger, instanceID)
	if err != nil {
		logger.Error("Failed to create database: %v", err)
		// Continue without database - will fall back to spec file
		database = nil
	}

	// Create query index
	queryIndex := NewNQEQueryIndex(embeddingService, logger)

	// Create memory system
	memorySystem, err := NewMemorySystem(logger, instanceID)
	if err != nil {
		logger.Error("Failed to create memory system: %v", err)
		// Continue without memory system
		memorySystem = nil
	}

	// Create API memory tracker
	var apiTracker *APIMemoryTracker
	if memorySystem != nil {
		apiTracker = NewAPIMemoryTracker(memorySystem, logger, instanceID)
		logger.Info("API memory tracker initialized for tracking API results and relationships")
	}

	// Create bloom search manager for efficient large result filtering
	bloomManager := NewBloomSearchManager(logger, instanceID)
	logger.Info("Bloom search manager initialized for efficient large result filtering")

	// Create persistent bloom index manager for large NQE results
	bloomIndexDir := filepath.Join("data", "bloom_indexes", instanceID)
	bloomIndexManager := NewBloomIndexManager(logger, bloomIndexDir)
	logger.Info("Persistent bloom index manager initialized for large NQE results")

	// Create context for cancellation
	ctx, cancelFunc := context.WithCancel(context.Background())

	service := &ForwardMCPService{
		forwardClient: forwardClient,
		config:        cfg,
		logger:        logger,
		instanceID:    instanceID,
		defaults: &ServiceDefaults{
			NetworkID:  cfg.Forward.DefaultNetworkID,
			SnapshotID: cfg.Forward.DefaultSnapshotID,
			QueryLimit: cfg.Forward.DefaultQueryLimit,
		},
		workflowManager:   NewWorkflowManager(),
		semanticCache:     semanticCache,
		queryIndex:        queryIndex,
		database:          database,
		memorySystem:      memorySystem,
		apiTracker:        apiTracker,
		bloomManager:      bloomManager,
		bloomIndexManager: bloomIndexManager,
		ctx:               ctx,
		cancelFunc:        cancelFunc,
	}

	// Set up database callback to automatically refresh query index when database is updated
	if database != nil && queryIndex != nil {
		database.AddUpdateCallback(func() {
			logger.Info("🔄 Database updated, automatically refreshing query index...")

			// Load updated queries from database
			queries, err := database.LoadQueries()
			if err != nil {
				logger.Warn("🔄 Failed to load updated queries for index refresh: %v", err)
				return
			}

			// Refresh query index with updated data
			if err := queryIndex.LoadFromQueries(queries); err != nil {
				logger.Warn("🔄 Failed to refresh query index after database update: %v", err)
			} else {
				logger.Info("🔄 Query index automatically refreshed with %d queries", len(queries))

				// Check embedding coverage after refresh
				stats := queryIndex.GetStatistics()
				embeddedCount := stats["embedded_queries"].(int)
				if embeddedCount > 0 && embeddedCount < len(queries) {
					coverage := stats["embedding_coverage"].(float64)
					logger.Info("🧠 AI embeddings coverage: %.1f%% (%d/%d queries)", coverage*100, embeddedCount, len(queries))
				}
			}
		})
		logger.Info("🔄 Database update callback registered for automatic query index refresh")
	}

	// Initialize query index with existing data synchronously
	if database != nil {
		// Try to load existing queries from database first
		logger.Info("🔄 Loading existing queries from database...")
		queries, err := database.LoadQueries()
		if err != nil {
			logger.Warn("🔄 Failed to load queries from database: %v", err)
			// Fallback to spec file
			if err := queryIndex.LoadFromSpec(); err != nil {
				logger.Warn("🔄 Failed to initialize query index from spec: %v", err)
			} else {
				logger.Info("🔄 Query index initialized from spec file as fallback")
			}
		} else if len(queries) > 0 {
			// Load existing queries into index
			if err := queryIndex.LoadFromQueries(queries); err != nil {
				logger.Error("🔄 Failed to load queries into index: %v", err)
				// Fallback to spec file
				if err := queryIndex.LoadFromSpec(); err != nil {
					logger.Warn("🔄 Failed to initialize query index from spec: %v", err)
				} else {
					logger.Info("🔄 Query index initialized from spec file as fallback")
				}
			} else {
				logger.Info("🔄 Query index initialized with %d existing queries from database", len(queries))
				// Count enhanced queries for informational purposes
				enhancedCount := 0
				for _, q := range queries {
					if q.SourceCode != "" || q.Description != "" {
						enhancedCount++
					}
				}
				if enhancedCount > 0 {
					logger.Info("🚀 Found %d queries with enhanced metadata (source code/descriptions)", enhancedCount)
				} else {
					logger.Info("💡 Tip: Run 'hydrate_database' with enhanced_mode for richer query metadata")
				}
			}
		} else {
			// Database is empty, load from spec file
			logger.Info("🔄 Database is empty, initializing from spec file...")
			if err := queryIndex.LoadFromSpec(); err != nil {
				logger.Warn("🔄 Failed to initialize query index from spec: %v", err)
			} else {
				logger.Info("🔄 Query index initialized from spec file")
				logger.Info("💡 Tip: Run 'hydrate_database' to populate with live data from API")
			}
		}
	} else {
		// No database, fallback to spec file loading
		logger.Info("🔄 No database available, loading from spec file...")
		if err := queryIndex.LoadFromSpec(); err != nil {
			logger.Warn("🔄 Failed to initialize query index from spec: %v", err)
		} else {
			logger.Info("🔄 Query index initialized from spec file")
		}
	}

	return service
}

// Shutdown gracefully shuts down the ForwardMCPService
func (s *ForwardMCPService) Shutdown(timeout time.Duration) error {
	s.logger.Info("Shutting down ForwardMCPService...")

	// Cancel the context
	s.cancelFunc()

	// Close database connection if it exists
	if s.database != nil {
		if err := s.database.Close(); err != nil {
			s.logger.Error("Failed to close database: %v", err)
			return fmt.Errorf("failed to close database: %w", err)
		}
	}

	// Close memory system if it exists
	if s.memorySystem != nil {
		if err := s.memorySystem.Close(); err != nil {
			s.logger.Error("Failed to close memory system: %v", err)
			return fmt.Errorf("failed to close memory system: %w", err)
		}
	}

	// Close bloom index manager
	if s.bloomIndexManager != nil {
		if err := s.bloomIndexManager.Close(); err != nil {
			s.logger.Error("Failed to close bloom index manager: %v", err)
		}
	}

	s.logger.Info("ForwardMCPService shutdown complete")
	return nil
}

// Helper function to get network ID with fallback to default
func (s *ForwardMCPService) getNetworkID(networkID string) string {
	if networkID != "" {
		return networkID
	}
	if s.defaults != nil {
		return s.defaults.NetworkID
	}
	return ""
}

// Helper function to get snapshot ID with fallback to default
func (s *ForwardMCPService) getSnapshotID(snapshotID string) string {
	if snapshotID != "" {
		return snapshotID
	}
	if s.defaults != nil {
		return s.defaults.SnapshotID
	}
	return ""
}

// Helper function to get query limit with fallback to default
func (s *ForwardMCPService) getQueryLimit(limit int) int {
	if limit > 0 {
		return limit
	}
	if s.defaults != nil {
		return s.defaults.QueryLimit
	}
	return 1000 // Default fallback if no defaults are set
}

// Helper function to log tool calls with detailed information (legacy compatibility)
func (s *ForwardMCPService) logToolCall(toolName string, args interface{}, err error) {
	// Use zero duration for legacy calls - timing will be handled at a higher level
	s.logger.LogToolCall(toolName, args, 0, err)
}

// Enhanced function to log tool calls with performance metrics
func (s *ForwardMCPService) logToolCallWithTiming(toolName string, args interface{}, duration time.Duration, err error) {
	s.logger.LogToolCall(toolName, args, duration, err)
}

// Wrapper function to time and log tool execution
func (s *ForwardMCPService) timeAndLogTool(toolName string, args interface{}, fn func() error) error {
	start := time.Now()
	err := fn()
	duration := time.Since(start)
	s.logToolCallWithTiming(toolName, args, duration, err)
	return err
}

// RegisterTools registers all Forward Networks tools with the MCP server
func (s *ForwardMCPService) RegisterTools(server *mcp.Server) error {
	// Network Management Tools
	if err := server.RegisterTool("list_networks",
		"List all networks in the Forward platform. Returns network IDs, names, and descriptions. Use this to discover available networks or find network IDs for other operations.",
		s.listNetworks); err != nil {
		return fmt.Errorf("failed to register list_networks tool: %w", err)
	}

	if err := server.RegisterTool("create_network",
		"Create a new network in the Forward platform. Requires a network name. Returns the new network with ID for subsequent operations.",
		s.createNetwork); err != nil {
		return fmt.Errorf("failed to register create_network tool: %w", err)
	}

	// if err := server.RegisterTool("delete_network",
	// 	"Delete a network from the Forward platform. Requires network_id. WARNING: This permanently deletes all associated data.",
	// 	s.deleteNetwork); err != nil {
	// 	return fmt.Errorf("failed to register delete_network tool: %w", err)
	// }

	if err := server.RegisterTool("update_network",
		"Update network properties in the Forward platform. Requires network_id and at least one property to update (name or description).",
		s.updateNetwork); err != nil {
		return fmt.Errorf("failed to register update_network tool: %w", err)
	}

	// Path Search Tools
	if err := server.RegisterTool("search_paths",
		"Search for network paths by tracing packets through the network. Requires network_id from, or src_ip and dst_ip. Use for connectivity verification, troubleshooting, and routing analysis. Can specify source IP, ports, and protocols for detailed path tracing.",
		s.searchPaths); err != nil {
		return fmt.Errorf("failed to register search_paths tool: %w", err)
	}

	// NQE Tools
	if err := server.RegisterTool("run_nqe_query_by_id",
		"Run a Network Query Engine (NQE) query using a predefined query ID from the library. Use for standard reports, compliance checks, and consistent analysis. Supports pagination with 'limit' and 'offset' in options, or set 'all_results: true' to fetch all results in batches.",
		s.runNQEQueryByID); err != nil {
		return fmt.Errorf("failed to register run_nqe_query_by_id tool: %w", err)
	}

	if err := server.RegisterTool("list_nqe_queries",
		"List available NQE queries from the Forward Networks query library. Use to discover predefined queries for reports and analysis. Can filter by directory (/L3/Basic/, /L3/Advanced/, /L3/Security/). Returns query IDs for use with run_nqe_query_by_id.",
		s.listNQEQueries); err != nil {
		return fmt.Errorf("failed to register list_nqe_queries tool: %w", err)
	}

	// First-Class Query Tools - Most Important Network Operations
	if err := server.RegisterTool("get_device_basic_info",
		"Get basic device information including names, platforms, and management IPs. Essential for device inventory and discovery. Uses predefined Device Basic Info query.",
		s.getDeviceBasicInfo); err != nil {
		return fmt.Errorf("failed to register get_device_basic_info tool: %w", err)
	}

	if err := server.RegisterTool("get_device_hardware",
		"Get device hardware information including models, serial numbers, and hardware details. Critical for hardware inventory and lifecycle management.",
		s.getDeviceHardware); err != nil {
		return fmt.Errorf("failed to register get_device_hardware tool: %w", err)
	}

	if err := server.RegisterTool("get_hardware_support",
		"Get hardware support status including end-of-life and support dates. Essential for compliance and planning hardware refreshes.",
		s.getHardwareSupport); err != nil {
		return fmt.Errorf("failed to register get_hardware_support tool: %w", err)
	}

	if err := server.RegisterTool("get_os_support",
		"Get operating system support status including OS versions and support dates. Critical for security compliance and OS upgrade planning.",
		s.getOSSupport); err != nil {
		return fmt.Errorf("failed to register get_os_support tool: %w", err)
	}

	if err := server.RegisterTool("search_configs",
		"Search device configurations for specific patterns, commands, or settings.\n\nTo create a block pattern, use triple backticks (```) to start and end the pattern, and indent lines to show hierarchy. Example:\n\npattern = ```\ninterface\n  zone-member security\n  ip address {ip:string}\n```\n\nEach line is a line pattern. Indentation defines parent/child relationships. Use curly braces for variable extraction (e.g., {ip:string}). For more, see the data extraction guide.",
		s.searchConfigs); err != nil {
		return fmt.Errorf("failed to register search_configs tool: %w", err)
	}

	if err := server.RegisterTool("get_config_diff",
		"Compare network configurations between snapshots to identify changes. Essential for change tracking and troubleshooting configuration drift.",
		s.getConfigDiff); err != nil {
		return fmt.Errorf("failed to register get_config_diff tool: %w", err)
	}

	// Device Management Tools
	if err := server.RegisterTool("list_devices",
		"List devices in a network. Requires network_id. Returns basic device inventory with names, types, and status. Supports pagination with limit and offset. Use for device discovery and inventory management.",
		s.listDevices); err != nil {
		return fmt.Errorf("failed to register list_devices tool: %w", err)
	}

	if err := server.RegisterTool("get_device_locations",
		"Get device location mappings for a network. Requires network_id. Shows which devices are assigned to which physical locations. Use for topology planning and device organization.",
		s.getDeviceLocations); err != nil {
		return fmt.Errorf("failed to register get_device_locations tool: %w", err)
	}

	// Snapshot Management Tools
	if err := server.RegisterTool("list_snapshots",
		"List network configuration snapshots. Requires network_id. Shows historical network states with timestamps and status. Use to view configuration history and find specific snapshots for queries.",
		s.listSnapshots); err != nil {
		return fmt.Errorf("failed to register list_snapshots tool: %w", err)
	}

	if err := server.RegisterTool("get_latest_snapshot",
		"Get the latest processed snapshot for a network. Requires network_id. Returns the most recent network state. Use to ensure queries run against current configuration.",
		s.getLatestSnapshot); err != nil {
		return fmt.Errorf("failed to register get_latest_snapshot tool: %w", err)
	}

	// Location Management Tools
	if err := server.RegisterTool("list_locations",
		"List locations in a network. Requires network_id. Returns physical locations with names and coordinates. Use to view network topology and organize devices by location.",
		s.listLocations); err != nil {
		return fmt.Errorf("failed to register list_locations tool: %w", err)
	}

	if err := server.RegisterTool("create_location",
		"Create a new location in a network. Requires network_id and location name. Optional description and coordinates. Use to set up new sites or data centers for device organization.",
		s.createLocation); err != nil {
		return fmt.Errorf("failed to register create_location tool: %w", err)
	}

	// Default Settings Management Tools
	if err := server.RegisterTool("get_default_settings",
		"View current default settings for network operations. Shows the default network ID, snapshot ID, and query limits configured for this session.",
		s.getDefaultSettings); err != nil {
		return fmt.Errorf("failed to register get_default_settings tool: %w", err)
	}

	if err := server.RegisterTool("set_default_network",
		"Set the default network for all operations. Accepts either a network ID or network name. This will be used when network_id is not specified in other tools.",
		s.setDefaultNetwork); err != nil {
		return fmt.Errorf("failed to register set_default_network tool: %w", err)
	}

	// Semantic Cache and AI Enhancement Tools
	if err := server.RegisterTool("get_cache_stats",
		"View semantic cache performance statistics including hit rates, total queries, and cache efficiency metrics.",
		s.getCacheStats); err != nil {
		return fmt.Errorf("failed to register get_cache_stats tool: %w", err)
	}

	if err := server.RegisterTool("suggest_similar_queries",
		"Get suggestions for similar NQE queries based on semantic similarity to your query intent. Helps discover relevant existing queries.",
		s.suggestSimilarQueries); err != nil {
		return fmt.Errorf("failed to register suggest_similar_queries tool: %w", err)
	}

	if err := server.RegisterTool("clear_cache",
		"Clear expired entries from the semantic cache to free up memory and improve performance.",
		s.clearCache); err != nil {
		return fmt.Errorf("failed to register clear_cache tool: %w", err)
	}

	// AI-Powered Query Discovery Tools
	if err := server.RegisterTool("search_nqe_queries",
		"🧠 AI-powered search through 6000+ predefined NQE queries using natural language. Describe what you want to analyze (e.g., 'AWS security issues', 'BGP routing problems', 'interface utilization') and get relevant query suggestions with similarity scores. Use this for EXPLORATION when you want to see what queries are available for a topic. For actionable results that can be immediately executed, use 'find_executable_query' instead.",
		s.searchNQEQueries); err != nil {
		return fmt.Errorf("failed to register search_nqe_queries tool: %w", err)
	}

	if err := server.RegisterTool("initialize_query_index",
		"Initialize or rebuild the AI-powered NQE query index from the spec file. REQUIRED before using search_nqe_queries or find_executable_query. Run this once at startup or when you get 'query index is empty' errors. Can generate embeddings for semantic search if OpenAI API key is available.",
		s.initializeQueryIndex); err != nil {
		return fmt.Errorf("failed to register initialize_query_index tool: %w", err)
	}

	// Database Hydration Tools
	if err := server.RegisterTool("hydrate_database",
		"Hydrate the NQE database by loading queries from the Forward Networks API. Use this to refresh the database with latest query metadata and ensure optimal performance for search operations. Automatically refreshes the query index and optionally regenerates AI embeddings.",
		s.hydrateDatabase); err != nil {
		return fmt.Errorf("failed to register hydrate_database tool: %w", err)
	}

	if err := server.RegisterTool("refresh_query_index",
		"Refresh the query index from the current database content. Use this after hydrating the database to ensure the search index reflects the latest data.",
		s.refreshQueryIndex); err != nil {
		return fmt.Errorf("failed to register refresh_query_index tool: %w", err)
	}

	if err := server.RegisterTool("get_database_status",
		"Get the current status of the database and query index including query counts, last update times, and performance metrics.",
		s.getDatabaseStatus); err != nil {
		return fmt.Errorf("failed to register get_database_status tool: %w", err)
	}

	// Memory Management Tools
	if err := server.RegisterTool("create_entity",
		"Create a new entity in the knowledge graph memory system. Entities represent people, networks, devices, projects, or any other important concept to remember.",
		s.createEntity); err != nil {
		return fmt.Errorf("failed to register create_entity tool: %w", err)
	}

	if err := server.RegisterTool("create_relation",
		"Create a relation between two entities in the knowledge graph. Relations represent how entities are connected (e.g., 'owns', 'manages', 'depends_on').",
		s.createRelation); err != nil {
		return fmt.Errorf("failed to register create_relation tool: %w", err)
	}

	if err := server.RegisterTool("add_observation",
		"Add an observation to an entity. Observations are additional facts, notes, preferences, or behaviors associated with an entity.",
		s.addObservation); err != nil {
		return fmt.Errorf("failed to register add_observation tool: %w", err)
	}

	if err := server.RegisterTool("search_entities",
		"Search for entities in the knowledge graph by name, type, or observation content. Use this to find information you've stored about people, networks, or concepts.",
		s.searchEntities); err != nil {
		return fmt.Errorf("failed to register search_entities tool: %w", err)
	}

	if err := server.RegisterTool("get_entity",
		"Retrieve a specific entity by ID or name. Use this to get detailed information about a specific person, network, device, or concept.",
		s.getEntity); err != nil {
		return fmt.Errorf("failed to register get_entity tool: %w", err)
	}

	if err := server.RegisterTool("get_relations",
		"Get all relations for a specific entity. Use this to understand how an entity is connected to others in the knowledge graph.",
		s.getRelations); err != nil {
		return fmt.Errorf("failed to register get_relations tool: %w", err)
	}

	if err := server.RegisterTool("get_observations",
		"Get all observations for a specific entity. Use this to retrieve all stored facts, notes, and preferences about an entity.",
		s.getObservations); err != nil {
		return fmt.Errorf("failed to register get_observations tool: %w", err)
	}

	if err := server.RegisterTool("delete_entity",
		"Delete an entity and all its relations and observations. Use with caution as this permanently removes all stored information about the entity.",
		s.deleteEntity); err != nil {
		return fmt.Errorf("failed to register delete_entity tool: %w", err)
	}

	if err := server.RegisterTool("delete_relation",
		"Delete a specific relation between entities. Use this to remove connections that are no longer relevant.",
		s.deleteRelation); err != nil {
		return fmt.Errorf("failed to register delete_relation tool: %w", err)
	}

	if err := server.RegisterTool("delete_observation",
		"Delete a specific observation from an entity. Use this to remove outdated or incorrect information.",
		s.deleteObservation); err != nil {
		return fmt.Errorf("failed to register delete_observation tool: %w", err)
	}

	if err := server.RegisterTool("get_memory_stats",
		"Get statistics about the memory system including counts of entities, relations, and observations by type.",
		s.getMemoryStats); err != nil {
		return fmt.Errorf("failed to register get_memory_stats tool: %w", err)
	}

	// API Analytics Tools
	if err := server.RegisterTool("get_query_analytics",
		"Get analytics about query patterns and performance for a specific network. Shows query counts, execution times, result patterns, and usage trends from the memory system.",
		s.getQueryAnalytics); err != nil {
		return fmt.Errorf("failed to register get_query_analytics tool: %w", err)
	}

	// Tool handler for get_nqe_result_chunks
	if err := server.RegisterTool("get_nqe_result_chunks",
		"Retrieve chunked NQE query results from the memory system. Provide either entity_id or (query_id, network_id, snapshot_id). Optionally, specify chunk_index to fetch a single chunk.",
		s.getNQEResultChunks); err != nil {
		return fmt.Errorf("failed to register get_nqe_result_chunks tool: %w", err)
	}

	// Add get_nqe_result_summary tool handler
	if err := server.RegisterTool("get_nqe_result_summary",
		"Get a summary of a stored NQE result (row count, columns, preview rows) by entity_id or (query_id, network_id, snapshot_id).",
		s.getNQEResultSummary); err != nil {
		return fmt.Errorf("failed to register get_nqe_result_summary tool: %w", err)
	}

	// Add analyze_nqe_result_sql tool handler
	if err := server.RegisterTool("analyze_nqe_result_sql",
		"Run a SQL query on a stored NQE result (by entity_id). Example: SELECT COUNT(*) FROM nqe_result;",
		s.analyzeNQEResultSQL); err != nil {
		return fmt.Errorf("failed to register analyze_nqe_result_sql tool: %w", err)
	}

	// Add bloom search tool handlers
	if err := server.RegisterTool("build_bloom_filter",
		"Build a bloom filter from NQE query results for efficient large dataset searching",
		s.buildBloomFilter); err != nil {
		return fmt.Errorf("failed to register build_bloom_filter tool: %w", err)
	}

	if err := server.RegisterTool("search_bloom_filter",
		"Search a bloom filter for matching items with sub-millisecond performance",
		s.searchBloomFilter); err != nil {
		return fmt.Errorf("failed to register search_bloom_filter tool: %w", err)
	}

	if err := server.RegisterTool("get_bloom_filter_stats",
		"Get statistics and performance metrics for all bloom filters",
		s.getBloomFilterStats); err != nil {
		return fmt.Errorf("failed to register get_bloom_filter_stats tool: %w", err)
	}

	return nil
}

// RegisterPrompts registers workflow prompts with the MCP server
func (s *ForwardMCPService) RegisterPrompts(server *mcp.Server) error {
	// Register NQE Query Discovery workflow as a prompt
	if err := server.RegisterPrompt("nqe_discovery", "Interactive NQE query discovery workflow to help find and run network queries", func(args NQEDiscoveryArgs) (*mcp.PromptResponse, error) {
		response, err := s.nqeQueryDiscoveryWorkflow(args)
		if err != nil {
			return nil, err
		}
		// Convert ToolResponse to PromptResponse
		if len(response.Content) > 0 {
			return mcp.NewPromptResponse("NQE Query Discovery", mcp.NewPromptMessage(response.Content[0], mcp.RoleAssistant)), nil
		}
		return mcp.NewPromptResponse("NQE Query Discovery", mcp.NewPromptMessage(mcp.NewTextContent("Welcome to NQE Query Discovery!"), mcp.RoleAssistant)), nil
	}); err != nil {
		return fmt.Errorf("failed to register nqe_discovery prompt: %w", err)
	}

	// Register Network Discovery workflow as a prompt
	if err := server.RegisterPrompt("network_discovery", "Interactive network discovery workflow to explore available networks and devices", func(args NetworkDiscoveryArgs) (*mcp.PromptResponse, error) {
		response, err := s.networkDiscoveryWorkflow(args)
		if err != nil {
			return nil, err
		}
		// Convert ToolResponse to PromptResponse
		if len(response.Content) > 0 {
			return mcp.NewPromptResponse("Network Discovery", mcp.NewPromptMessage(response.Content[0], mcp.RoleAssistant)), nil
		}
		return mcp.NewPromptResponse("Network Discovery", mcp.NewPromptMessage(mcp.NewTextContent("Network discovery workflow"), mcp.RoleAssistant)), nil
	}); err != nil {
		return fmt.Errorf("failed to register network_discovery prompt: %w", err)
	}

	// Register Large NQE Results Workflow as a prompt
	if err := server.RegisterPrompt("large_nqe_results_workflow", "Interactive workflow for handling large NQE query results with memory system storage and SQL analysis", func(args LargeNQEResultsWorkflowArgs) (*mcp.PromptResponse, error) {
		response, err := s.largeNQEResultsWorkflow(args)
		if err != nil {
			return nil, err
		}
		// Convert ToolResponse to PromptResponse
		if len(response.Content) > 0 {
			return mcp.NewPromptResponse("Large NQE Results Workflow", mcp.NewPromptMessage(response.Content[0], mcp.RoleAssistant)), nil
		}
		return mcp.NewPromptResponse("Large NQE Results Workflow", mcp.NewPromptMessage(mcp.NewTextContent("Welcome to Large NQE Results Workflow!"), mcp.RoleAssistant)), nil
	}); err != nil {
		return fmt.Errorf("failed to register large_nqe_results_workflow prompt: %w", err)
	}

	s.logger.Info("MCP ready - Forward Networks tools registered")
	return nil
}

// RegisterResources registers contextual resources with the MCP server
func (s *ForwardMCPService) RegisterResources(server *mcp.Server) error {
	// Register network context as a resource
	if err := server.RegisterResource("forward://network/context", "network_context", "Current network context including available networks and queries", "application/json", func() (*mcp.ResourceResponse, error) {
		context, err := s.getNetworkContext(NetworkContextArgs{})
		if err != nil {
			return nil, fmt.Errorf("failed to get network context: %w", err)
		}

		contextStr, ok := context.(string)
		if !ok {
			return nil, fmt.Errorf("network context is not a string")
		}

		return mcp.NewResourceResponse(mcp.NewTextEmbeddedResource("forward://network/context", contextStr, "application/json")), nil
	}); err != nil {
		return fmt.Errorf("failed to register network_context resource: %w", err)
	}

	s.logger.Debug("Successfully registered MCP resources")
	return nil
}

// nqeQueryDiscoveryWorkflow implements the NQE query discovery workflow
func (s *ForwardMCPService) nqeQueryDiscoveryWorkflow(args NQEDiscoveryArgs) (*mcp.ToolResponse, error) {
	sessionID := fmt.Sprintf("session_%v", args.SessionID) // In practice, extract from context
	state := s.workflowManager.GetState(sessionID)

	switch state.CurrentStep {
	case "start":
		return s.startQueryDiscovery(sessionID)
	case "category_selected":
		return s.listQueriesInCategory(sessionID, state.Parameters["directory"].(string))
	case "query_selected":
		return s.collectQueryParameters(sessionID)
	case "parameters_collected":
		return s.executeSelectedQuery(sessionID)
	default:
		return s.startQueryDiscovery(sessionID)
	}
}

// networkDiscoveryWorkflow implements the network discovery workflow
func (s *ForwardMCPService) networkDiscoveryWorkflow(args NetworkDiscoveryArgs) (*mcp.ToolResponse, error) {
	networks, err := s.forwardClient.GetNetworks()
	if err != nil {
		return nil, fmt.Errorf("failed to get networks: %w", err)
	}

	promptText := "Available networks:\n"
	for i, network := range networks {
		promptText += fmt.Sprintf("%d. %s (ID: %s)\n", i+1, network.Name, network.ID)
	}
	promptText += "\nWhat would you like to do?\n1. Select a network to explore\n2. Create a new network\n3. Search for specific devices"

	return mcp.NewToolResponse(mcp.NewTextContent(promptText)), nil
}

// largeNQEResultsWorkflow implements the large NQE results workflow
func (s *ForwardMCPService) largeNQEResultsWorkflow(args LargeNQEResultsWorkflowArgs) (*mcp.ToolResponse, error) {
	sessionID := fmt.Sprintf("session_%v", args.SessionID)
	state := s.workflowManager.GetState(sessionID)

	switch state.CurrentStep {
	case "start":
		return s.startLargeResultsWorkflow(sessionID)
	case "explain_process":
		return s.explainLargeResultsProcess(sessionID)
	case "show_example":
		return s.showLargeResultsExample(sessionID)
	case "demonstrate_sql":
		return s.demonstrateSQLAnalysis(sessionID)
	default:
		return s.startLargeResultsWorkflow(sessionID)
	}
}

// startLargeResultsWorkflow begins the large NQE results workflow
func (s *ForwardMCPService) startLargeResultsWorkflow(sessionID string) (*mcp.ToolResponse, error) {
	state := &WorkflowState{
		CurrentStep: "explain_process",
		Parameters:  make(map[string]interface{}),
	}
	s.workflowManager.SetState(sessionID, state)

	promptText := `🔍 **Large NQE Results Workflow Guide**

Welcome! This workflow teaches you how to handle large NQE query results efficiently using our memory system and SQL analysis capabilities.

**What you'll learn:**
1. How large results are automatically stored in chunks
2. How to retrieve and analyze stored results
3. How to use SQL queries for complex data analysis
4. Best practices for working with large datasets

**Key Concepts:**
- **Chunking**: Large results are split into 200-row chunks for LLM-friendly processing
- **Memory System**: Results are stored persistently with metadata and summaries
- **SQL Analysis**: Full SQL query capabilities on stored data
- **Entity Management**: Each result gets a unique entity ID for easy reference

Would you like to:
1. Learn about the process step-by-step
2. See a practical example
3. Try SQL analysis on existing data
4. Get best practices and tips

Which would you prefer?`

	return mcp.NewToolResponse(mcp.NewTextContent(promptText)), nil
}

// explainLargeResultsProcess explains the large results workflow process
func (s *ForwardMCPService) explainLargeResultsProcess(sessionID string) (*mcp.ToolResponse, error) {
	state := s.workflowManager.GetState(sessionID)
	state.CurrentStep = "show_example"
	s.workflowManager.SetState(sessionID, state)

	promptText := `📋 **Large NQE Results Process Explained**

**Step 1: Automatic Detection & Storage**
When you run an NQE query with "all_results: true" or when results exceed size limits:
- System automatically detects large result sets
- Results are fetched in batches using pagination
- Data is stored in the memory system with chunking (200 rows per chunk)
- Each result gets a unique entity ID for easy reference

**Step 2: Memory System Storage**
- **Entity Creation**: Creates a result entity with metadata (query_id, network_id, snapshot_id, row_count)
- **Chunking**: Splits data into manageable chunks stored as observations
- **Summary**: Generates a summary observation with columns, row count, and metadata
- **Persistence**: All data is stored in SQLite database for later retrieval

**Step 3: Analysis Tools Available**
- **get_nqe_result_summary**: View metadata and structure of stored results
- **get_nqe_result_chunks**: Retrieve raw data chunks (all or specific chunk)
- **analyze_nqe_result_sql**: Run SQL queries on the complete dataset

**Step 4: SQL Analysis Workflow**
- Retrieve all chunks for an entity
- Reconstruct complete dataset in memory
- Create temporary SQLite database with the data
- Execute your SQL queries
- Return formatted results

**Benefits:**
✅ **No API Limits**: Work with unlimited data sizes
✅ **Persistent Storage**: Results remain available across sessions
✅ **SQL Power**: Full SQL query capabilities for complex analysis
✅ **LLM Friendly**: Chunked data is easier for LLMs to process
✅ **Performance**: Avoid re-running expensive queries

Would you like to see a practical example of this workflow?`

	return mcp.NewToolResponse(mcp.NewTextContent(promptText)), nil
}

// showLargeResultsExample shows a practical example
func (s *ForwardMCPService) showLargeResultsExample(sessionID string) (*mcp.ToolResponse, error) {
	state := s.workflowManager.GetState(sessionID)
	state.CurrentStep = "demonstrate_sql"
	s.workflowManager.SetState(sessionID, state)

	promptText := `💡 **Practical Example: Device Inventory Analysis**

**Scenario**: You want to analyze all devices in your network, but the result is too large for direct API response.

**Step 1: Run Query with Large Results**
{
  "tool": "run_nqe_query_by_id",
  "arguments": {
    "query_id": "device_basic_info",
    "network_id": "your_network_id",
    "all_results": true
  }
}

**Step 2: System Response**
Fetched all results in batches.
Total items: 1,247
Columns: [device_name, platform, ip_address, status, location]
Preview (first 5 rows): [...]
Stored in memory system as entity: device_basic_info-your_network_id-latest
You can use get_nqe_result_summary to analyze this result locally.

**Step 3: Get Result Summary**
{
  "tool": "get_nqe_result_summary",
  "arguments": {
    "entity_id": "device_basic_info-your_network_id-latest"
  }
}

**Step 4: SQL Analysis Examples**
{
  "tool": "analyze_nqe_result_sql",
  "arguments": {
    "entity_id": "device_basic_info-your_network_id-latest",
    "sql_query": "SELECT platform, COUNT(*) as count FROM nqe_result GROUP BY platform ORDER BY count DESC"
  }
}

**Common SQL Queries:**
- "SELECT COUNT(*) FROM nqe_result" - Total devices
- "SELECT status, COUNT(*) FROM nqe_result GROUP BY status" - Status breakdown
- "SELECT * FROM nqe_result WHERE status = 'down'" - Down devices
- "SELECT platform, AVG(CAST(ip_address AS INTEGER)) FROM nqe_result GROUP BY platform" - Platform analysis

Would you like to try SQL analysis on some existing data?`

	return mcp.NewToolResponse(mcp.NewTextContent(promptText)), nil
}

// demonstrateSQLAnalysis demonstrates SQL analysis capabilities
func (s *ForwardMCPService) demonstrateSQLAnalysis(sessionID string) (*mcp.ToolResponse, error) {
	state := s.workflowManager.GetState(sessionID)
	state.CurrentStep = "start"
	s.workflowManager.SetState(sessionID, state)

	promptText := `🚀 **SQL Analysis Capabilities**

**Available SQL Features:**
- **Full SQLite Support**: All standard SQL operations
- **Aggregation**: COUNT, SUM, AVG, MIN, MAX, GROUP BY
- **Filtering**: WHERE clauses with complex conditions
- **Sorting**: ORDER BY with multiple columns
- **Joins**: Self-joins within the same dataset
- **Subqueries**: Nested queries for complex analysis
- **Functions**: String, numeric, and date functions

**Best Practices:**
1. **Always use LIMIT** for large result sets (system adds LIMIT 100 by default)
2. **Use GROUP BY** for aggregations and summaries
3. **Leverage WHERE** for filtering before aggregation
4. **Consider data types** - all columns are stored as TEXT initially
5. **Use CAST()** for numeric operations on text columns

**Example Workflows:**
- **Compliance Audit**: Count devices by platform, status, location
- **Performance Analysis**: Find devices with specific configurations
- **Security Assessment**: Identify devices with open ports or weak policies
- **Capacity Planning**: Analyze resource utilization patterns

**Next Steps:**
1. Run a query with "all_results: true" to get a large dataset
2. Use "get_nqe_result_summary" to understand the data structure
3. Write SQL queries to analyze the data
4. Use the results for reports, dashboards, or further analysis

**Pro Tips:**
- Store frequently used queries as entities for quick access
- Use the memory system to track analysis results over time
- Combine multiple query results for comprehensive analysis
- Export SQL results for external reporting tools

Ready to try this workflow with your own data? Start by running a query with "all_results: true"!`

	return mcp.NewToolResponse(mcp.NewTextContent(promptText)), nil
}

// getNetworkContext provides contextual network information as a resource
func (s *ForwardMCPService) getNetworkContext(args NetworkContextArgs) (interface{}, error) {
	networks, err := s.forwardClient.GetNetworks()
	if err != nil {
		return nil, fmt.Errorf("failed to get network context: %w", err)
	}

	context := map[string]interface{}{
		"networks":          networks,
		"timestamp":         "current",
		"available_queries": []string{"/L3/Basic/", "/L3/Advanced/", "/L3/Security/"},
	}

	contextJSON, _ := json.MarshalIndent(context, "", "  ")
	return string(contextJSON), nil
}

// startQueryDiscovery begins the NQE query discovery workflow
func (s *ForwardMCPService) startQueryDiscovery(sessionID string) (*mcp.ToolResponse, error) {
	state := &WorkflowState{
		CurrentStep: "category_selection",
		Parameters:  make(map[string]interface{}),
	}
	s.workflowManager.SetState(sessionID, state)

	promptText := "Welcome to NQE Query Discovery!\n\nSelect a query category:\n1. Basic (/L3/Basic/) - Device inventory, basic connectivity\n2. Advanced (/L3/Advanced/) - Complex routing, performance analysis\n3. Security (/L3/Security/) - Security policies, compliance\n\nWhich category interests you?"
	return mcp.NewToolResponse(mcp.NewTextContent(promptText)), nil
}

// listQueriesInCategory lists available queries in the selected category
func (s *ForwardMCPService) listQueriesInCategory(sessionID, directory string) (*mcp.ToolResponse, error) {
	queries, err := s.forwardClient.GetNQEQueries(directory)
	if err != nil {
		return nil, fmt.Errorf("failed to get queries: %w", err)
	}

	state := s.workflowManager.GetState(sessionID)
	state.CurrentStep = "query_selection"
	state.Parameters["directory"] = directory
	s.workflowManager.SetState(sessionID, state)

	promptText := fmt.Sprintf("Available queries in %s:\n", directory)
	for i, query := range queries {
		promptText += fmt.Sprintf("%d. %s (ID: %s)\n   Purpose: %s\n", i+1, query.Path, query.QueryID, query.Intent)
	}
	promptText += "\nWhich query would you like to run?"

	return mcp.NewToolResponse(mcp.NewTextContent(promptText)), nil
}

// collectQueryParameters collects parameters needed for the selected query
func (s *ForwardMCPService) collectQueryParameters(sessionID string) (*mcp.ToolResponse, error) {
	state := s.workflowManager.GetState(sessionID)

	// Check if we have network_id
	if _, exists := state.Parameters["network_id"]; !exists {
		return mcp.NewToolResponse(mcp.NewTextContent("Missing required parameter: network_id")), nil
	}

	// Check if we have snapshot_id
	if _, exists := state.Parameters["snapshot_id"]; !exists {
		return mcp.NewToolResponse(mcp.NewTextContent("Missing required parameter: snapshot_id")), nil
	}

	// All parameters collected, ready to execute
	state.CurrentStep = "ready_to_execute"
	s.workflowManager.SetState(sessionID, state)

	return mcp.NewToolResponse(mcp.NewTextContent("All parameters collected! Ready to execute query. Proceed?")), nil
}

// executeSelectedQuery executes the query with collected parameters
// This function is part of the workflow system that is now activated via MCP prompt registration
func (s *ForwardMCPService) executeSelectedQuery(sessionID string) (*mcp.ToolResponse, error) {
	state := s.workflowManager.GetState(sessionID)

	params := &forward.NQEQueryParams{
		NetworkID:  state.NetworkID,
		QueryID:    state.SelectedQuery,
		SnapshotID: state.SnapshotID,
	}

	result, err := s.forwardClient.RunNQEQueryByID(params)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}

	resultJSON, _ := json.MarshalIndent(result, "", "  ")
	promptText := fmt.Sprintf("Query executed successfully! Found %d results:\n%s\n\nWhat would you like to do next?\n1. Export results\n2. Run another query\n3. Get more details\n4. Exit", len(result.Items), string(resultJSON))

	return mcp.NewToolResponse(mcp.NewTextContent(promptText)), nil
}

// Network Observability Tool Implementations
func (s *ForwardMCPService) listNetworks(args ListNetworksArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("list_networks", args, nil)

	networks, err := s.forwardClient.GetNetworks()
	if err != nil {
		return nil, fmt.Errorf("failed to list networks: %w", err)
	}

	result := MarshalCompactJSONString(networks)
	return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Found %d networks:\n%s", len(networks), result))), nil
}

func (s *ForwardMCPService) createNetwork(args CreateNetworkArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("create_network", args, nil)
	network, err := s.forwardClient.CreateNetwork(args.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to create network: %w", err)
	}

	result, _ := json.MarshalIndent(network, "", "  ")
	return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Network created successfully:\n%s", string(result)))), nil
}

func (s *ForwardMCPService) deleteNetwork(args DeleteNetworkArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("delete_network", args, nil)
	network, err := s.forwardClient.DeleteNetwork(args.NetworkID)
	if err != nil {
		return nil, fmt.Errorf("failed to delete network: %w", err)
	}

	result, _ := json.MarshalIndent(network, "", "  ")
	return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Network deleted successfully:\n%s", string(result)))), nil
}

func (s *ForwardMCPService) updateNetwork(args UpdateNetworkArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("update_network", args, nil)
	update := &forward.NetworkUpdate{}
	if args.Name != "" {
		update.Name = &args.Name
	}
	if args.Description != "" {
		update.Description = &args.Description
	}

	network, err := s.forwardClient.UpdateNetwork(args.NetworkID, update)
	if err != nil {
		return nil, fmt.Errorf("failed to update network: %w", err)
	}

	result, _ := json.MarshalIndent(network, "", "  ")
	return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Network updated successfully:\n%s", string(result)))), nil
}

// Path Search Tool Implementations
func (s *ForwardMCPService) searchPaths(args SearchPathsArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("search_paths", args, nil)

	// Use defaults if not specified (like other functions do)
	networkID := s.getNetworkID(args.NetworkID)
	snapshotID := s.getSnapshotID(args.SnapshotID)

	// If no snapshot ID is available, fetch the latest snapshot for the network
	if snapshotID == "" || snapshotID == "latest" {
		s.logger.Info("searchPaths - No snapshot ID provided or in defaults, fetching latest snapshot for network %s", networkID)

		snapshot, err := s.forwardClient.GetLatestSnapshot(networkID)
		if err != nil {
			s.logger.Error("Failed to fetch latest snapshot for network %s: %v", networkID, err)
			return nil, fmt.Errorf("failed to get latest snapshot for network %s: %w", networkID, err)
		}

		if snapshot != nil && snapshot.ID != "" {
			snapshotID = snapshot.ID
			s.logger.Info("searchPaths - Using latest snapshot ID: %s", snapshotID)
		} else {
			s.logger.Warn("No valid snapshot found for network %s", networkID)
			return nil, fmt.Errorf("no valid snapshot found for network %s - ensure the network has been processed", networkID)
		}
	}

	s.logger.Debug("Path search: networkID=%s, snapshotID=%s, srcIP=%s, dstIP=%s",
		networkID, snapshotID, args.SrcIP, args.DstIP)

	params := &forward.PathSearchParams{
		DstIP:                   args.DstIP,
		SrcIP:                   args.SrcIP,
		From:                    args.From,
		Intent:                  args.Intent,
		SrcPort:                 args.SrcPort,
		DstPort:                 args.DstPort,
		MaxResults:              args.MaxResults,
		IncludeNetworkFunctions: args.IncludeNetworkFunctions,
		SnapshotID:              snapshotID, // Now uses latest snapshot if not provided
	}

	if args.IPProto != 0 {
		params.IPProto = &args.IPProto
	}

	response, err := s.forwardClient.SearchPaths(networkID, params)
	if err != nil {
		s.logger.Error("Path search failed: %v", err)
		return nil, fmt.Errorf("failed to search paths: %w", err)
	}

	// Track path search in memory system
	if s.apiTracker != nil {
		if trackErr := s.apiTracker.TrackPathSearch(networkID, args.SrcIP, args.DstIP, response); trackErr != nil {
			s.logger.Debug("Failed to track path search in memory system: %v", trackErr)
		}
	}

	s.logger.Debug("Path search completed: found %d paths, searchTime=%dms, candidates=%d, snapshotID=%s",
		len(response.Paths), response.SearchTimeMs, response.NumCandidatesFound, response.SnapshotID)

	result := MarshalCompactJSONString(response)

	// Enhanced response with debugging info
	debugInfo := ""
	if response.SnapshotID == "" {
		debugInfo += "\n⚠️  Warning: No snapshot ID in response - this might indicate an issue\n"
	}
	if response.SearchTimeMs == 0 {
		debugInfo += "\n⚠️  Warning: Search time was 0ms - this suggests no real search occurred\n"
	}
	if response.NumCandidatesFound == 0 && args.SrcIP != "" {
		debugInfo += fmt.Sprintf("\n💡 No candidates found for source IP %s - this IP might not exist in the network topology\n", args.SrcIP)
	}

	return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Path search completed. Found %d paths:%s\n%s", len(response.Paths), debugInfo, result))), nil
}

// Helper function to convert service NQEQueryOptions to forward NQEQueryOptions
func (s *ForwardMCPService) convertNQEQueryOptions(options *NQEQueryOptions) *forward.NQEQueryOptions {
	if options == nil {
		return nil
	}

	// Apply default limit if not specified
	limit := options.Limit
	if limit == 0 {
		limit = s.getQueryLimit(0)
	}

	forwardOptions := &forward.NQEQueryOptions{
		Limit:  limit,
		Offset: options.Offset,
		Format: options.Format,
	}

	if options.SortBy != nil {
		forwardOptions.SortBy = make([]forward.NQESortBy, len(options.SortBy))
		for i, sort := range options.SortBy {
			forwardOptions.SortBy[i] = forward.NQESortBy{
				ColumnName: sort.ColumnName,
				Order:      sort.Order,
			}
		}
	}

	if options.Filters != nil {
		forwardOptions.Filters = make([]forward.NQEColumnFilter, len(options.Filters))
		for i, filter := range options.Filters {
			forwardOptions.Filters[i] = forward.NQEColumnFilter{
				ColumnName: filter.ColumnName,
				Value:      filter.Value,
			}
		}
	}

	return forwardOptions
}

// NQE Tool Implementations
func (s *ForwardMCPService) runNQEQueryByID(args RunNQEQueryByIDArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("run_nqe_query_by_id", args, nil)

	// Use defaults if not specified
	networkID := s.getNetworkID(args.NetworkID)
	snapshotID := s.getSnapshotID(args.SnapshotID)

	// Proactive warning for potentially large queries
	if (args.Options == nil || args.Options.Limit == 0 || args.Options.Limit > 1000) && !args.AllResults {
		warnMsg := "⚠️ This query may return a large result set. To avoid hitting API size limits, consider setting 'all_results: true' to fetch results in batches for local analysis, or limit the output with a smaller 'limit' value.\n"
		warnMsg += "Would you like to proceed as is, or update your request?\n"
		warnMsg += "Example: { \"all_results\": true } or { \"options\": { \"limit\": 100 } }\n"
		return mcp.NewToolResponse(mcp.NewTextContent(warnMsg)), nil
	}

	if args.AllResults {
		// Fetch all results in batches using pagination
		limit := s.getQueryLimit(0)
		if args.Options != nil && args.Options.Limit > 0 {
			limit = args.Options.Limit
		}
		offset := 0
		if args.Options != nil && args.Options.Offset > 0 {
			offset = args.Options.Offset
		}

		allItems := []map[string]interface{}{}
		var lastResult *forward.NQERunResult
		for {
			params := &forward.NQEQueryParams{
				NetworkID:  networkID,
				QueryID:    args.QueryID,
				SnapshotID: snapshotID,
				Parameters: args.Parameters,
				Options: &forward.NQEQueryOptions{
					Limit:  limit,
					Offset: offset,
					// Format: "json", // REMOVED: API does not support this field
				},
			}
			result, err := s.forwardClient.RunNQEQueryByID(params)
			if err != nil {
				return nil, fmt.Errorf("failed to run NQE query (batch at offset %d): %w", offset, err)
			}
			if lastResult == nil {
				lastResult = result
			}
			allItems = append(allItems, result.Items...)
			if len(result.Items) < limit {
				break // No more data
			}
			offset += limit
		}
		// Use lastResult as template for metadata, but replace Items
		if lastResult == nil {
			return mcp.NewToolResponse(mcp.NewTextContent("No results found.")), nil
		}
		lastResult.Items = allItems

		// Store in memory system/database with chunking
		var entityID string
		if s.memorySystem != nil {
			id, chunkErr := s.memorySystem.StoreNQEResultWithChunking(args.QueryID, networkID, snapshotID, lastResult, 200)
			if chunkErr != nil {
				s.logger.Warn("Failed to store NQE result with chunking: %v", chunkErr)
			} else {
				s.logger.Debug("Stored NQE result in memory system with chunking (entity: %s)", id)
				entityID = id

				// Automatically build bloom filter for large results
				if s.bloomManager != nil && len(allItems) > 100 {
					filterType := s.determineFilterType(args.QueryID, allItems)
					buildErr := s.bloomManager.BuildFilterFromNQEResult(networkID, filterType, lastResult, 200)
					if buildErr != nil {
						s.logger.Warn("Failed to auto-build bloom filter for large result: %v", buildErr)
					} else {
						s.logger.Info("Auto-built bloom filter for large result - Network: %s, Type: %s, Items: %d", networkID, filterType, len(allItems))
					}
				}
			}
		}

		// Prepare summary
		rowCount := len(allItems)
		var columns []string
		if rowCount > 0 {
			for k := range allItems[0] {
				columns = append(columns, k)
			}
		}
		previewRows := 5
		if rowCount < previewRows {
			previewRows = rowCount
		}
		preview := allItems[:previewRows]
		response := "Fetched all results in batches.\n"
		response += fmt.Sprintf("Total items: %d\nColumns: %v\n", rowCount, columns)
		previewJSON, _ := json.MarshalIndent(preview, "", "  ")
		response += fmt.Sprintf("Preview (first %d rows):\n%s\n", previewRows, string(previewJSON))
		if entityID != "" {
			response += fmt.Sprintf("Stored in memory system as entity: %s\n", entityID)
			response += "You can use get_nqe_result_summary to analyze this result locally.\n"
		}
		return mcp.NewToolResponse(mcp.NewTextContent(response)), nil
	}

	// Single page (default) behavior
	// Validate query ID against database index if available
	stats := s.queryIndex.GetStatistics()
	totalQueries := stats["total_queries"].(int)
	if totalQueries > 0 {
		if entry, err := s.queryIndex.GetQueryByID(args.QueryID); err != nil {
			s.logger.Warn("Query ID %s not found in database index - may be deprecated or invalid", args.QueryID)
			// Continue execution anyway in case it's a newer query not yet in the database
		} else {
			s.logger.Debug("Executing validated query: %s (Path: %s)", entry.QueryID, entry.Path)
		}
	}

	// Create cache key from query parameters
	cacheKey := fmt.Sprintf("query_id:%s|params:%v", args.QueryID, args.Parameters)

	// Try to get result from cache first
	if s.config.Forward.SemanticCache.Enabled && s.semanticCache != nil {
		if cachedResult, found := s.semanticCache.Get(cacheKey, networkID, snapshotID); found {
			s.logger.Debug("Cache hit for NQE query %s", args.QueryID)
			return mcp.NewToolResponse(mcp.NewTextContent(MarshalCompactJSONString(cachedResult))), nil
		}
	}

	params := &forward.NQEQueryParams{
		NetworkID:  networkID,
		QueryID:    args.QueryID,
		SnapshotID: snapshotID,
		Parameters: args.Parameters,
		Options:    s.convertNQEQueryOptions(args.Options),
	}

	// Ensure we have options even if none were provided
	if params.Options == nil {
		params.Options = &forward.NQEQueryOptions{
			Limit: s.getQueryLimit(0),
		}
	}

	// Track execution time for API memory tracking
	start := time.Now()
	result, err := s.forwardClient.RunNQEQueryByID(params)
	executionTime := time.Since(start)

	if err != nil {
		s.logToolCall("run_nqe_query_by_id", args, err)

		// Check for specific NQE query errors and provide helpful messages
		errorStr := err.Error()
		if strings.Contains(errorStr, "Invalid module path") {
			return nil, fmt.Errorf("query contains outdated module imports (this is a data quality issue in the Forward Networks repository) - query ID: %s. Try using find_executable_query to discover alternative queries", args.QueryID)
		}
		if strings.Contains(errorStr, "NQE_RUNTIME_ERROR") {
			return nil, fmt.Errorf("query execution failed due to code issues (this may be a data quality issue) - query ID: %s. Try using find_executable_query to find working alternatives. Error: %w", args.QueryID, err)
		}
		if strings.Contains(errorStr, "result exceeds maximum length") {
			// Automatic fallback to batch mode for large results
			s.logger.Warn("Result too large, retrying with all_results: true for query %s", args.QueryID)
			args.AllResults = true
			// Inform the user that we're retrying in batch mode
			msg := "The result was too large to return directly. Fetching all results in batches for local analysis. A summary will be provided.\n"
			batchResp, batchErr := s.runNQEQueryByID(args)
			if batchErr != nil {
				return nil, batchErr
			}
			// Try to get a summary if possible
			if s.memorySystem != nil && batchResp != nil && len(batchResp.Content) > 0 {
				// Try to extract entity ID from the batch response text
				text := batchResp.Content[0].TextContent.Text
				entityID := ""
				if idx := strings.Index(text, "entity: "); idx != -1 {
					end := strings.Index(text[idx:], "\n")
					if end != -1 {
						entityID = strings.TrimSpace(text[idx+len("entity: ") : idx+end])
					} else {
						entityID = strings.TrimSpace(text[idx+len("entity: "):])
					}
				}
				if entityID != "" {
					summaryArgs := GetNQEResultChunksArgs{EntityID: entityID}
					summaryResp, summaryErr := s.getNQEResultSummary(summaryArgs)
					if summaryErr == nil && summaryResp != nil && len(summaryResp.Content) > 0 {
						msg += "\n" + summaryResp.Content[0].TextContent.Text
					}
				}
			}
			// Prepend our message to the batch response
			if batchResp != nil && len(batchResp.Content) > 0 {
				batchResp.Content[0].TextContent.Text = msg + "\n" + batchResp.Content[0].TextContent.Text
			}
			return batchResp, nil
		}
		if strings.Contains(errorStr, "Provided argument") && strings.Contains(errorStr, "is not a parameter to the given query") {
			// Parameter mismatch error, suggest find_executable_query
			return nil, fmt.Errorf("Query parameter mismatch: %s. Try using find_executable_query to find working alternatives or check the required parameters for this query.", errorStr)
		}
		return nil, fmt.Errorf("failed to run NQE query: %w", err)
	}

	// Track the query execution in memory system
	if s.apiTracker != nil {
		if trackErr := s.apiTracker.TrackNetworkQuery(args.QueryID, networkID, snapshotID, result, executionTime); trackErr != nil {
			s.logger.Debug("Failed to track query execution in memory system: %v", trackErr)
		}
	}

	// Store result in memory system with chunking for LLM/large result use
	if s.memorySystem != nil {
		_, chunkErr := s.memorySystem.StoreNQEResultWithChunking(args.QueryID, networkID, snapshotID, result, 200) // 200 rows per chunk
		if chunkErr != nil {
			s.logger.Warn("Failed to store NQE result with chunking: %v", chunkErr)
		} else {
			s.logger.Debug("Stored NQE result in memory system with chunking (entity: %s)", args.QueryID)
		}
	}

	// Store result in cache for future use
	if s.config.Forward.SemanticCache.Enabled && s.semanticCache != nil {
		if cacheErr := s.semanticCache.Put(cacheKey, networkID, snapshotID, result); cacheErr != nil {
			s.logger.Warn("Failed to cache NQE query result for %s: %v", args.QueryID, cacheErr)
		} else {
			s.logger.Debug("Cached result for NQE query %s (items: %d)", args.QueryID, len(result.Items))
		}
	}

	resultJSON := MarshalCompactJSONString(result)
	s.logger.Debug("NQE query completed with %d items", len(result.Items))

	response := fmt.Sprintf("NQE query completed. Found %d items:\n%s\n\n", len(result.Items), resultJSON)

	// Pagination warning if results may be truncated
	if params.Options != nil && len(result.Items) == params.Options.Limit {
		response += "\n⚠️ Results may be truncated. Use the 'offset' parameter to fetch the next page.\n"
		response += fmt.Sprintf("Example: set 'offset' to %d to get the next page.\n", params.Options.Offset+params.Options.Limit)
		response += "Or set 'all_results: true' in your request to fetch all results in batches.\n"
	}

	// Add helpful suggestions for predefined queries
	response += "Would you like to:\n" +
		"1. Run a different predefined query?\n" +
		"2. Create a custom query?\n" +
		"3. Export these results?"

	return mcp.NewToolResponse(mcp.NewTextContent(response)), nil
}

func (s *ForwardMCPService) listNQEQueries(args ListNQEQueriesArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("list_nqe_queries", args, nil)

	// Inline readiness check
	if !s.queryIndex.IsReady() {
		return nil, fmt.Errorf("Query index is not initialized. Try running 'initialize_query_index' tool to manually initialize.")
	}

	// Check if query index is initialized
	stats := s.queryIndex.GetStatistics()
	totalQueries := stats["total_queries"].(int)
	if totalQueries == 0 {
		s.logger.Info("Query index empty, initializing from database...")

		// Try to initialize from database first, then fallback to spec
		if s.database != nil {
			queries, err := s.database.loadWithSmartCachingContext(s.ctx, s.forwardClient, s.logger)
			if err != nil {
				s.logger.Warn("Database loading failed, falling back to spec file: %v", err)
				if err := s.queryIndex.LoadFromSpec(); err != nil {
					return nil, fmt.Errorf("failed to initialize query index: %w", err)
				}
			} else {
				if err := s.queryIndex.LoadFromQueries(queries); err != nil {
					return nil, fmt.Errorf("failed to load queries into index: %w", err)
				}
			}
		} else {
			if err := s.queryIndex.LoadFromSpec(); err != nil {
				return nil, fmt.Errorf("failed to initialize query index: %w", err)
			}
		}
		s.logger.Info("Query index initialized successfully")
	}

	// Use database-backed query index instead of direct API calls
	filteredEntries := s.queryIndex.FilterQueriesByDirectory(args.Directory)

	// Convert NQEQueryIndexEntry to forward.NQEQuery for compatibility
	var queries []forward.NQEQuery
	for _, entry := range filteredEntries {
		queries = append(queries, entry.ConvertToNQEQuery())
	}

	// Format the response with proper JSON structure
	result := MarshalCompactJSONString(queries)

	s.logger.Debug("Found %d valid NQE queries from database index", len(queries))

	// Build a helpful response message
	response := fmt.Sprintf("Found %d NQE queries (from database cache):\n%s\n\n", len(queries), result)

	// Add helpful suggestions based on the results
	if len(queries) == 0 {
		response += "No queries found in the specified directory. Try these common directories:\n" +
			"- /L3/Basic/: Basic network queries\n" +
			"- /L3/Advanced/: Advanced network analysis\n" +
			"- /L3/Security/: Security-related queries\n\n" +
			"Would you like to:\n" +
			"1. Try a different directory?\n" +
			"2. Create a custom query?\n" +
			"3. List all available directories?"
	} else {
		response += "To run a query:\n" +
			"1. Copy the 'queryId' field from the query you want to run\n" +
			"2. Use run_nqe_query_by_id with that queryId\n" +
			"3. Optionally specify limit, offset, or other options\n\n" +
			"Would you like to:\n" +
			"1. Run one of these queries?\n" +
			"2. See more details about a specific query?\n" +
			"3. Try a different directory?"
	}

	return mcp.NewToolResponse(mcp.NewTextContent(response)), nil
}

// Device Management Tool Implementations
func (s *ForwardMCPService) listDevices(args ListDevicesArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("list_devices", args, nil)

	// Apply default limit if not specified
	limit := args.Limit
	if limit == 0 {
		limit = s.getQueryLimit(0)
	}

	params := &forward.DeviceQueryParams{
		SnapshotID: args.SnapshotID,
		Limit:      limit,
		Offset:     args.Offset,
	}

	response, err := s.forwardClient.GetDevices(args.NetworkID, params)
	if err != nil {
		return nil, fmt.Errorf("failed to list devices: %w", err)
	}

	// Track device discovery in memory system
	if s.apiTracker != nil {
		if trackErr := s.apiTracker.TrackDeviceDiscovery(args.NetworkID, response.Devices); trackErr != nil {
			s.logger.Debug("Failed to track device discovery in memory system: %v", trackErr)
		}
	}

	result := MarshalCompactJSONString(response)
	return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Found %d devices (total: %d):\n%s", len(response.Devices), response.TotalCount, result))), nil
}

func (s *ForwardMCPService) getDeviceLocations(args GetDeviceLocationsArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("get_device_locations", args, nil)
	locations, err := s.forwardClient.GetDeviceLocations(args.NetworkID)
	if err != nil {
		return nil, fmt.Errorf("failed to get device locations: %w", err)
	}

	result, _ := json.MarshalIndent(locations, "", "  ")
	return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Device locations:\n%s", string(result)))), nil
}

// Snapshot Management Tool Implementations
func (s *ForwardMCPService) listSnapshots(args ListSnapshotsArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("list_snapshots", args, nil)
	snapshots, err := s.forwardClient.GetSnapshots(args.NetworkID)
	if err != nil {
		return nil, fmt.Errorf("failed to list snapshots: %w", err)
	}

	result, _ := json.MarshalIndent(snapshots, "", "  ")
	return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Found %d snapshots:\n%s", len(snapshots), string(result)))), nil
}

func (s *ForwardMCPService) getLatestSnapshot(args GetLatestSnapshotArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("get_latest_snapshot", args, nil)
	snapshot, err := s.forwardClient.GetLatestSnapshot(args.NetworkID)
	if err != nil {
		return nil, fmt.Errorf("failed to get latest snapshot: %w", err)
	}

	result, _ := json.MarshalIndent(snapshot, "", "  ")
	return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Latest snapshot:\n%s", string(result)))), nil
}

// Location Management Tool Implementations
func (s *ForwardMCPService) listLocations(args ListLocationsArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("list_locations", args, nil)
	locations, err := s.forwardClient.GetLocations(args.NetworkID)
	if err != nil {
		return nil, fmt.Errorf("failed to list locations: %w", err)
	}

	result, _ := json.MarshalIndent(locations, "", "  ")
	return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Found %d locations:\n%s", len(locations), string(result)))), nil
}

func (s *ForwardMCPService) createLocation(args CreateLocationArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("create_location", args, nil)
	location := &forward.LocationCreate{
		Name:        args.Name,
		Description: args.Description,
		Latitude:    args.Latitude,
		Longitude:   args.Longitude,
	}

	newLocation, err := s.forwardClient.CreateLocation(args.NetworkID, location)
	if err != nil {
		return nil, fmt.Errorf("failed to create location: %w", err)
	}

	result, _ := json.MarshalIndent(newLocation, "", "  ")
	return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Location created successfully:\n%s", string(result)))), nil
}

// resolveNetworkIDByName resolves a network name to its networkId using a case-insensitive match.
func (s *ForwardMCPService) resolveNetworkIDByName(name string) (string, error) {
	networks, err := s.forwardClient.GetNetworks()
	if err != nil {
		return "", err
	}
	var matches []forward.Network
	for _, n := range networks {
		if strings.EqualFold(n.Name, name) {
			matches = append(matches, n)
		}
	}
	if len(matches) == 1 {
		return matches[0].ID, nil
	} else if len(matches) > 1 {
		return "", fmt.Errorf("multiple networks found with the name '%s'", name)
	}
	return "", fmt.Errorf("no network found with the name '%s'", name)
}

// First-Class Query Tool Implementations - Critical Network Operations
// These wrap the most important predefined queries as dedicated tools

func (s *ForwardMCPService) getDeviceBasicInfo(args GetDeviceBasicInfoArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("get_device_basic_info", args, nil)

	queryArgs := RunNQEQueryByIDArgs{
		NetworkID:  args.NetworkID,
		SnapshotID: args.SnapshotID,
		QueryID:    "FQ_ac651cb2901b067fe7dbfb511613ab44776d8029", // Device Basic Info
		Options:    args.Options,
	}

	return s.runNQEQueryByID(queryArgs)
}

func (s *ForwardMCPService) getDeviceHardware(args GetDeviceHardwareArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("get_device_hardware", args, nil)

	queryArgs := RunNQEQueryByIDArgs{
		NetworkID:  args.NetworkID,
		SnapshotID: args.SnapshotID,
		QueryID:    "FQ_7ec4a8148b48a91271f342c512b2af1cdb276744", // Device Hardware
		Options:    args.Options,
	}

	return s.runNQEQueryByID(queryArgs)
}

func (s *ForwardMCPService) getHardwareSupport(args GetHardwareSupportArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("get_hardware_support", args, nil)

	queryArgs := RunNQEQueryByIDArgs{
		NetworkID:  args.NetworkID,
		SnapshotID: args.SnapshotID,
		QueryID:    "FQ_f0984b777b940b4376ed3ec4317ad47437426e7c", // Hardware Support
		Options:    args.Options,
	}

	return s.runNQEQueryByID(queryArgs)
}

func (s *ForwardMCPService) getOSSupport(args GetOSSupportArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("get_os_support", args, nil)

	queryArgs := RunNQEQueryByIDArgs{
		NetworkID:  args.NetworkID,
		SnapshotID: args.SnapshotID,
		QueryID:    "FQ_fc33d9fd70ba19a18455b0e4d26ca8420003d9cc", // OS Support
		Options:    args.Options,
	}

	return s.runNQEQueryByID(queryArgs)
}

func (s *ForwardMCPService) searchConfigs(args SearchConfigsArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("search_configs", args, nil)

	queryArgs := RunNQEQueryByIDArgs{
		NetworkID:  args.NetworkID,
		SnapshotID: args.SnapshotID,
		QueryID:    "FQ_e636c47826ad7144f09eaf6bc14dfb0b560e7cc9", // Config Search
		Parameters: map[string]interface{}{
			"searchPattern": args.SearchTerm,
		},
		Options: args.Options,
	}

	return s.runNQEQueryByID(queryArgs)
}

func (s *ForwardMCPService) getConfigDiff(args GetConfigDiffArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("get_config_diff", args, nil)

	params := map[string]interface{}{}
	if args.AfterSnapshot != "" {
		params["compareSnapshotId"] = args.AfterSnapshot
	}

	queryArgs := RunNQEQueryByIDArgs{
		NetworkID:  args.NetworkID,
		SnapshotID: args.BeforeSnapshot,
		QueryID:    "FQ_51f090cbea069b4049eb283716ab3bbb3f578aea", // Config Diff
		Parameters: params,
		Options:    args.Options,
	}

	return s.runNQEQueryByID(queryArgs)
}

// Default Settings Management Tool Implementations

func (s *ForwardMCPService) getDefaultSettings(args GetDefaultSettingsArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("get_default_settings", args, nil)

	// Get network name if possible
	networkName := "Not set"
	if s.defaults.NetworkID != "" {
		networks, err := s.forwardClient.GetNetworks()
		if err == nil {
			for _, network := range networks {
				if network.ID == s.defaults.NetworkID {
					networkName = fmt.Sprintf("%s (%s)", network.Name, network.ID)
					break
				}
			}
		}
	}

	settings := map[string]interface{}{
		"default_network_id":   s.defaults.NetworkID,
		"default_network_name": networkName,
		"default_snapshot_id":  s.defaults.SnapshotID,
		"default_query_limit":  s.defaults.QueryLimit,
		"environment_source":   "Loaded from environment variables and config files",
	}

	result := MarshalCompactJSONString(settings)

	response := fmt.Sprintf("Current default settings:\n%s\n\n", result)
	response += "To change defaults:\n"
	response += "• Use set_default_network to change the default network\n"
	response += "• Update environment variables (FORWARD_DEFAULT_NETWORK_ID, etc.)\n"
	response += "• Modify your .env file or config.json\n\n"

	if s.defaults.NetworkID == "" {
		response += " No default network is set. Consider setting FORWARD_DEFAULT_NETWORK_ID in your environment."
	}

	return mcp.NewToolResponse(mcp.NewTextContent(response)), nil
}

func (s *ForwardMCPService) setDefaultNetwork(args SetDefaultNetworkArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("set_default_network", args, nil)

	var networkID string
	var networkName string

	// Try to resolve the network identifier (could be ID or name)
	if args.NetworkIdentifier == "" {
		return mcp.NewToolResponse(mcp.NewTextContent("Please provide either a network ID or network name.")), nil
	}

	// First, try as network ID by listing networks and checking if it exists
	networks, err := s.forwardClient.GetNetworks()
	if err != nil {
		return nil, fmt.Errorf("failed to get networks: %w", err)
	}

	// Check if it's a direct network ID match
	for _, network := range networks {
		if network.ID == args.NetworkIdentifier {
			networkID = network.ID
			networkName = network.Name
			break
		}
	}

	// If not found as ID, try to resolve as name
	if networkID == "" {
		resolvedID, err := s.resolveNetworkIDByName(args.NetworkIdentifier)
		if err != nil {
			// List available networks for user reference
			availableNetworks := "Available networks:\n"
			for i, network := range networks {
				availableNetworks += fmt.Sprintf("%d. %s (ID: %s)\n", i+1, network.Name, network.ID)
			}

			return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Network '%s' not found.\n\n%s\nPlease use either a valid network ID or exact network name.", args.NetworkIdentifier, availableNetworks))), nil
		}

		networkID = resolvedID
		// Find the network name
		for _, network := range networks {
			if network.ID == networkID {
				networkName = network.Name
				break
			}
		}
	}

	// Update the default (for this session)
	s.defaults.NetworkID = networkID

	response := "Default network updated successfully!\n\n"
	response += fmt.Sprintf("New default: %s (ID: %s)\n\n", networkName, networkID)
	response += "This change applies to the current session. To make it permanent:\n"
	response += fmt.Sprintf("• Set FORWARD_DEFAULT_NETWORK_ID=%s in your environment\n", networkID)
	response += "• Or update your .env file or config.json\n\n"
	response += "All subsequent tool calls will now use this network by default when network_id is not specified."

	return mcp.NewToolResponse(mcp.NewTextContent(response)), nil
}

// Semantic Cache and AI Enhancement Tool Implementations

// getCacheStats returns semantic cache performance statistics
func (s *ForwardMCPService) getCacheStats(args GetCacheStatsArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("get_cache_stats", args, nil)

	stats := s.semanticCache.GetStats()

	statsJSON := MarshalCompactJSONString(stats)

	summary := fmt.Sprintf("Semantic Cache Performance Statistics:\n%s\n\nCache Summary:\n", statsJSON)
	summary += fmt.Sprintf("• Total Queries: %v\n", stats["total_queries"])
	summary += fmt.Sprintf("• Hit Rate: %v\n", stats["hit_rate_percent"])
	summary += fmt.Sprintf("• Active Entries: %v/%v\n", stats["total_entries"], stats["max_entries"])
	summary += fmt.Sprintf("• Similarity Threshold: %v\n", stats["threshold"])

	return mcp.NewToolResponse(mcp.NewTextContent(summary)), nil
}

// suggestSimilarQueries provides intelligent query suggestions based on cache history
func (s *ForwardMCPService) suggestSimilarQueries(args SuggestSimilarQueriesArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("suggest_similar_queries", args, nil)

	if args.Query == "" {
		return nil, fmt.Errorf("query parameter is required")
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 5
	}

	similarQueries, err := s.semanticCache.FindSimilarQueries(args.Query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to find similar queries: %w", err)
	}

	if len(similarQueries) == 0 {
		return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("No similar queries found for: '%s'\n\nTry running some NQE queries first to build up the cache.", args.Query))), nil
	}

	response := fmt.Sprintf("Similar queries found for: '%s'\n\n", args.Query)
	for i, entry := range similarQueries {
		response += fmt.Sprintf("%d. (%.1f%% similarity) %s\n", i+1, entry.SimilarityScore*100, entry.Query)
		if entry.NetworkID != "" {
			response += fmt.Sprintf("   Network: %s", entry.NetworkID)
			if entry.SnapshotID != "" {
				response += fmt.Sprintf(", Snapshot: %s", entry.SnapshotID)
			}
			response += "\n"
		}
		response += fmt.Sprintf("   Used %d times, last accessed: %s\n\n", entry.AccessCount, entry.LastAccessed.Format("2006-01-02 15:04:05"))
	}

	response += "You can use these suggestions to refine your query or explore related network analysis patterns."

	return mcp.NewToolResponse(mcp.NewTextContent(response)), nil
}

// clearCache removes expired or all cache entries
func (s *ForwardMCPService) clearCache(args ClearCacheArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("clear_cache", args, nil)

	var removed int
	var operation string

	if args.ClearAll {
		// For simplicity, we'll implement a full clear by creating a new cache
		// In production, you might want a more sophisticated approach
		stats := s.semanticCache.GetStats()
		totalEntries := stats["total_entries"].(int)

		// Reinitialize the cache
		var embeddingService EmbeddingService
		if s.config.Forward.SemanticCache.EmbeddingProvider == "openai" {
			if openaiKey := os.Getenv("OPENAI_API_KEY"); openaiKey != "" {
				embeddingService = NewOpenAIEmbeddingService(openaiKey)
			} else {
				embeddingService = NewMockEmbeddingService()
			}
		} else if s.config.Forward.SemanticCache.EmbeddingProvider == "keyword" {
			embeddingService = NewKeywordEmbeddingService()
		} else {
			embeddingService = NewMockEmbeddingService()
		}
		s.semanticCache = NewSemanticCache(embeddingService, s.logger, s.instanceID, &s.config.Forward.SemanticCache)

		removed = totalEntries
		operation = "Cleared all cache entries"
	} else {
		removed = s.semanticCache.ClearExpired()
		operation = "Cleared expired cache entries"
	}

	response := fmt.Sprintf("%s: %d entries removed\n\n", operation, removed)

	// Show updated stats
	newStats := s.semanticCache.GetStats()
	response += "Updated cache status:\n"
	response += fmt.Sprintf("• Active entries: %v\n", newStats["total_entries"])
	response += fmt.Sprintf("• Hit rate: %v\n", newStats["hit_rate_percent"])

	return mcp.NewToolResponse(mcp.NewTextContent(response)), nil
}

// AI-Powered Query Discovery Tool Implementations

// searchNQEQueries performs AI-powered search through the NQE query library
func (s *ForwardMCPService) searchNQEQueries(args SearchNQEQueriesArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("search_nqe_queries", args, nil)

	// Inline readiness check
	if !s.queryIndex.IsReady() {
		return nil, fmt.Errorf("Query index is not initialized. Try running 'initialize_query_index' tool to manually initialize.")
	}

	if args.Query == "" {
		return mcp.NewToolResponse(mcp.NewTextContent("Please provide a search query describing what you want to analyze (e.g., 'AWS security vulnerabilities', 'BGP routing issues', 'interface statistics')")), nil
	}

	// Set default limit
	limit := args.Limit
	if limit <= 0 {
		limit = 10
	}

	// Initialize query index if needed
	stats := s.queryIndex.GetStatistics()
	totalQueries := stats["total_queries"].(int)
	if totalQueries == 0 {
		s.logger.Info("Query index empty, initializing...")
		if err := s.queryIndex.LoadFromSpec(); err != nil {
			return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Failed to initialize query index: %v\n\n**Manual Fix:** Run this command:\n```json\n{\"tool\": \"initialize_query_index\", \"arguments\": {\"generate_embeddings\": false}}\n```", err))), nil
		}
		s.logger.Info("Query index initialized successfully")
	}

	// Use semantic search if embeddings are available, otherwise fallback to keyword search
	results, err := s.queryIndex.SearchQueries(args.Query, limit)
	if err != nil {
		return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Search failed: %v", err))), nil
	}

	// Apply category/subcategory filters if specified
	var filteredResults []*QuerySearchResult
	categoryFilterApplied := args.Category != ""
	subcategoryFilterApplied := args.Subcategory != ""

	for _, result := range results {
		if categoryFilterApplied && !strings.EqualFold(result.Category, args.Category) {
			continue
		}
		if subcategoryFilterApplied && !strings.EqualFold(result.Subcategory, args.Subcategory) {
			continue
		}
		filteredResults = append(filteredResults, result)
	}

	if len(filteredResults) == 0 {
		return mcp.NewToolResponse(mcp.NewTextContent("No relevant NQE queries found for your search. Try different keywords or check your query index.")), nil
	}

	// Format the response
	response := fmt.Sprintf("%s search found %d relevant NQE queries for: '%s'\n\n",
		filteredResults[0].MatchType, len(filteredResults), args.Query)
	for i, result := range filteredResults {
		if i >= limit {
			break
		}
		response += fmt.Sprintf("**%d. %s** (%.1f%% match)\n   **Intent:** %s\n   **Description:** %s\n   **Category:** %s\n   **Query ID:** `%s`\n\n",
			i+1, result.Path, result.SimilarityScore*100, result.Intent, result.Description, result.Category, result.QueryID)
	}

	return mcp.NewToolResponse(mcp.NewTextContent(response)), nil
}

// initializeQueryIndex builds or rebuilds the AI-powered query index
func (s *ForwardMCPService) initializeQueryIndex(args InitializeQueryIndexArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("initialize_query_index", args, nil)

	response := "🔧 Initializing AI-powered NQE query index...\n\n"

	// Prefer database data over spec file if available
	var queries []forward.NQEQueryDetail
	var dataSource string

	if s.database != nil {
		response += "📊 Checking database for query data...\n"
		dbQueries, err := s.database.LoadQueries()
		if err != nil {
			response += fmt.Sprintf("⚠️  Database load failed: %v\n", err)
		} else if len(dbQueries) > 0 {
			queries = dbQueries
			dataSource = "database"
			response += fmt.Sprintf("✅ Found %d queries in database (includes enhanced metadata)\n", len(queries))

			// Count queries with enhanced metadata
			enhancedCount := 0
			for _, q := range dbQueries {
				if q.SourceCode != "" || q.Description != "" {
					enhancedCount++
				}
			}
			if enhancedCount > 0 {
				response += fmt.Sprintf("🚀 %d queries have enhanced metadata (source code/descriptions)\n", enhancedCount)
			}
		} else {
			response += "📭 Database is empty\n"
		}
	}

	// Fallback to spec file if no database data
	if len(queries) == 0 {
		response += "📖 Loading from spec file as fallback...\n"

		// Check if spec file exists using robust path resolution
		specPath, err := findSpecFile("NQELibrary.json")
		if err != nil {
			return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("No database data available and NQE spec file not found. Error: %v\n\n💡 **Solutions:**\n• Run 'hydrate_database' to load queries from API\n• Ensure the spec file exists in the 'spec' directory\n• Check that the MCP server is running from the correct directory", err))), nil
		}

		response += fmt.Sprintf("📁 Found spec file at: %s\n", specPath)

		if err := s.queryIndex.LoadFromSpec(); err != nil {
			return nil, fmt.Errorf("failed to load query index from spec: %w", err)
		}
		dataSource = "spec file"
	} else {
		// Load database queries into index
		if err := s.queryIndex.LoadFromQueries(queries); err != nil {
			return nil, fmt.Errorf("failed to load database queries into index: %w", err)
		}
	}

	stats := s.queryIndex.GetStatistics()
	totalQueries := stats["total_queries"].(int)
	embeddedQueries := stats["embedded_queries"].(int)

	response += fmt.Sprintf("✅ Loaded %d NQE queries successfully from %s\n", totalQueries, dataSource)

	if embeddedQueries > 0 {
		coverage := stats["embedding_coverage"].(float64)
		response += fmt.Sprintf("Found %d cached embeddings (%.1f%% coverage) for offline AI search\n", embeddedQueries, coverage*100)
	}
	response += "\n"

	// Generate embeddings if requested
	if args.GenerateEmbeddings {
		if _, ok := s.queryIndex.embeddingService.(*MockEmbeddingService); ok {
			response += "Cannot generate embeddings: OpenAI API key not configured\n"
			response += "Set OPENAI_API_KEY environment variable to enable embedding generation\n"
			response += "Current functionality limited to keyword-based search\n\n"
		} else {
			response += "Generating AI embeddings for semantic search...\n"
			response += "   This will take several minutes for thousands of queries\n"
			response += "   Embeddings will be cached for offline use\n\n"

			if err := s.queryIndex.GenerateEmbeddings(); err != nil {
				if strings.Contains(err.Error(), "cannot generate real embeddings") {
					response += "Embedding generation failed: OpenAI API key required\n"
					response += "   Set FORWARD_EMBEDDING_PROVIDER=keyword for basic functionality\n\n"
				} else {
					return nil, fmt.Errorf("failed to generate embeddings: %w", err)
				}
			} else {
				updatedStats := s.queryIndex.GetStatistics()
				newEmbeddedCount := updatedStats["embedded_queries"].(int)
				newCoverage := updatedStats["embedding_coverage"].(float64)

				response += fmt.Sprintf("Generated and cached %d embeddings (%.1f%% coverage)\n", newEmbeddedCount, newCoverage*100)
				response += "Embeddings saved to spec/nqe-embeddings.json for offline use\n\n"
			}
		}
	}

	// Show final statistics
	finalStats := s.queryIndex.GetStatistics()
	response += "📊 **Query Index Status:**\n"
	response += fmt.Sprintf("• Total queries: %d\n", finalStats["total_queries"].(int))

	if categories, ok := finalStats["categories"].(map[string]int); ok {
		response += "• Categories:\n"
		categoryCount := 0
		for category, count := range categories {
			if category != "" && categoryCount < 5 { // Show top 5 categories
				response += fmt.Sprintf("  - %s: %d queries\n", category, count)
				categoryCount++
			}
		}
		if len(categories) > 5 {
			response += fmt.Sprintf("  - ... and %d more categories\n", len(categories)-5)
		}
	}

	finalEmbedded := finalStats["embedded_queries"].(int)
	if finalEmbedded > 0 {
		finalCoverage := finalStats["embedding_coverage"].(float64)
		response += fmt.Sprintf("• AI embeddings: %d queries (%.1f%% coverage) 🧠\n", finalEmbedded, finalCoverage*100)
		response += "  → Full semantic search available\n"
	} else {
		response += "• AI embeddings: None available\n"
		response += "  → Using keyword-based search fallback\n"
	}

	response += "\n**Query index ready!**\n"
	if finalEmbedded > 0 {
		response += "Use `search_nqe_queries` for AI-powered semantic search\n"
		response += "Works offline with cached embeddings (no OpenAI API calls needed)\n"
	} else {
		response += "Use `search_nqe_queries` for keyword-based search\n"
		response += "Generate embeddings with OpenAI for better semantic matching\n"
	}

	return mcp.NewToolResponse(mcp.NewTextContent(response)), nil
}

// hydrateDatabase hydrates the database by loading queries from the Forward Networks API
func (s *ForwardMCPService) hydrateDatabase(args HydrateDatabaseArgs) (*mcp.ToolResponse, error) {
	if s.database == nil {
		return nil, fmt.Errorf("database is not available")
	}

	// Set defaults
	if args.MaxRetries == 0 {
		args.MaxRetries = 3
	}

	s.logger.Info("🔄 Starting database hydration (async mode)...")

	// Check if we need to force refresh or if database is empty
	existingQueries, err := s.database.LoadQueries()
	if err != nil {
		s.logger.Warn("🔄 Failed to load existing queries: %v", err)
		existingQueries = []forward.NQEQueryDetail{}
	}

	if len(existingQueries) > 0 && !args.ForceRefresh {
		return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Database already contains %d queries. Use force_refresh=true to refresh anyway.", len(existingQueries)))), nil
	}

	// Run hydration in background
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		var queries []forward.NQEQueryDetail
		var err error
		if args.EnhancedMode {
			existingCommitIDs := make(map[string]string)
			for _, query := range existingQueries {
				if query.LastCommit.ID != "" {
					existingCommitIDs[query.Path] = query.LastCommit.ID
				}
			}
			queries, err = s.forwardClient.GetNQEAllQueriesEnhancedWithCacheContext(ctx, existingCommitIDs)
			if err != nil {
				s.logger.Warn("🔄 Enhanced API failed, falling back to basic API: %v", err)
				queries, err = s.database.loadFromBasicAPI(s.forwardClient, s.logger)
			}
		} else {
			queries, err = s.database.loadFromBasicAPI(s.forwardClient, s.logger)
		}
		if err != nil {
			s.logger.Error("failed to load queries from API: %v", err)
			return
		}
		if !args.ForceRefresh && len(existingQueries) > 0 {
			queries = s.database.mergeQueries(existingQueries, queries)
		}
		if err := s.database.SaveQueries(queries); err != nil {
			s.logger.Error("failed to save queries to database: %v", err)
			return
		}
		if err := s.database.SetMetadata("last_sync", time.Now().Format(time.RFC3339)); err != nil {
			s.logger.Warn("🔄 Failed to update sync time: %v", err)
		}
		s.logger.Info("🔄 Database hydration completed with %d queries", len(queries))
		s.logger.Info("🔄 Refreshing query index after hydration...")
		if s.queryIndex != nil {
			if err := s.queryIndex.LoadFromQueries(queries); err != nil {
				s.logger.Warn("🔄 Failed to refresh query index: %v", err)
			} else {
				s.logger.Info("🔄 Query index refreshed with %d queries", len(queries))
				stats := s.queryIndex.GetStatistics()
				embeddedCount := stats["embedded_queries"].(int)
				if embeddedCount > 0 && embeddedCount < len(queries) {
					s.logger.Info("🧠 Consider regenerating embeddings to include new queries in semantic search")
				}
			}
		}
		if s.queryIndex != nil && args.RegenerateEmbeddings {
			s.logger.Info("🧠 Regenerating AI embeddings after hydration...")
			if _, ok := s.queryIndex.embeddingService.(*MockEmbeddingService); ok {
				s.logger.Warn("⚠️  Cannot generate embeddings: OpenAI API key not configured")
			} else {
				if err := s.queryIndex.GenerateEmbeddings(); err != nil {
					s.logger.Warn("🧠 Failed to regenerate embeddings: %v", err)
				} else {
					updatedStats := s.queryIndex.GetStatistics()
					newEmbeddedCount := updatedStats["embedded_queries"].(int)
					newCoverage := updatedStats["embedding_coverage"].(float64)
					s.logger.Info("🧠 Successfully regenerated %d embeddings (%.1f%% coverage)", newEmbeddedCount, newCoverage*100)
				}
			}
		}
		// Optionally: log completion
		s.logger.Info("Database hydration background process complete.")
	}()

	return mcp.NewToolResponse(mcp.NewTextContent("Database hydration has started in the background. This process may take several minutes. You can continue using other tools, or check the status with get_database_status. Once hydration is complete, the query index will be refreshed automatically.")), nil
}

// refreshQueryIndex refreshes the query index from the current database content
func (s *ForwardMCPService) refreshQueryIndex(args RefreshQueryIndexArgs) (*mcp.ToolResponse, error) {
	if s.database == nil {
		return nil, fmt.Errorf("database is not available")
	}

	if s.queryIndex == nil {
		return nil, fmt.Errorf("query index is not available")
	}

	s.logger.Info("🔄 Refreshing query index from database...")

	// Load queries from database
	queries, err := s.database.LoadQueries()
	if err != nil {
		return nil, fmt.Errorf("failed to load queries from database: %w", err)
	}

	if len(queries) == 0 {
		return mcp.NewToolResponse(mcp.NewTextContent("No queries found in database. Use hydrate_database to load queries first.")), nil
	}

	// Load queries into index
	if err := s.queryIndex.LoadFromQueries(queries); err != nil {
		return nil, fmt.Errorf("failed to load queries into index: %w", err)
	}

	s.logger.Info("🔄 Query index refreshed with %d queries", len(queries))

	return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Query index refreshed successfully with %d queries.", len(queries)))), nil
}

// getDatabaseStatus returns the current status of the database and query index
func (s *ForwardMCPService) getDatabaseStatus(args GetDatabaseStatusArgs) (*mcp.ToolResponse, error) {
	status := map[string]interface{}{
		"database_available":    s.database != nil,
		"query_index_available": s.queryIndex != nil,
		"timestamp":             time.Now().Format(time.RFC3339),
	}

	if s.database != nil {
		// Get database stats
		queries, err := s.database.LoadQueries()
		if err != nil {
			status["database_error"] = err.Error()
			status["query_count"] = 0
		} else {
			status["query_count"] = len(queries)
		}

		// Get last sync time
		if lastSync, err := s.database.GetMetadata("last_sync"); err == nil {
			status["last_sync"] = lastSync
		}

		// Get database path
		status["database_path"] = s.database.dbPath
	}

	if s.queryIndex != nil {
		// Get query index stats
		status["query_index_empty"] = !s.queryIndex.IsReady()
		status["query_index_loading"] = s.queryIndex.IsLoading()

		// Get index stats if available
		indexStats := s.queryIndex.GetStatistics()
		if indexStats != nil {
			status["index_stats"] = indexStats
		}
	}

	// Marshal to JSON for pretty output
	statusJSON, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal status: %w", err)
	}

	return mcp.NewToolResponse(mcp.NewTextContent(string(statusJSON))), nil
}

// Memory Management Tool Implementations

// createEntity creates a new entity in the knowledge graph
func (s *ForwardMCPService) createEntity(args CreateEntityArgs) (*mcp.ToolResponse, error) {
	if s.memorySystem == nil {
		return nil, fmt.Errorf("memory system is not available")
	}

	entity, err := s.memorySystem.CreateEntity(args.Name, args.Type, args.Metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to create entity: %w", err)
	}

	entityJSON, err := json.MarshalIndent(entity, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal entity: %w", err)
	}

	return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Entity created successfully:\n%s", string(entityJSON)))), nil
}

// createRelation creates a relation between two entities
func (s *ForwardMCPService) createRelation(args CreateRelationArgs) (*mcp.ToolResponse, error) {
	if s.memorySystem == nil {
		return nil, fmt.Errorf("memory system is not available")
	}

	relation, err := s.memorySystem.CreateRelation(args.FromID, args.ToID, args.Type, args.Properties)
	if err != nil {
		return nil, fmt.Errorf("failed to create relation: %w", err)
	}

	relationJSON, err := json.MarshalIndent(relation, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal relation: %w", err)
	}

	return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Relation created successfully:\n%s", string(relationJSON)))), nil
}

// addObservation adds an observation to an entity
func (s *ForwardMCPService) addObservation(args AddObservationArgs) (*mcp.ToolResponse, error) {
	if s.memorySystem == nil {
		return nil, fmt.Errorf("memory system is not available")
	}

	observation, err := s.memorySystem.AddObservation(args.EntityID, args.Content, args.Type, args.Metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to add observation: %w", err)
	}

	observationJSON, err := json.MarshalIndent(observation, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal observation: %w", err)
	}

	return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Observation added successfully:\n%s", string(observationJSON)))), nil
}

// searchEntities searches for entities in the knowledge graph with automatic bloom filter optimization
func (s *ForwardMCPService) searchEntities(args SearchEntitiesArgs) (*mcp.ToolResponse, error) {
	if s.memorySystem == nil {
		return nil, fmt.Errorf("memory system is not available")
	}

	// Check if we have bloom filters available for NQE result entities
	if args.EntityType == "nqe_result" && s.bloomManager != nil {
		// Try to use bloom filter for faster searching
		networkID := s.getNetworkID("")
		if networkID != "" {
			// Check if we have any bloom filters for this network
			stats := s.bloomManager.GetFilterStats()
			for filterKey, metadata := range stats {
				if strings.Contains(filterKey, networkID) {
					// We have a bloom filter, use it for searching
					s.logger.Debug("Using bloom filter for entity search: %s", filterKey)

					// Extract search terms from the query
					searchTerms := s.extractSearchTerms(args.Query)
					if len(searchTerms) > 0 {
						// Use bloom filter search
						filterType := metadata.FilterType
						searchResult, err := s.bloomManager.SearchFilter(networkID, filterType, searchTerms, nil)
						if err == nil && searchResult.MatchedCount > 0 {
							// Bloom filter found matches, now get the actual entities
							entities, err := s.memorySystem.SearchEntities(args.Query, args.EntityType, args.Limit)
							if err != nil {
								return nil, fmt.Errorf("failed to search entities after bloom filter: %w", err)
							}

							response := fmt.Sprintf("🔍 Bloom filter search completed in %v!\n", searchResult.SearchTime)
							response += fmt.Sprintf("📊 Found %d potential matches (bloom filter)\n", searchResult.MatchedCount)
							response += fmt.Sprintf("📋 Retrieved %d entities:\n", len(entities))

							entitiesJSON, err := json.MarshalIndent(entities, "", "  ")
							if err != nil {
								return nil, fmt.Errorf("failed to marshal entities: %w", err)
							}
							response += string(entitiesJSON)

							return mcp.NewToolResponse(mcp.NewTextContent(response)), nil
						}
					}
				}
			}
		}
	}

	// Fallback to regular search
	entities, err := s.memorySystem.SearchEntities(args.Query, args.EntityType, args.Limit)
	if err != nil {
		return nil, fmt.Errorf("failed to search entities: %w", err)
	}

	if len(entities) == 0 {
		return mcp.NewToolResponse(mcp.NewTextContent("No entities found matching the search criteria.")), nil
	}

	entitiesJSON, err := json.MarshalIndent(entities, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal entities: %w", err)
	}

	return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Found %d entities:\n%s", len(entities), string(entitiesJSON)))), nil
}

// getEntity retrieves a specific entity by ID or name
func (s *ForwardMCPService) getEntity(args GetEntityArgs) (*mcp.ToolResponse, error) {
	if s.memorySystem == nil {
		return nil, fmt.Errorf("memory system is not available")
	}

	entity, err := s.memorySystem.GetEntity(args.Identifier)
	if err != nil {
		return nil, fmt.Errorf("failed to get entity: %w", err)
	}

	entityJSON, err := json.MarshalIndent(entity, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal entity: %w", err)
	}

	return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Entity found:\n%s", string(entityJSON)))), nil
}

// getRelations retrieves relations for an entity
func (s *ForwardMCPService) getRelations(args GetRelationsArgs) (*mcp.ToolResponse, error) {
	if s.memorySystem == nil {
		return nil, fmt.Errorf("memory system is not available")
	}

	relations, err := s.memorySystem.GetRelations(args.EntityID, args.RelationType)
	if err != nil {
		return nil, fmt.Errorf("failed to get relations: %w", err)
	}

	if len(relations) == 0 {
		return mcp.NewToolResponse(mcp.NewTextContent("No relations found for this entity.")), nil
	}

	relationsJSON, err := json.MarshalIndent(relations, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal relations: %w", err)
	}

	return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Found %d relations:\n%s", len(relations), string(relationsJSON)))), nil
}

// getObservations retrieves observations for an entity
func (s *ForwardMCPService) getObservations(args GetObservationsArgs) (*mcp.ToolResponse, error) {
	if s.memorySystem == nil {
		return nil, fmt.Errorf("memory system is not available")
	}

	observations, err := s.memorySystem.GetObservations(args.EntityID, args.ObservationType)
	if err != nil {
		return nil, fmt.Errorf("failed to get observations: %w", err)
	}

	if len(observations) == 0 {
		return mcp.NewToolResponse(mcp.NewTextContent("No observations found for this entity.")), nil
	}

	observationsJSON, err := json.MarshalIndent(observations, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal observations: %w", err)
	}

	return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Found %d observations:\n%s", len(observations), string(observationsJSON)))), nil
}

// deleteEntity deletes an entity and all its relations and observations
func (s *ForwardMCPService) deleteEntity(args DeleteEntityArgs) (*mcp.ToolResponse, error) {
	if s.memorySystem == nil {
		return nil, fmt.Errorf("memory system is not available")
	}

	// Get entity details before deletion for confirmation
	entity, err := s.memorySystem.GetEntity(args.EntityID)
	if err != nil {
		return nil, fmt.Errorf("entity not found: %w", err)
	}

	err = s.memorySystem.DeleteEntity(args.EntityID)
	if err != nil {
		return nil, fmt.Errorf("failed to delete entity: %w", err)
	}

	return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Entity '%s' (%s) deleted successfully, including all its relations and observations.", entity.Name, entity.Type))), nil
}

// deleteRelation deletes a specific relation
func (s *ForwardMCPService) deleteRelation(args DeleteRelationArgs) (*mcp.ToolResponse, error) {
	if s.memorySystem == nil {
		return nil, fmt.Errorf("memory system is not available")
	}

	err := s.memorySystem.DeleteRelation(args.RelationID)
	if err != nil {
		return nil, fmt.Errorf("failed to delete relation: %w", err)
	}

	return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Relation '%s' deleted successfully.", args.RelationID))), nil
}

// deleteObservation deletes a specific observation
func (s *ForwardMCPService) deleteObservation(args DeleteObservationArgs) (*mcp.ToolResponse, error) {
	if s.memorySystem == nil {
		return nil, fmt.Errorf("memory system is not available")
	}

	err := s.memorySystem.DeleteObservation(args.ObservationID)
	if err != nil {
		return nil, fmt.Errorf("failed to delete observation: %w", err)
	}

	return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Observation '%s' deleted successfully.", args.ObservationID))), nil
}

// getMemoryStats returns statistics about the memory system
func (s *ForwardMCPService) getMemoryStats(args GetMemoryStatsArgs) (*mcp.ToolResponse, error) {
	if s.memorySystem == nil {
		return nil, fmt.Errorf("memory system is not available")
	}

	stats, err := s.memorySystem.GetMemoryStats()
	if err != nil {
		return nil, fmt.Errorf("failed to get memory stats: %w", err)
	}

	statsJSON, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal stats: %w", err)
	}

	return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Memory system statistics:\n%s", string(statsJSON)))), nil
}

// getQueryAnalytics gets analytics about query patterns for a network
func (s *ForwardMCPService) getQueryAnalytics(args GetQueryAnalyticsArgs) (*mcp.ToolResponse, error) {
	if s.apiTracker == nil {
		return nil, fmt.Errorf("API memory tracker is not available")
	}

	analytics, err := s.apiTracker.GetQueryAnalytics(args.NetworkID)
	if err != nil {
		return nil, fmt.Errorf("failed to get query analytics: %w", err)
	}

	analyticsJSON, err := json.MarshalIndent(analytics, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal analytics: %w", err)
	}

	return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Query analytics for network %s:\n%s", args.NetworkID, string(analyticsJSON)))), nil
}

// getNQEResultChunks retrieves chunked NQE query results from the memory system
func (s *ForwardMCPService) getNQEResultChunks(args GetNQEResultChunksArgs) (*mcp.ToolResponse, error) {
	if s.memorySystem == nil {
		return nil, fmt.Errorf("memory system is not available")
	}

	entityID := args.EntityID
	// If entity_id is not provided, try to look up by query_id/network_id/snapshot_id
	if entityID == "" && args.QueryID != "" && args.NetworkID != "" && args.SnapshotID != "" {
		lookupName := fmt.Sprintf("%s-%s-%s", args.QueryID, args.NetworkID, args.SnapshotID)
		entity, err := s.memorySystem.getEntityByName(lookupName)
		if err != nil {
			return nil, fmt.Errorf("could not find result entity for query/network/snapshot: %w", err)
		}
		entityID = entity.ID
	}

	if entityID == "" {
		return nil, fmt.Errorf("must provide either entity_id or (query_id, network_id, snapshot_id)")
	}

	chunks, err := s.memorySystem.GetNQEResultChunks(entityID)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve result chunks: %w", err)
	}

	// If chunk_index is provided, return only that chunk
	if args.ChunkIndex != nil {
		idx := *args.ChunkIndex
		if idx < 0 || idx >= len(chunks) {
			return nil, fmt.Errorf("chunk_index %d out of range (total chunks: %d)", idx, len(chunks))
		}
		return mcp.NewToolResponse(mcp.NewTextContent(chunks[idx])), nil
	}

	// Otherwise, return all chunks as a JSON array
	chunksJSON, _ := json.Marshal(chunks)
	return mcp.NewToolResponse(mcp.NewTextContent(string(chunksJSON))), nil
}

// Add get_nqe_result_summary tool handler
// Arguments: entity_id OR (query_id, network_id, snapshot_id)
func (s *ForwardMCPService) getNQEResultSummary(args GetNQEResultChunksArgs) (*mcp.ToolResponse, error) {
	if s.memorySystem == nil {
		return nil, fmt.Errorf("memory system is not available")
	}
	entityID := args.EntityID
	if entityID == "" && args.QueryID != "" && args.NetworkID != "" && args.SnapshotID != "" {
		lookupName := fmt.Sprintf("%s-%s-%s", args.QueryID, args.NetworkID, args.SnapshotID)
		entity, err := s.memorySystem.getEntityByName(lookupName)
		if err != nil {
			return nil, fmt.Errorf("could not find result entity for query/network/snapshot: %w", err)
		}
		entityID = entity.ID
	}
	if entityID == "" {
		return nil, fmt.Errorf("must provide either entity_id or (query_id, network_id, snapshot_id)")
	}
	// Get summary observation
	obs, err := s.memorySystem.GetObservations(entityID, "nqe_result_summary")
	if err != nil || len(obs) == 0 {
		return nil, fmt.Errorf("no summary found for entity %s", entityID)
	}

	response := fmt.Sprintf("NQE result summary for entity %s:\n%s", entityID, obs[0].Content)

	// Check if bloom filter is available for this data
	if s.bloomManager != nil {
		networkID := s.getNetworkID(args.NetworkID)
		if networkID != "" {
			stats := s.bloomManager.GetFilterStats()
			for filterKey, metadata := range stats {
				if strings.Contains(filterKey, networkID) {
					response += fmt.Sprintf("\n\n🔍 Bloom Filter Available!\n")
					response += fmt.Sprintf("- Filter Type: %s\n", metadata.FilterType)
					response += fmt.Sprintf("- Items Indexed: %d\n", metadata.ItemCount)
					response += fmt.Sprintf("- Memory Usage: %s\n", formatBytes(metadata.MemoryUsage))
					response += fmt.Sprintf("- Last Updated: %v\n", metadata.LastUpdated)
					response += fmt.Sprintf("\n💡 Use search_bloom_filter for sub-millisecond searches!")
					break
				}
			}
		}
	}

	return mcp.NewToolResponse(mcp.NewTextContent(response)), nil
}

// Add analyze_nqe_result_sql tool handler
type AnalyzeNQEResultSQLArgs struct {
	EntityID string `json:"entity_id"`
	SQLQuery string `json:"sql_query"`
}

func (s *ForwardMCPService) analyzeNQEResultSQL(args AnalyzeNQEResultSQLArgs) (*mcp.ToolResponse, error) {
	if s.memorySystem == nil {
		return nil, fmt.Errorf("memory system is not available")
	}
	if args.EntityID == "" || args.SQLQuery == "" {
		return nil, fmt.Errorf("entity_id and sql_query are required")
	}
	// Get all chunks for the entity
	chunks, err := s.memorySystem.GetNQEResultChunks(args.EntityID)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve result chunks: %w", err)
	}
	if len(chunks) == 0 {
		return nil, fmt.Errorf("no data found for entity %s", args.EntityID)
	}
	// Parse all rows from all chunks
	var allRows []map[string]interface{}
	for _, chunk := range chunks {
		var rows []map[string]interface{}
		if err := json.Unmarshal([]byte(chunk), &rows); err != nil {
			return nil, fmt.Errorf("failed to unmarshal chunk: %w", err)
		}
		allRows = append(allRows, rows...)
	}
	if len(allRows) == 0 {
		return nil, fmt.Errorf("no rows found for entity %s", args.EntityID)
	}
	// Create in-memory SQLite DB
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("failed to create in-memory sqlite db: %w", err)
	}
	defer db.Close()
	// Infer columns from first row
	firstRow := allRows[0]
	var columns []string
	for k := range firstRow {
		columns = append(columns, k)
	}
	// Create table
	tableCols := ""
	for i, col := range columns {
		if i > 0 {
			tableCols += ", "
		}
		tableCols += fmt.Sprintf("%s TEXT", col)
	}
	tableName := "nqe_result"
	createStmt := fmt.Sprintf("CREATE TABLE %s (%s);", tableName, tableCols)
	_, err = db.Exec(createStmt)
	if err != nil {
		return nil, fmt.Errorf("failed to create table: %w", err)
	}
	// Insert rows
	insertStmt := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", tableName, strings.Join(columns, ", "), strings.TrimRight(strings.Repeat("?,", len(columns)), ","))
	for _, row := range allRows {
		vals := make([]interface{}, len(columns))
		for i, col := range columns {
			if v, ok := row[col]; ok {
				vals[i] = fmt.Sprintf("%v", v)
			} else {
				vals[i] = nil
			}
		}
		_, err := db.Exec(insertStmt, vals...)
		if err != nil {
			return nil, fmt.Errorf("failed to insert row: %w", err)
		}
	}
	// Run the query (limit to 100 rows)
	query := args.SQLQuery
	if !strings.Contains(strings.ToLower(query), "limit") {
		query += " LIMIT 100"
	}
	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("SQL query error: %w", err)
	}
	defer rows.Close()
	// Read results
	resultRows := []map[string]interface{}{}
	cols, _ := rows.Columns()
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		valPtrs := make([]interface{}, len(cols))
		for i := range vals {
			valPtrs[i] = &vals[i]
		}
		if err := rows.Scan(valPtrs...); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		rowMap := map[string]interface{}{}
		for i, col := range cols {
			rowMap[col] = vals[i]
		}
		resultRows = append(resultRows, rowMap)
	}
	resultJSON, _ := json.MarshalIndent(resultRows, "", "  ")
	response := fmt.Sprintf("SQL query result (%d rows, max 100 shown):\n%s", len(resultRows), string(resultJSON))
	return mcp.NewToolResponse(mcp.NewTextContent(response)), nil
}

// buildBloomFilter builds a bloom filter from NQE query results
func (s *ForwardMCPService) buildBloomFilter(args BuildBloomFilterArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("build_bloom_filter", args, nil)

	if s.bloomManager == nil {
		return nil, fmt.Errorf("bloom search manager is not available")
	}

	// Use defaults if not specified
	networkID := s.getNetworkID(args.NetworkID)
	chunkSize := args.ChunkSize
	if chunkSize <= 0 {
		chunkSize = 200 // Default chunk size
	}

	// Run the NQE query to get data for building the filter
	params := &forward.NQEQueryParams{
		NetworkID:  networkID,
		QueryID:    args.QueryID,
		SnapshotID: s.getSnapshotID(""),
		Options: &forward.NQEQueryOptions{
			Limit: 1000, // Reasonable limit for filter building
		},
	}

	result, err := s.forwardClient.RunNQEQueryByID(params)
	if err != nil {
		return nil, fmt.Errorf("failed to run NQE query for filter building: %w", err)
	}

	if len(result.Items) == 0 {
		return nil, fmt.Errorf("no data found for building bloom filter")
	}

	// Build the bloom filter
	err = s.bloomManager.BuildFilterFromNQEResult(networkID, args.FilterType, result, chunkSize)
	if err != nil {
		return nil, fmt.Errorf("failed to build bloom filter: %w", err)
	}

	// Get filter stats
	stats := s.bloomManager.GetFilterStats()
	filterKey := fmt.Sprintf("%s-%s", networkID, args.FilterType)
	metadata := stats[filterKey]

	response := fmt.Sprintf("✅ Bloom filter built successfully!\n\n"+
		"**Filter Details:**\n"+
		"- Network ID: %s\n"+
		"- Filter Type: %s\n"+
		"- Items Processed: %d\n"+
		"- Memory Usage: %d bytes\n"+
		"- False Positive Rate: %.2f%%\n"+
		"- Chunks: %d\n\n"+
		"**Next Steps:**\n"+
		"Use `search_bloom_filter` to efficiently search this dataset with sub-millisecond performance.",
		networkID, args.FilterType, metadata.ItemCount, metadata.MemoryUsage,
		metadata.FalsePositiveRate*100, metadata.ChunkCount)

	return mcp.NewToolResponse(mcp.NewTextContent(response)), nil
}

// searchBloomFilter searches a bloom filter for matching items
func (s *ForwardMCPService) searchBloomFilter(args SearchBloomFilterArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("search_bloom_filter", args, nil)

	if s.bloomManager == nil {
		return nil, fmt.Errorf("bloom search manager is not available")
	}

	if s.memorySystem == nil {
		return nil, fmt.Errorf("memory system is not available")
	}

	// Use defaults if not specified
	networkID := s.getNetworkID(args.NetworkID)

	// Check if filter exists
	if !s.bloomManager.IsFilterAvailable(networkID, args.FilterType) {
		return nil, fmt.Errorf("no bloom filter found for %s (network: %s). Use build_bloom_filter first.", args.FilterType, networkID)
	}

	// Get the full dataset from memory system
	chunks, err := s.memorySystem.GetNQEResultChunks(args.EntityID)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve result chunks: %w", err)
	}

	if len(chunks) == 0 {
		return nil, fmt.Errorf("no data found for entity %s", args.EntityID)
	}

	// Parse all rows from all chunks
	var allItems []map[string]interface{}
	for _, chunk := range chunks {
		var rows []map[string]interface{}
		if err := json.Unmarshal([]byte(chunk), &rows); err != nil {
			return nil, fmt.Errorf("failed to unmarshal chunk: %w", err)
		}
		allItems = append(allItems, rows...)
	}

	// Search the bloom filter
	searchResult, err := s.bloomManager.SearchFilter(networkID, args.FilterType, args.SearchTerms, allItems)
	if err != nil {
		return nil, fmt.Errorf("failed to search bloom filter: %w", err)
	}

	// Format response
	response := fmt.Sprintf("🔍 Bloom Search Results\n\n"+
		"**Search Performance:**\n"+
		"- Search Time: %v\n"+
		"- Total Items: %d\n"+
		"- Matched Items: %d\n"+
		"- Search Terms: %v\n\n"+
		"**Filter Stats:**\n"+
		"- Network ID: %s\n"+
		"- Filter Type: %s\n"+
		"- Memory Usage: %d bytes\n"+
		"- False Positive Rate: %.2f%%\n\n"+
		"**Matched Items (%d):**\n",
		searchResult.SearchTime, searchResult.TotalItems, searchResult.MatchedCount,
		args.SearchTerms, searchResult.FilterStats.NetworkID, searchResult.FilterStats.FilterType,
		searchResult.FilterStats.MemoryUsage, searchResult.FilterStats.FalsePositiveRate*100,
		len(searchResult.MatchedItems))

	// Add matched items (limit to first 10 for display)
	displayLimit := 10
	if len(searchResult.MatchedItems) < displayLimit {
		displayLimit = len(searchResult.MatchedItems)
	}

	for i := 0; i < displayLimit; i++ {
		itemJSON, _ := json.MarshalIndent(searchResult.MatchedItems[i], "", "  ")
		response += fmt.Sprintf("%d. %s\n", i+1, string(itemJSON))
	}

	if len(searchResult.MatchedItems) > displayLimit {
		response += fmt.Sprintf("\n... and %d more items (use analyze_nqe_result_sql for full analysis)\n",
			len(searchResult.MatchedItems)-displayLimit)
	}

	return mcp.NewToolResponse(mcp.NewTextContent(response)), nil
}

// getBloomFilterStats returns statistics for all bloom filters
func (s *ForwardMCPService) getBloomFilterStats(args GetBloomFilterStatsArgs) (*mcp.ToolResponse, error) {
	s.logToolCall("get_bloom_filter_stats", args, nil)

	if s.bloomManager == nil {
		return nil, fmt.Errorf("bloom search manager is not available")
	}

	stats := s.bloomManager.GetFilterStats()
	totalMemory := s.bloomManager.GetMemoryUsage()

	if len(stats) == 0 {
		return mcp.NewToolResponse(mcp.NewTextContent("No bloom filters found. Use `build_bloom_filter` to create filters for efficient searching.")), nil
	}

	response := fmt.Sprintf("📊 Bloom Filter Statistics\n\n"+
		"**Overall Stats:**\n"+
		"- Total Filters: %d\n"+
		"- Total Memory Usage: %d bytes (%.2f MB)\n\n"+
		"**Filter Details:**\n",
		len(stats), totalMemory, float64(totalMemory)/(1024*1024))

	for key, metadata := range stats {
		response += fmt.Sprintf("**%s**\n"+
			"- Network ID: %s\n"+
			"- Filter Type: %s\n"+
			"- Items: %d\n"+
			"- Memory: %d bytes\n"+
			"- False Positive Rate: %.2f%%\n"+
			"- Last Updated: %s\n"+
			"- Chunks: %d\n\n",
			key, metadata.NetworkID, metadata.FilterType, metadata.ItemCount,
			metadata.MemoryUsage, metadata.FalsePositiveRate*100,
			metadata.LastUpdated.Format("2006-01-02 15:04:05"), metadata.ChunkCount)
	}

	response += "**Performance Benefits:**\n" +
		"- Sub-millisecond search performance\n" +
		"- Memory-efficient filtering\n" +
		"- Reduced API calls for large datasets\n" +
		"- Pre-filtering before SQL analysis"

	return mcp.NewToolResponse(mcp.NewTextContent(response)), nil
}

// determineFilterType determines the appropriate filter type based on query ID and data content
func (s *ForwardMCPService) determineFilterType(queryID string, items []map[string]interface{}) string {
	// Check query ID patterns first
	if strings.Contains(queryID, "device") || strings.Contains(queryID, "devices") {
		return "device"
	}
	if strings.Contains(queryID, "interface") || strings.Contains(queryID, "interfaces") {
		return "interface"
	}
	if strings.Contains(queryID, "config") || strings.Contains(queryID, "configuration") {
		return "config"
	}
	if strings.Contains(queryID, "route") || strings.Contains(queryID, "routing") {
		return "route"
	}
	if strings.Contains(queryID, "vlan") {
		return "vlan"
	}
	if strings.Contains(queryID, "acl") || strings.Contains(queryID, "firewall") {
		return "security"
	}

	// Fallback: analyze the actual data structure
	if len(items) > 0 {
		item := items[0]
		if _, hasDevice := item["device_name"]; hasDevice {
			return "device"
		}
		if _, hasInterface := item["interface_name"]; hasInterface {
			return "interface"
		}
		if _, hasConfig := item["configuration"]; hasConfig {
			return "config"
		}
	}

	// Default to generic type
	return "data"
}

// extractSearchTerms extracts meaningful search terms from a query string
func (s *ForwardMCPService) extractSearchTerms(query string) []string {
	if query == "" {
		return nil
	}

	// Split by common delimiters and clean up
	terms := strings.FieldsFunc(query, func(r rune) bool {
		return r == ' ' || r == ',' || r == ';' || r == '|' || r == '&'
	})

	var cleanTerms []string
	for _, term := range terms {
		// Remove common stop words and short terms
		term = strings.TrimSpace(term)
		if len(term) > 2 && !s.isStopWord(term) {
			cleanTerms = append(cleanTerms, strings.ToLower(term))
		}
	}

	return cleanTerms
}

// isStopWord checks if a word is a common stop word
func (s *ForwardMCPService) isStopWord(word string) bool {
	stopWords := map[string]bool{
		"the": true, "and": true, "or": true, "but": true, "in": true, "on": true, "at": true,
		"to": true, "for": true, "of": true, "with": true, "by": true, "is": true, "are": true,
		"was": true, "were": true, "be": true, "been": true, "have": true, "has": true, "had": true,
		"do": true, "does": true, "did": true, "will": true, "would": true, "could": true, "should": true,
		"a": true, "an": true, "this": true, "that": true, "these": true, "those": true,
		"all": true, "any": true, "some": true, "no": true, "not": true, "only": true, "just": true,
	}
	return stopWords[strings.ToLower(word)]
}

// formatBytes formats bytes into human-readable format
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
