package engine

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

const (
	// How often a bulk job checks whether a dropped connection is back.
	defaultReconnectCheck = 5 * time.Second
	// Wait after an unexpected error (for example the database) so the loop does not spin.
	errorBackoff = 5 * time.Second
)

type OutboxOptions struct {
	ReconnectCheck time.Duration      // default 5s
	OnIdle         func()             // called each time nothing is left to send
	OnJobFinished  func(job *BulkJob) // called once per job this outbox finished (not for cancels)
	// When the previous outbox of this instance sent its last message. A rebuilt instance gets a
	// new outbox; without this, its first message could leave right after the last one of the
	// old outbox, below the minimum gap.
	LastSentAt time.Time
}

// SendOutcomeKind tells whether a direct message reached the provider.
type SendOutcomeKind int

const (
	OutcomeSent SendOutcomeKind = iota + 1
	OutcomeNotSent
	// The provider returned an error that does not prove the message stayed on this side.
	OutcomeUnknown
)

// SendOutcome is what really happened to one direct message, whatever its caller was told.
type SendOutcome struct {
	Kind      SendOutcomeKind
	MessageID string // the provider message id, only for OutcomeSent
}

type sendResult struct {
	messageID string
	err       error
}

// directMessage belongs to the code that removed it from the queue: that code calls report or finish.
type directMessage struct {
	ctx             context.Context
	recipient, text string
	recordID        int64             // the send record, 0 when the send has no idempotency key
	onDone          func(SendOutcome) // may be nil
	result          chan sendResult   // buffer 1: whoever answers never blocks
}

// report runs onDone. Never call it with o.mu held: onDone may write to the database.
func (m *directMessage) report(outcome SendOutcome) {
	if m.onDone != nil {
		m.onDone(outcome)
	}
}

// finish reports the outcome first, so a caller that retries at once finds it recorded.
func (m *directMessage) finish(outcome SendOutcome, r sendResult) {
	m.report(outcome)
	m.result <- r
}

// providerOutcome maps the answer of provider.SendText. Only UNAVAILABLE means "not sent".
func providerOutcome(messageID string, err error) SendOutcome {
	switch {
	case err == nil:
		return SendOutcome{Kind: OutcomeSent, MessageID: messageID}
	case calyxerr.CodeOf(err) == codes.Unavailable:
		return SendOutcome{Kind: OutcomeNotSent}
	default:
		return SendOutcome{Kind: OutcomeUnknown}
	}
}

var notSent = SendOutcome{Kind: OutcomeNotSent}

// activeJob is the bulk job the loop is working on.
type activeJob struct {
	id string
	// Next message to send, loaded from the database one at a time.
	next *BulkMessage
	// Moment from which next can start being typed.
	readyAt time.Time
	// The instance went offline for a long break and must come back before the next message.
	onBreak bool
}

// Outbox is the only way out for the messages of an instance. Direct messages and bulk jobs share
// one goroutine, one message at a time, so the pacing policy of the provider holds no matter how
// many requests arrive in parallel.
//
// Direct messages go first: someone is waiting for them. Bulk jobs fill the gaps.
type Outbox struct {
	provider   Provider
	store      *BulkStore
	instanceID string
	opts       OutboxOptions
	log        *slog.Logger

	// wake has a buffer of 1 and is written without blocking: a wake that arrives while the loop
	// is busy stays in the buffer, so the loop sees it the next time it waits.
	wake chan struct{}
	stop chan struct{} // closed by Stop: every wait returns at once
	done chan struct{} // closed when the loop goroutine returns
	// stopCtx is cancelled by Stop. Used for typing and presence, which may take seconds.
	stopCtx    context.Context
	cancelStop context.CancelFunc
	stopOnce   sync.Once

	mu          sync.Mutex // guards the fields below, shared with the callers
	direct      []*directMessage
	mayHaveJobs bool            // false once the database had no jobs, until NotifyNewJob
	cancelled   map[string]bool // job ids cancelled through NotifyJobCancelled
	idle        bool
	started     bool
	stopped     bool

	// Only touched by the loop goroutine.
	job        *activeJob
	lastSentAt time.Time
}

func NewOutbox(p Provider, store *BulkStore, instanceID string, opts OutboxOptions) *Outbox {
	if opts.ReconnectCheck == 0 {
		opts.ReconnectCheck = defaultReconnectCheck
	}
	stopCtx, cancelStop := context.WithCancel(context.Background())
	return &Outbox{
		provider:    p,
		store:       store,
		instanceID:  instanceID,
		opts:        opts,
		log:         slog.With("instance", p.InstanceName()),
		wake:        make(chan struct{}, 1),
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
		stopCtx:     stopCtx,
		cancelStop:  cancelStop,
		mayHaveJobs: true,
		cancelled:   map[string]bool{},
		lastSentAt:  opts.LastSentAt,
	}
}

