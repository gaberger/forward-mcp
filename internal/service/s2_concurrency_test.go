package service

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/forward"
)

// Spike S2 (docs/spikes in netorch): five agent identities driving one
// forward-mcp service concurrently. Proves under -race that partitioning
// holds when callers interleave, and reports per-identity cache hit rates.
//
// Each identity: puts results for its own queries, re-reads them (expected
// hits), probes every other identity's exact keys (must miss) and the
// semantic path (must not match another identity's entry), and writes to
// and searches the memory graph. Run with -v to see the hit-rate table.

const s2Identities = 5
const s2Queries = 20
const s2Rounds = 5

type s2Stats struct {
	hits, misses, semanticHits, leaks, memLeaks, memErrors atomic.Int64
}

func s2Callers() []entities.Caller {
	out := make([]entities.Caller, s2Identities)
	for i := range out {
		out[i] = entities.Caller{Subject: fmt.Sprintf("agent-%d", i), Issuer: "tokend"}
	}
	return out
}

func s2Query(subject string, q int) string {
	return fmt.Sprintf("query_id:FQ_%s_%d|params:map[]", subject, q)
}

func TestS2FiveIdentitiesConcurrentIsolation(t *testing.T) {
	svc := createTestService()
	callers := s2Callers()
	stats := make([]*s2Stats, len(callers))
	for i := range stats {
		stats[i] = &s2Stats{}
	}
	const network = "162112"

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, c := range callers {
		wg.Add(1)
		go func(i int, c entities.Caller) {
			defer wg.Done()
			<-start
			view := svc.forCaller(c)
			st := stats[i]
			for q := 0; q < s2Queries; q++ {
				res := &forward.NQERunResult{SnapshotID: "snap-1", Items: []map[string]interface{}{{"owner": c.Subject, "q": q}}}
				if err := view.cache().Put(s2Query(c.Subject, q), network, "snap-1", res); err != nil {
					t.Errorf("%s put: %v", c.Subject, err)
					return
				}
			}
			for r := 0; r < s2Rounds; r++ {
				for q := 0; q < s2Queries; q++ {
					got, hit := view.cache().Get(s2Query(c.Subject, q), network, "snap-1")
					if !hit {
						st.misses.Add(1)
						continue
					}
					st.hits.Add(1)
					if len(got.Items) == 0 || got.Items[0]["owner"] != c.Subject {
						st.leaks.Add(1)
					}
				}
				// Own entries via the semantic path (no snapshot pin, so the
				// exact key misses and findBestMatch runs against the shared
				// index while other identities write to it).
				for q := 0; q < s2Queries; q += 4 {
					if got, hit := view.cache().Get(s2Query(c.Subject, q), network, ""); hit {
						st.semanticHits.Add(1)
						if len(got.Items) == 0 || got.Items[0]["owner"] != c.Subject {
							st.leaks.Add(1)
						}
					}
				}
				// Probe every other identity's keys, exact and semantic.
				for j, other := range callers {
					if j == i {
						continue
					}
					for q := 0; q < s2Queries; q += 5 {
						if got, hit := view.cache().Get(s2Query(other.Subject, q), network, "snap-1"); hit {
							if len(got.Items) > 0 && got.Items[0]["owner"] != c.Subject {
								st.leaks.Add(1)
							}
						}
						if got, hit := view.cache().Get(s2Query(other.Subject, q), network, ""); hit {
							if len(got.Items) > 0 && got.Items[0]["owner"] != c.Subject {
								st.leaks.Add(1)
							}
						}
					}
				}
			}
			if view.memorySystem != nil {
				name := "device-of-" + c.Subject
				if _, err := view.memorySystem.CreateEntity(name, "device", map[string]interface{}{"network_id": network}); err != nil {
					st.memErrors.Add(1)
				}
				found, err := view.memorySystem.SearchEntities("device-of-", "", 50)
				if err != nil {
					st.memErrors.Add(1)
				}
				for _, e := range found {
					if e.Name != name {
						st.memLeaks.Add(1)
					}
				}
			}
		}(i, c)
	}
	// An operator polling cache stats while agents work.
	stop := make(chan struct{})
	var statsWG sync.WaitGroup
	statsWG.Add(1)
	go func() {
		defer statsWG.Done()
		<-start
		for {
			select {
			case <-stop:
				return
			default:
				_ = svc.semanticCache.GetStats()
			}
		}
	}()
	close(start)
	wg.Wait()
	close(stop)
	statsWG.Wait()

	var totalLeaks, totalMemLeaks, totalMemErrors int64
	t.Logf("S2: %d identities x %d queries x %d rounds", s2Identities, s2Queries, s2Rounds)
	t.Logf("%-10s %6s %6s %8s %8s %6s %8s %7s", "identity", "hits", "misses", "hit-rate", "semantic", "leaks", "memLeaks", "memErr")
	for i, c := range callers {
		st := stats[i]
		h, m := st.hits.Load(), st.misses.Load()
		rate := 0.0
		if h+m > 0 {
			rate = float64(h) / float64(h+m)
		}
		t.Logf("%-10s %6d %6d %8.2f %8d %6d %8d %7d", c.Subject, h, m, rate, st.semanticHits.Load(), st.leaks.Load(), st.memLeaks.Load(), st.memErrors.Load())
		totalLeaks += st.leaks.Load()
		totalMemLeaks += st.memLeaks.Load()
		totalMemErrors += st.memErrors.Load()
		if rate < 0.99 {
			t.Errorf("%s: own-entry hit rate %.2f, want >= 0.99 (eviction or partition bug)", c.Subject, rate)
		}
	}
	if totalLeaks != 0 {
		t.Fatalf("cache leaked across identities: %d reads returned another identity's rows", totalLeaks)
	}
	if totalMemLeaks != 0 {
		t.Fatalf("memory graph leaked across identities: %d entities", totalMemLeaks)
	}
	if totalMemErrors != 0 {
		// Not an isolation failure, but a concurrency finding worth surfacing
		// (e.g. SQLite busy under parallel writers).
		t.Errorf("memory graph returned %d errors under concurrency", totalMemErrors)
	}
}
