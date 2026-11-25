package engine

import (
	"strings"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
)

type BulkMessageInput struct {
	To, Text string
	// Fixed wait before this message instead of a random one. nil means random.
	DelayMs *int
}

// BulkPacingOptions are the client's overrides. nil fields use the policy's fallback.
type BulkPacingOptions struct {
	Delay, PauseEvery, PauseDuration *Range
}

type PlannedMessage struct {
	Recipient, Text       string
	DelayMs, PauseAfterMs int
}

// PlanBulk decides, before anything is sent, how long to wait before each message and where the
// long human-like breaks go. Drawing every random value up front is what gives a real time
// estimate and lets a job continue after a restart.
func PlanBulk(messages []BulkMessageInput, opts BulkPacingOptions, policy PacingPolicy) ([]PlannedMessage, int, error) {
	if len(messages) == 0 {
		return nil, 0, calyxerr.New(codes.InvalidArgument, "A bulk job needs at least one message.")
	}

	delayRange, err := SafeRange(opts.Delay, policy.Delay, policy.RequireRandomness, "delay")
	if err != nil {
		return nil, 0, err
	}
	usesPauses := policy.PauseEvery != nil && policy.PauseDuration != nil
	var pauseEveryRange, pauseDurationRange Range
	if usesPauses {
		if pauseEveryRange, err = SafeRange(opts.PauseEvery, *policy.PauseEvery, policy.RequireRandomness, "pause_every"); err != nil {
			return nil, 0, err
		}
		if pauseDurationRange, err = SafeRange(opts.PauseDuration, *policy.PauseDuration, policy.RequireRandomness, "pause_duration"); err != nil {
			return nil, 0, err
		}
	}

	plan := make([]PlannedMessage, 0, len(messages))
	estimatedMs := 0
	messagesSincePause := 0
	nextPauseAt := 0
	if usesPauses {
		nextPauseAt = randomIn(pauseEveryRange)
	}

	for i, message := range messages {
		if strings.TrimSpace(message.Text) == "" {
			return nil, 0, calyxerr.New(codes.InvalidArgument, "Message %d: text is empty.", i)
		}
		var delayMs int
		if message.DelayMs != nil {
			if *message.DelayMs < policy.Delay.Min {
				return nil, 0, calyxerr.New(codes.InvalidArgument,
					"Message %d: delay_ms is too low (minimum allowed: %dms).", i, policy.Delay.Min)
			}
			delayMs = *message.DelayMs
		} else {
			delayMs = randomIn(delayRange)
		}

		// A break after this message, unless it is the last one.
		pauseAfterMs := 0
		messagesSincePause++
		isLast := i == len(messages)-1
		if usesPauses && messagesSincePause >= nextPauseAt && !isLast {
			pauseAfterMs = randomIn(pauseDurationRange)
			messagesSincePause = 0
			nextPauseAt = randomIn(pauseEveryRange)
		}

		plan = append(plan, PlannedMessage{Recipient: message.To, Text: message.Text, DelayMs: delayMs, PauseAfterMs: pauseAfterMs})
		estimatedMs += delayMs + pauseAfterMs
	}
	return plan, estimatedMs, nil
}
