package auth

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"calyx/backend/internal/clock"
)

// fakeClock is a minimal clock for the limiter. The limiter never calls NewTimer.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}
func (c *fakeClock) NewTimer(time.Duration) clock.Timer { panic("the limiter does not use timers") }
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newTestLimiter() (*Limiter, *fakeClock) {
	c := &fakeClock{now: time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)}
	return NewLimiter(c), c
}

func TestTenPerIP(t *testing.T) {
	l, _ := newTestLimiter()
	for i := 0; i < 10; i++ {
		if !l.Allow("login", "1.2.3.4", fmt.Sprintf("u%d@x.example", i)) {
			t.Fatalf("attempt %d refused", i+1)
		}
	}
	if l.Allow("login", "1.2.3.4", "u10@x.example") {
		t.Fatal("the 11th attempt was allowed")
	}
}

func TestFivePerEmail(t *testing.T) {
	l, _ := newTestLimiter()
	for i := 0; i < 5; i++ {
		if !l.Allow("login", fmt.Sprintf("10.0.0.%d", i), "ana@x.example") {
			t.Fatalf("attempt %d refused", i+1)
		}
	}
	if l.Allow("login", "10.0.0.99", "ana@x.example") {
		t.Fatal("the 6th attempt was allowed")
	}
}

func TestTheWindowIsFixedAtOneMinute(t *testing.T) {
	l, c := newTestLimiter()
	for i := 0; i < 10; i++ {
		l.Allow("login", "1.2.3.4", "")
	}
	c.Advance(59 * time.Second)
	if l.Allow("login", "1.2.3.4", "") {
		t.Fatal("allowed inside the window")
	}
	c.Advance(time.Second)
	if !l.Allow("login", "1.2.3.4", "") {
		t.Fatal("refused after the window")
	}
}

func TestKindsDoNotShareBuckets(t *testing.T) {
	l, _ := newTestLimiter()
	for i := 0; i < 10; i++ {
		l.Allow("login", "1.2.3.4", "")
	}
	if !l.Allow("reset", "1.2.3.4", "x@x.example") {
		t.Fatal("kinds share a bucket")
	}
}

func TestEmailCaseIsIgnored(t *testing.T) {
	l, _ := newTestLimiter()
	for i := 0; i < 5; i++ {
		l.Allow("login", "", "Ana@X.example")
	}
	if l.Allow("login", "", "ana@x.example") {
		t.Fatal("the case of the email changed the bucket")
	}
}

func TestEmptyKeysAreSkipped(t *testing.T) {
	l, _ := newTestLimiter()
	for i := 0; i < 20; i++ {
		got := l.Allow("accept", "1.2.3.4", "")
		if got != (i < 10) {
			t.Fatalf("attempt %d: %v", i+1, got)
		}
	}
	for i := 0; i < 5; i++ {
		if !l.Allow("accept", "", "ana@x.example") {
			t.Fatalf("email attempt %d refused", i+1)
		}
	}
	for i := 0; i < 50; i++ {
		if !l.Allow("accept", "", "") {
			t.Fatal("empty keys were refused")
		}
	}
}

func TestARefusedAttemptCountsNothing(t *testing.T) {
	l, _ := newTestLimiter()
	for i := 0; i < 10; i++ {
		l.Allow("login", "A", "")
	}
	if l.Allow("login", "A", "bob@x.example") {
		t.Fatal("allowed")
	}
	for i := 0; i < 5; i++ {
		if !l.Allow("login", "B", "bob@x.example") {
			t.Fatalf("the refused attempt was counted (attempt %d)", i+1)
		}
	}
}

func TestConcurrentAttempts(t *testing.T) {
	l, _ := newTestLimiter()
	var ok atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.Allow("login", "1.2.3.4", "") {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 10 {
		t.Fatalf("allowed %d, want 10", ok.Load())
	}
}

func TestOldBucketsAreDeleted(t *testing.T) {
	l, c := newTestLimiter()
	for i := 0; i < 10001; i++ {
		l.Allow("login", fmt.Sprintf("ip-%d", i), "")
	}
	c.Advance(time.Minute)
	l.Allow("login", "fresh", "")
	if len(l.buckets) != 1 {
		t.Fatalf("buckets = %d, want 1", len(l.buckets))
	}
}

func TestTheSweepRunsOncePerWindow(t *testing.T) {
	l, c := newTestLimiter()
	for i := 0; i < 10001; i++ {
		l.Allow("login", fmt.Sprintf("ip-%d", i), "")
	}
	c.Advance(time.Minute)
	l.Allow("login", "a", "") // sweeps
	swept := l.lastSweep
	for i := 0; i < 10001; i++ {
		l.Allow("login", fmt.Sprintf("new-%d", i), "")
	}
	c.Advance(time.Second)
	l.Allow("login", "b", "") // more than 10000 buckets, but the window has not passed
	if !l.lastSweep.Equal(swept) {
		t.Fatal("the sweep ran again inside one window")
	}
}
