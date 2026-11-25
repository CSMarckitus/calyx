package instances_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"calyx/backend/internal/instances"
	"calyx/backend/internal/tenancy"
	"calyx/backend/internal/testutil"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

func webhooksForPair() []instances.WebhookInput {
	return []instances.WebhookInput{
		{URL: "https://a.example/c", Event: "connection.update", Secret: strp("s1")},
		{URL: "https://b.example/m", Event: "message.received", Secret: strp("s2")},
		{URL: "https://c.example/r", Event: "whatsmeow.Receipt"},
	}
}

func TestRePairSendsEveryStoredWebhookWithItsSecret(t *testing.T) {
	e := newEnv(t)
	in := webInput()
	in.Webhooks = webhooksForPair()
	v := e.createPaired(t, in)
	sc := newPairingScript(e.fc)
	sc.send(needsAuth, "ABCD1234", "enter")
	if _, err := e.svc.Pair(context.Background(), e.owner, v.ID, instances.PairInput{Pairing: "code", Phone: "34600111222"}); err != nil {
		t.Fatal(err)
	}
	reqs := e.fc.CreateRequests()
	if len(reqs) != 2 {
		t.Fatalf("%d create requests", len(reqs))
	}
	req := reqs[1]
	if req.GetInstanceName() != v.CoreName || req.GetIsUsedOnlyForSendMessages() ||
		req.GetWhatsappWeb().GetPairingCode().GetPhoneNumber() != "34600111222" {
		t.Fatalf("request %v", req)
	}
	hooks := req.GetWebhooks()
	sort.Slice(hooks, func(i, j int) bool { return hooks[i].GetEvent() < hooks[j].GetEvent() })
	want := []struct {
		url, event string
		secret     *string
	}{
		{"https://a.example/c", "connection.update", strp("s1")},
		{"https://b.example/m", "message.received", strp("s2")},
		{"https://c.example/r", "whatsmeow.Receipt", nil},
	}
	if len(hooks) != 3 {
		t.Fatalf("webhooks %v", hooks)
	}
	for i, w := range want {
		h := hooks[i]
		if h.GetUrl() != w.url || h.GetEvent() != w.event || (h.Secret == nil) != (w.secret == nil) ||
			(w.secret != nil && h.GetSecret() != *w.secret) {
			t.Fatalf("webhook %d: %v", i, h)
		}
	}
}

func TestRePairSendsTheSendOnlyFlag(t *testing.T) {
	e := newEnv(t)
	in := webInput()
	in.SendOnly = true
	v := e.createPaired(t, in)
	sc := newPairingScript(e.fc)
	sc.send(needsAuth, "QR9", "scan")
	if _, err := e.svc.Pair(context.Background(), e.owner, v.ID, instances.PairInput{Pairing: "qr"}); err != nil {
		t.Fatal(err)
	}
	if reqs := e.fc.CreateRequests(); !reqs[len(reqs)-1].GetIsUsedOnlyForSendMessages() {
		t.Fatal("the send-only flag was not sent")
	}
}

func TestRePairAnswersTheFirstUpdate(t *testing.T) {
	e := newEnv(t)
	v := e.createPaired(t, webInput())
	sc := newPairingScript(e.fc)
	sc.send(needsAuth, "QR9", "scan")
	u, err := e.svc.Pair(context.Background(), e.owner, v.ID, instances.PairInput{Pairing: "qr"})
	if err != nil || u.QRText == nil || *u.QRText != "QR9" {
		t.Fatalf("update %+v, %v", u, err)
	}
}

func TestRePairSuccessSetsConnected(t *testing.T) {
	e := newEnv(t)
	in := webInput()
	in.Webhooks = webhooksForPair()
	v := e.createPaired(t, in)
	sc := newPairingScript(e.fc)
	sc.send(needsAuth, "QR9", "scan")
	if _, err := e.svc.Pair(context.Background(), e.owner, v.ID, instances.PairInput{Pairing: "qr"}); err != nil {
		t.Fatal(err)
	}
	sc.send(connected, "", "ok")
	sc.finish(nil)
	eventually(t, func() bool { r := e.row(t, v.ID); return r.LastStatus != nil && *r.LastStatus == "CONNECTED" })
	if r := e.row(t, v.ID); r.State != "active" {
		t.Fatalf("state %q", r.State)
	}
	if got := e.count(t, "webhooks"); got != 3 {
		t.Fatalf("%d webhooks", got)
	}
}

