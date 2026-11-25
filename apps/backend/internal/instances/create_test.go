package instances_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"calyx/backend/internal/core"
	"calyx/backend/internal/instances"
	"calyx/backend/internal/tenancy"
	"calyx/backend/internal/testutil"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

func webInput() instances.CreateInput {
	return instances.CreateInput{DisplayName: "Front desk", Provider: "WHATSAPP_WEB", Locale: strp("es-ES")}
}

func metaInput(phoneID string) instances.CreateInput {
	return instances.CreateInput{
		DisplayName: "Meta line", Provider: "META_CLOUD",
		Meta: &core.MetaCredentials{PhoneNumberID: phoneID, AccessToken: "tok-ACCESS-1", AppSecret: "sec-APP-1"},
	}
}

func (e *env) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

// createAsync runs Create in a goroutine and returns a function that waits for the answer.
type createResult struct {
	view   instances.View
	update instances.PairingUpdate
	err    error
}

func (e *env) createAsync(p tenancy.Principal, in instances.CreateInput) func(t *testing.T) createResult {
	ch := make(chan createResult, 1)
	go func() {
		v, u, err := e.svc.Create(context.Background(), p, in)
		ch <- createResult{v, u, err}
	}()
	return func(t *testing.T) createResult {
		t.Helper()
		select {
		case r := <-ch:
			return r
		case <-time.After(3 * time.Second):
			t.Fatal("Create did not answer within 3 s")
		}
		return createResult{}
	}
}

// createQR creates a WhatsApp Web instance with a scripted QR update and returns its view.
func (e *env) createQR(t *testing.T, sc *pairingScript, in instances.CreateInput) (instances.View, instances.PairingUpdate) {
	t.Helper()
	sc.send(needsAuth, "QR1", "scan")
	v, u, err := e.svc.Create(context.Background(), e.owner, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return v, u
}

func TestCreateChecksInOrder(t *testing.T) {
	type tc struct {
		name   string
		setup  func(e *env, other testutil.Tenant)
		in     func(other testutil.Tenant) instances.CreateInput
		status int
		code   string
	}
	full := func(e *env) {
		e.exec(t, `UPDATE organizations SET max_instances = 0 WHERE id = $1`, e.tn.OrganizationID)
	}
	block := func(e *env) {
		e.exec(t, `UPDATE organizations SET whatsapp_web_allowed = false WHERE id = $1`, e.tn.OrganizationID)
	}
	unack := func(e *env) {
		e.exec(t, `UPDATE organizations SET whatsapp_web_acknowledged_at = NULL, whatsapp_web_acknowledged_by = NULL WHERE id = $1`, e.tn.OrganizationID)
	}
	cases := []tc{
		{
			"invalid body first", func(e *env, _ testutil.Tenant) { block(e); full(e) },
			func(testutil.Tenant) instances.CreateInput { in := webInput(); in.DisplayName = ""; return in }, 400, "invalid_request",
		},
		{
			"client of another tenant", func(e *env, _ testutil.Tenant) { block(e); full(e) },
			func(o testutil.Tenant) instances.CreateInput {
				in := webInput()
				in.ClientID = o.ClientA1.String()
				return in
			}, 404, "not_found",
		},
		{
			"web not allowed", func(e *env, _ testutil.Tenant) { block(e); unack(e); full(e) },
			func(testutil.Tenant) instances.CreateInput { return webInput() }, 403, "whatsapp_web_not_allowed",
		},
		{
			"web not acknowledged", func(e *env, _ testutil.Tenant) { unack(e); full(e) },
			func(testutil.Tenant) instances.CreateInput { return webInput() }, 403, "whatsapp_web_not_acknowledged",
		},
		{"quota before phone number id", func(e *env, o testutil.Tenant) {
			full(e)
			testutil.SeedInstance(t, e.pool, testutil.InstanceSeed{
				OrganizationID: o.OrganizationID, ClientID: o.ClientA1,
				Provider: "META_CLOUD", PhoneNumberID: strp("1001"),
			})
		}, func(testutil.Tenant) instances.CreateInput { return metaInput("1001") }, 409, "quota_exceeded"},
		{"phone number id in use", func(e *env, o testutil.Tenant) {
			testutil.SeedInstance(t, e.pool, testutil.InstanceSeed{
				OrganizationID: o.OrganizationID, ClientID: o.ClientA1,
				Provider: "META_CLOUD", PhoneNumberID: strp("1001"),
			})
		}, func(testutil.Tenant) instances.CreateInput { return metaInput("1001") }, 409, "phone_number_id_in_use"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			other := testutil.SeedTenant(t, e.pool, testutil.TenantOptions{Name: "Other", WhatsAppWebAllowed: true, Acknowledged: true})
			c.setup(e, other)
			before := e.count(t, "instances")
			_, _, err := e.svc.Create(context.Background(), e.owner, c.in(other))
			requireCode(t, err, c.status, c.code)
			if n := len(e.fc.CreateRequests()); n != 0 {
				t.Fatalf("%d core requests", n)
			}
			if e.count(t, "instances") != before {
				t.Fatal("the instances table changed")
			}
		})
	}
}

