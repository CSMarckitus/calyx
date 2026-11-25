package instances_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/core"
	"calyx/backend/internal/instances"
	"calyx/backend/internal/testutil"
)

// fakeRun is the core call of a test session. The test pushes updates and ends the run.
type fakeRun struct {
	updates chan core.Update
	end     chan error
}

func newFakeRun() *fakeRun {
	return &fakeRun{updates: make(chan core.Update, 64), end: make(chan error, 1)}
}

func (r *fakeRun) run(ctx context.Context, onUpdate func(core.Update)) error {
	for {
		select {
		case u := <-r.updates:
			onUpdate(u)
		case err := <-r.end:
			// Deliver the updates that the test sent before it ended the run.
			for {
				select {
				case u := <-r.updates:
					onUpdate(u)
				default:
					return err
				}
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func next(t *testing.T, ch <-chan instances.PairingEvent) instances.PairingEvent {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("the channel is closed")
		}
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("no event within 2 s")
	}
	return instances.PairingEvent{}
}

func closed(t *testing.T, ch <-chan instances.PairingEvent) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("the channel did not close within 2 s")
		}
	}
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 2 s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type endRecorder struct {
	mu      sync.Mutex
	results []instances.PairingResult
}

func (e *endRecorder) onEnd(r instances.PairingResult) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.results = append(e.results, r)
}

func (e *endRecorder) all() []instances.PairingResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]instances.PairingResult(nil), e.results...)
}

func newPairings() (*instances.Pairings, *testutil.FakeClock) {
	clk := testutil.NewFakeClock(time.Now())
	return instances.NewPairings(clk, 3*time.Minute), clk
}

func TestSessionKeepsRunningAfterItsFollowerLeaves(t *testing.T) {
	p, _ := newPairings()
	r := newFakeRun()
	rec := &endRecorder{}
	id := uuid.New()
	ctxA, cancelA := context.WithCancel(context.Background())
	ch, err := p.Start(ctxA, id, orgID, clientID, "qr", r.run, rec.onEnd)
	if err != nil {
		t.Fatal(err)
	}
	r.updates <- core.Update{Status: "NEEDS_AUTH", PairingValue: "QR1"}
	if ev := next(t, ch); ev.Kind != "update" || *ev.Update.QRText != "QR1" {
		t.Fatalf("got %+v", ev)
	}
	cancelA()
	closed(t, ch)
	if !p.Running(id) {
		t.Fatal("the session stopped with its follower")
	}
	r.updates <- core.Update{Status: "NEEDS_AUTH", PairingValue: "QR2"}
	eventually(t, func() bool { u, ok := p.Latest(id); return ok && u.QRText != nil && *u.QRText == "QR2" })
	r.updates <- core.Update{Status: "CONNECTED"}
	r.end <- nil
	eventually(t, func() bool { return len(rec.all()) == 1 })
	if res := rec.all()[0]; !res.Connected {
		t.Fatalf("result %+v", res)
	}
	eventually(t, func() bool { return !p.Running(id) })
}

func TestNewFollowerGetsTheLatestUpdateFirst(t *testing.T) {
	p, _ := newPairings()
	r := newFakeRun()
	id := uuid.New()
	if _, err := p.Start(context.Background(), id, orgID, clientID, "qr", r.run, func(instances.PairingResult) {}); err != nil {
		t.Fatal(err)
	}
	r.updates <- core.Update{Status: "NEEDS_AUTH", PairingValue: "QR1"}
	r.updates <- core.Update{Status: "NEEDS_AUTH", PairingValue: "QR2"}
	eventually(t, func() bool { u, ok := p.Latest(id); return ok && *u.QRText == "QR2" })
	ch, _, ok := p.Follow(context.Background(), id)
	if !ok {
		t.Fatal("no session")
	}
	if ev := next(t, ch); *ev.Update.QRText != "QR2" {
		t.Fatalf("got %+v", ev)
	}
	r.updates <- core.Update{Status: "NEEDS_AUTH", PairingValue: "QR3"}
	if ev := next(t, ch); *ev.Update.QRText != "QR3" {
		t.Fatalf("got %+v", ev)
	}
}

