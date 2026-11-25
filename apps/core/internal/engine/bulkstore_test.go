package engine_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/engine"
	"calyx/core/internal/testutil"
)

func plannedMessages(n int) []engine.PlannedMessage {
	plan := make([]engine.PlannedMessage, n)
	for i := range plan {
		plan[i] = engine.PlannedMessage{Recipient: "34600111222", Text: "Reminder", DelayMs: 2000}
	}
	return plan
}

// newJob returns a store and a job with n messages on a new instance.
func newJob(t *testing.T, n int) (*sql.DB, *engine.BulkStore, *engine.BulkJob) {
	t.Helper()
	db := testutil.NewDB(t)
	store := engine.NewBulkStore(db)
	instanceID := testutil.CreateInstance(t, db, "shop")
	job, _, err := store.Create(context.Background(), instanceID, plannedMessages(n), n*2000, nil)
	if err != nil {
		t.Fatal(err)
	}
	return db, store, job
}

func countMessages(t *testing.T, db *sql.DB, where string) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM bulk_job_messages WHERE " + where).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestCreateStoresTheWholePlan(t *testing.T) {
	_, store, job := newJob(t, 1200)
	if job.Status != engine.JobQueued || job.TotalMessages != 1200 || job.InstanceName != "shop" {
		t.Fatalf("got %+v", job)
	}
	next, err := store.NextPending(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if next == nil || next.Position != 0 {
		t.Fatalf("got %+v", next)
	}
}

func TestMarkSentClearsTheTextAndCounts(t *testing.T) {
	ctx := context.Background()
	db, store, job := newJob(t, 3)
	next, _ := store.NextPending(ctx, job.ID)
	if err := store.MarkSent(ctx, job.ID, next.ID, "wamid-1"); err != nil {
		t.Fatal(err)
	}
	if n := countMessages(t, db, "status = 'SENT' AND text = '' AND provider_message_id = 'wamid-1' AND sent_at IS NOT NULL"); n != 1 {
		t.Fatalf("%d messages sent with their text cleared, want 1", n)
	}
	job, _ = store.Get(ctx, job.ID)
	if job.SentMessages != 1 {
		t.Fatalf("SentMessages %d", job.SentMessages)
	}
}

