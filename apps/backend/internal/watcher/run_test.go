package watcher_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"calyx/backend/internal/live"
	"calyx/backend/internal/testutil"
	"calyx/backend/internal/watcher"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

func rowExists(t *testing.T, e *env, id uuid.UUID) bool {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM instances WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRoundRecoversCreatingAndRemovingRows(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	creating := testutil.SeedInstance(t, e.pool, testutil.InstanceSeed{
		OrganizationID: e.tn.OrganizationID, ClientID: e.tn.ClientA1, State: "creating", CreatedAt: e.clk.Now().Add(-5 * time.Minute),
	})
	removing := testutil.SeedInstance(t, e.pool, testutil.InstanceSeed{
		OrganizationID: e.tn.OrganizationID, ClientID: e.tn.ClientA1, State: "removing",
	})
	round(t, e.newWatcher())
	if rowExists(t, e, creating.ID) {
		t.Fatal("the creating row still exists")
	}
	if rowExists(t, e, removing.ID) {
		t.Fatal("the removing row still exists")
	}
	reqs := e.fc.RemoveRequests()
	if len(reqs) != 1 || reqs[0].GetInstanceName() != removing.CoreName {
		t.Fatalf("remove requests %v", reqs)
	}
}

func TestRoundSkipsRecoveryWhenTheCoreIsUnreachable(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	removing := testutil.SeedInstance(t, e.pool, testutil.InstanceSeed{
		OrganizationID: e.tn.OrganizationID, ClientID: e.tn.ClientA1, State: "removing",
	})
	e.fc.SetUnavailable(true)
	if err := e.newWatcher().Round(context.Background()); err == nil {
		t.Fatal("no error")
	}
	if e.row(t, removing.ID).State != "removing" {
		t.Fatal("state changed")
	}
	e.fc.SetUnavailable(false)
	if n := len(e.fc.RemoveRequests()); n != 0 {
		t.Fatalf("remove requests %d", n)
	}
}

// CheckInstance saves the status and the alert, and signals Run. Run sends the mail with no tick
// of the timer.
func TestCheckInstanceAfterAFailedSend(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stConnected)
	w := e.newWatcher()
	e.startRun(t, w)
	e.fc.SetStatus(in.CoreName, stNeedsAuth)
	w.CheckInstance(context.Background(), in.ID)
	r := e.row(t, in.ID)
	if *r.LastStatus != "NEEDS_AUTH" || r.AlertKind == nil || *r.AlertKind != "needs_auth" {
		t.Fatalf("row %+v", r)
	}
	waitFor(t, "the alert mail", func() bool { return len(e.smtp.Messages()) == 1 })
}

func TestCheckInstanceDoesNotSendTheMail(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stNeedsAuth)
	e.newWatcher().CheckInstance(context.Background(), in.ID)
	if r := e.row(t, in.ID); r.AlertKind == nil || *r.AlertKind != "needs_auth" || r.AlertedAt != nil {
		t.Fatalf("row %+v", r)
	}
	wantMails(t, e, 0)
}

func TestCheckInstanceWhenTheCoreLostTheInstance(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stConnected)
	w := e.newWatcher()
	e.startRun(t, w)
	e.fc.DropInstance(in.CoreName)
	w.CheckInstance(context.Background(), in.ID)
	if e.row(t, in.ID).State != "missing" {
		t.Fatal("not missing")
	}
	waitFor(t, "the alert mail", func() bool { return len(e.smtp.Messages()) == 1 })
	if s := subjectOf(e.smtp.Messages()[0].Data); s != "[Calyx] Front desk no longer exists in the core" {
		t.Fatalf("subject %q", s)
	}
}

