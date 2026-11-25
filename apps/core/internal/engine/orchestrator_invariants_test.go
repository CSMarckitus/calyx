package engine_test

// Stress tests that formally check the invariants of the instance lifecycle. Each round fires
// random calls at one instance concurrently: sends, bulk jobs, config changes that rebuild or
// disable it, removals (and re-creation of the row), while its idle timer (20 ms) puts it to
// sleep whenever it can.

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/engine"
	"calyx/core/internal/events"
	"calyx/core/internal/testutil"
)

type lifecycleRound struct {
	seed           uint64
	maxConnections int
	openAtEnd      int
	sendsAfterEnd  int
	sendsToClosed  int
	stuckCalls     int64
	disabledOpen   int // connections open for a disabled row when the calls end
	// Sends per instance row, in time order. A row removed and created again with the same name
	// is a different instance (a real one needs a new pairing), so pacing is checked per row.
	sent   map[string][]testutil.SentMessage
	minGap time.Duration
}

func runLifecycleRound(t *testing.T, seed uint64) *lifecycleRound {
	t.Helper()
	rng := rand.New(rand.NewPCG(seed, seed))
	db := testutil.NewDB(t)
	store := engine.NewBulkStore(db)
	counter := testutil.NewConnectionCounter()
	var fakesMu sync.Mutex
	var fakes []*testutil.FakeProvider
	rowOf := map[*testutil.FakeProvider]string{}
	o := engine.NewOrchestrator(db, store, events.NewBus(), engine.OrchestratorOptions{
		NormalizeRecipient: testutil.NormalizeRecipient,
		BuildProvider: func(row engine.InstanceRow, hooks engine.ProviderHooks) (engine.Provider, error) {
			fake := testutil.NewFakeProvider(row.Name)
			fake.SetHooks(hooks)
			fake.CountConnections(counter)
			fakesMu.Lock()
			fakes = append(fakes, fake)
			rowOf[fake] = row.ID
			fakesMu.Unlock()
			return fake, nil
		},
		SendOnlyIdle: 20 * time.Millisecond,
	})
	testutil.CreateInstance(t, db, "shop")
	_, _ = db.Exec("UPDATE instances SET is_used_only_for_send_messages = 1")

	var calls sync.WaitGroup
	var disabledOpen int
	var stuck atomic.Int64
	call := func(fn func(ctx context.Context) error) {
		calls.Add(1)
		go func() {
			defer calls.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan struct{})
			go func() { _ = fn(ctx); close(done) }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				stuck.Add(1)
			}
		}()
	}

	next := 0
	deadline := time.Now().Add(roundLength)
	for time.Now().Before(deadline) {
		next++
		if op := randomLifecycleOp(rng, o, db, fmt.Sprintf("3460%07d", next), lifecycleHooks{}); op != nil {
			call(op)
		}
		time.Sleep(time.Duration(rng.IntN(10)) * time.Millisecond)
	}
	calls.Wait()

	var enabled bool
	if err := db.QueryRow("SELECT is_enabled FROM instances WHERE name = 'shop'").Scan(&enabled); err == nil && !enabled {
		disabledOpen = counter.Open("shop")
	}

	// Finally remove it for good: nothing may stay open or keep sending.
	err := o.RemoveInstance(context.Background(), "shop")
	if err != nil && calyxerr.CodeOf(err) != codes.NotFound {
		t.Fatalf("seed %d: final remove: %v", seed, err)
	}
	countSent := func() int {
		fakesMu.Lock()
		defer fakesMu.Unlock()
		n := 0
		for _, f := range fakes {
			n += len(f.Sent())
		}
		return n
	}
	before := countSent()
	time.Sleep(100 * time.Millisecond)

	r := &lifecycleRound{
		seed:           seed,
		maxConnections: counter.Max("shop"),
		openAtEnd:      counter.Open("shop"),
		sendsAfterEnd:  countSent() - before,
		sendsToClosed:  counter.SendsAfterClose(),
		stuckCalls:     stuck.Load(),
		disabledOpen:   disabledOpen,
		minGap:         time.Duration(testutil.NewFakeProvider("x").Pacing().MinGapMs) * time.Millisecond,
	}
	r.sent = map[string][]testutil.SentMessage{}
	fakesMu.Lock()
	for _, f := range fakes {
		r.sent[rowOf[f]] = append(r.sent[rowOf[f]], f.Sent()...)
	}
	fakesMu.Unlock()
	for _, sent := range r.sent {
		slices.SortFunc(sent, func(a, b testutil.SentMessage) int { return a.At.Compare(b.At) })
	}
	o.DestroyAllSessions()
	return r
}