// LastSentAt is when this outbox last sent a message. It is only known once the outbox is
// stopped (the loop goroutine owns it until then); before that it returns the zero time.
func (o *Outbox) LastSentAt() time.Time {
	select {
	case <-o.done:
		return o.lastSentAt
	default:
		return time.Time{}
	}
}

func (o *Outbox) Start() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.started || o.stopped {
		return
	}
	o.started = true
	go o.run()
}

// Send queues a direct message and returns once the provider accepted it. If ctx ends while the
// message is still waiting, it is never sent; once the loop took it, it is sent anyway.
//
// recordID is the id of the send record, or 0 when the send has no idempotency key. Place uses it.
//
// onDone (may be nil) runs exactly once with the real outcome, before Send returns. The one
// exception: when ctx ends after the loop took the message, Send returns ctx.Err() at once and
// the loop runs onDone later.
func (o *Outbox) Send(ctx context.Context, recipient, text string, recordID int64, onDone func(SendOutcome)) (string, error) {
	msg := &directMessage{ctx: ctx, recipient: recipient, text: text, recordID: recordID, onDone: onDone, result: make(chan sendResult, 1)}
	o.mu.Lock()
	if o.stopped {
		o.mu.Unlock()
		msg.report(notSent)
		return "", calyxerr.New(codes.Unavailable, "The instance is shutting down.")
	}
	if waiting := len(o.direct); waiting >= o.provider.Pacing().MaxPendingDirect {
		o.mu.Unlock()
		msg.report(notSent)
		return "", calyxerr.WithReason(codes.ResourceExhausted, corev1.ErrorReason_ERROR_REASON_INSTANCE_QUEUE_FULL, nil,
			"There are already %d messages waiting on this instance. To send to many recipients use a bulk job.", waiting).
			WithRetryAfter(o.provider.Pacing().QueueWait())
	}
	o.direct = append(o.direct, msg)
	o.idle = false
	o.mu.Unlock()
	o.signal()

	select {
	case r := <-msg.result:
		return r.messageID, r.err
	case <-ctx.Done():
		removed := false
		o.mu.Lock()
		for i, waiting := range o.direct {
			if waiting == msg {
				o.direct = append(o.direct[:i], o.direct[i+1:]...)
				removed = true
				break
			}
		}
		o.mu.Unlock()
		// When not removed, the loop or Stop took the message and reports its outcome.
		if removed {
			msg.report(notSent)
		}
		return "", ctx.Err()
	}
}

// Place is the position of the waiting message with this send record id, counted from 1. It
// returns false when the message is not in the queue (never queued, or already taken).
func (o *Outbox) Place(recordID int64) (int, bool) {
	if recordID == 0 {
		return 0, false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for i, msg := range o.direct {
		if msg.recordID == recordID {
			return i + 1, true
		}
	}
	return 0, false
}

// NotifyNewJob tells the loop a bulk job was stored for this instance.
func (o *Outbox) NotifyNewJob() {
	o.mu.Lock()
	o.mayHaveJobs = true
	o.idle = false
	o.mu.Unlock()
	o.signal()
}

// NotifyJobCancelled tells the loop the job was cancelled in the database: stop working on it.
func (o *Outbox) NotifyJobCancelled(jobID string) {
	o.mu.Lock()
	o.cancelled[jobID] = true
	o.mu.Unlock()
	o.signal()
}

// Stop ends the loop and blocks until it returned. Waiting direct messages are rejected; bulk
// messages stay in the database and continue the next time the instance is loaded.
func (o *Outbox) Stop() {
	o.stopOnce.Do(func() {
		o.mu.Lock()
		o.stopped = true
		waiting := o.direct
		o.direct = nil
		started := o.started
		o.mu.Unlock()

		for _, msg := range waiting {
			msg.finish(notSent, sendResult{err: stoppedError()})
		}
		// Close o.stop before the cancel: when the cancel ends the typing, the loop must see the stop.
		close(o.stop)
		o.cancelStop()
		if !started {
			close(o.done)
		}
	})
	<-o.done
}

func (o *Outbox) IsIdle() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.idle
}

func stoppedError() error {
	return calyxerr.New(codes.Unavailable, "The instance was stopped before the message could be sent.")
}

func (o *Outbox) signal() {
	select {
	case o.wake <- struct{}{}:
	default: // a wake is already pending
	}
}

