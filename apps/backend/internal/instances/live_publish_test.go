package instances_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"calyx/backend/internal/core"
	"calyx/backend/internal/instances"
	"calyx/backend/internal/live"
	"calyx/backend/internal/testutil"
)

// watchers holds the streams of the owner, of the user of client A1 and of the user of client
// A2. A change of client A1 must reach the first two and never the third.
type watchers struct{ owner, a1, a2 *live.Subscription }

func (e *env) watch(t *testing.T) watchers {
	t.Helper()
	w := watchers{e.lv.Subscribe(e.owner), e.lv.Subscribe(e.userA1), e.lv.Subscribe(e.userA2)}
	t.Cleanup(func() { w.owner.Close(); w.a1.Close(); w.a2.Close() })
	return w
}

func changed(sub *live.Subscription, wait time.Duration) bool {
	select {
	case <-sub.Changed():
		return true
	case <-time.After(wait):
		return false
	}
}

// expectChange waits for one notification for the owner and the user of A1, and none for A2.
func (w watchers) expectChange(t *testing.T) {
	t.Helper()
	if !changed(w.owner, 2*time.Second) || !changed(w.a1, 2*time.Second) {
		t.Fatal("no notification")
	}
	if changed(w.a2, 50*time.Millisecond) {
		t.Fatal("a foreign client got a notification")
	}
	if changed(w.owner, 50*time.Millisecond) {
		t.Fatal("two notifications")
	}
}

func (w watchers) expectQuiet(t *testing.T) {
	t.Helper()
	if changed(w.owner, 100*time.Millisecond) || changed(w.a1, time.Millisecond) {
		t.Fatal("a notification without a change")
	}
}

func (w watchers) drain() {
	changed(w.owner, 50*time.Millisecond)
	changed(w.a1, time.Millisecond)
	changed(w.a2, time.Millisecond)
}

func TestCreatePublishesAtTheStartAndAtTheEnd(t *testing.T) {
	e := newEnv(t)
	w := e.watch(t)
	sc := newPairingScript(e.fc)
	sc.send(needsAuth, "QR1", "scan")
	in := webInput()
	in.ClientID = e.tn.ClientA1.String()
	v, _, err := e.svc.Create(context.Background(), e.owner, in)
	if err != nil {
		t.Fatal(err)
	}
	if !changed(w.owner, time.Second) || !changed(w.a1, time.Second) || changed(w.a2, 50*time.Millisecond) {
		t.Fatal("no notification at the start, or a foreign one")
	}
	if st := e.row(t, v.ID).State; st != "creating" {
		t.Fatalf("state %q at the start", st)
	}
	sc.send(connected, "", "ok")
	sc.finish(nil)
	w.expectChange(t)
	if st := e.row(t, v.ID).State; st != "active" {
		t.Fatalf("state %q at the notification", st)
	}
}

func TestCreateThatFailsPublishesTheDeletion(t *testing.T) {
	e := newEnv(t)
	w := e.watch(t)
	sc := newPairingScript(e.fc)
	sc.send(needsAuth, "QR1", "scan")
	in := webInput()
	in.ClientID = e.tn.ClientA1.String()
	v, _, err := e.svc.Create(context.Background(), e.owner, in)
	if err != nil {
		t.Fatal(err)
	}
	w.drain()
	sc.finish(status.Error(codes.Unavailable, "x"))
	w.expectChange(t)
	if e.rowExists(t, v.ID) {
		t.Fatal("the row remains at the notification")
	}
}

func TestRePairPublishesAtTheEnd(t *testing.T) {
	e := newEnv(t)
	in := webInput()
	in.ClientID = e.tn.ClientA1.String()
	v := e.createPaired(t, in)
	sc := newPairingScript(e.fc)
	sc.send(needsAuth, "QR9", "scan")
	w := e.watch(t)
	hook := e.hookRecorder()
	if _, err := e.svc.Pair(context.Background(), e.owner, v.ID, instances.PairInput{Pairing: "qr"}); err != nil {
		t.Fatal(err)
	}
	w.expectQuiet(t)
	sc.send(connected, "", "ok")
	sc.finish(nil)
	w.expectChange(t)
	if r := e.row(t, v.ID); r.LastStatus == nil || *r.LastStatus != "CONNECTED" {
		t.Fatalf("row %+v at the notification", r)
	}
	expectHook(t, hook, v.ID)
}

func TestRePairThatFailsDoesNotTriggerAStatusCheck(t *testing.T) {
	e := newEnv(t)
	v := e.createPaired(t, webInput())
	sc := newPairingScript(e.fc)
	sc.send(needsAuth, "QR9", "scan")
	hook := e.hookRecorder()
	if _, err := e.svc.Pair(context.Background(), e.owner, v.ID, instances.PairInput{Pairing: "qr"}); err != nil {
		t.Fatal(err)
	}
	sc.finish(status.Error(codes.Unavailable, "x"))
	eventually(t, func() bool { return !e.running(v.ID) })
	expectNoHook(t, hook)
}