func TestCreateByAClientUserWhoseClientMayNotCreate(t *testing.T) {
	e := newEnv(t)
	e.exec(t, `UPDATE clients SET can_create_instances = false WHERE id = $1`, e.tn.ClientA1)
	_, _, err := e.svc.Create(context.Background(), e.userA1, webInput())
	requireCode(t, err, 403, "instance_creation_not_allowed")
	if e.count(t, "instances") != 0 || len(e.fc.CreateRequests()) != 0 {
		t.Fatal("a row or a core call exists")
	}
}

func TestCreateDefaultsTheClient(t *testing.T) {
	e := newEnv(t)
	sc := newPairingScript(e.fc)
	v, _ := e.createQR(t, sc, webInput())
	if v.ClientID != e.tn.DefaultClientID {
		t.Fatalf("owner: client %v", v.ClientID)
	}
	sc2 := newPairingScript(e.fc) // the first session still reads sc
	sc2.send(needsAuth, "QR1", "scan")
	v2, _, err := e.svc.Create(context.Background(), e.userA1, webInput())
	if err != nil {
		t.Fatal(err)
	}
	if v2.ClientID != e.tn.ClientA1 {
		t.Fatalf("client user: client %v", v2.ClientID)
	}
}

func TestCreateForAnotherClientOfTheSameOrganizationIsNotFound(t *testing.T) {
	e := newEnv(t)
	in := webInput()
	in.ClientID = e.tn.ClientA2.String()
	_, _, err := e.svc.Create(context.Background(), e.userA1, in)
	requireCode(t, err, 404, "not_found")
}

func TestCreateWithQRAnswersTheFirstUpdate(t *testing.T) {
	e := newEnv(t)
	sc := newPairingScript(e.fc)
	sc.send(needsAuth, "QRDATA", "scan")
	v, u, err := e.svc.Create(context.Background(), e.owner, webInput())
	if err != nil {
		t.Fatal(err)
	}
	if v.State != "creating" || u.Status != "NEEDS_AUTH" || u.QRText == nil || *u.QRText != "QRDATA" || u.PairingCode != nil {
		t.Fatalf("view %q update %+v", v.State, u)
	}
	req := e.fc.CreateRequests()[0]
	if req.GetInstanceName() != v.CoreName || v.CoreName != tenancy.CoreName(v.ID) || !req.GetWhatsappWeb().GetPairingQr() {
		t.Fatalf("request %v", req)
	}
}

