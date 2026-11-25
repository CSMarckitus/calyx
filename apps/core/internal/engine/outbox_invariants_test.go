package engine_test

// Stress tests that formally check the invariants of the outbox. Each round fires random
// operations at one outbox, concurrently: direct sends (some with short deadlines), new bulk
// jobs, cancels, connection drops and restarts of the outbox. Every invariant is then checked on
// what happened in every round.

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"calyx/core/internal/engine"
	"calyx/core/internal/testutil"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

const (
	invariantRounds = 30
	roundLength     = 300 * time.Millisecond
)

type cancelRecord struct {
	jobID string
	at    time.Time
}

type outboxRound struct {
	seed       uint64
	minGap     time.Duration
	sent       []testutil.SentMessage
	jobs       map[string][]string // job id -> its recipients
	cancels    []cancelRecord
	issued     int64 // direct sends started
	returned   int64 // direct sends that returned
	jobsAtEnd  map[string]*engine.BulkJob
	leftOpen   map[string]int // job id -> PENDING or SENDING messages of a finished job
	finishedOK bool           // every job finished before the deadline
	// Direct recipient -> every outcome its onDone got. Complete once every outbox stopped.
	outcomes map[string][]engine.SendOutcome
}

func runOutboxRound(t *testing.T, seed uint64) *outboxRound {
	t.Helper()
	rng := rand.New(rand.NewPCG(seed, seed))
	db := testutil.NewDB(t)
	store := engine.NewBulkStore(db)
	fake := testutil.NewFakeProvider("shop")
	instanceID := testutil.CreateInstance(t, db, "shop")
	r := &outboxRound{seed: seed, minGap: time.Duration(fake.Pacing().MinGapMs) * time.Millisecond, jobs: map[string][]string{}, outcomes: map[string][]engine.SendOutcome{}}
	var outcomesMu sync.Mutex // guards r.outcomes: onDone runs on the loop goroutines

	var mu sync.Mutex // guards outbox and r.jobs / r.cancels
	// A rebuilt outbox gets the last send of the previous one, as Orchestrator.register does.
	newOutbox := func(lastSentAt time.Time) *engine.Outbox {
		o := engine.NewOutbox(fake, store, instanceID, engine.OutboxOptions{ReconnectCheck: 20 * time.Millisecond, LastSentAt: lastSentAt})
		o.Start()
		return o
	}
	outbox := newOutbox(time.Time{})
	current := func() *engine.Outbox { mu.Lock(); defer mu.Unlock(); return outbox }

	var callers sync.WaitGroup
	next := 0
	deadline := time.Now().Add(roundLength)
	for time.Now().Before(deadline) {
		switch op := rng.IntN(100); {
		case op < 40: // a direct send, sometimes with a caller that gives up quickly
			atomic.AddInt64(&r.issued, 1)
			next++
			recipient := fmt.Sprintf("3469%07d", next)
			timeout := time.Duration(rng.IntN(300)) * time.Millisecond
			o := current()
			outcomesMu.Lock()
			r.outcomes[recipient] = nil
			outcomesMu.Unlock()
			onDone := func(outcome engine.SendOutcome) {
				outcomesMu.Lock()
				defer outcomesMu.Unlock()
				r.outcomes[recipient] = append(r.outcomes[recipient], outcome)
			}
			callers.Add(1)
			go func() {
				defer callers.Done()
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				_, _ = o.Send(ctx, recipient, "direct", 0, onDone)
				atomic.AddInt64(&r.returned, 1)
			}()
		case op < 60: // a new bulk job with recipients nobody else uses
			messages := make([]engine.BulkMessageInput, 2+rng.IntN(4))
			recipients := make([]string, len(messages))
			for i := range messages {
				next++
				recipients[i] = fmt.Sprintf("3460%07d", next)
				messages[i] = engine.BulkMessageInput{To: recipients[i], Text: "bulk"}
			}
			plan, ms, err := engine.PlanBulk(messages, engine.BulkPacingOptions{}, fake.Pacing())
			if err != nil {
				t.Fatal(err)
			}
			job, _, err := store.Create(context.Background(), instanceID, plan, ms, nil)
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			r.jobs[job.ID] = recipients
			mu.Unlock()
			current().NotifyNewJob()
		case op < 72: // cancel a random job, as Orchestrator.CancelBulkJob does
			mu.Lock()
			var ids []string
			for id := range r.jobs {
				ids = append(ids, id)
			}
			mu.Unlock()
			if len(ids) == 0 {
				continue
			}
			id := ids[rng.IntN(len(ids))]
			at := time.Now()
			if finished, _ := store.Finish(context.Background(), id, engine.JobCancelled, ""); finished != nil {
				mu.Lock()
				r.cancels = append(r.cancels, cancelRecord{jobID: id, at: at})
				mu.Unlock()
				current().NotifyJobCancelled(id)
			}
		case op < 84: // the connection drops or comes back
			if rng.IntN(2) == 0 {
				fake.ForceStatus(corev1.InstanceStatus_INSTANCE_STATUS_DISCONNECTED)
			} else {
				fake.ForceStatus(corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED)
			}
		case op < 90: // the instance is rebuilt: a new outbox on the same database
			mu.Lock()
			old := outbox
			mu.Unlock()
			old.Stop()
			mu.Lock()
			outbox = newOutbox(old.LastSentAt())
			mu.Unlock()
		}
		time.Sleep(time.Duration(rng.IntN(15)) * time.Millisecond)
	}

	// Let everything finish with a stable connection.
	fake.ForceStatus(corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED)
	current().NotifyNewJob()
	callers.Wait()
	finishBy := time.Now().Add(10 * time.Second)
	for {
		if next, _ := store.NextUnfinished(context.Background(), instanceID); next == nil {
			r.finishedOK = true
			break
		}
		if time.Now().After(finishBy) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	current().Stop()

	r.sent = fake.Sent()
	r.jobsAtEnd = map[string]*engine.BulkJob{}
	r.leftOpen = map[string]int{}
	for id := range r.jobs {
		job, err := store.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		r.jobsAtEnd[id] = job
		var open int
		_ = db.QueryRow("SELECT COUNT(*) FROM bulk_job_messages WHERE job_id = ? AND status IN ('PENDING', 'SENDING')", id).Scan(&open)
		r.leftOpen[id] = open
	}
	return r
}

func TestOutboxInvariants(t *testing.T) {
	base := uint64(time.Now().UnixNano())
	var rounds []*outboxRound
	for i := range uint64(invariantRounds) {
		rounds = append(rounds, runOutboxRound(t, base+i))
	}
	failed := func(t *testing.T, r *outboxRound, format string, args ...any) {
		t.Helper()
		t.Errorf("seed %d: "+format, append([]any{r.seed}, args...)...)
	}

	t.Run("PacingRespected", func(t *testing.T) {
		for _, r := range rounds {
			for i := 1; i < len(r.sent); i++ {
				if gap := r.sent[i].At.Sub(r.sent[i-1].At); gap < r.minGap {
					failed(t, r, "sends %d and %d only %v apart", i-1, i, gap)
				}
			}
		}
	})

	t.Run("AtMostOnce", func(t *testing.T) {
		for _, r := range rounds {
			seen := map[string]bool{}
			for _, m := range r.sent {
				if seen[m.Recipient] {
					failed(t, r, "%s received twice", m.Recipient)
				}
				seen[m.Recipient] = true
			}
		}
	})

	t.Run("NothingAfterCancel", func(t *testing.T) {
		for _, r := range rounds {
			for _, c := range r.cancels {
				after := 0
				for _, recipient := range r.jobs[c.jobID] {
					for _, m := range r.sent {
						if m.Recipient == recipient && m.At.After(c.at) {
							after++
						}
					}
				}
				if after > 1 {
					failed(t, r, "job %s sent %d messages after its cancel", c.jobID, after)
				}
			}
		}
	})

	t.Run("SentMeansLogged", func(t *testing.T) {
		for _, r := range rounds {
			for id, recipients := range r.jobs {
				really := 0
				for _, recipient := range recipients {
					for _, m := range r.sent {
						if m.Recipient == recipient {
							really++
						}
					}
				}
				if job := r.jobsAtEnd[id]; job.SentMessages != really {
					failed(t, r, "job %s counts %d sent, the provider got %d", id, job.SentMessages, really)
				}
			}
		}
	})

	t.Run("FinishedIsFinal", func(t *testing.T) {
		for _, r := range rounds {
			if !r.finishedOK {
				failed(t, r, "jobs still unfinished 10 s after the round")
			}
			for id, job := range r.jobsAtEnd {
				finished := job.Status == engine.JobCompleted || job.Status == engine.JobCancelled || job.Status == engine.JobFailed
				if finished && r.leftOpen[id] > 0 {
					failed(t, r, "job %s is %s with %d messages PENDING or SENDING", id, job.Status, r.leftOpen[id])
				}
			}
		}
	})

	t.Run("OnDoneOncePerSend", func(t *testing.T) {
		for _, r := range rounds {
			for recipient, got := range r.outcomes {
				if len(got) != 1 {
					failed(t, r, "onDone of %s ran %d times", recipient, len(got))
				}
			}
		}
	})

	// The fake never fails a send, so a direct message is sent or not sent, never unknown.
	t.Run("OutcomeMatchesProvider", func(t *testing.T) {
		for _, r := range rounds {
			sent := map[string]bool{}
			for _, m := range r.sent {
				sent[m.Recipient] = true
			}
			for recipient, got := range r.outcomes {
				if len(got) != 1 {
					continue // reported by OnDoneOncePerSend
				}
				want := engine.OutcomeNotSent
				if sent[recipient] {
					want = engine.OutcomeSent
				}
				if got[0].Kind != want {
					failed(t, r, "outcome of %s is %v, want %v", recipient, got[0].Kind, want)
				}
			}
		}
	})

	t.Run("DirectsAreAnswered", func(t *testing.T) {
		for _, r := range rounds {
			if r.issued != r.returned {
				failed(t, r, "%d sends started, %d returned", r.issued, r.returned)
			}
		}
	})
}
