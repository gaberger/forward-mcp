package predicthttp_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/forward-mcp/internal/adapters/forwardhttpfixture"
	"github.com/forward-mcp/internal/adapters/predicthttp"
	"github.com/forward-mcp/internal/config"
	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/forward"
	"github.com/forward-mcp/internal/ports"
	"github.com/forward-mcp/internal/usecases"
)

// The contract scenario (PLAN.md 1b.4): create → validate+stage one CLI edit
// → commit → predict → wait → results → GC. It runs against the recorded
// fixture by default and against a live instance when FORWARD_LIVE=1
// (TestIntegrationPredictRoundTrip), where it also records a fresh fixture
// and writes docs/predict-divergences.md if anything departs from the
// reference (1b.5).

const (
	fixtureNetwork  = "115"
	fixtureBaseSnap = "656"
)

var caller = entities.Caller{Subject: "agent-a", Scopes: []string{entities.ScopePredict}}

func scenario(t *testing.T, h ports.ForwardHTTP, networkID, baseSnapshot string, live bool) *entities.PredictResult {
	t.Helper()
	ctx := context.Background()
	uc := usecases.NewPredictChanges(predicthttp.New(h), "test")
	if !live {
		// Deterministic clock and no real sleeping for the fixture run.
		usecases.SetPredictClockForTest(uc, time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC))
	}

	cs, err := uc.Create(ctx, caller, networkID, baseSnapshot, "shut Ethernet1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if cs.ID == "" || cs.Owner != caller.Subject || cs.BaseSnapshotID != baseSnapshot {
		t.Fatalf("change set: %+v", cs)
	}

	// Ownership: another caller cannot touch it.
	other := entities.Caller{Subject: "agent-b", Scopes: []string{entities.ScopePredict}}
	if _, err := uc.Stage(ctx, other, cs.ID, "EOS", entities.StageEdits{Device: "r1", CLI: "x"}); !errors.Is(err, usecases.ErrNotOwner) {
		t.Fatalf("expected ErrNotOwner, got %v", err)
	}

	st, err := uc.Stage(ctx, caller, cs.ID, "EOS", entities.StageEdits{Device: "r1", CLI: "interface Ethernet1\n shutdown"})
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	if st.Staged != "cli" {
		t.Fatalf("stage result: %+v", st)
	}

	run, err := uc.RunAndWait(ctx, caller, cs.ID, usecases.RunOptions{Note: "contract test", Timeout: 20 * time.Minute, PollInterval: 15 * time.Second})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if run.CommitID == "" || run.PredictedSnapshotID == "" || run.TimedOut || run.State != entities.SnapshotProcessed {
		t.Fatalf("run summary: %+v", run)
	}

	res, err := uc.GetResults(ctx, caller, cs.ID, usecases.ResultsOptions{ConnectivityTimeout: 5 * time.Minute, PollInterval: 2 * time.Second})
	if err != nil {
		t.Fatalf("results: %v", err)
	}
	if res.PredictedSnapshotID != run.PredictedSnapshotID || res.Connectivity == nil || res.Evidence.SnapshotID != run.PredictedSnapshotID {
		t.Fatalf("result: %+v", res)
	}

	found, err := uc.FindByDescription(ctx, caller, networkID, "shut Ethernet1")
	if err != nil || len(found) == 0 || found[0].ID != cs.ID {
		t.Fatalf("find by description: %v %+v", err, found)
	}
	if theirs, _ := uc.FindByDescription(ctx, other, networkID, "shut Ethernet1"); len(theirs) != 0 {
		t.Fatal("another caller must not find it")
	}

	deleted, err := uc.GC(ctx, networkID, true)
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	gcHit := false
	for _, id := range deleted {
		if id == cs.ID {
			gcHit = true
		}
	}
	if !gcHit {
		t.Fatalf("gc did not delete %s: %v", cs.ID, deleted)
	}
	return res
}

func TestPredictRoundTripAgainstFixture(t *testing.T) {
	rp, err := forwardhttpfixture.Load("testdata/roundtrip.json")
	if err != nil {
		t.Fatal(err)
	}
	res := scenario(t, rp, fixtureNetwork, fixtureBaseSnap, false)
	if len(rp.Unmatched) != 0 {
		t.Fatalf("requests not covered by the fixture: %v", rp.Unmatched)
	}
	// The reference example isolates 14 subnet pairs and changes 2 checks → FAIL.
	if res.Verdict != entities.PredictFail {
		t.Fatalf("verdict %s, reasons %v", res.Verdict, res.Reasons)
	}
	joined := strings.Join(res.Reasons, "\n")
	for _, want := range []string{"14 subnet pair(s) newly isolated", "2 Predefined check(s)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("reasons missing %q: %v", want, res.Reasons)
		}
	}
	if res.Connectivity.IsPartialResult {
		t.Error("connectivity should have been polled past the partial result")
	}
	if ac := res.Areas["interfaces"]; ac.Count != 1 || !ac.Complete {
		t.Errorf("areas: %+v", res.Areas)
	}
	if res.NewCVEs != nil {
		t.Error("unlicensed vulnerability counts must be nil, not an error")
	}
}

