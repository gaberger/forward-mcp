package service

import (
	"testing"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/forward-mcp/internal/adapters/auditlog"
	mcpadapter "github.com/forward-mcp/internal/adapters/mcp"
	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/domain/services/toolpolicy"
	"github.com/forward-mcp/internal/usecases"
)

func newTestServer() *mcp.Server {
	return mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
}

func registerWithMode(t *testing.T, mode entities.ServeMode, propose bool) *mcpadapter.Registrar {
	t.Helper()
	svc := newTestService(t)
	r := mcpadapter.NewRegistrar(newTestServer(), usecases.NewToolExposure(mode, propose), auditlog.New(svc.logger))
	if err := svc.RegisterToolsWith(r); err != nil {
		t.Fatalf("RegisterToolsWith(%s): %v", mode, err)
	}
	return r
}

// newTestService reuses the mock-backed constructor from mcp_service_test.go.
func newTestService(t *testing.T) *ForwardMCPService {
	t.Helper()
	return createTestService()
}

// TestEveryRegisteredToolIsCatalogued: RegisterToolsWith returns an error
// for any tool missing from toolpolicy.DefaultCatalog, so a passing stdio
// registration proves the catalog is complete.
func TestEveryRegisteredToolIsCatalogued(t *testing.T) {
	r := registerWithMode(t, entities.ServeModeStdio, true)
	if len(r.Skipped()) != 0 {
		t.Errorf("stdio must register everything; skipped: %v", r.Skipped())
	}
	// 3.0.0 registers 54 tools; propose tools arrive in M1b and are not
	// registered by the service yet, so the catalog may be larger.
	if got := len(r.Registered()); got < 54 {
		t.Errorf("expected at least 54 registered tools, got %d", got)
	}
}

// TestRemoteModeRegistersOnlyReadTools is the M1.0 acceptance test for
// spec non-negotiable #1.
func TestRemoteModeRegistersOnlyReadTools(t *testing.T) {
	r := registerWithMode(t, entities.ServeModeRemote, false)
	for _, name := range r.Registered() {
		spec, ok := toolpolicy.DefaultCatalog.Lookup(name)
		if !ok {
			t.Fatalf("%s registered but not catalogued", name)
		}
		if spec.Class != entities.ToolClassRead {
			t.Errorf("%s (%s/%s) must not be registered in remote mode", name, spec.Class, spec.Target)
		}
	}
	for _, must := range []string{"list_networks", "run_nqe_query_by_id", "search_paths_bulk", "get_config_diff", "list_snapshots"} {
		if !contains(joinNames(r.Registered()), " "+must+" ") {
			t.Errorf("read tool %s missing in remote mode", must)
		}
	}
	for _, mustNot := range []string{"delete_snapshot", "create_network", "set_default_network", "clear_cache", "create_entity"} {
		if contains(joinNames(r.Registered()), " "+mustNot+" ") {
			t.Errorf("%s must be withheld in remote mode", mustNot)
		}
	}
}

func joinNames(names []string) string {
	s := " "
	for _, n := range names {
		s += n + " "
	}
	return s
}