func TestCreateWithACodeAnswersThePairingCode(t *testing.T) {
	e := newEnv(t)
	sc := newPairingScript(e.fc)
	sc.send(needsAuth, "ABCD1234", "enter")
	in := webInput()
	in.Pairing, in.Phone = "code", "34600111222"
	_, u, err := e.svc.Create(context.Background(), e.owner, in)
	if err != nil {
		t.Fatal(err)
	}
	if u.PairingCode == nil || *u.PairingCode != "ABCD1234" || u.QRText != nil {
		t.Fatalf("update %+v", u)
	}
	if got := e.fc.CreateRequests()[0].GetWhatsappWeb().GetPairingCode().GetPhoneNumber(); got != "34600111222" {
		t.Fatalf("phone %q", got)
	}
}

func TestCreateDefaultsToQRAndAlwaysSendsTheProvider(t *testing.T) {
	e := newEnv(t)
	sc := newPairingScript(e.fc)
	e.createQR(t, sc, webInput())
	req := e.fc.CreateRequests()[0]
	if _, ok := req.GetProviderSettings().(*corev1.CreateInstanceRequest_WhatsappWeb); !ok || !req.GetWhatsappWeb().GetPairingQr() {
		t.Fatalf("request %v", req)
	}
}

func TestCreateRefusesABadPhone(t *testing.T) {
	e := newEnv(t)
	for _, phone := range []string{"+34 600", "123456", "1234567890123456", ""} {
		in := webInput()
		in.Pairing, in.Phone = "code", phone
		_, _, err := e.svc.Create(context.Background(), e.owner, in)
		requireFields(t, err, "phone")
	}
	in := webInput()
	in.Phone = "34600111222" // a phone with qr
	_, _, err := e.svc.Create(context.Background(), e.owner, in)
	requireCode(t, err, 400, "invalid_request")
	if e.count(t, "instances") != 0 {
		t.Fatal("a row exists")
	}
}

func (e *env) textColumnsContain(t *testing.T, needle string) int {
	t.Helper()
	n := 0
	for _, table := range []string{"instances", "webhooks", "bulk_jobs"} {
		var c int
		err := e.pool.QueryRow(context.Background(),
			`SELECT count(*) FROM `+table+` x WHERE row_to_json(x)::text LIKE '%'||$1||'%'`, needle).Scan(&c)
		if err != nil {
			t.Fatal(err)
		}
		n += c
	}
	return n
}

func TestCreateMetaAnswersConnected(t *testing.T) {
	e := newEnv(t)
	sc := newPairingScript(e.fc)
	sc.send(connected, "", "ok")
	sc.finish(nil)
	v, u, err := e.svc.Create(context.Background(), e.owner, metaInput("1001"))
	if err != nil {
		t.Fatal(err)
	}
	if u.Status != "CONNECTED" || v.State != "active" || v.LastStatus == nil || *v.LastStatus != "CONNECTED" ||
		v.PhoneNumberID == nil || *v.PhoneNumberID != "1001" {
		t.Fatalf("view %+v update %+v", v.Instance, u)
	}
	m := e.fc.CreateRequests()[0].GetMetaCloud()
	if m.GetPhoneNumberId() != "1001" || m.GetAccessToken() != "tok-ACCESS-1" || m.GetAppSecret() != "sec-APP-1" {
		t.Fatalf("request %v", m)
	}
	for _, needle := range []string{"tok-ACCESS-1", "sec-APP-1"} {
		if e.textColumnsContain(t, needle) != 0 {
			t.Fatalf("%s is stored", needle)
		}
	}
}

