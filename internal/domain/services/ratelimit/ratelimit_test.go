package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func newTestLimiter(perMinute, burst int) (*Limiter, *time.Time) {
	l := New(perMinute, burst)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	return l, &now
}

func TestBurstThenRefill(t *testing.T) {
	l, now := newTestLimiter(60, 3) // 1 token/s, burst 3
	for i := 0; i < 3; i++ {
		if !l.Allow("a") {
			t.Fatalf("call %d within burst should pass", i)
		}
	}
	if l.Allow("a") {
		t.Fatal("4th call should be limited")
	}
	*now = now.Add(2 * time.Second)
	if !l.Allow("a") || !l.Allow("a") {
		t.Fatal("two tokens should have refilled")
	}
	if l.Allow("a") {
		t.Fatal("third should be limited")
	}
	*now = now.Add(time.Hour)
	for i := 0; i < 3; i++ {
		if !l.Allow("a") {
			t.Fatal("bucket should cap at burst, not grow unbounded")
		}
	}
	if l.Allow("a") {
		t.Fatal("cap at burst")
	}
}

func TestKeysAreIndependent(t *testing.T) {
	l, _ := newTestLimiter(60, 1)
	if !l.Allow("a") || l.Allow("a") {
		t.Fatal("a: one then limited")
	}
	if !l.Allow("b") {
		t.Fatal("b must have its own bucket")
	}
}

func TestDisabledAndNil(t *testing.T) {
	var nilL *Limiter
	if !nilL.Allow("x") {
		t.Error("nil limiter allows")
	}
	if !New(0, 0).Allow("x") {
		t.Error("zero rate allows")
	}
}

func TestForget(t *testing.T) {
	l, now := newTestLimiter(60, 1)
	l.Allow("a")
	*now = now.Add(time.Minute)
	l.Allow("b")
	if n := l.Forget(30 * time.Second); n != 1 {
		t.Errorf("expected 1 forgotten, got %d", n)
	}
}

func TestConcurrentAllow(t *testing.T) {
	l := New(6000, 100)
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for i := 0; i < 500; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.Allow("k") {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed < 100 || allowed > 150 {
		t.Errorf("expected roughly the burst to pass, got %d", allowed)
	}
}