// wait returns after d, on a wake, or on Stop, whichever comes first.
func (o *Outbox) wait(d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-o.wake:
	case <-timer.C:
	case <-o.stop:
	}
}

func (o *Outbox) isStopped() bool {
	select {
	case <-o.stop:
		return true
	default:
		return false
	}
}

func (o *Outbox) run() {
	defer close(o.done)
	// Background, not stopCtx: a database write that started must finish even during Stop.
	ctx := context.Background()

	if err := o.store.RecoverInterrupted(ctx, o.instanceID); err != nil {
		o.log.Error("Could not check for interrupted bulk messages.", "err", err)
	}

	for !o.isStopped() {
		if err := o.step(ctx); err != nil {
			o.log.Error("Unexpected error in the outbox. Retrying soon.", "err", err)
			o.wait(errorBackoff)
		}
	}
}

// step does one small piece of work and returns, so a direct message never waits long.
func (o *Outbox) step(ctx context.Context) error {
	o.dropCancelledJob()

	if o.hasDirect() {
		o.deliverDirect(ctx)
		return nil
	}

	if o.job == nil && o.takeMayHaveJobs() {
		job, err := o.loadNextJob(ctx)
		if err != nil {
			o.setMayHaveJobs()
			return err
		}
		o.job = job
	}
	if o.job == nil {
		o.goIdle()
		return nil
	}
	return o.advanceJob(ctx, o.job)
}

func (o *Outbox) hasDirect() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.direct) > 0
}

// takeDirect removes the first waiting message whose caller is still there. It also removes and
// answers the messages before it whose caller stopped waiting.
func (o *Outbox) takeDirect() *directMessage {
	var gaveUp []*directMessage
	var taken *directMessage
	o.mu.Lock()
	for len(o.direct) > 0 {
		msg := o.direct[0]
		o.direct = o.direct[1:]
		if msg.ctx.Err() != nil {
			gaveUp = append(gaveUp, msg)
			continue
		}
		taken = msg
		break
	}
	o.mu.Unlock()

	for _, msg := range gaveUp {
		msg.finish(notSent, sendResult{err: msg.ctx.Err()})
	}
	return taken
}

func (o *Outbox) deliverDirect(ctx context.Context) {
	if o.provider.Status() != corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED {
		if msg := o.takeDirect(); msg != nil {
			msg.finish(notSent, sendResult{err: calyxerr.New(codes.Unavailable,
				"Instance %s is not connected right now.", o.provider.InstanceName())})
		}
		return
	}
	// The message stays in the queue during the gap, so a caller that gives up meanwhile is
	// removed and never sent.
	if wait := time.Until(o.lastSentAt.Add(o.minGap())); wait > 0 {
		o.wait(wait)
		return
	}

	msg := o.takeDirect()
	if msg == nil {
		return
	}
	o.typeLikeAHuman(msg.recipient, msg.text)
	if o.isStopped() {
		msg.finish(notSent, sendResult{err: stoppedError()})
		return
	}
	id, err := o.provider.SendText(ctx, msg.recipient, msg.text)
	if err == nil {
		o.lastSentAt = time.Now()
	}
	msg.finish(providerOutcome(id, err), sendResult{messageID: id, err: err})
}

func (o *Outbox) takeMayHaveJobs() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	// Cleared before the query, not after: a NotifyNewJob during the query sets it again.
	may := o.mayHaveJobs
	o.mayHaveJobs = false
	return may
}

func (o *Outbox) setMayHaveJobs() {
	o.mu.Lock()
	o.mayHaveJobs = true
	o.mu.Unlock()
}

func (o *Outbox) dropCancelledJob() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.job != nil && o.cancelled[o.job.id] {
		delete(o.cancelled, o.job.id)
		o.job = nil
		o.mayHaveJobs = true // other jobs may be queued behind it
	}
}

func (o *Outbox) wasCancelled(jobID string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.cancelled[jobID]
}

func (o *Outbox) goIdle() {
	o.mu.Lock()
	if len(o.direct) > 0 || o.mayHaveJobs {
		o.mu.Unlock()
		return // something arrived meanwhile
	}
	o.idle = true
	clear(o.cancelled) // no job is active, so these can no longer matter
	o.mu.Unlock()

	if o.opts.OnIdle != nil {
		o.opts.OnIdle()
	}
	select {
	case <-o.wake:
	case <-o.stop:
	}
}

