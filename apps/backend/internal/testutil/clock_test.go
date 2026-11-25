package testutil

import (
	"testing"
	"time"
)

var start = time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)

func recv(t *testing.T, ch <-chan time.Time) time.Time {
	t.Helper()
	select {
	case v := <-ch:
		return v
	default:
		t.Fatal("channel is empty")
		return time.Time{}
	}
}

func empty(t *testing.T, ch <-chan time.Time) {
	t.Helper()
	select {
	case v := <-ch:
		t.Fatalf("channel has %v", v)
	default:
	}
}

func TestFakeNowMovesOnlyWithAdvance(t *testing.T) {
	c := NewFakeClock(start)
	if !c.Now().Equal(start) {
		t.Fatalf("Now() = %v", c.Now())
	}
	c.Advance(90 * time.Second)
	if want := start.Add(90 * time.Second); !c.Now().Equal(want) {
		t.Fatalf("Now() = %v, want %v", c.Now(), want)
	}
}

func TestFakeTimerFiresAtItsDeadline(t *testing.T) {
	c := NewFakeClock(start)
	tm := c.NewTimer(10 * time.Second)
	c.Advance(9 * time.Second)
	empty(t, tm.C())
	c.Advance(time.Second)
	if got, want := recv(t, tm.C()), start.Add(10*time.Second); !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestFakeTimersFireInDeadlineOrder(t *testing.T) {
	c := NewFakeClock(start)
	t30 := c.NewTimer(30 * time.Second)
	t10 := c.NewTimer(10 * time.Second)
	t20 := c.NewTimer(20 * time.Second)
	c.Advance(time.Minute)
	for _, tc := range []struct {
		ch   <-chan time.Time
		want time.Duration
	}{{t10.C(), 10 * time.Second}, {t20.C(), 20 * time.Second}, {t30.C(), 30 * time.Second}} {
		if got := recv(t, tc.ch); !got.Equal(start.Add(tc.want)) {
			t.Errorf("got %v, want start+%v", got, tc.want)
		}
	}
}

func TestFakeTimerStop(t *testing.T) {
	c := NewFakeClock(start)
	tm := c.NewTimer(10 * time.Second)
	if !tm.Stop() {
		t.Fatal("first Stop() = false")
	}
	c.Advance(time.Minute)
	empty(t, tm.C())
	if tm.Stop() {
		t.Fatal("second Stop() = true")
	}
}

func TestFakeTimerWithZeroDurationFiresAtOnce(t *testing.T) {
	c := NewFakeClock(start)
	tm := c.NewTimer(0)
	if got := recv(t, tm.C()); !got.Equal(start) {
		t.Fatalf("got %v", got)
	}
	if n := c.PendingTimers(); n != 0 {
		t.Fatalf("PendingTimers() = %d", n)
	}
}

func TestPendingTimers(t *testing.T) {
	c := NewFakeClock(start)
	a := c.NewTimer(10 * time.Second)
	c.NewTimer(20 * time.Second)
	if n := c.PendingTimers(); n != 2 {
		t.Fatalf("PendingTimers() = %d, want 2", n)
	}
	a.Stop()
	if n := c.PendingTimers(); n != 1 {
		t.Fatalf("PendingTimers() = %d, want 1", n)
	}
	c.Advance(time.Minute)
	if n := c.PendingTimers(); n != 0 {
		t.Fatalf("PendingTimers() = %d, want 0", n)
	}
}
