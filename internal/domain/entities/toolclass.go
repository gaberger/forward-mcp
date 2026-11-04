package entities

// ToolClass is the access class of an MCP tool. Every tool the server
// registers carries exactly one class. The class is enforced at
// registration time by the tool policy (domain/services/toolpolicy), not
// merely advertised as an annotation hint.
//
//   - ToolClassRead:    observes Forward or local state; never mutates anything
//     another caller can see.
//   - ToolClassPropose: stages or evaluates a change without applying it to a
//     device (e.g. Forward Predict). Off by default; audited per call.
//   - ToolClassWrite:   mutates Forward state (networks, locations, snapshots)
//     or shared local state (memory graph, caches, indexes). Never exposed in
//     remote mode in this phase.
type ToolClass string

const (
	ToolClassRead    ToolClass = "read"
	ToolClassPropose ToolClass = "propose"
	ToolClassWrite   ToolClass = "write"
)

// IsValid reports whether c is one of the three known classes.
func (c ToolClass) IsValid() bool {
	switch c {
	case ToolClassRead, ToolClassPropose, ToolClassWrite:
		return true
	}
	return false
}

func (c ToolClass) String() string { return string(c) }

// ToolTarget records what a tool acts on. It refines ToolClassWrite so that
// a later policy can distinguish a Forward-side write (never allowed to
// agents) from a mutation of per-caller local state (allowed once that
// state is partitioned by caller, M1c).
type ToolTarget string

const (
	// ToolTargetForward: the tool calls the Forward Networks API.
	ToolTargetForward ToolTarget = "forward"
	// ToolTargetLocal: the tool only touches this server's own state
	// (semantic cache, memory graph, NQE index, bloom indexes, defaults).
	ToolTargetLocal ToolTarget = "local"
)

// ToolSpec is the policy-relevant description of a registered tool.
type ToolSpec struct {
	Name   string
	Class  ToolClass
	Target ToolTarget
}

// ServeMode is how the server is being run. It is the primary input to the
// tool policy.
type ServeMode string

const (
	// ServeModeStdio: a single local operator drives the server over stdio
	// with their own Forward credentials. Behaviour is unchanged from 3.0.0.
	ServeModeStdio ServeMode = "stdio"
	// ServeModeRemote: the server listens over HTTP for many authenticated
	// callers (agents, humans). Read-only by default.
	ServeModeRemote ServeMode = "remote"
)

func (m ServeMode) IsValid() bool {
	return m == ServeModeStdio || m == ServeModeRemote
}
