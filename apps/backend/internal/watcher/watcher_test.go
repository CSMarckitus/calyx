package watcher_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"calyx/backend/internal/live"
	"calyx/backend/internal/tenancy"
	"calyx/backend/internal/testutil"
)

func TestRoundStoresTheObservedStatus(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	w := e.newWatcher()
	in := e.addInstance(t, testutil.InstanceSeed{}, stConnected)
	round(t, w)
	t1 := e.clk.Now()
	r := e.row(t, in.ID)
	if r.LastStatus == nil || *r.LastStatus != "CONNECTED" || r.LastStatusAt == nil || !r.LastStatusAt.Equal(t1) ||
		r.StatusCheckedAt == nil || !r.StatusCheckedAt.Equal(t1) {
		t.Fatalf("row %+v", r)
	}
	e.clk.Advance(30 * time.Second)
	round(t, w)
	r = e.row(t, in.ID)
	if !r.LastStatusAt.Equal(t1) || !r.StatusCheckedAt.Equal(e.clk.Now()) {
		t.Fatalf("after the second round: last_status_at %v checked %v", r.LastStatusAt, r.StatusCheckedAt)
	}
	e.fc.SetStatus(in.CoreName, stDisconnected)
	e.clk.Advance(30 * time.Second)
	round(t, w)
	r = e.row(t, in.ID)
	if *r.LastStatus != "DISCONNECTED" || !r.LastStatusAt.Equal(e.clk.Now()) {
		t.Fatalf("after the change: %+v", r)
	}
}

func TestRoundMarksAnAbsentInstanceMissing(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{}, stConnected)
	e.fc.DropInstance(in.CoreName)
	round(t, e.newWatcher())
	if got := e.row(t, in.ID).State; got != "missing" {
		t.Fatalf("state %s", got)
	}
}

func TestRoundBringsBackAMissingInstance(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{State: "missing"}, stConnected)
	round(t, e.newWatcher())
	r := e.row(t, in.ID)
	if r.State != "active" || r.LastStatus == nil || *r.LastStatus != "CONNECTED" {
		t.Fatalf("row %+v", r)
	}
}

func TestRoundDoesNotTouchCreatingRows(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{State: "creating"}, stConnected)
	e.fc.DropInstance(in.CoreName)
	before := e.row(t, in.ID)
	round(t, e.newWatcher())
	after := e.row(t, in.ID)
	if after.State != "creating" || after.LastStatus != nil || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("row %+v", after)
	}
}

func TestFirstFailedCheckOnlyCounts(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stConnected)
	before := e.row(t, in.ID)
	e.fc.SetUnavailable(true)
	if err := e.newWatcher().Round(context.Background()); err == nil {
		t.Fatal("no error")
	}
	cs := e.coreStatus(t)
	if cs.FailedChecks != 1 || cs.UnreachableSince != nil {
		t.Fatalf("core_status %+v", cs)
	}
	if n := len(e.smtp.Messages()); n != 0 {
		t.Fatalf("mails %d", n)
	}
	after := e.row(t, in.ID)
	if after.State != "active" || *after.LastStatus != "CONNECTED" || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("row changed: %+v", after)
	}
}

func ownerPrincipal(e *env) tenancy.Principal {
	return tenancy.Principal{UserID: e.tn.OwnerID, OrganizationID: e.tn.OrganizationID, Role: tenancy.RoleOwner}
}

func TestSecondFailedCheckMailsTheOperatorOnce(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stConnected)
	w := e.newWatcher()
	e.fc.SetUnavailable(true)
	_ = w.Round(context.Background())
	e.clk.Advance(30 * time.Second)
	_ = w.Round(context.Background())
	cs := e.coreStatus(t)
	if cs.FailedChecks != 2 || cs.UnreachableSince == nil || !cs.UnreachableSince.Equal(e.clk.Now()) || cs.AlertedAt == nil {
		t.Fatalf("core_status %+v", cs)
	}
	ms := e.smtp.Messages()
	if len(ms) != 1 || ms[0].To[0] != "ops@test.example" || !strings.Contains(ms[0].Data, `"main"`) {
		t.Fatalf("mails %+v", ms)
	}
	if n := len(e.mailsTo(e.tn.OwnerEmail)); n != 0 {
		t.Fatalf("owner mails %d", n)
	}
	views, err := e.inst.List(context.Background(), ownerPrincipal(e))
	if err != nil || len(views) == 0 {
		t.Fatalf("List: %v %d", err, len(views))
	}
	for _, v := range views {
		if !v.StatusStale {
			t.Fatal("status_stale is false")
		}
	}
	e.clk.Advance(30 * time.Second)
	_ = w.Round(context.Background())
	if cs := e.coreStatus(t); cs.FailedChecks != 3 {
		t.Fatalf("failed_checks %d", cs.FailedChecks)
	}
	if n := len(e.smtp.Messages()); n != 1 {
		t.Fatalf("mails %d", n)
	}
}

