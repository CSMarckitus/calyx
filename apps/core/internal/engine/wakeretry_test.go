package engine_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/engine"
	"calyx/core/internal/events"
	"calyx/core/internal/testutil"
)

const (
	wakeWarn = "The wake of the instance failed."
	wakeInfo = "The wake of the instance worked after a failure."
)

// wakeClock replaces the waits of the wake loop. Each wait reports its length on asked, and a
// value on fire ends it.
type wakeClock struct {
	asked chan time.Duration
	fire  chan time.Time
}

// startWakeRetry starts the wake loop with a wakeClock and returns after its first wait started.
func (f *orchestratorFixture) startWakeRetry(t *testing.T) *wakeClock {
	t.Helper()
	c := &wakeClock{asked: make(chan time.Duration, 10), fire: make(chan time.Time)}
	engine.SetWakeTimer(f.o, func(d time.Duration) <-chan time.Time {
		c.asked <- d
		return c.fire
	})
	f.o.StartWakeRetry()
	if d := c.nextWait(t); d != time.Minute {
		t.Fatalf("the first run waits %v, want one minute", d)
	}
	return c
}

// nextWait returns the length of the next wait that the loop starts.
func (c *wakeClock) nextWait(t *testing.T) time.Duration {
	t.Helper()
	select {
	case d := <-c.asked:
		return d
	case <-time.After(5 * time.Second):
		t.Fatal("the wake loop did not start a wait within 5s")
		return 0
	}
}

// run starts one run and returns after it ended (the loop starts its next wait).
func (c *wakeClock) run(t *testing.T) {
	t.Helper()
	c.fire <- time.Now()
	if d := c.nextWait(t); d != time.Minute {
		t.Fatalf("the next run waits %v, want one minute", d)
	}
}

// startRun starts one run and does not wait for its end.
func (c *wakeClock) startRun() { c.fire <- time.Now() }

const asleep = "is_used_only_for_send_messages = 1, connection_status = 'DISCONNECTED'"

func TestAWakeWorksAfterTheNetworkReturns(t *testing.T) {
	f := newOrchestratorFixture(t)
	logs := captureLogs(t)
	f.instance(t, "shop", asleep)
	var down atomic.Bool
	down.Store(true)
	f.configure = func(fake *testutil.FakeProvider) {
		if down.Load() {
			fake.FailConnect(calyxerr.New(codes.Unavailable, "fake: no network"))
		}
	}
	c := f.startWakeRetry(t)

	c.run(t)
	if engine.IsLive(f.o, "shop") {
		t.Fatal("the instance is live after a failed wake")
	}
	c.run(t)
	down.Store(false)
	c.run(t)

	if f.buildCount("shop") != 3 {
		t.Fatalf("%d wakes, want 3", f.buildCount("shop"))
	}
	text := logs.String()
	if n := strings.Count(text, wakeWarn); n != 1 {
		t.Fatalf("%d WARN lines for two failures, want 1:\n%s", n, text)
	}
	if n := strings.Count(text, wakeInfo); n != 1 {
		t.Fatalf("%d INFO lines for the wake that worked, want 1:\n%s", n, text)
	}
	if !strings.Contains(text, "level=WARN msg=\""+wakeWarn) || !strings.Contains(text, "level=INFO msg=\""+wakeInfo) {
		t.Fatalf("wrong levels:\n%s", text)
	}
}

func TestAWakeThatWorksTheFirstTimeLogsNothing(t *testing.T) {
	f := newOrchestratorFixture(t)
	logs := captureLogs(t)
	f.instance(t, "shop", asleep)
	c := f.startWakeRetry(t)

	c.run(t)
	if text := logs.String(); strings.Contains(text, wakeInfo) || strings.Contains(text, wakeWarn) {
		t.Fatalf("a wake with no failure logged:\n%s", text)
	}
}

func TestTheWakeTriesOnlyEnabledSendOnlyRowsThatAreNotConnected(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "disconnected", asleep)
	f.instance(t, "connecting", "is_used_only_for_send_messages = 1, connection_status = 'CONNECTING'")
	f.instance(t, "sleeping", "is_used_only_for_send_messages = 1, connection_status = 'CONNECTED'")
	f.instance(t, "lost", "is_used_only_for_send_messages = 1, connection_status = 'NEEDS_AUTH'")
	f.instance(t, "banned", "is_used_only_for_send_messages = 1, connection_status = 'ERROR'")
	f.instance(t, "off", asleep+", is_enabled = 0")
	f.instance(t, "long-lived", "connection_status = 'DISCONNECTED'")
	c := f.startWakeRetry(t)

	c.run(t)
	want := map[string]int{
		"disconnected": 1, "connecting": 1,
		"sleeping": 0, "lost": 0, "banned": 0, "off": 0, "long-lived": 0,
	}
	for name, n := range want {
		if got := f.buildCount(name); got != n {
			t.Errorf("%s woken %d times, want %d", name, got, n)
		}
	}
}

