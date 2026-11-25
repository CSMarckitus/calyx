package httpapi

import (
	"fmt"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

func TestCreateClientDefaults(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	w := e.asKey(o.Owner, "POST", "/api/v1/clients", map[string]string{"name": "Gym Centre"})
	requireStatus(t, w, 201)
	m := decodeMap(t, w)
	if _, err := uuid.Parse(m["id"].(string)); err != nil {
		t.Fatal(err)
	}
	if m["name"] != "Gym Centre" || m["is_default"] != false || m["can_create_instances"] != false || m["max_instances"] != float64(0) ||
		m["instance_count"] != float64(0) || m["user_email"] != nil || m["pending_invitation"] != nil || len(m) != 8 {
		t.Fatalf("client = %v", m)
	}
}

func TestCreateClientRules(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	for name, c := range map[string]struct{ body, field string }{
		"empty name":    {`{"name":""}`, "name"},
		"negative max":  {`{"name":"A","max_instances":-1}`, "max_instances"},
		"max too large": {`{"name":"A","max_instances":10001}`, "max_instances"},
		"long name":     {`{"name":"` + strings.Repeat("n", 101) + `"}`, "name"},
		"both wrong":    {`{"name":" ","max_instances":-1}`, "name,max_instances"},
	} {
		t.Run(name, func(t *testing.T) {
			requireFields(t, e.asKey(o.Owner, "POST", "/api/v1/clients", c.body), strings.Split(c.field, ",")...)
		})
	}
	requireMissing(t, e.asKey(o.Owner, "POST", "/api/v1/clients", `{}`), "name")
	w := e.asKey(o.Owner, "POST", "/api/v1/clients", `{"name":"A","max_instances":"2"}`)
	requireProblem(t, w, 400, "invalid_request")
	if got := problemErrors(t, w); len(got) != 1 || got[0][0] != "body.max_instances" || strings.Contains(got[0][1], "2") {
		t.Fatalf("errors = %v", got)
	}
	requireStatus(t, e.asKey(o.Owner, "POST", "/api/v1/clients", `{"name":"A","max_instances":10000,"can_create_instances":true}`), 201)
	// An explicit null is an absent field, as before.
	w = e.asKey(o.Owner, "POST", "/api/v1/clients", `{"name":"B","max_instances":null,"can_create_instances":null}`)
	requireStatus(t, w, 201)
	if m := decodeMap(t, w); m["max_instances"] != float64(0) || m["can_create_instances"] != false {
		t.Fatalf("client = %v", m)
	}
}

func TestListClientsHasTheDefault(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	e.newClient(o, "Gym", true, 0)
	w := e.asKey(o.Owner, "GET", "/api/v1/clients", nil)
	requireStatus(t, w, 200)
	items := decodeMap(t, w)["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %v", items)
	}
	defaults := 0
	for _, it := range items {
		if it.(map[string]any)["is_default"] == true {
			defaults++
		}
	}
	if defaults != 1 {
		t.Fatalf("defaults = %d", defaults)
	}
}

func TestGetClientOfAnotherOrganization(t *testing.T) {
	e := newTestEnv(t)
	a := e.newOrg("Org A", 10, true, true)
	b := e.newOrg("Org B", 10, true, true)
	cb := e.newClient(b, "Gym B", true, 0)
	requireProblem(t, e.asKey(a.Owner, "GET", "/api/v1/clients/"+cb.ID.String(), nil), 404, "not_found")
	requireProblem(t, e.asKey(a.Owner, "PATCH", "/api/v1/clients/"+cb.ID.String(), map[string]string{"name": "x"}), 404, "not_found")
	requireProblem(t, e.asKey(a.Owner, "DELETE", "/api/v1/clients/"+cb.ID.String(), nil), 404, "not_found")
	requireProblem(t, e.asCookie(a.Owner, "POST", "/api/v1/clients/"+cb.ID.String()+"/invitations", map[string]string{"email": "x@y.example"}), 404, "not_found")
}

