package ratelimit

import (
	"sync"
	"testing"
)

func TestAccessKeyConcurrencyRejectsAtLimitAndRecoversOnRelease(t *testing.T) {
	limiter := NewAccessKeyConcurrency()
	first, ok := limiter.Acquire(7, 2)
	if !ok {
		t.Fatal("first acquire rejected")
	}
	second, ok := limiter.Acquire(7, 2)
	if !ok {
		t.Fatal("second acquire rejected")
	}
	if release, ok := limiter.Acquire(7, 2); ok || release != nil {
		t.Fatalf("third acquire = (release=%t, ok=%t), want rejected with nil release", release != nil, ok)
	}
	if got := limiter.InFlight(7); got != 2 {
		t.Fatalf("InFlight() = %d, want 2", got)
	}
	first()
	if release, ok := limiter.Acquire(7, 2); !ok {
		t.Fatal("acquire after release rejected")
	} else {
		release()
	}
	second()
	if got := limiter.InFlight(7); got != 0 {
		t.Fatalf("InFlight() after full release = %d, want 0", got)
	}
}

func TestAccessKeyConcurrencyZeroLimitDoesNotTrack(t *testing.T) {
	limiter := NewAccessKeyConcurrency()
	for range 3 {
		release, ok := limiter.Acquire(7, 0)
		if !ok || release == nil {
			t.Fatal("unlimited acquire rejected")
		}
		defer release()
	}
	if got := limiter.InFlight(7); got != 0 {
		t.Fatalf("InFlight() with zero limit = %d, want 0", got)
	}
}

func TestAccessKeyConcurrencyReleaseIsIdempotent(t *testing.T) {
	limiter := NewAccessKeyConcurrency()
	release, ok := limiter.Acquire(7, 1)
	if !ok {
		t.Fatal("acquire rejected")
	}
	release()
	release()
	if got := limiter.InFlight(7); got != 0 {
		t.Fatalf("InFlight() after double release = %d, want 0", got)
	}
	if _, ok := limiter.Acquire(7, 1); !ok {
		t.Fatal("acquire after double release rejected")
	}
}

func TestAccessKeyConcurrencyIsolatesAccessKeys(t *testing.T) {
	limiter := NewAccessKeyConcurrency()
	if _, ok := limiter.Acquire(7, 1); !ok {
		t.Fatal("key 7 acquire rejected")
	}
	if _, ok := limiter.Acquire(8, 1); !ok {
		t.Fatal("key 8 acquire rejected while key 7 is at its own limit")
	}
}

func TestAccessKeyConcurrencyConcurrentLimit(t *testing.T) {
	limiter := NewAccessKeyConcurrency()
	start := make(chan struct{})
	var group sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for range 64 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			if _, ok := limiter.Acquire(7, 5); ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	close(start)
	group.Wait()
	if allowed != 5 {
		t.Fatalf("allowed = %d, want 5", allowed)
	}
}
