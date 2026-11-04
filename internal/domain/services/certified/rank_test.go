package certified

import (
	"encoding/json"
	"errors"
	"hash/fnv"
	"strings"
	"testing"

	"github.com/forward-mcp/internal/domain/entities"
)

// bagEmbedder is a deterministic bag-of-words embedder: each lower-cased
// token is hashed into one of 64 buckets. Good enough to make overlapping
// vocabularies similar, which is all the ranking tests need.
type bagEmbedder struct{ fail bool }

func (b bagEmbedder) GenerateEmbedding(text string) ([]float64, error) {
	if b.fail {
		return nil, errors.New("embedder down")
	}
	vec := make([]float64, 64)
	for _, tok := range strings.Fields(strings.ToLower(text)) {
		tok = strings.Trim(tok, ".,;:?!()")
		if tok == "" {
			continue
		}
		h := fnv.New32a()
		_, _ = h.Write([]byte(tok))
		vec[h.Sum32()%64]++
	}
	return vec, nil
}

func testLib() *entities.CertifiedLibrary {
	schema := json.RawMessage(`{"type":"object","required":["name"],"properties":{"name":{"type":"string"},"count":{"type":"integer"},"ip":{"type":["string","null"]}}}`)
	return &entities.CertifiedLibrary{Version: "0.1.0", Queries: []entities.CertifiedQuery{
		{ID: "device-basic-info", Version: 1, Title: "Device basic info", Intent: "Inventory of every device: name vendor platform os version",
			Synonyms: []string{"device inventory", "list all devices"}, Owner: "o", Certified: true, OutputSchema: schema,
			Source: entities.QuerySource{QueryID: "FQ_ac651cb2901b067fe7dbfb511613ab44776d8029"}, Fixture: "f"},
		{ID: "bgp-peerings-not-established", Version: 1, Title: "BGP peerings not established", Intent: "BGP neighbors whose session is not established",
			Synonyms: []string{"bgp neighbors down", "bgp sessions not up"}, Owner: "o", Certified: true, OutputSchema: schema,
			Source: entities.QuerySource{QueryID: "FQ_34f8d29d3c32a7a183d38cf22dc9eea7ea645bea"}, Fixture: "f",
			Parameters: []entities.QueryParameter{{Name: "device", Type: entities.ParamDevice, Required: true, Description: "device name"}}},
		{ID: "os-support-status", Version: 1, Title: "OS support status", Intent: "Whether each device OS version is supported end of support dates",
			Synonyms: []string{"end of life os", "unsupported os"}, Owner: "o", Certified: false, OutputSchema: schema,
			Source: entities.QuerySource{QueryID: "FQ_fc33d9fd70ba19a18455b0e4d26ca8420003d9cc"}, Fixture: "f"},
	}}
}

func TestSearchRanksExpectedEntryFirst(t *testing.T) {
	r, err := NewRanker(testLib(), bagEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"which bgp neighbors down on router-1":   "bgp-peerings-not-established",
		"give me the device inventory":           "device-basic-info",
		"list all devices with their os version": "device-basic-info",
		"which devices have end of life os":      "os-support-status",
	}
	for q, want := range cases {
		m, err := r.Search(q, 3)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		if len(m) == 0 || m[0].ID != want {
			t.Errorf("%q: want %s first, got %+v", q, want, m)
		}
		if m[0].Score <= 0 || m[0].Score > 1 {
			t.Errorf("%q: score out of range: %v", q, m[0].Score)
		}
	}
}

func TestSearchSynonymBoostIsExplained(t *testing.T) {
	r, _ := NewRanker(testLib(), bagEmbedder{})
	m, _ := r.Search("bgp neighbors down please", 1)
	if len(m) != 1 || !strings.Contains(m[0].Why, "bgp neighbors down") {
		t.Errorf("expected matched phrase in why: %+v", m)
	}
}

func TestSearchLimitsAndValidates(t *testing.T) {
	r, _ := NewRanker(testLib(), bagEmbedder{})
	if _, err := r.Search("   ", 5); err == nil {
		t.Error("empty question must error")
	}
	m, _ := r.Search("device os bgp", 2)
	if len(m) > 2 {
		t.Errorf("limit not applied: %d", len(m))
	}
	m, _ = r.Search("device os bgp", 0)
	if len(m) > 5 {
		t.Errorf("default limit not applied: %d", len(m))
	}
}

