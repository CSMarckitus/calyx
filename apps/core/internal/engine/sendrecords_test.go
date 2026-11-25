package engine_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/engine"
	"calyx/core/internal/testutil"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// The base time is not UTC, so the tests also check that "at" is converted to UTC.
var t0 = time.Date(2026, 11, 19, 10, 0, 0, 0, time.FixedZone("CET", 3600))

const t0UTC = "2026-11-19T09:00:00Z"

type recordsFixture struct {
	db         *sql.DB
	records    *engine.SendRecords
	instanceID string
	now        time.Time
}

func newRecordsFixture(t *testing.T) *recordsFixture {
	t.Helper()
	db := testutil.NewDB(t)
	f := &recordsFixture{db: db, instanceID: testutil.CreateInstance(t, db, "shop"), now: t0}
	f.records = engine.NewSendRecords(db, func() time.Time { return f.now })
	return f
}

func fp(label string) []byte {
	sum := sha256.Sum256([]byte(label))
	return sum[:]
}

func key(k string) *string { return &k }

func (f *recordsFixture) begin(t *testing.T, k *string, fingerprint []byte) engine.SingleCheck {
	t.Helper()
	check, err := f.records.BeginSingle(context.Background(), f.instanceID, k, fingerprint)
	if err != nil {
		t.Fatalf("BeginSingle: code %v, reason %v", calyxerr.CodeOf(err), reasonOf(err))
	}
	if check.Replayed || check.RecordID == 0 {
		t.Fatalf("BeginSingle: want a new row, got replayed=%v id=%d", check.Replayed, check.RecordID)
	}
	return check
}

func reasonOf(err error) corev1.ErrorReason {
	var e *calyxerr.Error
	if errors.As(err, &e) {
		return e.Reason
	}
	return corev1.ErrorReason_ERROR_REASON_UNSPECIFIED
}

// refusal checks the code, the reason, the error message and the metadata of a refusal.
func refusal(t *testing.T, err error, code codes.Code, reason corev1.ErrorReason, message string, metadata map[string]string) {
	t.Helper()
	var e *calyxerr.Error
	if !errors.As(err, &e) {
		t.Fatalf("want a *calyxerr.Error with reason %v, got %T", reason, err)
	}
	if e.Code != code || e.Reason != reason {
		t.Fatalf("got code %v reason %v, want code %v reason %v", e.Code, e.Reason, code, reason)
	}
	if e.Message != message {
		t.Errorf("error message:\n got %q\nwant %q", e.Message, message)
	}
	if len(e.Metadata) != len(metadata) {
		t.Errorf("metadata %v, want %v", e.Metadata, metadata)
	}
	for k, v := range metadata {
		if e.Metadata[k] != v {
			t.Errorf("metadata %s = %q, want %q", k, e.Metadata[k], v)
		}
	}
}

func (f *recordsFixture) refuse(t *testing.T, k *string, fingerprint []byte) error {
	t.Helper()
	check, err := f.records.BeginSingle(context.Background(), f.instanceID, k, fingerprint)
	if err == nil {
		t.Fatalf("BeginSingle accepted the send: replayed=%v id=%d", check.Replayed, check.RecordID)
	}
	return err
}

// bulk creates a job of one message with the key, through BulkStore.Create. It returns the job
// id and whether the key replayed an earlier job.
func (f *recordsFixture) bulk(k string, fingerprint []byte, now time.Time) (string, bool, error) {
	job, replayed, err := engine.NewBulkStore(f.db).Create(context.Background(), f.instanceID, plannedMessages(1), 2000,
		&engine.BulkKey{Key: k, Fingerprint: fingerprint, Now: now})
	if err != nil {
		return "", false, err
	}
	return job.ID, replayed, nil
}

func (f *recordsFixture) count(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM send_records").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *recordsFixture) status(t *testing.T, id int64) (string, sql.NullInt64) {
	t.Helper()
	var status string
	var finishedAt sql.NullInt64
	if err := f.db.QueryRow("SELECT status, finished_at FROM send_records WHERE id = ?", id).Scan(&status, &finishedAt); err != nil {
		t.Fatal(err)
	}
	return status, finishedAt
}