func TestRePairFailureKeepsTheRow(t *testing.T) {
	e := newEnv(t)
	in := webInput()
	in.Webhooks = webhooksForPair()
	v := e.createPaired(t, in)
	sc := newPairingScript(e.fc)
	sc.send(needsAuth, "QR9", "scan")
	if _, err := e.svc.Pair(context.Background(), e.owner, v.ID, instances.PairInput{Pairing: "qr"}); err != nil {
		t.Fatal(err)
	}
	sc.finish(status.Error(codes.Unavailable, "x"))
	eventually(t, func() bool { return !e.running(v.ID) })
	r := e.row(t, v.ID)
	if r.State != "active" || r.LastStatus == nil || *r.LastStatus != "NEEDS_AUTH" || e.count(t, "webhooks") != 3 {
		t.Fatalf("row %+v", r)
	}
}

func TestRePairExpiryKeepsTheRow(t *testing.T) {
	e := newEnv(t)
	v := e.createPaired(t, webInput())
	sc := newPairingScript(e.fc)
	sc.send(needsAuth, "QR9", "scan")
	if _, err := e.svc.Pair(context.Background(), e.owner, v.ID, instances.PairInput{Pairing: "qr"}); err != nil {
		t.Fatal(err)
	}
	ch, err := e.svc.FollowPairing(context.Background(), e.owner, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	next(t, ch) // the latest update
	eventually(t, func() bool { return e.clk.PendingTimers() >= 1 })
	e.clk.Advance(3 * time.Minute)
	if ev := next(t, ch); ev.Kind != "failed" || ev.FailCode != "pairing_expired" {
		t.Fatalf("event %+v", ev)
	}
	r := e.row(t, v.ID)
	if r.State != "active" || *r.LastStatus != "NEEDS_AUTH" {
		t.Fatalf("row %+v", r)
	}
}

func TestRePairRefusals(t *testing.T) {
	st := func(s string) *string { return &s }
	cases := []struct {
		name    string
		seed    testutil.InstanceSeed
		block   bool
		pairing string
		phone   string
		status  int
		code    string
	}{
		{"meta", testutil.InstanceSeed{Provider: "META_CLOUD", LastStatus: st("NEEDS_AUTH")}, false, "qr", "", 409, "pairing_not_supported"},
		{"error", testutil.InstanceSeed{LastStatus: st("ERROR")}, false, "qr", "", 409, "instance_in_error"},
		{"connected", testutil.InstanceSeed{LastStatus: st("CONNECTED")}, false, "qr", "", 409, "instance_connected"},
		{"disconnected", testutil.InstanceSeed{LastStatus: st("DISCONNECTED")}, false, "qr", "", 409, "instance_connected"},
		{"creating", testutil.InstanceSeed{State: "creating"}, false, "qr", "", 409, "pairing_in_progress"},
		{"missing", testutil.InstanceSeed{State: "missing", LastStatus: st("NEEDS_AUTH")}, false, "qr", "", 409, "conflict"},
		{"removing", testutil.InstanceSeed{State: "removing", LastStatus: st("NEEDS_AUTH")}, false, "qr", "", 409, "conflict"},
		{"disabled", testutil.InstanceSeed{LastStatus: st("NEEDS_AUTH"), Enabled: new(false)}, false, "qr", "", 409, "conflict"},
		{"web blocked", testutil.InstanceSeed{LastStatus: st("NEEDS_AUTH")}, true, "qr", "", 403, "whatsapp_web_not_allowed"},
		{"bad phone", testutil.InstanceSeed{LastStatus: st("NEEDS_AUTH")}, false, "code", "12", 400, "invalid_request"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			if c.block {
				e.exec(t, `UPDATE organizations SET whatsapp_web_allowed = false WHERE id = $1`, e.tn.OrganizationID)
			}
			row := e.seed(t, c.seed)
			_, err := e.svc.Pair(context.Background(), e.owner, row.ID, instances.PairInput{Pairing: c.pairing, Phone: c.phone})
			requireCode(t, err, c.status, c.code)
			if len(e.fc.CreateRequests()) != 0 {
				t.Fatal("a core request was sent")
			}
		})
	}
	t.Run("other client", func(t *testing.T) {
		e := newEnv(t)
		row := e.seed(t, testutil.InstanceSeed{LastStatus: st("NEEDS_AUTH")})
		_, err := e.svc.Pair(context.Background(), e.userA2, row.ID, instances.PairInput{Pairing: "qr"})
		requireCode(t, err, 404, "not_found")
	})
}

