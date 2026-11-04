package certifiedfs

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

// TestLoadsCheckedInLibrary is the golden test over spec/certified: the
// library that ships with the server must always load.
func TestLoadsCheckedInLibrary(t *testing.T) {
	lib, err := New(os.DirFS("../../../spec/certified")).Load(context.Background())
	if err != nil {
		t.Fatalf("spec/certified does not load:\n%v", err)
	}
	if lib.Version == "" {
		t.Fatal("empty version")
	}
	if len(lib.Queries) < 3 {
		t.Fatalf("expected at least 3 seed entries, got %d", len(lib.Queries))
	}
	q, ok := lib.Lookup("device-basic-info")
	if !ok {
		t.Fatal("device-basic-info missing")
	}
	if !q.Certified || q.Source.QueryID == "" {
		t.Errorf("device-basic-info should be certified with a query id: %+v", q)
	}
	res, err := New(os.DirFS("../../../spec/certified")).ReadFixture(context.Background(), q)
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if res.SnapshotID == "" || len(res.Items) == 0 {
		t.Errorf("fixture should carry a snapshot id and rows: %+v", res)
	}
	// Uncertified entries load too; they are just labelled.
	if u, ok := lib.Lookup("os-support-status"); !ok || u.Certified {
		t.Errorf("os-support-status should load as uncertified")
	}
}

const goodEntry = `{
  "id": "%s", "version": 1, "title": "T", "intent": "I",
  "output_schema": {"type": "object"},
  "owner": "o@example.org", "certified": %s,
  "source": {"nqe_query_id": "FQ_ac651cb2901b067fe7dbfb511613ab44776d8029"},
  "fixture": "%s"
}`

func lib(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{"VERSION": {Data: []byte("0.1.0\n")}}
	for k, v := range files {
		m[k] = &fstest.MapFile{Data: []byte(v)}
	}
	return m
}

func TestLoadReportsFileAndField(t *testing.T) {
	fsys := lib(map[string]string{
		"queries/good.json":     fmt.Sprintf(goodEntry, "good", "false", "f.json"),
		"queries/bad-id.json":   fmt.Sprintf(goodEntry, "other", "false", "f.json"),
		"queries/no-fix.json":   fmt.Sprintf(goodEntry, "no-fix", "false", "missing.json"),
		"queries/not-cert.json": `{"id":"not-cert","version":1,"title":"T","intent":"I","output_schema":{"type":"object"},"owner":"","certified":true,"source":{"nqe_path":"/x"},"fixture":"f.json"}`,
		"queries/unknown.json":  `{"id":"unknown","version":1,"title":"T","intent":"I","output_schema":{"type":"object"},"owner":"o","certified":false,"source":{"nqe_path":"/x"},"fixture":"f.json","extra":1}`,
		"fixtures/f.json":       `{"snapshotId":"s1","items":[]}`,
	})
	_, err := New(fsys).Load(context.Background())
	if err == nil {
		t.Fatal("expected errors")
	}
	msg := err.Error()
	for _, want := range []string{
		`queries/bad-id.json: id "other" must equal the file name "bad-id"`,
		`queries/no-fix.json: fixture "missing.json" not found`,
		`queries/not-cert.json: owner is required`,
		`queries/not-cert.json: certified:true but entry does not meet the bar`,
		`queries/unknown.json: json: unknown field "extra"`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q in:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "queries/good.json") {
		t.Errorf("good entry should not be reported:\n%s", msg)
	}
}

func TestLoadRejectsBadFixtureAndEscape(t *testing.T) {
	fsys := lib(map[string]string{
		"queries/a.json":       fmt.Sprintf(goodEntry, "a", "false", "a.json"),
		"fixtures/a.json":      `{"items":[]}`, // no snapshotId
		"queries/b.json":       fmt.Sprintf(goodEntry, "b", "false", "../queries/a.json"),
		"queries/c.json":       fmt.Sprintf(goodEntry, "c", "false", "c.json"),
		"fixtures/c.json":      `{"snapshotId":"s","items":[]}`,
		"queries/README.notes": "ignored",
	})
	_, err := New(fsys).Load(context.Background())
	if err == nil {
		t.Fatal("expected errors")
	}
	if !strings.Contains(err.Error(), "queries/a.json: fixture: a.json: snapshotId is required") {
		t.Errorf("bad fixture not reported: %v", err)
	}
	// "../queries/a.json" is cleaned to fixtures/queries/a.json, which does not exist.
	if !strings.Contains(err.Error(), `queries/b.json: fixture "../queries/a.json" not found`) {
		t.Errorf("escape not rejected: %v", err)
	}
}

func TestLoadRequiresVersion(t *testing.T) {
	fsys := fstest.MapFS{"queries/a.json": {Data: []byte("{}")}}
	if _, err := New(fsys).Load(context.Background()); err == nil || !strings.Contains(err.Error(), "VERSION") {
		t.Errorf("expected VERSION error, got %v", err)
	}
	fsys = lib(map[string]string{"queries/c.json": fmt.Sprintf(goodEntry, "c", "false", "c.json"), "fixtures/c.json": `{"snapshotId":"s","items":[]}`})
	fsys["VERSION"] = &fstest.MapFile{Data: []byte("v1\n")}
	if _, err := New(fsys).Load(context.Background()); err == nil || !strings.Contains(err.Error(), "MAJOR.MINOR.PATCH") {
		t.Errorf("expected semver error, got %v", err)
	}
}