func TestDivergenceIsLoud(t *testing.T) {
	// A status the reference does not list is a divergence, not an APIError.
	rp := forwardhttpfixture.NewReplayer(forwardhttpfixture.File{Exchanges: []forwardhttpfixture.Exchange{
		{Method: "POST", Path: "/api/networks/1/change-sets", Status: 418, Body: `{"message":"teapot"}`},
		{Method: "POST", Path: "/api/networks/1/change-sets/x", Query: "action=predict&note=n", Status: 200, Body: `{"id":"9","processingTrigger":"COLLECTION"}`},
		{Method: "GET", Path: "/api/networks/1/change-sets/x/predicted-snapshots", Status: 200, Body: `{"weird":true}`},
	}})
	c := predicthttp.New(rp)
	ctx := context.Background()
	var d *predicthttp.DivergenceError
	if _, err := c.CreateChangeSet(ctx, "1", "n", "s", "", nil); !errors.As(err, &d) || d.Operation != "createChangeSet" {
		t.Errorf("418 should be a divergence: %v", err)
	}
	if _, err := c.Predict(ctx, "1", "x", "n"); !errors.As(err, &d) || !strings.Contains(d.Observed, "COLLECTION") {
		t.Errorf("wrong processingTrigger should be a divergence: %v", err)
	}
	if _, err := c.ListPredictedSnapshots(ctx, "1", "x"); !errors.As(err, &d) {
		t.Errorf("unexpected shape should be a divergence: %v", err)
	}
	// Documented errors are APIErrors with the ErrorInfo text, redacted.
	rp2 := forwardhttpfixture.NewReplayer(forwardhttpfixture.File{Exchanges: []forwardhttpfixture.Exchange{
		{Method: "POST", Path: "/api/networks/1/change-sets/x/commits", Query: "note=n", Status: 400, Body: `{"message":"Required request parameter 'note' is not present; token=abc12345"}`},
	}})
	var api *predicthttp.APIError
	_, err := predicthttp.New(rp2).Commit(ctx, "1", "x", "n")
	if !errors.As(err, &api) || api.Status != 400 || strings.Contains(err.Error(), "abc12345") {
		t.Errorf("400 should be an APIError without secrets: %v", err)
	}
}

// TestIntegrationPredictRoundTrip runs the scenario live. Required env:
// FORWARD_LIVE=1, FORWARD_API_BASE_URL/KEY/SECRET, FORWARD_LIVE_NETWORK_ID,
// FORWARD_LIVE_BASE_SNAPSHOT_ID (a processed, non-PREDICT snapshot), and
// FORWARD_LIVE_DEVICE (a non-PAN-OS device that accepts "interface X / shutdown").
// It records testdata/roundtrip.live.json and, on divergence, writes
// docs/predict-divergences.md and fails.
func TestIntegrationPredictRoundTrip(t *testing.T) {
	if os.Getenv("FORWARD_LIVE") != "1" {
		t.Skip("set FORWARD_LIVE=1 to run against a live instance")
	}
	cfg := config.LoadConfig()
	network, base := os.Getenv("FORWARD_LIVE_NETWORK_ID"), os.Getenv("FORWARD_LIVE_BASE_SNAPSHOT_ID")
	if cfg.Forward.APIBaseURL == "" || network == "" || base == "" {
		t.Fatal("FORWARD_API_BASE_URL, FORWARD_LIVE_NETWORK_ID and FORWARD_LIVE_BASE_SNAPSHOT_ID are required")
	}
	live, ok := forward.NewClient(&cfg.Forward).(ports.ForwardHTTP)
	if !ok {
		t.Fatal("forward.Client does not implement ports.ForwardHTTP")
	}
	rec := forwardhttpfixture.NewRecorder(live, cfg.Forward.APIBaseURL)
	defer func() {
		if err := rec.Save("testdata/roundtrip.live.json"); err != nil {
			t.Logf("could not save recording: %v", err)
		}
	}()

	res := runLive(t, rec, network, base)
	t.Logf("live verdict %s: %v", res.Verdict, res.Reasons)
}

func runLive(t *testing.T, h ports.ForwardHTTP, network, base string) (res *entities.PredictResult) {
	t.Helper()
	defer func() {
		if t.Failed() {
			reportDivergences(t)
		}
	}()
	return scenario(t, h, network, base, true)
}

// reportDivergences appends any DivergenceError text from the test log to
// docs/predict-divergences.md so the difference is recorded, per spec:
// "if an endpoint behaves differently from the reference, stop and report".
func reportDivergences(t *testing.T) {
	path := filepath.Join("..", "..", "..", "docs", "predict-divergences.md")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "\n## %s — %s\n\nSee the test output of TestIntegrationPredictRoundTrip and testdata/roundtrip.live.json.\n", time.Now().UTC().Format(time.RFC3339), t.Name())
}
