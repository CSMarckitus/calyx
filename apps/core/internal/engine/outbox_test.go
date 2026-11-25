package engine_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/engine"
	"calyx/core/internal/testutil"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

type outboxFixture struct {
	db         *sql.DB
	store      *engine.BulkStore
	fake       *testutil.FakeProvider
	outbox     *engine.Outbox
	instanceID string
	finished   atomic.Int32 // calls to OnJobFinished
}

// newOutboxFixture builds everything but does not start the outbox, so a test can prepare the
// database or the fake first. configure may be nil.
func newOutboxFixture(t *testing.T, configure func(f *outboxFixture)) *outboxFixture {
	t.Helper()
	f := &outboxFixture{db: testutil.NewDB(t), fake: testutil.NewFakeProvider("shop")}
	f.store = engine.NewBulkStore(f.db)
	f.instanceID = testutil.CreateInstance(t, f.db, "shop")
	if configure != nil {
		configure(f)
	}
	f.outbox = engine.NewOutbox(f.fake, f.store, f.instanceID, engine.OutboxOptions{
		ReconnectCheck: 50 * time.Millisecond,
		OnJobFinished:  func(*engine.BulkJob) { f.finished.Add(1) },
	})
	f.outbox.Start()
	t.Cleanup(f.outbox.Stop)
	return f
}

