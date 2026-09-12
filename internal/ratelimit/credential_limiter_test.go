package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func TestCredentialLimiterEnforcesRPMAndConcurrencyTogether(t *testing.T) {
	base := time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: base}
	limiter := NewCredentialLimiter()
	limiter.now = clock.current

	first, ok := limiter.Acquire(7, 3, 1)
	if !ok {
		t.Fatal("first acquire rejected")
	}
	if _, ok := limiter.Acquire(7, 3, 1); ok {
		t.Fatal("second acquire allowed while one is in flight with concurrency 1")
	}
	first()
	second, ok := limiter.Acquire(7, 3, 1)
	if !ok {
		t.Fatal("acquire after release rejected")
	}
	second()
	third, ok := limiter.Acquire(7, 3, 1)
	if !ok {
		t.Fatal("third acquire rejected")
	}
	third()
	if _, ok := limiter.Acquire(7, 3, 1); ok {
		t.Fatal("fourth acquire allowed beyond rpm 3 even with no in-flight")
	}
	if got := limiter.RetryAfter(7, 3); !got.Equal(base.Add(time.Minute)) {
		t.Fatalf("RetryAfter = %v, want %v", got, base.Add(time.Minute))
	}
	clock.set(base.Add(time.Minute))
	if _, ok := limiter.Acquire(7, 3, 1); !ok {
		t.Fatal("acquire after window expiry rejected")
	}
}

func TestCredentialLimiterAvailableDoesNotCharge(t *testing.T) {
	limiter := NewCredentialLimiter()
	for range 5 {
		if !limiter.Available(7, 1, 1) {
			t.Fatal("Available charged the window")
		}
	}
	if _, ok := limiter.Acquire(7, 1, 1); !ok {
		t.Fatal("acquire after read-only checks rejected")
	}
	if limiter.Available(7, 1, 1) {
		t.Fatal("Available reports capacity while both limits are exhausted")
	}
}

func TestCredentialLimiterUnlimitedIsNoop(t *testing.T) {
	limiter := NewCredentialLimiter()
	for range 3 {
		release, ok := limiter.Acquire(7, 0, 0)
		if !ok || release == nil {
			t.Fatal("unlimited acquire rejected")
		}
		release()
	}
	if len(limiter.windows) != 0 || len(limiter.inFlight) != 0 {
		t.Fatal("unlimited acquire left state behind")
	}
}

func TestCredentialLimiterConcurrentAcquireNeverExceedsLimit(t *testing.T) {
	limiter := NewCredentialLimiter()
	start := make(chan struct{})
	var group sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for range 64 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			if _, ok := limiter.Acquire(7, 5, 3); ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	close(start)
	group.Wait()
	if allowed != 3 {
		t.Fatalf("allowed = %d, want 3 (min of rpm 5 and concurrency 3)", allowed)
	}
}
