package instances_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/prototext"

	"calyx/backend/internal/core"
	"calyx/backend/internal/instances"
	"calyx/backend/internal/tenancy"
	"calyx/backend/internal/testutil"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

const web = corev1.Provider_PROVIDER_WHATSAPP_WEB

// activeRow seeds an active WhatsApp Web row that the fake core knows as CONNECTED.
func (e *env) activeRow(t *testing.T) testutil.SeededInstance {
	t.Helper()
	st := "CONNECTED"
	row := e.seed(t, testutil.InstanceSeed{LastStatus: &st})
	e.fc.AddInstance(row.CoreName, web, connected)
	return row
}

func boolp(b bool) *bool { return &b }

func TestUpdateDisplayNameOnlyNeverCallsTheCore(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	v, err := e.svc.Update(context.Background(), e.owner, row.ID, instances.Change{DisplayName: strp("Desk 2")})
	if err != nil || v.DisplayName != "Desk 2" {
		t.Fatalf("got %+v, %v", v.DisplayName, err)
	}
	if n := len(e.fc.UpdateRequests()); n != 0 {
		t.Fatalf("%d core requests", n)
	}
}

func TestUpdateCallsTheCoreThenSaves(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	e.addWebhook(t, row.ID, "https://a.example/1", "connection.update", strp("s1"))
	e.addWebhook(t, row.ID, "https://b.example/1", "message.received", nil)
	v, err := e.svc.Update(context.Background(), e.owner, row.ID, instances.Change{Enabled: boolp(false)})
	if err != nil {
		t.Fatal(err)
	}
	if v.Enabled {
		t.Fatal("the row is still enabled")
	}
	reqs := e.fc.UpdateRequests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests", len(reqs))
	}
	r := reqs[0]
	if r.IsEnabled == nil || *r.IsEnabled || r.IsUsedOnlyForSendMessages != nil || r.Webhooks == nil {
		t.Fatalf("request %v", r)
	}
	hooks := r.GetWebhooks().GetWebhooks()
	if len(hooks) != 2 || hooks[0].GetEvent() != "connection.update" || hooks[0].GetSecret() != "s1" || hooks[1].Secret != nil {
		t.Fatalf("webhooks %v", hooks)
	}
}

func TestUpdateSendsSendOnlyOnlyWhenChanged(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	v, err := e.svc.Update(context.Background(), e.owner, row.ID, instances.Change{SendOnly: boolp(true)})
	if err != nil || !v.SendOnly {
		t.Fatalf("%v %v", v.SendOnly, err)
	}
	if r := e.fc.UpdateRequests()[0]; r.IsUsedOnlyForSendMessages == nil || !*r.IsUsedOnlyForSendMessages {
		t.Fatalf("request %v", r)
	}
	if _, err := e.svc.Update(context.Background(), e.owner, row.ID, instances.Change{Enabled: boolp(true)}); err != nil {
		t.Fatal(err)
	}
	if r := e.fc.UpdateRequests()[1]; r.IsUsedOnlyForSendMessages != nil {
		t.Fatalf("request %v", r)
	}
}

func TestUpdateSendsEnabledOnlyWhenChanged(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	hook := []instances.WebhookInput{{URL: "https://a.example/1", Event: "connection.update"}}
	if _, err := e.svc.Update(context.Background(), e.owner, row.ID, instances.Change{Webhooks: &hook}); err != nil {
		t.Fatal(err)
	}
	if r := e.fc.UpdateRequests()[0]; r.IsEnabled != nil {
		t.Fatalf("a change of webhooks sent enabled: %v", r)
	}
	if _, err := e.svc.Update(context.Background(), e.owner, row.ID, instances.Change{Enabled: boolp(false)}); err != nil {
		t.Fatal(err)
	}
	if r := e.fc.UpdateRequests()[1]; r.IsEnabled == nil || *r.IsEnabled {
		t.Fatalf("request %v", r)
	}
}

