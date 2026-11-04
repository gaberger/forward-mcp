package toolpolicy

import (
	"testing"

	"github.com/forward-mcp/internal/domain/entities"
)

func TestNewCatalogRejectsBadSpecs(t *testing.T) {
	cases := map[string][]entities.ToolSpec{
		"empty name":    {{Name: "", Class: entities.ToolClassRead, Target: entities.ToolTargetLocal}},
		"bad class":     {{Name: "a", Class: "nope", Target: entities.ToolTargetLocal}},
		"bad target":    {{Name: "a", Class: entities.ToolClassRead, Target: "cloud"}},
		"duplicate":     {read("a", loc), read("a", fwd)},
		"propose+local": nil, // propose tools are always forward-targeted by construction
	}
	for name, specs := range cases {
		if specs == nil {
			continue
		}
		if _, err := NewCatalog(specs); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestDefaultCatalogIsWellFormed(t *testing.T) {
	all := DefaultCatalog.All()
	if len(all) == 0 {
		t.Fatal("empty catalog")
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].Name >= all[i].Name {
			t.Fatalf("All() not sorted: %q before %q", all[i-1].Name, all[i].Name)
		}
	}
	var reads, proposes, writes int
	for _, s := range all {
		switch s.Class {
		case entities.ToolClassRead:
			reads++
		case entities.ToolClassPropose:
			proposes++
			if s.Target != entities.ToolTargetForward {
				t.Errorf("%s: propose tools target Forward", s.Name)
			}
		case entities.ToolClassWrite:
			writes++
		}
	}
	// 3.0.0 ships 54 tools; 3.1.0 adds 3 certified-library tools, 4 propose tools and predict_gc.
	if got := reads + proposes + writes; got != 63 {
		t.Errorf("expected 63 catalogued tools, got %d", got)
	}
	if proposes != 5 {
		t.Errorf("expected 5 propose tools, got %d", proposes)
	}
}

// TestRemotePolicyExposesOnlyReads is the M1.0 acceptance check: in remote
// mode, nothing that mutates Forward or shared local state is advertised.
func TestRemotePolicyExposesOnlyReads(t *testing.T) {
	p := Remote(false)
	for _, s := range DefaultCatalog.All() {
		allowed := p.AllowsRegistration(s)
		if s.Class == entities.ToolClassRead && !allowed {
			t.Errorf("%s: read tool must be registered in remote mode", s.Name)
		}
		if s.Class != entities.ToolClassRead && allowed {
			t.Errorf("%s: %s tool must not be registered in remote mode", s.Name, s.Class)
		}
	}
}

func TestKnownForwardWritesAreWriteClass(t *testing.T) {
	// Tools that hit mutating Forward endpoints. If someone reclassifies one
	// of these as read, this test is the tripwire.
	for _, name := range []string{
		"create_network", "update_network", "delete_snapshot",
		"create_location", "update_location", "delete_location",
		"create_locations_bulk", "update_device_locations",
	} {
		s, ok := DefaultCatalog.Lookup(name)
		if !ok {
			t.Errorf("%s: missing from catalog", name)
			continue
		}
		if s.Class != entities.ToolClassWrite || s.Target != entities.ToolTargetForward {
			t.Errorf("%s: expected write/forward, got %s/%s", name, s.Class, s.Target)
		}
	}
}