func (o *Outbox) loadNextJob(ctx context.Context) (*activeJob, error) {
	job, err := o.store.NextUnfinished(ctx, o.instanceID)
	if err != nil || job == nil {
		return nil, err
	}
	o.setMayHaveJobs() // more jobs may be queued behind this one
	if job.Status == JobQueued {
		if err := o.store.MarkRunning(ctx, job.ID); err != nil {
			return nil, err
		}
	}
	o.log.Info("Bulk job running.", "job", job.ID,
		"done", job.SentMessages+job.FailedMessages, "total", job.TotalMessages)
	_ = o.provider.SetOnline(o.stopCtx, true) // presence is cosmetic
	return &activeJob{id: job.ID, readyAt: time.Now()}, nil
}

func (o *Outbox) advanceJob(ctx context.Context, job *activeJob) error {
	switch o.provider.Status() {
	case corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED:
	case corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH, corev1.InstanceStatus_INSTANCE_STATUS_ERROR:
		return o.finishJob(ctx, job, JobFailed, "The instance lost its session before the job could finish.")
	default:
		// Probably reconnecting. The job waits; nothing is marked as failed.
		o.wait(o.opts.ReconnectCheck)
		return nil
	}

	if job.next == nil {
		next, err := o.store.NextPending(ctx, job.id)
		if err != nil {
			return err
		}
		if next == nil {
			return o.finishJob(ctx, job, JobCompleted, "")
		}
		job.next = next
		// The planned delay already includes the time spent typing.
		idle := max(time.Duration(next.DelayMs)*time.Millisecond-o.typingDuration(next.Text), 0)
		job.readyAt = later(job.readyAt, time.Now()).Add(idle)
	}

	if wait := time.Until(later(job.readyAt, o.lastSentAt.Add(o.minGap()))); wait > 0 {
		o.wait(wait)
		return nil
	}

	message := job.next
	if job.onBreak {
		_ = o.provider.SetOnline(o.stopCtx, true)
		job.onBreak = false
	}
	o.typeLikeAHuman(message.Recipient, message.Text)
	// Cancelled or stopping while typing: nothing was sent, the message stays pending.
	if o.isStopped() || o.wasCancelled(job.id) {
		return nil
	}

	if err := o.store.MarkSending(ctx, message.ID); err != nil {
		return err
	}
	id, err := o.provider.SendText(ctx, message.Recipient, message.Text)
	switch {
	case err == nil:
		o.lastSentAt = time.Now()
		if err := o.store.MarkSent(ctx, job.id, message.ID, id); err != nil {
			return err
		}
	case calyxerr.CodeOf(err) == codes.Unavailable:
		// Nothing reached WhatsApp (connection dropped): try this same message again later.
		if err := o.store.MarkPending(ctx, message.ID); err != nil {
			return err
		}
		o.wait(o.opts.ReconnectCheck)
		return nil
	default:
		o.log.Warn("Bulk message failed.", "job", job.id, "position", message.Position, "err", err)
		if err := o.store.MarkFailed(ctx, job.id, message.ID, err.Error()); err != nil {
			return err
		}
	}

	job.next = nil
	job.readyAt = time.Now().Add(time.Duration(message.PauseAfterMs) * time.Millisecond)
	if message.PauseAfterMs > 0 && o.provider.Pacing().SimulateTyping {
		o.log.Debug("Human-like break.", "ms", message.PauseAfterMs)
		_ = o.provider.SetOnline(o.stopCtx, false)
		job.onBreak = true
	}
	return nil
}

func (o *Outbox) finishJob(ctx context.Context, job *activeJob, status JobStatus, reason string) error {
	finished, err := o.store.Finish(ctx, job.id, status, reason)
	if err != nil {
		return err // the job stays active and is finished on the next step
	}
	o.job = nil
	o.setMayHaveJobs()
	_ = o.provider.SetOnline(o.stopCtx, false)
	if finished == nil {
		return nil // someone else (a cancel) closed it first
	}
	o.log.Info("Bulk job finished.", "job", job.id, "status", status,
		"sent", finished.SentMessages, "failed", finished.FailedMessages)
	if o.opts.OnJobFinished != nil {
		o.opts.OnJobFinished(finished)
	}
	return nil
}

func (o *Outbox) minGap() time.Duration {
	return time.Duration(o.provider.Pacing().MinGapMs) * time.Millisecond
}

func (o *Outbox) typingDuration(text string) time.Duration {
	if !o.provider.Pacing().SimulateTyping {
		return 0
	}
	return TypingDuration(text)
}

func (o *Outbox) typeLikeAHuman(recipient, text string) {
	if !o.provider.Pacing().SimulateTyping {
		return
	}
	// Presence is cosmetic: if it fails, the real problem shows up when sending.
	_ = o.provider.ShowTyping(o.stopCtx, recipient, TypingDuration(text))
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
