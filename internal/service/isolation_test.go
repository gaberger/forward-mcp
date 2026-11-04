package service

import (
	"strings"
	"testing"

	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/forward"
)

// Spec non-negotiable #5 / PLAN.md 1c.4: one identity cannot read another's
// cached results, memory-graph records or bloom filters. These run under
// -race in CI (make test uses the race flag via CI; locally: go test -race).

var (
	callerA = entities.Caller{Subject: "agent-a", Issuer: "test"}
	callerB = entities.Caller{Subject: "agent-b", Issuer: "test"}
)

func TestLocalCallerGetsTheUnscopedService(t *testing.T) {
	svc := createTestService()
	if svc.forCaller(entities.LocalCaller()) != svc {
		t.Fatal("local caller must use the original service (stdio behaviour)")
	}
	if svc.forCaller(entities.Caller{}) != svc {
		t.Fatal("empty caller must use the original service")
	}
	a := svc.forCaller(callerA)
	if a == svc || a.subject != "agent-a" {
		t.Fatal("remote caller must get a scoped view")
	}
	if a.forwardClient != svc.forwardClient || a.semanticCache != svc.semanticCache || a.config != svc.config {
		t.Error("shared components must be shared by the view")
	}
}

func TestPartitionIDs(t *testing.T) {
	if entities.PartitionID("inst", entities.LocalSubject) != "inst" || entities.PartitionID("inst", "") != "inst" {
		t.Error("local subject keeps the bare instance id")
	}
	a, b := entities.PartitionID("inst", "agent-a"), entities.PartitionID("inst", "agent-b")
	if a == b || !strings.HasPrefix(a, "inst-") || len(a) != len("inst-")+16 {
		t.Errorf("partition ids: %s %s", a, b)
	}
	if strings.Contains(a, "agent") {
		t.Error("subject must not appear in the partition id")
	}
}

func TestSemanticCacheIsolation(t *testing.T) {
	svc := createTestService()
	a, b := svc.forCaller(callerA), svc.forCaller(callerB)
	result := &forward.NQERunResult{SnapshotID: "snap-1", Items: []map[string]interface{}{{"name": "router-1"}}}

	if err := a.cache().Put("query_id:FQ_1|params:map[]", "162112", "snap-1", result); err != nil {
		t.Fatal(err)
	}
	if _, hit := a.cache().Get("query_id:FQ_1|params:map[]", "162112", "snap-1"); !hit {
		t.Error("A must hit its own exact entry")
	}
	if _, hit := b.cache().Get("query_id:FQ_1|params:map[]", "162112", "snap-1"); hit {
		t.Error("B must not hit A's exact entry")
	}
	if _, hit := svc.cache().Get("query_id:FQ_1|params:map[]", "162112", "snap-1"); hit {
		t.Error("local caller must not hit A's entry")
	}
	// Semantic (similarity) path: identical query text, so similarity is 1.0.
	if _, hit := b.cache().Get("query_id:FQ_1|params:map[]", "162112", ""); hit {
		t.Error("B must not get a semantic match on A's entry")
	}
	if sims, _ := b.cache().FindSimilarQueries("query_id:FQ_1|params:map[]", 10); len(sims) != 0 {
		t.Errorf("B's similar-query suggestions leaked A's entries: %d", len(sims))
	}
	if sims, _ := a.cache().FindSimilarQueries("query_id:FQ_1|params:map[]", 10); len(sims) == 0 {
		t.Error("A should see its own entry in suggestions")
	}
	// Same subject, different network: separate keys.
	if _, hit := a.cache().Get("query_id:FQ_1|params:map[]", "999", "snap-1"); hit {
		t.Error("network id must partition the cache")
	}
}

func TestMemoryGraphIsolation(t *testing.T) {
	svc := createTestService()
	if svc.memorySystem == nil {
		t.Skip("memory system unavailable in this environment")
	}
	a, b := svc.forCaller(callerA), svc.forCaller(callerB)

	ent, err := a.memorySystem.CreateEntity("router-1", "device", map[string]interface{}{"network_id": "162112"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.memorySystem.AddObservation(ent.ID, "bgp neighbor 192.0.2.2 idle", "finding", nil); err != nil {
		t.Fatal(err)
	}

	if got, err := a.memorySystem.GetEntity(ent.ID); err != nil || got == nil {
		t.Errorf("A must read its own entity: %v", err)
	}
	if got, err := b.memorySystem.GetEntity(ent.ID); err == nil && got != nil {
		t.Error("B must not read A's entity by id")
	}
	if got, err := svc.memorySystem.GetEntity(ent.ID); err == nil && got != nil {
		t.Error("local caller must not read A's entity")
	}
	found, err := b.memorySystem.SearchEntities("router", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Errorf("B's search leaked A's entities: %d", len(found))
	}
	obs, _ := b.memorySystem.GetObservations(ent.ID, "")
	if len(obs) != 0 {
		t.Errorf("B read A's observations: %d", len(obs))
	}
	if err := b.memorySystem.DeleteEntity(ent.ID); err == nil {
		if got, _ := a.memorySystem.GetEntity(ent.ID); got == nil {
			t.Error("B must not be able to delete A's entity")
		}
	}
}

func TestBloomFilterIsolation(t *testing.T) {
	svc := createTestService()
	a, b := svc.forCaller(callerA), svc.forCaller(callerB)
	if a.bloomManager == b.bloomManager || a.bloomManager == svc.bloomManager {
		t.Fatal("bloom managers must be per caller")
	}
	result := &forward.NQERunResult{SnapshotID: "snap-1"}
	for i := 0; i < 150; i++ {
		result.Items = append(result.Items, map[string]interface{}{"name": "device-" + string(rune('a'+i%26)), "idx": i})
	}
	if err := a.bloomManager.BuildFilterFromNQEResult("162112", "devices", result, 50); err != nil {
		t.Fatal(err)
	}
	if !a.bloomManager.IsFilterAvailable("162112", "devices") {
		t.Error("A must see its own filter")
	}
	if b.bloomManager.IsFilterAvailable("162112", "devices") || svc.bloomManager.IsFilterAvailable("162112", "devices") {
		t.Error("other callers must not see A's filter")
	}
	// Same caller again gets the same partition (and therefore the filter).
	if !svc.forCaller(callerA).bloomManager.IsFilterAvailable("162112", "devices") {
		t.Error("partition must be stable for a subject")
	}
}
