package httpapi

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"calyx/backend/internal/tenancy"
	"calyx/backend/internal/testutil"
	corev1 "github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// webOrg is an organization that may use WhatsApp Web.
func webOrg(e *testEnv, name string) testOrg { return e.newOrg(name, 10, true, true) }

func metaBody(display, phoneNumberID string) map[string]any {
	return map[string]any{
		"display_name": display, "provider": "META_CLOUD",
		"meta_credentials": map[string]string{"phone_number_id": phoneNumberID, "access_token": "tok", "app_secret": "sec"},
	}
}

func uniquePN() string { return strconv.FormatInt(time.Now().UnixNano(), 10) }

// createMeta creates a Meta instance through the API and returns its id.
func (e *testEnv) createMeta(u testUser, display string, extra ...map[string]any) uuid.UUID {
	e.t.Helper()
	body := metaBody(display, uniquePN())
	for _, x := range extra {
		for k, v := range x {
			body[k] = v
		}
	}
	w := e.asKey(u, "POST", "/api/v1/instances", body)
	requireStatus(e.t, w, 201)
	id, err := uuid.Parse(decodeMap(e.t, w)["instance"].(map[string]any)["id"].(string))
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

// createWeb starts the creation of a WhatsApp Web instance. The fake core holds the session.
func (e *testEnv) createWeb(u testUser, display, pairing string) (uuid.UUID, map[string]any) {
	e.t.Helper()
	body := map[string]any{"display_name": display, "provider": "WHATSAPP_WEB", "pairing": pairing, "locale": "es-ES"}
	if pairing == "code" {
		body["phone"] = "34600111222"
	}
	w := e.asKey(u, "POST", "/api/v1/instances", body)
	requireStatus(e.t, w, 201)
	m := decodeMap(e.t, w)
	id, err := uuid.Parse(m["instance"].(map[string]any)["id"].(string))
	if err != nil {
		e.t.Fatal(err)
	}
	return id, m
}

func (e *testEnv) getInstance(u testUser, id uuid.UUID) *httptest.ResponseRecorder {
	return e.asKey(u, "GET", "/api/v1/instances/"+id.String(), nil)
}

func TestCreateMetaInstance(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	w := e.asKey(o.Owner, "POST", "/api/v1/instances", metaBody("Front desk", "123456"))
	requireStatus(t, w, 201)
	m := decodeMap(t, w)
	inst := m["instance"].(map[string]any)
	if inst["provider"] != "META_CLOUD" || inst["state"] != "active" || inst["status"] != "CONNECTED" || inst["status_stale"] != false ||
		inst["alert"] != nil || inst["display_name"] != "Front desk" || inst["phone_number_id"] != "123456" || inst["client_id"] != o.DefaultClientID.String() {
		t.Fatalf("instance = %v", inst)
	}
	if wh := inst["webhooks"].([]any); len(wh) != 0 {
		t.Fatalf("webhooks = %v", wh)
	}
	if p := m["pairing"].(map[string]any); p["status"] != "CONNECTED" {
		t.Fatalf("pairing = %v", p)
	}
	if got := e.Fake.CreateRequests(); len(got) != 1 {
		t.Fatalf("core create requests = %d", len(got))
	}
}

func TestCreateWhatsAppWebWithCode(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	_, m := e.createWeb(o.Owner, "Front", "code")
	p := m["pairing"].(map[string]any)
	if p["status"] != "NEEDS_AUTH" || p["pairing_code"] != "FAKE1234" || p["qr_text"] != nil {
		t.Fatalf("pairing = %v", p)
	}
	if st := m["instance"].(map[string]any)["state"]; st != "creating" {
		t.Fatalf("state = %v", st)
	}
}

func TestCreateWhatsAppWebWithQR(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	_, m := e.createWeb(o.Owner, "Front", "qr")
	p := m["pairing"].(map[string]any)
	if p["qr_text"] != "fake-qr-1" || p["pairing_code"] != nil {
		t.Fatalf("pairing = %v", p)
	}
}

func TestCreateDefaultsTheClient(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	c := e.newClient(o, "Gym", true, 0)
	u := e.newClientUser(o, c.ID, "front@gym.example")
	id := e.createMeta(o.Owner, "Owner one")
	if m := decodeMap(t, e.getInstance(o.Owner, id)); m["client_id"] != o.DefaultClientID.String() {
		t.Fatalf("owner client_id = %v", m["client_id"])
	}
	id = e.createMeta(u, "Client one")
	if m := decodeMap(t, e.getInstance(u, id)); m["client_id"] != c.ID.String() {
		t.Fatalf("client user client_id = %v", m["client_id"])
	}
	id = e.createMeta(o.Owner, "Owner for a client", map[string]any{"client_id": c.ID.String()})
	if m := decodeMap(t, e.getInstance(o.Owner, id)); m["client_id"] != c.ID.String() {
		t.Fatalf("explicit client_id = %v", m["client_id"])
	}
}

func TestCreateForAnotherClient(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	c1 := e.newClient(o, "Gym 1", true, 0)
	c2 := e.newClient(o, "Gym 2", true, 0)
	u := e.newClientUser(o, c1.ID, "front@gym.example")
	body := metaBody("X", uniquePN())
	body["client_id"] = c2.ID.String()
	requireProblem(t, e.asKey(u, "POST", "/api/v1/instances", body), 404, "not_found")
	if n := len(decodeMap(t, e.asKey(o.Owner, "GET", "/api/v1/instances", nil))["items"].([]any)); n != 0 {
		t.Fatalf("instances = %d", n)
	}
	if len(e.Fake.CreateRequests()) != 0 {
		t.Fatal("the core received a create request")
	}
	body["client_id"] = "not-a-uuid"
	requireFields(t, e.asKey(u, "POST", "/api/v1/instances", body), "client_id")
}

func TestCreateValidation(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	hooks := make([]map[string]string, 21)
	for i := range hooks {
		hooks[i] = map[string]string{"url": "https://h.example/" + strconv.Itoa(i), "event": "message.received"}
	}
	meta := func(pn string) map[string]string {
		return map[string]string{"phone_number_id": pn, "access_token": "t", "app_secret": "s"}
	}
	web := func(extra map[string]any) map[string]any {
		b := map[string]any{"display_name": "X", "provider": "WHATSAPP_WEB", "locale": "es-ES"}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}
	cloud := func(extra map[string]any) map[string]any {
		b := map[string]any{"display_name": "X", "provider": "META_CLOUD", "meta_credentials": meta("123")}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}
	cases := []struct {
		name  string
		body  any
		field string
	}{
		{"empty display_name", web(map[string]any{"display_name": " "}), "display_name"},
		// The two provider cases are Huma's enum check, not the service rule: same location.
		{"provider not in the enum", web(map[string]any{"provider": "TELEGRAM"}), "provider"},
		{"empty provider, not in the enum", web(map[string]any{"provider": ""}), "provider"},
		{"send_only with META_CLOUD", cloud(map[string]any{"send_only": true}), "send_only"},
		{"device_identity with META_CLOUD", cloud(map[string]any{"device_identity": "FIREFOX_LINUX"}), "device_identity"},
		{"locale with META_CLOUD", cloud(map[string]any{"locale": "es-ES"}), "locale"},
		// Huma's enum check, at the same location as a service field error.
		{"device_identity not in the enum", web(map[string]any{"device_identity": "Front desk"}), "device_identity"},
		{"bad locale", web(map[string]any{"locale": "es_ES"}), "locale"},
		{"no locale", map[string]any{"display_name": "X", "provider": "WHATSAPP_WEB"}, "locale"},
		{"bad pairing", web(map[string]any{"pairing": "sms"}), "pairing"},
		{"code without phone", web(map[string]any{"pairing": "code"}), "phone"},
		{"bad phone", web(map[string]any{"pairing": "code", "phone": "12345"}), "phone"},
		{"phone with qr", web(map[string]any{"pairing": "qr", "phone": "34600111222"}), "phone"},
		{"META_CLOUD without credentials", map[string]any{"display_name": "X", "provider": "META_CLOUD"}, "meta_credentials"},
		{"credentials with WHATSAPP_WEB", web(map[string]any{"meta_credentials": meta("123")}), "meta_credentials"},
		{"bad phone_number_id", cloud(map[string]any{"meta_credentials": meta("abc")}), "meta_credentials.phone_number_id"},
		{"long phone_number_id", cloud(map[string]any{"meta_credentials": meta(strings.Repeat("1", 33))}), "meta_credentials.phone_number_id"},
		{"long access_token", cloud(map[string]any{"meta_credentials": map[string]string{"phone_number_id": "1", "access_token": strings.Repeat("a", 4097), "app_secret": "s"}}), "meta_credentials.access_token"},
		{"long app_secret", cloud(map[string]any{"meta_credentials": map[string]string{"phone_number_id": "1", "access_token": "t", "app_secret": strings.Repeat("a", 257)}}), "meta_credentials.app_secret"},
		{"empty app_secret", cloud(map[string]any{"meta_credentials": map[string]string{"phone_number_id": "1", "access_token": "t", "app_secret": " "}}), "meta_credentials.app_secret"},
		{"21 webhooks", cloud(map[string]any{"webhooks": hooks}), "webhooks"},
		{"webhook with an empty url", cloud(map[string]any{"webhooks": []map[string]string{{"url": "", "event": "message.received"}}}), "webhooks[0].url"},
		{"webhook with an empty event", cloud(map[string]any{"webhooks": []map[string]string{{"url": "https://h.example/x", "event": ""}}}), "webhooks[0].event"},
		{"webhook with a bad event", cloud(map[string]any{"webhooks": []map[string]string{{"url": "https://h.example/x", "event": "nope"}}}), "webhooks[0].event"},
		{"webhook with an empty secret", cloud(map[string]any{"webhooks": []map[string]string{{"url": "https://h.example/x", "event": "message.received", "secret": ""}}}), "webhooks[0].secret"},
		{"two webhooks for one event", cloud(map[string]any{"webhooks": []map[string]string{{"url": "https://h.example/x", "event": "message.received"}, {"url": "https://h.example/y", "event": "message.received"}}}), "webhooks[1].event"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			requireFields(t, e.asKey(o.Owner, "POST", "/api/v1/instances", c.body), c.field)
		})
	}
	// A missing required field is a shape error of Huma, at the object that misses it.
	requireMissing(t, e.asKey(o.Owner, "POST", "/api/v1/instances", map[string]any{"provider": "WHATSAPP_WEB"}), "display_name")
	requireMissing(t, e.asKey(o.Owner, "POST", "/api/v1/instances", map[string]any{"display_name": "X"}), "provider")
	for name, c := range map[string]struct {
		body           any
		location, prop string
	}{
		"missing app_secret": {cloud(map[string]any{"meta_credentials": map[string]string{"phone_number_id": "1", "access_token": "t"}}),
			"body.meta_credentials", "app_secret"},
		"webhook without url": {cloud(map[string]any{"webhooks": []map[string]string{{"event": "message.received"}}}),
			"body.webhooks[0]", "url"},
		"webhook without event": {cloud(map[string]any{"webhooks": []map[string]string{{"url": "https://h.example/x"}}}),
			"body.webhooks[0]", "event"},
	} {
		t.Run(name, func(t *testing.T) {
			w := e.asKey(o.Owner, "POST", "/api/v1/instances", c.body)
			requireProblem(t, w, 400, "invalid_request")
			want := [][2]string{{c.location, "expected required property " + c.prop + " to be present"}}
			if got := problemErrors(t, w); fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("errors = %v, want %v", got, want)
			}
		})
	}
	if len(e.Fake.CreateRequests()) != 0 {
		t.Fatal("the core received a create request")
	}
}

