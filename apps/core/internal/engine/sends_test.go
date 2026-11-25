package engine_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/database"
	"calyx/core/internal/engine"
	"calyx/core/internal/events"
	"calyx/core/internal/testutil"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

const shipped = "Your order has shipped"

// wantReason checks the code and the reason of a refusal, and that its error message does not
// repeat the key, the recipient or the message text.
func wantReason(t *testing.T, err error, code codes.Code, reason corev1.ErrorReason, secrets ...string) {
	t.Helper()
	if calyxerr.CodeOf(err) != code || reasonOf(err) != reason {
		t.Fatalf("got code %v reason %v, want code %v reason %v", calyxerr.CodeOf(err), reasonOf(err), code, reason)
	}
	for i, s := range secrets {
		if strings.Contains(err.Error(), s) {
			t.Fatalf("the error message repeats a key, a recipient or a message text (forbidden value %d)", i)
		}
	}
}

// keyStatus returns the status of the row of an idempotency key.
func (f *orchestratorFixture) keyStatus(t *testing.T, k string) string {
	t.Helper()
	var status string
	if err := f.db.QueryRow("SELECT status FROM send_records WHERE idempotency_key = ?", k).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func (f *orchestratorFixture) recordCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM send_records").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// holdSends makes every SendText of the fake wait until the returned release is called; sending
// is closed when the first one starts waiting.
func holdSends(t *testing.T, fake *testutil.FakeProvider) (sending chan struct{}, release func()) {
	sending, proceed := make(chan struct{}), make(chan struct{})
	first := true
	fake.BeforeSend(func(string) {
		if first {
			first = false
			close(sending)
		}
		<-proceed
	})
	released := false
	release = func() {
		if !released {
			released = true
			close(proceed)
		}
	}
	t.Cleanup(release) // before the cleanup of the fixture: Stop waits for the held send
	return sending, release
}

func TestARetryWhileTheSendIsInProgressIsAbortedThenReplayed(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.o.LoadAllActiveInstances(ctx)
	if _, err := f.o.SendText(ctx, "shop", "34600000009", "load the instance", nil); err != nil {
		t.Fatal(err)
	}
	fake := f.fake("shop")
	sending, release := holdSends(t, fake)

	callerCtx, stopWaiting := context.WithCancel(ctx)
	first := make(chan error, 1)
	go func() {
		_, err := f.o.SendText(callerCtx, "shop", "34600111222", shipped, key("k1"))
		first <- err
	}()
	<-sending
	stopWaiting()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("first caller: %v", err)
	}

	retryCtx, cancelRetry := context.WithTimeout(ctx, 5*time.Second) // a regression fails fast
	defer cancelRetry()
	_, err := f.o.SendText(retryCtx, "shop", "34600111222", shipped, key("k1"))
	wantReason(t, err, codes.Aborted, corev1.ErrorReason_ERROR_REASON_SEND_IN_PROGRESS, "k1", "34600111222", shipped)

	release()
	waitFor(t, "the outcome is recorded", func() bool { return f.keyStatus(t, "k1") == "SENT" }, 3*time.Second)
	result, err := f.o.SendText(retryCtx, "shop", "34600111222", shipped, key("k1"))
	if err != nil || !result.Replayed || result.MessageID != "fake-2" {
		t.Fatalf("replay: got %+v, %v", result, err)
	}
	if n := len(fake.Sent()); n != 2 { // the load message and one message of k1
		t.Fatalf("the provider got %d messages", n)
	}
}

// retryWait sends again with the key and returns the wait of the SEND_IN_PROGRESS refusal.
func (f *orchestratorFixture) retryWait(t *testing.T, k string) time.Duration {
	t.Helper()
	_, err := f.o.SendText(ctx, "shop", "34600111222", shipped, key(k))
	wantReason(t, err, codes.Aborted, corev1.ErrorReason_ERROR_REASON_SEND_IN_PROGRESS, k)
	var refusal *calyxerr.Error
	if !errors.As(err, &refusal) {
		t.Fatalf("not a calyxerr.Error: %v", err)
	}
	return refusal.RetryAfter
}