const (
	conflictMessage   = "This idempotency key belongs to another message on this instance. Use a new key for a new message."
	inProgressMessage = "A send with this idempotency key is in progress."
	spamRule          = "Calyx does not allow the same message to the same recipient within 10 minutes: it looks like spam."
)

func TestSendRecordsKnownKeyWithAnotherFingerprintIsAConflict(t *testing.T) {
	f := newRecordsFixture(t)
	first := f.begin(t, key("k1"), fp("a"))
	if err := f.records.MarkSent(context.Background(), first.RecordID, "wamid.1"); err != nil {
		t.Fatal(err)
	}
	err := f.refuse(t, key("k1"), fp("b"))
	refusal(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT, conflictMessage, nil)
}

func TestSendRecordsKnownKeyWhilePendingIsInProgress(t *testing.T) {
	f := newRecordsFixture(t)
	f.begin(t, key("k1"), fp("a"))
	err := f.refuse(t, key("k1"), fp("a"))
	refusal(t, err, codes.Aborted, corev1.ErrorReason_ERROR_REASON_SEND_IN_PROGRESS, inProgressMessage, nil)
}

func TestSendRecordsKnownKeyAfterSentIsAReplay(t *testing.T) {
	f := newRecordsFixture(t)
	first := f.begin(t, key("k1"), fp("a"))
	if err := f.records.MarkSent(context.Background(), first.RecordID, "wamid.1"); err != nil {
		t.Fatal(err)
	}
	f.now = t0.Add(time.Minute)
	check, err := f.records.BeginSingle(context.Background(), f.instanceID, key("k1"), fp("a"))
	if err != nil {
		t.Fatalf("code %v, reason %v", calyxerr.CodeOf(err), reasonOf(err))
	}
	if !check.Replayed || check.MessageID != "wamid.1" || check.RecordID != 0 {
		t.Fatalf("got %+v, want a replay of wamid.1", check)
	}
	if n := f.count(t); n != 1 {
		t.Fatalf("%d rows after a replay, want 1", n)
	}
}

func TestSendRecordsKnownKeyAfterUnknownOutcomeIsRefused(t *testing.T) {
	f := newRecordsFixture(t)
	first := f.begin(t, key("k1"), fp("a"))
	if err := f.records.MarkUnknown(context.Background(), first.RecordID); err != nil {
		t.Fatal(err)
	}
	f.now = t0.Add(time.Hour) // the key rule has no 10-minute limit
	err := f.refuse(t, key("k1"), fp("a"))
	refusal(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_SEND_OUTCOME_UNKNOWN,
		"The outcome of the send with this idempotency key is unknown. Check the chat. To send the message again, use a new key after 2026-11-19T09:10:00Z.",
		map[string]string{"at": t0UTC})
}

func TestSendRecordsNotSentFreesTheKey(t *testing.T) {
	f := newRecordsFixture(t)
	first := f.begin(t, key("k1"), fp("a"))
	if err := f.records.MarkNotSent(context.Background(), first.RecordID); err != nil {
		t.Fatal(err)
	}
	f.begin(t, key("k1"), fp("a"))
	if n := f.count(t); n != 1 {
		t.Fatalf("%d rows, want 1", n)
	}
}

func TestSendRecordsKnownBulkKeyIsAReplayOrAConflict(t *testing.T) {
	f := newRecordsFixture(t)
	ctx := context.Background()
	first, replayed, err := f.bulk("k1", fp("bulk"), f.now)
	if err != nil || replayed {
		t.Fatalf("first create: replayed %v, code %v", replayed, calyxerr.CodeOf(err))
	}
	again, replayed, err := f.bulk("k1", fp("bulk"), f.now)
	if err != nil || !replayed || again != first {
		t.Fatalf("retry: job %q replayed %v, code %v; want a replay of the first job", again, replayed, calyxerr.CodeOf(err))
	}
	_, _, err = f.bulk("k1", fp("other"), f.now)
	refusal(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT, conflictMessage, nil)
	var jobs int
	if err := f.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM bulk_jobs").Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 || f.count(t) != 1 {
		t.Fatalf("%d jobs and %d key rows, want 1 and 1", jobs, f.count(t))
	}

	err = f.refuse(t, key("k1"), fp("bulk"))
	refusal(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT, conflictMessage, nil)
}

