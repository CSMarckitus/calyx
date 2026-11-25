package engine_test

// A hard gRPC stop does not wait for the handlers, so a pairing or
// a removal can go on after DestroyAllSessions. The orchestrator must refuse their last steps.

import (
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/engine"
)

func TestAPairingThatEndsAfterTheShutdownRegistersNothing(t *testing.T) {
	f, counter := countedFixture(t)
	f.instance(t, "shop", "connection_status = 'NEEDS_AUTH'")
	engine.SetPairedHook(f.o, f.o.DestroyAllSessions)

	err := f.o.CreateInstance(ctx, newPairing("shop"), func(engine.CreateInstanceUpdate) {})
	if calyxerr.CodeOf(err) != codes.Unavailable {
		t.Errorf("the pairing after the shutdown got %v, want Unavailable", err)
	}
	if engine.IsLive(f.o, "shop") {
		t.Error("the pairing registered a live instance after the shutdown")
	}
	if got := counter.Open("shop"); got != 0 {
		t.Errorf("%d sessions still open after the shutdown", got)
	}
}

func TestRemovalThatStartsAfterTheShutdownChangesNothing(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.o.LoadAllActiveInstances(ctx)
	fake := f.fake("shop")
	engine.SetRemovalHook(f.o, f.o.DestroyAllSessions)

	err := f.o.RemoveInstance(ctx, "shop")
	if calyxerr.CodeOf(err) != codes.Unavailable {
		t.Errorf("the removal after the shutdown got %v, want Unavailable", err)
	}
	var rows int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM instances WHERE name = 'shop'").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("%d rows of shop after the shutdown, want 1", rows)
	}
	if _, _, logouts := fake.Counts(); logouts != 0 {
		t.Errorf("the device was logged out %d times after the shutdown", logouts)
	}
}

// Guard: a removal that published its close before the shutdown ends before DestroyAllSessions
// returns, so its DELETE runs before main closes the database.
func TestShutdownWaitsForARemovalInProgress(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.o.LoadAllActiveInstances(ctx)
	destroying, proceed := make(chan struct{}), make(chan struct{})
	f.fake("shop").BeforeDestroy(func() {
		close(destroying)
		<-proceed
	})

	removed := make(chan error, 1)
	go func() { removed <- f.o.RemoveInstance(ctx, "shop") }()
	<-destroying
	shutDown := make(chan struct{})
	go func() {
		f.o.DestroyAllSessions()
		close(shutDown)
	}()
	select {
	case <-shutDown:
		t.Error("DestroyAllSessions returned before the removal in progress ended")
	case <-time.After(100 * time.Millisecond):
	}
	close(proceed)
	if err := <-removed; err != nil {
		t.Fatal(err)
	}
	<-shutDown
	var rows int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM instances WHERE name = 'shop'").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("%d rows of shop after the removal, want 0", rows)
	}
}
