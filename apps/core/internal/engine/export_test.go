package engine

import (
	"context"
	"time"
)

// Hooks for the external tests of package engine_test.

var CheckKeyFormat = checkKeyFormat

// PendingDirect returns how many direct messages wait in the queue of o.
func PendingDirect(o *Outbox) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.direct)
}

// QueueGivenUp puts a message in the queue of o with no Send call waiting for it. With a done
// ctx, this is a caller that stopped waiting before its Send could remove the message.
func QueueGivenUp(ctx context.Context, o *Outbox, recipient, text string, onDone func(SendOutcome)) {
	msg := &directMessage{ctx: ctx, recipient: recipient, text: text, onDone: onDone, result: make(chan sendResult, 1)}
	o.mu.Lock()
	o.direct = append(o.direct, msg)
	o.idle = false
	o.mu.Unlock()
	o.signal()
}

// SetSweepTick makes the sweeper of o run a sweep on each value of tick instead of every
// sweepInterval. Call it before StartSendRecords.
func SetSweepTick(o *Orchestrator, tick <-chan time.Time) { o.sweepTick = tick }

// PlaceOf is the queue place of a send record on the outbox of the live instance name.
func PlaceOf(o *Orchestrator, name string, recordID int64) (int, bool) {
	o.mu.Lock()
	live := o.live[name]
	o.mu.Unlock()
	return live.outbox.Place(recordID)
}

// NormalizeRecipientOf runs the recipient normalizer of o.
func NormalizeRecipientOf(o *Orchestrator, provider, to string) (string, error) {
	return o.opts.NormalizeRecipient(provider, to)
}

var SingleFingerprint = singleFingerprint

// QueuedOf is the number of direct messages in the queue of the live instance name.
func QueuedOf(o *Orchestrator, name string) int {
	o.mu.Lock()
	live := o.live[name]
	o.mu.Unlock()
	return PendingDirect(live.outbox)
}

// AcquireWithin acquires the instance name with the limit of a wake, and releases it at once.
func AcquireWithin(o *Orchestrator, name string, limit time.Duration) error {
	live, err := o.acquireWithin(name, limit)
	if err != nil {
		return err
	}
	o.release(live)
	return nil
}

// IsLive tells whether the instance name is loaded in memory.
func IsLive(o *Orchestrator, name string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.live[name] != nil
}

// SetWakeTimer makes the wake loop of o wait with after in place of time.After. Call it before
// StartWakeRetry.
func SetWakeTimer(o *Orchestrator, after func(time.Duration) <-chan time.Time) { o.wakeAfter = after }

// SetWakeLimit replaces the 20 s limit of a wake. Call it before StartWakeRetry.
func SetWakeLimit(o *Orchestrator, limit time.Duration) { o.wakeLimit = limit }

// Acquire acquires the instance name, as a send does. Call release to end the task.
func Acquire(o *Orchestrator, name string) (release func(), err error) {
	live, err := o.acquire(name)
	if err != nil {
		return nil, err
	}
	return func() { o.release(live) }, nil
}

// SetPairedHook makes CreateInstance call fn after its CONNECTED write and before it registers the
// paired provider. Call it before CreateInstance.
func SetPairedHook(o *Orchestrator, fn func()) { o.pairedHook = fn }

// Register registers provider as the live instance of row, as the end of a load or a pairing does.
func Register(o *Orchestrator, row InstanceRow, provider Provider) error {
	return o.register(row, provider)
}

// SetRemovalHook makes RemoveInstance call fn after its first pairing check and before it
// publishes its close. Call it before RemoveInstance.
func SetRemovalHook(o *Orchestrator, fn func()) { o.removalHook = fn }

// ClosingOf returns the close or the hold that a new load of name waits for, or nil.
func ClosingOf(o *Orchestrator, name string) chan struct{} {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.closing[name]
}

// LiveState returns how many instances are loaded in memory, and how many of them have an armed
// sleep timer. A timer is armed only for a loaded instance (canSleepLocked), and forgetLocked
// stops it, so these are all the armed timers.
func LiveState(o *Orchestrator) (live, timers int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, l := range o.live {
		if l.sleepTimer != nil {
			timers++
		}
	}
	return len(o.live), timers
}
