package engine_test

// A pairing must not open its session while an older session of the same device
// is still open.

import (
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/engine"
	"calyx/core/internal/testutil"
)

// The idle timer of a send-only instance takes it out of the registry and then closes it. A
// re-pair that starts in between sees no live instance, so it must wait for that close.
func TestAPairingWaitsForTheCloseOfTheLastSession(t *testing.T) {
	f, counter := countedFixture(t)
	f.instance(t, "shop", "is_used_only_for_send_messages = 1")
	destroying, proceed := make(chan struct{}), make(chan struct{})
	f.configure = func(fake *testutil.FakeProvider) {
		fake.CountConnections(counter)
		fake.BeforeDestroy(func() {
			select {
			case <-destroying: // a later close does not wait
			default:
				close(destroying)
				<-proceed
			}
		})
	}
	release, err := engine.Acquire(f.o, "shop")
	if err != nil {
		t.Fatal(err)
	}
	release() // the idle timer starts the close, which waits in Destroy
	<-destroying
	if engine.IsLive(f.o, "shop") {
		t.Fatal("the closing instance is still in the registry")
	}

	if _, err := f.db.Exec("UPDATE instances SET connection_status = 'NEEDS_AUTH' WHERE name = 'shop'"); err != nil {
		t.Fatal(err)
	}
	paired := make(chan error, 1)
	go func() {
		paired <- f.o.CreateInstance(ctx, newPairing("shop"), func(engine.CreateInstanceUpdate) {})
	}()
	select {
	case err := <-paired:
		t.Errorf("the pairing ended (err %v) before the last session was closed", err)
		close(proceed)
	case <-time.After(100 * time.Millisecond):
		close(proceed)
		if err := <-paired; err != nil {
			t.Fatal(err)
		}
	}
	if got := counter.Max("shop"); got != 1 {
		t.Errorf("%d sessions open at the same time, want 1", got)
	}
}

// A load that read the row before the re-pair wrote NEEDS_AUTH connects and registers after the
// pairing began. The pairing must wait for it and close it, as it closes a live instance.
func TestAPairingWaitsForALoadInFlight(t *testing.T) {
	f, counter := countedFixture(t)
	f.instance(t, "shop", "is_used_only_for_send_messages = 1")
	connecting, proceed := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.configure = func(fake *testutil.FakeProvider) {
		fake.CountConnections(counter)
		fake.BeforeConnect(func(context.Context) {
			once.Do(func() {
				close(connecting)
				<-proceed
			})
		})
	}
	loaded := make(chan error, 1)
	go func() { loaded <- engine.AcquireWithin(f.o, "shop", 0) }()
	<-connecting

	if _, err := f.db.Exec("UPDATE instances SET connection_status = 'NEEDS_AUTH' WHERE name = 'shop'"); err != nil {
		t.Fatal(err)
	}
	paired := make(chan error, 1)
	go func() {
		paired <- f.o.CreateInstance(ctx, newPairing("shop"), func(engine.CreateInstanceUpdate) {})
	}()
	select {
	case err := <-paired:
		t.Errorf("the pairing ended (err %v) before the load in flight ended", err)
		close(proceed)
	case <-time.After(100 * time.Millisecond):
		close(proceed)
		if err := <-paired; err != nil {
			t.Fatal(err)
		}
	}
	// The load ends before the pairing closes it, so the caller gets it or the pairing refusal.
	if err := <-loaded; err != nil && calyxerr.CodeOf(err) != codes.FailedPrecondition {
		t.Fatal(err)
	}
	if got := counter.Max("shop"); got != 1 {
		t.Errorf("%d sessions open at the same time, want 1", got)
	}
	f.o.DestroyAllSessions()
	if got := counter.Open("shop"); got != 0 {
		t.Errorf("%d sessions still open after DestroyAllSessions", got)
	}
}

