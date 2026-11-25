package engine_test

// A stress test of DestroyAllSessions in the middle of a round. The random calls of
// orchestrator_invariants_test.go, plus acquires, loads and wakes, run against one instance
// while the sweeper and the wake loop run. At a random time the round calls DestroyAllSessions,
// and the calls go on after it. Then the test formally checks that
// nothing is live after the shutdown, in every state after the shutdown and at the end.

import (
	"context"
	"fmt"
	"math/rand/v2"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"calyx/core/internal/engine"
	"calyx/core/internal/events"
	"calyx/core/internal/testutil"
)

const (
	shutdownRounds      = 10
	shutdownRoundLength = 150 * time.Millisecond
	// How long the calls go on after DestroyAllSessions returned.
	shutdownTail = 50 * time.Millisecond
	// The limit for DestroyAllSessions and for each call.
	shutdownCallLimit = 5 * time.Second
)

type shutdownRound struct {
	seed           uint64
	shutdownStuck  bool
	stuckCalls     int64
	maxConnections int
	samples        int      // states checked after the shutdown
	liveAfter      []string // NothingLiveAfterShutdown failures, first ones only
	deletedAfter   []string // rows that existed when the shutdown returned and are gone
	logoutsAfter   int
}

func (r *shutdownRound) liveProblem(format string, args ...any) {
	if len(r.liveAfter) < 5 {
		r.liveAfter = append(r.liveAfter, fmt.Sprintf(format, args...))
	}
}

// outboxLoops counts the goroutines that run an outbox loop. Other tests of the package can have
// loops too, so a round compares the count with the one at its start.
func outboxLoops() int {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	loops := 0
	for g := range strings.SplitSeq(string(buf), "\n\n") {
		if strings.Contains(g, "engine.(*Outbox).run(") {
			loops++
		}
	}
	return loops
}

