package watcher_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"calyx/backend/internal/testutil"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

func subjectOf(data string) string {
	for _, l := range strings.Split(data, "\r\n") {
		if strings.HasPrefix(l, "Subject: ") {
			return strings.TrimPrefix(l, "Subject: ")
		}
	}
	return ""
}

func wantMails(t *testing.T, e *env, n int) {
	t.Helper()
	if got := len(e.smtp.Messages()); got != n {
		t.Fatalf("mails = %d, want %d", got, n)
	}
}

func TestNeedsAuthIsMailedOnceAcrossManyRounds(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stNeedsAuth)
	w := e.newWatcher()
	round(t, w)
	first := e.clk.Now()
	for i := 0; i < 4; i++ {
		e.clk.Advance(30 * time.Second)
		round(t, w)
	}
	ms := e.mailsTo(e.tn.OwnerEmail)
	wantMails(t, e, 1)
	if len(ms) != 1 || subjectOf(ms[0].Data) != "[Calyx] Front desk must be paired again" ||
		!strings.Contains(ms[0].Data, "Acme A1") {
		t.Fatalf("mail %+v", ms)
	}
	// The link opens the frontend; the API line has the API address.
	for _, want := range []string{
		"Open https://calyx.test/instances/" + in.ID.String() + " in Calyx.",
		"With the API: send POST https://calyx.test/api/v1/instances/" + in.ID.String() + "/pair ",
	} {
		if !strings.Contains(ms[0].Data, want) {
			t.Fatalf("the mail has no %q:\n%s", want, ms[0].Data)
		}
	}
	r := e.row(t, in.ID)
	if r.AlertKind == nil || *r.AlertKind != "needs_auth" || r.AlertedAt == nil || !r.AlertedAt.Equal(first) {
		t.Fatalf("row %+v", r)
	}
}

func TestMetaNeedsAuthUsesTheMetaTemplate(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	e.addInstance(t, testutil.InstanceSeed{Provider: "META_CLOUD", LastStatus: strp("CONNECTED")}, stNeedsAuth)
	round(t, e.newWatcher())
	ms := e.smtp.Messages()
	if len(ms) != 1 || !strings.Contains(subjectOf(ms[0].Data), "needs new Meta credentials") {
		t.Fatalf("mails %+v", ms)
	}
}

func TestErrorIsMailedAtOnce(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stError)
	round(t, e.newWatcher())
	ms := e.smtp.Messages()
	if len(ms) != 1 || !strings.Contains(subjectOf(ms[0].Data), "stopped with an error") {
		t.Fatalf("mails %+v", ms)
	}
}

func TestDisconnectedIsMailedOnlyAfterTheGracePeriod(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stDisconnected)
	w := e.newWatcher()
	round(t, w)
	if r := e.row(t, in.ID); r.AlertKind == nil || *r.AlertKind != "disconnected" {
		t.Fatalf("row %+v", r)
	}
	wantMails(t, e, 0)
	e.clk.Advance(4*time.Minute + 59*time.Second)
	round(t, w)
	wantMails(t, e, 0)
	e.clk.Advance(time.Second)
	round(t, w)
	ms := e.smtp.Messages()
	if len(ms) != 1 || !strings.Contains(subjectOf(ms[0].Data), "is disconnected") {
		t.Fatalf("mails %+v", ms)
	}
	e.clk.Advance(time.Minute)
	round(t, w)
	wantMails(t, e, 1)
}

func TestDisconnectedReplacedByNeedsAuthIsMailedAtOnce(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stDisconnected)
	w := e.newWatcher()
	round(t, w)
	wantMails(t, e, 0)
	e.clk.Advance(time.Minute)
	e.fc.SetStatus(in.CoreName, stNeedsAuth)
	round(t, w)
	ms := e.smtp.Messages()
	if len(ms) != 1 || !strings.Contains(subjectOf(ms[0].Data), "must be paired again") {
		t.Fatalf("mails %+v", ms)
	}
	if r := e.row(t, in.ID); !r.AlertOpenedAt.Equal(e.clk.Now()) {
		t.Fatalf("opened %v, now %v", r.AlertOpenedAt, e.clk.Now())
	}
}

func TestNeedsAuthThenErrorMailsBothOnce(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stNeedsAuth)
	w := e.newWatcher()
	round(t, w)
	wantMails(t, e, 1)
	e.fc.SetStatus(in.CoreName, stError)
	e.clk.Advance(30 * time.Second)
	round(t, w)
	wantMails(t, e, 2)
	e.clk.Advance(30 * time.Second)
	round(t, w)
	wantMails(t, e, 2)
}

