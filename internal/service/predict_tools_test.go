package service

import (
	"testing"

	"github.com/forward-mcp/internal/domain/entities"
)

// Propose tools are catalogued and registered only when the policy allows.
func TestPredictToolsFollowProposePolicy(t *testing.T) {
	r := registerWithMode(t, entities.ServeModeRemote, false)
	names := joinNames(r.Registered())
	for _, n := range []string{"predict_create_change_set", "predict_stage_edits", "predict_run_and_wait", "predict_get_results", "predict_gc"} {
		if contains(names, " "+n+" ") {
			t.Errorf("%s must be withheld in remote mode without the flag", n)
		}
	}
	r = registerWithMode(t, entities.ServeModeRemote, true)
	names = joinNames(r.Registered())
	for _, n := range []string{"predict_create_change_set", "predict_stage_edits", "predict_run_and_wait", "predict_get_results"} {
		if !contains(names, " "+n+" ") {
			t.Errorf("%s must be registered with the propose flag", n)
		}
	}
	if contains(names, " predict_gc ") {
		t.Error("predict_gc is write-class and must never be registered remotely")
	}
	r = registerWithMode(t, entities.ServeModeStdio, false)
	if !contains(joinNames(r.Registered()), " predict_gc ") {
		t.Error("stdio registers predict_gc")
	}
}

// The mock Forward client has no raw-request support, so the tools report
// that clearly instead of panicking.
func TestPredictToolsReportUnavailableClient(t *testing.T) {
	svc := createTestService()
	svc.initPredict()
	if svc.predict != nil {
		t.Skip("mock client gained ForwardHTTP support")
	}
	if _, err := svc.predictReady(); err == nil {
		t.Fatal("expected not-initialised error")
	}
}