func TestCreateMetaRefusesSendOnlyAndPairingFields(t *testing.T) {
	e := newEnv(t)
	mutations := map[string]func(*instances.CreateInput){
		"send_only":       func(in *instances.CreateInput) { in.SendOnly = true },
		"pairing":         func(in *instances.CreateInput) { in.Pairing = "code" },
		"device_identity": func(in *instances.CreateInput) { in.DeviceIdentity = strp("FIREFOX_LINUX") },
		"locale":          func(in *instances.CreateInput) { in.Locale = strp("es-ES") },
		"no app_secret":   func(in *instances.CreateInput) { in.Meta.AppSecret = " " },
		"no meta":         func(in *instances.CreateInput) { in.Meta = nil },
	}
	for name, mut := range mutations {
		in := metaInput("1001")
		mut(&in)
		_, _, err := e.svc.Create(context.Background(), e.owner, in)
		if ae, ok := apierrAs(err); !ok || ae.Status != 400 {
			t.Fatalf("%s: got %v", name, err)
		}
	}
	in := webInput()
	in.Meta = &core.MetaCredentials{PhoneNumberID: "x", AccessToken: "y", AppSecret: "z"}
	_, _, err := e.svc.Create(context.Background(), e.owner, in)
	requireCode(t, err, 400, "invalid_request")
	if e.count(t, "instances") != 0 || len(e.fc.CreateRequests()) != 0 {
		t.Fatal("a row or a core call exists")
	}
}

func TestCreateStoresWebhookSecretsEncrypted(t *testing.T) {
	e := newEnv(t)
	sc := newPairingScript(e.fc)
	in := webInput()
	in.Webhooks = []instances.WebhookInput{
		{URL: "https://a.example/c", Event: "connection.update", Secret: strp("s1")},
		{URL: "https://b.example/m", Event: "message.received"},
	}
	v, _ := e.createQR(t, sc, in)
	rows, err := e.pool.Query(context.Background(), `SELECT event, secret_ciphertext FROM webhooks WHERE instance_id = $1 ORDER BY event`, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string][]byte{}
	for rows.Next() {
		var ev string
		var ct []byte
		if err := rows.Scan(&ev, &ct); err != nil {
			t.Fatal(err)
		}
		got[ev] = ct
	}
	if string(got["connection.update"]) == "s1" || got["connection.update"] == nil {
		t.Fatal("the secret is not sealed")
	}
	if plain, err := instances.DecryptSecret(e.key, got["connection.update"]); err != nil || plain != "s1" {
		t.Fatalf("decrypt: %q %v", plain, err)
	}
	if got["message.received"] != nil {
		t.Fatal("the second webhook has a secret")
	}
	req := e.fc.CreateRequests()[0]
	secrets := map[string]*string{}
	for _, w := range req.GetWebhooks() {
		secrets[w.GetEvent()] = w.Secret
	}
	if secrets["connection.update"] == nil || *secrets["connection.update"] != "s1" || secrets["message.received"] != nil {
		t.Fatalf("core webhooks %v", req.GetWebhooks())
	}
}

func TestCreateRefusesInvalidWebhooks(t *testing.T) {
	e := newEnv(t)
	in := webInput()
	in.SendOnly = true
	in.Webhooks = []instances.WebhookInput{{URL: "https://a.example/", Event: "connection.update"}}
	_, _, err := e.svc.Create(context.Background(), e.owner, in)
	requireCode(t, err, 400, "invalid_request")
	in = webInput()
	in.Webhooks = []instances.WebhookInput{{URL: "https://a.example/", Event: "meta.webhook"}}
	_, _, err = e.svc.Create(context.Background(), e.owner, in)
	requireCode(t, err, 400, "invalid_request")
	if e.count(t, "instances") != 0 || len(e.fc.CreateRequests()) != 0 {
		t.Fatal("a row or a core call exists")
	}
}

func TestCreateWhenTheCoreRefusesFirst(t *testing.T) {
	e := newEnv(t)
	sc := newPairingScript(e.fc)
	sc.finish(status.Error(codes.InvalidArgument, "bad thing"))
	in := webInput()
	in.Webhooks = []instances.WebhookInput{{URL: "https://a.example/", Event: "connection.update"}}
	_, _, err := e.svc.Create(context.Background(), e.owner, in)
	requireCode(t, err, 400, "invalid_request")
	if ae, _ := apierrAs(err); ae.Detail != "The core refused a value of the request." {
		t.Fatalf("detail %q", ae.Detail)
	}
	if e.count(t, "instances") != 0 || e.count(t, "webhooks") != 0 {
		t.Fatal("rows remain")
	}
}

