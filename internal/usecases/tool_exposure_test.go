package usecases

import (
	"testing"

	"github.com/forward-mcp/internal/domain/entities"
)

func TestDecideUnknownToolIsAnError(t *testing.T) {
	e := NewToolExposure(entities.ServeModeStdio, false)
	if _, _, err := e.Decide("not_a_tool"); err == nil {
		t.Fatal("expected error for uncatalogued tool")
	}
}

func TestStdioPlanRegistersAll(t *testing.T) {
	e := NewToolExposure(entities.ServeModeStdio, false)
	for _, p := range e.Plan() {
		if !p.Registered {
			t.Errorf("stdio: %s should be registered", p.Spec.Name)
		}
	}
}

func TestRemotePlanNeverRegistersWrites(t *testing.T) {
	for _, flag := range []bool{false, true} {
		e := NewToolExposure(entities.ServeModeRemote, flag)
		for _, p := range e.Plan() {
			switch p.Spec.Class {
			case entities.ToolClassWrite:
				if p.Registered {
					t.Errorf("remote(flag=%v): write tool %s registered", flag, p.Spec.Name)
				}
			case entities.ToolClassPropose:
				if p.Registered != flag {
					t.Errorf("remote(flag=%v): propose tool %s registered=%v", flag, p.Spec.Name, p.Registered)
				}
			case entities.ToolClassRead:
				if !p.Registered {
					t.Errorf("remote(flag=%v): read tool %s not registered", flag, p.Spec.Name)
				}
			}
		}
	}
}