func TestNewRankerFailsWhenEmbedderFails(t *testing.T) {
	if _, err := NewRanker(testLib(), bagEmbedder{fail: true}); err == nil {
		t.Error("expected error")
	}
}

func TestCheckAnswerableThresholds(t *testing.T) {
	lib := testLib()
	cases := []struct {
		name    string
		matches []Match
		want    Recommendation
		missing string
	}{
		{"strong certified", []Match{{ID: "bgp-peerings-not-established", Score: 0.9, Certified: true}}, RecommendCertified, `parameter "device" (device) is required`},
		{"strong uncertified", []Match{{ID: "os-support-status", Score: 0.8, Certified: false}}, RecommendAdhoc, "not certified yet"},
		{"weak", []Match{{ID: "device-basic-info", Score: 0.4, Certified: true}}, RecommendAdhoc, "no certified query covers"},
		{"none", []Match{{ID: "device-basic-info", Score: 0.1, Certified: true}}, RecommendUnanswerable, "no library query matches"},
		{"empty", nil, RecommendUnanswerable, "no library query matches"},
	}
	for _, c := range cases {
		a := CheckAnswerable("q", c.matches, lib)
		if a.Recommendation != c.want {
			t.Errorf("%s: want %s got %s", c.name, c.want, a.Recommendation)
		}
		if !strings.Contains(strings.Join(a.Missing, "\n"), c.missing) {
			t.Errorf("%s: missing %q in %v", c.name, c.missing, a.Missing)
		}
	}
	a := CheckAnswerable("q", []Match{{ID: "a", Score: 0.7}, {ID: "b", Score: 0.35}, {ID: "c", Score: 0.1}}, lib)
	if len(a.MatchedQueryIDs) != 2 || a.Coverage != 0.7 {
		t.Errorf("matched ids/coverage wrong: %+v", a)
	}
}

func TestBindParameters(t *testing.T) {
	q := entities.CertifiedQuery{ID: "x", Parameters: []entities.QueryParameter{
		{Name: "device", Type: entities.ParamDevice, Required: true},
		{Name: "limit", Type: entities.ParamInt, Default: json.RawMessage(`100`)},
		{Name: "ip", Type: entities.ParamIP},
		{Name: "net", Type: entities.ParamCIDR},
		{Name: "flag", Type: entities.ParamBool},
		{Name: "names", Type: entities.ParamList},
	}}
	out, err := BindParameters(q, map[string]any{"device": "r1", "ip": "10.0.0.1", "net": "10.0.0.0/24", "flag": true, "names": []any{"a"}})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if out["limit"] != float64(100) {
		t.Errorf("default not applied: %v", out["limit"])
	}
	bad := map[string]any{"limit": 1.5, "ip": "nope", "net": "10.0.0.1", "flag": "yes", "names": "a", "extra": 1}
	_, err = BindParameters(q, bad)
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{`"device" (device) is required`, `"limit" must be an integer`, `"ip" must be an IP`, `"net" must be a CIDR`, `"flag" must be a boolean`, `"names" must be a list`, `"extra" is not declared`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
}

func TestCheckOutput(t *testing.T) {
	q := testLib().Queries[0]
	rows := []map[string]any{
		{"name": "r1", "count": float64(3), "ip": nil},
		{"name": "r2", "count": 2.5, "ip": "10.0.0.1"},
		{"count": float64(1)},
	}
	w := CheckOutput(q, rows, 0)
	joined := strings.Join(w, "\n")
	for _, want := range []string{`row missing required column "name"`, `column "count": expected [integer], got number`} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %v", want, w)
		}
	}
	if len(w) != 2 {
		t.Errorf("warnings should be de-duplicated: %v", w)
	}
	if CheckOutput(q, nil, 0) != nil {
		t.Error("no rows, no warnings")
	}
	if w := CheckOutput(q, rows[:1], 0); len(w) != 0 {
		t.Errorf("clean row should not warn: %v", w)
	}
}
