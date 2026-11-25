package engine_test

// A rename must not open a session of the new name while the session of the old
// name is still open, and the minimum gap must hold across the rename.

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

const renameGap = 500 * time.Millisecond

func TestARenameWaitsForTheCloseOfTheOldName(t *testing.T) {
	cases := map[string]func(t *testing.T, f *orchestratorFixture){
		// The rename itself closes the live instance of the old name.
		"live instance": func(t *testing.T, f *orchestratorFixture) {
			// An active task keeps the idle timer away, so only the rename closes the instance.
			if _, err := engine.Acquire(f.o, "shop"); err != nil {
				t.Fatal(err)
			}
		},
		// The idle timer took the instance out of the registry and its close still runs.
		"close in progress": func(t *testing.T, f *orchestratorFixture) {
			deadline := time.Now().Add(5 * time.Second)
			for engine.IsLive(f.o, "shop") {
				if time.Now().After(deadline) {
					t.Fatal("the idle timer did not close the instance")
				}
				time.Sleep(5 * time.Millisecond)
			}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			f, counter := countedFixture(t)
			f.instance(t, "shop", "is_used_only_for_send_messages = 1")
			destroying, proceed := make(chan struct{}), make(chan struct{})
			var oldOpenAtNewConnect []int
			var mu sync.Mutex
			f.configure = func(fake *testutil.FakeProvider) {
				fake.CountConnections(counter)
				fake.SetPacing(engine.PacingPolicy{MinGapMs: int(renameGap.Milliseconds()), MaxPendingDirect: 3,
					Delay: engine.Rule{Fallback: engine.Range{Min: 1, Max: 2}, Min: 1}})
				if fake.InstanceName() == "store" {
					fake.BeforeConnect(func(context.Context) {
						mu.Lock()
						oldOpenAtNewConnect = append(oldOpenAtNewConnect, counter.Open("shop"))
						mu.Unlock()
					})
					return
				}
				fake.BeforeDestroy(func() {
					select {
					case <-destroying: // a later close does not wait
					default:
						close(destroying)
						<-proceed
					}
				})
			}

			if _, err := f.o.SendText(ctx, "shop", "34600000001", "first", nil); err != nil {
				t.Fatal(err)
			}
			setup(t, f)

			newName := "store"
			renamed := make(chan error, 1)
			go func() {
				renamed <- f.o.UpdateInstanceConfig(ctx, "shop", engine.UpdateInstanceParams{NewName: &newName})
			}()
			<-destroying // a close of the old session holds in Destroy
			// A rename that waits for a close in progress before its commit leaves no window: then
			// the test lets the close end first, and checks the gap only.
			committed := rowAppears(t, f, "store", 300*time.Millisecond)
			if !committed {
				close(proceed)
				if err := <-renamed; err != nil {
					t.Fatal(err)
				}
			}

			sent := make(chan error, 1)
			go func() {
				_, err := f.o.SendText(ctx, "store", "34600000002", "second", nil)
				sent <- err
			}()
			if committed {
				time.Sleep(100 * time.Millisecond)
				close(proceed)
				if err := <-renamed; err != nil {
					t.Fatal(err)
				}
			}
			if err := <-sent; err != nil {
				t.Fatal(err)
			}

			mu.Lock()
			for _, open := range oldOpenAtNewConnect {
				if open != 0 {
					t.Errorf("the new name connected while %d sessions of the old name were open", open)
				}
			}
			mu.Unlock()
			f.mu.Lock()
			first, second := f.fakes["shop"].Sent(), f.fakes["store"].Sent()
			f.mu.Unlock()
			if len(first) != 1 || len(second) != 1 {
				t.Fatalf("sent %d and %d messages, want 1 and 1", len(first), len(second))
			}
			if gap := second[0].At.Sub(first[0].At); gap < renameGap {
				t.Errorf("the gap across the rename is %v, want at least %v", gap, renameGap)
			}
			f.o.DestroyAllSessions()
			if open := counter.Open("shop") + counter.Open("store"); open != 0 {
				t.Errorf("%d sessions still open after DestroyAllSessions", open)
			}
		})
	}
}