// createJob stores a bulk job with one message per recipient, each with the given delay.
func (f *outboxFixture) createJob(t *testing.T, delayMs int, recipients ...string) *engine.BulkJob {
	t.Helper()
	messages := make([]engine.BulkMessageInput, len(recipients))
	for i, r := range recipients {
		delay := delayMs
		messages[i] = engine.BulkMessageInput{To: r, Text: "Reminder for " + r, DelayMs: &delay}
	}
	plan, estimatedMs, err := engine.PlanBulk(messages, engine.BulkPacingOptions{}, f.fake.Pacing())
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := f.store.Create(context.Background(), f.instanceID, plan, estimatedMs, nil)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func (f *outboxFixture) job(t *testing.T, id string) *engine.BulkJob {
	t.Helper()
	job, err := f.store.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func (f *outboxFixture) countMessages(t *testing.T, where string, args ...any) int {
	t.Helper()
	var count int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM bulk_job_messages WHERE "+where, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func (f *outboxFixture) sentTo(recipient string) bool {
	for _, m := range f.fake.Sent() {
		if m.Recipient == recipient {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, what string, condition func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestParallelDirectMessagesLeaveOneAtATimeAtLeastMinGapApart(t *testing.T) {
	f := newOutboxFixture(t, nil)
	var wg sync.WaitGroup
	for _, r := range []string{"34600000001", "34600000002", "34600000003"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.outbox.Send(context.Background(), r, "hi", 0, nil); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	sent := f.fake.Sent()
	if len(sent) != 3 {
		t.Fatalf("%d sent, want 3", len(sent))
	}
	for i := 1; i < len(sent); i++ {
		if gap := sent[i].At.Sub(sent[i-1].At); gap < 30*time.Millisecond {
			t.Errorf("messages %d and %d only %v apart", i-1, i, gap)
		}
	}
}

func TestTooManyWaitingDirectMessagesAreResourceExhausted(t *testing.T) {
	f := newOutboxFixture(t, nil)
	var wg sync.WaitGroup
	var exhausted atomic.Int32
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.outbox.Send(context.Background(), "34600000001", "hi", 0, nil)
			var full *calyxerr.Error
			if errors.As(err, &full) && full.Code == codes.ResourceExhausted {
				exhausted.Add(1)
				if full.Reason != corev1.ErrorReason_ERROR_REASON_INSTANCE_QUEUE_FULL {
					t.Errorf("reason = %v, want INSTANCE_QUEUE_FULL", full.Reason)
				}
				if want := f.fake.Pacing().QueueWait(); full.RetryAfter != want {
					t.Errorf("RetryAfter = %v, want %v", full.RetryAfter, want)
				}
			}
		}()
	}
	wg.Wait()
	if exhausted.Load() == 0 {
		t.Fatal("5 messages at once with MaxPendingDirect 3 and none rejected")
	}
}

func TestPlaceCountsTheQueueFromOne(t *testing.T) {
	f := newOutboxFixture(t, nil)
	// Never started: nothing leaves its queue.
	o := engine.NewOutbox(f.fake, f.store, f.instanceID, engine.OutboxOptions{})
	t.Cleanup(o.Stop)
	for i := range 3 {
		go func() { _, _ = o.Send(context.Background(), fmt.Sprintf("3460000000%d", i), "hi", int64(i+1), nil) }()
		waitFor(t, "message queued", func() bool { return engine.PendingDirect(o) == i+1 }, time.Second)
	}

	if place, ok := o.Place(3); !ok || place != 3 {
		t.Errorf("Place(3) = %d, %v, want 3, true", place, ok)
	}
	if place, ok := o.Place(1); !ok || place != 1 {
		t.Errorf("Place(1) = %d, %v, want 1, true", place, ok)
	}
	if _, ok := o.Place(99); ok {
		t.Error("Place(99) found an unknown record")
	}
}

func TestADirectMessageIsUnavailableWhileDisconnected(t *testing.T) {
	f := newOutboxFixture(t, func(f *outboxFixture) {
		f.fake.ForceStatus(corev1.InstanceStatus_INSTANCE_STATUS_DISCONNECTED)
	})
	_, err := f.outbox.Send(context.Background(), "34600000001", "hi", 0, nil)
	if calyxerr.CodeOf(err) != codes.Unavailable {
		t.Fatalf("got %v", err)
	}
}

func TestACancelledCallerIsNeverSent(t *testing.T) {
	f := newOutboxFixture(t, func(f *outboxFixture) {
		p := f.fake.Pacing()
		p.MinGapMs = 300
		f.fake.SetPacing(p)
	})
	if _, err := f.outbox.Send(context.Background(), "34600000001", "A", 0, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := f.outbox.Send(ctx, "34600000002", "B", 0, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	time.Sleep(400 * time.Millisecond) // past the gap, when B would have been sent
	if f.sentTo("34600000002") {
		t.Fatal("B was sent after its caller gave up")
	}
}

func TestABulkJobCompletesAndCounts(t *testing.T) {
	f := newOutboxFixture(t, func(f *outboxFixture) { f.fake.FailFor("34600000003") })
	job := f.createJob(t, 20, "34600000001", "34600000002", "34600000003", "34600000004")
	f.outbox.NotifyNewJob()

	waitFor(t, "job completed", func() bool { return f.job(t, job.ID).Status == engine.JobCompleted }, 3*time.Second)
	job = f.job(t, job.ID)
	if job.SentMessages != 3 || job.FailedMessages != 1 {
		t.Fatalf("sent %d, failed %d", job.SentMessages, job.FailedMessages)
	}
	if n := f.countMessages(t, "text <> ''"); n != 0 {
		t.Fatalf("%d messages still have their text", n)
	}
	waitFor(t, "OnJobFinished", func() bool { return f.finished.Load() == 1 }, time.Second)
	time.Sleep(100 * time.Millisecond)
	if n := f.finished.Load(); n != 1 {
		t.Fatalf("OnJobFinished called %d times", n)
	}
}

func TestADirectMessageOvertakesARunningBulkJob(t *testing.T) {
	f := newOutboxFixture(t, nil)
	f.createJob(t, 200, "34600000001", "34600000002", "34600000003", "34600000004", "34600000005")
	f.outbox.NotifyNewJob()

	waitFor(t, "first bulk message", func() bool { return len(f.fake.Sent()) >= 1 }, 3*time.Second)
	if _, err := f.outbox.Send(context.Background(), "34699999999", "direct", 0, nil); err != nil {
		t.Fatal(err)
	}
	sent := f.fake.Sent()
	position := -1
	for i, m := range sent {
		if m.Recipient == "34699999999" {
			position = i
		}
	}
	if position != 1 && position != 2 {
		t.Fatalf("the direct message went out in position %d", position)
	}
}

func TestACancelledJobStopsSending(t *testing.T) {
	f := newOutboxFixture(t, nil)
	job := f.createJob(t, 100, "34600000001", "34600000002", "34600000003", "34600000004", "34600000005")
	f.outbox.NotifyNewJob()

	waitFor(t, "first bulk message", func() bool { return len(f.fake.Sent()) >= 1 }, 3*time.Second)
	if _, err := f.store.Finish(context.Background(), job.ID, engine.JobCancelled, ""); err != nil {
		t.Fatal(err)
	}
	f.outbox.NotifyJobCancelled(job.ID)

	time.Sleep(400 * time.Millisecond)
	if n := len(f.fake.Sent()); n > 2 {
		t.Fatalf("%d messages sent after the cancel", n)
	}
	if n := f.countMessages(t, "status = 'CANCELLED'"); n < 3 {
		t.Fatalf("only %d messages cancelled", n)
	}
	if f.job(t, job.ID).Status != engine.JobCancelled {
		t.Fatal("the job is no longer CANCELLED")
	}
	if n := f.finished.Load(); n != 0 {
		t.Fatalf("OnJobFinished called %d times for a cancelled job", n)
	}
}

func TestABulkJobWaitsWhileDisconnectedAndContinues(t *testing.T) {
	f := newOutboxFixture(t, func(f *outboxFixture) {
		f.fake.ForceStatus(corev1.InstanceStatus_INSTANCE_STATUS_DISCONNECTED)
	})
	job := f.createJob(t, 20, "34600000001", "34600000002")
	f.outbox.NotifyNewJob()

	time.Sleep(300 * time.Millisecond)
	if n := len(f.fake.Sent()); n != 0 {
		t.Fatalf("%d sent while disconnected", n)
	}
	if n := f.countMessages(t, "status = 'FAILED'"); n != 0 {
		t.Fatalf("%d messages failed while disconnected", n)
	}

	f.fake.ForceStatus(corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED)
	waitFor(t, "job completed", func() bool { return f.job(t, job.ID).Status == engine.JobCompleted }, 3*time.Second)
}

func TestAnUnavailableSendIsRetriedNotFailed(t *testing.T) {
	f := newOutboxFixture(t, func(f *outboxFixture) { f.fake.UnavailableFor("34600000001") })
	job := f.createJob(t, 20, "34600000001")
	f.outbox.NotifyNewJob()

	waitFor(t, "job completed", func() bool { return f.job(t, job.ID).Status == engine.JobCompleted }, 3*time.Second)
	job = f.job(t, job.ID)
	if job.SentMessages != 1 || job.FailedMessages != 0 {
		t.Fatalf("sent %d, failed %d", job.SentMessages, job.FailedMessages)
	}
}

func TestAJobFailsWhenTheSessionIsLost(t *testing.T) {
	f := newOutboxFixture(t, func(f *outboxFixture) {
		f.fake.ForceStatus(corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH)
	})
	job := f.createJob(t, 20, "34600000001")
	f.outbox.NotifyNewJob()

	waitFor(t, "job failed", func() bool { return f.job(t, job.ID).Status == engine.JobFailed }, 3*time.Second)
	if e := f.job(t, job.ID).Error; !strings.Contains(e, "lost its session") {
		t.Fatalf("error %q", e)
	}
}

func TestAMessageInterruptedByARestartIsFailedAndNeverResent(t *testing.T) {
	var job *engine.BulkJob
	f := newOutboxFixture(t, func(f *outboxFixture) {
		job = f.createJob(t, 20, "34600000001", "34600000002")
		first, err := f.store.NextPending(context.Background(), job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.MarkSending(context.Background(), first.ID); err != nil {
			t.Fatal(err)
		}
	})

	waitFor(t, "job completed", func() bool { return f.job(t, job.ID).Status == engine.JobCompleted }, 3*time.Second)
	if n := f.countMessages(t, "position = 0 AND status = 'FAILED' AND error LIKE '%result is unknown%'"); n != 1 {
		t.Fatal("the interrupted message was not failed")
	}
	if f.sentTo("34600000001") {
		t.Fatal("the interrupted message was sent again")
	}
	if !f.sentTo("34600000002") {
		t.Fatal("the rest of the job was not sent")
	}
}

func TestStopInterruptsALongWait(t *testing.T) {
	f := newOutboxFixture(t, nil)
	job := f.createJob(t, 45000, "34600000001")
	f.outbox.NotifyNewJob()
	waitFor(t, "job running", func() bool { return f.job(t, job.ID).Status == engine.JobRunning }, 3*time.Second)

	start := time.Now()
	f.outbox.Stop()
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Fatalf("Stop took %v", took)
	}
	if n := f.countMessages(t, "status = 'PENDING'"); n != 1 {
		t.Fatal("the message is no longer PENDING")
	}
}

func TestStopRejectsWaitingDirectMessages(t *testing.T) {
	f := newOutboxFixture(t, func(f *outboxFixture) {
		p := f.fake.Pacing()
		p.MinGapMs = 1000
		f.fake.SetPacing(p)
	})
	if _, err := f.outbox.Send(context.Background(), "34600000001", "first", 0, nil); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := f.outbox.Send(context.Background(), "34600000002", "second", 0, nil)
		result <- err
	}()
	time.Sleep(100 * time.Millisecond) // the second one is waiting for the gap
	f.outbox.Stop()

	if err := <-result; calyxerr.CodeOf(err) != codes.Unavailable {
		t.Fatalf("got %v", err)
	}
}

func TestAWakeDuringBusyWorkIsNotLost(t *testing.T) {
	var jobID atomic.Value
	f := newOutboxFixture(t, nil)
	f.fake.BeforeSend(func(recipient string) {
		if recipient != "34699999999" {
			return
		}
		// The loop is busy inside SendText right now.
		job := f.createJob(t, 20, "34600000001")
		jobID.Store(job.ID)
		f.outbox.NotifyNewJob()
	})
	if _, err := f.outbox.Send(context.Background(), "34699999999", "direct", 0, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "job completed", func() bool {
		return f.job(t, jobID.Load().(string)).Status == engine.JobCompleted
	}, 3*time.Second)
}

// outcomes records every call of onDone.
type outcomes struct {
	mu  sync.Mutex
	got []engine.SendOutcome
}

func (r *outcomes) onDone(o engine.SendOutcome) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, o)
}

func (r *outcomes) list() []engine.SendOutcome {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]engine.SendOutcome(nil), r.got...)
}

// expectOne fails unless onDone ran exactly once, with want.
func (r *outcomes) expectOne(t *testing.T, want engine.SendOutcome) {
	t.Helper()
	if got := r.list(); len(got) != 1 || got[0] != want {
		t.Fatalf("onDone got %+v, want exactly one %+v", got, want)
	}
}

var notSent = engine.SendOutcome{Kind: engine.OutcomeNotSent}

// slowGap makes the outbox wait 1 s between two messages, so a second message stays queued.
func slowGap(f *outboxFixture) {
	p := f.fake.Pacing()
	p.MinGapMs = 1000
	f.fake.SetPacing(p)
}

func TestOnDoneWhenTheQueueIsFullIsNotSent(t *testing.T) {
	f := newOutboxFixture(t, nil)
	// Never started: nothing leaves its queue.
	o := engine.NewOutbox(f.fake, f.store, f.instanceID, engine.OutboxOptions{})
	t.Cleanup(o.Stop)
	for i := range 3 {
		go func() { _, _ = o.Send(context.Background(), fmt.Sprintf("3460000000%d", i), "hi", 0, nil) }()
	}
	waitFor(t, "3 messages queued", func() bool { return engine.PendingDirect(o) == 3 }, time.Second)

	var r outcomes
	_, err := o.Send(context.Background(), "34600000009", "hi", 0, r.onDone)
	if calyxerr.CodeOf(err) != codes.ResourceExhausted {
		t.Fatalf("got %v", err)
	}
	r.expectOne(t, notSent)
}

func TestOnDoneWhenTheOutboxStoppedIsNotSent(t *testing.T) {
	f := newOutboxFixture(t, nil)
	f.outbox.Stop()

	var r outcomes
	_, err := f.outbox.Send(context.Background(), "34600000001", "hi", 0, r.onDone)
	if calyxerr.CodeOf(err) != codes.Unavailable {
		t.Fatalf("got %v", err)
	}
	r.expectOne(t, notSent)
}

func TestOnDoneWhenTheCallerGivesUpInTheQueueIsNotSent(t *testing.T) {
	f := newOutboxFixture(t, slowGap)
	if _, err := f.outbox.Send(context.Background(), "34600000001", "A", 0, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var r outcomes
	_, err := f.outbox.Send(ctx, "34600000002", "B", 0, r.onDone)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	r.expectOne(t, notSent)
	f.outbox.Stop()
	if f.sentTo("34600000002") {
		t.Fatal("B was sent after its caller gave up")
	}
}

func TestOnDoneWhenTheLoopSkipsAGivenUpMessageIsNotSent(t *testing.T) {
	f := newOutboxFixture(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var r outcomes
	engine.QueueGivenUp(ctx, f.outbox, "34600000001", "hi", r.onDone)
	waitFor(t, "onDone", func() bool { return len(r.list()) > 0 }, time.Second)
	f.outbox.Stop()
	r.expectOne(t, notSent)
	if f.sentTo("34600000001") {
		t.Fatal("a message whose caller gave up was sent")
	}
}

func TestOnDoneWhenStopEmptiesTheQueueIsNotSentForEach(t *testing.T) {
	f := newOutboxFixture(t, slowGap)
	if _, err := f.outbox.Send(context.Background(), "34600000001", "A", 0, nil); err != nil {
		t.Fatal(err)
	}
	var r [2]outcomes
	errs := make(chan error, 2)
	for i := range r {
		go func() {
			_, err := f.outbox.Send(context.Background(), fmt.Sprintf("3460000001%d", i), "hi", 0, r[i].onDone)
			errs <- err
		}()
	}
	waitFor(t, "2 messages queued", func() bool { return engine.PendingDirect(f.outbox) == 2 }, time.Second)
	f.outbox.Stop()

	for range r {
		if err := <-errs; calyxerr.CodeOf(err) != codes.Unavailable {
			t.Fatalf("got %v", err)
		}
	}
	for i := range r {
		r[i].expectOne(t, notSent)
	}
}

func TestOnDoneWhenNotConnectedIsNotSent(t *testing.T) {
	f := newOutboxFixture(t, func(f *outboxFixture) {
		f.fake.ForceStatus(corev1.InstanceStatus_INSTANCE_STATUS_DISCONNECTED)
	})
	var r outcomes
	_, err := f.outbox.Send(context.Background(), "34600000001", "hi", 0, r.onDone)
	if calyxerr.CodeOf(err) != codes.Unavailable {
		t.Fatalf("got %v", err)
	}
	r.expectOne(t, notSent)
}

func TestOnDoneWhenStoppedWhileTypingIsNotSent(t *testing.T) {
	typing := make(chan struct{})
	f := newOutboxFixture(t, func(f *outboxFixture) {
		p := f.fake.Pacing()
		p.SimulateTyping = true
		f.fake.SetPacing(p)
		// Typing lasts until Stop cancels it.
		f.fake.BeforeTyping(func(ctx context.Context) {
			close(typing)
			<-ctx.Done()
		})
	})
	var r outcomes
	errs := make(chan error, 1)
	go func() {
		_, err := f.outbox.Send(context.Background(), "34600000001", "hi", 0, r.onDone)
		errs <- err
	}()
	<-typing
	f.outbox.Stop()

	if err := <-errs; calyxerr.CodeOf(err) != codes.Unavailable {
		t.Fatalf("got %v", err)
	}
	r.expectOne(t, notSent)
	if len(f.fake.Sent()) != 0 {
		t.Fatal("the message was sent after Stop")
	}
}

func TestOnDoneWhenTheProviderSendsIsSentWithItsID(t *testing.T) {
	f := newOutboxFixture(t, nil)
	var r outcomes
	id, err := f.outbox.Send(context.Background(), "34600000001", "hi", 0, r.onDone)
	if err != nil || id == "" {
		t.Fatalf("got %q, %v", id, err)
	}
	r.expectOne(t, engine.SendOutcome{Kind: engine.OutcomeSent, MessageID: id})
}

func TestOnDoneWhenTheProviderIsUnavailableIsNotSent(t *testing.T) {
	f := newOutboxFixture(t, func(f *outboxFixture) { f.fake.UnavailableFor("34600000001") })
	var r outcomes
	_, err := f.outbox.Send(context.Background(), "34600000001", "hi", 0, r.onDone)
	if calyxerr.CodeOf(err) != codes.Unavailable {
		t.Fatalf("got %v", err)
	}
	r.expectOne(t, notSent)
}

func TestOnDoneWhenTheProviderFailsIsUnknown(t *testing.T) {
	f := newOutboxFixture(t, func(f *outboxFixture) { f.fake.FailFor("34600000001") })
	var r outcomes
	if _, err := f.outbox.Send(context.Background(), "34600000001", "hi", 0, r.onDone); err == nil {
		t.Fatal("no error")
	}
	r.expectOne(t, engine.SendOutcome{Kind: engine.OutcomeUnknown})
}

func TestOnDoneRunsOnceWithTheRealOutcomeWhenTheCallerGivesUpDuringTheSend(t *testing.T) {
	inSend, release := make(chan struct{}), make(chan struct{})
	f := newOutboxFixture(t, func(f *outboxFixture) {
		f.fake.BeforeSend(func(string) {
			close(inSend)
			<-release
		})
	})
	ctx, cancel := context.WithCancel(context.Background())
	var r outcomes
	errs := make(chan error, 1)
	go func() {
		_, err := f.outbox.Send(ctx, "34600000001", "hi", 0, r.onDone)
		errs <- err
	}()
	<-inSend
	cancel()
	if err := <-errs; !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if got := r.list(); len(got) != 0 {
		t.Errorf("onDone ran before the provider answered: %+v", got) // not Fatal: release the send first
	}
	close(release)
	f.outbox.Stop() // returns once the loop finished the send
	r.expectOne(t, engine.SendOutcome{Kind: engine.OutcomeSent, MessageID: "fake-1"})
}

func TestOnDoneRunsBeforeSendReturns(t *testing.T) {
	f := newOutboxFixture(t, nil)
	var r outcomes
	slow := func(o engine.SendOutcome) {
		time.Sleep(20 * time.Millisecond) // a slow database write
		r.onDone(o)
	}
	id, err := f.outbox.Send(context.Background(), "34600000001", "hi", 0, slow)
	if err != nil {
		t.Fatal(err)
	}
	r.expectOne(t, engine.SendOutcome{Kind: engine.OutcomeSent, MessageID: id})
}