func TestCreateWithWebhooks(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	w := e.asKey(o.Owner, "POST", "/api/v1/instances", func() map[string]any {
		b := metaBody("Front", uniquePN())
		b["webhooks"] = []map[string]string{
			{"url": "https://h.example/a", "event": "message.received", "secret": "s"},
			{"url": "https://h.example/b", "event": "connection.update"},
		}
		return b
	}())
	requireStatus(t, w, 201)
	hooks := decodeMap(t, w)["instance"].(map[string]any)["webhooks"].([]any)
	if len(hooks) != 2 {
		t.Fatalf("webhooks = %v", hooks)
	}
	if strings.Contains(w.Body.String(), `"secret"`) {
		t.Fatal("the answer holds a webhook secret")
	}
}

func TestCreatePassesServiceErrors(t *testing.T) {
	e := newTestEnv(t)
	small := e.newOrg("Small", 1, true, true)
	e.createMeta(small.Owner, "First")
	requireProblem(t, e.asKey(small.Owner, "POST", "/api/v1/instances", metaBody("Second", uniquePN())), 409, "quota_exceeded")

	noAck := e.newOrg("No Ack", 10, true, false)
	requireProblem(t, e.asKey(noAck.Owner, "POST", "/api/v1/instances", map[string]any{"display_name": "X", "provider": "WHATSAPP_WEB", "locale": "es-ES"}), 403, "whatsapp_web_not_acknowledged")

	blocked := e.newOrg("Blocked", 10, false, true)
	requireProblem(t, e.asKey(blocked.Owner, "POST", "/api/v1/instances", map[string]any{"display_name": "X", "provider": "WHATSAPP_WEB", "locale": "es-ES"}), 403, "whatsapp_web_not_allowed")

	o := webOrg(e, "Org Two")
	c := e.newClient(o, "Gym", false, 0)
	u := e.newClientUser(o, c.ID, "front@gym.example")
	requireProblem(t, e.asKey(u, "POST", "/api/v1/instances", metaBody("X", uniquePN())), 403, "instance_creation_not_allowed")

	pn := uniquePN()
	requireStatus(t, e.asKey(o.Owner, "POST", "/api/v1/instances", metaBody("A", pn)), 201)
	requireProblem(t, e.asKey(o.Owner, "POST", "/api/v1/instances", metaBody("B", pn)), 409, "phone_number_id_in_use")
}