func TestGetClient(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	c := e.newClient(o, "Gym", true, 3)
	e.newClientUser(o, c.ID, "front@gym.example")
	w := e.asKey(o.Owner, "GET", "/api/v1/clients/"+c.ID.String(), nil)
	requireStatus(t, w, 200)
	m := decodeMap(t, w)
	if m["name"] != "Gym" || m["user_email"] != "front@gym.example" || m["max_instances"] != float64(3) {
		t.Fatalf("client = %v", m)
	}
}

func TestPatchClient(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	c := e.newClient(o, "Gym", false, 0)
	e.newClientUser(o, c.ID, "front@gym.example")
	w := e.asKey(o.Owner, "PATCH", "/api/v1/clients/"+c.ID.String(), map[string]any{"can_create_instances": true, "max_instances": 2})
	requireStatus(t, w, 200)
	m := decodeMap(t, w)
	if m["can_create_instances"] != true || m["max_instances"] != float64(2) || m["name"] != "Gym" || m["user_email"] != "front@gym.example" {
		t.Fatalf("client = %v", m)
	}
	requireFields(t, e.asKey(o.Owner, "PATCH", "/api/v1/clients/"+c.ID.String(), map[string]int{"max_instances": -3}), "max_instances")
	requireStatus(t, e.asKey(o.Owner, "PATCH", "/api/v1/clients/"+c.ID.String(), `{}`), 200)
	// An explicit null is an absent field, as before.
	w = e.asKey(o.Owner, "PATCH", "/api/v1/clients/"+c.ID.String(), `{"name":null,"can_create_instances":null,"max_instances":null}`)
	requireStatus(t, w, 200)
	if m := decodeMap(t, w); m["can_create_instances"] != true || m["max_instances"] != float64(2) || m["name"] != "Gym" {
		t.Fatalf("client = %v", m)
	}
	w = e.asKey(o.Owner, "PATCH", "/api/v1/clients/"+c.ID.String(), map[string]any{"is_default": true})
	requireProblem(t, w, 400, "invalid_request")
	if got := problemErrors(t, w); fmt.Sprint(got) != "[[body.is_default unexpected property]]" {
		t.Fatalf("errors = %v", got)
	}
}

func TestDeleteClient(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	c := e.newClient(o, "Gym", true, 0)
	u := e.newClientUser(o, c.ID, "front@gym.example")
	requireStatus(t, e.asKey(o.Owner, "DELETE", "/api/v1/clients/"+c.ID.String(), nil), 204)
	requireProblem(t, e.asKey(o.Owner, "GET", "/api/v1/clients/"+c.ID.String(), nil), 404, "not_found")
	requireProblem(t, e.do("GET", "/api/v1/me", nil, withKey(u.Key)), 401, "unauthenticated")
}

func TestDeleteDefaultClient(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	requireProblem(t, e.asKey(o.Owner, "DELETE", "/api/v1/clients/"+o.DefaultClientID.String(), nil), 409, "default_client")
}

func TestDeleteClientWithInstances(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	c := e.newClient(o, "Gym", true, 0)
	seedInstance(t, e, o, c.ID)
	requireProblem(t, e.asKey(o.Owner, "DELETE", "/api/v1/clients/"+c.ID.String(), nil), 409, "client_has_instances")
}

func TestInviteClientUserWithoutMail(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	c := e.newClient(o, "Gym Centre", true, 0)
	w := e.asCookie(o.Owner, "POST", "/api/v1/clients/"+c.ID.String()+"/invitations", map[string]string{"email": "front@gymcentre.example"})
	requireStatus(t, w, 201)
	m := decodeMap(t, w)
	link, _ := m["invite_link"].(string)
	if m["email"] != "front@gymcentre.example" || m["expires_at"] != "2026-11-26T09:00:00Z" || m["mail_sent"] != false || len(m) != 4 {
		t.Fatalf("answer = %v", m)
	}
	if !regexp.MustCompile(`^https://calyx\.example/invite\?token=[A-Za-z0-9_-]{43}$`).MatchString(link) {
		t.Fatalf("invite_link = %q", link)
	}
}

