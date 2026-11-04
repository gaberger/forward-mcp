// Package usecases holds application services that orchestrate domain
// rules over ports. They know nothing about MCP, HTTP or SQLite.
package usecases

import (
	"fmt"

	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/domain/services/toolpolicy"
	"github.com/forward-mcp/internal/ports"
)

// ToolExposure decides, for one serve mode, which catalogued tools are
// advertised. It is the single input the MCP registration adapter needs.
type ToolExposure struct {
	Registry ports.ToolRegistry
	Policy   ports.ToolPolicy
}

// NewToolExposure builds the exposure for a mode from the default catalog.
// proposeEnabled is ignored in stdio mode (everything is exposed there).
func NewToolExposure(mode entities.ServeMode, proposeEnabled bool) *ToolExposure {
	return &ToolExposure{
		Registry: toolpolicy.DefaultCatalog,
		Policy:   toolpolicy.New(mode, toolpolicy.WithProposeEnabled(proposeEnabled)),
	}
}

// Decide returns the tool's spec and whether it should be registered. An
// uncatalogued name is an error: every tool must carry a class.
func (e *ToolExposure) Decide(name string) (entities.ToolSpec, bool, error) {
	spec, ok := e.Registry.Lookup(name)
	if !ok {
		return entities.ToolSpec{}, false, fmt.Errorf("tool %q is not in the tool catalog; add it to toolpolicy.DefaultCatalog with a class", name)
	}
	return spec, e.Policy.AllowsRegistration(spec), nil
}

// Plan lists every catalogued tool with its registration decision, for
// startup logging and tests.
type PlanEntry struct {
	Spec       entities.ToolSpec
	Registered bool
}

func (e *ToolExposure) Plan() []PlanEntry {
	all := e.Registry.All()
	out := make([]PlanEntry, 0, len(all))
	for _, s := range all {
		out = append(out, PlanEntry{Spec: s, Registered: e.Policy.AllowsRegistration(s)})
	}
	return out
}