func TestCreateWhenTheCoreRefuses(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	e.Fake.SetError(testutil.MethodCreateInstance, status.Error(codes.InvalidArgument, "the core says no"))
	requireProblem(t, e.asKey(o.Owner, "POST", "/api/v1/instances", metaBody("X", uniquePN())), 400, "invalid_request")
	if n := len(decodeMap(t, e.asKey(o.Owner, "GET", "/api/v1/instances", nil))["items"].([]any)); n != 0 {
		t.Fatalf("instances = %d, want 0", n)
	}
}

func TestListIsScoped(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	c1 := e.newClient(o, "Gym 1", true, 0)
	c2 := e.newClient(o, "Gym 2", true, 0)
	u := e.newClientUser(o, c1.ID, "front@gym.example")
	a := seedInstance(t, e, o, c1.ID)
	seedInstance(t, e, o, c2.ID)
	seedInstance(t, e, o, c2.ID)
	ids := func(w *httptest.ResponseRecorder) []string {
		requireStatus(t, w, 200)
		var out []string
		for _, it := range decodeMap(t, w)["items"].([]any) {
			out = append(out, it.(map[string]any)["id"].(string))
		}
		return out
	}
	if got := ids(e.asKey(o.Owner, "GET", "/api/v1/instances", nil)); len(got) != 3 {
		t.Fatalf("owner sees %v", got)
	}
	if got := ids(e.asKey(u, "GET", "/api/v1/instances", nil)); len(got) != 1 || got[0] != a.ID.String() {
		t.Fatalf("client user sees %v", got)
	}
	other := webOrg(e, "Org Two")
	if got := ids(e.asKey(other.Owner, "GET", "/api/v1/instances", nil)); len(got) != 0 {
		t.Fatalf("another organization sees %v", got)
	}
}

func TestGetInstance(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	s := seedInstance(t, e, o, o.DefaultClientID)
	w := e.getInstance(o.Owner, s.ID)
	requireStatus(t, w, 200)
	m := decodeMap(t, w)
	if m["id"] != s.ID.String() || m["status"] != "CONNECTED" || m["state"] != "active" || m["provider"] != "WHATSAPP_WEB" || len(m) != 15 {
		t.Fatalf("instance = %v", m)
	}
	requireProblem(t, e.getInstance(o.Owner, uuid.New()), 404, "not_found")
}

func TestPatchDisplayNameOnly(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	s := seedInstance(t, e, o, o.DefaultClientID)
	w := e.asKey(o.Owner, "PATCH", "/api/v1/instances/"+s.ID.String(), map[string]string{"display_name": "  Renamed  "})
	requireStatus(t, w, 200)
	if m := decodeMap(t, w); m["display_name"] != "Renamed" {
		t.Fatalf("display_name = %v", m["display_name"])
	}
	if n := len(e.Fake.UpdateRequests()); n != 0 {
		t.Fatalf("the core received %d update requests", n)
	}
}

func TestPatchWebhookSecrets(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front", map[string]any{"webhooks": []map[string]string{
		{"url": "https://h.example/1", "event": "message.received", "secret": "s1"},
		{"url": "https://h.example/2", "event": "connection.update", "secret": "s2"},
	}})
	w := e.asKey(o.Owner, "PATCH", "/api/v1/instances/"+id.String(), map[string]any{"webhooks": []map[string]any{
		{"url": "https://h.example/1", "event": "message.received"}, // same URL: the stored secret stays
		{"url": "https://h.example/2b", "event": "connection.update", "secret": ""},
	}})
	requireStatus(t, w, 200)
	hooks := decodeMap(t, w)["webhooks"].([]any)
	if len(hooks) != 2 {
		t.Fatalf("webhooks = %v", hooks)
	}
	has := map[string]any{}
	for _, h := range hooks {
		hm := h.(map[string]any)
		has[hm["event"].(string)] = hm["has_secret"]
	}
	if has["message.received"] != true || has["connection.update"] != false {
		t.Fatalf("has_secret = %v", has)
	}
	reqs := e.Fake.UpdateRequests()
	if len(reqs) != 1 {
		t.Fatalf("update requests = %d", len(reqs))
	}
	// An empty list removes every webhook.
	w = e.asKey(o.Owner, "PATCH", "/api/v1/instances/"+id.String(), `{"webhooks":[]}`)
	requireStatus(t, w, 200)
	if hooks := decodeMap(t, w)["webhooks"].([]any); len(hooks) != 0 {
		t.Fatalf("webhooks = %v", hooks)
	}
}

func TestPatchValidation(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	s := seedInstance(t, e, o, o.DefaultClientID)
	p := "/api/v1/instances/" + s.ID.String()
	requireProblem(t, e.asKey(o.Owner, "PATCH", p, map[string]string{"display_name": " "}), 400, "invalid_request")
	requireProblem(t, e.asKey(o.Owner, "PATCH", p, map[string]any{"meta_credentials": map[string]string{"phone_number_id": "1"}}), 400, "invalid_request")
	requireProblem(t, e.asKey(o.Owner, "PATCH", p, map[string]any{"provider": "META_CLOUD"}), 400, "invalid_request")
	requireProblem(t, e.asKey(o.Owner, "PATCH", p, map[string]any{"enabled": "yes"}), 400, "invalid_request")
}

func TestPatchOutcomeUnknown(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	e.Fake.SetError(testutil.MethodUpdateInstanceConfig, status.Error(codes.DeadlineExceeded, "slow"))
	requireProblem(t, e.asKey(o.Owner, "PATCH", "/api/v1/instances/"+id.String(), map[string]bool{"enabled": false}), 504, "change_outcome_unknown")
	if m := decodeMap(t, e.getInstance(o.Owner, id)); m["enabled"] != true {
		t.Fatalf("enabled = %v, want the old value", m["enabled"])
	}
}

func TestPatchEmptyBody(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	s := seedInstance(t, e, o, o.DefaultClientID)
	w := e.asKey(o.Owner, "PATCH", "/api/v1/instances/"+s.ID.String(), `{}`)
	requireStatus(t, w, 200)
	if m := decodeMap(t, w); m["display_name"] != "Seeded" {
		t.Fatalf("instance = %v", m)
	}
}

func TestRemoveActive(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	w := e.asKey(o.Owner, "DELETE", "/api/v1/instances/"+id.String(), nil)
	requireStatus(t, w, 204)
	if w.Body.Len() != 0 {
		t.Fatalf("body = %q", w.Body.String())
	}
	requireProblem(t, e.getInstance(o.Owner, id), 404, "not_found")
}