func TestOrchestratorInvariants(t *testing.T) {
	base := uint64(time.Now().UnixNano())
	var rounds []*lifecycleRound
	for i := range uint64(invariantRounds) {
		rounds = append(rounds, runLifecycleRound(t, base+i))
	}

	t.Run("AtMostOneConnection", func(t *testing.T) {
		for _, r := range rounds {
			if r.maxConnections > 1 {
				t.Errorf("seed %d: %d connections open at once", r.seed, r.maxConnections)
			}
		}
	})
	t.Run("NoUseAfterClose", func(t *testing.T) {
		for _, r := range rounds {
			if r.sendsToClosed > 0 {
				t.Errorf("seed %d: %d sends reached a closed provider", r.seed, r.sendsToClosed)
			}
		}
	})
	t.Run("RemovedIsGone", func(t *testing.T) {
		for _, r := range rounds {
			if r.openAtEnd != 0 || r.sendsAfterEnd != 0 {
				t.Errorf("seed %d: after removal %d connections open, %d messages sent", r.seed, r.openAtEnd, r.sendsAfterEnd)
			}
		}
	})
	t.Run("DisabledIsClosed", func(t *testing.T) {
		for _, r := range rounds {
			if r.disabledOpen != 0 {
				t.Errorf("seed %d: %d connections open for a disabled instance", r.seed, r.disabledOpen)
			}
		}
	})
	t.Run("EveryCallReturns", func(t *testing.T) {
		for _, r := range rounds {
			if r.stuckCalls > 0 {
				t.Errorf("seed %d: %d calls did not return within 5 s", r.seed, r.stuckCalls)
			}
		}
	})
	t.Run("PacingRespected", func(t *testing.T) {
		for _, r := range rounds {
			for row, sent := range r.sent {
				for i := 1; i < len(sent); i++ {
					if gap := sent[i].At.Sub(sent[i-1].At); gap < r.minGap {
						t.Errorf("seed %d: instance %s sent twice only %v apart, across rebuilds", r.seed, row, gap)
					}
				}
			}
		}
	})
}

// lifecycleHooks lets a round see some operations. A nil field is not called.
type lifecycleHooks struct {
	// Runs around a pairing (CreateInstance).
	pairing func(run func() error) error
	// Receives the id of each row that the round inserts itself.
	inserted func(id string)
}

// randomLifecycleOp picks one of the random calls of a round, or nil for a pause.
func randomLifecycleOp(rng *rand.Rand, o *engine.Orchestrator, db *sql.DB, recipient string, h lifecycleHooks) func(ctx context.Context) error {
	switch op := rng.IntN(100); {
	case op < 45:
		return func(ctx context.Context) error { _, err := o.SendText(ctx, "shop", recipient, "hi", nil); return err }
	case op < 55:
		return func(ctx context.Context) error {
			_, _, err := o.CreateBulkJob(ctx, "shop", engine.BulkCustom, bulkOf(recipient), engine.BulkPacingOptions{}, nil)
			return err
		}
	case op < 70: // switch between send-only and always connected: rebuilds the instance
		sendOnly := rng.IntN(2) == 0
		return func(ctx context.Context) error {
			return o.UpdateInstanceConfig(ctx, "shop", engine.UpdateInstanceParams{SendOnly: &sendOnly})
		}
	case op < 80:
		enabled := rng.IntN(3) > 0
		return func(ctx context.Context) error {
			return o.UpdateInstanceConfig(ctx, "shop", engine.UpdateInstanceParams{IsEnabled: &enabled})
		}
	case op < 84: // a re-pair of an instance that lost its session, against concurrent disables
		return func(ctx context.Context) error {
			pair := func() error {
				_, _ = db.ExecContext(ctx, "UPDATE instances SET connection_status = 'NEEDS_AUTH' WHERE name = 'shop' AND is_enabled = 1")
				return o.CreateInstance(ctx, engine.CreateInstanceParams{
					Name: "shop", Provider: "WHATSAPP_WEB", Pairing: engine.PairingMethod{QR: true}, Locale: "es-ES",
				}, func(engine.CreateInstanceUpdate) {})
			}
			if h.pairing != nil {
				return h.pairing(pair)
			}
			return pair()
		}
	case op < 86:
		return func(ctx context.Context) error { return o.RemoveInstance(ctx, "shop") }
	case op < 92: // the instance is created again after a removal
		return func(ctx context.Context) error {
			// One statement: two of these at once must not both try to insert (and
			// testutil.CreateInstance cannot be used here: t.Fatal outside the test goroutine).
			id, now := uuid.NewString(), time.Now().UnixMilli()
			res, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO instances
				(id, name, provider, connection_status, created_at, updated_at)
				VALUES (?, 'shop', 'WHATSAPP_WEB', 'CONNECTED', ?, ?)`, id, now, now)
			if err == nil && h.inserted != nil {
				if n, _ := res.RowsAffected(); n == 1 {
					h.inserted(id)
				}
			}
			return err
		}
	}
	return nil
}