func TestTheWaitOfSendInProgressIsTheQueuePlaceTimesQueueWait(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.o.LoadAllActiveInstances(ctx)
	if _, err := f.o.SendText(ctx, "shop", "34600000009", "load the instance", nil); err != nil {
		t.Fatal(err)
	}
	fake := f.fake("shop")
	sending, release := holdSends(t, fake)
	runCtx, stop := context.WithCancel(ctx)
	t.Cleanup(stop)

	// k0 is in flight, so k1 to k3 wait in the queue at the places 1 to 3.
	for i, k := range []string{"k0", "k1", "k2", "k3"} {
		go func() { _, _ = f.o.SendText(runCtx, "shop", "3460011122"+k[1:], "text "+k, key(k)) }()
		if i == 0 {
			<-sending
		}
		waitFor(t, "the row exists", func() bool { return f.recordCount(t) == i+2 }, 3*time.Second)
		if i > 0 { // k(i) must be in the queue before k(i+1) starts, or the places change
			id := int64(i + 2) // the load message has the row 1
			waitFor(t, "the message is queued", func() bool { _, ok := engine.PlaceOf(f.o, "shop", id); return ok }, 3*time.Second)
		}
	}
	want := 3 * fake.Pacing().QueueWait()
	waitFor(t, "k3 is at place 3", func() bool {
		_, err := f.o.SendText(ctx, "shop", "34600111223", "text k3", key("k3"))
		var refusal *calyxerr.Error
		return errors.As(err, &refusal) && refusal.RetryAfter == want
	}, 3*time.Second)
	release()
}

func TestTheWaitOfSendInProgressIsOneQueueWaitWhenTheMessageIsNotInTheQueue(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.o.LoadAllActiveInstances(ctx)
	if _, err := f.o.SendText(ctx, "shop", "34600000009", "load the instance", nil); err != nil {
		t.Fatal(err)
	}
	fake := f.fake("shop")
	sending, release := holdSends(t, fake)
	runCtx, stop := context.WithCancel(ctx)
	t.Cleanup(stop)
	t.Cleanup(release)
	go func() { _, _ = f.o.SendText(runCtx, "shop", "34600111222", shipped, key("k1")) }()
	<-sending // the loop took the message: it is no longer in the queue

	if got, want := f.retryWait(t, "k1"), fake.Pacing().QueueWait(); got != want {
		t.Fatalf("wait = %v, want %v", got, want)
	}
}

func TestTheSameMessageWithANewKeyIsADuplicateFor10Minutes(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.o.LoadAllActiveInstances(ctx)
	first, err := f.o.SendText(ctx, "shop", "34600111222", shipped, key("k1"))
	if err != nil || first.Replayed {
		t.Fatalf("first send: %+v, %v", first, err)
	}

	f.advance(3 * time.Minute)
	for _, k := range []*string{key("k2"), nil} {
		_, err := f.o.SendText(ctx, "shop", "34600111222", "  "+shipped+"\n", k)
		wantReason(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_DUPLICATE_MESSAGE, "k2", "34600111222", shipped)
		var e *calyxerr.Error
		if !errors.As(err, &e) || e.Metadata["message_id"] != first.MessageID || e.Metadata["at"] == "" {
			t.Fatalf("metadata %v", e.Metadata)
		}
	}

	f.advance(8 * time.Minute)
	if _, err := f.o.SendText(ctx, "shop", "34600111222", shipped, key("k2")); err != nil {
		t.Fatalf("11 minutes later: %v", err)
	}
	if n := len(f.fake("shop").Sent()); n != 2 {
		t.Fatalf("the provider got %d messages", n)
	}
}

func TestAKeyReusedForAnotherMessageIsAConflict(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.o.LoadAllActiveInstances(ctx)
	if _, err := f.o.SendText(ctx, "shop", "34600111222", shipped, key("k1")); err != nil {
		t.Fatal(err)
	}
	_, err := f.o.SendText(ctx, "shop", "34600111222", "Another text", key("k1"))
	wantReason(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT, "k1", "34600111222", "Another text")
	_, err = f.o.SendText(ctx, "shop", "34600333444", shipped, key("k1"))
	wantReason(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT, "k1", "34600333444", shipped)
	if n := len(f.fake("shop").Sent()); n != 1 {
		t.Fatalf("the provider got %d messages", n)
	}
}