func TestQRAndCodeGoToTheirOwnField(t *testing.T) {
	cases := []struct {
		mode         string
		qr, code     bool
		pairingValue string
	}{{"qr", true, false, "V"}, {"code", false, true, "ABCD1234"}, {"", false, false, "X"}}
	for _, c := range cases {
		t.Run("mode "+c.mode, func(t *testing.T) {
			p, _ := newPairings()
			r := newFakeRun()
			ch, err := p.Start(context.Background(), uuid.New(), orgID, clientID, c.mode, r.run, func(instances.PairingResult) {})
			if err != nil {
				t.Fatal(err)
			}
			r.updates <- core.Update{Status: "NEEDS_AUTH", PairingValue: c.pairingValue, Message: "m"}
			ev := next(t, ch)
			if (ev.Update.QRText != nil) != c.qr || (ev.Update.PairingCode != nil) != c.code {
				t.Fatalf("got %+v", ev.Update)
			}
			if c.qr && *ev.Update.QRText != c.pairingValue || c.code && *ev.Update.PairingCode != c.pairingValue {
				t.Fatalf("wrong value %+v", ev.Update)
			}
			if ev.Update.Status != "NEEDS_AUTH" || ev.Update.Message != "m" {
				t.Fatalf("got %+v", ev.Update)
			}
		})
	}
}

func TestDoneIsTheLastEventAndClosesEveryFollower(t *testing.T) {
	p, _ := newPairings()
	r := newFakeRun()
	id := uuid.New()
	a, _ := p.Start(context.Background(), id, orgID, clientID, "qr", r.run, func(instances.PairingResult) {})
	b, _, ok := p.Follow(context.Background(), id)
	if !ok {
		t.Fatal("no session")
	}
	r.updates <- core.Update{Status: "CONNECTED"}
	r.end <- nil
	for _, ch := range []<-chan instances.PairingEvent{a, b} {
		if ev := next(t, ch); ev.Kind != "update" || ev.Update.Status != "CONNECTED" {
			t.Fatalf("got %+v", ev)
		}
		if ev := next(t, ch); ev.Kind != "done" {
			t.Fatalf("got %+v", ev)
		}
		closed(t, ch)
	}
	if p.Running(id) {
		t.Fatal("still running")
	}
	if _, _, ok := p.Follow(context.Background(), id); ok {
		t.Fatal("Follow found a finished session")
	}
}

func waitTimers(t *testing.T, clk *testutil.FakeClock, n int) {
	t.Helper()
	eventually(t, func() bool { return clk.PendingTimers() >= n })
}

func TestExpiryAfterTheTimeout(t *testing.T) {
	p, clk := newPairings()
	r := newFakeRun()
	rec := &endRecorder{}
	ch, _ := p.Start(context.Background(), uuid.New(), orgID, clientID, "qr", r.run, rec.onEnd)
	waitTimers(t, clk, 1)
	clk.Advance(3 * time.Minute)
	ev := next(t, ch)
	if ev.Kind != "failed" || ev.FailCode != "pairing_expired" {
		t.Fatalf("got %+v", ev)
	}
	eventually(t, func() bool { return len(rec.all()) == 1 })
	if rec.all()[0].FailCode != "pairing_expired" {
		t.Fatalf("got %+v", rec.all()[0])
	}
}