func TestInviteClientUserWithMail(t *testing.T) {
	e := newTestEnv(t)
	e.MailOn()
	o := e.newOrg("Org One", 10, true, true)
	c := e.newClient(o, "Gym Centre", true, 0)
	w := e.asCookie(o.Owner, "POST", "/api/v1/clients/"+c.ID.String()+"/invitations", map[string]string{"email": "front@gymcentre.example"})
	requireStatus(t, w, 201)
	m := decodeMap(t, w)
	if m["mail_sent"] != true || m["invite_link"] != nil {
		t.Fatalf("answer = %v", m)
	}
	sent := e.Mailer.Sent()
	if len(sent) != 1 || sent[0].To != "front@gymcentre.example" {
		t.Fatalf("sent = %+v", sent)
	}
}

func TestInviteToTheDefaultClient(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	requireProblem(t, e.asCookie(o.Owner, "POST", "/api/v1/clients/"+o.DefaultClientID.String()+"/invitations", map[string]string{"email": "a@b.example"}), 409, "default_client")
}

func TestInviteRules(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	c := e.newClient(o, "Gym", true, 0)
	path := "/api/v1/clients/" + c.ID.String() + "/invitations"
	msgs := requireFields(t, e.asCookie(o.Owner, "POST", path, map[string]string{"email": "nope"}), "email")
	if strings.Contains(msgs[0], "nope") {
		t.Fatalf("the message quotes the email: %q", msgs[0])
	}
	requireMissing(t, e.asCookie(o.Owner, "POST", path, `{}`), "email")
	requireStatus(t, e.asCookie(o.Owner, "POST", path, map[string]string{"email": "front@gym.example"}), 201)
	// the client already has an invitation, but no active user: a second invitation is fine; an email in use gives the same answer
	requireStatus(t, e.asCookie(o.Owner, "POST", path, map[string]string{"email": o.Owner.Email}), 201)
}

func TestClientsAreOwnerOnly(t *testing.T) {
	e := newTestEnv(t)
	a := e.newOrg("Org A", 10, true, true)
	b := e.newOrg("Org B", 10, true, true)
	ca := e.newClient(a, "Gym", true, 0)
	cb := e.newClient(b, "Gym B", true, 0)
	u := e.newClientUser(a, ca.ID, "front@gym.example")
	for _, id := range []uuid.UUID{ca.ID, cb.ID, uuid.New()} {
		p := "/api/v1/clients/" + id.String()
		requireProblem(t, e.asKey(u, "GET", p, nil), 403, "owner_only")
		requireProblem(t, e.asKey(u, "PATCH", p, `{}`), 403, "owner_only")
		requireProblem(t, e.asKey(u, "DELETE", p, nil), 403, "owner_only")
		requireProblem(t, e.asCookie(u, "POST", p+"/invitations", map[string]string{"email": "x@y.example"}), 403, "owner_only")
	}
	requireProblem(t, e.asKey(u, "GET", "/api/v1/clients", nil), 403, "owner_only")
	requireProblem(t, e.asKey(u, "POST", "/api/v1/clients", map[string]string{"name": "x"}), 403, "owner_only")
}

// A malformed client id cannot exist: 404, as before.
func TestAMalformedClientIDIs404(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	for _, m := range []string{"GET", "PATCH", "DELETE"} {
		requireProblem(t, e.asKey(o.Owner, m, "/api/v1/clients/abc", `{}`), 404, "not_found")
	}
	requireProblem(t, e.asCookie(o.Owner, "POST", "/api/v1/clients/abc/invitations", map[string]string{"email": "a@b.example"}), 404, "not_found")
}

func TestClientAnswersKeepTheirHeaders(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	c := e.newClient(o, "Gym", true, 0)
	p := "/api/v1/clients/" + c.ID.String()
	for _, c := range []struct {
		w      *httptest.ResponseRecorder
		status int
	}{
		{e.asKey(o.Owner, "GET", "/api/v1/clients", nil), 200},
		{e.asKey(o.Owner, "POST", "/api/v1/clients", map[string]string{"name": "New"}), 201},
		{e.asKey(o.Owner, "GET", p, nil), 200},
		{e.asKey(o.Owner, "PATCH", p, map[string]string{"name": "Gym 2"}), 200},
		{e.asCookie(o.Owner, "POST", p+"/invitations", map[string]string{"email": "front@gym.example"}), 201},
	} {
		w := c.w
		requireStatus(t, w, c.status)
		requireNoStore(t, w)
		if ct := w.Header().Get("Content-Type"); ct != "application/json" {
			t.Fatalf("Content-Type = %q", ct)
		}
	}
	w := e.asKey(o.Owner, "DELETE", p, nil)
	requireStatus(t, w, 204)
	requireNoStore(t, w)
	if w.Body.Len() != 0 {
		t.Fatalf("body = %q", w.Body.String())
	}
}

