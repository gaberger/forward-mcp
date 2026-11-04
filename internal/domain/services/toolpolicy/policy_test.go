package toolpolicy

import (
	"testing"

	"github.com/forward-mcp/internal/domain/entities"
)

func spec(class entities.ToolClass, target entities.ToolTarget) entities.ToolSpec {
	return entities.ToolSpec{Name: "t", Class: class, Target: target}
}

func TestStdioRegistersEverything(t *testing.T) {
	p := Stdio()
	for _, c := range []entities.ToolClass{entities.ToolClassRead, entities.ToolClassPropose, entities.ToolClassWrite} {
		for _, tg := range []entities.ToolTarget{entities.ToolTargetForward, entities.ToolTargetLocal} {
			if !p.AllowsRegistration(spec(c, tg)) {
				t.Errorf("stdio should register %s/%s", c, tg)
			}
			if ok, _ := p.AllowsCall(spec(c, tg), entities.LocalCaller()); !ok {
				t.Errorf("stdio should allow call to %s/%s", c, tg)
			}
		}
	}
}

func TestRemoteDefaultIsReadOnly(t *testing.T) {
	p := Remote(false)
	if !p.AllowsRegistration(spec(entities.ToolClassRead, entities.ToolTargetForward)) {
		t.Error("remote must register read tools")
	}
	if p.AllowsRegistration(spec(entities.ToolClassPropose, entities.ToolTargetForward)) {
		t.Error("remote without flag must not register propose tools")
	}
	for _, tg := range []entities.ToolTarget{entities.ToolTargetForward, entities.ToolTargetLocal} {
		if p.AllowsRegistration(spec(entities.ToolClassWrite, tg)) {
			t.Errorf("remote must never register write/%s tools", tg)
		}
	}
}

func TestRemoteProposeNeedsFlagAndScope(t *testing.T) {
	p := Remote(true)
	s := spec(entities.ToolClassPropose, entities.ToolTargetForward)
	if !p.AllowsRegistration(s) {
		t.Fatal("remote with flag must register propose tools")
	}
	if ok, reason := p.AllowsCall(s, entities.Caller{Subject: "agent-a"}); ok {
		t.Error("propose call without predict scope must be denied")
	} else if reason == "" {
		t.Error("denial must carry a reason")
	}
	if ok, _ := p.AllowsCall(s, entities.Caller{Subject: "agent-a", Scopes: []string{entities.ScopePredict}}); !ok {
		t.Error("propose call with predict scope must be allowed")
	}
	// Write stays off even with the propose flag.
	if p.AllowsRegistration(spec(entities.ToolClassWrite, entities.ToolTargetForward)) {
		t.Error("propose flag must not enable write tools")
	}
}

func TestInvalidModeFallsBackToRemote(t *testing.T) {
	p := New(entities.ServeMode("bogus"))
	if p.Mode() != entities.ServeModeRemote {
		t.Fatalf("invalid mode should become remote, got %s", p.Mode())
	}
	if p.AllowsRegistration(spec(entities.ToolClassWrite, entities.ToolTargetLocal)) {
		t.Error("fallback policy must be restrictive")
	}
}

func TestInvalidClassNeverRegistered(t *testing.T) {
	bad := entities.ToolSpec{Name: "x", Class: "maybe", Target: entities.ToolTargetLocal}
	if Stdio().AllowsRegistration(bad) || Remote(true).AllowsRegistration(bad) {
		t.Error("invalid class must never be registered")
	}
}
