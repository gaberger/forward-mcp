package usecases

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/domain/services/predict"
	"github.com/forward-mcp/internal/ports"
)

// PredictChanges is the application service behind the four propose-class
// tools (PLAN.md 1b.3). It owns the round trip described in
// docs/predict-endpoints.md and binds change sets to the caller that
// created them, so one identity cannot operate on another's.
type PredictChanges struct {
	client ports.PredictClient
	now    func() time.Time
	sleep  func(context.Context, time.Duration) error

	mu    sync.Mutex
	owned map[string]ownedChangeSet // change set id -> owner + metadata
	runID string
}

type ownedChangeSet struct {
	cs        entities.ChangeSet
	predicted string // predicted snapshot id once known
}

// Tag prefix put on every change set forward-mcp creates (PLAN.md 1b.6).
const TagPrefix = "netorch:"

// NewPredictChanges builds the service. runID distinguishes this process's
// change sets in tags; use a short random or startup id.
func NewPredictChanges(client ports.PredictClient, runID string) *PredictChanges {
	return &PredictChanges{
		client: client, now: time.Now, runID: runID, owned: map[string]ownedChangeSet{},
		sleep: func(ctx context.Context, d time.Duration) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d):
				return nil
			}
		},
	}
}

// ErrNotOwner is returned when a caller touches a change set it did not create.
var ErrNotOwner = errors.New("change set is not owned by this caller")

// Create implements predict_create_change_set. The base snapshot must be
// processed and must not itself be a prediction.
func (p *PredictChanges) Create(ctx context.Context, caller entities.Caller, networkID, baseSnapshotID, description string) (entities.ChangeSet, error) {
	if networkID == "" || baseSnapshotID == "" {
		return entities.ChangeSet{}, fmt.Errorf("network_id and base_snapshot_id are required")
	}
	snap, err := p.client.SnapshotState(ctx, networkID, baseSnapshotID)
	if err != nil {
		return entities.ChangeSet{}, fmt.Errorf("base snapshot: %w", err)
	}
	if snap.ProcessingTrigger == "PREDICT" {
		return entities.ChangeSet{}, fmt.Errorf("base snapshot %s is itself a prediction; base changes on a collected snapshot", baseSnapshotID)
	}
	if snap.State != "" && snap.State != entities.SnapshotProcessed {
		return entities.ChangeSet{}, fmt.Errorf("base snapshot %s is %s, not PROCESSED", baseSnapshotID, snap.State)
	}
	name := fmt.Sprintf("netorch %s %s", caller.Subject, p.now().UTC().Format("2006-01-02 15:04:05"))
	tags := []string{TagPrefix + caller.Subject, TagPrefix + "run:" + p.runID}
	cs, err := p.client.CreateChangeSet(ctx, networkID, name, baseSnapshotID, description, tags)
	if err != nil {
		return entities.ChangeSet{}, err
	}
	cs.Owner = caller.Subject
	if cs.BaseSnapshotID == "" {
		cs.BaseSnapshotID = baseSnapshotID
	}
	p.mu.Lock()
	p.owned[cs.ID] = ownedChangeSet{cs: cs}
	p.mu.Unlock()
	return cs, nil
}

func (p *PredictChanges) lookup(caller entities.Caller, changeSetID string) (ownedChangeSet, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	o, ok := p.owned[changeSetID]
	if !ok || o.cs.Owner != caller.Subject {
		return ownedChangeSet{}, fmt.Errorf("%w: %s (create it with predict_create_change_set in this session)", ErrNotOwner, changeSetID)
	}
	return o, nil
}

// StageResult reports what was staged for one device.
type StageResult struct {
	Device     string                      `json:"device"`
	Staged     string                      `json:"staged"` // "cli" or "firewall_rules"
	Rules      int                         `json:"rules,omitempty"`
	Validation *entities.CommandValidation `json:"validation,omitempty"`
}

// Stage implements predict_stage_edits. platform is the device's OS as
// known to the caller (used only for the PAN-OS CLI guard; "" skips it).
func (p *PredictChanges) Stage(ctx context.Context, caller entities.Caller, changeSetID, platform string, edits entities.StageEdits) (StageResult, error) {
	o, err := p.lookup(caller, changeSetID)
	if err != nil {
		return StageResult{}, err
	}
	if err := predict.ValidateStageEdits(platform, edits); err != nil {
		return StageResult{}, err
	}
	cs := o.cs
	if edits.CLI != "" {
		v, verr := p.client.ValidateDeviceCommands(ctx, cs.NetworkID, cs.ID, edits.Device, edits.CLI)
		if err := p.client.SetDeviceCommands(ctx, cs.NetworkID, cs.ID, edits.Device, edits.CLI); err != nil {
			return StageResult{}, err
		}
		res := StageResult{Device: edits.Device, Staged: "cli"}
		if verr == nil {
			res.Validation = &v
		}
		return res, nil
	}
	for i, r := range edits.FirewallRules {
		scope, rulebase := r.ScopeID, r.RulebaseID
		if scope == "" {
			scope = "LOCAL"
		}
		if rulebase == "" {
			rulebase = "PRIMARY"
		}
		var err error
		switch strings.ToLower(r.Action) {
		case "add":
			err = p.client.AddSecurityRule(ctx, cs.NetworkID, cs.ID, edits.Device, scope, rulebase, r.Definition)
		case "update":
			err = p.client.UpdateSecurityRule(ctx, cs.NetworkID, cs.ID, edits.Device, scope, rulebase, r.RuleID, r.Definition)
		case "delete":
			err = p.client.DeleteSecurityRule(ctx, cs.NetworkID, cs.ID, edits.Device, scope, rulebase, r.RuleID)
		}
		if err != nil {
			return StageResult{}, fmt.Errorf("firewall_rules[%d]: %w", i, err)
		}
	}
	return StageResult{Device: edits.Device, Staged: "firewall_rules", Rules: len(edits.FirewallRules)}, nil
}