func TestSendRecordsSingleKeyReusedForABulkJobIsAConflict(t *testing.T) {
	f := newRecordsFixture(t)
	f.begin(t, key("k1"), fp("a"))
	_, _, err := f.bulk("k1", fp("a"), f.now)
	refusal(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT, conflictMessage, nil)
}

func TestSendRecordsKeyOlderThan24HoursIsAbsent(t *testing.T) {
	f := newRecordsFixture(t)
	ctx := context.Background()
	first := f.begin(t, key("k1"), fp("a"))
	if err := f.records.MarkSent(ctx, first.RecordID, "wamid.1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.bulk("k2", fp("bulk"), f.now); err != nil {
		t.Fatal(err)
	}

	f.now = t0.Add(engine.KeyWindow) // before the sweeper deletes the rows
	f.begin(t, key("k1"), fp("b"))
	_, replayed, err := f.bulk("k2", fp("other"), f.now)
	if err != nil || replayed {
		t.Fatalf("bulk key of 24 hours: replayed %v, code %v; want a new key row", replayed, calyxerr.CodeOf(err))
	}
}

func TestSendRecordsGuardAfterASentRow(t *testing.T) {
	f := newRecordsFixture(t)
	first := f.begin(t, nil, fp("a"))
	if err := f.records.MarkSent(context.Background(), first.RecordID, "wamid.1"); err != nil {
		t.Fatal(err)
	}

	f.now = t0.Add(9*time.Minute + 59*time.Second)
	err := f.refuse(t, key("new"), fp("a")) // a new key does not pass the guard
	refusal(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_DUPLICATE_MESSAGE,
		"This instance sent the same message to this recipient at "+t0UTC+". "+spamRule,
		map[string]string{"at": t0UTC, "message_id": "wamid.1"})

	f.now = t0.Add(10*time.Minute + time.Second)
	f.begin(t, nil, fp("a"))
}

func TestSendRecordsGuardAfterAnUnknownRow(t *testing.T) {
	f := newRecordsFixture(t)
	first := f.begin(t, nil, fp("a"))
	if err := f.records.MarkUnknown(context.Background(), first.RecordID); err != nil {
		t.Fatal(err)
	}
	f.now = t0.Add(5 * time.Minute)
	err := f.refuse(t, nil, fp("a"))
	refusal(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_DUPLICATE_MESSAGE,
		"The outcome of the same message to this recipient at "+t0UTC+" is unknown. "+spamRule,
		map[string]string{"at": t0UTC})
}

func TestSendRecordsGuardRefusesWhileAnOldRowIsPending(t *testing.T) {
	f := newRecordsFixture(t)
	f.begin(t, nil, fp("a"))
	f.now = t0.Add(15 * time.Minute)
	err := f.refuse(t, nil, fp("a"))
	refusal(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_DUPLICATE_MESSAGE,
		"This instance is sending the same message to this recipient now. "+spamRule,
		map[string]string{"at": t0UTC})
}

func TestSendRecordsGuardIsPerInstance(t *testing.T) {
	f := newRecordsFixture(t)
	f.begin(t, nil, fp("a"))
	other := testutil.CreateInstance(t, f.db, "other")
	if _, err := f.records.BeginSingle(context.Background(), other, nil, fp("a")); err != nil {
		t.Fatalf("other instance: code %v, reason %v", calyxerr.CodeOf(err), reasonOf(err))
	}
}

