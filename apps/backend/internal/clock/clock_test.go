package clock_test

import (
	"testing"
	"time"

	"calyx/backend/internal/clock"
)

func TestRealNowIsTheSystemTime(t *testing.T) {
	d := -time.Until(clock.Real().Now())
	if d < -time.Second || d > time.Second {
		t.Fatalf("difference %v", d)
	}
}

func TestRealTimerFires(t *testing.T) {
	tm := clock.Real().NewTimer(10 * time.Millisecond)
	select {
	case <-tm.C():
	case <-time.After(time.Second):
		t.Fatal("timer did not fire")
	}
}

func TestRealTimerStop(t *testing.T) {
	tm := clock.Real().NewTimer(time.Hour)
	if !tm.Stop() {
		t.Fatal("Stop() = false, want true")
	}
	select {
	case <-tm.C():
		t.Fatal("stopped timer fired")
	case <-time.After(50 * time.Millisecond):
	}
}