// The limited fields of the area tell their limit in the description, from the service constant,
// and set no maxLength, minimum or maximum.
func TestTheLimitsOfOrganizationsAndClientsAreInTheDescriptions(t *testing.T) {
	_, api := newMux(Deps{Config: testConfigForRoutes()})
	doc := api.OpenAPI()
	field := func(op *huma.Operation, name string) *huma.Schema {
		t.Helper()
		s := op.RequestBody.Content["application/json"].Schema
		if s.Ref != "" {
			s = doc.Components.Schemas.SchemaFromRef(s.Ref)
		}
		return s.Properties[name]
	}
	cases := []struct {
		name  string
		op    *huma.Operation
		field string
		want  string
	}{
		{"PATCH /organization", doc.Paths["/api/v1/organization"].Patch, "name", "1 to 100 characters"},
		{"POST /clients", doc.Paths["/api/v1/clients"].Post, "name", "1 to 100 characters"},
		{"POST /clients", doc.Paths["/api/v1/clients"].Post, "max_instances", "0 to 10000"},
		{"PATCH /clients/{id}", doc.Paths["/api/v1/clients/{id}"].Patch, "name", "1 to 100 characters"},
		{"PATCH /clients/{id}", doc.Paths["/api/v1/clients/{id}"].Patch, "max_instances", "0 to 10000"},
		{"POST /clients/{id}/invitations", doc.Paths["/api/v1/clients/{id}/invitations"].Post, "email", "At most 254 bytes"},
	}
	for _, c := range cases {
		if c.op == nil {
			t.Errorf("%s: no operation", c.name)
			continue
		}
		s := field(c.op, c.field)
		if s == nil || !strings.Contains(s.Description, c.want) || s.MaxLength != nil || s.MinLength != nil ||
			s.Minimum != nil || s.Maximum != nil || s.Format != "" {
			t.Errorf("%s %s: schema = %+v", c.name, c.field, s)
		}
	}
}

func TestInviteClientUserRefusesAPIKey(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	c := e.newClient(o, "Gym Centre", true, 0)
	w := e.asKey(o.Owner, "POST", "/api/v1/clients/"+c.ID.String()+"/invitations", map[string]string{"email": "front@gymcentre.example"})
	requireProblem(t, w, 403, "api_key_not_allowed")
}

func TestTheClientShowsItsPendingInvitation(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	c := e.newClient(o, "Gym", true, 0)
	inviteClientUser(t, e, o, c.ID, "front@gym.example")
	want := map[string]any{"email": "front@gym.example", "expires_at": "2026-11-26T09:00:00Z"}
	m := decodeMap(t, e.asKey(o.Owner, "GET", "/api/v1/clients/"+c.ID.String(), nil))
	if !reflect.DeepEqual(m["pending_invitation"], want) || m["user_email"] != nil {
		t.Fatalf("client = %v", m)
	}
	// An expired invitation still shows, so the owner can cancel it or invite again.
	e.Clock.Advance(8 * 24 * time.Hour)
	for _, it := range decodeMap(t, e.asKey(o.Owner, "GET", "/api/v1/clients", nil))["items"].([]any) {
		item := it.(map[string]any)
		if item["id"] == c.ID.String() && !reflect.DeepEqual(item["pending_invitation"], want) {
			t.Fatalf("listed client = %v", item)
		}
		if item["id"] == o.DefaultClientID.String() && item["pending_invitation"] != nil {
			t.Fatalf("default client = %v", item)
		}
	}
}

