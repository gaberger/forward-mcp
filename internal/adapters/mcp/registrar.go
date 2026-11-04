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
	"reflect"
	"sort"
	"strings"
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
	limiter  ports.RateLimiter // optional; nil = unlimited

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

// WithRateLimiter bounds calls per caller subject. Denied calls return a
// tool error naming the limit; propose calls are audited as denied.
func (r *Registrar) WithRateLimiter(l ports.RateLimiter) *Registrar {
	r.limiter = l
	return r
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
	limiter := r.limiter

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        name,
		Description: description,
		Annotations: annotationsFor(spec),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		ctx, caller := callerFor(ctx, req)
		deny := func(reason string) (*mcp.CallToolResult, any, error) {
			if spec.Class == entities.ToolClassPropose && audit != nil {
				audit.LogProposeCall(ctx, auditEvent(spec, caller, in, entities.AuditOutcomeDenied, reason, 0))
			}
			return nil, nil, errors.New(reason)
		}
		if ok, reason := policy.AllowsCall(spec, caller); !ok {
			return deny(reason)
		}
		if reason := networkDenial(spec, caller, in); reason != "" {
			return deny(reason)
		}
		if limiter != nil && caller.Subject != entities.LocalSubject && !limiter.Allow(caller.Subject) {
			return deny(fmt.Sprintf("rate limit exceeded for caller %q; retry shortly", caller.Subject))
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

// networkDenial enforces the caller's network allow-list (PLAN.md 1c.5).
// A restricted caller must name a network on tools that take one, because
// the process-wide default network is not theirs to use.
func networkDenial(spec entities.ToolSpec, caller entities.Caller, in any) string {
	if len(caller.NetworkIDs) == 0 || spec.Target != entities.ToolTargetForward {
		return ""
	}
	fields, hasField := networkField(in)
	if !hasField {
		return ""
	}
	if fields == "" {
		return fmt.Sprintf("network_id is required: caller %q is restricted to networks %v", caller.Subject, caller.NetworkIDs)
	}
	if !caller.MayAccessNetwork(fields) {
		return fmt.Sprintf("caller %q is not authorised for network %s (allowed: %v)", caller.Subject, fields, caller.NetworkIDs)
	}
	return ""
}

// networkField returns the input's network_id and whether the input type
// declares one at all (so tools without a network parameter are exempt).
func networkField(in any) (string, bool) {
	b, err := json.Marshal(in)
	if err != nil {
		return "", false
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return "", false
	}
	raw, ok := m["network_id"]
	if !ok {
		// omitempty drops empty strings; detect the field via the type.
		return "", hasNetworkIDField(in)
	}
	var v string
	_ = json.Unmarshal(raw, &v)
	return v, true
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

// hasNetworkIDField reports whether in's struct type has a json field
// named network_id, via reflection, for the omitempty case.
func hasNetworkIDField(in any) bool {
	t := reflect.TypeOf(in)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return false
	}
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		if name, _, _ := strings.Cut(tag, ","); name == "network_id" {
			return true
		}
	}
	return false
}