func TestRecoverInterruptedFailsWhatWasBeingSent(t *testing.T) {
	ctx := context.Background()
	db, store, job := newJob(t, 3)
	next, _ := store.NextPending(ctx, job.ID)
	if err := store.MarkSending(ctx, next.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RecoverInterrupted(ctx, job.InstanceID); err != nil {
		t.Fatal(err)
	}
	if n := countMessages(t, db, "status = 'FAILED' AND error LIKE '%result is unknown%'"); n != 1 {
		t.Fatalf("%d interrupted messages failed, want 1", n)
	}
	job, _ = store.Get(ctx, job.ID)
	if job.FailedMessages != 1 {
		t.Fatalf("FailedMessages %d", job.FailedMessages)
	}
}

func TestFinishCancelsThePendingMessages(t *testing.T) {
	ctx := context.Background()
	db, store, job := newJob(t, 3)
	finished, err := store.Finish(ctx, job.ID, engine.JobCancelled, "")
	if err != nil {
		t.Fatal(err)
	}
	if finished == nil || finished.Status != engine.JobCancelled || finished.FinishedAt == nil {
		t.Fatalf("got %+v", finished)
	}
	if n := countMessages(t, db, "status = 'CANCELLED' AND text = ''"); n != 3 {
		t.Fatalf("%d messages cancelled, want 3", n)
	}
}

func TestFinishTwiceReturnsNilTheSecondTime(t *testing.T) {
	ctx := context.Background()
	_, store, job := newJob(t, 1)
	if _, err := store.Finish(ctx, job.ID, engine.JobCompleted, ""); err != nil {
		t.Fatal(err)
	}
	again, err := store.Finish(ctx, job.ID, engine.JobCancelled, "")
	if err != nil || again != nil {
		t.Fatalf("got %+v, %v", again, err)
	}
	job, _ = store.Get(ctx, job.ID)
	if job.Status != engine.JobCompleted {
		t.Fatalf("the second finish changed the status to %s", job.Status)
	}
}

func TestInstancesWithUnfinishedJobsSkipsDisabledInstances(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)
	store := engine.NewBulkStore(db)
	enabled := testutil.CreateInstance(t, db, "enabled")
	disabled := testutil.CreateInstance(t, db, "disabled")
	for _, id := range []string{enabled, disabled} {
		if _, _, err := store.Create(ctx, id, plannedMessages(1), 2000, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("UPDATE instances SET is_enabled = 0 WHERE id = ?", disabled); err != nil {
		t.Fatal(err)
	}

	names, err := store.InstancesWithUnfinishedJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "enabled" {
		t.Fatalf("got %v", names)
	}
}

func TestGetOfAMissingJobIsNotFound(t *testing.T) {
	store := engine.NewBulkStore(testutil.NewDB(t))
	_, err := store.Get(context.Background(), "missing")
	if calyxerr.CodeOf(err) != codes.NotFound {
		t.Fatalf("got %v", err)
	}
}

func TestGetManyReturnsFoundJobsOnceAndLeavesOutMissingIds(t *testing.T) {
	ctx := context.Background()
	db, store, first := newJob(t, 1)
	second, _, err := store.Create(ctx, testutil.CreateInstance(t, db, "other"), plannedMessages(2), 4000, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.GetMany(ctx, []string{first.ID, "missing", second.ID, first.ID})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, job := range got {
		names[job.ID] = job.InstanceName
	}
	if len(got) != 2 || names[first.ID] != "shop" || names[second.ID] != "other" {
		t.Fatalf("got %+v", got)
	}
}

// Formally checks that a finished job stays finished: the outbox reads a QUEUED job,
// a cancel commits, and then the outbox marks the job RUNNING.
func TestMarkRunningDoesNotReopenACancelledJob(t *testing.T) {
	ctx := context.Background()
	_, store, job := newJob(t, 2)
	if _, err := store.Finish(ctx, job.ID, engine.JobCancelled, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkRunning(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	job, _ = store.Get(ctx, job.ID)
	if job.Status != engine.JobCancelled {
		t.Fatalf("a cancelled job became %s", job.Status)
	}
}

// Formally checks that a finished job stays finished: a message is being sent, the job is
// cancelled, the send comes back Unavailable and the message is put back in the queue of a job
// that will never run again.
func TestMarkPendingOfAFinishedJobCancelsTheMessage(t *testing.T) {
	ctx := context.Background()
	db, store, job := newJob(t, 2)
	next, _ := store.NextPending(ctx, job.ID)
	if err := store.MarkSending(ctx, next.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Finish(ctx, job.ID, engine.JobCancelled, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkPending(ctx, next.ID); err != nil {
		t.Fatal(err)
	}
	if n := countMessages(t, db, "status = 'PENDING'"); n != 0 {
		t.Fatalf("%d pending messages in a cancelled job", n)
	}
	if n := countMessages(t, db, "status = 'CANCELLED' AND text = ''"); n != 2 {
		t.Fatalf("%d messages cancelled, want 2", n)
	}
}

func TestCreateWithoutAKeyWritesNoSendRecord(t *testing.T) {
	db, _, _ := newJob(t, 2)
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM send_records").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d send_records rows, want 0", n)
	}
}

func TestCreateWithAKnownKeyReturnsTheJobInItsCurrentStateAndStoresNothing(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)
	store := engine.NewBulkStore(db)
	instanceID := testutil.CreateInstance(t, db, "shop")
	bulkKey := &engine.BulkKey{Key: "b1", Fingerprint: []byte("fingerprint"), Now: time.Now()}
	job, replayed, err := store.Create(ctx, instanceID, plannedMessages(2), 4000, bulkKey)
	if err != nil || replayed {
		t.Fatalf("first create: replayed %v, %v", replayed, err)
	}
	if _, err := store.Finish(ctx, job.ID, engine.JobCancelled, ""); err != nil {
		t.Fatal(err)
	}

	again, replayed, err := store.Create(ctx, instanceID, plannedMessages(3), 6000, bulkKey)
	if err != nil || !replayed {
		t.Fatalf("retry: replayed %v, %v", replayed, err)
	}
	if again.ID != job.ID || again.Status != engine.JobCancelled || again.TotalMessages != 2 {
		t.Fatalf("got %+v, want the first job, cancelled", again)
	}
	if n := countMessages(t, db, "1 = 1"); n != 2 {
		t.Fatalf("%d messages stored, want 2", n)
	}
}