func TestSendRecordsRecoverTurnsOlderPendingRowsIntoUnknown(t *testing.T) {
	f := newRecordsFixture(t)
	old := f.begin(t, nil, fp("a"))
	f.now = t0.Add(2 * time.Hour)
	recent := f.begin(t, nil, fp("b"))

	f.now = t0.Add(3 * time.Hour)
	if err := f.records.Recover(context.Background(), t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if status, finishedAt := f.status(t, old.RecordID); status != "UNKNOWN" || finishedAt.Int64 != f.now.UnixMilli() {
		t.Errorf("older row: %s at %v, want UNKNOWN at now", status, finishedAt)
	}
	if status, finishedAt := f.status(t, recent.RecordID); status != "PENDING" || finishedAt.Valid {
		t.Errorf("newer row: %s at %v, want PENDING", status, finishedAt)
	}
}

func TestSendRecordsSweepDeletesFinishedRowsOf24Hours(t *testing.T) {
	f := newRecordsFixture(t)
	ctx := context.Background()
	// One SENT, one UNKNOWN and one CREATED row at t0, and the same at t0 + 1 hour.
	for i, created := range []time.Time{t0, t0.Add(time.Hour)} {
		f.now = created
		sent := f.begin(t, nil, fp("sent"+created.String()))
		if err := f.records.MarkSent(ctx, sent.RecordID, "wamid"); err != nil {
			t.Fatal(err)
		}
		unknown := f.begin(t, nil, fp("unknown"+created.String()))
		if err := f.records.MarkUnknown(ctx, unknown.RecordID); err != nil {
			t.Fatal(err)
		}
		if _, _, err := f.bulk("k"+string(rune('0'+i)), fp("bulk"), created); err != nil {
			t.Fatal(err)
		}
	}
	f.now = t0.Add(engine.KeyWindow)
	if err := f.records.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	var oldLeft, newLeft int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM send_records WHERE created_at = ?", t0.UnixMilli()).Scan(&oldLeft); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow("SELECT COUNT(*) FROM send_records WHERE created_at = ?", t0.Add(time.Hour).UnixMilli()).Scan(&newLeft); err != nil {
		t.Fatal(err)
	}
	if oldLeft != 0 || newLeft != 3 {
		t.Fatalf("rows of 24 hours left: %d (want 0); rows of 23 hours left: %d (want 3)", oldLeft, newLeft)
	}
}

func TestSendRecordsGoAwayWithTheInstance(t *testing.T) {
	f := newRecordsFixture(t)
	f.begin(t, key("k1"), fp("a"))
	if _, err := f.db.Exec("DELETE FROM instances WHERE id = ?", f.instanceID); err != nil {
		t.Fatal(err)
	}
	if n := f.count(t); n != 0 {
		t.Fatalf("%d rows left after the instance was removed", n)
	}
}

func TestSendRecordsAcceptOneOfTwoConcurrentChecks(t *testing.T) {
	f := newRecordsFixture(t)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.records.BeginSingle(context.Background(), f.instanceID, nil, fp("a"))
		}()
	}
	wg.Wait()
	accepted := 0
	for _, err := range errs {
		switch {
		case err == nil:
			accepted++
		case reasonOf(err) != corev1.ErrorReason_ERROR_REASON_DUPLICATE_MESSAGE:
			t.Errorf("unexpected refusal: code %v, reason %v", calyxerr.CodeOf(err), reasonOf(err))
		}
	}
	if accepted != 1 {
		t.Fatalf("%d checks accepted, want 1", accepted)
	}
}

func TestCheckKeyFormat(t *testing.T) {
	const message = "The idempotency key must have 1 to 255 printable ASCII characters."
	valid := []*string{nil, key("a"), key("!~"), key(strings.Repeat("x", 255)), key("550e8400-e29b-41d4-a716-446655440000")}
	for i, k := range valid {
		if err := engine.CheckKeyFormat(k); err != nil {
			t.Errorf("valid case %d: code %v", i, calyxerr.CodeOf(err))
		}
	}
	invalid := []*string{key(""), key(strings.Repeat("x", 256)), key("a b"), key("a\tb"), key("clé"), key("a\x7f")}
	for i, k := range invalid {
		err := engine.CheckKeyFormat(k)
		var e *calyxerr.Error
		if !errors.As(err, &e) || e.Code != codes.InvalidArgument || e.Message != message {
			t.Errorf("invalid case %d: code %v", i, calyxerr.CodeOf(err))
		}
	}
}