func TestOperatorMailIsRetriedWhenItFails(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	w := e.newWatcher()
	e.smtp.SetFail(true)
	e.fc.SetUnavailable(true)
	_ = w.Round(context.Background())
	_ = w.Round(context.Background())
	if e.coreStatus(t).AlertedAt != nil {
		t.Fatal("alerted_at set")
	}
	e.smtp.SetFail(false)
	_ = w.Round(context.Background())
	if n := len(e.smtp.Messages()); n != 1 || e.coreStatus(t).AlertedAt == nil {
		t.Fatalf("mails %d", n)
	}
}

func TestUnreachableCoreWithMailOff(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	w := e.mailOff()
	e.fc.SetUnavailable(true)
	_ = w.Round(context.Background())
	_ = w.Round(context.Background())
	cs := e.coreStatus(t)
	if cs.UnreachableSince == nil || cs.AlertedAt != nil {
		t.Fatalf("core_status %+v", cs)
	}
}

func TestTheCoreAnswersAgain(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stConnected)
	w := e.newWatcher()
	e.fc.SetUnavailable(true)
	_ = w.Round(context.Background())
	_ = w.Round(context.Background())
	e.fc.SetUnavailable(false)
	e.lv.SetCoreLink(true, "") // status_stale ends only with ok: the link must be up
	e.clk.Advance(30 * time.Second)
	round(t, w)
	cs := e.coreStatus(t)
	if cs.FailedChecks != 0 || cs.UnreachableSince != nil || cs.AlertedAt != nil || cs.LastOkAt == nil || !cs.LastOkAt.Equal(e.clk.Now()) {
		t.Fatalf("core_status %+v", cs)
	}
	views, err := e.inst.List(context.Background(), ownerPrincipal(e))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range views {
		if v.StatusStale {
			t.Fatal("status_stale is true")
		}
	}
}

func TestANewOutageMailsTheOperatorAgain(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	w := e.newWatcher()
	for i := 0; i < 2; i++ {
		e.fc.SetUnavailable(true)
		_ = w.Round(context.Background())
		_ = w.Round(context.Background())
		e.fc.SetUnavailable(false)
		round(t, w)
	}
	if n := len(e.mailsTo("ops@test.example")); n != 2 {
		t.Fatalf("operator mails %d", n)
	}
}