func TestTheFirstRunIsOneMinuteAfterTheStart(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", asleep)
	f.startWakeRetry(t)
	if f.buildCount("shop") != 0 {
		t.Fatal("the wake loop ran before its first minute")
	}

	// A restart: a new orchestrator on the same database.
	var builds atomic.Int32
	again := engine.NewOrchestrator(f.db, f.store, events.NewBus(), engine.OrchestratorOptions{
		BuildProvider: func(row engine.InstanceRow, hooks engine.ProviderHooks) (engine.Provider, error) {
			builds.Add(1)
			return testutil.NewFakeProvider(row.Name), nil
		},
	})
	t.Cleanup(again.DestroyAllSessions)
	asked := make(chan time.Duration, 1)
	engine.SetWakeTimer(again, func(d time.Duration) <-chan time.Time {
		asked <- d
		return nil
	})
	again.StartWakeRetry()
	if d := <-asked; d != time.Minute {
		t.Fatalf("after a restart the first run waits %v, want one minute", d)
	}
	if builds.Load() != 0 {
		t.Fatal("the wake loop ran at the restart")
	}
}

func TestTheNextRunWaitsOneMinuteAfterTheEndOfTheLast(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", asleep)
	connecting, proceed := f.blockFirstConnect()
	c := f.startWakeRetry(t)

	c.startRun()
	<-connecting
	select {
	case <-c.asked:
		t.Fatal("the next wait started before the end of the run")
	case <-time.After(100 * time.Millisecond):
	}
	close(proceed)
	if d := c.nextWait(t); d != time.Minute {
		t.Fatalf("the next run waits %v, want one minute", d)
	}
}

func TestAWokenInstanceSleepsAgainAndSendsNothing(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", asleep)
	c := f.startWakeRetry(t)

	c.run(t)
	waitFor(t, "the sleep", func() bool {
		_, destroys, _ := f.fake("shop").Counts()
		return destroys == 1 && !engine.IsLive(f.o, "shop")
	}, 3*time.Second)
	if sent := f.fake("shop").Sent(); len(sent) != 0 {
		t.Fatalf("the wake sent %+v", sent)
	}
	if f.jobCount(t) != 0 || f.recordCount(t) != 0 {
		t.Fatal("the wake created a job or a send record")
	}
}

