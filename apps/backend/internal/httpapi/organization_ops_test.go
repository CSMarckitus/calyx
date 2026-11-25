package httpapi

import (
	"context"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/config"
	"calyx/backend/internal/tenancy"
)

func TestGetOrganization(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, false)
	w := e.asKey(o.Owner, "GET", "/api/v1/organization", nil)
	requireStatus(t, w, 200)
	m := decodeMap(t, w)
	if m["id"] != o.ID.String() || m["name"] != "Org One" || m["max_instances"] != float64(10) || m["instance_count"] != float64(0) ||
		m["whatsapp_web_allowed"] != true || m["whatsapp_web_acknowledged_at"] != nil || m["meta_webhook"] != nil || m["smtp_configured"] != false || len(m) != 8 {
		t.Fatalf("organization = %v", m)
	}
}

func TestOrganizationIsOwnerOnly(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	c := e.newClient(o, "Gym", true, 0)
	u := e.newClientUser(o, c.ID, "front@gym.example")
	requireProblem(t, e.asKey(u, "GET", "/api/v1/organization", nil), 403, "owner_only")
	requireProblem(t, e.asKey(u, "PATCH", "/api/v1/organization", map[string]string{"name": "x"}), 403, "owner_only")
	requireProblem(t, e.asCookie(u, "POST", "/api/v1/organization/whatsapp-web-acknowledgement", map[string]bool{"accept": true}), 403, "owner_only")
}

func TestRenameOrganization(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	w := e.asKey(o.Owner, "PATCH", "/api/v1/organization", map[string]string{"name": "  New  "})
	requireStatus(t, w, 200)
	if m := decodeMap(t, w); m["name"] != "New" {
		t.Fatalf("name = %v", m["name"])
	}
	requireProblem(t, e.asKey(o.Owner, "PATCH", "/api/v1/organization", map[string]string{"name": "   "}), 400, "invalid_request")
	requireProblem(t, e.asKey(o.Owner, "PATCH", "/api/v1/organization", map[string]string{"name": strings.Repeat("n", 101)}), 400, "invalid_request")
}

func TestOrganizationLimitsAreNotPatchable(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	for _, field := range []string{"max_instances", "whatsapp_web_allowed"} {
		w := e.asKey(o.Owner, "PATCH", "/api/v1/organization", map[string]any{field: 1})
		requireProblem(t, w, 400, "invalid_request")
		if got := problemErrors(t, w); fmt.Sprint(got) != "[[body."+field+" unexpected property]]" {
			t.Fatalf("errors = %v", got)
		}
	}
	m := decodeMap(t, e.asKey(o.Owner, "GET", "/api/v1/organization", nil))
	if m["max_instances"] != float64(10) || m["whatsapp_web_allowed"] != true {
		t.Fatalf("organization = %v", m)
	}
}

func TestEmptyOrganizationPatch(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	w := e.asKey(o.Owner, "PATCH", "/api/v1/organization", `{}`)
	requireStatus(t, w, 200)
	if m := decodeMap(t, w); m["name"] != "Org One" {
		t.Fatalf("organization = %v", m)
	}
	// An explicit null is an absent field, as before.
	w = e.asKey(o.Owner, "PATCH", "/api/v1/organization", `{"name":null}`)
	requireStatus(t, w, 200)
	if m := decodeMap(t, w); m["name"] != "Org One" {
		t.Fatalf("organization = %v", m)
	}
}

func TestAcknowledgeWhatsAppWeb(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, false)
	w := e.asCookie(o.Owner, "POST", "/api/v1/organization/whatsapp-web-acknowledgement", map[string]bool{"accept": true})
	requireStatus(t, w, 200)
	if m := decodeMap(t, w); m["whatsapp_web_acknowledged_at"] != "2026-11-19T09:00:00Z" {
		t.Fatalf("acknowledged_at = %v", m["whatsapp_web_acknowledged_at"])
	}
}

func TestAcknowledgeNeedsTrue(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, false)
	d := requireProblem(t, e.asCookie(o.Owner, "POST", "/api/v1/organization/whatsapp-web-acknowledgement", map[string]bool{"accept": false}), 400, "invalid_request")
	if d != "accept must be true." {
		t.Fatalf("detail = %q", d)
	}
	w := e.asCookie(o.Owner, "POST", "/api/v1/organization/whatsapp-web-acknowledgement", map[string]bool{"accept": false})
	if got := problemErrors(t, w); fmt.Sprint(got) != "[[body.accept must be true.]]" {
		t.Fatalf("errors = %v", got)
	}
	requireMissing(t, e.asCookie(o.Owner, "POST", "/api/v1/organization/whatsapp-web-acknowledgement", `{}`), "accept")
	m := decodeMap(t, e.asKey(o.Owner, "GET", "/api/v1/organization", nil))
	if m["whatsapp_web_acknowledged_at"] != nil {
		t.Fatal("a refused acknowledgement was stored")
	}
}