func (f *recordsFixture) lookup(t *testing.T, k string, fingerprint []byte) (engine.SingleCheck, bool, error) {
	t.Helper()
	return f.records.LookupSingle(context.Background(), f.instanceID, k, fingerprint)
}

func TestLookupSingleUnknownKeyIsNotKnown(t *testing.T) {
	f := newRecordsFixture(t)
	check, known, err := f.lookup(t, "k1", fp("a"))
	if err != nil || known || check != (engine.SingleCheck{}) {
		t.Fatalf("got %+v known=%v err=%v, want an empty answer", check, known, err)
	}
	if n := f.count(t); n != 0 {
		t.Fatalf("%d rows after a lookup, want 0", n)
	}
}

func TestLookupSingleGivesTheAnswerOfBeginSingle(t *testing.T) {
	ctx := context.Background()
	for _, status := range []string{"PENDING", "UNKNOWN", "SENT"} {
		t.Run(status, func(t *testing.T) {
			f := newRecordsFixture(t)
			first := f.begin(t, key("k1"), fp("a"))
			switch status {
			case "UNKNOWN":
				if err := f.records.MarkUnknown(ctx, first.RecordID); err != nil {
					t.Fatal(err)
				}
			case "SENT":
				if err := f.records.MarkSent(ctx, first.RecordID, "wamid.1"); err != nil {
					t.Fatal(err)
				}
			}
			want, wantErr := f.records.BeginSingle(ctx, f.instanceID, key("k1"), fp("a"))
			got, known, err := f.lookup(t, "k1", fp("a"))
			if known != (err == nil) {
				t.Errorf("known = %v with err %v", known, err)
			}
			if got != want {
				t.Errorf("check %+v, want %+v", got, want)
			}
			if status == "PENDING" && got.PendingRecordID != first.RecordID {
				t.Errorf("PendingRecordID %d, want %d", got.PendingRecordID, first.RecordID)
			}
			if (err == nil) != (wantErr == nil) {
				t.Fatalf("err %v, want %v", err, wantErr)
			}
			if err != nil {
				var e, w *calyxerr.Error
				if !errors.As(err, &e) || !errors.As(wantErr, &w) {
					t.Fatalf("want *calyxerr.Error, got %T and %T", err, wantErr)
				}
				refusal(t, err, w.Code, w.Reason, w.Message, w.Metadata)
			}
			if n := f.count(t); n != 1 {
				t.Errorf("%d rows after a lookup, want 1", n)
			}
		})
	}
}

func TestLookupSingleWithAnotherFingerprintIsAConflict(t *testing.T) {
	f := newRecordsFixture(t)
	f.begin(t, key("k1"), fp("a"))
	_, known, err := f.lookup(t, "k1", fp("b"))
	if known {
		t.Error("known = true with an error, want false")
	}
	refusal(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT, conflictMessage, nil)
}

func TestLookupSingleKeyOlderThan24HoursIsNotKnown(t *testing.T) {
	f := newRecordsFixture(t)
	first := f.begin(t, key("k1"), fp("a"))
	if err := f.records.MarkSent(context.Background(), first.RecordID, "wamid.1"); err != nil {
		t.Fatal(err)
	}
	f.now = t0.Add(engine.KeyWindow)
	check, known, err := f.lookup(t, "k1", fp("a"))
	if err != nil || known || check != (engine.SingleCheck{}) {
		t.Fatalf("got %+v known=%v err=%v, want not known", check, known, err)
	}
	var isNull bool
	if err := f.db.QueryRow("SELECT idempotency_key IS NULL FROM send_records WHERE id = ?", first.RecordID).Scan(&isNull); err != nil || !isNull {
		t.Fatalf("key is NULL = %v (err %v), want true", isNull, err)
	}
}
