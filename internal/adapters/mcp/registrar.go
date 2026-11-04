// Package mcpadapter registers service handlers with the official MCP Go
// SDK while enforcing the tool-class policy (ports.ToolPolicy). It is the
// only place that talks to mcp.AddTool; service code goes through a
// Registrar and never decides on its own whether a tool is exposed.
package mcpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/ports"
	"github.com/forward-mcp/internal/usecases"
)

// Registrar wraps an mcp.Server with the exposure decision for one serve
// mode. Build one per server with NewRegistrar, pass it to the service's
// RegisterToolsWith, then check Err().
type Registrar struct {
	server   *mcp.Server
	exposure *usecases.ToolExposure
	audit    ports.AuditLogger

	mu         sync.Mutex
	registered []string
	skipped    []string
	errs       []error
}

// NewRegistrar builds a registrar. audit may be nil in stdio mode; propose
// calls are then not audited, which the policy only permits in stdio.
func NewRegistrar(server *mcp.Server, exposure *usecases.ToolExposure, audit ports.AuditLogger) *Registrar {
	return &Registrar{server: server, exposure: exposure, audit: audit}
}

// Server exposes the underlying SDK server for prompts and resources, which
// are not class-gated.
func (r *Registrar) Server() *mcp.Server { return r.server }

// Registered returns the names of tools actually advertised, sorted.
func (r *Registrar) Registered() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]string(nil), r.registered...)
	sort.Strings(out)
	return out
}

// Skipped returns the names of tools withheld by policy, sorted.
func (r *Registrar) Skipped() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]string(nil), r.skipped...)
	sort.Strings(out)
	return out
}

// Err returns the accumulated registration errors (uncatalogued tools).
func (r *Registrar) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return errors.Join(r.errs...)
}

// Handler is the ctx-aware handler shape. The caller identity is available
// via ports.CallerFromContext(ctx).
type Handler[In any] func(ctx context.Context, in In) (*mcp.CallToolResult, error)

// AddTool registers a ctx-aware handler if the policy allows the tool in
// this mode. Uncatalogued tools are recorded as errors and not registered.
func AddTool[In any](r *Registrar, name, description string, h Handler[In]) {
	spec, register, err := r.exposure.Decide(name)
	if err != nil {
		r.mu.Lock()
		r.errs = append(r.errs, err)
		r.mu.Unlock()
		return
	}
	if !register {
		r.mu.Lock()
		r.skipped = append(r.skipped, name)
		r.mu.Unlock()
		return
	}

	policy := r.exposure.Policy
	audit := r.audit

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        name,
		Description: description,
		Annotations: annotationsFor(spec),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		caller := ports.CallerFromContext(ctx)
		if ok, reason := policy.AllowsCall(spec, caller); !ok {
			if spec.Class == entities.ToolClassPropose && audit != nil {
				audit.LogProposeCall(ctx, auditEvent(spec, caller, in, entities.AuditOutcomeDenied, reason, 0))
			}
			return nil, nil, errors.New(reason)
		}
		if spec.Class != entities.ToolClassPropose {
			res, err := h(ctx, in)
			return res, nil, err
		}
		start := time.Now()
		res, err := h(ctx, in)
		if audit != nil {
			outcome, msg := entities.AuditOutcomeOK, ""
			if err != nil {
				outcome, msg = entities.AuditOutcomeError, err.Error()
			} else if res != nil && res.IsError {
				outcome, msg = entities.AuditOutcomeError, firstText(res)
			}
			audit.LogProposeCall(ctx, auditEvent(spec, caller, in, outcome, msg, time.Since(start)))
		}
		return res, nil, err
	})

	r.mu.Lock()
	r.registered = append(r.registered, name)
	r.mu.Unlock()
}

// AddToolLegacy adapts the 3.0.0 argument-only handler style. New handlers
// should use AddTool; existing ones migrate as they are touched (PLAN.md 1c.2).
func AddToolLegacy[In any](r *Registrar, name, description string, h func(In) (*mcp.CallToolResult, error)) {
	AddTool(r, name, description, func(_ context.Context, in In) (*mcp.CallToolResult, error) {
		return h(in)
	})
}

// auditEvent builds the audit record, pulling only identifier fields out of
// the tool input. Inputs are never logged wholesale.
func auditEvent(spec entities.ToolSpec, caller entities.Caller, in any, outcome, msg string, d time.Duration) entities.AuditEvent {
	ids := identifierFields(in)
	return entities.AuditEvent{
		Time:        time.Now(),
		Tool:        spec.Name,
		Subject:     caller.Subject,
		Issuer:      caller.Issuer,
		NetworkID:   ids["network_id"],
		ChangeSetID: ids["change_set_id"],
		SnapshotID:  firstNonEmpty(ids["snapshot_id"], ids["base_snapshot_id"]),
		Outcome:     outcome,
		Error:       msg,
		Duration:    d,
	}
}

// identifierFields extracts well-known identifier keys from an arbitrary
// input struct via its JSON encoding. Only string-valued identifiers are
// returned; everything else is dropped.
func identifierFields(in any) map[string]string {
	out := map[string]string{}
	b, err := json.Marshal(in)
	if err != nil {
		return out
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return out
	}
	for _, k := range []string{"network_id", "change_set_id", "snapshot_id", "base_snapshot_id"} {
		if v, ok := m[k].(string); ok {
			out[k] = v
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstText(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// Summary is a one-line description of what was exposed, for startup logs.
func (r *Registrar) Summary() string {
	return fmt.Sprintf("mode=%s registered=%d skipped=%d", r.exposure.Policy.Mode(), len(r.Registered()), len(r.Skipped()))
}