func TestUpdateSavesNothingWhenTheOutcomeIsUnknown(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	e.addWebhook(t, row.ID, "https://a.example/1", "connection.update", nil)
	e.fc.SetUnavailable(true)
	_, err := e.svc.Update(context.Background(), e.owner, row.ID,
		instances.Change{Enabled: boolp(false), Webhooks: &[]instances.WebhookInput{}})
	requireCode(t, err, 504, "change_outcome_unknown")
	if !e.row(t, row.ID).Enabled || e.count(t, "webhooks") != 1 {
		t.Fatal("something was saved")
	}
}

func TestUpdateSavesNothingWhenTheCoreRefuses(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	e.fc.SetError("UpdateInstanceConfig", status.Error(codes.InvalidArgument, "no"))
	_, err := e.svc.Update(context.Background(), e.owner, row.ID, instances.Change{Enabled: boolp(false)})
	requireCode(t, err, 400, "invalid_request")
	if ae, _ := apierrAs(err); ae.Detail != "The core refused a value of the request." {
		t.Fatalf("detail %q", ae.Detail)
	}
	if !e.row(t, row.ID).Enabled {
		t.Fatal("something was saved")
	}
}

func TestUpdateWebhookSecretRules(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	e.addWebhook(t, row.ID, "https://a.example/1", "connection.update", strp("s1"))
	e.addWebhook(t, row.ID, "https://b.example/1", "message.received", strp("s2"))
	e.addWebhook(t, row.ID, "https://d.example/1", "message.status", strp("s4"))
	list := []instances.WebhookInput{
		{URL: "https://a.example/1", Event: "connection.update"},                 // same URL, nil: keep
		{URL: "https://b.example/2", Event: "message.received"},                  // new URL, nil: no secret
		{URL: "https://d.example/1", Event: "message.status", Secret: strp("")},  // "": remove
		{URL: "https://c.example/1", Event: "bulk.finished", Secret: strp("s3")}, // new
	}
	v, err := e.svc.Update(context.Background(), e.owner, row.ID, instances.Change{Webhooks: &list})
	if err != nil {
		t.Fatal(err)
	}
	secrets := map[string]*string{}
	for _, w := range e.fc.UpdateRequests()[0].GetWebhooks().GetWebhooks() {
		secrets[w.GetEvent()] = w.Secret
	}
	if secrets["connection.update"] == nil || *secrets["connection.update"] != "s1" || secrets["message.received"] != nil ||
		secrets["message.status"] != nil || secrets["bulk.finished"] == nil || *secrets["bulk.finished"] != "s3" || len(secrets) != 4 {
		t.Fatalf("core secrets %v", secrets)
	}
	rows, err := e.pool.Query(context.Background(), `SELECT event, secret_ciphertext FROM webhooks WHERE instance_id = $1`, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	stored := map[string][]byte{}
	for rows.Next() {
		var ev string
		var ct []byte
		if err := rows.Scan(&ev, &ct); err != nil {
			t.Fatal(err)
		}
		stored[ev] = ct
	}
	plain := func(ev string) string {
		if stored[ev] == nil {
			return "<none>"
		}
		p, err := instances.DecryptSecret(e.key, stored[ev])
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	if plain("connection.update") != "s1" || plain("message.received") != "<none>" ||
		plain("message.status") != "<none>" || plain("bulk.finished") != "s3" || len(stored) != 4 {
		t.Fatalf("stored %v", stored)
	}
	want := map[string]bool{"connection.update": true, "message.received": false, "message.status": false, "bulk.finished": true}
	for _, w := range v.Webhooks {
		if w.HasSecret != want[w.Event] {
			t.Fatalf("HasSecret of %s is %v", w.Event, w.HasSecret)
		}
	}
}

func TestUpdateWithAnEmptyListRemovesEveryWebhook(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	e.addWebhook(t, row.ID, "https://a.example/1", "connection.update", nil)
	if _, err := e.svc.Update(context.Background(), e.owner, row.ID, instances.Change{Webhooks: &[]instances.WebhookInput{}}); err != nil {
		t.Fatal(err)
	}
	r := e.fc.UpdateRequests()[0]
	if r.Webhooks == nil || len(r.Webhooks.GetWebhooks()) != 0 {
		t.Fatalf("request %v", r)
	}
	if e.count(t, "webhooks") != 0 {
		t.Fatal("webhook rows remain")
	}
}

func TestUpdateRefusesSendOnlyWithStoredWebhooks(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	e.addWebhook(t, row.ID, "https://a.example/1", "connection.update", nil)
	_, err := e.svc.Update(context.Background(), e.owner, row.ID, instances.Change{SendOnly: boolp(true)})
	requireCode(t, err, 400, "invalid_request")
	if len(e.fc.UpdateRequests()) != 0 {
		t.Fatal("a core request was sent")
	}
}

func (e *env) metaRow(t *testing.T) testutil.SeededInstance {
	t.Helper()
	st := "CONNECTED"
	row := e.seed(t, testutil.InstanceSeed{Provider: "META_CLOUD", LastStatus: &st, PhoneNumberID: strp("1001")})
	e.fc.AddInstance(row.CoreName, corev1.Provider_PROVIDER_META_CLOUD, connected)
	return row
}

func TestUpdateMetaCredentials(t *testing.T) {
	e := newEnv(t)
	row := e.metaRow(t)
	v, err := e.svc.Update(context.Background(), e.owner, row.ID, instances.Change{
		Meta: &core.MetaCredentials{PhoneNumberID: "1002", AccessToken: "tok-NEW-1", AppSecret: "sec-NEW-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := e.fc.UpdateRequests()[0].GetMetaCloud()
	if m.GetPhoneNumberId() != "1002" || m.GetAccessToken() != "tok-NEW-1" || m.GetAppSecret() != "sec-NEW-1" {
		t.Fatalf("request %v", m)
	}
	if v.PhoneNumberID == nil || *v.PhoneNumberID != "1002" || *e.row(t, row.ID).PhoneNumberID != "1002" {
		t.Fatal("the phone number id was not saved")
	}
	for _, n := range []string{"tok-NEW-1", "sec-NEW-1"} {
		if e.textColumnsContain(t, n) != 0 {
			t.Fatalf("%s is stored", n)
		}
	}
}

func TestUpdateMetaCredentialsRefusals(t *testing.T) {
	e := newEnv(t)
	creds := &core.MetaCredentials{PhoneNumberID: "1002", AccessToken: "t", AppSecret: "s"}
	web := e.activeRow(t)
	_, err := e.svc.Update(context.Background(), e.owner, web.ID, instances.Change{Meta: creds})
	requireCode(t, err, 400, "invalid_request")
	meta := e.metaRow(t)
	other := testutil.SeedTenant(t, e.pool, testutil.TenantOptions{Name: "Other"})
	testutil.SeedInstance(t, e.pool, testutil.InstanceSeed{
		OrganizationID: other.OrganizationID, ClientID: other.ClientA1,
		Provider: "META_CLOUD", PhoneNumberID: strp("1002"),
	})
	_, err = e.svc.Update(context.Background(), e.owner, meta.ID, instances.Change{Meta: creds})
	requireCode(t, err, 409, "phone_number_id_in_use")
	_, err = e.svc.Update(context.Background(), e.owner, meta.ID, instances.Change{SendOnly: boolp(true)})
	requireCode(t, err, 400, "invalid_request")
	_, err = e.svc.Update(context.Background(), e.owner, meta.ID, instances.Change{
		Meta: &core.MetaCredentials{PhoneNumberID: "1003", AccessToken: " ", AppSecret: "s"},
	})
	requireCode(t, err, 400, "invalid_request")
	if len(e.fc.UpdateRequests()) != 0 {
		t.Fatal("a core request was sent")
	}
}

func TestUpdateOnRowsThatAreNotActive(t *testing.T) {
	e := newEnv(t)
	seedState := func(state string) testutil.SeededInstance {
		return e.seed(t, testutil.InstanceSeed{State: state})
	}
	creating, removing, missing := seedState("creating"), seedState("removing"), seedState("missing")
	enable := instances.Change{Enabled: boolp(false)}
	_, err := e.svc.Update(context.Background(), e.owner, creating.ID, enable)
	requireCode(t, err, 409, "pairing_in_progress")
	_, err = e.svc.Update(context.Background(), e.owner, removing.ID, enable)
	requireCode(t, err, 409, "conflict")
	_, err = e.svc.Update(context.Background(), e.owner, missing.ID, enable)
	requireCode(t, err, 409, "conflict")
	v, err := e.svc.Update(context.Background(), e.owner, missing.ID, instances.Change{DisplayName: strp("Renamed")})
	if err != nil || v.DisplayName != "Renamed" {
		t.Fatalf("%v %v", v.DisplayName, err)
	}
	if len(e.fc.UpdateRequests()) != 0 {
		t.Fatal("a core request was sent")
	}
}

func TestUpdateDuringARePairIsPairingInProgress(t *testing.T) {
	e := newEnv(t)
	v := e.createPaired(t, webInput())
	sc := newPairingScript(e.fc)
	sc.send(needsAuth, "QR9", "scan")
	if _, err := e.svc.Pair(context.Background(), e.owner, v.ID, instances.PairInput{Pairing: "qr"}); err != nil {
		t.Fatal(err)
	}
	before := len(e.fc.UpdateRequests()) // the re-pair itself clears the webhooks of the core
	_, err := e.svc.Update(context.Background(), e.owner, v.ID, instances.Change{Enabled: boolp(false)})
	requireCode(t, err, 409, "pairing_in_progress")
	got, err := e.svc.Update(context.Background(), e.owner, v.ID, instances.Change{DisplayName: strp("Still ok")})
	if err != nil || got.DisplayName != "Still ok" {
		t.Fatalf("%v %v", got.DisplayName, err)
	}
	if len(e.fc.UpdateRequests()) != before {
		t.Fatal("a core request was sent")
	}
}

func TestDisplayNameNeverReachesTheCore(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	_, err := e.svc.Update(context.Background(), e.owner, row.ID, instances.Change{DisplayName: strp("Unique-Name-42"), Enabled: boolp(false)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prototext.Format(e.fc.UpdateRequests()[0]), "Unique-Name-42") {
		t.Fatal("the display name reached the core")
	}
	if e.row(t, row.ID).DisplayName != "Unique-Name-42" {
		t.Fatal("the display name was not saved")
	}
}

func TestUpdateOutOfScopeIsNotFound(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	_, err := e.svc.Update(context.Background(), e.userA2, row.ID, instances.Change{Enabled: boolp(false)})
	requireCode(t, err, 404, "not_found")
	if len(e.fc.UpdateRequests()) != 0 {
		t.Fatal("a core request was sent")
	}
}

func TestUpdateValidatesTheDisplayName(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	for _, name := range []string{"", "   ", strings.Repeat("x", 101)} {
		_, err := e.svc.Update(context.Background(), e.owner, row.ID, instances.Change{DisplayName: strp(name)})
		requireCode(t, err, 400, "invalid_request")
	}
}

// lockRow holds a row lock on the instance until the returned function runs. A write to the row
// (or a foreign key check on it) waits for it.
func (e *env) lockRow(t *testing.T, id uuid.UUID) (release func()) {
	t.Helper()
	tx, err := e.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), `SELECT 1 FROM instances WHERE id = $1 FOR UPDATE`, id); err != nil {
		t.Fatal(err)
	}
	return func() { _ = tx.Commit(context.Background()) }
}

func TestUpdateSavesAfterTheRequestContextEnds(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	release := e.lockRow(t, row.ID) // the save waits for the lock
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := e.svc.Update(ctx, e.owner, row.ID, instances.Change{Enabled: boolp(false)})
		done <- err
	}()
	e.fc.WaitForRequests(t, "UpdateInstanceConfig", 1)
	time.Sleep(200 * time.Millisecond) // the core has answered; the save waits for the lock
	cancel()
	time.Sleep(50 * time.Millisecond)
	release()
	if err := <-done; err != nil {
		t.Fatalf("Update: %v", err)
	}
	if e.row(t, row.ID).Enabled {
		t.Fatal("the core accepted the change but the row was not saved")
	}
}

func TestUpdateOfDisplayNameOnlyIsRefusedOnCreatingAndRemovingRows(t *testing.T) {
	e := newEnv(t)
	creating := e.seed(t, testutil.InstanceSeed{State: "creating"})
	removing := e.seed(t, testutil.InstanceSeed{State: "removing"})
	_, err := e.svc.Update(context.Background(), e.owner, creating.ID, instances.Change{DisplayName: strp("New")})
	requireCode(t, err, 409, "pairing_in_progress")
	_, err = e.svc.Update(context.Background(), e.owner, removing.ID, instances.Change{DisplayName: strp("New")})
	requireCode(t, err, 409, "conflict")
}

func TestUpdateRefusesControlCharactersInTheDisplayName(t *testing.T) {
	e := newEnv(t)
	sc := newPairingScript(e.fc)
	v, _ := e.createQR(t, sc, webInput())
	bad := "a\nb"
	_, err := e.svc.Update(context.Background(), e.owner, v.ID, instances.Change{DisplayName: &bad})
	if ae, ok := apierrAs(err); !ok || ae.Status != 400 || ae.Code != "invalid_request" {
		t.Fatalf("got %v", err)
	}
}

// Each limit is accepted at its value and refused one step past it, with the field path.
func TestUpdateLimits(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	ctx := context.Background()
	if _, err := e.svc.Update(ctx, e.owner, row.ID, instances.Change{DisplayName: strp(strings.Repeat("é", instances.MaxDisplayNameLength))}); err != nil {
		t.Fatalf("display_name limit: %v", err)
	}
	_, err := e.svc.Update(ctx, e.owner, row.ID, instances.Change{DisplayName: strp(strings.Repeat("é", instances.MaxDisplayNameLength+1))})
	requireFields(t, err, "display_name")
	// In a PATCH, a secret "" removes the stored secret; a secret past the limit is refused.
	hooks := []instances.WebhookInput{{URL: "https://a.example/", Event: "connection.update", Secret: strp("")}}
	if _, err := e.svc.Update(ctx, e.owner, row.ID, instances.Change{Webhooks: &hooks}); err != nil {
		t.Fatalf("empty secret: %v", err)
	}
	hooks[0].Secret = strp(strings.Repeat("s", instances.MaxSecretLength+1))
	_, err = e.svc.Update(ctx, e.owner, row.ID, instances.Change{Webhooks: &hooks, DisplayName: strp(" ")})
	requireFields(t, err, "display_name", "webhooks[0].secret")
	meta := e.metaRow(t)
	_, err = e.svc.Update(ctx, e.owner, meta.ID, instances.Change{
		Meta: &core.MetaCredentials{PhoneNumberID: strings.Repeat("1", instances.MaxPhoneNumberIDLength+1), AccessToken: "t", AppSecret: "s"},
	})
	requireFields(t, err, "meta_credentials.phone_number_id")
}

// The checks that do not need the row run before the load: a bad body on an unknown or
// out-of-scope id is 400, not 404. The provider rules run after the load.
func TestUpdateChecksTheBodyBeforeTheLoad(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	row := e.activeRow(t)
	long := strings.Repeat("s", instances.MaxSecretLength+1)
	hooks := []instances.WebhookInput{{URL: "ftp://a.example/", Event: "connection.update", Secret: &long}}
	for name, p := range map[string]tenancy.Principal{"unknown id": e.owner, "out of scope": e.userA2} {
		id := row.ID
		if name == "unknown id" {
			id = uuid.New()
		}
		_, err := e.svc.Update(ctx, p, id, instances.Change{
			DisplayName: strp(" "), Webhooks: &hooks,
			Meta: &core.MetaCredentials{PhoneNumberID: "x", AccessToken: "t", AppSecret: "s"},
		})
		requireFields(t, err, "display_name", "meta_credentials.phone_number_id", "webhooks[0].url", "webhooks[0].secret")
		many := make([]instances.WebhookInput, instances.MaxWebhooks+1)
		_, err = e.svc.Update(ctx, p, id, instances.Change{Webhooks: &many})
		requireFields(t, err, "webhooks")
	}
	// A provider rule needs the row: an unknown id is 404.
	_, err := e.svc.Update(ctx, e.owner, uuid.New(), instances.Change{SendOnly: boolp(true)})
	requireCode(t, err, 404, "not_found")
	// On the row, the provider rules run: meta_credentials on a WhatsApp Web instance.
	_, err = e.svc.Update(ctx, e.owner, row.ID, instances.Change{Meta: &core.MetaCredentials{PhoneNumberID: "1", AccessToken: "t", AppSecret: "s"}})
	requireFields(t, err, "meta_credentials")
	if len(e.fc.UpdateRequests()) != 0 {
		t.Fatal("a core request was sent")
	}
}

// slowUpdate starts an Update that waits in the fake core, and returns when the core has it.
func (e *env) slowUpdate(t *testing.T, id uuid.UUID) <-chan error {
	t.Helper()
	e.fc.SetDelay(testutil.MethodUpdateInstanceConfig, 300*time.Millisecond)
	done := make(chan error, 1)
	go func() {
		_, err := e.svc.Update(context.Background(), e.owner, id, instances.Change{Enabled: boolp(false)})
		done <- err
	}()
	e.fc.WaitForRequests(t, testutil.MethodUpdateInstanceConfig, 1)
	return done
}

func TestSecondUpdateWhileOneRunsIsAConflict(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	done := e.slowUpdate(t, row.ID)
	// This change would send the old enabled = true to the core.
	_, err := e.svc.Update(context.Background(), e.owner, row.ID, instances.Change{SendOnly: boolp(true)})
	requireCode(t, err, 409, "conflict")
	if ae, _ := apierrAs(err); ae.Detail != "Another change of this instance is running. Try again." {
		t.Fatalf("detail %q", ae.Detail)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := len(e.fc.UpdateRequests()); n != 1 {
		t.Fatalf("%d core requests, want 1", n)
	}
	if e.row(t, row.ID).Enabled {
		t.Fatal("the first change was not saved")
	}
	// The reservation ends with the first change.
	if _, err := e.svc.Update(context.Background(), e.owner, row.ID, instances.Change{Enabled: boolp(true)}); err != nil {
		t.Fatal(err)
	}
}

func TestPairWhileAnUpdateRunsIsAConflict(t *testing.T) {
	e := newEnv(t)
	st := "NEEDS_AUTH"
	row := e.seed(t, testutil.InstanceSeed{LastStatus: &st})
	e.fc.AddInstance(row.CoreName, web, needsAuth)
	done := e.slowUpdate(t, row.ID)
	_, err := e.svc.Pair(context.Background(), e.owner, row.ID, instances.PairInput{Pairing: "qr"})
	requireCode(t, err, 409, "conflict")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := len(e.fc.CreateRequests()); n != 0 {
		t.Fatalf("%d create requests, want 0", n)
	}
}

func TestAnUnknownOutcomeReleasesTheReservation(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	e.fc.SetUnavailable(true)
	_, err := e.svc.Update(context.Background(), e.owner, row.ID, instances.Change{Enabled: boolp(false)})
	requireCode(t, err, 504, "change_outcome_unknown")
	e.fc.SetUnavailable(false)
	// The next change sends the full configuration again.
	if _, err := e.svc.Update(context.Background(), e.owner, row.ID, instances.Change{Enabled: boolp(false)}); err != nil {
		t.Fatal(err)
	}
}