func TestRemoveRetried(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	e.Fake.SetError(testutil.MethodRemoveInstance, status.Error(codes.Unavailable, "down"))
	w := e.asKey(o.Owner, "DELETE", "/api/v1/instances/"+id.String(), nil)
	requireStatus(t, w, 202)
	if w.Body.Len() != 0 {
		t.Fatalf("body = %q", w.Body.String())
	}
	if m := decodeMap(t, e.getInstance(o.Owner, id)); m["state"] != "removing" {
		t.Fatalf("state = %v", m["state"])
	}
}

func TestRemoveCreating(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id, _ := e.createWeb(o.Owner, "Front", "qr")
	requireProblem(t, e.asKey(o.Owner, "DELETE", "/api/v1/instances/"+id.String(), nil), 409, "pairing_in_progress")
}

// needsAuth seeds a WhatsApp Web row that needs a new pairing, and the same instance in the core.
func needsAuth(t *testing.T, e *testEnv, o testOrg, provider string) uuid.UUID {
	t.Helper()
	st := "NEEDS_AUTH"
	s := testutil.SeedInstance(t, e.Deps.Pool, testutil.InstanceSeed{OrganizationID: o.ID, ClientID: o.DefaultClientID, Provider: provider, LastStatus: &st})
	p := corev1.Provider_PROVIDER_WHATSAPP_WEB
	if provider == "META_CLOUD" {
		p = corev1.Provider_PROVIDER_META_CLOUD
	}
	e.Fake.AddInstance(s.CoreName, p, corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH)
	return s.ID
}

func TestPairAgain(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := needsAuth(t, e, o, "WHATSAPP_WEB")
	w := e.asKey(o.Owner, "POST", "/api/v1/instances/"+id.String()+"/pair", map[string]string{"pairing": "code", "phone": "34600111222"})
	requireStatus(t, w, 202)
	p := decodeMap(t, w)["pairing"].(map[string]any)
	if p["status"] != "NEEDS_AUTH" || p["pairing_code"] != "FAKE1234" {
		t.Fatalf("pairing = %v", p)
	}
}

func TestPairSendsTheIdentityAndTheLocale(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := needsAuth(t, e, o, "WHATSAPP_WEB")
	w := e.asKey(o.Owner, "POST", "/api/v1/instances/"+id.String()+"/pair",
		map[string]string{"device_identity": "SAFARI_MAC_OS", "locale": "pt-BR"})
	requireStatus(t, w, 202)
	reqs := e.Fake.CreateRequests()
	web := reqs[len(reqs)-1].GetWhatsappWeb()
	if web.GetDeviceIdentity() != corev1.DeviceIdentity_DEVICE_IDENTITY_SAFARI_MAC_OS || web.GetLocale() != "pt-BR" {
		t.Fatalf("request %v", web)
	}
	p := "/api/v1/instances/" + id.String() + "/pair"
	requireFields(t, e.asKey(o.Owner, "POST", p, map[string]string{"device_identity": "NETSCAPE"}), "device_identity")
	requireFields(t, e.asKey(o.Owner, "POST", p, map[string]string{"locale": "ES"}), "locale")
}

func TestPairDefaultsToQR(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := needsAuth(t, e, o, "WHATSAPP_WEB")
	w := e.asKey(o.Owner, "POST", "/api/v1/instances/"+id.String()+"/pair", `{}`)
	requireStatus(t, w, 202)
	p := decodeMap(t, w)["pairing"].(map[string]any)
	if p["qr_text"] != "fake-qr-1" || p["pairing_code"] != nil {
		t.Fatalf("pairing = %v", p)
	}
}

func TestPairMeta(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	requireProblem(t, e.asKey(o.Owner, "POST", "/api/v1/instances/"+id.String()+"/pair", `{}`), 409, "pairing_not_supported")
}

func TestPairValidation(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := needsAuth(t, e, o, "WHATSAPP_WEB")
	p := "/api/v1/instances/" + id.String() + "/pair"
	requireProblem(t, e.asKey(o.Owner, "POST", p, map[string]string{"pairing": "code"}), 400, "invalid_request")
	requireProblem(t, e.asKey(o.Owner, "POST", p, map[string]string{"pairing": "sms"}), 400, "invalid_request")
}

// openStream opens the pairing stream with a real HTTP server, so Flush works.
func openStream(t *testing.T, e *testEnv, u testUser, id uuid.UUID) (*http.Response, *bufio.Reader) {
	t.Helper()
	srv := httptest.NewServer(e.Handler)
	t.Cleanup(srv.Close)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/instances/"+id.String()+"/pairing", nil)
	req.Header.Set("Authorization", "Bearer "+u.Key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp, bufio.NewReader(resp.Body)
}

func TestFollowPairing(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id, _ := e.createWeb(o.Owner, "Front", "qr")
	resp, br := openStream(t, e, o.Owner, id) //nolint:bodyclose // openStream closes the body in t.Cleanup
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	// The headers of startSSE, as before: one Cache-Control, no-cache replaces no-store.
	for k, v := range map[string]string{
		"Content-Type": "text/event-stream", "Cache-Control": "no-cache", "X-Accel-Buffering": "no",
	} {
		if got := resp.Header.Values(k); len(got) != 1 || got[0] != v {
			t.Fatalf("%s = %q, want %q", k, got, v)
		}
	}
	first := readBlock(t, br)
	if !strings.HasPrefix(first, "event: update\n") || !strings.Contains(first, `"qr_text":"fake-qr-1"`) {
		t.Fatalf("first event = %q", first)
	}
	e.Fake.Release(tenancy.CoreName(id))
	rest, err := io.ReadAll(br)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rest), "event: done\ndata: {\"status\":\"CONNECTED\"}\n\n") {
		t.Fatalf("rest = %q", rest)
	}
}

