package usecases

import (
	"context"
	"errors"
	"hash/fnv"
	"os"
	"strings"
	"testing"

	"github.com/forward-mcp/internal/adapters/certifiedfs"
	"github.com/forward-mcp/internal/domain/entities"
)

type bagEmbedder struct{}

func (bagEmbedder) GenerateEmbedding(text string) ([]float64, error) {
	vec := make([]float64, 64)
	for _, tok := range strings.Fields(strings.ToLower(text)) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(strings.Trim(tok, ".,;:?!()")))
		vec[h.Sum32()%64]++
	}
	return vec, nil
}

type fakeRunner struct {
	got  entities.NQEQueryParams
	res  *entities.NQERunResult
	err  error
	fsys *certifiedfs.Loader
}

func (f *fakeRunner) RunQueryByID(_ context.Context, p entities.NQEQueryParams) (*entities.NQERunResult, error) {
	f.got = p
	return f.res, f.err
}

func newUC(t *testing.T, runner *fakeRunner) *CertifiedQueries {
	t.Helper()
	loader := certifiedfs.New(os.DirFS("../../spec/certified"))
	uc, err := NewCertifiedQueries(context.Background(), loader, bagEmbedder{}, runner)
	if err != nil {
		t.Fatal(err)
	}
	return uc
}

func TestSearchAndAnswerableOverSeedLibrary(t *testing.T) {
	uc := newUC(t, &fakeRunner{})
	m, err := uc.Search("which bgp peers are down", 3)
	if err != nil || len(m) == 0 || m[0].ID != "bgp-peerings-not-established" {
		t.Fatalf("search: %v %+v", err, m)
	}
	a, err := uc.CheckAnswerable("which bgp peers are down")
	if err != nil {
		t.Fatal(err)
	}
	if a.Recommendation != "certified" || a.MatchedQueryIDs[0] != "bgp-peerings-not-established" {
		t.Errorf("answerable: %+v", a)
	}
	if uc.Library().Version == "" {
		t.Error("library version missing")
	}
}

func TestRunWrapsEvidenceAndWarnings(t *testing.T) {
	loader := certifiedfs.New(os.DirFS("../../spec/certified"))
	lib, _ := loader.Load(context.Background())
	q, _ := lib.Lookup("device-basic-info")
	fixture, _ := loader.ReadFixture(context.Background(), q)
	// Introduce drift: a row with the wrong type.
	fixture.Items = append(fixture.Items, map[string]any{"name": 42})

	runner := &fakeRunner{res: fixture}
	uc := newUC(t, runner)
	out, err := uc.Run(context.Background(), RunRequest{ID: "device-basic-info", NetworkID: "162112", SnapshotID: "snap-9", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if runner.got.QueryID != q.Source.QueryID || runner.got.NetworkID != "162112" || runner.got.SnapshotID != "snap-9" || runner.got.Options.MaxResults != 10 {
		t.Errorf("runner params: %+v", runner.got)
	}
	if out.Evidence.Ref != "certified:device-basic-info@1" || out.Evidence.SnapshotID != fixture.SnapshotID || !out.Evidence.Certified || out.Evidence.LibraryVersion != lib.Version {
		t.Errorf("evidence: %+v", out.Evidence)
	}
	if out.RowCount != len(fixture.Items) || len(out.Warnings) == 0 {
		t.Errorf("rows/warnings: %d %v", out.RowCount, out.Warnings)
	}
}

func TestRunErrors(t *testing.T) {
	uc := newUC(t, &fakeRunner{err: errors.New("boom")})
	if _, err := uc.Run(context.Background(), RunRequest{ID: "nope", NetworkID: "n"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("unknown id: %v", err)
	}
	if _, err := uc.Run(context.Background(), RunRequest{ID: "device-basic-info"}); err == nil || !strings.Contains(err.Error(), "network_id is required") {
		t.Errorf("no network: %v", err)
	}
	if _, err := uc.Run(context.Background(), RunRequest{ID: "bgp-peerings-not-established", NetworkID: "n", Params: map[string]any{"device": 1}}); err == nil || !strings.Contains(err.Error(), `"device" must be a non-empty string`) {
		t.Errorf("bad param: %v", err)
	}
	if _, err := uc.Run(context.Background(), RunRequest{ID: "device-basic-info", NetworkID: "n"}); err == nil || err.Error() != "boom" {
		t.Errorf("runner error should pass through: %v", err)
	}
}

func TestEvidenceBuilders(t *testing.T) {
	e := AdhocNQEEvidence("FQ_x", "s1")
	if e.Ref != "nqe:FQ_x" || e.Certified || e.Kind != "nqe" {
		t.Errorf("%+v", e)
	}
	p := PathSearchEvidence("10.0.0.1", "10.0.0.2", "s1")
	if p.Ref != "path:10.0.0.1->10.0.0.2" || p.Kind != "path_search" {
		t.Errorf("%+v", p)
	}
}