func TestNoExpiryBeforeTheTimeout(t *testing.T) {
	p, clk := newPairings()
	r := newFakeRun()
	id := uuid.New()
	ch, _ := p.Start(context.Background(), id, orgID, clientID, "qr", r.run, func(instances.PairingResult) {})
	waitTimers(t, clk, 1)
	clk.Advance(3*time.Minute - time.Millisecond)
	select {
	case ev := <-ch:
		t.Fatalf("unexpected event %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
	if !p.Running(id) {
		t.Fatal("the session ended early")
	}
}

func TestCancelEndsTheSession(t *testing.T) {
	p, _ := newPairings()
	r := newFakeRun()
	rec := &endRecorder{}
	id := uuid.New()
	ch, _ := p.Start(context.Background(), id, orgID, clientID, "qr", r.run, rec.onEnd)
	if !p.Cancel(id) {
		t.Fatal("Cancel returned false")
	}
	if len(rec.all()) != 1 {
		t.Fatal("Cancel returned before onEnd ran")
	}
	if ev := next(t, ch); ev.Kind != "failed" || ev.FailCode != "pairing_cancelled" {
		t.Fatalf("got %+v", ev)
	}
	if p.Cancel(id) {
		t.Fatal("second Cancel returned true")
	}
}

func TestCoreErrorIsPairingFailedWithTheError(t *testing.T) {
	p, _ := newPairings()
	r := newFakeRun()
	rec := &endRecorder{}
	ch, _ := p.Start(context.Background(), uuid.New(), orgID, clientID, "code", r.run, rec.onEnd)
	r.end <- apierr.Invalid("bad phone")
	ev := next(t, ch)
	if ev.Kind != "failed" || ev.FailCode != "pairing_failed" || ev.Detail != "bad phone" {
		t.Fatalf("got %+v", ev)
	}
	res := rec.all()[0]
	if res.Err == nil || res.Err.Code != "invalid_request" || res.FailCode != "pairing_failed" || res.Detail != "bad phone" {
		t.Fatalf("got %+v", res)
	}
}

func TestStreamEndingWithoutConnectedIsAFailure(t *testing.T) {
	p, _ := newPairings()
	r := newFakeRun()
	rec := &endRecorder{}
	ch, _ := p.Start(context.Background(), uuid.New(), orgID, clientID, "qr", r.run, rec.onEnd)
	r.updates <- core.Update{Status: "NEEDS_AUTH", PairingValue: "Q"}
	r.end <- nil
	next(t, ch) // the update
	ev := next(t, ch)
	if ev.Kind != "failed" || ev.FailCode != "pairing_failed" {
		t.Fatalf("got %+v", ev)
	}
	if rec.all()[0].Err != nil {
		t.Fatal("Err should be nil")
	}
}

func TestSecondStartIsPairingInProgress(t *testing.T) {
	p, _ := newPairings()
	r := newFakeRun()
	id := uuid.New()
	if _, err := p.Start(context.Background(), id, orgID, clientID, "qr", r.run, func(instances.PairingResult) {}); err != nil {
		t.Fatal(err)
	}
	_, err := p.Start(context.Background(), id, orgID, clientID, "qr", newFakeRun().run, func(instances.PairingResult) {})
	ae, ok := apierr.As(err)
	if !ok || ae.Status != 409 || ae.Code != "pairing_in_progress" {
		t.Fatalf("got %v", err)
	}
	if !p.Running(id) {
		t.Fatal("the first session was affected")
	}
}

func TestASlowFollowerIsDroppedNotWaitedFor(t *testing.T) {
	p, _ := newPairings()
	r := newFakeRun()
	id := uuid.New()
	slow, _ := p.Start(context.Background(), id, orgID, clientID, "qr", r.run, func(instances.PairingResult) {})
	fast, _, _ := p.Follow(context.Background(), id)
	var n atomic.Int32
	go func() {
		for ev := range fast {
			if ev.Kind == "update" {
				n.Add(1)
			}
		}
	}()
	// The fast follower reads each update before the next one goes out, so it gets all 20
	// although the slow one never reads.
	for i := 1; i <= 20; i++ {
		r.updates <- core.Update{Status: "NEEDS_AUTH", PairingValue: "Q"}
		eventually(t, func() bool { return n.Load() == int32(i) })
	}
	// Only now the slow follower reads: its buffer holds 16 events, and then the channel is closed.
	count := 0
	for range slow {
		count++
	}
	if count != 16 {
		t.Fatalf("the slow follower got %d events, want 16", count)
	}
	if !p.Running(id) {
		t.Fatal("the session stopped")
	}
}

func TestCancelAllEndsEverySession(t *testing.T) {
	p, _ := newPairings()
	rec := &endRecorder{}
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for _, id := range ids {
		if _, err := p.Start(context.Background(), id, orgID, clientID, "qr", newFakeRun().run, rec.onEnd); err != nil {
			t.Fatal(err)
		}
	}
	p.CancelAll()
	res := rec.all()
	if len(res) != 3 {
		t.Fatalf("%d sessions ended", len(res))
	}
	for _, r := range res {
		if r.FailCode != "pairing_cancelled" {
			t.Fatalf("got %+v", r)
		}
	}
	for _, id := range ids {
		if p.Running(id) {
			t.Fatal("a session still runs")
		}
	}
}

func TestOnEndRunsBeforeTheFinalEvent(t *testing.T) {
	p, _ := newPairings()
	r := newFakeRun()
	var flag atomic.Bool
	ch, _ := p.Start(context.Background(), uuid.New(), orgID, clientID, "qr", r.run, func(instances.PairingResult) { flag.Store(true) })
	r.end <- errors.New("x")
	next(t, ch)
	if !flag.Load() {
		t.Fatal("onEnd had not run when the final event arrived")
	}
}

func TestFinalEventReachesAFollowerWithAFullBuffer(t *testing.T) {
	p, _ := newPairings()
	r := newFakeRun()
	id := uuid.New()
	slow, _ := p.Start(context.Background(), id, orgID, clientID, "qr", r.run, func(instances.PairingResult) {})
	for i := 0; i < 16; i++ { // fills the buffer exactly; the follower is not dropped
		r.updates <- core.Update{Status: "NEEDS_AUTH", PairingValue: "Q"}
	}
	eventually(t, func() bool { return len(slow) == 16 })
	r.end <- errors.New("stop")
	// The session has sent its final event when it has left the map (both happen under one lock).
	eventually(t, func() bool { return !p.Running(id) })
	var last instances.PairingEvent
	n := 0
	for ev := range slow {
		last = ev
		n++
	}
	if n != 16 {
		t.Fatalf("%d events, want 16 (15 updates and the final event)", n)
	}
	if last.Kind != "failed" {
		t.Fatalf("last event %+v", last)
	}
}

// The organization and the client that the sessions of these tests belong to.
var orgID, clientID = uuid.New(), uuid.New()

// failSession starts a session for id, sends one QR update and ends it with err.
func failSession(t *testing.T, p *instances.Pairings, id uuid.UUID, err error) {
	t.Helper()
	r := newFakeRun()
	ch, startErr := p.Start(context.Background(), id, orgID, clientID, "qr", r.run, func(instances.PairingResult) {})
	if startErr != nil {
		t.Fatal(startErr)
	}
	r.updates <- core.Update{Status: "NEEDS_AUTH", PairingValue: "QR1"}
	r.end <- err
	closed(t, ch)
}

// followKept follows id and returns the events until the channel closes.
func followKept(t *testing.T, p *instances.Pairings, id uuid.UUID) []instances.PairingEvent {
	t.Helper()
	ch, ended, ok := p.Follow(context.Background(), id)
	if !ok || !ended {
		t.Fatalf("Follow: ok %v, ended %v", ok, ended)
	}
	var evs []instances.PairingEvent
	for ev := range ch {
		evs = append(evs, ev)
	}
	return evs
}

func TestAFailureIsKeptFor5MinutesWithoutItsCode(t *testing.T) {
	p, clk := newPairings()
	id := uuid.New()
	failSession(t, p, id, apierr.Invalid("bad phone"))
	for _, wait := range []time.Duration{0, 5*time.Minute - time.Millisecond} {
		clk.Advance(wait)
		evs := followKept(t, p, id)
		if len(evs) != 1 || evs[0].Kind != "failed" || evs[0].FailCode != "pairing_failed" || evs[0].Detail != "bad phone" {
			t.Fatalf("after %v: events %+v", wait, evs)
		}
		if evs[0].Update.QRText != nil || evs[0].Update.PairingCode != nil {
			t.Fatalf("the kept failure holds a code: %+v", evs[0])
		}
		org, client, ok := p.Owner(id)
		if !ok || org != orgID || client != clientID {
			t.Fatalf("Owner = %v %v %v", org, client, ok)
		}
	}
	clk.Advance(time.Millisecond)
	if _, _, ok := p.Follow(context.Background(), id); ok {
		t.Fatal("Follow found the failure after 5 minutes")
	}
	if _, _, ok := p.Owner(id); ok {
		t.Fatal("Owner found the failure after 5 minutes")
	}
}

func TestOwnerOfARunningSession(t *testing.T) {
	p, _ := newPairings()
	id := uuid.New()
	if _, err := p.Start(context.Background(), id, orgID, clientID, "qr", newFakeRun().run, func(instances.PairingResult) {}); err != nil {
		t.Fatal(err)
	}
	org, client, ok := p.Owner(id)
	if !ok || org != orgID || client != clientID {
		t.Fatalf("Owner = %v %v %v", org, client, ok)
	}
	if _, ended, ok := p.Follow(context.Background(), id); !ok || ended {
		t.Fatalf("Follow: ok %v, ended %v", ok, ended)
	}
	if _, _, ok := p.Owner(uuid.New()); ok {
		t.Fatal("Owner found an unknown id")
	}
}

func TestTheEndsOfAPairing(t *testing.T) {
	cases := []struct {
		name string
		end  func(p *instances.Pairings, clk *testutil.FakeClock, id uuid.UUID, r *fakeRun)
		code string
	}{
		{"the codes end in the core", func(_ *instances.Pairings, _ *testutil.FakeClock, _ uuid.UUID, r *fakeRun) {
			r.end <- fmt.Errorf("%w: %w", core.ErrPairingExpired, apierr.New(503, "core_unavailable", "The core is not available."))
		}, "pairing_expired"},
		{"the time of the backend ends", func(_ *instances.Pairings, clk *testutil.FakeClock, _ uuid.UUID, _ *fakeRun) {
			clk.Advance(3 * time.Minute)
		}, "pairing_expired"},
		{"a person cancels", func(p *instances.Pairings, _ *testutil.FakeClock, id uuid.UUID, _ *fakeRun) {
			p.Cancel(id)
		}, "pairing_cancelled"},
		{"the server stops", func(p *instances.Pairings, _ *testutil.FakeClock, _ uuid.UUID, _ *fakeRun) {
			p.CancelAll()
		}, "pairing_cancelled"},
		{"another fault", func(_ *instances.Pairings, _ *testutil.FakeClock, _ uuid.UUID, r *fakeRun) {
			r.end <- status.Error(codes.Unavailable, "x")
		}, "pairing_failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, clk := newPairings()
			r := newFakeRun()
			rec := &endRecorder{}
			id := uuid.New()
			ch, err := p.Start(context.Background(), id, orgID, clientID, "qr", r.run, rec.onEnd)
			if err != nil {
				t.Fatal(err)
			}
			waitTimers(t, clk, 1)
			c.end(p, clk, id, r)
			if ev := next(t, ch); ev.Kind != "failed" || ev.FailCode != c.code {
				t.Fatalf("event %+v", ev)
			}
			eventually(t, func() bool { return len(rec.all()) == 1 })
			if got := rec.all()[0].FailCode; got != c.code {
				t.Fatalf("result %q", got)
			}
			if evs := followKept(t, p, id); len(evs) != 1 || evs[0].FailCode != c.code {
				t.Fatalf("kept %+v", evs)
			}
		})
	}
}

func TestANewSessionReplacesAKeptFailure(t *testing.T) {
	p, _ := newPairings()
	id := uuid.New()
	failSession(t, p, id, errors.New("x"))
	r := newFakeRun()
	ch, err := p.Start(context.Background(), id, orgID, clientID, "qr", r.run, func(instances.PairingResult) {})
	if err != nil {
		t.Fatalf("Start after a kept failure: %v", err)
	}
	if !p.Running(id) {
		t.Fatal("the new session does not run")
	}
	r.updates <- core.Update{Status: "NEEDS_AUTH", PairingValue: "QR2"}
	if ev := next(t, ch); ev.Kind != "update" || *ev.Update.QRText != "QR2" {
		t.Fatalf("event %+v", ev)
	}
	f, ended, ok := p.Follow(context.Background(), id)
	if !ok || ended {
		t.Fatalf("Follow: ok %v, ended %v", ok, ended)
	}
	if ev := next(t, f); ev.Kind != "update" || *ev.Update.QRText != "QR2" {
		t.Fatalf("event %+v", ev)
	}
}

func TestStartAndFollowRemoveTheOldEntries(t *testing.T) {
	for _, call := range []string{"Start", "Follow"} {
		t.Run(call, func(t *testing.T) {
			p, clk := newPairings()
			a, b := uuid.New(), uuid.New()
			failSession(t, p, a, errors.New("x"))
			clk.Advance(time.Minute)
			failSession(t, p, b, errors.New("x"))
			clk.Advance(4*time.Minute + time.Millisecond) // a is old, b is not
			switch call {
			case "Start":
				if _, err := p.Start(context.Background(), uuid.New(), orgID, clientID, "qr", newFakeRun().run, func(instances.PairingResult) {}); err != nil {
					t.Fatal(err)
				}
				if n := instances.PairingEntries(p); n != 2 {
					t.Fatalf("%d entries, want 2 (b and the new session)", n)
				}
			case "Follow":
				p.Follow(context.Background(), uuid.New())
				if n := instances.PairingEntries(p); n != 1 {
					t.Fatalf("%d entries, want 1 (b)", n)
				}
			}
		})
	}
}

func TestAnEndedSessionIsIgnored(t *testing.T) {
	p, _ := newPairings()
	id := uuid.New()
	failSession(t, p, id, errors.New("x"))
	if p.Running(id) {
		t.Fatal("Running is true")
	}
	if u, ok := p.Latest(id); ok {
		t.Fatalf("Latest = %+v", u)
	}
	if p.Cancel(id) {
		t.Fatal("Cancel is true")
	}
	done := make(chan struct{})
	go func() { p.CancelAll(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("CancelAll waits for an ended session")
	}
	if evs := followKept(t, p, id); len(evs) != 1 || evs[0].FailCode != "pairing_failed" {
		t.Fatalf("the failure changed: %+v", evs)
	}
}

func TestASuccessIsNotKept(t *testing.T) {
	p, _ := newPairings()
	r := newFakeRun()
	id := uuid.New()
	ch, _ := p.Start(context.Background(), id, orgID, clientID, "qr", r.run, func(instances.PairingResult) {})
	r.updates <- core.Update{Status: "CONNECTED"}
	r.end <- nil
	closed(t, ch)
	if _, _, ok := p.Owner(id); ok {
		t.Fatal("Owner found a session that connected")
	}
	if n := instances.PairingEntries(p); n != 0 {
		t.Fatalf("%d entries", n)
	}
}

// Create and Pair answer with the error of the core when the codes end before the first update.
func TestTheEndOfTheCodesKeepsTheErrorOfTheCore(t *testing.T) {
	p, _ := newPairings()
	r := newFakeRun()
	rec := &endRecorder{}
	ch, _ := p.Start(context.Background(), uuid.New(), orgID, clientID, "qr", r.run, rec.onEnd)
	r.end <- fmt.Errorf("%w: %w", core.ErrPairingExpired, apierr.New(503, "core_unavailable", "The core is not available."))
	next(t, ch)
	if res := rec.all()[0]; res.FailCode != "pairing_expired" || res.Err == nil || res.Err.Status != 503 {
		t.Fatalf("result %+v", res)
	}
}