func TestReconnectionClosesTheAlert(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stNeedsAuth)
	w := e.newWatcher()
	round(t, w)
	wantMails(t, e, 1)
	e.fc.SetStatus(in.CoreName, stConnected)
	e.clk.Advance(30 * time.Second)
	round(t, w)
	r := e.row(t, in.ID)
	if r.AlertKind != nil || r.AlertOpenedAt != nil || r.AlertedAt != nil || r.AlertAttempts != 0 || r.AlertNextAttemptAt != nil {
		t.Fatalf("row %+v", r)
	}
	wantMails(t, e, 1)
	e.fc.SetStatus(in.CoreName, stNeedsAuth)
	e.clk.Advance(30 * time.Second)
	round(t, w)
	wantMails(t, e, 2)
}

func TestAFailedMailIsRetriedWithBackoff(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stNeedsAuth)
	w := e.newWatcher()
	e.smtp.SetFail(true)
	round(t, w)
	r := e.row(t, in.ID)
	if r.AlertAttempts != 1 || r.AlertNextAttemptAt == nil || !r.AlertNextAttemptAt.Equal(e.clk.Now().Add(2*time.Minute)) || e.smtp.MailAttempts() != 1 {
		t.Fatalf("row %+v attempts %d", r, e.smtp.MailAttempts())
	}
	e.clk.Advance(time.Minute)
	round(t, w)
	if e.smtp.MailAttempts() != 1 {
		t.Fatalf("attempts %d", e.smtp.MailAttempts())
	}
	e.clk.Advance(time.Minute)
	round(t, w)
	r = e.row(t, in.ID)
	if e.smtp.MailAttempts() != 2 || r.AlertAttempts != 2 || !r.AlertNextAttemptAt.Equal(e.clk.Now().Add(4*time.Minute)) {
		t.Fatalf("row %+v attempts %d", r, e.smtp.MailAttempts())
	}
	e.smtp.SetFail(false)
	e.clk.Advance(4 * time.Minute)
	round(t, w)
	wantMails(t, e, 1)
	if e.row(t, in.ID).AlertedAt == nil {
		t.Fatal("alerted_at not set")
	}
	e.clk.Advance(time.Hour)
	round(t, w)
	wantMails(t, e, 1)
}

func TestADisabledInstanceIsNeverAlerted(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	off := false
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED"), Enabled: &off}, stNeedsAuth)
	round(t, e.newWatcher())
	wantMails(t, e, 0)
	if r := e.row(t, in.ID); r.AlertKind != nil || r.AlertOpenedAt != nil {
		t.Fatalf("row %+v", r)
	}
}

func TestAMissingInstanceIsMailedOnce(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stConnected)
	e.fc.DropInstance(in.CoreName)
	w := e.newWatcher()
	round(t, w)
	r := e.row(t, in.ID)
	if r.State != "missing" || r.AlertKind == nil || *r.AlertKind != "missing" {
		t.Fatalf("row %+v", r)
	}
	ms := e.smtp.Messages()
	if len(ms) != 1 || !strings.Contains(subjectOf(ms[0].Data), "no longer exists in the core") {
		t.Fatalf("mails %+v", ms)
	}
	e.fc.AddInstance(in.CoreName, 1, stConnected)
	e.clk.Advance(30 * time.Second)
	round(t, w)
	r = e.row(t, in.ID)
	if r.State != "active" || r.AlertKind != nil || r.AlertedAt != nil {
		t.Fatalf("row %+v", r)
	}
	wantMails(t, e, 1)
}

func TestNoActiveOwnerCountsAsAFailedMail(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stNeedsAuth)
	if _, err := e.pool.Exec(context.Background(), `UPDATE users SET disabled_at = now() WHERE id = $1`, e.tn.OwnerID); err != nil {
		t.Fatal(err)
	}
	round(t, e.newWatcher())
	wantMails(t, e, 0)
	r := e.row(t, in.ID)
	if r.AlertAttempts != 1 || r.AlertNextAttemptAt == nil || !r.AlertNextAttemptAt.Equal(e.clk.Now().Add(2*time.Minute)) {
		t.Fatalf("row %+v", r)
	}
}

func TestMailOffKeepsTheAlertVisible(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stNeedsAuth)
	round(t, e.mailOff())
	r := e.row(t, in.ID)
	if r.AlertKind == nil || *r.AlertKind != "needs_auth" || r.AlertedAt != nil || r.AlertAttempts != 0 {
		t.Fatalf("row %+v", r)
	}
}