func TestARetryAfterARestartDuringTheSendIsAnUnknownOutcome(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.sqlite")
	open := func() *engine.Orchestrator {
		db, err := database.Open(ctx, "file:"+path)
		if err != nil {
			t.Fatal(err)
		}
		o := engine.NewOrchestrator(db, engine.NewBulkStore(db), events.NewBus(), engine.OrchestratorOptions{
			NormalizeRecipient: testutil.NormalizeRecipient,
			BuildProvider: func(row engine.InstanceRow, hooks engine.ProviderHooks) (engine.Provider, error) {
				fake := testutil.NewFakeProvider(row.Name)
				fake.SetHooks(hooks)
				if row.Name == "shop" {
					holdSends(t, fake)
				}
				return fake, nil
			},
		})
		t.Cleanup(func() { o.DestroyAllSessions(); db.Close() })
		return o
	}

	before := open()
	db, err := database.Open(ctx, "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	testutil.CreateInstance(t, db, "shop")
	before.LoadAllActiveInstances(ctx)
	sending := make(chan struct{})
	go func() {
		callerCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()
		close(sending)
		_, _ = before.SendText(callerCtx, "shop", "34600111222", shipped, key("k1"))
	}()
	<-sending
	waitFor(t, "a PENDING row", func() bool {
		var n int
		_ = db.QueryRow("SELECT COUNT(*) FROM send_records WHERE status = 'PENDING'").Scan(&n)
		return n == 1
	}, 3*time.Second)

	// The process stops with the send in progress; the next process recovers at start.
	after := open()
	if err := after.StartSendRecords(ctx); err != nil {
		t.Fatal(err)
	}
	after.LoadAllActiveInstances(ctx)
	retryCtx, cancelRetry := context.WithTimeout(ctx, 5*time.Second) // a regression fails fast
	defer cancelRetry()
	_, err = after.SendText(retryCtx, "shop", "34600111222", shipped, key("k1"))
	wantReason(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_SEND_OUTCOME_UNKNOWN, "k1", "34600111222", shipped)
	if !strings.Contains(err.Error(), "Check the chat") {
		t.Fatalf("error message %q", err.Error())
	}
	_, err = after.SendText(retryCtx, "shop", "34600111222", shipped, nil)
	wantReason(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_DUPLICATE_MESSAGE, "34600111222", shipped)
}

func TestANotSentErrorFreesTheKey(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.o.LoadAllActiveInstances(ctx)
	f.fake("shop").ForceStatus(disconnect)
	_, err := f.o.SendText(ctx, "shop", "34600111222", shipped, key("k1"))
	if calyxerr.CodeOf(err) != codes.Unavailable {
		t.Fatalf("disconnected: %v", err)
	}
	if n := f.recordCount(t); n != 0 {
		t.Fatalf("the failed attempt left %d rows", n)
	}

	f.fake("shop").ForceStatus(connected)
	result, err := f.o.SendText(ctx, "shop", "34600111222", shipped, key("k1"))
	if err != nil || result.Replayed || result.MessageID != "fake-1" {
		t.Fatalf("retry: %+v, %v", result, err)
	}
}

func TestAnUnavailableProviderErrorFreesTheKey(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.o.LoadAllActiveInstances(ctx)
	f.fake("shop").UnavailableFor("34600111222")
	if _, err := f.o.SendText(ctx, "shop", "34600111222", shipped, key("k1")); calyxerr.CodeOf(err) != codes.Unavailable {
		t.Fatalf("first: %v", err)
	}
	if _, err := f.o.SendText(ctx, "shop", "34600111222", shipped, key("k1")); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestAnotherProviderErrorIsAnUnknownOutcome(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.o.LoadAllActiveInstances(ctx)
	f.fake("shop").FailFor("34600111222")
	if _, err := f.o.SendText(ctx, "shop", "34600111222", shipped, key("k1")); err == nil {
		t.Fatal("the send did not fail")
	}
	if s := f.keyStatus(t, "k1"); s != "UNKNOWN" {
		t.Fatalf("row status %s", s)
	}
	_, err := f.o.SendText(ctx, "shop", "34600111222", shipped, key("k1"))
	wantReason(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_SEND_OUTCOME_UNKNOWN, "k1", "34600111222", shipped)
}

func TestACallerThatStopsWaitingInTheQueueLeavesNoRow(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.o.LoadAllActiveInstances(ctx)
	if _, err := f.o.SendText(ctx, "shop", "34600000009", "load the instance", nil); err != nil {
		t.Fatal(err)
	}
	sending, release := holdSends(t, f.fake("shop"))
	go func() { _, _ = f.o.SendText(ctx, "shop", "34600000008", "hold the loop", nil) }()
	<-sending

	callerCtx, stopWaiting := context.WithCancel(ctx)
	queued := make(chan error, 1)
	go func() {
		_, err := f.o.SendText(callerCtx, "shop", "34600111222", shipped, key("k1"))
		queued <- err
	}()
	waitFor(t, "a row for the queued message", func() bool { return f.recordCount(t) == 3 }, 3*time.Second)
	stopWaiting()
	if err := <-queued; !errors.Is(err, context.Canceled) {
		t.Fatalf("queued caller: %v", err)
	}
	if n := f.recordCount(t); n != 2 {
		t.Fatalf("the queued message left its row: %d rows", n)
	}
	release()
	waitFor(t, "the hold message is recorded", func() bool {
		var n int
		_ = f.db.QueryRow("SELECT COUNT(*) FROM send_records WHERE status = 'SENT'").Scan(&n)
		return n == 2
	}, 3*time.Second)
	if n := f.recordCount(t); n != 2 { // the load message and the hold message
		t.Fatalf("%d rows", n)
	}
}

func TestAnInvalidKeyIsInvalidArgumentBeforeTheInstanceIsUsed(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	for _, k := range []string{"", strings.Repeat("a", 256), "a key"} {
		for _, name := range []string{"shop", "nope"} {
			_, err := f.o.SendText(ctx, name, "34600111222", shipped, key(k))
			if calyxerr.CodeOf(err) != codes.InvalidArgument {
				t.Fatalf("key of %d characters on %s: %v", len(k), name, err)
			}
		}
	}
	if n := f.buildCount("shop"); n != 0 {
		t.Fatalf("the instance was loaded %d times", n)
	}
	f.o.LoadAllActiveInstances(ctx)
	if _, err := f.o.SendText(ctx, "shop", "34600111222", shipped, key(strings.Repeat("a", 255))); err != nil {
		t.Fatalf("a key of 255 characters: %v", err)
	}
}

func TestRefusalsAndReplaysLogTheInstanceAndTheReasonOnly(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.o.LoadAllActiveInstances(ctx)
	lost := f.instance(t, "lost", "connection_status = 'NEEDS_AUTH'")
	f.seedKey(t, lost, "secret-key", "wamid-1")
	logs := captureLogs(t)
	if _, err := f.o.SendText(ctx, "shop", "34600111222", shipped, key("secret-key")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.o.SendText(ctx, "shop", "34600111222", shipped, key("secret-key")); err != nil {
		t.Fatal(err)
	}
	_, _ = f.o.SendText(ctx, "shop", "34600111222", shipped, nil)
	_, _ = f.o.SendText(ctx, "shop", "34600111222", "Another text", key("secret-key"))
	// The same lines come from an instance that is not connected.
	_, _ = f.o.SendText(ctx, "lost", "34600111222", shipped, key("secret-key"))
	_, _ = f.o.SendText(ctx, "lost", "34600111222", "Another text", key("secret-key"))

	out := logs.String()
	for _, want := range []string{"ERROR_REASON_DUPLICATE_MESSAGE", "ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT", "replayed",
		"instance=lost reason=ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT", `"Send replayed: the idempotency key belongs to a message already sent." instance=lost`} {
		if !strings.Contains(out, want) {
			t.Errorf("the logs do not contain %q", want)
		}
	}
	for _, secret := range []string{"secret-key", "34600111222", shipped, "Another text"} {
		if strings.Contains(out, secret) {
			t.Errorf("the logs contain a key, a recipient or a message text")
		}
	}
}

func (f *orchestratorFixture) jobCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM bulk_jobs").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestARetriedBulkRequestReturnsTheSameJobInItsCurrentState(t *testing.T) {
	for name, kind := range map[string]engine.BulkKind{"same": engine.BulkSame, "custom": engine.BulkCustom} {
		t.Run(name, func(t *testing.T) {
			f := newOrchestratorFixture(t)
			f.instance(t, "shop", "")
			logs := captureLogs(t)
			messages := bulkOf("34600000001", "34600000002")
			job, replayed, err := f.o.CreateBulkJob(ctx, "shop", kind, messages, engine.BulkPacingOptions{}, key("b1"))
			if err != nil || replayed {
				t.Fatalf("first: replayed %v, %v", replayed, err)
			}
			waitFor(t, "bulk.finished", func() bool { return len(f.eventsOf("bulk.finished")) == 1 }, 3*time.Second)

			again, replayed, err := f.o.CreateBulkJob(ctx, "shop", kind, messages, engine.BulkPacingOptions{}, key("b1"))
			if err != nil || !replayed {
				t.Fatalf("retry: replayed %v, %v", replayed, err)
			}
			if again.ID != job.ID || again.Status != engine.JobCompleted || again.SentMessages != 2 {
				t.Fatalf("retry: got %+v, want the first job, completed", again)
			}
			if n := f.jobCount(t); n != 1 {
				t.Fatalf("%d jobs, want 1", n)
			}
			if n := len(f.fake("shop").Sent()); n != 2 {
				t.Fatalf("the provider got %d messages", n)
			}
			if n := strings.Count(logs.String(), "Bulk job planned."); n != 1 {
				t.Fatalf("%d \"Bulk job planned.\" log lines, want 1", n)
			}
		})
	}
}

func TestABulkKeyReusedWithOtherRecipientsIsAConflict(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	if _, _, err := f.o.CreateBulkJob(ctx, "shop", engine.BulkSame, bulkOf("34600000001", "34600000002"), engine.BulkPacingOptions{}, key("b1")); err != nil {
		t.Fatal(err)
	}
	_, _, err := f.o.CreateBulkJob(ctx, "shop", engine.BulkSame, bulkOf("34600000001", "34600000003"), engine.BulkPacingOptions{}, key("b1"))
	wantReason(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT, "b1", "34600000003", "Reminder")
	// The kind is part of the fingerprint: the same messages as a custom job are another request.
	_, _, err = f.o.CreateBulkJob(ctx, "shop", engine.BulkCustom, bulkOf("34600000001", "34600000002"), engine.BulkPacingOptions{}, key("b1"))
	wantReason(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT, "b1")
	if n := f.jobCount(t); n != 1 {
		t.Fatalf("%d jobs, want 1", n)
	}
}

func TestBulkKeysAndSingleKeysNeverMatch(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	if _, _, err := f.o.CreateBulkJob(ctx, "shop", engine.BulkSame, bulkOf("34600111222"), engine.BulkPacingOptions{}, key("b1")); err != nil {
		t.Fatal(err)
	}
	_, err := f.o.SendText(ctx, "shop", "34600111222", "Reminder", key("b1"))
	wantReason(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT, "b1", "34600111222", "Reminder")

	if _, err := f.o.SendText(ctx, "shop", "34600333444", shipped, key("k1")); err != nil {
		t.Fatal(err)
	}
	_, _, err = f.o.CreateBulkJob(ctx, "shop", engine.BulkCustom,
		[]engine.BulkMessageInput{{To: "34600333444", Text: shipped}}, engine.BulkPacingOptions{}, key("k1"))
	wantReason(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT, "k1", "34600333444", shipped)
	if n := len(f.fake("shop").Sent()); n > 2 {
		t.Fatalf("the provider got %d messages", n)
	}
}

func TestABulkJobWithoutAKeyWritesNoSendRecord(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	for range 2 {
		if _, _, err := f.o.CreateBulkJob(ctx, "shop", engine.BulkSame, bulkOf("34600000001"), engine.BulkPacingOptions{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.recordCount(t); n != 0 {
		t.Fatalf("%d send_records rows, want 0", n)
	}
	if n := f.jobCount(t); n != 2 {
		t.Fatalf("%d jobs, want 2", n)
	}
}

func TestAnInvalidBulkKeyIsInvalidArgumentBeforeTheInstanceIsUsed(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	for _, k := range []string{"", strings.Repeat("a", 256), "a key"} {
		_, _, err := f.o.CreateBulkJob(ctx, "shop", engine.BulkSame, bulkOf("34600000001"), engine.BulkPacingOptions{}, key(k))
		if calyxerr.CodeOf(err) != codes.InvalidArgument {
			t.Fatalf("key of %d characters: %v", len(k), err)
		}
	}
	if n := f.buildCount("shop"); n != 0 {
		t.Fatalf("the instance was loaded %d times", n)
	}
}

func TestBulkRefusalsAndReplaysLogTheInstanceAndTheReasonOnly(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	logs := captureLogs(t)
	for range 2 {
		if _, _, err := f.o.CreateBulkJob(ctx, "shop", engine.BulkSame, bulkOf("34600111222"), engine.BulkPacingOptions{}, key("secret-key")); err != nil {
			t.Fatal(err)
		}
	}
	_, _, _ = f.o.CreateBulkJob(ctx, "shop", engine.BulkSame, bulkOf("34600333444"), engine.BulkPacingOptions{}, key("secret-key"))

	out := logs.String()
	for _, want := range []string{"ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT", "replayed"} {
		if !strings.Contains(out, want) {
			t.Errorf("the logs do not contain %q", want)
		}
	}
	for _, secret := range []string{"secret-key", "34600111222", "34600333444", "Reminder"} {
		if strings.Contains(out, secret) {
			t.Errorf("the logs contain a key, a recipient or a message text")
		}
	}
}

// recoveryFixture is an orchestrator with a fixed clock, for the recovery at start and the sweeper.
type recoveryFixture struct {
	db         *sql.DB
	o          *engine.Orchestrator
	instanceID string
}

func newRecoveryFixture(t *testing.T) *recoveryFixture {
	t.Helper()
	db := testutil.NewDB(t)
	f := &recoveryFixture{db: db, instanceID: testutil.CreateInstance(t, db, "shop")}
	f.o = engine.NewOrchestrator(db, engine.NewBulkStore(db), events.NewBus(), engine.OrchestratorOptions{
		NormalizeRecipient: testutil.NormalizeRecipient,
		Now:                func() time.Time { return t0 },
	})
	t.Cleanup(f.o.DestroyAllSessions)
	return f
}

// insert adds a row with the given status, created age before t0, and returns its id.
func (f *recoveryFixture) insert(t *testing.T, status string, age time.Duration) int64 {
	t.Helper()
	res, err := f.db.Exec(`INSERT INTO send_records (instance_id, kind, fingerprint, status, created_at)
		VALUES (?, 'SINGLE', ?, ?, ?)`, f.instanceID, fp(status+age.String()), status, t0.Add(-age).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// status returns the status of a row, or "deleted".
func (f *recoveryFixture) status(t *testing.T, id int64) string {
	t.Helper()
	var status string
	err := f.db.QueryRow("SELECT status FROM send_records WHERE id = ?", id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "deleted"
	}
	if err != nil {
		t.Fatal(err)
	}
	return status
}

// returnsWithin fails the test when fn does not return within timeout.
func returnsWithin(t *testing.T, what string, fn func(), timeout time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("%s did not return within %v", what, timeout)
	}
}

func TestStartSendRecordsTurnsEveryPendingRowIntoUnknown(t *testing.T) {
	f := newRecoveryFixture(t)
	recent := f.insert(t, "PENDING", time.Second)
	old := f.insert(t, "PENDING", 2*time.Hour)
	sent := f.insert(t, "SENT", time.Minute)

	if err := f.o.StartSendRecords(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{recent, old} {
		var status string
		var finishedAt int64
		if err := f.db.QueryRow("SELECT status, finished_at FROM send_records WHERE id = ?", id).Scan(&status, &finishedAt); err != nil {
			t.Fatal(err)
		}
		if status != "UNKNOWN" || finishedAt != t0.UnixMilli() {
			t.Errorf("row %d: %s at %d, want UNKNOWN at now", id, status, finishedAt)
		}
	}
	if got := f.status(t, sent); got != "SENT" {
		t.Errorf("SENT row: %s, want SENT", got)
	}
}

func TestStartSendRecordsTwiceIsAnError(t *testing.T) {
	f := newRecoveryFixture(t)
	if err := f.o.StartSendRecords(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := f.o.StartSendRecords(context.Background()); err == nil {
		t.Fatal("the second StartSendRecords returned no error")
	}
}

func TestTheSweeperRecoversOldPendingRowsAndDeletesOldRows(t *testing.T) {
	f := newRecoveryFixture(t)
	tick := make(chan time.Time)
	engine.SetSweepTick(f.o, tick)
	if err := f.o.StartSendRecords(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Rows written after the start: only a sweep on a tick can change them.
	pendingOld := f.insert(t, "PENDING", time.Hour+time.Minute)
	pendingNew := f.insert(t, "PENDING", time.Hour-time.Minute)
	sentOld := f.insert(t, "SENT", 24*time.Hour+time.Minute)
	unknownNew := f.insert(t, "UNKNOWN", 23*time.Hour)

	// The sweeper takes the second tick only after the first sweep is complete.
	tick <- t0
	tick <- t0

	want := map[int64]string{pendingOld: "UNKNOWN", pendingNew: "PENDING", sentOld: "deleted", unknownNew: "UNKNOWN"}
	for id, status := range want {
		if got := f.status(t, id); got != status {
			t.Errorf("row %d: %s, want %s", id, got, status)
		}
	}
}

func TestDestroyAllSessionsStopsTheSweeper(t *testing.T) {
	f := newRecoveryFixture(t)
	tick := make(chan time.Time)
	engine.SetSweepTick(f.o, tick)
	if err := f.o.StartSendRecords(context.Background()); err != nil {
		t.Fatal(err)
	}
	returnsWithin(t, "DestroyAllSessions", f.o.DestroyAllSessions, 3*time.Second)
	select {
	case tick <- t0:
		t.Fatal("the sweeper still runs after DestroyAllSessions")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestDestroyAllSessionsWithoutStartSendRecordsReturnsAtOnce(t *testing.T) {
	f := newRecoveryFixture(t)
	returnsWithin(t, "DestroyAllSessions", f.o.DestroyAllSessions, time.Second)
}

func TestASendWithAKeyLogsItsOutcomeWithTheKeyHashOnly(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.o.LoadAllActiveInstances(ctx)
	logs := captureLogs(t)
	if _, err := f.o.SendText(ctx, "shop", "34600111222", shipped, key("abc")); err != nil {
		t.Fatal(err)
	}
	// The outcome is written when the loop calls onDone, so it can come after SendText returns.
	waitFor(t, "the outcome line", func() bool { return strings.Contains(logs.String(), "Send finished.") }, 3*time.Second)

	out := logs.String()
	for _, want := range []string{`msg="Send finished."`, "instance=shop", "outcome=SENT", "message_id=", "key_hash=ba7816bf8f01cfea"} {
		if !strings.Contains(out, want) {
			t.Errorf("the logs do not contain %q: %s", want, out)
		}
	}
	for _, secret := range []string{"abc", "34600111222", shipped} {
		if strings.Contains(out, secret) {
			t.Errorf("the logs contain a key, a recipient or a message text: %s", out)
		}
	}
}

func TestASendWithoutAKeyWritesNoOutcomeLine(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.o.LoadAllActiveInstances(ctx)
	logs := captureLogs(t)
	if _, err := f.o.SendText(ctx, "shop", "34600111222", shipped, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if strings.Contains(logs.String(), "Send finished.") {
		t.Errorf("a send without a key wrote an outcome line: %s", logs.String())
	}
}

// ---------------------------------------------------------------- an instance that is not connected

// seedKey writes the row of the key k for a send of shipped to 34600111222 on the instance id, as
// an earlier send left it. An empty messageID leaves the row PENDING; any other makes it SENT.
func (f *orchestratorFixture) seedKey(t *testing.T, instanceID, k, messageID string) {
	t.Helper()
	records := engine.NewSendRecords(f.db, time.Now)
	check, err := records.BeginSingle(ctx, instanceID, key(k), engine.SingleFingerprint("34600111222", shipped))
	if err != nil {
		t.Fatal(err)
	}
	if messageID != "" {
		if err := records.MarkSent(ctx, check.RecordID, messageID); err != nil {
			t.Fatal(err)
		}
	}
}

// wantNotConnected checks the refusal of a send to an instance that is not connected.
func wantNotConnected(t *testing.T, err error, name string) {
	t.Helper()
	var refusal *calyxerr.Error
	if !errors.As(err, &refusal) || refusal.Code != codes.Unavailable ||
		refusal.Message != "Instance "+name+" is not connected." || refusal.RetryAfter != 60*time.Second {
		t.Fatalf("got %#v, want UNAVAILABLE with a wait of 60 s", err)
	}
}

func TestALiveInstanceThatIsNotConnectedAnswersAKnownKeyAndRefusesTheRest(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.o.LoadAllActiveInstances(ctx)
	first, err := f.o.SendText(ctx, "shop", "34600111222", shipped, key("k1"))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the outcome is recorded", func() bool { return f.keyStatus(t, "k1") == "SENT" }, 3*time.Second)
	fake := f.fake("shop")
	fake.ForceStatus(disconnect)

	result, err := f.o.SendText(ctx, "shop", "34600111222", shipped, key("k1"))
	if err != nil || !result.Replayed || result.MessageID != first.MessageID {
		t.Fatalf("replay: got %+v, %v", result, err)
	}
	for _, k := range []*string{key("k2"), nil} {
		_, err := f.o.SendText(ctx, "shop", "34600333444", "Another text", k)
		wantNotConnected(t, err, "shop")
	}
	if n := f.recordCount(t); n != 1 {
		t.Fatalf("%d rows, want only the row of k1", n)
	}
	if n := engine.QueuedOf(f.o, "shop"); n != 0 {
		t.Fatalf("%d messages in the queue", n)
	}
	if n := len(fake.Sent()); n != 1 {
		t.Fatalf("the provider got %d messages", n)
	}
}

func TestSendInProgressOfALiveInstanceThatIsNotConnectedWaitsForItsPlaceInTheQueue(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.o.LoadAllActiveInstances(ctx)
	fake := f.fake("shop")
	sending, release := holdSends(t, fake)
	runCtx, stop := context.WithCancel(ctx)
	t.Cleanup(stop)

	// k0 is in flight, so k1 and k2 wait in the queue at the places 1 and 2.
	for i, k := range []string{"k0", "k1", "k2"} {
		go func() { _, _ = f.o.SendText(runCtx, "shop", "3460011122"+k[1:], "text "+k, key(k)) }()
		if i == 0 {
			<-sending
		}
		waitFor(t, "the row exists", func() bool { return f.recordCount(t) == i+1 }, 3*time.Second)
		if i > 0 {
			id := int64(i + 1)
			waitFor(t, "the message is queued", func() bool { _, ok := engine.PlaceOf(f.o, "shop", id); return ok }, 3*time.Second)
		}
	}
	fake.ForceStatus(disconnect)

	_, err := f.o.SendText(ctx, "shop", "34600111222", "text k2", key("k2"))
	wantReason(t, err, codes.Aborted, corev1.ErrorReason_ERROR_REASON_SEND_IN_PROGRESS, "k2")
	var refusal *calyxerr.Error
	errors.As(err, &refusal)
	if want := 2 * fake.Pacing().QueueWait(); refusal.RetryAfter != want {
		t.Fatalf("wait = %v, want %v", refusal.RetryAfter, want)
	}
	release()
}

func TestAnInstanceThatIsNotLiveAnswersAKnownKeyWithoutALoad(t *testing.T) {
	for _, changes := range []string{"connection_status = 'NEEDS_AUTH'", "is_enabled = 0"} {
		t.Run(changes, func(t *testing.T) {
			f := newOrchestratorFixture(t)
			id := f.instance(t, "shop", changes)
			f.seedKey(t, id, "k1", "wamid-1")

			result, err := f.o.SendText(ctx, "shop", "34600111222", shipped, key("k1"))
			if err != nil || !result.Replayed || result.MessageID != "wamid-1" {
				t.Fatalf("replay: got %+v, %v", result, err)
			}
			_, err = f.o.SendText(ctx, "shop", "34600333444", shipped, key("k2"))
			wantNotConnected(t, err, "shop")
			if n := f.buildCount("shop"); n != 0 {
				t.Fatalf("%d providers built", n)
			}
		})
	}
}

func TestSendInProgressOfAnInstanceThatIsNotLiveWaits60Seconds(t *testing.T) {
	f := newOrchestratorFixture(t)
	id := f.instance(t, "shop", "")
	f.seedKey(t, id, "k1", "")

	_, err := f.o.SendText(ctx, "shop", "34600111222", shipped, key("k1"))
	wantReason(t, err, codes.Aborted, corev1.ErrorReason_ERROR_REASON_SEND_IN_PROGRESS, "k1")
	var refusal *calyxerr.Error
	errors.As(err, &refusal)
	if refusal.RetryAfter != 60*time.Second {
		t.Fatalf("wait = %v, want 60 s", refusal.RetryAfter)
	}
	if n := f.buildCount("shop"); n != 0 {
		t.Fatalf("%d providers built", n)
	}
}

func TestASleepingSendOnlyInstanceWakesOnlyWhenItsStoredStatusIsConnected(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "sender", "is_used_only_for_send_messages = 1")
	f.instance(t, "failed", "is_used_only_for_send_messages = 1, connection_status = 'DISCONNECTED'")

	result, err := f.o.SendText(ctx, "sender", "34600111222", shipped, key("k1"))
	if err != nil || result.Replayed {
		t.Fatalf("stored CONNECTED: got %+v, %v", result, err)
	}
	if n := f.buildCount("sender"); n != 1 {
		t.Fatalf("stored CONNECTED: %d providers built", n)
	}
	_, err = f.o.SendText(ctx, "failed", "34600111222", shipped, key("k1"))
	wantNotConnected(t, err, "failed")
	if n := f.buildCount("failed"); n != 0 {
		t.Fatalf("stored DISCONNECTED: %d providers built", n)
	}
}

func TestAnInvalidRecipientOfAnInstanceThatIsNotConnectedIsInvalidArgument(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	if _, err := f.o.SendText(ctx, "shop", "not-a-number", shipped, key("k1")); calyxerr.CodeOf(err) != codes.InvalidArgument {
		t.Fatalf("got %v", err)
	}
	if n := f.buildCount("shop"); n != 0 {
		t.Fatalf("%d providers built", n)
	}
}
