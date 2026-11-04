package httpserver

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Spike S2 over the wire: five identities with their own bearer tokens hit
// one remote-mode server concurrently. Every call must see its own subject
// (no identity bleed between interleaved sessions), and the per-subject
// rate limiter must throttle each identity independently.
func TestS2FiveIdentitiesOverHTTP(t *testing.T) {
	const n = 5
	var spec []string
	for i := 0; i < n; i++ {
		spec = append(spec, fmt.Sprintf("s2-token-%02d-0123456789=agent-%d", i, i))
	}
	ts, _ := newTestServerWithTokens(t, false, strings.Join(spec, ","))

	const calls = 15 // under the burst of 20: none should be limited
	var wrong, failed atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		session := connect(t, ts, fmt.Sprintf("s2-token-%02d-0123456789", i))
		want := fmt.Sprintf("agent-%d|", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for c := 0; c < calls; c++ {
				res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "whoami", Arguments: map[string]any{}})
				if err != nil || res.IsError {
					failed.Add(1)
					continue
				}
				if got := res.Content[0].(*mcp.TextContent).Text; !strings.HasPrefix(got, want) {
					wrong.Add(1)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	if wrong.Load() != 0 {
		t.Fatalf("%d calls saw another identity", wrong.Load())
	}
	if failed.Load() != 0 {
		t.Fatalf("%d calls failed under the per-subject burst; limiter is not per subject", failed.Load())
	}
	t.Logf("S2/http: %d identities x %d concurrent calls, 0 identity bleed, 0 throttled", n, calls)
}

// One noisy identity exhausting its bucket must not throttle the others.
func TestS2NoisyNeighbourIsIsolated(t *testing.T) {
	ts, _ := newTestServerWithTokens(t, false, "noisy-token-0123456789=noisy,quiet-token-0123456789=quiet")
	noisy := connect(t, ts, "noisy-token-0123456789")
	quiet := connect(t, ts, "quiet-token-0123456789")

	var limited atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := 0; c < 15; c++ {
				res, err := noisy.CallTool(context.Background(), &mcp.CallToolParams{Name: "whoami", Arguments: map[string]any{}})
				if err != nil || res.IsError {
					limited.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if limited.Load() == 0 {
		t.Fatal("noisy identity should have been throttled (60 calls vs burst 20)")
	}
	for c := 0; c < 10; c++ {
		res, err := quiet.CallTool(context.Background(), &mcp.CallToolParams{Name: "whoami", Arguments: map[string]any{}})
		if err != nil || res.IsError {
			t.Fatalf("quiet identity throttled by its neighbour on call %d: %v %+v", c, err, res)
		}
	}
	t.Logf("S2/http: noisy identity throttled %d/60, quiet identity unaffected", limited.Load())
}