func TestARestartSendsADueAlertOnce(t *testing.T) { // KEY
	e := newEnv(t, testutil.FakeSMTPOptions{})
	// Part A: the status changed while the backend was stopped.
	e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stNeedsAuth)
	round(t, e.newWatcher())
	wantMails(t, e, 1)
	e.clk.Advance(30 * time.Second)
	round(t, e.newWatcher())
	wantMails(t, e, 1)

	// Part B: the mail fails, then the backend restarts.
	b := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stNeedsAuth)
	e.smtp.SetFail(true)
	e.clk.Advance(30 * time.Second)
	round(t, e.newWatcher())
	wantMails(t, e, 1)
	if r := e.row(t, b.ID); r.AlertAttempts != 1 {
		t.Fatalf("attempts %d", r.AlertAttempts)
	}
	e.smtp.SetFail(false)
	e.clk.Advance(2 * time.Minute)
	round(t, e.newWatcher())
	wantMails(t, e, 2)
	round(t, e.newWatcher())
	wantMails(t, e, 2)
}

func TestTwoWatchersInARowNeverMailTwice(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stNeedsAuth)
	round(t, e.newWatcher())
	round(t, e.newWatcher())
	wantMails(t, e, 1)
}

func TestAHungMailerCostsOneAttemptPerRound(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	a := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stNeedsAuth)
	time.Sleep(10 * time.Millisecond)
	b := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stNeedsAuth)
	e.smtp.SetFail(true)
	w := e.newWatcher()
	round(t, w)
	if n := e.smtp.MailAttempts(); n != 1 {
		t.Fatalf("send attempts in one round = %d, want 1", n)
	}
	ra, rb := e.row(t, a.ID), e.row(t, b.ID)
	// One of the two was tried; the other stays due with no attempt counted.
	if ra.AlertAttempts+rb.AlertAttempts != 1 || ra.AlertKind == nil || rb.AlertKind == nil {
		t.Fatalf("attempts %d and %d", ra.AlertAttempts, rb.AlertAttempts)
	}
	e.smtp.SetFail(false)
	e.clk.Advance(30 * time.Second)
	round(t, w)
	e.clk.Advance(5 * time.Minute)
	round(t, w)
	wantMails(t, e, 2)
}

func TestEachOwnerGetsOnlyTheirOwnMail(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	other := testutil.SeedTenant(t, e.pool, testutil.TenantOptions{Name: "Other", WhatsAppWebAllowed: true})
	e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stNeedsAuth)
	e.addInstance(t, testutil.InstanceSeed{OrganizationID: other.OrganizationID, ClientID: other.ClientA1, LastStatus: strp("CONNECTED")}, stNeedsAuth)
	round(t, e.newWatcher())
	wantMails(t, e, 2)
	for addr, org := range map[string]string{e.tn.OwnerEmail: "Acme", other.OwnerEmail: "Other"} {
		ms := e.mailsTo(addr)
		if len(ms) != 1 || !strings.Contains(ms[0].Data, org+" A1") {
			t.Fatalf("mails of %s: %+v", addr, ms)
		}
	}
	if strings.Contains(e.mailsTo(e.tn.OwnerEmail)[0].Data, "Other") {
		t.Fatal("the mail of one owner names the other organization")
	}
}

func TestAnAlertThatChangedDuringItsMailIsNotMarked(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stNeedsAuth)
	g := newGate()
	w := e.watcherWith(&hookSender{next: e.smtpSender(), hook: g.wait})
	done := make(chan struct{})
	go func() { defer close(done); _ = w.Round(context.Background()) }()
	g.waitEntered(t) // the needs_auth mail is in progress
	e.clk.Advance(time.Second)
	e.fc.SetStatus(in.CoreName, stError)
	w.CheckInstance(context.Background(), in.ID)
	close(g.release)
	<-done
	// The needs_auth mail did not mark the error alert, so the error alert got its own mail.
	ms := e.smtp.Messages()
	if len(ms) != 2 || !strings.Contains(subjectOf(ms[0].Data), "must be paired again") ||
		!strings.Contains(subjectOf(ms[1].Data), "stopped with an error") {
		t.Fatalf("mails %+v", ms)
	}
	if r := e.row(t, in.ID); *r.AlertKind != "error" || r.AlertedAt == nil {
		t.Fatalf("row %+v", r)
	}
}

