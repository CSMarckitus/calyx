package httpapi

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"calyx/backend/internal/auth"
	"calyx/backend/internal/bulk"
	"calyx/backend/internal/config"
	"calyx/backend/internal/core"
	"calyx/backend/internal/database/gen"
	"calyx/backend/internal/instances"
	"calyx/backend/internal/tenancy"
)

func toMap(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestInstanceResourceMapping(t *testing.T) {
	opened := time.Date(2026, 11, 19, 12, 0, 0, 0, time.FixedZone("x", 3600))
	changed := time.Date(2026, 11, 19, 11, 0, 0, 0, time.UTC)
	status, kind, identity, locale := "NEEDS_AUTH", "needs_auth", "FIREFOX_LINUX", "es-ES"
	v := instances.View{
		Instance: gen.Instance{
			ID: uuid.New(), ClientID: uuid.New(), DisplayName: "Front", Provider: "WHATSAPP_WEB", Enabled: true, State: "active",
			LastStatus: &status, LastStatusAt: &changed, AlertKind: &kind, AlertOpenedAt: &opened,
			DeviceIdentity: &identity, Locale: &locale,
		},
		StatusStale: true,
		Webhooks:    []instances.WebhookView{{URL: "https://h.example/x", Event: "message.received", HasSecret: true}},
	}
	m := toMap(t, instanceResource(v))
	if m["status"] != "NEEDS_AUTH" || m["status_stale"] != true || m["state"] != "active" || m["display_name"] != "Front" {
		t.Fatalf("m = %v", m)
	}
	if m["status_changed_at"] != "2026-11-19T11:00:00Z" {
		t.Fatalf("status_changed_at = %v", m["status_changed_at"])
	}
	alert := m["alert"].(map[string]any)
	if alert["kind"] != "needs_auth" || alert["since"] != "2026-11-19T11:00:00Z" || alert["emailed_at"] != nil {
		t.Fatalf("alert = %v", alert)
	}
	wh := m["webhooks"].([]any)[0].(map[string]any)
	if wh["url"] != "https://h.example/x" || wh["event"] != "message.received" || wh["has_secret"] != true || len(wh) != 3 {
		t.Fatalf("webhook = %v", wh)
	}
	if m["device_identity"] != "FIREFOX_LINUX" || m["locale"] != "es-ES" {
		t.Fatalf("identity and locale: %v %v", m["device_identity"], m["locale"])
	}
	for _, k := range []string{"id", "client_id", "provider", "send_only", "enabled", "device_identity", "locale", "phone_number_id"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("no key %s in %v", k, m)
		}
	}
	if len(m) != 15 {
		t.Fatalf("keys = %d: %v", len(m), m)
	}
}

func TestInstanceWithoutAlert(t *testing.T) {
	m := toMap(t, instanceResource(instances.View{Instance: gen.Instance{ID: uuid.New()}}))
	if v, ok := m["alert"]; !ok || v != nil {
		t.Fatalf("alert = %v", v)
	}
	if v, ok := m["status"]; !ok || v != nil {
		t.Fatalf("status = %v", v)
	}
	if v, ok := m["webhooks"]; !ok || len(v.([]any)) != 0 {
		t.Fatalf("webhooks = %v", v)
	}
}

func TestAnAlertWithAnEmailedTime(t *testing.T) {
	kind := "disconnected"
	opened := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	sent := time.Date(2026, 11, 19, 10, 5, 0, 0, time.UTC)
	m := toMap(t, instanceResource(instances.View{Instance: gen.Instance{AlertKind: &kind, AlertOpenedAt: &opened, AlertedAt: &sent}}))
	if a := m["alert"].(map[string]any); a["emailed_at"] != "2026-11-19T10:05:00Z" {
		t.Fatalf("alert = %v", a)
	}
}