func TestSecondPairWhileOneRunsIsPairingInProgress(t *testing.T) {
	e := newEnv(t)
	v := e.createPaired(t, webInput())
	sc := newPairingScript(e.fc)
	sc.send(needsAuth, "QR9", "scan")
	if _, err := e.svc.Pair(context.Background(), e.owner, v.ID, instances.PairInput{Pairing: "qr"}); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Pair(context.Background(), e.owner, v.ID, instances.PairInput{Pairing: "qr"})
	requireCode(t, err, 409, "pairing_in_progress")
	if n := len(e.fc.CreateRequests()); n != 2 {
		t.Fatalf("%d create requests, want 2", n)
	}
}

func TestFollowPairingAfterTheFollowerLeft(t *testing.T) {
	e := newEnv(t)
	sc := newPairingScript(e.fc)
	sc.send(needsAuth, "QR1", "scan")
	v, _, err := e.svc.Create(context.Background(), e.owner, webInput())
	if err != nil {
		t.Fatal(err)
	}
	ctxA, cancelA := context.WithCancel(context.Background())
	chA, err := e.svc.FollowPairing(ctxA, e.owner, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	next(t, chA)
	cancelA()
	closed(t, chA)
	sc.send(needsAuth, "QR2", "scan")
	eventually(t, func() bool {
		ch, err := e.svc.FollowPairing(context.Background(), e.owner, v.ID)
		if err != nil {
			return false
		}
		ev := next(t, ch)
		return ev.Update.QRText != nil && *ev.Update.QRText == "QR2"
	})
	ch, err := e.svc.FollowPairing(context.Background(), e.owner, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ev := next(t, ch); ev.Kind != "update" || *ev.Update.QRText != "QR2" {
		t.Fatalf("event %+v", ev)
	}
	sc.send(connected, "", "ok")
	sc.finish(nil)
	if ev := next(t, ch); ev.Kind != "update" || ev.Update.Status != "CONNECTED" {
		t.Fatalf("event %+v", ev)
	}
	if ev := next(t, ch); ev.Kind != "done" {
		t.Fatalf("event %+v", ev)
	}
	closed(t, ch)
}

func TestFollowPairingWithoutASessionIsNotFound(t *testing.T) {
	e := newEnv(t)
	row := e.seed(t, testutil.InstanceSeed{})
	_, err := e.svc.FollowPairing(context.Background(), e.owner, row.ID)
	requireCode(t, err, 404, "not_found")
	holdOpenScript(e.fc)
	v, _, err := e.svc.Create(context.Background(), e.userA1, webInput())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.FollowPairing(context.Background(), e.userA2, v.ID)
	requireCode(t, err, 404, "not_found")
}

func TestCancelPairingDeletesACreatingRow(t *testing.T) {
	e := newEnv(t)
	sc := newPairingScript(e.fc)
	sc.send(needsAuth, "QR1", "scan")
	v, _, err := e.svc.Create(context.Background(), e.owner, webInput())
	if err != nil {
		t.Fatal(err)
	}
	ch, err := e.svc.FollowPairing(context.Background(), e.owner, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.CancelPairing(context.Background(), e.owner, v.ID); err != nil {
		t.Fatal(err)
	}
	if e.rowExists(t, v.ID) {
		t.Fatal("the row exists after CancelPairing returned")
	}
	select {
	case <-sc.ctxDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the core stream was not cancelled")
	}
	var last instances.PairingEvent
	for ev := range ch {
		last = ev
	}
	if last.Kind != "failed" || last.FailCode != "pairing_cancelled" {
		t.Fatalf("last event %+v", last)
	}
	requireCode(t, e.svc.CancelPairing(context.Background(), e.owner, v.ID), 404, "not_found")
}

func TestCancelPairingOfARePairKeepsTheRow(t *testing.T) {
	e := newEnv(t)
	v := e.createPaired(t, webInput())
	sc := newPairingScript(e.fc)
	sc.send(needsAuth, "QR9", "scan")
	if _, err := e.svc.Pair(context.Background(), e.owner, v.ID, instances.PairInput{Pairing: "qr"}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.CancelPairing(context.Background(), e.owner, v.ID); err != nil {
		t.Fatal(err)
	}
	r := e.row(t, v.ID)
	if r.State != "active" || *r.LastStatus != "NEEDS_AUTH" {
		t.Fatalf("row %+v", r)
	}
}

func TestCancelAllPairingsAtShutdown(t *testing.T) {
	e := newEnv(t)
	holdOpenScript(e.fc)
	a, _, err := e.svc.Create(context.Background(), e.owner, webInput())
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := e.svc.Create(context.Background(), e.userA1, webInput())
	if err != nil {
		t.Fatal(err)
	}
	e.svc.CancelAllPairings()
	if e.rowExists(t, a.ID) || e.rowExists(t, b.ID) {
		t.Fatal("a creating row remains")
	}
	if e.running(a.ID) || e.running(b.ID) {
		t.Fatal("a session still runs")
	}
}

// A Remove that sets "removing" after Pair read the row must stop the pairing before the core
// call. The test holds a lock on the organizations table, so Pair waits after its first read of
// the row. Then it sets the state and lets Pair go on.
func TestPairStopsWhenTheRowIsRemovingAfterStart(t *testing.T) {
	e := newEnv(t)
	before, err := pairWithRowChange(t, e, `UPDATE instances SET state = 'removing' WHERE id = $1`)
	if err == nil {
		t.Fatal("Pair succeeded on a row that is being removed")
	}
	if n := len(e.fc.CreateRequests()); n != before {
		t.Fatalf("the core got %d create request(s) for a removing instance", n-before)
	}
}

// A Remove that ends after Pair read the row deletes the row. Pair answers 404, not 500.
func TestPairAnswers404WhenTheRowIsDeletedAfterStart(t *testing.T) {
	e := newEnv(t)
	before, err := pairWithRowChange(t, e, `DELETE FROM instances WHERE id = $1`)
	requireCode(t, err, 404, "not_found")
	if n := len(e.fc.CreateRequests()); n != before {
		t.Fatalf("the core got %d create request(s) for a deleted instance", n-before)
	}
}

// pairWithRowChange runs Pair, holds it after its first read of the row, runs changeSQL on the
// row and lets Pair go on. It returns the number of core create requests before Pair and the
// error of Pair.
func pairWithRowChange(t *testing.T, e *env, changeSQL string) (int, error) {
	t.Helper()
	v := e.createPaired(t, webInput())
	before := len(e.fc.CreateRequests())
	ctx := context.Background()
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `LOCK TABLE organizations IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := e.svc.Pair(ctx, e.owner, v.ID, instances.PairInput{Pairing: "qr"})
		done <- err
	}()
	// Wait until Pair is blocked on the lock: it already read the row as "active".
	deadline := time.Now().Add(3 * time.Second)
	for {
		var n int
		if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND datname = current_database()`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Pair did not reach the lock")
		}
		time.Sleep(5 * time.Millisecond)
	}
	e.exec(t, changeSQL, v.ID)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		return before, err
	case <-time.After(3 * time.Second):
		t.Fatal("Pair did not answer")
	}
	return before, nil
}

func TestRePairSendsOnlyTheIdentityAndTheLocaleThatTheUserGave(t *testing.T) {
	e := newEnv(t)
	in := webInput()
	in.DeviceIdentity = strp("FIREFOX_LINUX")
	v := e.createPaired(t, in)

	sc := newPairingScript(e.fc)
	sc.send(needsAuth, "QR9", "scan")
	sc.send(connected, "", "ok")
	sc.finish(nil)
	if _, err := e.svc.Pair(context.Background(), e.owner, v.ID, instances.PairInput{Pairing: "qr"}); err != nil {
		t.Fatal(err)
	}
	reqs := e.fc.CreateRequests()
	web := reqs[len(reqs)-1].GetWhatsappWeb()
	if web.GetDeviceIdentity() != corev1.DeviceIdentity_DEVICE_IDENTITY_UNSPECIFIED || web.Locale != nil {
		t.Fatalf("values that the user did not give were sent: %v", web)
	}
	// The core picked a new identity (CHROME_WINDOWS in the fake) and kept the stored locale.
	eventually(t, func() bool {
		row := e.row(t, v.ID)
		return row.DeviceIdentity != nil && *row.DeviceIdentity == "CHROME_WINDOWS"
	})
	if row := e.row(t, v.ID); row.Locale == nil || *row.Locale != "es-ES" {
		t.Fatalf("locale %v", row.Locale)
	}
}

func TestRePairSendsTheIdentityAndTheLocaleThatTheUserGave(t *testing.T) {
	e := newEnv(t)
	v := e.createPaired(t, webInput())
	sc := newPairingScript(e.fc)
	sc.send(needsAuth, "QR9", "scan")
	sc.send(connected, "", "ok")
	sc.finish(nil)
	pin := instances.PairInput{Pairing: "qr", DeviceIdentity: strp("SAFARI_MAC_OS"), Locale: strp("pt-BR")}
	if _, err := e.svc.Pair(context.Background(), e.owner, v.ID, pin); err != nil {
		t.Fatal(err)
	}
	reqs := e.fc.CreateRequests()
	web := reqs[len(reqs)-1].GetWhatsappWeb()
	if web.GetDeviceIdentity() != corev1.DeviceIdentity_DEVICE_IDENTITY_SAFARI_MAC_OS || web.GetLocale() != "pt-BR" {
		t.Fatalf("request %v", web)
	}
	eventually(t, func() bool {
		row := e.row(t, v.ID)
		return row.DeviceIdentity != nil && *row.DeviceIdentity == "SAFARI_MAC_OS" && row.Locale != nil && *row.Locale == "pt-BR"
	})
}

func TestRePairRefusesABadIdentityOrLocale(t *testing.T) {
	e := newEnv(t)
	v := e.createPaired(t, webInput())
	for name, pin := range map[string]instances.PairInput{
		"identity": {Pairing: "qr", DeviceIdentity: strp("NETSCAPE_BEOS")},
		"locale":   {Pairing: "qr", Locale: strp("ES")},
	} {
		_, err := e.svc.Pair(context.Background(), e.owner, v.ID, pin)
		requireCode(t, err, 400, "invalid_request")
		_ = name
	}
}

// An instance created before the backend stored a locale has none, and neither has the core.
// A re-pair without a locale must say so with a field error, before any call to the core.
func TestRePairOfAnInstanceWithoutAStoredLocaleNeedsOne(t *testing.T) {
	e := newEnv(t)
	v := e.createPaired(t, webInput())
	e.exec(t, `UPDATE instances SET locale = NULL WHERE id = $1`, v.ID)
	before := len(e.fc.CreateRequests())
	// The answer of the real core, if the request reached it.
	newPairingScript(e.fc).finish(status.Error(codes.InvalidArgument, "locale is required for a new pairing, for example es-ES."))

	_, err := e.svc.Pair(context.Background(), e.owner, v.ID, instances.PairInput{Pairing: "qr"})
	requireCode(t, err, 400, "invalid_request")
	ae, _ := apierrAs(err)
	if len(ae.Errors) != 1 || ae.Errors[0].Field != "locale" {
		t.Fatalf("field errors %+v", ae.Errors)
	}
	if n := len(e.fc.CreateRequests()); n != before {
		t.Fatal("the request reached the core")
	}
}

// The core keeps its webhooks when a re-pair carries none. When the backend has none, a webhook
// that someone added in the core directly must not survive the re-pair.
func TestRePairWithoutWebhooksClearsTheWebhooksOfTheCore(t *testing.T) {
	e := newEnv(t)
	v := e.createPaired(t, webInput())
	in, _ := e.fc.Instance(v.CoreName)
	in.Webhooks = []*corev1.WebhookConfig{{Url: "https://drift.example/x", Event: "message.received"}}
	e.fc.PutInstance(in)

	sc := newPairingScript(e.fc)
	sc.send(needsAuth, "QR9", "scan")
	sc.send(connected, "", "ok")
	sc.finish(nil)
	if _, err := e.svc.Pair(context.Background(), e.owner, v.ID, instances.PairInput{Pairing: "qr"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { got, _ := e.fc.Instance(v.CoreName); return got.Status == connected })
	if got, _ := e.fc.Instance(v.CoreName); len(got.Webhooks) != 0 {
		t.Fatalf("the core kept %v", got.Webhooks)
	}
}

// otherTenant returns the owner of a second organization.
func (e *env) otherTenant(t *testing.T) tenancy.Principal {
	t.Helper()
	tn := testutil.SeedTenant(t, e.pool, testutil.TenantOptions{Name: "Other", WhatsAppWebAllowed: true, Acknowledged: true})
	return tenancy.Principal{UserID: tn.OwnerID, OrganizationID: tn.OrganizationID, Role: tenancy.RoleOwner}
}

// requireKeptFailure follows the pairing as p and wants only the event failed with code.
func (e *env) requireKeptFailure(t *testing.T, p tenancy.Principal, id uuid.UUID, code string) {
	t.Helper()
	ch, err := e.svc.FollowPairing(context.Background(), p, id)
	if err != nil {
		t.Fatalf("FollowPairing: %v", err)
	}
	var evs []instances.PairingEvent
	for ev := range ch {
		evs = append(evs, ev)
	}
	if len(evs) != 1 || evs[0].Kind != "failed" || evs[0].FailCode != code {
		t.Fatalf("events %+v", evs)
	}
}

func (e *env) requireFollowNotFound(t *testing.T, p tenancy.Principal, id uuid.UUID) {
	t.Helper()
	_, err := e.svc.FollowPairing(context.Background(), p, id)
	requireCode(t, err, 404, "not_found")
}

var codesEnded = status.Error(codes.DeadlineExceeded, "The pairing codes ended.")

// A pairing that expires, for a new instance: the row is gone, and the failure stays for 5 minutes
// in its scope.
func TestFollowPairingGivesTheKeptFailureOfANewInstance(t *testing.T) {
	e := newEnv(t)
	other := e.otherTenant(t)
	sc := newPairingScript(e.fc)
	sc.send(needsAuth, "QR1", "scan")
	v, _, err := e.svc.Create(context.Background(), e.userA1, webInput())
	if err != nil {
		t.Fatal(err)
	}
	sc.finish(codesEnded)
	eventually(t, func() bool { return !e.running(v.ID) })
	if e.rowExists(t, v.ID) {
		t.Fatal("the new instance was not deleted")
	}
	e.requireKeptFailure(t, e.userA1, v.ID, "pairing_expired")
	e.requireKeptFailure(t, e.owner, v.ID, "pairing_expired")
	e.requireFollowNotFound(t, e.userA2, v.ID)
	e.requireFollowNotFound(t, other, v.ID)
	e.clk.Advance(5*time.Minute - time.Millisecond)
	e.requireKeptFailure(t, e.userA1, v.ID, "pairing_expired")
	e.clk.Advance(time.Millisecond)
	e.requireFollowNotFound(t, e.userA1, v.ID)
}

// An owner starts the pairing for a client: the scope is the one of the row, not of the owner.
func TestAKeptFailureHasTheScopeOfTheRow(t *testing.T) {
	e := newEnv(t)
	sc := newPairingScript(e.fc)
	sc.send(needsAuth, "QR1", "scan")
	in := webInput()
	in.ClientID = e.tn.ClientA1.String()
	v, _, err := e.svc.Create(context.Background(), e.owner, in)
	if err != nil {
		t.Fatal(err)
	}
	sc.finish(status.Error(codes.Unavailable, "x"))
	eventually(t, func() bool { return !e.running(v.ID) && !e.rowExists(t, v.ID) })
	e.requireKeptFailure(t, e.userA1, v.ID, "pairing_failed")
	e.requireFollowNotFound(t, e.userA2, v.ID)
}

// A pairing that expires, for a re-pair: the row stays in NEEDS_AUTH, and the failure stays until
// the row connects.
func TestFollowPairingGivesTheKeptFailureOfARePair(t *testing.T) {
	e := newEnv(t)
	other := e.otherTenant(t)
	v := e.createPaired(t, webInput())
	sc := newPairingScript(e.fc)
	sc.send(needsAuth, "QR9", "scan")
	if _, err := e.svc.Pair(context.Background(), e.owner, v.ID, instances.PairInput{Pairing: "qr"}); err != nil {
		t.Fatal(err)
	}
	sc.finish(codesEnded)
	eventually(t, func() bool { return !e.running(v.ID) })
	if r := e.row(t, v.ID); r.State != "active" || *r.LastStatus != "NEEDS_AUTH" {
		t.Fatalf("row %+v", r)
	}
	e.requireKeptFailure(t, e.owner, v.ID, "pairing_expired")
	e.requireFollowNotFound(t, e.userA1, v.ID) // the instance is in the default client
	e.requireFollowNotFound(t, other, v.ID)
	e.exec(t, `UPDATE instances SET last_status = 'CONNECTED' WHERE id = $1`, v.ID)
	e.requireFollowNotFound(t, e.owner, v.ID)
}

// The row of a new instance is deleted before the final event. A follow in that time uses the
// scope of the session and gets the stream.
func TestFollowPairingWhenTheRowIsGoneBeforeTheEnd(t *testing.T) {
	e := newEnv(t)
	sc := newPairingScript(e.fc)
	sc.send(needsAuth, "QR1", "scan")
	v, _, err := e.svc.Create(context.Background(), e.userA1, webInput())
	if err != nil {
		t.Fatal(err)
	}
	e.exec(t, `DELETE FROM instances WHERE id = $1`, v.ID)
	e.requireFollowNotFound(t, e.userA2, v.ID)
	ch, err := e.svc.FollowPairing(context.Background(), e.userA1, v.ID)
	if err != nil {
		t.Fatalf("FollowPairing: %v", err)
	}
	if ev := next(t, ch); ev.Kind != "update" || *ev.Update.QRText != "QR1" {
		t.Fatalf("event %+v", ev)
	}
	sc.finish(codesEnded)
	if ev := next(t, ch); ev.Kind != "failed" || ev.FailCode != "pairing_expired" {
		t.Fatalf("event %+v", ev)
	}
	closed(t, ch)
}

func TestFollowPairingOfAnUnknownInstanceIsNotFound(t *testing.T) {
	e := newEnv(t)
	e.requireFollowNotFound(t, e.owner, uuid.New())
}

func TestASuccessIsNotKeptForAFollow(t *testing.T) {
	e := newEnv(t)
	v := e.createPaired(t, webInput())
	e.requireFollowNotFound(t, e.owner, v.ID)
}