func TestCheckInstanceWhenTheCoreIsUnreachable(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stNeedsAuth)
	before := e.row(t, in.ID)
	e.fc.SetUnavailable(true)
	e.newWatcher().CheckInstance(context.Background(), in.ID)
	after := e.row(t, in.ID)
	if *after.LastStatus != "CONNECTED" || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("row changed %+v", after)
	}
	wantMails(t, e, 0)
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core_status`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("core_status rows %d %v", n, err)
	}
}

func TestCheckInstanceIgnoresRowsThatAreNotActive(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{State: "creating"}, stNeedsAuth)
	e.newWatcher().CheckInstance(context.Background(), in.ID)
	if n := len(e.fc.StatusRequests()); n != 0 {
		t.Fatalf("status requests %d", n)
	}
	wantMails(t, e, 0)
}

func TestASendThatFailsStartsTheCheck(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED"), SendOnly: true}, stConnected)
	w := e.newWatcher()
	e.inst.SetStatusCheck(w.CheckInstance)
	e.startRun(t, w)
	e.fc.OnSendPlainText(func(ctx context.Context, req *corev1.SendPlainTextRequest) (*corev1.SendPlainTextResponse, error) {
		e.fc.SetStatus(in.CoreName, stNeedsAuth)
		return nil, status.Error(codes.FailedPrecondition, "needs to authenticate again")
	})
	_, err := e.inst.Send(context.Background(), ownerPrincipal(e), in.ID, "34600000000", "hello", "")
	if err == nil {
		t.Fatal("no error")
	}
	waitFor(t, "the alert mail", func() bool { return len(e.mailsTo(e.tn.OwnerEmail)) == 1 })
	ms := e.smtp.Messages()
	if subjectOf(ms[0].Data) != "[Calyx] Front desk must be paired again" {
		t.Fatalf("subject %q", subjectOf(ms[0].Data))
	}
}

func TestCheckInstanceAndRunTogetherMailOnce(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stConnected)
	w := e.newWatcher()
	e.startRun(t, w)
	e.fc.SetStatus(in.CoreName, stNeedsAuth)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); w.CheckInstance(context.Background(), in.ID) }()
	}
	wg.Wait()
	e.clk.Advance(30 * time.Second)
	waitFor(t, "the alert mail", func() bool { return len(e.smtp.Messages()) >= 1 })
	waitFor(t, "the next timer", func() bool { return e.clk.PendingTimers() >= 1 })
	wantMails(t, e, 1)
}
func TestRunDoesTheFirstRoundAtOnce(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stNeedsAuth)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.newWatcher().Run(ctx)
	waitFor(t, "the first mail", func() bool { return len(e.smtp.Messages()) == 1 })
}

func TestRunRepeatsEveryInterval(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stNeedsAuth)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.newWatcher().Run(ctx)
	waitFor(t, "the first mail", func() bool { return len(e.smtp.Messages()) == 1 })
	waitFor(t, "the timer", func() bool { return e.clk.PendingTimers() >= 1 })
	e.fc.SetStatus(in.CoreName, stError)
	e.clk.Advance(30 * time.Second)
	waitFor(t, "the second mail", func() bool { return len(e.smtp.Messages()) == 2 })
}

func TestRunStopsWhenTheContextEnds(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.newWatcher().Run(ctx); close(done) }()
	waitFor(t, "the timer", func() bool { return e.clk.PendingTimers() >= 1 })
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// listCalls is the number of ListInstances calls: one for each round, failed ones included.
func listCalls(e *env) int { return len(e.fc.Requests(testutil.MethodListInstances)) }

// signalRoundAndWait signals Run and waits until the round of that signal ended. Run takes a
// second signal only after the first round ended, so the second ListInstances call proves it.
func signalRoundAndWait(t *testing.T, e *env, w *watcher.Watcher) {
	t.Helper()
	n := listCalls(e)
	w.SignalRound()
	waitFor(t, "the round of the signal", func() bool { return listCalls(e) >= n+1 })
	w.SignalRound()
	waitFor(t, "the round of the second signal", func() bool { return listCalls(e) >= n+2 })
}

func TestAMailOf15SecondsDoesNotDelayCheckInstance(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stNeedsAuth)
	other := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stConnected)
	g := newGate()
	w := e.watcherWith(&hookSender{next: e.smtpSender(), hook: g.wait})
	go func() { _ = w.Round(context.Background()) }()
	g.waitEntered(t) // the mail now waits, as a slow mail server does
	e.fc.SetStatus(other.CoreName, stError)
	start := time.Now()
	w.CheckInstance(context.Background(), other.ID)
	if d := time.Since(start); d > time.Second {
		t.Fatalf("CheckInstance took %v", d)
	}
	if r := e.row(t, other.ID); *r.LastStatus != "ERROR" || r.AlertKind == nil || *r.AlertKind != "error" {
		t.Fatalf("row %+v", r)
	}
	close(g.release)
	waitFor(t, "the two mails", func() bool { return len(e.smtp.Messages()) == 2 })
}

func TestTheSignalDoesNotMoveTheTimer(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stConnected)
	w := e.newWatcher()
	e.startRun(t, w)
	e.clk.Advance(20 * time.Second)
	e.fc.SetStatus(in.CoreName, stDisconnected)
	signalRoundAndWait(t, e, w)
	if *e.row(t, in.ID).LastStatus != "DISCONNECTED" {
		t.Fatal("the round of the signal did not run")
	}
	if n := e.clk.PendingTimers(); n != 1 {
		t.Fatalf("pending timers %d", n)
	}
	// The timer of the first round fires 30 s after that round, not 30 s after the signal.
	e.fc.SetUnavailable(true)
	e.clk.Advance(10 * time.Second)
	waitFor(t, "the counted round", func() bool {
		var n int
		_ = e.pool.QueryRow(context.Background(), `SELECT failed_checks FROM core_status`).Scan(&n)
		return n == 1
	})
}

func TestARoundOfTheSignalCountsNoFailure(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stConnected)
	e.lv.SetCoreLink(true, "")
	w := e.newWatcher()
	e.startRun(t, w)
	if got := e.lv.Core(); got != live.CoreOK {
		t.Fatalf("core %s after the first round", got)
	}
	e.fc.SetUnavailable(true)
	signalRoundAndWait(t, e, w)
	signalRoundAndWait(t, e, w)
	if cs := e.coreStatus(t); cs.FailedChecks != 0 || cs.UnreachableSince != nil {
		t.Fatalf("core_status %+v", cs)
	}
	if got := e.lv.Core(); got != live.CoreOK {
		t.Fatalf("core %s after failed rounds of the signal", got)
	}
	wantMails(t, e, 0)
}

func TestARoundOfTheSignalSendsTheDueMails(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stConnected)
	w := e.newWatcher()
	e.startRun(t, w)
	e.fc.SetStatus(in.CoreName, stNeedsAuth)
	signalRoundAndWait(t, e, w)
	wantMails(t, e, 1)
}

func TestRecoverDoesNotHoldTheLock(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	testutil.SeedInstance(t, e.pool, testutil.InstanceSeed{
		OrganizationID: e.tn.OrganizationID, ClientID: e.tn.ClientA1, State: "removing",
	})
	other := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stConnected)
	e.fc.SetDelay(testutil.MethodRemoveInstance, 2*time.Second)
	w := e.newWatcher()
	done := make(chan struct{})
	go func() { defer close(done); _ = w.Round(context.Background()) }()
	waitFor(t, "the recovery", func() bool { return len(e.fc.RemoveRequests()) == 1 })
	e.fc.SetStatus(other.CoreName, stError)
	start := time.Now()
	w.CheckInstance(context.Background(), other.ID)
	if d := time.Since(start); d > time.Second {
		t.Fatalf("CheckInstance took %v", d)
	}
	if *e.row(t, other.ID).LastStatus != "ERROR" {
		t.Fatal("status not stored")
	}
	<-done
}
