package engine_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/engine"
	"calyx/core/internal/testutil"
)

const sendOnly = "is_used_only_for_send_messages = 1"

// hangConnect makes every Connect of the fixture wait until its context ends.
func (f *orchestratorFixture) hangConnect() {
	f.configure = func(fake *testutil.FakeProvider) {
		fake.BeforeConnect(func(ctx context.Context) { <-ctx.Done() })
	}
}

func TestAWakeOfASendOnlyInstanceEndsWithUnavailableAtItsLimit(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", sendOnly)
	f.hangConnect()

	var err error
	returnsWithin(t, "the wake", func() { err = engine.AcquireWithin(f.o, "shop", 50*time.Millisecond) }, 3*time.Second)
	if calyxerr.CodeOf(err) != codes.Unavailable {
		t.Fatalf("got %v", err)
	}
	if engine.IsLive(f.o, "shop") {
		t.Fatal("the instance is live after a failed wake")
	}
	if _, destroys, _ := f.fake("shop").Counts(); destroys != 1 {
		t.Fatalf("%d destroys of the provider of the failed wake", destroys)
	}
}

func TestACallThatJoinsALimitedWakeGetsTheSameUnavailable(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", sendOnly)
	connecting := make(chan struct{})
	f.configure = func(fake *testutil.FakeProvider) {
		fake.BeforeConnect(func(ctx context.Context) {
			close(connecting)
			<-ctx.Done()
		})
	}

	wake := make(chan error, 1)
	go func() { wake <- engine.AcquireWithin(f.o, "shop", 100*time.Millisecond) }()
	<-connecting
	var joined error
	returnsWithin(t, "the joined call", func() { joined = engine.AcquireWithin(f.o, "shop", 0) }, 3*time.Second)
	if calyxerr.CodeOf(<-wake) != codes.Unavailable || calyxerr.CodeOf(joined) != codes.Unavailable {
		t.Fatalf("joined call: %v", joined)
	}
	if f.buildCount("shop") != 1 {
		t.Fatalf("%d builds: the joined call did not share the load", f.buildCount("shop"))
	}
}

func TestALongLivedInstanceIgnoresTheLimitOfAWake(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	var limited atomic.Bool
	f.configure = func(fake *testutil.FakeProvider) {
		fake.BeforeConnect(func(ctx context.Context) {
			_, hasDeadline := ctx.Deadline()
			limited.Store(hasDeadline)
		})
	}

	if err := engine.AcquireWithin(f.o, "shop", 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if limited.Load() {
		t.Fatal("the connect of a long-lived instance got the limit of a wake")
	}
	if !engine.IsLive(f.o, "shop") {
		t.Fatal("the long-lived instance is not live")
	}
}

func TestDestroyAllSessionsEndsALoadThatHangs(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	connecting := make(chan struct{})
	f.configure = func(fake *testutil.FakeProvider) {
		fake.BeforeConnect(func(ctx context.Context) {
			close(connecting)
			<-ctx.Done()
		})
	}
	loaded := make(chan struct{})
	go func() {
		defer close(loaded)
		f.o.LoadAllActiveInstances(ctx)
	}()
	<-connecting

	returnsWithin(t, "DestroyAllSessions", f.o.DestroyAllSessions, 3*time.Second)
	<-loaded
	if engine.IsLive(f.o, "shop") {
		t.Fatal("the instance is live after DestroyAllSessions")
	}
	if connects, destroys, _ := f.fake("shop").Counts(); connects != destroys {
		t.Fatalf("%d connects, %d destroys", connects, destroys)
	}
}

func TestALoadAfterDestroyAllSessionsFails(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.o.DestroyAllSessions()

	if err := engine.AcquireWithin(f.o, "shop", 0); err == nil {
		t.Fatal("a load after DestroyAllSessions worked")
	}
	if f.buildCount("shop") != 0 {
		t.Fatal("a load after DestroyAllSessions built a provider")
	}
}