func TestACancelledContextIsNotACoreFailure(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stConnected)
	e.fc.SetDelay("ListInstances", 300*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	if err := e.newWatcher().Round(ctx); err == nil {
		t.Fatal("no error")
	}
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core_status`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("core_status rows %d %v", n, err)
	}
}

func TestARowCreatedDuringTheCoreCallIsNotMissing(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	e.fc.SetDelay("ListInstances", 300*time.Millisecond)
	done := make(chan struct{})
	go func() { defer close(done); _ = e.newWatcher().Round(context.Background()) }()
	time.Sleep(100 * time.Millisecond)
	late := testutil.SeedInstance(t, e.pool, testutil.InstanceSeed{OrganizationID: e.tn.OrganizationID, ClientID: e.tn.ClientA1})
	<-done
	if got := e.row(t, late.ID).State; got != "active" {
		t.Fatalf("state %s", got)
	}
}

// The core is the source of the device identity and the locale: a round copies them when the
// backend missed a write, or after an abandoned re-pair that stored a new identity in the core.
func TestRoundStoresTheIdentityAndTheLocaleOfTheCore(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	w := e.newWatcher()
	in := e.addInstance(t, testutil.InstanceSeed{}, stConnected) // the fake core has CHROME_WINDOWS, es-ES
	if _, err := e.pool.Exec(context.Background(), `UPDATE instances SET device_identity = 'SAFARI_MAC_OS', locale = 'pt-BR' WHERE id = $1`, in.ID); err != nil {
		t.Fatal(err)
	}
	round(t, w)
	r := e.row(t, in.ID)
	if r.DeviceIdentity == nil || *r.DeviceIdentity != "CHROME_WINDOWS" || r.Locale == nil || *r.Locale != "es-ES" {
		t.Fatalf("identity %v, locale %v", r.DeviceIdentity, r.Locale)
	}
}

func TestTheHealthFollowsTheCountedRounds(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	e.lv.SetCoreLink(true, "")
	w := e.newWatcher()
	round(t, w)
	if got := e.lv.Core(); got != live.CoreOK {
		t.Fatalf("core %s after a round", got)
	}
	e.fc.SetUnavailable(true)
	_ = w.Round(context.Background())
	if got := e.lv.Core(); got != live.CoreOK {
		t.Fatalf("core %s after a first failure", got)
	}
	_ = w.Round(context.Background())
	if got := e.lv.Core(); got != live.CoreUnreachable {
		t.Fatalf("core %s after two failures", got)
	}
	e.fc.SetUnavailable(false)
	round(t, w)
	if got := e.lv.Core(); got != live.CoreOK {
		t.Fatalf("core %s after the core answered again", got)
	}
}

func TestARoundThatStartedBeforeTheLinkCameUpDoesNotGiveOK(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	e.fc.SetDelay(testutil.MethodListInstances, 300*time.Millisecond)
	w := e.newWatcher()
	done := make(chan struct{})
	go func() { defer close(done); _ = w.Round(context.Background()) }()
	waitFor(t, "the core call", func() bool { return listCalls(e) == 1 })
	e.lv.SetCoreLink(true, "")
	<-done
	if got := e.lv.Core(); got != live.CoreUnknown {
		t.Fatalf("core %s", got)
	}
	round(t, w)
	if got := e.lv.Core(); got != live.CoreOK {
		t.Fatalf("core %s after a round of the link", got)
	}
}

func TestADatabaseErrorDoesNotChangeTheHealth(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	if _, err := e.pool.Exec(context.Background(), `ALTER TABLE core_status RENAME TO core_status_gone`); err != nil {
		t.Fatal(err)
	}
	e.lv.SetCoreLink(true, "")
	w := e.newWatcher()
	if err := w.Round(context.Background()); err == nil {
		t.Fatal("no error")
	}
	if got := e.lv.Core(); got != live.CoreUnknown {
		t.Fatalf("core %s after a round that could not save", got)
	}
	e.fc.SetUnavailable(true)
	_ = w.Round(context.Background())
	_ = w.Round(context.Background())
	if got := e.lv.Core(); got != live.CoreUnknown {
		t.Fatalf("core %s after failures that could not be saved", got)
	}

	// A success that cannot be saved does not end an outage either.
	e.lv.SetUnreachable()
	e.fc.SetUnavailable(false)
	_ = w.Round(context.Background())
	if got := e.lv.Core(); got != live.CoreUnreachable {
		t.Fatalf("core %s", got)
	}
}

// notified reads one waiting notification of sub.
func notified(sub *live.Subscription) bool {
	select {
	case <-sub.Changed():
		return true
	default:
		return false
	}
}

func TestTheRoundPublishesOnlyAChange(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stConnected)
	sub := e.lv.Subscribe(ownerPrincipal(e))
	defer sub.Close()
	w := e.mailOff()
	round(t, w)
	if notified(sub) {
		t.Fatal("a round with no change published")
	}
	e.fc.SetStatus(in.CoreName, stDisconnected)
	round(t, w)
	if !notified(sub) {
		t.Fatal("a new status was not published")
	}
	round(t, w)
	if notified(sub) {
		t.Fatal("the same status was published again")
	}
	e.clk.Advance(time.Minute)
	e.fc.DropInstance(in.CoreName)
	round(t, w)
	if !notified(sub) {
		t.Fatal("a new state was not published")
	}
}

func TestCheckInstancePublishesAChange(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stConnected)
	sub := e.lv.Subscribe(ownerPrincipal(e))
	defer sub.Close()
	w := e.mailOff()
	w.CheckInstance(context.Background(), in.ID)
	if notified(sub) {
		t.Fatal("a check with no change published")
	}
	e.fc.SetStatus(in.CoreName, stNeedsAuth)
	w.CheckInstance(context.Background(), in.ID)
	if !notified(sub) {
		t.Fatal("a new status was not published")
	}
}

func TestASentMailIsPublished(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stNeedsAuth)
	w := e.newWatcher()
	w.CheckInstance(context.Background(), in.ID) // opens the alert, with no mail
	sub := e.lv.Subscribe(ownerPrincipal(e))
	defer sub.Close()
	e.smtp.SetFail(true)
	round(t, w)
	if notified(sub) {
		t.Fatal("a failed mail was published")
	}
	e.smtp.SetFail(false)
	e.clk.Advance(2 * time.Minute)
	round(t, w)
	if !notified(sub) {
		t.Fatal("the sent mail was not published")
	}
	wantMails(t, e, 1)
}
