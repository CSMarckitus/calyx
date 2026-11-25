// Package testutil holds the helpers of the tests of the backend.
package testutil

import (
	"sort"
	"sync"
	"time"

	"calyx/backend/internal/clock"
)

// FakeClock is a clock that moves only when a test calls Advance.
type FakeClock struct {
	mu      sync.Mutex
	now     time.Time
	seq     int
	pending []*fakeTimer
}

type fakeTimer struct {
	c        *FakeClock
	ch       chan time.Time
	deadline time.Time
	seq      int
	active   bool
}

func NewFakeClock(start time.Time) *FakeClock { return &FakeClock{now: start} }

func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// NewTimer returns a timer. A duration of 0 or less fires at once.
func (c *FakeClock) NewTimer(d time.Duration) clock.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{c: c, ch: make(chan time.Time, 1), deadline: c.now.Add(d), seq: c.seq}
	c.seq++
	if d <= 0 {
		t.ch <- c.now
		return t
	}
	t.active = true
	c.pending = append(c.pending, t)
	return t
}

// Advance moves the time forward and fires the due timers in deadline order.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	target := c.now.Add(d)
	var due []*fakeTimer
	var rest []*fakeTimer
	for _, t := range c.pending {
		if t.active && !t.deadline.After(target) {
			t.active = false
			due = append(due, t)
		} else if t.active {
			rest = append(rest, t)
		}
	}
	c.pending = rest
	sort.SliceStable(due, func(i, j int) bool {
		if !due[i].deadline.Equal(due[j].deadline) {
			return due[i].deadline.Before(due[j].deadline)
		}
		return due[i].seq < due[j].seq
	})
	// Each timer sees Now() at its own deadline while it fires. The channels have a buffer of 1
	// and only one value is ever sent, so the sends never block.
	for _, t := range due {
		c.now = t.deadline
		t.ch <- t.deadline
	}
	c.now = target
	c.mu.Unlock()
}

// PendingTimers returns the number of timers created and neither fired nor stopped.
func (c *FakeClock) PendingTimers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, t := range c.pending {
		if t.active {
			n++
		}
	}
	return n
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }

func (t *fakeTimer) Stop() bool {
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	was := t.active
	t.active = false
	return was
}