func TestUpdatePublishesAfterTheWrite(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	w := e.watch(t)
	hook := e.hookRecorder()
	name := "New name"
	if _, err := e.svc.Update(context.Background(), e.owner, row.ID, instances.Change{DisplayName: &name}); err != nil {
		t.Fatal(err)
	}
	w.expectChange(t)
	if got := e.row(t, row.ID).DisplayName; got != name {
		t.Fatalf("name %q at the notification", got)
	}
	expectNoHook(t, hook) // the core did not change
}

func TestUpdateOfTheCoreTriggersAStatusCheck(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	w := e.watch(t)
	hook := e.hookRecorder()
	off := false
	if _, err := e.svc.Update(context.Background(), e.owner, row.ID, instances.Change{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	w.expectChange(t)
	expectHook(t, hook, row.ID)
}

func TestARefusedUpdateDoesNotPublish(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	w := e.watch(t)
	hook := e.hookRecorder()
	e.fc.SetError("UpdateInstanceConfig", status.Error(codes.FailedPrecondition, "no"))
	off := false
	if _, err := e.svc.Update(context.Background(), e.owner, row.ID, instances.Change{Enabled: &off}); err == nil {
		t.Fatal("no error")
	}
	empty := ""
	if _, err := e.svc.Update(context.Background(), e.owner, row.ID, instances.Change{DisplayName: &empty}); err == nil {
		t.Fatal("no error")
	}
	w.expectQuiet(t)
	expectNoHook(t, hook)
}

func TestRemovePublishesAfterTheDeletion(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	w := e.watch(t)
	removed, err := e.svc.Remove(context.Background(), e.owner, row.ID)
	if err != nil || !removed {
		t.Fatalf("removed %v, %v", removed, err)
	}
	if !changed(w.a1, time.Second) {
		t.Fatal("no notification")
	}
	if changed(w.a2, 50*time.Millisecond) {
		t.Fatal("a foreign client got a notification")
	}
	if e.rowExists(t, row.ID) {
		t.Fatal("the row remains")
	}
}

func TestRemoveOfAMissingRowPublishes(t *testing.T) {
	e := newEnv(t)
	row := e.seed(t, testutil.InstanceSeed{State: "missing"})
	w := e.watch(t)
	if _, err := e.svc.Remove(context.Background(), e.owner, row.ID); err != nil {
		t.Fatal(err)
	}
	w.expectChange(t)
}

func TestARefusedRemoveDoesNotPublish(t *testing.T) {
	e := newEnv(t)
	row := e.seed(t, testutil.InstanceSeed{State: "creating"})
	w := e.watch(t)
	if _, err := e.svc.Remove(context.Background(), e.owner, row.ID); err == nil {
		t.Fatal("no error")
	}
	if _, err := e.svc.Remove(context.Background(), e.userA2, row.ID); err == nil {
		t.Fatal("no error")
	}
	w.expectQuiet(t)
}

func TestRecoverPublishesForEachChangedRow(t *testing.T) {
	e := newEnv(t)
	row := e.seed(t, testutil.InstanceSeed{State: "creating", CreatedAt: e.clk.Now().Add(-5 * time.Minute)})
	w := e.watch(t)
	infos := []core.InstanceInfo{{CoreName: row.CoreName, Provider: "WHATSAPP_WEB", Status: "NEEDS_AUTH", Enabled: true}}
	if err := e.svc.Recover(context.Background(), infos); err != nil {
		t.Fatal(err)
	}
	w.expectChange(t)
	if st := e.row(t, row.ID).State; st != "active" {
		t.Fatalf("state %q", st)
	}
}

func TestRecoverWithNothingToDoDoesNotPublish(t *testing.T) {
	e := newEnv(t)
	e.seed(t, testutil.InstanceSeed{State: "creating", CreatedAt: e.clk.Now().Add(-time.Minute)})
	w := e.watch(t)
	if err := e.svc.Recover(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	w.expectQuiet(t)
}

func TestAServiceWithoutLiveNeverGivesStale(t *testing.T) {
	e := newEnv(t)
	svc := instances.NewService(e.pool, nil, e.clk, e.key, "main", time.Minute)
	row := e.seed(t, testutil.InstanceSeed{State: "missing"})
	if _, err := svc.Remove(context.Background(), e.owner, row.ID); err != nil || e.rowExists(t, row.ID) {
		t.Fatalf("remove: %v", err)
	}
	e.seed(t, testutil.InstanceSeed{})
	vs, err := svc.List(context.Background(), e.owner)
	if err != nil || len(vs) == 0 {
		t.Fatalf("list: %v", err)
	}
	for _, x := range vs {
		if x.StatusStale {
			t.Fatal("a nil Live gives stale")
		}
	}
}
