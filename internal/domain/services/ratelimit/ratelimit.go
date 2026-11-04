// Package ratelimit is a per-key token bucket used to bound how fast one
// caller can drive the remote server (PLAN.md 1c.7, S2). It is pure and
// safe for concurrent use.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter holds one bucket per key.
type Limiter struct {
	rate  float64 // tokens per second
	burst float64
	now   func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

// New builds a limiter allowing perMinute sustained calls per key with a
// burst of burst calls. perMinute <= 0 disables limiting.
func New(perMinute int, burst int) *Limiter {
	if burst <= 0 {
		burst = perMinute
	}
	return &Limiter{
		rate:    float64(perMinute) / 60.0,
		burst:   float64(burst),
		now:     time.Now,
		buckets: map[string]*bucket{},
	}
}

// Allow reports whether one more call for key may proceed now.
func (l *Limiter) Allow(key string) bool {
	if l == nil || l.rate <= 0 {
		return true
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * l.rate
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Forget drops idle buckets older than idle, bounding memory. Call it from
// a maintenance goroutine; it is cheap.
func (l *Limiter) Forget(idle time.Duration) int {
	if l == nil {
		return 0
	}
	cutoff := l.now().Add(-idle)
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for k, b := range l.buckets {
		if b.last.Before(cutoff) {
			delete(l.buckets, k)
			n++
		}
	}
	return n
}
