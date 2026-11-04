// Package toolpolicy holds the pure rules that decide which MCP tools are
// registered and callable in each serve mode. It has no dependencies beyond
// the domain entities and can be tested without the MCP SDK.
package toolpolicy

import (
	"fmt"

	"github.com/forward-mcp/internal/domain/entities"
)

// Policy implements ports.ToolPolicy.
//
// Rules (PLAN.md M1.0, D1, D3):
//
//   - stdio: every class is registered and callable (3.0.0 behaviour).
//   - remote: only read tools are registered by default.
//   - remote + ProposeEnabled: propose tools are also registered, but a call
//     additionally requires the caller to hold entities.ScopePredict.
//   - remote: write tools are never registered. This is not configurable.
type Policy struct {
	mode           entities.ServeMode
	proposeEnabled bool
}

// Option configures a Policy.
type Option func(*Policy)

// WithProposeEnabled turns on registration of propose-class tools in remote
// mode. It is wired to FORWARD_ENABLE_PROPOSE_TOOLS by the config layer.
func WithProposeEnabled(on bool) Option {
	return func(p *Policy) { p.proposeEnabled = on }
}

// New builds a policy for the serve mode. An invalid mode is treated as
// remote (the more restrictive choice).
func New(mode entities.ServeMode, opts ...Option) *Policy {
	if !mode.IsValid() {
		mode = entities.ServeModeRemote
	}
	p := &Policy{mode: mode}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Stdio is the policy for the historical single-operator stdio server.
func Stdio() *Policy { return New(entities.ServeModeStdio) }

// Remote is the default remote policy: read tools only.
func Remote(proposeEnabled bool) *Policy {
	return New(entities.ServeModeRemote, WithProposeEnabled(proposeEnabled))
}

func (p *Policy) Mode() entities.ServeMode { return p.mode }

// ProposeEnabled reports whether propose tools are registered in this policy.
func (p *Policy) ProposeEnabled() bool {
	return p.mode == entities.ServeModeStdio || p.proposeEnabled
}

// AllowsRegistration implements ports.ToolPolicy.
func (p *Policy) AllowsRegistration(spec entities.ToolSpec) bool {
	if !spec.Class.IsValid() {
		return false
	}
	if p.mode == entities.ServeModeStdio {
		return true
	}
	switch spec.Class {
	case entities.ToolClassRead:
		return true
	case entities.ToolClassPropose:
		return p.proposeEnabled
	default: // ToolClassWrite: never in remote mode, regardless of target.
		return false
	}
}

// AllowsCall implements ports.ToolPolicy. The second return value is a
// caller-facing reason when the call is denied.
func (p *Policy) AllowsCall(spec entities.ToolSpec, caller entities.Caller) (bool, string) {
	if !p.AllowsRegistration(spec) {
		return false, fmt.Sprintf("tool %q (%s) is not available in %s mode", spec.Name, spec.Class, p.mode)
	}
	if p.mode == entities.ServeModeStdio {
		return true, ""
	}
	if spec.Class == entities.ToolClassPropose && !caller.HasScope(entities.ScopePredict) {
		return false, fmt.Sprintf("tool %q requires the %q scope", spec.Name, entities.ScopePredict)
	}
	return true, ""
}