// openSSE opens a stream with a real HTTP server, so Flush works.
func openSSE(t *testing.T, e *testEnv, path string, opts ...reqOpt) (*http.Response, *bufio.Reader) {
	t.Helper()
	srv := httptest.NewServer(e.Handler)
	t.Cleanup(srv.Close)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	for _, o := range opts {
		o(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp, bufio.NewReader(resp.Body)
}

// requireEnd reads the rest of a stream and expects a clean end with no byte.
func requireEnd(t *testing.T, br *bufio.Reader) {
	t.Helper()
	done := make(chan string, 1)
	go func() { b, err := io.ReadAll(br); done <- fmt.Sprintf("%q %v", b, err) }()
	select {
	case got := <-done:
		if got != `"" <nil>` {
			t.Fatalf("rest of the stream = %s, want a clean end", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the stream did not end")
	}
}

// The pairing stream runs the access check at each ping, with the API key of the request.
func TestFollowPairingEndsAfterItsKeyIsRevoked(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id, _ := e.createWeb(o.Owner, "Front", "qr")
	_, br := openStream(t, e, o.Owner, id) //nolint:bodyclose // openStream closes the body in t.Cleanup
	readBlock(t, br)
	e.Clock.Advance(pingInterval)
	if got := readBlock(t, br); got != ": ping\n\n" {
		t.Fatalf("ping = %q", got)
	}
	if _, err := e.Deps.Pool.Exec(context.Background(), `UPDATE api_keys SET revoked_at = now() WHERE user_id = $1`, o.Owner.ID); err != nil {
		t.Fatal(err)
	}
	e.Clock.Advance(pingInterval)
	requireEnd(t, br)
}

// The same check with the login cookie of the request.
func TestFollowPairingEndsAfterALogout(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id, _ := e.createWeb(o.Owner, "Front", "qr")
	_, br := openSSE(t, e, "/api/v1/instances/"+id.String()+"/pairing", withCookie(o.Owner.Cookie)) //nolint:bodyclose // openSSE closes the body in t.Cleanup
	readBlock(t, br)
	if err := e.Deps.Auth.Logout(context.Background(), o.Owner.Cookie); err != nil {
		t.Fatal(err)
	}
	e.Clock.Advance(pingInterval)
	requireEnd(t, br)
}

func TestFollowWithoutSession(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	s := seedInstance(t, e, o, o.DefaultClientID)
	requireProblem(t, e.asKey(o.Owner, "GET", "/api/v1/instances/"+s.ID.String()+"/pairing", nil), 404, "not_found")
}

func TestCancelPairing(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id, _ := e.createWeb(o.Owner, "Front", "qr")
	_, br := openStream(t, e, o.Owner, id) //nolint:bodyclose // openStream closes the body in t.Cleanup
	readBlock(t, br)
	requireStatus(t, e.asKey(o.Owner, "DELETE", "/api/v1/instances/"+id.String()+"/pairing", nil), 204)
	rest, err := io.ReadAll(br)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rest), "event: failed\n") || !strings.Contains(string(rest), `"code":"pairing_cancelled"`) {
		t.Fatalf("rest = %q", rest)
	}
	requireProblem(t, e.getInstance(o.Owner, id), 404, "not_found")
}

func sendBody(to, text string) map[string]string { return map[string]string{"to": to, "text": text} }

func TestSendMessage(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	e.Fake.OnSendPlainText(func(ctx context.Context, req *corev1.SendPlainTextRequest) (*corev1.SendPlainTextResponse, error) {
		return &corev1.SendPlainTextResponse{MessageId: "wamid.1"}, nil
	})
	w := e.asKey(o.Owner, "POST", "/api/v1/instances/"+id.String()+"/messages", sendBody("34600111222", "See you at 10."))
	requireStatus(t, w, 200)
	if m := decodeMap(t, w); m["message_id"] != "wamid.1" || m["replayed"] != false || len(m) != 2 {
		t.Fatalf("answer = %v", m)
	}
}

func TestSendValidation(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	p := "/api/v1/instances/" + id.String() + "/messages"
	requireMissing(t, e.asKey(o.Owner, "POST", p, map[string]string{"text": "hi"}), "to")
	requireFields(t, e.asKey(o.Owner, "POST", p, sendBody(" ", "hi")), "to")
	requireFields(t, e.asKey(o.Owner, "POST", p, sendBody("34600111222", "   ")), "text")
	requireFields(t, e.asKey(o.Owner, "POST", p, sendBody("34600111222", "")), "text")
	requireFields(t, e.asKey(o.Owner, "POST", p, sendBody(strings.Repeat("1", 65), "hi")), "to")
	requireFields(t, e.asKey(o.Owner, "POST", p, sendBody("34600111222", strings.Repeat("é", 4097))), "text")
	// Both wrong fields in one answer, and no value of the request in it.
	w := e.asKey(o.Owner, "POST", p, sendBody(strings.Repeat("9", 65), strings.Repeat("secret ", 600)))
	body := w.Body.String()
	requireFields(t, w, "to", "text")
	if strings.Contains(body, "secret") || strings.Contains(body, "9999") {
		t.Fatalf("the answer quotes the request: %s", body)
	}
	if n := len(e.Fake.SendRequests()); n != 0 {
		t.Fatalf("the core received %d sends", n)
	}
	requireStatus(t, e.asKey(o.Owner, "POST", p, sendBody("34600111222", strings.Repeat("é", 4096))), 200)
}

func TestSendNotConnected(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	e.Fake.SetStatus(tenancy.CoreName(id), corev1.InstanceStatus_INSTANCE_STATUS_DISCONNECTED)
	requireProblem(t, e.asKey(o.Owner, "POST", "/api/v1/instances/"+id.String()+"/messages", sendBody("34600111222", "hi")), 409, "instance_not_connected")
	if n := len(e.Fake.SendRequests()); n != 0 {
		t.Fatalf("the core received %d sends", n)
	}
}

func TestSendCoreErrors(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"outcome unknown", status.Error(codes.Unavailable, "down"), 504, "send_outcome_unknown"},
		{"not ready", status.Error(codes.FailedPrecondition, "login again"), 409, "instance_not_ready"},
		{"core not found", status.Error(codes.NotFound, "no such instance"), 404, "not_found"},
		{"token refused", status.Error(codes.Unauthenticated, "bad token"), 502, "core_rejected_token"},
		{"queue full", testutil.ReasonError(codes.ResourceExhausted, corev1.ErrorReason_ERROR_REASON_INSTANCE_QUEUE_FULL, nil, "slow down"), 429, "instance_queue_full"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newTestEnv(t)
			o := webOrg(e, "Org One")
			id := e.createMeta(o.Owner, "Front")
			e.Fake.SetError(testutil.MethodSendPlainText, c.err)
			requireProblem(t, e.asKey(o.Owner, "POST", "/api/v1/instances/"+id.String()+"/messages", sendBody("34600111222", "hi")), c.status, c.code)
		})
	}
}

func TestSendGivesTheCoreABoundedContext(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	var left time.Duration
	e.Fake.OnSendPlainText(func(ctx context.Context, req *corev1.SendPlainTextRequest) (*corev1.SendPlainTextResponse, error) {
		if d, ok := ctx.Deadline(); ok {
			left = time.Until(d)
		}
		return &corev1.SendPlainTextResponse{MessageId: "m"}, nil
	})
	requireStatus(t, e.asKey(o.Owner, "POST", "/api/v1/instances/"+id.String()+"/messages", sendBody("34600111222", "hi")), 200)
	if left <= sendDeadline-time.Minute || left > sendDeadline {
		t.Fatalf("the core call has %s left, want about %s", left, sendDeadline)
	}
}

func idempotencyKeyHeader(k string) reqOpt { return withHeader("Idempotency-Key", k) }