// RunOptions tune RunAndWait.
type RunOptions struct {
	Timeout      time.Duration // default 30m
	PollInterval time.Duration // default 15s (SDK default)
	Note         string
}

// RunResultSummary is predict_run_and_wait's output.
type RunResultSummary struct {
	ChangeSetID         string `json:"change_set_id"`
	CommitID            string `json:"commit_id"`
	PredictedSnapshotID string `json:"predicted_snapshot_id"`
	State               string `json:"state"`
	WaitedSeconds       int    `json:"waited_seconds"`
	// TimedOut is true when the snapshot was still processing at the
	// deadline; processing continues on Forward and predict_get_results
	// can be called later.
	TimedOut bool `json:"timed_out"`
}

// RunAndWait implements predict_run_and_wait: commit, predict, then poll
// the snapshot list until PROCESSED, a failure state, or the deadline.
func (p *PredictChanges) RunAndWait(ctx context.Context, caller entities.Caller, changeSetID string, opts RunOptions) (RunResultSummary, error) {
	o, err := p.lookup(caller, changeSetID)
	if err != nil {
		return RunResultSummary{}, err
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Minute
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 15 * time.Second
	}
	if opts.Note == "" {
		opts.Note = "netorch " + caller.Subject
	}
	cs := o.cs
	commitID, err := p.client.Commit(ctx, cs.NetworkID, cs.ID, opts.Note)
	if err != nil {
		return RunResultSummary{}, fmt.Errorf("commit: %w", err)
	}
	snap, err := p.client.Predict(ctx, cs.NetworkID, cs.ID, opts.Note)
	if err != nil {
		// Predict is not idempotent: never retry blindly. If one was created
		// despite the error, adopt the newest predicted snapshot instead.
		if list, lerr := p.client.ListPredictedSnapshots(ctx, cs.NetworkID, cs.ID); lerr == nil && len(list) > 0 {
			snap = list[len(list)-1]
		} else {
			return RunResultSummary{}, fmt.Errorf("predict: %w", err)
		}
	}
	p.mu.Lock()
	o.predicted = snap.ID
	p.owned[cs.ID] = o
	p.mu.Unlock()

	start := p.now()
	deadline := start.Add(opts.Timeout)
	state := snap.State
	for {
		cur, err := p.client.SnapshotState(ctx, cs.NetworkID, snap.ID)
		if err == nil {
			state = cur.State
		}
		if state == entities.SnapshotProcessed || entities.SnapshotFailedStates[state] {
			break
		}
		if p.now().After(deadline) {
			return RunResultSummary{ChangeSetID: cs.ID, CommitID: commitID, PredictedSnapshotID: snap.ID, State: state, WaitedSeconds: int(p.now().Sub(start).Seconds()), TimedOut: true}, nil
		}
		if err := p.sleep(ctx, opts.PollInterval); err != nil {
			return RunResultSummary{}, err
		}
	}
	return RunResultSummary{ChangeSetID: cs.ID, CommitID: commitID, PredictedSnapshotID: snap.ID, State: state, WaitedSeconds: int(p.now().Sub(start).Seconds())}, nil
}

// ResultsOptions tune GetResults.
type ResultsOptions struct {
	// ConnectivityTimeout bounds polling for a non-partial connectivity
	// result (default 5m, SDK default); zero polling leaves it partial →
	// UNKNOWN.
	ConnectivityTimeout time.Duration
	PollInterval        time.Duration // default 2s
}

