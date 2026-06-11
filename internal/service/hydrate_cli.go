package service

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/forward-mcp/internal/config"
	"github.com/forward-mcp/internal/forward"
	"github.com/forward-mcp/internal/logger"
)

// HydrateCLIOptions controls a one-shot hydration run executed as a CLI
// subcommand, outside the MCP server lifecycle. The process model is the
// durability mechanism: a client respawn of the MCP server cannot kill a
// hydration running in its own process.
type HydrateCLIOptions struct {
	Enhanced   bool          // fetch full metadata (one API call per changed query)
	Embeddings bool          // regenerate the embedding cache after syncing
	Force      bool          // ignore stored commit IDs and refetch everything
	Timeout    time.Duration // overall budget for the API fetch
}

// RunHydrateCLI synchronously hydrates the NQE query database from the
// Forward Networks API and optionally regenerates the embedding cache.
// The MCP server only ever reads this database; this is the single writer.
func RunHydrateCLI(cfg *config.Config, log *logger.Logger, opts HydrateCLIOptions) error {
	instanceID := cfg.Forward.InstanceID
	if instanceID == "" {
		instanceID = GenerateInstanceID(cfg.Forward.APIBaseURL)
	}
	fmt.Printf("Hydrating NQE database (instance %s, API %s)\n", instanceID, cfg.Forward.APIBaseURL)

	if cfg.Forward.APIKey == "" || cfg.Forward.APISecret == "" {
		return fmt.Errorf("FORWARD_API_KEY / FORWARD_API_SECRET not configured (check .env or environment)")
	}

	client := forward.NewClient(&cfg.Forward)

	database, err := NewNQEDatabase(log, instanceID)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close()

	existing, err := database.LoadQueries()
	if err != nil {
		log.Warn("Failed to load existing queries (continuing with empty set): %v", err)
		existing = nil
	}
	fmt.Printf("Existing queries in database: %d\n", len(existing))

	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()

	var fetched []forward.NQEQueryDetail
	if opts.Enhanced {
		commitIDs := make(map[string]string)
		if !opts.Force {
			for _, q := range existing {
				if q.LastCommit.ID != "" {
					commitIDs[q.Path] = q.LastCommit.ID
				}
			}
		}
		fmt.Printf("Fetching enhanced metadata (timeout %s, %d commit IDs for incremental skip)...\n", opts.Timeout, len(commitIDs))
		fetched, err = client.GetNQEAllQueriesEnhancedWithCacheContext(ctx, commitIDs)
		if err != nil {
			return fmt.Errorf("enhanced fetch failed: %w (re-run without --enhanced for a basic sync)", err)
		}
	} else {
		fmt.Println("Fetching query list (basic mode)...")
		fetched, err = database.loadFromBasicAPI(client, log)
		if err != nil {
			return fmt.Errorf("basic fetch failed: %w", err)
		}
	}
	fmt.Printf("Fetched %d queries from API\n", len(fetched))

	merged := mergeQueriesPreservingMetadata(existing, fetched)
	if err := database.SaveQueries(merged); err != nil {
		return fmt.Errorf("failed to save queries: %w", err)
	}
	if err := database.SetMetadata("last_sync", time.Now().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("failed to update last_sync: %w", err)
	}
	fmt.Printf("Saved %d queries, last_sync updated\n", len(merged))

	if opts.Embeddings {
		if err := regenerateEmbeddingsCLI(cfg, log, merged); err != nil {
			return fmt.Errorf("embedding generation failed: %w", err)
		}
	}

	fmt.Println("Hydration complete. If an MCP server is running, call the refresh_query_index tool (or restart the client) to pick up the new data.")
	return nil
}

// mergeQueriesPreservingMetadata merges fetched queries over existing ones
// without letting a metadata-poor fetch (basic API) clobber enriched rows:
// incoming fields only win when they carry data.
func mergeQueriesPreservingMetadata(existing, fetched []forward.NQEQueryDetail) []forward.NQEQueryDetail {
	byID := make(map[string]forward.NQEQueryDetail, len(existing)+len(fetched))
	for _, q := range existing {
		byID[q.QueryID] = q
	}
	for _, incoming := range fetched {
		if prior, ok := byID[incoming.QueryID]; ok {
			if incoming.Intent == "" {
				incoming.Intent = prior.Intent
			}
			if incoming.Description == "" {
				incoming.Description = prior.Description
			}
			if incoming.SourceCode == "" {
				incoming.SourceCode = prior.SourceCode
			}
			if incoming.LastCommit.ID == "" {
				incoming.LastCommit = prior.LastCommit
			}
		}
		byID[incoming.QueryID] = incoming
	}

	result := make([]forward.NQEQueryDetail, 0, len(byID))
	for _, q := range byID {
		result = append(result, q)
	}
	return result
}

// regenerateEmbeddingsCLI builds a query index from the merged queries and
// writes the embedding cache file, using the same provider selection as the
// server (keyword by default, OpenAI when configured with a key).
func regenerateEmbeddingsCLI(cfg *config.Config, log *logger.Logger, queries []forward.NQEQueryDetail) error {
	var embeddingService EmbeddingService
	if cfg.Forward.SemanticCache.EmbeddingProvider == "openai" {
		if key := os.Getenv("OPENAI_API_KEY"); key != "" {
			embeddingService = NewOpenAIEmbeddingService(key)
			fmt.Println("Generating embeddings with OpenAI (this makes paid API calls)...")
		} else {
			embeddingService = NewKeywordEmbeddingService()
			fmt.Println("OPENAI_API_KEY not set - generating keyword embeddings instead...")
		}
	} else {
		embeddingService = NewKeywordEmbeddingService()
		fmt.Println("Generating keyword embeddings (free, offline)...")
	}

	idx := NewNQEQueryIndex(embeddingService, log)
	if err := idx.LoadFromQueries(queries); err != nil {
		return err
	}
	// Reuse any cached embeddings so only new/changed queries are computed.
	if err := idx.loadEmbeddingsFromCache(); err != nil {
		log.Debug("No reusable embedding cache: %v", err)
	}
	if err := idx.GenerateEmbeddings(); err != nil {
		return err
	}

	stats := idx.GetStatistics()
	fmt.Printf("Embedding cache written: %v queries embedded (%.1f%% coverage) at %s\n",
		stats["embedded_queries"], toFloat(stats["embedding_coverage"])*100, idx.embeddingsCachePath)
	return nil
}

func toFloat(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}