func TestSendKeyReachesTheCoreAndAReplayIsMarked(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	p := "/api/v1/instances/" + id.String() + "/messages"
	w := e.asKey(o.Owner, "POST", p, sendBody("34600111222", "hi"), idempotencyKeyHeader("key-1"))
	requireStatus(t, w, 200)
	first := decodeMap(t, w)
	if first["replayed"] != false {
		t.Fatalf("first answer = %v", first)
	}
	w = e.asKey(o.Owner, "POST", p, sendBody("34600111222", "hi"), idempotencyKeyHeader("key-1"))
	requireStatus(t, w, 200)
	if m := decodeMap(t, w); m["replayed"] != true || m["message_id"] != first["message_id"] {
		t.Fatalf("replay answer = %v", m)
	}
	for _, r := range e.Fake.SendRequests() {
		if r.IdempotencyKey == nil || *r.IdempotencyKey != "key-1" {
			t.Fatalf("the core got the key %v", r.IdempotencyKey)
		}
	}
}

func TestSendWrongKeyIsRefusedWithoutTheValue(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	w := e.asKey(o.Owner, "POST", "/api/v1/instances/"+id.String()+"/messages", sendBody("34600111222", "hi"),
		idempotencyKeyHeader("bad key SECRET"))
	d := requireProblem(t, w, 400, "invalid_request")
	if d != "The Idempotency-Key header must have 1 to 255 printable ASCII characters." || strings.Contains(w.Body.String(), "SECRET") {
		t.Fatalf("detail = %q, body = %s", d, w.Body.String())
	}
	if n := len(e.Fake.SendRequests()); n != 0 {
		t.Fatalf("the core received %d sends", n)
	}
}

func TestSendKeyConflictsOverHTTP(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
		detail string
	}{
		{"conflict", testutil.ReasonError(codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT, nil, "Use a new key."), 409, "idempotency_key_conflict", "Use a new key."},
		{"in progress", testutil.ReasonError(codes.Aborted, corev1.ErrorReason_ERROR_REASON_SEND_IN_PROGRESS, nil, "Retry later."), 409, "send_in_progress", "Retry later."},
		{"outcome unknown", testutil.ReasonError(codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_SEND_OUTCOME_UNKNOWN, nil, "Use a new key later."), 409, "idempotency_key_outcome_unknown", "Use a new key later."},
		{"duplicate", testutil.ReasonError(codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_DUPLICATE_MESSAGE, map[string]string{"message_id": "wamid.9"}, "Sent before."), 409, "duplicate_message", "Sent before. Earlier message id: wamid.9."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newTestEnv(t)
			o := webOrg(e, "Org One")
			id := e.createMeta(o.Owner, "Front")
			e.Fake.SetError(testutil.MethodSendPlainText, c.err)
			w := e.asKey(o.Owner, "POST", "/api/v1/instances/"+id.String()+"/messages", sendBody("34600111222", "hi"), idempotencyKeyHeader("k"))
			if d := requireProblem(t, w, c.status, c.code); d != c.detail {
				t.Fatalf("detail = %q, want %q", d, c.detail)
			}
		})
	}
}

// withContext gives the request a context that the test can cancel.
func withContext(ctx context.Context) reqOpt {
	return func(r *http.Request) { *r = *r.WithContext(ctx) }
}

// A keyed send goes on when the client stops waiting. A send without a key stops, so the core
// does not send a message that the client cannot learn about.
func TestSendContinuesAfterTheClientLeavesOnlyWithAKey(t *testing.T) {
	for _, tc := range []struct {
		name   string
		opts   []reqOpt
		status int
	}{
		{"with a key", []reqOpt{idempotencyKeyHeader("k")}, 200},
		{"without a key", nil, 504},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			o := webOrg(e, "Org One")
			id := e.createMeta(o.Owner, "Front")
			e.Fake.SetDelay(testutil.MethodSendPlainText, 300*time.Millisecond)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				opts := append([]reqOpt{withContext(ctx)}, tc.opts...)
				done <- e.asKey(o.Owner, "POST", "/api/v1/instances/"+id.String()+"/messages", sendBody("34600111222", "hi"), opts...)
			}()
			// The fake core records the call before its delay, so the call is in flight.
			deadline := time.Now().Add(5 * time.Second)
			for len(e.Fake.SendRequests()) == 0 {
				if time.Now().After(deadline) {
					t.Fatal("the core call did not start")
				}
				time.Sleep(5 * time.Millisecond)
			}
			cancel()
			w := <-done
			requireStatus(t, w, tc.status)
		})
	}
}

func TestInstanceRoutesNeedAMember(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	s := seedInstance(t, e, o, o.DefaultClientID)
	for _, r := range []struct{ method, path string }{
		{"GET", "/api/v1/instances"}, {"GET", "/api/v1/instances/" + s.ID.String()},
	} {
		requireProblem(t, e.do(r.method, r.path, nil), 401, "unauthenticated")
	}
}

// An explicit null for an optional field is an absent field, as before.
func TestAnExplicitNullIsAnAbsentInstanceField(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	w := e.asKey(o.Owner, "POST", "/api/v1/instances", `{"display_name":"Front","provider":"WHATSAPP_WEB",`+
		`"client_id":null,"send_only":null,"device_identity":null,"pairing":null,"phone":null,"webhooks":null,"locale":"es-ES"}`)
	requireStatus(t, w, 201)
	m := decodeMap(t, w)
	if p := m["pairing"].(map[string]any); p["qr_text"] != "fake-qr-1" {
		t.Fatalf("pairing = %v", p)
	}
	s := seedInstance(t, e, o, o.DefaultClientID)
	w = e.asKey(o.Owner, "PATCH", "/api/v1/instances/"+s.ID.String(),
		`{"display_name":null,"enabled":null,"send_only":null,"webhooks":null}`)
	requireStatus(t, w, 200)
	if m := decodeMap(t, w); m["display_name"] != "Seeded" || m["enabled"] != true {
		t.Fatalf("instance = %v", m)
	}
	id := needsAuth(t, e, o, "WHATSAPP_WEB")
	requireStatus(t, e.asKey(o.Owner, "POST", "/api/v1/instances/"+id.String()+"/pair", `{"pairing":null,"phone":null}`), 202)
}

// meta_credentials is not documented as nullable, but Huma skips a null optional property
// (validate.go:839): an explicit null is an absent field, as before.
func TestANullMetaCredentialsIsAnAbsentField(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	w := e.asKey(o.Owner, "POST", "/api/v1/instances", `{"display_name":"X","provider":"META_CLOUD","meta_credentials":null}`)
	if msgs := requireFields(t, w, "meta_credentials"); msgs[0] != "is required for META_CLOUD." {
		t.Fatalf("messages = %v", msgs)
	}
	w = e.asKey(o.Owner, "PATCH", "/api/v1/instances/"+seedInstance(t, e, o, o.DefaultClientID).ID.String(), `{"meta_credentials":null}`)
	requireStatus(t, w, 200)
	if len(e.Fake.CreateRequests()) != 0 || len(e.Fake.UpdateRequests()) != 0 {
		t.Fatal("the core received a request")
	}
}