func TestCancelInvitation(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	c := e.newClient(o, "Gym", true, 0)
	tok := inviteClientUser(t, e, o, c.ID, "front@gym.example")
	path := "/api/v1/clients/" + c.ID.String() + "/invitations"
	requireProblem(t, e.asKey(o.Owner, "DELETE", path, nil), 403, "api_key_not_allowed")
	requireProblem(t, e.do("DELETE", path, nil, withCookie(o.Owner.Cookie), withOrigin("https://evil.example")), 403, "csrf_check_failed")
	requireProblem(t, e.do("DELETE", path, nil), 401, "unauthenticated")
	w := e.asCookie(o.Owner, "DELETE", path, nil)
	requireStatus(t, w, 204)
	requireNoStore(t, w)
	if w.Body.Len() != 0 {
		t.Fatalf("body = %q", w.Body.String())
	}
	if m := decodeMap(t, e.asKey(o.Owner, "GET", "/api/v1/clients/"+c.ID.String(), nil)); m["pending_invitation"] != nil {
		t.Fatalf("client = %v", m)
	}
	requireProblem(t, e.do("POST", "/api/v1/invitations/accept", map[string]string{"token": tok, "password": "a brand new password"}), 410, "token_invalid")
	requireProblem(t, e.asCookie(o.Owner, "DELETE", path, nil), 404, "not_found")
	requireProblem(t, e.asCookie(o.Owner, "DELETE", "/api/v1/clients/"+o.DefaultClientID.String()+"/invitations", nil), 404, "not_found")
	requireProblem(t, e.asCookie(o.Owner, "DELETE", "/api/v1/clients/abc/invitations", nil), 404, "not_found")
}

func TestRemoveClientAccess(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	c := e.newClient(o, "Gym", true, 0)
	u := e.newClientUser(o, c.ID, "front@gym.example")
	path := "/api/v1/clients/" + c.ID.String() + "/user"
	requireProblem(t, e.asKey(o.Owner, "DELETE", path, nil), 403, "api_key_not_allowed")
	requireProblem(t, e.asCookie(u, "DELETE", path, nil), 403, "owner_only")
	requireProblem(t, e.do("DELETE", path, nil, withCookie(o.Owner.Cookie), withOrigin("https://evil.example")), 403, "csrf_check_failed")
	w := e.asCookie(o.Owner, "DELETE", path, nil)
	requireStatus(t, w, 204)
	requireNoStore(t, w)
	if w.Body.Len() != 0 {
		t.Fatalf("body = %q", w.Body.String())
	}
	// The session and the API key of the user end at once.
	requireProblem(t, e.asKey(u, "GET", "/api/v1/me", nil), 401, "unauthenticated")
	requireProblem(t, e.asCookie(u, "GET", "/api/v1/me", nil), 401, "unauthenticated")
	if m := decodeMap(t, e.asKey(o.Owner, "GET", "/api/v1/clients/"+c.ID.String(), nil)); m["user_email"] != nil {
		t.Fatalf("client = %v", m)
	}
	requireProblem(t, e.asCookie(o.Owner, "DELETE", path, nil), 404, "not_found")
	requireProblem(t, e.asCookie(o.Owner, "DELETE", "/api/v1/clients/"+o.DefaultClientID.String()+"/user", nil), 404, "not_found")
	requireProblem(t, e.asCookie(o.Owner, "DELETE", "/api/v1/clients/abc/user", nil), 404, "not_found")
}

// A user whose access was removed returns through a new invitation of the same client, with the
// new password. The old key stays revoked.
func TestARemovedUserReturnsByAnInvitation(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	c := e.newClient(o, "Gym", true, 0)
	u := e.newClientUser(o, c.ID, "front@gym.example")
	requireStatus(t, e.asCookie(o.Owner, "DELETE", "/api/v1/clients/"+c.ID.String()+"/user", nil), 204)
	tok := inviteClientUser(t, e, o, c.ID, "front@gym.example")
	w := e.do("POST", "/api/v1/invitations/accept", map[string]string{"token": tok, "password": "a brand new password"})
	requireStatus(t, w, 200)
	if m := decodeMap(t, w); m["id"] != u.ID.String() || m["client_id"] != c.ID.String() {
		t.Fatalf("user = %v", m)
	}
	requireProblem(t, e.asKey(u, "GET", "/api/v1/me", nil), 401, "unauthenticated")
	requireStatus(t, login(e, "front@gym.example", "a brand new password"), 200)
}