func TestAReopenedAlertIsNotMarkedByTheMailOfTheOldOne(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stNeedsAuth)
	g := newGate()
	w := e.watcherWith(&hookSender{next: e.smtpSender(), hook: g.wait})
	done := make(chan struct{})
	go func() { defer close(done); _ = w.Round(context.Background()) }()
	g.waitEntered(t)
	// The same kind opens again at a later time: only the opening time differs.
	e.fc.SetStatus(in.CoreName, stConnected)
	w.CheckInstance(context.Background(), in.ID)
	e.clk.Advance(time.Second)
	e.fc.SetStatus(in.CoreName, stNeedsAuth)
	w.CheckInstance(context.Background(), in.ID)
	close(g.release)
	<-done
	wantMails(t, e, 2)
}

func TestOneMailForEachAlert(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	var ids []uuid.UUID
	for _, st := range []corev1.InstanceStatus{stNeedsAuth, stError, stNeedsAuth} {
		ids = append(ids, e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, st).ID)
	}
	w := e.newWatcher()
	round(t, w)
	wantMails(t, e, 3)
	for _, id := range ids {
		if e.row(t, id).AlertedAt == nil {
			t.Fatalf("alerted_at of %s not set", id)
		}
	}
	e.clk.Advance(30 * time.Second)
	round(t, w)
	wantMails(t, e, 3)
}

func TestAFailedSaveStopsTheMails(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stNeedsAuth)
	e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stError)
	failUpdates := func(ctx context.Context, n int) error {
		if n == 1 { // every write of an instance fails from the first mail on
			_, err := e.pool.Exec(ctx, `
				CREATE FUNCTION fail_update() RETURNS trigger LANGUAGE plpgsql AS $$
				BEGIN RAISE EXCEPTION 'no writes'; END $$;
				CREATE TRIGGER fail_update BEFORE UPDATE ON instances FOR EACH ROW EXECUTE FUNCTION fail_update();`)
			return err
		}
		return nil
	}
	w := e.watcherWith(&hookSender{next: e.smtpSender(), hook: failUpdates})
	round(t, w)
	if n := e.smtp.MailAttempts(); n != 1 {
		t.Fatalf("send attempts %d, want 1", n)
	}
}

// No false mail: the stored alert is older than the grace period, but nobody confirmed the state.
func TestNoMailForAnOldDisconnectedAlertWhileTheCoreIsDown(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("DISCONNECTED")}, stDisconnected)
	e.openAlert(t, in.ID, "disconnected", 10*time.Minute)
	e.fc.SetUnavailable(true)
	w := e.newWatcher()
	_ = w.Round(context.Background())
	_ = w.Round(context.Background())
	if n := len(e.mailsTo(e.tn.OwnerEmail)); n != 0 {
		t.Fatalf("owner mails %d", n)
	}
}

func TestNoMailInTheFirstRoundAfterAnOutage(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("DISCONNECTED")}, stConnected)
	e.openAlert(t, in.ID, "disconnected", 10*time.Minute)
	w := e.newWatcher()
	e.fc.SetUnavailable(true)
	_ = w.Round(context.Background())
	_ = w.Round(context.Background())
	e.fc.SetUnavailable(false)
	round(t, w)
	if n := len(e.mailsTo(e.tn.OwnerEmail)); n != 0 {
		t.Fatalf("owner mails %d", n)
	}
	if r := e.row(t, in.ID); r.AlertKind != nil {
		t.Fatalf("alert %v still open", *r.AlertKind)
	}
}

func TestNoMailForAnInstanceInRemoving(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{State: "removing", LastStatus: strp("NEEDS_AUTH")}, stNeedsAuth)
	e.openAlert(t, in.ID, "needs_auth", time.Minute)
	round(t, e.newWatcher())
	wantMails(t, e, 0)
}

// A removal can start after the round and before the mail of its alert.
func TestNoMailForAnInstanceThatStartedItsRemovalAfterTheRound(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	first := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("NEEDS_AUTH")}, stNeedsAuth)
	e.openAlert(t, first.ID, "needs_auth", 2*time.Minute)
	second := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("NEEDS_AUTH")}, stNeedsAuth)
	e.openAlert(t, second.ID, "needs_auth", time.Minute)
	remove := func(ctx context.Context, n int) error {
		if n == 1 {
			_, err := e.pool.Exec(ctx, `UPDATE instances SET state = 'removing' WHERE id = $1`, second.ID)
			return err
		}
		return nil
	}
	round(t, e.watcherWith(&hookSender{next: e.smtpSender(), hook: remove}))
	wantMails(t, e, 1)
}

// openAlert writes an open alert that was not mailed, opened the given time ago.
func (e *env) openAlert(t *testing.T, id uuid.UUID, kind string, ago time.Duration) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE instances SET alert_kind = $2, alert_opened_at = $3 WHERE id = $1`, id, kind, e.clk.Now().Add(-ago)); err != nil {
		t.Fatal(err)
	}
}
