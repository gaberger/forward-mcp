package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCertifiedToolsAreRegisteredAsRead(t *testing.T) {
	r := registerWithMode(t, "remote", false)
	names := joinNames(r.Registered())
	for _, n := range []string{"search_certified_queries", "check_answerable", "run_certified_query"} {
		if !contains(names, " "+n+" ") {
			t.Errorf("%s must be available in remote mode", n)
		}
	}
}

func TestSearchAndCheckAnswerableTools(t *testing.T) {
	svc := createTestService()
	if svc.certified == nil {
		t.Fatalf("certified library not loaded in test service: %v", svc.certifiedErr)
	}
	res, err := svc.searchCertifiedQueries(context.Background(), SearchCertifiedQueriesArgs{Question: "list all devices", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	out := contentText(res.Content[0])
	if !contains(out, `"device-basic-info"`) || !contains(out, `"library_version"`) {
		t.Errorf("unexpected search output: %s", out)
	}

	res, err = svc.checkAnswerable(context.Background(), CheckAnswerableArgs{Question: "which bgp neighbors are down"})
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Answerability struct {
			Recommendation string   `json:"recommendation"`
			Matched        []string `json:"matched_query_ids"`
		} `json:"answerability"`
	}
	if err := json.Unmarshal([]byte(contentText(res.Content[0])), &parsed); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if parsed.Answerability.Recommendation == "" || len(parsed.Answerability.Matched) == 0 {
		t.Errorf("empty answerability: %+v", parsed)
	}
}

func TestRunCertifiedQueryToolUsesMockClientAndCitesEvidence(t *testing.T) {
	svc := createTestService()
	res, err := svc.runCertifiedQuery(context.Background(), RunCertifiedQueryArgs{ID: "device-basic-info"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	out := contentText(res.Content[0])
	var parsed struct {
		Evidence struct {
			K         string `json:"kind"`
			R         string `json:"ref"`
			S         string `json:"snapshot_id"`
			Certified bool   `json:"certified"`
		} `json:"evidence"`
		Certified bool `json:"certified"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if parsed.Evidence.K != "nqe" || !strings.HasPrefix(parsed.Evidence.R, "certified:device-basic-info@") || !parsed.Evidence.Certified {
		t.Errorf("evidence: %+v", parsed.Evidence)
	}

	// Unknown parameter is rejected before any call to Forward.
	if _, err := svc.runCertifiedQuery(context.Background(), RunCertifiedQueryArgs{ID: "device-basic-info", Parameters: map[string]any{"bogus": 1}}); err == nil || !contains(err.Error(), "not declared") {
		t.Errorf("expected undeclared parameter error, got %v", err)
	}
}

func TestCertifiedToolsReportMissingLibrary(t *testing.T) {
	svc := createTestService()
	svc.certified = nil
	svc.initCertified("/nonexistent/certified")
	if _, err := svc.checkAnswerable(context.Background(), CheckAnswerableArgs{Question: "x"}); err == nil || !contains(err.Error(), "FORWARD_CERTIFIED_LIBRARY_PATH") {
		t.Errorf("expected helpful error, got %v", err)
	}
}

func TestAdhocNQEResponseCarriesEvidence(t *testing.T) {
	svc := createTestService()
	res, err := svc.runNQEQueryByID(RunNQEQueryByIDArgs{QueryID: "FQ_ac651cb2901b067fe7dbfb511613ab44776d8029", Options: &NQEQueryOptions{Limit: 10}})
	if err != nil {
		t.Fatalf("run_nqe_query_by_id: %v", err)
	}
	out := contentText(res.Content[0])
	if !contains(out, `Evidence: {"kind":"nqe","ref":"nqe:FQ_ac651cb2901b067fe7dbfb511613ab44776d8029"`) || !contains(out, `"certified":false`) {
		t.Errorf("missing uncertified evidence line:\n%s", out)
	}
}