func TestAWakeContinuesAnUnfinishedJobWithItsPacing(t *testing.T) {
	f := newOrchestratorFixture(t)
	id := f.instance(t, "shop", asleep)
	pacing := testutil.NewFakeProvider("x").Pacing()
	plan, ms, _ := engine.PlanBulk(bulkOf("34600000001", "34600000002"), engine.BulkPacingOptions{}, pacing)
	job, _, err := f.store.Create(ctx, id, plan, ms, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := f.startWakeRetry(t)

	c.run(t)
	waitFor(t, "job completed", func() bool {
		j, _ := f.store.Get(ctx, job.ID)
		return j.Status == engine.JobCompleted
	}, 3*time.Second)
	sent := f.fake("shop").Sent()
	if len(sent) != 2 {
		t.Fatalf("sent %+v", sent)
	}
	if gap := sent[1].At.Sub(sent[0].At); gap < time.Duration(pacing.MinGapMs)*time.Millisecond {
		t.Fatalf("the job sent with a gap of %v, below the pacing", gap)
	}
}

func TestAWakeEndsWithUnavailableAtItsLimit(t *testing.T) {
	f := newOrchestratorFixture(t)
	logs := captureLogs(t)
	f.instance(t, "shop", asleep)
	f.hangConnect()
	engine.SetWakeLimit(f.o, 50*time.Millisecond)
	c := f.startWakeRetry(t)

	c.run(t)
	if engine.IsLive(f.o, "shop") {
		t.Fatal("the instance is live after a wake that hung")
	}
	line := ""
	for l := range strings.Lines(logs.String()) {
		if strings.Contains(l, wakeWarn) {
			line = l
		}
	}
	if !strings.Contains(line, "could not connect within") {
		t.Fatalf("no WARN line with the end of the limit:\n%s", logs.String())
	}
}

func TestTheLimitOfAWakeIsTwentySeconds(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", asleep)
	left := make(chan time.Duration, 1)
	f.configure = func(fake *testutil.FakeProvider) {
		fake.BeforeConnect(func(ctx context.Context) {
			deadline, ok := ctx.Deadline()
			if !ok {
				left <- 0
				return
			}
			left <- time.Until(deadline)
		})
	}
	c := f.startWakeRetry(t)

	c.run(t)
	if d := <-left; d <= 19*time.Second || d > 20*time.Second {
		t.Fatalf("the connect of a wake had %v left, want 20s", d)
	}
}

func TestADisableDuringAWakeEndsBeforeTheLimitOfTheBackend(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", asleep)
	connecting := make(chan struct{})
	f.configure = func(fake *testutil.FakeProvider) {
		fake.BeforeConnect(func(ctx context.Context) {
			close(connecting)
			<-ctx.Done()
		})
	}
	engine.SetWakeLimit(f.o, 300*time.Millisecond)
	c := f.startWakeRetry(t)

	c.startRun()
	<-connecting
	off := false
	returnsWithin(t, "the disable", func() {
		if err := f.o.UpdateInstanceConfig(ctx, "shop", engine.UpdateInstanceParams{IsEnabled: &off}); err != nil {
			t.Error(err)
		}
	}, 3*time.Second)
	c.nextWait(t)
	if engine.IsLive(f.o, "shop") {
		t.Fatal("the disabled instance is live")
	}
}

func TestAChangeToLongLivedBesideAWakeLeavesTheInstanceLoaded(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", asleep)
	connecting := make(chan struct{})
	var first atomic.Bool
	first.Store(true)
	f.configure = func(fake *testutil.FakeProvider) {
		if !first.Swap(false) {
			return
		}
		fake.BeforeConnect(func(ctx context.Context) {
			close(connecting)
			<-ctx.Done()
		})
	}
	engine.SetWakeLimit(f.o, 100*time.Millisecond)
	c := f.startWakeRetry(t)

	c.startRun()
	<-connecting
	longLived := false
	if err := f.o.UpdateInstanceConfig(ctx, "shop", engine.UpdateInstanceParams{SendOnly: &longLived}); err != nil {
		t.Fatal(err)
	}
	c.nextWait(t)
	time.Sleep(300 * time.Millisecond) // longer than the idle time of a send-only instance
	if !engine.IsLive(f.o, "shop") {
		t.Fatal("the long-lived instance is not loaded after the wake")
	}
}

func TestNothingIsLiveAfterDestroyAllSessionsDuringAWake(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", asleep)
	f.instance(t, "other", asleep)
	connecting := make(chan struct{})
	f.configure = func(fake *testutil.FakeProvider) {
		if fake.InstanceName() == "other" {
			fake.BeforeConnect(func(ctx context.Context) {
				close(connecting)
				<-ctx.Done()
			})
		}
	}
	c := f.startWakeRetry(t)

	c.startRun()
	<-connecting
	returnsWithin(t, "DestroyAllSessions", f.o.DestroyAllSessions, 3*time.Second)
	for _, name := range []string{"shop", "other"} {
		if engine.IsLive(f.o, name) {
			t.Errorf("%s is live after DestroyAllSessions", name)
		}
		if connects, destroys, _ := f.fake(name).Counts(); connects != destroys {
			t.Errorf("%s: %d connects, %d destroys", name, connects, destroys)
		}
	}
}

func TestTheWakeSkipsALoadedInstance(t *testing.T) {
	f := newOrchestratorFixture(t)
	logs := captureLogs(t)
	f.instance(t, "shop", asleep)
	var down atomic.Bool
	down.Store(true)
	f.configure = func(fake *testutil.FakeProvider) {
		if down.Load() {
			fake.FailConnect(calyxerr.New(codes.Unavailable, "fake: no network"))
		}
	}
	c := f.startWakeRetry(t)
	c.run(t) // a failure, so a wake that works logs an INFO line

	// A send holds the instance loaded, and its stored status stays DISCONNECTED.
	down.Store(false)
	release, err := engine.Acquire(f.o, "shop")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	builds := f.buildCount("shop")
	connects, _, _ := f.fake("shop").Counts()
	c.run(t)

	if f.buildCount("shop") != builds {
		t.Fatal("the wake built the loaded instance again")
	}
	if now, _, _ := f.fake("shop").Counts(); now != connects {
		t.Fatal("the wake connected the loaded instance again")
	}
	if text := logs.String(); strings.Contains(text, wakeInfo) {
		t.Fatalf("the wake acquired a loaded instance:\n%s", text)
	}
}

func TestTheWakeSkipsARowThatChangedDuringTheRun(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "first", asleep)
	f.instance(t, "second", asleep)
	connecting, proceed := f.blockFirstConnect()
	c := f.startWakeRetry(t)

	c.startRun()
	<-connecting
	// A temporary ban, while the run wakes the first row.
	if _, err := f.db.Exec("UPDATE instances SET connection_status = 'ERROR' WHERE name = 'second'"); err != nil {
		t.Fatal(err)
	}
	close(proceed)
	c.nextWait(t)
	if f.buildCount("second") != 0 {
		t.Fatal("the wake tried a row that is ERROR now")
	}
}