// The HTTP API gives the message of the service for a name above the limit.
func TestALongNameGivesTheMessageOfTheService(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	long := strings.Repeat("n", tenancy.MaxNameLength+1)
	_, _, err := e.Deps.Tenancy.CreateOrganization(context.Background(), tenancy.NewOrganization{Name: long, MaxInstances: 1})
	ae, ok := apierr.As(err)
	if !ok || len(ae.Errors) != 1 {
		t.Fatalf("service error %v", err)
	}
	msgs := requireFields(t, e.asKey(o.Owner, "PATCH", "/api/v1/organization", map[string]string{"name": long}), "name")
	if msgs[0] != ae.Errors[0].Message {
		t.Fatalf("message = %q, want %q", msgs[0], ae.Errors[0].Message)
	}
}

func TestNamesWithControlCharactersAreRefusedOverHTTP(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	msgs := requireFields(t, e.asKey(o.Owner, "PATCH", "/api/v1/organization", map[string]string{"name": "a\nb"}), "name")
	if !strings.Contains(msgs[0], "control characters") {
		t.Fatalf("message = %q", msgs[0])
	}
	requireProblem(t, e.asKey(o.Owner, "POST", "/api/v1/clients", map[string]string{"name": "a b"}), 400, "invalid_request")
	requireProblem(t, e.asCookie(o.Owner, "POST", "/api/v1/api-keys", map[string]string{"name": "a\x01b"}), 400, "invalid_request")
	requireProblem(t, e.asKey(o.Owner, "POST", "/api/v1/instances", map[string]any{"display_name": "a\tb", "provider": "WHATSAPP_WEB"}), 400, "invalid_request")
}

func TestOrganizationAnswersKeepTheirHeaders(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, false)
	for _, w := range []*httptest.ResponseRecorder{
		e.asKey(o.Owner, "GET", "/api/v1/organization", nil),
		e.asKey(o.Owner, "PATCH", "/api/v1/organization", map[string]string{"name": "New"}),
		e.asCookie(o.Owner, "POST", "/api/v1/organization/whatsapp-web-acknowledgement", map[string]bool{"accept": true}),
	} {
		requireStatus(t, w, 200)
		requireNoStore(t, w)
		if ct := w.Header().Get("Content-Type"); ct != "application/json" {
			t.Fatalf("Content-Type = %q", ct)
		}
	}
}

// meta_webhook is the object or null. Huma has no nullable object field, so a SchemaProvider
// documents it.
func TestTheMetaWebhookIsAnObjectOrNull(t *testing.T) {
	_, api := newMux(Deps{Config: testConfigForRoutes()})
	schemas := api.OpenAPI().Components.Schemas
	org := schemas.SchemaFromRef(api.OpenAPI().Paths["/api/v1/organization"].Get.Responses["200"].Content["application/json"].Schema.Ref)
	if org == nil {
		t.Fatal("no organization schema")
	}
	mw := org.Properties["meta_webhook"]
	if mw == nil || len(mw.OneOf) != 2 || mw.OneOf[1].Type != "null" || !slices.Contains(org.Required, "meta_webhook") {
		t.Fatalf("meta_webhook = %+v", mw)
	}
	obj := schemas.SchemaFromRef(mw.OneOf[0].Ref)
	if obj == nil || obj.Type != "object" || len(obj.Properties) != 2 || len(obj.Required) != 2 {
		t.Fatalf("meta_webhook object = %+v", obj)
	}
}

// The organization with a Meta webhook shows the object.
func TestOrganizationShowsTheMetaWebhook(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) {
		c.CoreMetaWebhookURL = "https://core.example/meta/webhook"
		c.CoreMetaVerifyToken = "verify-me"
	})
	o := e.newOrg("Org One", 10, true, true)
	m := decodeMap(t, e.asKey(o.Owner, "GET", "/api/v1/organization", nil))
	mw, _ := m["meta_webhook"].(map[string]any)
	if mw["url"] != "https://core.example/meta/webhook" || mw["verify_token"] != "verify-me" || len(mw) != 2 {
		t.Fatalf("meta_webhook = %v", m["meta_webhook"])
	}
}

func TestAcknowledgeRefusesAPIKey(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, false)
	w := e.asKey(o.Owner, "POST", "/api/v1/organization/whatsapp-web-acknowledgement", map[string]bool{"accept": true})
	requireProblem(t, w, 403, "api_key_not_allowed")
	if m := decodeMap(t, e.asKey(o.Owner, "GET", "/api/v1/organization", nil)); m["whatsapp_web_acknowledged_at"] != nil {
		t.Fatal("a refused call stored the acknowledgement")
	}
}