// A rename to the same name must not wait for itself.
func TestARenameToTheSameNameEnds(t *testing.T) {
	f, _ := countedFixture(t)
	f.instance(t, "shop", "")
	f.o.LoadAllActiveInstances(ctx)
	same := "shop"
	done := make(chan error, 1)
	go func() { done <- f.o.UpdateInstanceConfig(ctx, "shop", engine.UpdateInstanceParams{NewName: &same}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the rename to the same name did not end")
	}
	if !engine.IsLive(f.o, "shop") {
		t.Error("the instance was not loaded again after the rename")
	}
}

// A rename to a name that another instance has fails, and the other instance stays usable.
func TestARenameToATakenNameLeavesTheOtherInstance(t *testing.T) {
	f, _ := countedFixture(t)
	f.instance(t, "shop", "")
	f.instance(t, "store", "")
	f.o.LoadAllActiveInstances(ctx)
	taken := "store"
	if err := f.o.UpdateInstanceConfig(ctx, "shop", engine.UpdateInstanceParams{NewName: &taken}); err == nil {
		t.Fatal("the rename to a taken name succeeded")
	}
	if _, err := f.o.SendText(ctx, "store", "34600000001", "hi", nil); err != nil {
		t.Fatal(err)
	}
	if !engine.IsLive(f.o, "shop") || !engine.IsLive(f.o, "store") {
		t.Error("a failed rename closed an instance")
	}
}

// rowAppears tells whether a row named name exists within d.
func rowAppears(t *testing.T, f *orchestratorFixture, name string, d time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		var n int
		if err := f.db.QueryRow("SELECT COUNT(*) FROM instances WHERE name = ?", name).Scan(&n); err != nil {
			t.Error(err)
			return false
		}
		if n == 1 {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
}

// heldDestroys makes the first Destroy of each given name wait until its proceed channel is
// closed. destroying[name] is closed when that Destroy starts.
func heldDestroys(f *orchestratorFixture, counter *testutil.ConnectionCounter, names ...string) (destroying, proceed map[string]chan struct{}) {
	destroying, proceed = map[string]chan struct{}{}, map[string]chan struct{}{}
	for _, name := range names {
		destroying[name], proceed[name] = make(chan struct{}), make(chan struct{})
	}
	f.configure = func(fake *testutil.FakeProvider) {
		fake.CountConnections(counter)
		name := fake.InstanceName()
		if destroying[name] == nil {
			return
		}
		fake.BeforeDestroy(func() {
			select {
			case <-destroying[name]: // a later close does not wait
			default:
				close(destroying[name])
				<-proceed[name]
			}
		})
	}
	return destroying, proceed
}

// await stops the test binary when ch is not closed within d. A hang here is a deadlock, and the
// cleanup of the fixture (DestroyAllSessions) would hang too, so the panic runs in another
// goroutine, where no cleanup runs.
func await(ch <-chan struct{}, d time.Duration, what string) {
	select {
	case <-ch:
	case <-time.After(d):
		go func() { panic("deadlock: " + what + " did not happen within " + d.String()) }()
		select {}
	}
}

// waitForChange waits a short time for the close entry of name to be replaced. It does not fail:
// with the fix, the next rename waits for configMu and replaces nothing.
func waitForChange(o *engine.Orchestrator, name string, old chan struct{}) {
	deadline := time.Now().Add(300 * time.Millisecond)
	for engine.ClosingOf(o, name) == old && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
}

// Three renames that overlap and rotate the names. Each rename must end; none may wait for the
// hold of the next one.
func TestARotationOfNamesEnds(t *testing.T) {
	f, counter := countedFixture(t)
	f.instance(t, "alpha", "")
	f.instance(t, "beta", "")
	destroying, proceed := heldDestroys(f, counter, "alpha", "beta")
	f.o.LoadAllActiveInstances(ctx)

	results := make(chan error, 3)
	rename := func(from, to string) {
		go func() { results <- f.o.UpdateInstanceConfig(ctx, from, engine.UpdateInstanceParams{NewName: &to}) }()
	}
	rename("alpha", "tmp")
	await(destroying["alpha"], 5*time.Second, "the close of alpha")
	held := engine.ClosingOf(f.o, "alpha")
	rename("beta", "alpha")
	waitForChange(f.o, "alpha", held)
	close(proceed["alpha"])
	await(destroying["beta"], 5*time.Second, "the close of beta")
	held = engine.ClosingOf(f.o, "beta")
	rename("tmp", "beta")
	waitForChange(f.o, "beta", held)
	close(proceed["beta"])

	all := make(chan struct{})
	var errs []error
	go func() {
		for range 3 {
			errs = append(errs, <-results)
		}
		close(all)
	}()
	await(all, 5*time.Second, "the end of the three renames")
	for _, err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if got := counter.Open("alpha") + counter.Open("beta") + counter.Open("tmp"); got != 2 {
		t.Errorf("%d sessions open, want 2", got)
	}
}

// A rename that commits after a removal read the row must not let the removal delete the
// renamed instance.
func TestARemovalDoesNotDeleteARenamedInstance(t *testing.T) {
	f, counter := countedFixture(t)
	f.instance(t, "shop", "is_used_only_for_send_messages = 1")
	newName := "store"
	var renameErr error
	engine.SetRemovalHook(f.o, func() {
		renameErr = f.o.UpdateInstanceConfig(ctx, "shop", engine.UpdateInstanceParams{NewName: &newName})
		if renameErr == nil {
			_, renameErr = f.o.SendText(ctx, "store", "34600000001", "hi", nil)
		}
	})

	err := f.o.RemoveInstance(ctx, "shop")
	if renameErr != nil {
		t.Fatal(renameErr)
	}
	if calyxerr.CodeOf(err) != codes.NotFound {
		t.Errorf("the removal got %v, want NotFound", err)
	}
	var rows int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM instances WHERE name = 'store'").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("%d rows of store, want 1", rows)
	}
	if engine.IsLive(f.o, "store") && rows == 0 {
		t.Error("a live instance has no row")
	}
	f.mu.Lock()
	fake := f.fakes["store"]
	f.mu.Unlock()
	if _, _, logouts := fake.Counts(); logouts != 0 {
		t.Errorf("the renamed device was logged out %d times", logouts)
	}
	f.o.DestroyAllSessions()
	if got := counter.Open("shop") + counter.Open("store"); got != 0 {
		t.Errorf("%d sessions still open after DestroyAllSessions", got)
	}
}

// A rename of an instance that a removal is closing waits for the removal and finds no row.
func TestARenameDuringARemovalFindsNoRow(t *testing.T) {
	f, counter := countedFixture(t)
	f.instance(t, "shop", "")
	destroying, proceed := heldDestroys(f, counter, "shop")
	f.o.LoadAllActiveInstances(ctx)
	removed := make(chan error, 1)
	go func() { removed <- f.o.RemoveInstance(ctx, "shop") }()
	await(destroying["shop"], 5*time.Second, "the close of the removal")

	newName := "store"
	renamed := make(chan error, 1)
	go func() {
		renamed <- f.o.UpdateInstanceConfig(ctx, "shop", engine.UpdateInstanceParams{NewName: &newName})
	}()
	time.Sleep(100 * time.Millisecond)
	close(proceed["shop"])
	if err := <-removed; err != nil {
		t.Fatal(err)
	}
	if err := <-renamed; calyxerr.CodeOf(err) != codes.NotFound {
		t.Errorf("the rename got %v, want NotFound", err)
	}
	if engine.IsLive(f.o, "store") {
		t.Error("the rename loaded a removed instance")
	}
	if got := counter.Open("shop") + counter.Open("store"); got != 0 {
		t.Errorf("%d sessions open, want 0", got)
	}
}

// A removal that wakes a send-only instance and then stops early (here its caller gives up) must
// end its task, so the instance can go to sleep.
func TestARemovalThatStopsEarlyLetsTheInstanceSleep(t *testing.T) {
	f, _ := countedFixture(t)
	f.instance(t, "shop", "is_used_only_for_send_messages = 1")
	callCtx, cancel := context.WithCancel(ctx)
	engine.SetRemovalHook(f.o, cancel)

	if err := f.o.RemoveInstance(callCtx, "shop"); err == nil {
		t.Fatal("the removal with a cancelled context succeeded")
	}
	if !engine.IsLive(f.o, "shop") {
		t.Fatal("the removal did not wake the instance")
	}
	deadline := time.Now().Add(2 * time.Second)
	for engine.IsLive(f.o, "shop") {
		if time.Now().After(deadline) {
			t.Fatal("the instance did not go to sleep: the removal did not end its task")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