func runShutdownRound(t *testing.T, seed uint64) *shutdownRound {
	t.Helper()
	r := &shutdownRound{seed: seed}
	rng := rand.New(rand.NewPCG(seed, seed))
	baseLoops := outboxLoops()
	db := testutil.NewDB(t)
	counter := testutil.NewConnectionCounter()
	var fakesMu sync.Mutex
	var fakes []*testutil.FakeProvider
	o := engine.NewOrchestrator(db, engine.NewBulkStore(db), events.NewBus(), engine.OrchestratorOptions{
		NormalizeRecipient: testutil.NormalizeRecipient,
		BuildProvider: func(row engine.InstanceRow, hooks engine.ProviderHooks) (engine.Provider, error) {
			fake := testutil.NewFakeProvider(row.Name)
			fake.SetHooks(hooks)
			fake.CountConnections(counter)
			fakesMu.Lock()
			fakes = append(fakes, fake)
			fakesMu.Unlock()
			return fake, nil
		},
		SendOnlyIdle: 20 * time.Millisecond,
	})
	testutil.CreateInstance(t, db, "shop")
	_, _ = db.Exec("UPDATE instances SET is_used_only_for_send_messages = 1")

	// The rows that the round itself put in the table. Only a removal may delete them.
	var ownRows sync.Map
	var firstID string
	if err := db.QueryRow("SELECT id FROM instances WHERE name = 'shop'").Scan(&firstID); err != nil {
		t.Fatal(err)
	}
	ownRows.Store(firstID, true)

	sweepTick := time.NewTicker(5 * time.Millisecond)
	defer sweepTick.Stop()
	engine.SetSweepTick(o, sweepTick.C)
	engine.SetWakeTimer(o, func(time.Duration) <-chan time.Time { return time.After(2 * time.Millisecond) })
	engine.SetWakeLimit(o, 100*time.Millisecond)
	if err := o.StartSendRecords(context.Background()); err != nil {
		t.Fatal(err)
	}
	o.StartWakeRetry()

	var calls sync.WaitGroup
	var stuck atomic.Int64
	call := func(fn func(ctx context.Context) error) {
		calls.Add(1)
		go func() {
			defer calls.Done()
			ctx, cancel := context.WithTimeout(context.Background(), shutdownCallLimit)
			defer cancel()
			done := make(chan struct{})
			go func() { _ = fn(ctx); close(done) }()
			select {
			case <-done:
			case <-time.After(shutdownCallLimit):
				stuck.Add(1)
			}
		}()
	}

	// Known gap (r3-review.md, concern 1): a pairing is not refused when it starts after the
	// shutdown. Its provider opens a connection, and register then refuses and closes it. The
	// model has no pairings, so a state check of the connections skips the states where a pairing
	// runs. The final check, after every call returned, counts every connection.
	var pairingsStarted, pairingsRunning atomic.Int64
	hooks := lifecycleHooks{
		pairing: func(run func() error) error {
			pairingsStarted.Add(1)
			pairingsRunning.Add(1)
			defer pairingsRunning.Add(-1)
			return run()
		},
		inserted: func(id string) { ownRows.Store(id, true) },
	}

	rowIDs := func() map[string]bool {
		ids := map[string]bool{}
		rows, err := db.Query("SELECT id FROM instances")
		if err != nil {
			return ids
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				ids[id] = true
			}
		}
		return ids
	}
	logouts := func() int {
		fakesMu.Lock()
		defer fakesMu.Unlock()
		n := 0
		for _, f := range fakes {
			_, _, l := f.Counts()
			n += l
		}
		return n
	}

	// The state that NothingLiveAfterShutdown checks.
	check := func() {
		r.samples++
		if live, timers := engine.LiveState(o); live != 0 || timers != 0 {
			r.liveProblem("%d live instances, %d armed sleep timers", live, timers)
		}
		if loops := outboxLoops() - baseLoops; loops > 0 {
			r.liveProblem("%d outbox loops run", loops)
		}
	}

	var rowsAtShutdown map[string]bool
	var logoutsAtShutdown int
	shutdownDone := make(chan struct{})
	shutdownAt := time.Now().Add(shutdownRoundLength/5 + time.Duration(rng.Int64N(int64(shutdownRoundLength/2))))
	started := false
	var tailEnd time.Time

	next := 0
	for {
		now := time.Now()
		if !started && now.After(shutdownAt) {
			started = true
			go func() {
				o.DestroyAllSessions()
				// Right after the return: a removal that ends later must not delete anything.
				rowsAtShutdown, logoutsAtShutdown = rowIDs(), logouts()
				close(shutdownDone)
			}()
		}
		if started {
			select {
			case <-shutdownDone:
				if tailEnd.IsZero() {
					tailEnd = now.Add(shutdownTail)
				}
				check()
				// No pairing ran from the first read to the last one, so every connection counts.
				before := pairingsStarted.Load()
				idle := pairingsRunning.Load() == 0
				open := counter.Open("shop")
				if idle && pairingsStarted.Load() == before && open != 0 {
					r.liveProblem("%d connections open", open)
				}
			default:
				if now.After(shutdownAt.Add(shutdownCallLimit)) {
					r.shutdownStuck = true
					return r // DestroyAllSessions does not return: nothing more to check.
				}
			}
			if !tailEnd.IsZero() && now.After(tailEnd) {
				break
			}
		}
		next++
		op := randomLifecycleOp(rng, o, db, fmt.Sprintf("3460%07d", next), hooks)
		if extra := rng.IntN(100); extra < 15 {
			op = extraLifecycleOp(extra, o)
		}
		if op != nil {
			call(op)
		}
		time.Sleep(time.Duration(rng.IntN(6)) * time.Millisecond)
	}
	calls.Wait()

	// Every call returned. An outbox loop can need a moment to return after it closed its done
	// channel, so the last check waits up to 1 s for that.
	var final shutdownRound
	for range 100 {
		final = shutdownRound{}
		live, timers := engine.LiveState(o)
		if live != 0 || timers != 0 {
			final.liveProblem("at the end: %d live instances, %d armed sleep timers", live, timers)
		}
		if loops := outboxLoops() - baseLoops; loops > 0 {
			final.liveProblem("at the end: %d outbox loops run", loops)
		}
		if open := counter.Open("shop"); open != 0 {
			final.liveProblem("at the end: %d connections open", open)
		}
		if len(final.liveAfter) == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, p := range final.liveAfter {
		r.liveProblem("%s", p)
	}

	now := rowIDs()
	for id := range rowsAtShutdown {
		if now[id] {
			continue
		}
		// Known gap (r3-review.md, concern 1): a pairing that ends after the shutdown still writes
		// to the database. A failed pairing deletes the new row that it created. Only the rows
		// that the round inserted are checked.
		if _, own := ownRows.Load(id); own {
			r.deletedAfter = append(r.deletedAfter, id)
		}
	}
	r.logoutsAfter = logouts() - logoutsAtShutdown
	r.stuckCalls = stuck.Load()
	r.maxConnections = counter.Max("shop")
	return r
}

