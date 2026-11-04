package certified

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/forward-mcp/internal/domain/entities"
)

func good() entities.CertifiedQuery {
	return entities.CertifiedQuery{
		ID:           "device-basic-info",
		Version:      1,
		Title:        "Device basic info",
		Intent:       "Inventory of devices",
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`),
		Owner:        "gary@example.org",
		Certified:    true,
		Source:       entities.QuerySource{QueryID: "FQ_ac651cb2901b067fe7dbfb511613ab44776d8029"},
		Fixture:      "device-basic-info.v1.json",
		Parameters: []entities.QueryParameter{
			{Name: "device", Type: entities.ParamDevice},
		},
	}
}

func TestValidateEntryAcceptsGoodEntry(t *testing.T) {
	if err := ValidateEntry(good()); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !MayBeCertified(good()) {
		t.Fatal("good entry should be certifiable")
	}
}

func TestValidateEntryReportsEveryProblem(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*entities.CertifiedQuery)
		want   string // substring of the error
	}{
		{"bad id", func(q *entities.CertifiedQuery) { q.ID = "Device_Info" }, "id \"Device_Info\""},
		{"version 0", func(q *entities.CertifiedQuery) { q.Version = 0 }, "version must be >= 1"},
		{"no title", func(q *entities.CertifiedQuery) { q.Title = "" }, "title is required"},
		{"no intent", func(q *entities.CertifiedQuery) { q.Intent = "" }, "intent is required"},
		{"no owner", func(q *entities.CertifiedQuery) { q.Owner = "" }, "owner is required"},
		{"no fixture", func(q *entities.CertifiedQuery) { q.Fixture = "" }, "fixture is required"},
		{"no source", func(q *entities.CertifiedQuery) { q.Source = entities.QuerySource{} }, "one of nqe_query_id or nqe_path"},
		{"both sources", func(q *entities.CertifiedQuery) { q.Source.Path = "/x" }, "only one of"},
		{"bad query id", func(q *entities.CertifiedQuery) { q.Source.QueryID = "FQ_short" }, "FQ_<40 hex>"},
		{"bad repo", func(q *entities.CertifiedQuery) { q.Source.Repository = "mine" }, "repository"},
		{"no schema", func(q *entities.CertifiedQuery) { q.OutputSchema = nil }, "output_schema is required"},
		{"schema not json", func(q *entities.CertifiedQuery) { q.OutputSchema = json.RawMessage(`{`) }, "not valid JSON"},
		{"schema not object", func(q *entities.CertifiedQuery) { q.OutputSchema = json.RawMessage(`{"type":"array"}`) }, "type must be \"object\""},
		{"param no name", func(q *entities.CertifiedQuery) { q.Parameters[0].Name = "" }, "parameters[0].name is required"},
		{"param bad type", func(q *entities.CertifiedQuery) { q.Parameters[0].Type = "uuid" }, "type \"uuid\""},
		{"param dup", func(q *entities.CertifiedQuery) {
			q.Parameters = append(q.Parameters, entities.QueryParameter{Name: "device", Type: entities.ParamString})
		}, "duplicated"},
		{"required with default", func(q *entities.CertifiedQuery) {
			q.Parameters[0].Required = true
			q.Parameters[0].Default = json.RawMessage(`"r1"`)
		}, "cannot have a default"},
	}
	for _, c := range cases {
		q := good()
		c.mutate(&q)
		err := ValidateEntry(q)
		if err == nil {
			t.Errorf("%s: expected error", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q does not mention %q", c.name, err, c.want)
		}
	}

	// Several problems at once are all reported.
	q := good()
	q.Title, q.Owner, q.Fixture = "", "", ""
	err := ValidateEntry(q)
	for _, want := range []string{"title", "owner", "fixture"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("multi-error should mention %s: %v", want, err)
		}
	}
	if MayBeCertified(q) {
		t.Error("invalid entry must not be certifiable")
	}
}

func TestValidateLibrary(t *testing.T) {
	lib := &entities.CertifiedLibrary{Version: "0.1.0", Queries: []entities.CertifiedQuery{good(), good()}}
	err := ValidateLibrary(lib)
	if err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Errorf("expected duplicate id error, got %v", err)
	}
	lib = &entities.CertifiedLibrary{Version: "1.0", Queries: []entities.CertifiedQuery{good()}}
	if err := ValidateLibrary(lib); err == nil || !strings.Contains(err.Error(), "MAJOR.MINOR.PATCH") {
		t.Errorf("expected version error, got %v", err)
	}
	lib.Version = "1.0.0"
	if err := ValidateLibrary(lib); err != nil {
		t.Errorf("unexpected: %v", err)
	}
}
