package engine_test

import (
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/engine"
)

func hundredMessages() []engine.BulkMessageInput {
	messages := make([]engine.BulkMessageInput, 100)
	for i := range messages {
		messages[i] = engine.BulkMessageInput{To: "34600111222", Text: "Reminder"}
	}
	return messages
}

func TestEveryDelayIsInsideTheDefaultRangeAndTheEstimateIsTheirSum(t *testing.T) {
	plan, estimatedMs, err := engine.PlanBulk(hundredMessages(), engine.BulkPacingOptions{}, engine.HumanPacing)
	if err != nil {
		t.Fatal(err)
	}
	sum := 0
	for i, m := range plan {
		if m.DelayMs < 15000 || m.DelayMs > 45000 {
			t.Errorf("message %d: delay %d out of [15000, 45000]", i, m.DelayMs)
		}
		sum += m.DelayMs + m.PauseAfterMs
	}
	if sum != estimatedMs {
		t.Fatalf("estimate %d, sum %d", estimatedMs, sum)
	}
}

func TestBreaksComeEvery10To25MessagesAndNeverAfterTheLast(t *testing.T) {
	plan, _, err := engine.PlanBulk(hundredMessages(), engine.BulkPacingOptions{}, engine.HumanPacing)
	if err != nil {
		t.Fatal(err)
	}
	last := -1
	breaks := 0
	for i, m := range plan {
		if m.PauseAfterMs == 0 {
			continue
		}
		breaks++
		if gap := i - last; gap < 10 || gap > 25 {
			t.Errorf("break after message %d comes %d messages after the previous one", i, gap)
		}
		if m.PauseAfterMs < 60000 || m.PauseAfterMs > 300000 {
			t.Errorf("break of %d ms out of [60000, 300000]", m.PauseAfterMs)
		}
		last = i
	}
	if breaks == 0 {
		t.Fatal("100 messages and no break")
	}
	if plan[len(plan)-1].PauseAfterMs != 0 {
		t.Fatal("a break after the last message")
	}
}

func TestAPerMessageDelayIsUsedAsIs(t *testing.T) {
	delay := 9000
	messages := hundredMessages()
	messages[0].DelayMs = &delay
	plan, _, err := engine.PlanBulk(messages, engine.BulkPacingOptions{}, engine.HumanPacing)
	if err != nil {
		t.Fatal(err)
	}
	if plan[0].DelayMs != 9000 {
		t.Fatalf("got %d", plan[0].DelayMs)
	}
}

func TestAPerMessageDelayBelow2000IsRejected(t *testing.T) {
	delay := 1999
	messages := hundredMessages()
	messages[0].DelayMs = &delay
	_, _, err := engine.PlanBulk(messages, engine.BulkPacingOptions{}, engine.HumanPacing)
	if calyxerr.CodeOf(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "Message 0") {
		t.Fatalf("got %v", err)
	}
}

func TestTheOfficialAPIPolicyPlansNoBreaks(t *testing.T) {
	plan, _, err := engine.PlanBulk(hundredMessages(), engine.BulkPacingOptions{}, engine.OfficialAPIPacing)
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range plan {
		if m.PauseAfterMs != 0 {
			t.Fatalf("break after message %d", i)
		}
	}
}

func TestAnEmptyBulkIsRejected(t *testing.T) {
	_, _, err := engine.PlanBulk(nil, engine.BulkPacingOptions{}, engine.HumanPacing)
	if calyxerr.CodeOf(err) != codes.InvalidArgument {
		t.Fatalf("got %v", err)
	}
}

func TestAWhitespaceTextIsRejected(t *testing.T) {
	messages := hundredMessages()
	messages[3].Text = "  \n"
	_, _, err := engine.PlanBulk(messages, engine.BulkPacingOptions{}, engine.HumanPacing)
	if calyxerr.CodeOf(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "Message 3") {
		t.Fatalf("got %v", err)
	}
}