func TestCreateWhenNothingArrivesIn15Seconds(t *testing.T) {
	e := newEnv(t)
	newPairingScript(e.fc)
	wait := e.createAsync(e.owner, webInput())
	eventually(t, func() bool { return e.clk.PendingTimers() >= 2 })
	e.clk.Advance(instances.FirstUpdateWait)
	r := wait(t)
	if r.err != nil {
		t.Fatal(r.err)
	}
	want := instances.PairingUpdate{Status: "CONNECTING", Message: "Waiting for the core."}
	if r.update != want {
		t.Fatalf("update %+v", r.update)
	}
	if r.view.State != "creating" {
		t.Fatalf("state %q", r.view.State)
	}
	ch, err := e.svc.FollowPairing(context.Background(), e.owner, r.view.ID)
	if err != nil || ch == nil {
		t.Fatalf("FollowPairing: %v", err)
	}
}

func TestCreatedRowBecomesActiveWhenThePairingSucceedsLater(t *testing.T) {
	e := newEnv(t)
	sc := newPairingScript(e.fc)
	v, _ := e.createQR(t, sc, webInput()) // the caller does not follow
	sc.send(connected, "", "ok")
	sc.finish(nil)
	eventually(t, func() bool { return e.row(t, v.ID).State == "active" })
	row := e.row(t, v.ID)
	if row.LastStatus == nil || *row.LastStatus != "CONNECTED" || row.LastStatusAt == nil {
		t.Fatalf("row %+v", row)
	}
	if d := row.LastStatusAt.Sub(e.clk.Now()); d > time.Millisecond || d < -time.Millisecond {
		t.Fatalf("last_status_at %v, now %v", row.LastStatusAt, e.clk.Now())
	}
}

func TestCreatedRowIsDeletedWhenThePairingFailsLater(t *testing.T) {
	e := newEnv(t)
	sc := newPairingScript(e.fc)
	v, _ := e.createQR(t, sc, webInput())
	sc.finish(status.Error(codes.Unavailable, "x"))
	eventually(t, func() bool { return !e.rowExists(t, v.ID) })
}

func TestCreatedRowIsDeletedWhenThePairingExpires(t *testing.T) {
	e := newEnv(t)
	sc := newPairingScript(e.fc)
	v, _ := e.createQR(t, sc, webInput())
	eventually(t, func() bool { return e.clk.PendingTimers() >= 1 })
	e.clk.Advance(3 * time.Minute)
	select {
	case <-sc.ctxDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the core stream was not cancelled")
	}
	eventually(t, func() bool { return !e.rowExists(t, v.ID) })
}

func TestCreateValidatesTheDisplayNameTheIdentityAndTheLocale(t *testing.T) {
	e := newEnv(t)
	bad := map[string]func(*instances.CreateInput){
		"display 101 runes": func(in *instances.CreateInput) { in.DisplayName = strings.Repeat("é", 101) },
		"display blank":     func(in *instances.CreateInput) { in.DisplayName = "  " },
		"unknown identity":  func(in *instances.CreateInput) { in.DeviceIdentity = strp("NETSCAPE_BEOS") },
		"prefixed identity": func(in *instances.CreateInput) { in.DeviceIdentity = strp("DEVICE_IDENTITY_FIREFOX_LINUX") },
		"bad locale":        func(in *instances.CreateInput) { in.Locale = strp("es_ES") },
		"no locale":         func(in *instances.CreateInput) { in.Locale = nil },
		"display newline":   func(in *instances.CreateInput) { in.DisplayName = "a\nb" },
		"display U+2028":    func(in *instances.CreateInput) { in.DisplayName = "a\u2028b" },
		"no provider":       func(in *instances.CreateInput) { in.Provider = "" },
		"unknown provider":  func(in *instances.CreateInput) { in.Provider = "TELEGRAM" },
		"unknown pairing":   func(in *instances.CreateInput) { in.Pairing = "sms" },
	}
	for name, mut := range bad {
		in := webInput()
		mut(&in)
		_, _, err := e.svc.Create(context.Background(), e.owner, in)
		if ae, ok := apierrAs(err); !ok || ae.Status != 400 || ae.Code != "invalid_request" {
			t.Fatalf("%s: got %v", name, err)
		}
	}
	if n := len(e.fc.CreateRequests()); n != 0 {
		t.Fatalf("%d requests reached the core", n)
	}
}