// A bad client_id is a service field error, reported with the other wrong fields.
func TestABadClientIDComesWithTheOtherFieldErrors(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	w := e.asKey(o.Owner, "POST", "/api/v1/instances", `{"client_id":"not-a-uuid","display_name":" ","provider":"WHATSAPP_WEB","locale":"es-ES"}`)
	msgs := requireFields(t, w, "client_id", "display_name")
	if msgs[0] != "must be a UUID." || strings.Contains(w.Body.String(), "not-a-uuid") {
		t.Fatalf("answer = %s", w.Body.String())
	}
}

// A malformed instance id cannot exist: 404, as before.
func TestAMalformedInstanceIDIs404(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	for _, r := range []struct{ method, path string }{
		{"GET", ""}, {"PATCH", ""}, {"DELETE", ""}, {"POST", "/pair"}, {"GET", "/pairing"},
		{"DELETE", "/pairing"}, {"POST", "/messages"},
	} {
		requireProblem(t, e.asKey(o.Owner, r.method, "/api/v1/instances/abc"+r.path, `{}`), 404, "not_found")
	}
}

func TestInstanceAnswersKeepTheirHeaders(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	p := "/api/v1/instances/" + id.String()
	e.Fake.OnSendPlainText(func(ctx context.Context, req *corev1.SendPlainTextRequest) (*corev1.SendPlainTextResponse, error) {
		return &corev1.SendPlainTextResponse{MessageId: "m"}, nil
	})
	pairID := needsAuth(t, e, o, "WHATSAPP_WEB")
	for _, c := range []struct {
		w      *httptest.ResponseRecorder
		status int
	}{
		{e.asKey(o.Owner, "GET", "/api/v1/instances", nil), 200},
		{e.asKey(o.Owner, "POST", "/api/v1/instances", metaBody("Second", uniquePN())), 201},
		{e.asKey(o.Owner, "GET", p, nil), 200},
		{e.asKey(o.Owner, "PATCH", p, map[string]string{"display_name": "Desk"}), 200},
		{e.asKey(o.Owner, "POST", p+"/messages", sendBody("34600111222", "hi")), 200},
		{e.asKey(o.Owner, "POST", "/api/v1/instances/"+pairID.String()+"/pair", `{}`), 202},
	} {
		w := c.w
		requireStatus(t, w, c.status)
		requireNoStore(t, w)
		if ct := w.Header().Get("Content-Type"); ct != "application/json" {
			t.Fatalf("Content-Type = %q", ct)
		}
	}
	webID, _ := e.createWeb(o.Owner, "Web", "qr")
	for _, c := range []struct {
		w      *httptest.ResponseRecorder
		status int
	}{
		{e.asKey(o.Owner, "DELETE", "/api/v1/instances/"+webID.String()+"/pairing", nil), 204},
		{e.asKey(o.Owner, "DELETE", p, nil), 204},
	} {
		w := c.w
		requireStatus(t, w, c.status)
		requireNoStore(t, w)
		if w.Body.Len() != 0 {
			t.Fatalf("body = %q", w.Body.String())
		}
	}
}

// The limited fields of the area tell their limit in the description, from the service constant,
// and set no length, range or pattern.
func TestTheLimitsOfInstancesAreInTheDescriptions(t *testing.T) {
	_, api := newMux(Deps{Config: testConfigForRoutes()})
	doc := api.OpenAPI()
	resolve := func(s *huma.Schema) *huma.Schema {
		if s != nil && s.Ref != "" {
			return doc.Components.Schemas.SchemaFromRef(s.Ref)
		}
		return s
	}
	body := func(op *huma.Operation) *huma.Schema {
		return resolve(op.RequestBody.Content["application/json"].Schema)
	}
	items := []*huma.PathItem{doc.Paths["/api/v1/instances"], doc.Paths["/api/v1/instances/{id}"],
		doc.Paths["/api/v1/instances/{id}/messages"]}
	if slices.Contains(items, nil) || items[0].Post == nil || items[1].Patch == nil || items[2].Post == nil {
		t.Fatal("an operation is missing")
	}
	create, update, send := items[0].Post, items[1].Patch, items[2].Post
	meta := resolve(body(create).Properties["meta_credentials"])
	hook := resolve(body(create).Properties["webhooks"].Items)
	cases := []struct {
		name string
		s    *huma.Schema
		want string
	}{
		{"create display_name", body(create).Properties["display_name"], "1 to 100 characters"},
		{"create locale", body(create).Properties["locale"], "A new WHATSAPP_WEB instance needs it"},
		{"create webhooks", body(create).Properties["webhooks"], "At most 20 webhooks"},
		{"phone_number_id", meta.Properties["phone_number_id"], "1 to 32 digits"},
		{"access_token", meta.Properties["access_token"], "1 to 4096 bytes"},
		{"app_secret", meta.Properties["app_secret"], "1 to 256 bytes"},
		{"webhook url", hook.Properties["url"], "At most 2048 bytes"},
		{"webhook secret", hook.Properties["secret"], "At most 256 bytes"},
		{"update display_name", body(update).Properties["display_name"], "1 to 100 characters"},
		{"update webhooks", body(update).Properties["webhooks"], "At most 20 webhooks"},
		{"send to", body(send).Properties["to"], "At most 64 bytes"},
		{"send text", body(send).Properties["text"], "At most 4096 characters"},
	}
	for _, c := range cases {
		s := c.s
		if s == nil || !strings.Contains(s.Description, c.want) || s.MaxLength != nil || s.MinLength != nil ||
			s.MaxItems != nil || s.Pattern != "" || s.Format != "" {
			t.Errorf("%s: schema = %+v", c.name, s)
		}
	}
	for _, op := range []*huma.Operation{create, doc.Paths["/api/v1/instances/{id}/pair"].Post} {
		p := resolve(body(op).Properties["device_identity"])
		if p == nil || fmt.Sprint(p.Enum) != "[CHROME_WINDOWS EDGE_WINDOWS FIREFOX_WINDOWS FIREFOX_LINUX SAFARI_MAC_OS]" {
			t.Errorf("%s device_identity: %v", op.OperationID, p)
		}
	}
	if p := body(create).Properties["provider"]; p == nil || fmt.Sprint(p.Enum) != "[WHATSAPP_WEB META_CLOUD]" {
		t.Errorf("provider = %+v", p)
	}
}

// alert is the object or null. Huma has no nullable object field, so a SchemaProvider documents
// it, as for meta_webhook.
func TestTheAlertIsAnObjectOrNull(t *testing.T) {
	_, api := newMux(Deps{Config: testConfigForRoutes()})
	schemas := api.OpenAPI().Components.Schemas
	item := api.OpenAPI().Paths["/api/v1/instances/{id}"]
	if item == nil || item.Get == nil {
		t.Fatal("no operation")
	}
	inst := schemas.SchemaFromRef(item.Get.Responses["200"].Content["application/json"].Schema.Ref)
	if inst == nil {
		t.Fatal("no instance schema")
	}
	a := inst.Properties["alert"]
	if a == nil || len(a.OneOf) != 2 || a.OneOf[1].Type != "null" || !slices.Contains(inst.Required, "alert") {
		t.Fatalf("alert = %+v", a)
	}
	obj := schemas.SchemaFromRef(a.OneOf[0].Ref)
	if obj == nil || obj.Type != "object" || len(obj.Properties) != 3 || len(obj.Required) != 3 {
		t.Fatalf("alert object = %+v", obj)
	}
}

