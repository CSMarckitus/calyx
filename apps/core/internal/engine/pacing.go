// Package engine holds what the core does with an instance, independent of the provider:
// pacing rules, bulk planning, the bulk job store and the outbound queue.
package engine

import (
	"math/rand/v2"
	"time"
	"unicode/utf8"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
)

// Range is in milliseconds, or in number of messages for PauseEvery.
type Range struct{ Min, Max int }

// Rule is the range used when the client gives none, and the lowest value a client may ask for.
type Rule struct {
	Fallback Range
	Min      int
}

// PacingPolicy says how fast an instance may send. Each provider declares its own and the Outbox
// enforces it for every message, so no client can go below these limits.
type PacingPolicy struct {
	// Show "typing..." to the recipient before each message.
	SimulateTyping bool
	// Minimum time between two messages of the same instance, whatever their origin.
	MinGapMs int
	// Direct (non bulk) messages allowed to wait in the queue before new ones are rejected.
	MaxPendingDirect int
	// Reject ranges where min equals max: a fixed rhythm is the easiest pattern to detect.
	RequireRandomness bool
	Delay             Rule
	// nil means this provider takes no long breaks.
	PauseEvery    *Rule
	PauseDuration *Rule
}

// HumanPacing is the policy of WhatsApp Web. To keep spammers away from this project the
// minimums must never become configurable.
var HumanPacing = PacingPolicy{
	SimulateTyping:    true,
	MinGapMs:          2000,
	MaxPendingDirect:  20,
	RequireRandomness: true,
	Delay:             Rule{Fallback: Range{15000, 45000}, Min: 2000},
	PauseEvery:        &Rule{Fallback: Range{10, 25}, Min: 5},
	PauseDuration:     &Rule{Fallback: Range{60000, 300000}, Min: 10000},
}

// The windows of the repeat guard and of the idempotency key. To keep spammers away from this
// project they must never become configurable.
const (
	// A single send with the same recipient and message text as a send of the last 10 minutes
	// is refused.
	RepeatWindow = 10 * time.Minute
	// An idempotency key is known for 24 hours after its first request.
	KeyWindow = 24 * time.Hour
)

// OfficialAPIPacing is the policy of the Meta Cloud API. Meta applies its own rate limits and
// quality rating, so this only keeps a small gap to stay far from its throughput limit.
var OfficialAPIPacing = PacingPolicy{
	SimulateTyping:    false,
	MinGapMs:          100,
	MaxPendingDirect:  500,
	RequireRandomness: false,
	Delay:             Rule{Fallback: Range{300, 800}, Min: 100},
}

// SafeRange returns the fallback when nothing is provided, and rejects provided values below the
// rule's minimum instead of silently raising them, so the client knows its request was unsafe.
func SafeRange(provided *Range, rule Rule, requireRandomness bool, field string) (Range, error) {
	if provided == nil {
		return rule.Fallback, nil
	}
	if provided.Min < rule.Min || provided.Max < rule.Min {
		return Range{}, calyxerr.New(codes.InvalidArgument,
			"The %s range is too low (minimum allowed: %d). This project doesn't allow any type of spammy behavior.",
			field, rule.Min)
	}
	if requireRandomness && provided.Min == provided.Max {
		return Range{}, calyxerr.New(codes.InvalidArgument,
			"The min and max values of %s cannot be equal: a fixed rhythm is what gets accounts banned.", field)
	}
	return Range{Min: min(provided.Min, provided.Max), Max: max(provided.Min, provided.Max)}, nil
}

// maxTyping is the longest time spent "typing" one message.
const maxTyping = 10 * time.Second

// QueueWait is the time one waiting message takes to leave the queue, rounded up to whole
// seconds, at least 1 s. A client that finds the queue full uses it as the wait before it retries.
func (p PacingPolicy) QueueWait() time.Duration {
	wait := time.Duration(p.MinGapMs) * time.Millisecond
	if p.SimulateTyping {
		wait += maxTyping
	}
	rounded := (wait + time.Second - 1) / time.Second * time.Second
	return max(rounded, time.Second)
}

// TypingDuration is the time spent "typing" a message: 100 ms per character, between 2 and 10 s.
func TypingDuration(text string) time.Duration {
	ms := utf8.RuneCountInString(text) * 100
	return min(max(time.Duration(ms)*time.Millisecond, 2*time.Second), maxTyping)
}

// randomIn returns a value between r.Min and r.Max, both included.
func randomIn(r Range) int {
	return r.Min + rand.IntN(r.Max-r.Min+1)
}
