package evalrerun

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/forward-mcp/internal/ports"
)

// FixtureFile is the on-disk shape for the fixture backend: results keyed
// by "<ref>@<snapshot>". Record one by running the Forward backend with
// -record (cmd/eval-grader) against a sandbox network.
type FixtureFile struct {
	Queries map[string][]map[string]any `json:"queries"`
	Paths   map[string]string           `json:"paths"`
}

// Fixture replays recorded re-runs. Missing keys are errors, so a case
// that needs evidence the fixture lacks fails loudly.
type Fixture struct {
	data FixtureFile
}

var _ ports.EvidenceRerun = (*Fixture)(nil)

// LoadFixture reads a FixtureFile.
func LoadFixture(path string) (*Fixture, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ReadFixture(f)
}

// ReadFixture parses a FixtureFile.
func ReadFixture(r io.Reader) (*Fixture, error) {
	var data FixtureFile
	if err := json.NewDecoder(r).Decode(&data); err != nil {
		return nil, fmt.Errorf("fixture: %w", err)
	}
	if data.Queries == nil {
		data.Queries = map[string][]map[string]any{}
	}
	if data.Paths == nil {
		data.Paths = map[string]string{}
	}
	return &Fixture{data: data}, nil
}

func key(ref, snapshotID string) string { return ref + "@" + snapshotID }

// RerunQuery implements ports.EvidenceRerun.
func (f *Fixture) RerunQuery(_ context.Context, ref, snapshotID, _ string, _ map[string]any) ([]map[string]any, error) {
	rows, ok := f.data.Queries[key(ref, snapshotID)]
	if !ok {
		return nil, fmt.Errorf("fixture has no recording for %s @ %s", ref, snapshotID)
	}
	return rows, nil
}

// RerunPathSearch implements ports.EvidenceRerun.
func (f *Fixture) RerunPathSearch(_ context.Context, ref, snapshotID, _ string) (string, error) {
	out, ok := f.data.Paths[key(ref, snapshotID)]
	if !ok {
		return "", fmt.Errorf("fixture has no recording for %s @ %s", ref, snapshotID)
	}
	return out, nil
}

// Recorder wraps a live backend and captures every re-run into a
// FixtureFile so it can be replayed later.
type Recorder struct {
	live ports.EvidenceRerun
	out  FixtureFile
}

// NewRecorder wraps live.
func NewRecorder(live ports.EvidenceRerun) *Recorder {
	return &Recorder{live: live, out: FixtureFile{Queries: map[string][]map[string]any{}, Paths: map[string]string{}}}
}

func (r *Recorder) RerunQuery(ctx context.Context, ref, snapshotID, networkID string, params map[string]any) ([]map[string]any, error) {
	rows, err := r.live.RerunQuery(ctx, ref, snapshotID, networkID, params)
	if err == nil {
		r.out.Queries[key(ref, snapshotID)] = rows
	}
	return rows, err
}

func (r *Recorder) RerunPathSearch(ctx context.Context, ref, snapshotID, networkID string) (string, error) {
	out, err := r.live.RerunPathSearch(ctx, ref, snapshotID, networkID)
	if err == nil {
		r.out.Paths[key(ref, snapshotID)] = out
	}
	return out, err
}

// Save writes the recording.
func (r *Recorder) Save(path string) error {
	b, err := json.MarshalIndent(r.out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}