// The pairing stream is documented as text/event-stream with one schema per event, in the form
// of Huma's sse package.
func TestThePairingStreamIsDocumented(t *testing.T) {
	_, api := newMux(Deps{Config: testConfigForRoutes()})
	item := api.OpenAPI().Paths["/api/v1/instances/{id}/pairing"]
	if item == nil || item.Get == nil {
		t.Fatal("no operation")
	}
	op := item.Get
	res := op.Responses["200"]
	if res == nil || len(res.Content) != 1 || res.Content["text/event-stream"] == nil || !strings.Contains(res.Description, ": ping") {
		t.Fatalf("200 = %+v", res)
	}
	s := res.Content["text/event-stream"].Schema
	events, _ := s.Items.Extensions["oneOf"].([]*huma.Schema)
	if s.Type != huma.TypeArray || len(events) != 3 {
		t.Fatalf("schema = %+v", s)
	}
	want := map[string]string{"done": "status", "failed": "code", "update": "qr_text"}
	for _, ev := range events {
		name, _ := ev.Properties["event"].Extensions["const"].(string)
		data := api.OpenAPI().Components.Schemas.SchemaFromRef(ev.Properties["data"].Ref)
		if ev.Title != "Event "+name || data == nil || data.Properties[want[name]] == nil ||
			!slices.Contains(ev.Required, "event") || !slices.Contains(ev.Required, "data") {
			t.Fatalf("event %q = %+v, data = %+v", name, ev, data)
		}
		delete(want, name)
	}
	if len(want) != 0 {
		t.Fatalf("events without a schema: %v", want)
	}
}

// waitForInstance reads the instance until ok accepts it (2 s at most).
func waitForInstance(t *testing.T, e *testEnv, u testUser, id uuid.UUID, ok func(m map[string]any) bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		w := e.getInstance(u, id)
		if w.Code == http.StatusOK && ok(decodeMap(t, w)) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("instance = %s", w.Body.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A client user pairs by code on the same phone. The stream ends while nobody follows it.
func TestAPairingByCodeThatEndsInTheBackground(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	c := e.newClient(o, "Shop", true, 5)
	u := e.newClientUser(o, c.ID, "shop@example.com")
	id, m := e.createWeb(u, "Front", "code")
	if p := m["pairing"].(map[string]any); p["pairing_code"] != "FAKE1234" {
		t.Fatalf("pairing = %v", p)
	}
	e.Fake.Release(tenancy.CoreName(id)) // the user types the code in WhatsApp
	waitForInstance(t, e, u, id, func(m map[string]any) bool { return m["state"] == "active" && m["status"] == "CONNECTED" })
	requireProblem(t, e.asKey(u, "GET", "/api/v1/instances/"+id.String()+"/pairing", nil), 404, "not_found")
}

// expireCodes makes each CreateInstance send one QR and end with DEADLINE_EXCEEDED (the codes
// ended) when the test sends on the returned channel.
func expireCodes(e *testEnv) chan<- struct{} {
	end := make(chan struct{}, 4)
	e.Fake.OnCreateInstance(func(ctx context.Context, _ *corev1.CreateInstanceRequest, send func(*corev1.CreateInstanceResponse) error) error {
		qr := "fake-qr-1"
		if err := send(&corev1.CreateInstanceResponse{Status: corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH, PairingCode: &qr}); err != nil {
			return err
		}
		select {
		case <-end:
			return status.Error(codes.DeadlineExceeded, "The pairing codes ended.")
		case <-ctx.Done():
			return status.FromContextError(ctx.Err()).Err()
		}
	})
	return end
}

// requireExpiredStream opens the pairing stream and wants only the event failed with
// pairing_expired, and then the end of the stream.
func requireExpiredStream(t *testing.T, e *testEnv, u testUser, id uuid.UUID) {
	t.Helper()
	resp, br := openStream(t, e, u, id) //nolint:bodyclose // openStream closes the body in t.Cleanup
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	all, err := io.ReadAll(br)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(all), "event: failed\n") || !strings.Contains(string(all), `"code":"pairing_expired"`) ||
		strings.Count(string(all), "event: ") != 1 {
		t.Fatalf("stream = %q", all)
	}
}

// Nobody scans the QR, and the last code ends.
func TestAPairingThatExpires(t *testing.T) {
	t.Run("new instance", func(t *testing.T) {
		e := newTestEnv(t)
		o := webOrg(e, "Org One")
		end := expireCodes(e)
		id, _ := e.createWeb(o.Owner, "Front", "qr")
		endStream(t, e, o.Owner, id, end)
		requireProblem(t, e.getInstance(o.Owner, id), 404, "not_found")
		requireExpiredStream(t, e, o.Owner, id)
		e.Clock.Advance(5*time.Minute - time.Millisecond)
		requireExpiredStream(t, e, o.Owner, id)
		e.Clock.Advance(time.Millisecond)
		requireProblem(t, e.asKey(o.Owner, "GET", "/api/v1/instances/"+id.String()+"/pairing", nil), 404, "not_found")
	})
	t.Run("re-pair", func(t *testing.T) {
		e := newTestEnv(t)
		o := webOrg(e, "Org One")
		id := needsAuth(t, e, o, "WHATSAPP_WEB")
		end := expireCodes(e)
		requireStatus(t, e.asKey(o.Owner, "POST", "/api/v1/instances/"+id.String()+"/pair", map[string]string{"pairing": "qr"}), 202)
		endStream(t, e, o.Owner, id, end)
		requireExpiredStream(t, e, o.Owner, id)
		w := e.getInstance(o.Owner, id)
		requireStatus(t, w, 200)
		if m := decodeMap(t, w); m["state"] != "active" || m["status"] != "NEEDS_AUTH" {
			t.Fatalf("instance = %v", m)
		}
	})
}

// endStream follows the running pairing, ends the codes, and reads the stream to its end. The
// session is then a kept failure: the final event and that change happen under one lock.
func endStream(t *testing.T, e *testEnv, u testUser, id uuid.UUID, end chan<- struct{}) {
	t.Helper()
	resp, br := openStream(t, e, u, id) //nolint:bodyclose // openStream closes the body in t.Cleanup
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if first := readBlock(t, br); !strings.HasPrefix(first, "event: update\n") {
		t.Fatalf("first event = %q", first)
	}
	end <- struct{}{}
	if rest, err := io.ReadAll(br); err != nil || !strings.Contains(string(rest), `"code":"pairing_expired"`) {
		t.Fatalf("rest = %q, %v", rest, err)
	}
}