func TestCreateSendsAndStoresTheIdentityAndTheLocale(t *testing.T) {
	e := newEnv(t)
	in := webInput()
	in.DeviceIdentity = strp("FIREFOX_LINUX")
	v := e.createPaired(t, in)
	web := e.fc.CreateRequests()[0].GetWhatsappWeb()
	if web.GetDeviceIdentity() != corev1.DeviceIdentity_DEVICE_IDENTITY_FIREFOX_LINUX || web.GetLocale() != "es-ES" {
		t.Fatalf("request %v", web)
	}
	row := e.row(t, v.ID)
	if row.DeviceIdentity == nil || *row.DeviceIdentity != "FIREFOX_LINUX" || row.Locale == nil || *row.Locale != "es-ES" {
		t.Fatalf("row %v %v", row.DeviceIdentity, row.Locale)
	}
}

func TestCreateStoresTheIdentityThatTheCorePicked(t *testing.T) {
	e := newEnv(t)
	v := e.createPaired(t, webInput()) // no identity: the fake core picks CHROME_WINDOWS
	eventually(t, func() bool {
		row := e.row(t, v.ID)
		return row.DeviceIdentity != nil && *row.DeviceIdentity == "CHROME_WINDOWS"
	})
	if row := e.row(t, v.ID); row.Locale == nil || *row.Locale != "es-ES" {
		t.Fatalf("locale %v", row.Locale)
	}
}

func TestCreateWhenTheStreamEndsWithoutConnecting(t *testing.T) {
	e := newEnv(t)
	sc := newPairingScript(e.fc)
	sc.finish(nil) // the core ends the stream with no update at all
	_, _, err := e.svc.Create(context.Background(), e.owner, webInput())
	requireCode(t, err, 409, "conflict")
	if ae, _ := apierrAs(err); ae.Detail != "The core ended the pairing without connecting." {
		t.Fatalf("detail %q", ae.Detail)
	}
	if e.count(t, "instances") != 0 {
		t.Fatal("the row remains")
	}
}

// A client that reads the instance when the CONNECTED event arrives sees the stored identity.
func TestTheConnectedEventComesAfterTheIdentityIsStored(t *testing.T) {
	e := newEnv(t)
	sc := newPairingScript(e.fc)
	in := webInput()
	in.DeviceIdentity = strp("SAFARI_MAC_OS")
	v, _ := e.createQR(t, sc, in)
	// The insert stored the requested values: clear them to see the write of CONNECTED.
	e.exec(t, `UPDATE instances SET device_identity = NULL, locale = NULL WHERE id = $1`, v.ID)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := e.svc.FollowPairing(ctx, e.owner, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	sc.send(connected, "", "ok") // the stream stays open: the end of the session cannot write
	for ev := range events {
		if ev.Kind == "update" && ev.Update.Status == "CONNECTED" {
			row := e.row(t, v.ID)
			if row.DeviceIdentity == nil || *row.DeviceIdentity != "SAFARI_MAC_OS" || row.Locale == nil || *row.Locale != "es-ES" {
				t.Fatalf("at the CONNECTED event: identity %v, locale %v", row.DeviceIdentity, row.Locale)
			}
			break
		}
	}
	sc.finish(nil)
}
