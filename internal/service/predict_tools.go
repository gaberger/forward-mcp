package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	mcpadapter "github.com/forward-mcp/internal/adapters/mcp"
	"github.com/forward-mcp/internal/adapters/predicthttp"
	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/ports"
	"github.com/forward-mcp/internal/usecases"
)

// Forward Predict tools (PLAN.md 1b.3). All are propose-class: the
// registrar withholds them unless FORWARD_ENABLE_PROPOSE_TOOLS=true (and in
// remote mode the caller holds the "predict" scope) and audit-logs every
// call. The handlers only parse arguments; the round trip is
// usecases.PredictChanges over ports.PredictClient.

// initPredict builds the Predict use case over the Forward client. The
// client must implement ports.ForwardHTTP (forward.Client does).
func (s *ForwardMCPService) initPredict() {
	h, ok := s.forwardClient.(ports.ForwardHTTP)
	if !ok {
		s.logger.Warn("Predict tools unavailable: Forward client does not support raw requests")
		return
	}
	s.predict = usecases.NewPredictChanges(predicthttp.New(h), newRunID())
}

func newRunID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().Unix())
	}
	return hex.EncodeToString(b)
}

func (s *ForwardMCPService) predictReady() (*usecases.PredictChanges, error) {
	if s.predict == nil {
		return nil, fmt.Errorf("Predict tools are not initialised in this server")
	}
	return s.predict, nil
}

// PredictCreateChangeSetArgs are the arguments for predict_create_change_set.
type PredictCreateChangeSetArgs struct {
	NetworkID      string `json:"network_id" jsonschema:"Network the change targets"`
	BaseSnapshotID string `json:"base_snapshot_id" jsonschema:"Processed, collected (not PREDICT) snapshot to base the change on; use get_latest_snapshot"`
	Description    string `json:"description,omitempty" jsonschema:"What the change is for; recorded on the change set"`
}

// PredictStageEditsArgs are the arguments for predict_stage_edits.
type PredictStageEditsArgs struct {
	ChangeSetID   string                      `json:"change_set_id" jsonschema:"Change set id from predict_create_change_set"`
	Device        string                      `json:"device" jsonschema:"Device name as known to Forward"`
	Platform      string                      `json:"platform,omitempty" jsonschema:"Device OS/platform if known (e.g. EOS, IOS, PAN_OS); PAN-OS rejects CLI and needs firewall_rules"`
	CLI           string                      `json:"cli,omitempty" jsonschema:"CLI configuration text to stage for the device (replaces any previously staged text)"`
	FirewallRules []entities.FirewallRuleEdit `json:"firewall_rules,omitempty" jsonschema:"Structured security-rule edits for firewalls: [{action: add|update|delete, rule_id?, definition?}]"`
}

// PredictRunAndWaitArgs are the arguments for predict_run_and_wait.
type PredictRunAndWaitArgs struct {
	ChangeSetID    string `json:"change_set_id" jsonschema:"Change set id"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"How long to wait for the predicted snapshot to process (default 1800)"`
	Note           string `json:"note,omitempty" jsonschema:"Commit/prediction note (default 'netorch <caller>')"`
}

// PredictGetResultsArgs are the arguments for predict_get_results.
type PredictGetResultsArgs struct {
	ChangeSetID                string `json:"change_set_id" jsonschema:"Change set id"`
	ConnectivityTimeoutSeconds int    `json:"connectivity_timeout_seconds,omitempty" jsonschema:"How long to wait for the connectivity comparison to finish (default 300)"`
}

// PredictGCArgs are the arguments for predict_gc.
type PredictGCArgs struct {
	NetworkID string `json:"network_id" jsonschema:"Network whose netorch-created change sets to delete"`
	All       bool   `json:"all,omitempty" jsonschema:"Also delete change sets from this server process (default: only earlier runs)"`
}

func (s *ForwardMCPService) predictCreateChangeSet(ctx context.Context, args PredictCreateChangeSetArgs) (*mcp.CallToolResult, error) {
	s.logToolCall("predict_create_change_set", args, nil)
	uc, err := s.predictReady()
	if err != nil {
		return nil, err
	}
	networkID := s.getNetworkID(args.NetworkID)
	if err := s.validateNetworkID(networkID); err != nil {
		return nil, err
	}
	cs, err := uc.Create(ctx, ports.CallerFromContext(ctx), networkID, args.BaseSnapshotID, args.Description)
	if err != nil {
		return nil, err
	}
	return jsonToolResponse(map[string]any{
		"change_set": cs,
		"next":       "Stage edits with predict_stage_edits, then predict_run_and_wait, then predict_get_results.",
	})
}