func TestOrganizationMetaWebhook(t *testing.T) {
	view := tenancy.OrganizationView{Organization: gen.Organization{ID: uuid.New(), Name: "O", MaxInstances: 4}, InstanceCount: 2}
	m := toMap(t, organizationResource(view, config.Config{}))
	if v, ok := m["meta_webhook"]; !ok || v != nil {
		t.Fatalf("meta_webhook = %v", v)
	}
	if m["smtp_configured"] != false || m["instance_count"] != float64(2) || m["max_instances"] != float64(4) {
		t.Fatalf("m = %v", m)
	}
	cfg := config.Config{CoreMetaWebhookURL: "https://core.example/meta", CoreMetaVerifyToken: "vt", SMTP: config.SMTP{Host: "smtp.example"}}
	m = toMap(t, organizationResource(view, cfg))
	mw := m["meta_webhook"].(map[string]any)
	if mw["url"] != "https://core.example/meta" || mw["verify_token"] != "vt" || m["smtp_configured"] != true {
		t.Fatalf("m = %v", m)
	}
}

func TestClientResource(t *testing.T) {
	m := toMap(t, clientResource(tenancy.ClientView{Client: gen.Client{ID: uuid.New(), Name: "C", MaxInstances: 3}, InstanceCount: 1}))
	if v, ok := m["user_email"]; !ok || v != nil {
		t.Fatalf("user_email = %v", v)
	}
	m = toMap(t, clientResource(tenancy.ClientView{UserEmail: "a@b.c"}))
	if m["user_email"] != "a@b.c" {
		t.Fatalf("user_email = %v", m["user_email"])
	}
	if v, ok := m["pending_invitation"]; !ok || v != nil {
		t.Fatalf("pending_invitation = %v", v)
	}
	madrid := time.FixedZone("Madrid", 3600)
	m = toMap(t, clientResource(tenancy.ClientView{PendingInvitation: &tenancy.PendingInvitation{
		Email: "new@b.c", ExpiresAt: time.Date(2026, 11, 26, 10, 0, 0, 0, madrid)}}))
	if got := m["pending_invitation"]; !reflect.DeepEqual(got, map[string]any{"email": "new@b.c", "expires_at": "2026-11-26T09:00:00Z"}) {
		t.Fatalf("pending_invitation = %v", got)
	}
}

func TestBulkJobInstanceIDIsTheBackendID(t *testing.T) {
	id := uuid.New()
	est := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	m := toMap(t, bulkJobResource(bulk.Job{
		BulkJob:    core.BulkJob{ID: "job-1", Status: "QUEUED", Total: 3, ProgressPercent: 0, EstimatedFinishAt: &est, CreatedAt: est},
		InstanceID: id,
	}))
	if m["instance_id"] != id.String() || m["id"] != "job-1" || m["total_messages"] != float64(3) ||
		m["estimated_finish_at"] != "2026-11-19T12:00:00Z" || m["sent_messages"] != float64(0) {
		t.Fatalf("m = %v", m)
	}
	m = toMap(t, bulkJobResource(bulk.Job{}))
	if v, ok := m["estimated_finish_at"]; !ok || v != nil {
		t.Fatalf("estimated_finish_at = %v", v)
	}
}

func TestAPIKeyTokenOnlyOnCreation(t *testing.T) {
	k := gen.ApiKey{ID: uuid.New(), Name: "n", Prefix: "cx_abc"}
	m := toMap(t, apiKeyResource(k, ""))
	if _, ok := m["token"]; ok {
		t.Fatalf("token in %v", m)
	}
	if v, ok := m["last_used_at"]; !ok || v != nil {
		t.Fatalf("last_used_at = %v", v)
	}
	m = toMap(t, apiKeyResource(k, auth.APIKeyPrefix+"xyz"))
	if m["token"] != "cx_xyz" {
		t.Fatalf("m = %v", m)
	}
}

func TestUserAndSummaryResources(t *testing.T) {
	cid := uuid.New()
	m := toMap(t, userResource(gen.User{ID: uuid.New(), Email: "a@b.c", Role: "client", ClientID: &cid, OrganizationID: uuid.New()}))
	if m["client_id"] != cid.String() || len(m) != 5 {
		t.Fatalf("m = %v", m)
	}
	m = toMap(t, userResource(gen.User{Role: "owner"}))
	if v, ok := m["client_id"]; !ok || v != nil {
		t.Fatalf("client_id = %v", v)
	}
	oid := uuid.New()
	m = toMap(t, organizationSummary(gen.HTTPGetOrganizationSummaryRow{ID: oid, Name: "N"}))
	if len(m) != 2 || m["id"] != oid.String() || m["name"] != "N" {
		t.Fatalf("m = %v", m)
	}
}
