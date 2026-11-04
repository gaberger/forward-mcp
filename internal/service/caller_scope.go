package service

import (
	"path/filepath"
	"sync"

	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/forward"
)

// Per-identity isolation (PLAN.md M1c.3, spec non-negotiable #5).
//
// Every store that is keyed by Forward instance id — the memory graph, the
// bloom filters and indexes, the API tracker, the semantic cache — is also
// keyed by caller: the caller's partition id (entities.PartitionID) stands
// in for the instance id. Tool handlers never see this; the registrar hands
// each call a *ForwardMCPService view built by forCaller, whose store
// fields point at the caller's partition. The local caller (stdio) gets the
// original service, so 3.0.0 behaviour and on-disk data are untouched.

// partitions lazily creates per-caller bloom managers, which own engines
// and must not be shared across callers. It is pointer-shared by all views.
type partitions struct {
	mu          sync.Mutex
	bloom       map[string]*BloomSearchManager
	bloomIndex  map[string]*BloomIndexManager
	bloomBase   string
	bloomIdxDir string
}

func newPartitions(bloomIndexBase string) *partitions {
	return &partitions{
		bloom:       map[string]*BloomSearchManager{},
		bloomIndex:  map[string]*BloomIndexManager{},
		bloomIdxDir: bloomIndexBase,
	}
}

// forCaller returns the service view for c. The returned value shares every
// shared component (client, config, logger, caches' backing stores) and
// differs only in the partition-scoped fields and in subject.
func (s *ForwardMCPService) forCaller(c entities.Caller) *ForwardMCPService {
	if c.Subject == "" || c.Subject == entities.LocalSubject {
		return s
	}
	pid := entities.PartitionID(s.instanceID, c.Subject)

	view := *s // all fields are pointers or immutable values; no locks copied
	view.subject = c.Subject
	view.caller = c
	if s.memorySystem != nil {
		view.memorySystem = s.memorySystem.ForInstance(pid)
		view.apiTracker = NewAPIMemoryTracker(view.memorySystem, s.logger, pid)
	}
	parts := s.partitions
	if parts == nil {
		// No shared registry (hand-built service in tests): still never
		// share bloom engines across callers.
		parts = newPartitions(filepath.Join("data", "bloom_indexes"))
	}
	view.bloomManager, view.bloomIndexManager = parts.managersFor(s, pid)
	return &view
}

func (p *partitions) managersFor(s *ForwardMCPService, pid string) (*BloomSearchManager, *BloomIndexManager) {
	p.mu.Lock()
	defer p.mu.Unlock()
	bm, ok := p.bloom[pid]
	if !ok && s.bloomManager != nil {
		bm = NewBloomSearchManager(s.logger, pid)
		p.bloom[pid] = bm
	}
	bim, ok := p.bloomIndex[pid]
	if !ok && s.bloomIndexManager != nil {
		bim = NewBloomIndexManager(s.logger, filepath.Join(p.bloomIdxDir, pid))
		p.bloomIndex[pid] = bim
	}
	return bm, bim
}

// scopedCache is the semantic cache as seen by one caller. Handlers use
// s.cache() instead of s.semanticCache so that keys, exact hits and
// similarity scans are all confined to the caller's partition.
type scopedCache struct {
	sc      *SemanticCache
	subject string
}

func (s *ForwardMCPService) cache() scopedCache {
	return scopedCache{sc: s.semanticCache, subject: s.subject}
}

func (c scopedCache) Get(query, networkID, snapshotID string) (*forward.NQERunResult, bool) {
	return c.sc.GetFor(c.subject, query, networkID, snapshotID)
}

func (c scopedCache) Put(query, networkID, snapshotID string, result *forward.NQERunResult) error {
	return c.sc.PutFor(c.subject, query, networkID, snapshotID, result)
}

func (c scopedCache) FindSimilarQueries(query string, limit int) ([]*CacheEntry, error) {
	return c.sc.FindSimilarQueriesFor(c.subject, query, limit)
}

func (c scopedCache) GetStats() map[string]interface{} { return c.sc.GetStats() }

func (c scopedCache) ClearExpired() int { return c.sc.ClearExpired() }