func (s *ForwardMCPService) predictStageEdits(ctx context.Context, args PredictStageEditsArgs) (*mcp.CallToolResult, error) {
	s.logToolCall("predict_stage_edits", args, nil)
	uc, err := s.predictReady()
	if err != nil {
		return nil, err
	}
	res, err := uc.Stage(ctx, ports.CallerFromContext(ctx), args.ChangeSetID, args.Platform, entities.StageEdits{Device: args.Device, CLI: args.CLI, FirewallRules: args.FirewallRules})
	if err != nil {
		return nil, err
	}
	return jsonToolResponse(res)
}

func (s *ForwardMCPService) predictRunAndWait(ctx context.Context, args PredictRunAndWaitArgs) (*mcp.CallToolResult, error) {
	s.logToolCall("predict_run_and_wait", args, nil)
	uc, err := s.predictReady()
	if err != nil {
		return nil, err
	}
	res, err := uc.RunAndWait(ctx, ports.CallerFromContext(ctx), args.ChangeSetID, usecases.RunOptions{Timeout: time.Duration(args.TimeoutSeconds) * time.Second, Note: args.Note})
	if err != nil {
		return nil, err
	}
	return jsonToolResponse(res)
}

func (s *ForwardMCPService) predictGetResults(ctx context.Context, args PredictGetResultsArgs) (*mcp.CallToolResult, error) {
	s.logToolCall("predict_get_results", args, nil)
	uc, err := s.predictReady()
	if err != nil {
		return nil, err
	}
	res, err := uc.GetResults(ctx, ports.CallerFromContext(ctx), args.ChangeSetID, usecases.ResultsOptions{ConnectivityTimeout: time.Duration(args.ConnectivityTimeoutSeconds) * time.Second})
	if err != nil {
		return nil, err
	}
	return jsonToolResponse(res)
}

func (s *ForwardMCPService) predictGC(ctx context.Context, args PredictGCArgs) (*mcp.CallToolResult, error) {
	s.logToolCall("predict_gc", args, nil)
	uc, err := s.predictReady()
	if err != nil {
		return nil, err
	}
	networkID := s.getNetworkID(args.NetworkID)
	if err := s.validateNetworkID(networkID); err != nil {
		return nil, err
	}
	deleted, err := uc.GC(ctx, networkID, args.All)
	if err != nil {
		return nil, err
	}
	return jsonToolResponse(map[string]any{"deleted_change_sets": deleted, "note": "deleting a change set also deletes its predicted snapshots"})
}

// registerPredictTools adds the propose-class tools and the GC maintenance tool.
func (s *ForwardMCPService) registerPredictTools(r *mcpadapter.Registrar) {
	mcpadapter.AddTool(r, "predict_create_change_set",
		"🧪 **PREDICT (propose)**: Create a Forward Predict change set against a processed base snapshot. "+
			"Nothing touches a device: Predict models the change and reports its impact. Returns the change set id "+
			"to use with predict_stage_edits.",
		s.predictCreateChangeSet)
	mcpadapter.AddTool(r, "predict_stage_edits",
		"🧪 **PREDICT (propose)**: Stage edits for one device in a change set: CLI text (most platforms) or "+
			"structured firewall rule edits (PAN-OS). CLI text is validated first; validation is advisory.",
		s.predictStageEdits)
	mcpadapter.AddTool(r, "predict_run_and_wait",
		"🧪 **PREDICT (propose)**: Commit the staged edits, run the prediction and wait for the predicted "+
			"snapshot to process. Returns the predicted snapshot id; call predict_get_results next.",
		s.predictRunAndWait)
	mcpadapter.AddTool(r, "predict_get_results",
		"🧪 **PREDICT (propose)**: Collect the prediction's impact: subnet connectivity changes, per-area diff "+
			"counts, routing loops, changed checks, and a verdict PASS / FAIL / UNKNOWN with reasons. UNKNOWN means "+
			"the comparison was incomplete or compared nothing; it is never an approval.",
		s.predictGetResults)
	mcpadapter.AddTool(r, "predict_gc",
		"Delete change sets this server created on a network (tagged netorch:*). Also deletes their predicted snapshots.",
		s.predictGC)
}