// GetResults implements predict_get_results.
func (p *PredictChanges) GetResults(ctx context.Context, caller entities.Caller, changeSetID string, opts ResultsOptions) (*entities.PredictResult, error) {
	o, err := p.lookup(caller, changeSetID)
	if err != nil {
		return nil, err
	}
	if opts.ConnectivityTimeout <= 0 {
		opts.ConnectivityTimeout = 5 * time.Minute
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 2 * time.Second
	}
	cs := o.cs
	r := &entities.PredictResult{ChangeSetID: cs.ID, NetworkID: cs.NetworkID, BaseSnapshotID: cs.BaseSnapshotID, PredictedSnapshotID: o.predicted}
	if r.PredictedSnapshotID == "" {
		if list, err := p.client.ListPredictedSnapshots(ctx, cs.NetworkID, cs.ID); err == nil && len(list) > 0 {
			r.PredictedSnapshotID = list[len(list)-1].ID
		}
	}
	if r.PredictedSnapshotID != "" {
		if snap, err := p.client.SnapshotState(ctx, cs.NetworkID, r.PredictedSnapshotID); err == nil {
			r.SnapshotState = snap.State
		}
	}
	if r.SnapshotState == entities.SnapshotProcessed {
		before, after := cs.BaseSnapshotID, r.PredictedSnapshotID
		deadline := p.now().Add(opts.ConnectivityTimeout)
		for {
			c, err := p.client.SubnetConnectivityDiff(ctx, before, after)
			if err != nil {
				r.Reasons = append(r.Reasons, "connectivity: "+err.Error())
				break
			}
			r.Connectivity = &c
			if !c.IsPartialResult || p.now().After(deadline) {
				break
			}
			if err := p.sleep(ctx, opts.PollInterval); err != nil {
				return nil, err
			}
		}
		r.Areas = map[string]entities.AreaCount{}
		for _, area := range entities.DiffAreas {
			ac, err := p.client.AreaDiffCount(ctx, before, after, area)
			if err != nil {
				r.Reasons = append(r.Reasons, area+": "+err.Error())
				r.Areas[area] = entities.AreaCount{Complete: false}
				continue
			}
			r.Areas[area] = ac
		}
		if rl, err := p.client.RoutingLoopDiffCount(ctx, before, after); err == nil {
			r.RoutingLoops = &rl
		} else {
			r.Reasons = append(r.Reasons, "routing loops: "+err.Error())
		}
		if checks, err := p.client.CheckDiffCounts(ctx, before, after); err == nil {
			r.Checks = checks
		} else {
			r.Reasons = append(r.Reasons, "checks: "+err.Error())
		}
		if cves, err := p.client.VulnerabilityDiffCounts(ctx, before, after); err == nil {
			r.NewCVEs = cves
		}
	}
	verdict, reasons := predict.Verdict(r)
	r.Verdict = verdict
	r.Reasons = append(reasons, r.Reasons...)
	r.Evidence = entities.Evidence{Kind: "predict", Ref: "predict:" + cs.ID, SnapshotID: r.PredictedSnapshotID}
	return r, nil
}

// FindByDescription returns the caller-visible change sets on a network
// whose description contains needle, newest first, and adopts them into
// this process's ownership for the caller (so a workflow that crashed
// after creating a change set can resume with it instead of creating a
// second one). Only change sets tagged for this caller are returned.
func (p *PredictChanges) FindByDescription(ctx context.Context, caller entities.Caller, networkID, needle string) ([]entities.ChangeSet, error) {
	if strings.TrimSpace(needle) == "" {
		return nil, fmt.Errorf("description text to search for is required")
	}
	list, err := p.client.ListChangeSets(ctx, networkID)
	if err != nil {
		return nil, err
	}
	var out []entities.ChangeSet
	for _, cs := range list {
		if !strings.Contains(cs.Description, needle) || !hasTag(cs.Tags, TagPrefix+caller.Subject) {
			continue
		}
		cs.Owner = caller.Subject
		if cs.NetworkID == "" {
			cs.NetworkID = networkID
		}
		p.mu.Lock()
		if _, known := p.owned[cs.ID]; !known {
			p.owned[cs.ID] = ownedChangeSet{cs: cs}
		}
		p.mu.Unlock()
		out = append(out, cs)
	}
	// Newest first by created_at string (RFC3339 sorts lexically).
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].CreatedAt > out[i].CreatedAt {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

func hasTag(tags []string, t string) bool {
	for _, x := range tags {
		if x == t {
			return true
		}
	}
	return false
}

// GC deletes this process's stale change sets on a network (tagged
// TagPrefix+"run:<runID>" from earlier runs, or everything netorch-tagged
// when all is true). Deleting a change set cascades to its predicted
// snapshots (documented in the reference).
func (p *PredictChanges) GC(ctx context.Context, networkID string, all bool) ([]string, error) {
	list, err := p.client.ListChangeSets(ctx, networkID)
	if err != nil {
		return nil, err
	}
	var deleted []string
	for _, cs := range list {
		mine, current := false, false
		for _, t := range cs.Tags {
			if strings.HasPrefix(t, TagPrefix) {
				mine = true
			}
			if t == TagPrefix+"run:"+p.runID {
				current = true
			}
		}
		if !mine || (current && !all) {
			continue
		}
		if err := p.client.DeleteChangeSet(ctx, networkID, cs.ID); err != nil {
			return deleted, fmt.Errorf("delete %s: %w", cs.ID, err)
		}
		deleted = append(deleted, cs.ID)
	}
	return deleted, nil
}

// SetPredictClockForTest replaces the clock and sleep with a virtual clock
// that advances on sleep, so fixture-driven contract tests run instantly
// and deterministically. Not for production use.
func SetPredictClockForTest(p *PredictChanges, start time.Time) {
	cur := start
	p.now = func() time.Time { return cur }
	p.sleep = func(_ context.Context, d time.Duration) error {
		cur = cur.Add(d)
		return nil
	}
}