// The re-pair waits for a removal that started before it. After the wait the row is gone, so
// the re-pair must stop with NotFound and open no session.
func TestAPairingStopsWhenARemovalDeletedTheRow(t *testing.T) {
	f, counter := countedFixture(t)
	f.instance(t, "shop", "")
	destroying, proceed := make(chan struct{}), make(chan struct{})
	f.configure = func(fake *testutil.FakeProvider) {
		fake.CountConnections(counter)
		fake.BeforeDestroy(func() {
			select {
			case <-destroying:
			default:
				close(destroying)
				<-proceed
			}
		})
	}
	f.o.LoadAllActiveInstances(ctx)
	removed := make(chan error, 1)
	go func() { removed <- f.o.RemoveInstance(ctx, "shop") }()
	<-destroying // the removal holds its close, and the row still exists

	if _, err := f.db.Exec("UPDATE instances SET connection_status = 'NEEDS_AUTH' WHERE name = 'shop'"); err != nil {
		t.Fatal(err)
	}
	// The re-pair picks its random identity after it read the row and before it waits.
	picked := make(chan struct{})
	f.mu.Lock()
	f.intn = func(int) int { close(picked); return 0 }
	f.mu.Unlock()
	paired := make(chan error, 1)
	go func() {
		paired <- f.o.CreateInstance(ctx, newPairing("shop"), func(engine.CreateInstanceUpdate) {})
	}()
	<-picked
	close(proceed)
	if err := <-removed; err != nil {
		t.Fatal(err)
	}
	if err := <-paired; calyxerr.CodeOf(err) != codes.NotFound {
		t.Errorf("the re-pair got %v, want NotFound", err)
	}
	if got := f.buildCount("shop"); got != 1 {
		t.Errorf("%d providers built, want 1: the re-pair opened a session", got)
	}
	if got := counter.Open("shop"); got != 0 {
		t.Errorf("%d sessions open, want 0", got)
	}
	var rows int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM instances WHERE name = 'shop'").Scan(&rows); err != nil || rows != 0 {
		t.Errorf("%d rows of shop (err %v), want 0", rows, err)
	}
}

// pairHeldUntil makes the Pair of every fake wait until release is closed. paired is closed when
// the first Pair starts.
func pairHeldUntil(f *orchestratorFixture, counter *testutil.ConnectionCounter, release <-chan struct{}) (paired chan struct{}) {
	paired = make(chan struct{})
	var once sync.Once
	f.configure = func(fake *testutil.FakeProvider) {
		fake.CountConnections(counter)
		fake.SetPair(func(context.Context, engine.PairingMethod, func(string)) error {
			once.Do(func() { close(paired) })
			<-release
			return nil
		})
	}
	return paired
}

// A removal checks the pairing flag, and later publishes its close. A re-pair that starts between
// the two passes its wait. The removal must not then delete the row under the pairing.
func TestARemovalDoesNotDeleteTheRowOfAPairingThatPassedItsWait(t *testing.T) {
	f, counter := countedFixture(t)
	f.instance(t, "shop", "connection_status = 'NEEDS_AUTH'")
	removalDone := make(chan struct{})
	pairing := pairHeldUntil(f, counter, removalDone)
	paired := make(chan error, 1)
	engine.SetRemovalHook(f.o, func() {
		go func() {
			paired <- f.o.CreateInstance(ctx, newPairing("shop"), func(engine.CreateInstanceUpdate) {})
		}()
		<-pairing // the re-pair passed its wait and wrote its row
	})

	removeErr := f.o.RemoveInstance(ctx, "shop")
	close(removalDone)
	pairErr := <-paired

	var rows int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM instances WHERE name = 'shop'").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if engine.IsLive(f.o, "shop") && rows == 0 {
		t.Errorf("a live instance has no row (removal: %v, pairing: %v)", removeErr, pairErr)
	}
	if calyxerr.CodeOf(removeErr) != codes.FailedPrecondition {
		t.Errorf("the removal got %v, want FailedPrecondition", removeErr)
	}
	if pairErr != nil {
		t.Errorf("the pairing got %v, want nil", pairErr)
	}
}

// Second guard: when the row is gone at the end of the pairing, the pairing closes its session
// and returns NotFound.
func TestAPairingWhoseRowIsGoneClosesItsSession(t *testing.T) {
	f, counter := countedFixture(t)
	f.instance(t, "shop", "connection_status = 'NEEDS_AUTH'")
	release := make(chan struct{})
	pairing := pairHeldUntil(f, counter, release)
	paired := make(chan error, 1)
	go func() {
		paired <- f.o.CreateInstance(ctx, newPairing("shop"), func(engine.CreateInstanceUpdate) {})
	}()
	<-pairing
	if _, err := f.db.Exec("DELETE FROM instances WHERE name = 'shop'"); err != nil {
		t.Fatal(err)
	}
	close(release)

	if err := <-paired; calyxerr.CodeOf(err) != codes.NotFound {
		t.Errorf("the pairing got %v, want NotFound", err)
	}
	if engine.IsLive(f.o, "shop") {
		t.Error("the pairing registered an instance with no row")
	}
	if got := counter.Open("shop"); got != 0 {
		t.Errorf("%d sessions open, want 0", got)
	}
}
