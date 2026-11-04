package ports

import "github.com/forward-mcp/internal/domain/entities"

// ToolRegistry answers "what class is this tool?" for every tool the server
// can register. The catalog adapter (domain/services/toolpolicy) is the
// in-repo implementation; tests may supply their own.
type ToolRegistry interface {
	// Lookup returns the spec for a tool name, and false if the tool is not
	// catalogued. Registering an uncatalogued tool is a programming error
	// and must fail loudly (see adapters/mcp.Registrar).
	Lookup(name string) (entities.ToolSpec, bool)
	// All returns every catalogued tool, sorted by name.
	All() []entities.ToolSpec
}

// ToolPolicy decides whether a tool of a given spec may be registered for
// the current serve mode, and whether a given caller may invoke it.
type ToolPolicy interface {
	// Mode this policy was built for.
	Mode() entities.ServeMode
	// AllowsRegistration reports whether the tool should be advertised at all.
	AllowsRegistration(spec entities.ToolSpec) bool
	// AllowsCall reports whether the caller may invoke the tool now. It is
	// evaluated per request so that scope checks (e.g. "predict") apply even
	// to tools that were registered.
	AllowsCall(spec entities.ToolSpec, caller entities.Caller) (bool, string)
}
