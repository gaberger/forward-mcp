package mcpadapter

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/forward-mcp/internal/domain/entities"
)

func boolPtr(b bool) *bool { return &b }

// Annotation classes. All hints are advisory (clients use them for
// confirmation UX and display); enforcement is the policy's job.
var (
	annReadOnly = &mcp.ToolAnnotations{ReadOnlyHint: true}
	annCreate   = &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)}
	annRebuild  = &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), IdempotentHint: true}
	annUpdate   = &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), IdempotentHint: true}
	annDelete   = &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), IdempotentHint: true}
	// annPropose: stages changes in Forward Predict; creates sandbox objects
	// (change sets, predicted snapshots) but touches no device.
	annPropose = &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)}
)

// writeHints preserves the finer-grained 3.0.0 hints for write-class tools.
// Read-class tools always get annReadOnly; propose-class tools annPropose.
var writeHints = map[string]*mcp.ToolAnnotations{
	// Additive creation tools.
	"create_network":  annCreate,
	"create_location": annCreate,
	"create_entity":   annCreate,
	"create_relation": annCreate,
	"add_observation": annCreate,

	// Maintenance tools that (re)build derived state.
	"initialize_query_index": annRebuild,
	"hydrate_database":       annRebuild,
	"refresh_query_index":    annRebuild,
	"build_bloom_filter":     annRebuild,
	"set_default_network":    annRebuild,

	// Tools that overwrite existing data.
	"update_network":          annUpdate,
	"update_location":         annUpdate,
	"update_device_locations": annUpdate,
	"create_locations_bulk":   annUpdate, // upsert: may overwrite existing locations

	// Tools that permanently remove data.
	"delete_snapshot":    annDelete,
	"delete_location":    annDelete,
	"delete_entity":      annDelete,
	"delete_relation":    annDelete,
	"delete_observation": annDelete,
	"clear_cache":        annDelete,
}

// annotationsFor derives the advertised hints from the tool's class, so the
// hints can never disagree with the enforced class (readOnlyHint ⇔ read).
func annotationsFor(spec entities.ToolSpec) *mcp.ToolAnnotations {
	switch spec.Class {
	case entities.ToolClassRead:
		return annReadOnly
	case entities.ToolClassPropose:
		return annPropose
	default:
		if a, ok := writeHints[spec.Name]; ok {
			return a
		}
		return annUpdate
	}
}
