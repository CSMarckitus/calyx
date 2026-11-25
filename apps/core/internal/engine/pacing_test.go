package engine_test

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/engine"
)

func TestSafeRangeReturnsTheFallbackWhenNothingIsProvided(t *testing.T) {
	got, err := engine.SafeRange(nil, engine.HumanPacing.Delay, true, "delay")
	if err != nil {
		t.Fatal(err)
	}
	if got != (engine.Range{Min: 15000, Max: 45000}) {
		t.Fatalf("got %+v", got)
	}
}

func TestSafeRangeRejectsADelayBelowTheMinimum(t *testing.T) {
	_, err := engine.SafeRange(&engine.Range{Min: 1999, Max: 5000}, engine.HumanPacing.Delay, true, "delay")
	if calyxerr.CodeOf(err) != codes.InvalidArgument {
		t.Fatalf("got %v", err)
	}
}

func TestSafeRangeRejectsEqualMinAndMaxWhenRandomnessIsRequired(t *testing.T) {
	_, err := engine.SafeRange(&engine.Range{Min: 3000, Max: 3000}, engine.HumanPacing.Delay, true, "delay")
	if calyxerr.CodeOf(err) != codes.InvalidArgument {
		t.Fatalf("human pacing: got %v", err)
	}

	official := engine.OfficialAPIPacing
	got, err := engine.SafeRange(&engine.Range{Min: 300, Max: 300}, official.Delay, official.RequireRandomness, "delay")
	if err != nil {
		t.Fatalf("official pacing: %v", err)
	}
	if got != (engine.Range{Min: 300, Max: 300}) {
		t.Fatalf("official pacing: got %+v", got)
	}
}

func TestSafeRangeOrdersMinAndMax(t *testing.T) {
	got, err := engine.SafeRange(&engine.Range{Min: 9000, Max: 3000}, engine.HumanPacing.Delay, true, "delay")
	if err != nil {
		t.Fatal(err)
	}
	if got != (engine.Range{Min: 3000, Max: 9000}) {
		t.Fatalf("got %+v", got)
	}
}

func TestTypingIs100msPerCharacterBetween2And10Seconds(t *testing.T) {
	cases := []struct {
		text string
		want time.Duration
	}{
		{"hi", 2 * time.Second},
		{strings.Repeat("a", 50), 5 * time.Second},
		{strings.Repeat("a", 500), 10 * time.Second},
		{strings.Repeat("é", 30), 3 * time.Second}, // characters, not bytes
	}
	for _, c := range cases {
		if got := engine.TypingDuration(c.text); got != c.want {
			t.Errorf("%d characters: got %v, want %v", len([]rune(c.text)), got, c.want)
		}
	}
}

func TestPacingMinimumsAreTheAgreedOnes(t *testing.T) {
	p := engine.HumanPacing
	if p.Delay.Min != 2000 || p.PauseEvery.Min != 5 || p.PauseDuration.Min != 10000 || !p.RequireRandomness {
		t.Fatalf("the WhatsApp Web minimums changed: %+v", p)
	}
}

func TestQueueWaitIsOneMessageOfTheQueue(t *testing.T) {
	if got := engine.HumanPacing.QueueWait(); got != 12*time.Second {
		t.Errorf("HumanPacing: got %v, want 12s", got)
	}
	if got := engine.OfficialAPIPacing.QueueWait(); got != time.Second {
		t.Errorf("OfficialAPIPacing: got %v, want 1s", got)
	}
}
