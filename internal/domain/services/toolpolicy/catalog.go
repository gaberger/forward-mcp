package toolpolicy

import (
	"fmt"
	"sort"

	"github.com/forward-mcp/internal/domain/entities"
)

// Catalog is the in-repo ports.ToolRegistry: a static classification of
// every tool the server registers. Adding a tool without an entry here is
// a programming error caught by adapters/mcp.Registrar at startup and by
// TestCatalogCoversEveryRegisteredTool.
type Catalog struct {
	specs map[string]entities.ToolSpec
}

// Lookup implements ports.ToolRegistry.
func (c *Catalog) Lookup(name string) (entities.ToolSpec, bool) {
	s, ok := c.specs[name]
	return s, ok
}

// All implements ports.ToolRegistry.
func (c *Catalog) All() []entities.ToolSpec {
	out := make([]entities.ToolSpec, 0, len(c.specs))
	for _, s := range c.specs {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// NewCatalog builds a catalog from specs, rejecting duplicates and invalid
// classes so misconfiguration fails at construction.
func NewCatalog(specs []entities.ToolSpec) (*Catalog, error) {
	c := &Catalog{specs: make(map[string]entities.ToolSpec, len(specs))}
	for _, s := range specs {
		if s.Name == "" {
			return nil, fmt.Errorf("toolpolicy: tool spec with empty name")
		}
		if !s.Class.IsValid() {
			return nil, fmt.Errorf("toolpolicy: tool %q has invalid class %q", s.Name, s.Class)
		}
		if s.Target != entities.ToolTargetForward && s.Target != entities.ToolTargetLocal {
			return nil, fmt.Errorf("toolpolicy: tool %q has invalid target %q", s.Name, s.Target)
		}
		if _, dup := c.specs[s.Name]; dup {
			return nil, fmt.Errorf("toolpolicy: duplicate tool %q", s.Name)
		}
		c.specs[s.Name] = s
	}
	return c, nil
}

// MustCatalog is NewCatalog for package-level initialisation.
func MustCatalog(specs []entities.ToolSpec) *Catalog {
	c, err := NewCatalog(specs)
	if err != nil {
		panic(err)
	}
	return c
}

func read(name string, t entities.ToolTarget) entities.ToolSpec {
	return entities.ToolSpec{Name: name, Class: entities.ToolClassRead, Target: t}
}

func write(name string, t entities.ToolTarget) entities.ToolSpec {
	return entities.ToolSpec{Name: name, Class: entities.ToolClassWrite, Target: t}
}

// propose is used by the Predict tools (M1b). Declared now so the class is
// exercised by tests before any propose tool exists.
func propose(name string) entities.ToolSpec {
	return entities.ToolSpec{Name: name, Class: entities.ToolClassPropose, Target: entities.ToolTargetForward}
}

var (
	fwd = entities.ToolTargetForward
	loc = entities.ToolTargetLocal
)

// DefaultCatalog classifies the 54 tools registered by
// service.ForwardMCPService.RegisterTools as of 3.0.0, plus the propose
// tools planned for 3.1.0.
//
// Classification rules:
//   - Anything that only reads Forward or local state: read.
//   - Anything that calls a mutating Forward endpoint: write/forward.
//   - Anything that mutates server state another caller could observe in
//     remote mode (memory graph, caches, indexes, session defaults): write/local.
//     These may be re-admitted to remote mode once partitioned per caller (M1c);
//     that is a policy change, not a catalog change.
var DefaultCatalog = MustCatalog([]entities.ToolSpec{
	// --- Networks ---
	read("list_networks", fwd),
	write("create_network", fwd),
	write("update_network", fwd),

	// --- Path search ---
	read("search_paths", fwd),
	read("search_paths_bulk", fwd),
	read("analyze_network_prefixes", fwd),

	// --- NQE ---
	read("run_nqe_query_by_id", fwd),
	read("list_nqe_queries", fwd),
	read("search_nqe_queries", loc),
	read("suggest_similar_queries", loc),
	read("get_nqe_result_chunks", loc),
	read("get_nqe_result_summary", loc),
	read("analyze_nqe_result_sql", loc),

	// --- First-class device queries ---
	read("get_device_basic_info", fwd),
	read("get_device_hardware", fwd),
	read("get_hardware_support", fwd),
	read("get_os_support", fwd),
	read("search_configs", fwd),
	read("get_config_diff", fwd),

	// --- Devices ---
	read("list_devices", fwd),
	read("get_device_locations", fwd),
	write("update_device_locations", fwd),

	// --- Snapshots ---
	read("list_snapshots", fwd),
	read("get_latest_snapshot", fwd),
	write("delete_snapshot", fwd),

	// --- Locations ---
	read("list_locations", fwd),
	write("create_location", fwd),
	write("update_location", fwd),
	write("delete_location", fwd),
	write("create_locations_bulk", fwd),

	// --- Session defaults (process-wide state; shared across remote callers) ---
	read("get_default_settings", loc),
	write("set_default_network", loc),

	// --- Semantic cache ---
	read("get_cache_stats", loc),
	write("clear_cache", loc),

	// --- NQE index / database maintenance ---
	write("initialize_query_index", loc),
	write("hydrate_database", loc), // reports staleness today, but is a maintenance entry point
	write("refresh_query_index", loc),
	read("get_database_status", loc),
	read("get_query_analytics", loc),
	read("list_instance_ids", loc),

	// --- Memory graph (knowledge graph) ---
	write("create_entity", loc),
	write("create_relation", loc),
	write("add_observation", loc),
	write("delete_entity", loc),
	write("delete_relation", loc),
	write("delete_observation", loc),
	read("search_entities", loc),
	read("get_entity", loc),
	read("get_relations", loc),
	read("get_observations", loc),
	read("get_memory_stats", loc),

	// --- Bloom search ---
	write("build_bloom_filter", loc),
	read("search_bloom_filter", loc),
	read("get_bloom_filter_stats", loc),

	// --- Predict (M1b; propose class, registered only behind the flag) ---
	propose("predict_create_change_set"),
	propose("predict_stage_edits"),
	propose("predict_run_and_wait"),
	propose("predict_get_results"),
})