// extraLifecycleOp is a call that randomLifecycleOp does not make: an acquire that holds the
// instance for some time, a load of every active instance, or a wake.
func extraLifecycleOp(pick int, o *engine.Orchestrator) func(ctx context.Context) error {
	switch {
	case pick < 6:
		return func(ctx context.Context) error {
			release, err := engine.Acquire(o, "shop")
			if err != nil {
				return err
			}
			time.Sleep(3 * time.Millisecond)
			release()
			return nil
		}
	case pick < 10:
		return func(ctx context.Context) error { o.LoadAllActiveInstances(ctx); return nil }
	default:
		return func(ctx context.Context) error { return engine.AcquireWithin(o, "shop", 50*time.Millisecond) }
	}
}

func TestOrchestratorShutdownMidRound(t *testing.T) {
	base := uint64(time.Now().UnixNano())
	var rounds []*shutdownRound
	for i := range uint64(shutdownRounds) {
		rounds = append(rounds, runShutdownRound(t, base+i))
	}

	t.Run("ShutdownReturns", func(t *testing.T) {
		for _, r := range rounds {
			if r.shutdownStuck {
				t.Errorf("seed %d: DestroyAllSessions did not return within %v", r.seed, shutdownCallLimit)
			}
		}
	})
	t.Run("EveryCallReturns", func(t *testing.T) {
		for _, r := range rounds {
			if r.stuckCalls > 0 {
				t.Errorf("seed %d: %d calls did not return within %v", r.seed, r.stuckCalls, shutdownCallLimit)
			}
		}
	})
	// The model: DownDone => live = 0 /\ Connections = 0 /\ no outbox runs or sends /\ no timer.
	t.Run("NothingLiveAfterShutdown", func(t *testing.T) {
		for _, r := range rounds {
			for _, p := range r.liveAfter {
				t.Errorf("seed %d: after the shutdown: %s", r.seed, p)
			}
			if !r.shutdownStuck && r.samples == 0 {
				t.Errorf("seed %d: no state was checked after the shutdown", r.seed)
			}
		}
	})
	// A removal that did not publish its close before the shutdown stops before its logout
	// and its DELETE.
	t.Run("NoRemovalAfterShutdown", func(t *testing.T) {
		for _, r := range rounds {
			if len(r.deletedAfter) > 0 || r.logoutsAfter > 0 {
				t.Errorf("seed %d: after the shutdown, %d rows deleted and %d logouts", r.seed, len(r.deletedAfter), r.logoutsAfter)
			}
		}
	})
	t.Run("AtMostOneConnection", func(t *testing.T) {
		for _, r := range rounds {
			if r.maxConnections > 1 {
				t.Errorf("seed %d: %d connections open at once", r.seed, r.maxConnections)
			}
		}
	})
}
