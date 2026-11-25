package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"calyx/backend/internal/config"
)

func findCookie(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func tokenFromLink(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	tok := u.Query().Get("token")
	if tok == "" {
		t.Fatalf("no token in %q", link)
	}
	return tok
}

func login(e *testEnv, email, password string) *httptest.ResponseRecorder {
	return e.do("POST", "/api/v1/auth/login", map[string]string{"email": email, "password": password})
}

func TestLoginSetsTheCookie(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	w := login(e, o.Owner.Email, testPassword)
	requireStatus(t, w, 200)
	m := decodeMap(t, w)
	if m["email"] != o.Owner.Email || m["role"] != "owner" || m["client_id"] != nil || m["organization_id"] != o.ID.String() || m["id"] != o.Owner.ID.String() || len(m) != 5 {
		t.Fatalf("user = %v", m)
	}
	c := findCookie(w, "calyx_session")
	if c == nil || c.Value == "" || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.MaxAge != 7776000 || !c.Secure {
		t.Fatalf("cookie = %+v", c)
	}
}

func TestLoginWithoutSecureOnHTTP(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) { c.PublicURL, _ = url.Parse("http://127.0.0.1:8080") })
	o := e.newOrg("Org One", 10, true, true)
	c := findCookie(login(e, o.Owner.Email, testPassword), "calyx_session")
	if c == nil || c.Secure {
		t.Fatalf("cookie = %+v", c)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	w := login(e, o.Owner.Email, "not the password")
	requireProblem(t, w, 401, "unauthenticated")
	if findCookie(w, "calyx_session") != nil {
		t.Fatal("a failed login sets a cookie")
	}
}

// requireMissing checks the 400 answer of Huma for a body without the required fields.
func requireMissing(t *testing.T, w *httptest.ResponseRecorder, fields ...string) {
	t.Helper()
	requireProblem(t, w, 400, "invalid_request")
	var want [][2]string
	for _, f := range fields {
		want = append(want, [2]string{"body", "expected required property " + f + " to be present"})
	}
	if got := problemErrors(t, w); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("errors = %v, want %v", got, want)
	}
}

func TestLoginMissingField(t *testing.T) {
	e := newTestEnv(t)
	requireMissing(t, e.do("POST", "/api/v1/auth/login", `{"email":"a@b.c"}`), "password")
	requireMissing(t, e.do("POST", "/api/v1/auth/login", `{"password":"x"}`), "email")
	// A field that is present but empty is a rule of the service.
	requireFields(t, e.do("POST", "/api/v1/auth/login", `{"email":"","password":""}`), "email", "password")
}

func TestLogoutEndsTheSession(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	w := e.asCookie(o.Owner, "POST", "/api/v1/auth/logout", nil)
	requireStatus(t, w, 204)
	c := findCookie(w, "calyx_session")
	if c == nil || c.Value != "" || c.MaxAge >= 0 {
		t.Fatalf("cookie = %+v", c)
	}
	requireProblem(t, e.do("GET", "/api/v1/me", nil, withCookie(o.Owner.Cookie)), 401, "unauthenticated")
}

func TestLogoutWithAKey(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	requireProblem(t, e.asKey(o.Owner, "POST", "/api/v1/auth/logout", nil), 403, "api_key_not_allowed")
	requireStatus(t, e.asKey(o.Owner, "GET", "/api/v1/me", nil), 200)
}

func TestResetRequestIsAlways202(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	w := e.do("POST", "/api/v1/auth/password-reset", map[string]string{"email": o.Owner.Email})
	requireStatus(t, w, 202)
	if w.Body.Len() != 0 {
		t.Fatalf("body = %q", w.Body.String())
	}
	e.Mailer.WaitForAttempts(t, 1) // the service sends the mail in a goroutine
	w = e.do("POST", "/api/v1/auth/password-reset", map[string]string{"email": "nobody@example.com"})
	requireStatus(t, w, 202)
	if w.Body.Len() != 0 {
		t.Fatalf("body = %q", w.Body.String())
	}
}

func TestResetRequestBadEmail(t *testing.T) {
	e := newTestEnv(t)
	msgs := requireFields(t, e.do("POST", "/api/v1/auth/password-reset", map[string]string{"email": "not-an-email"}), "email")
	if strings.Contains(msgs[0], "not-an-email") {
		t.Fatalf("the message quotes the email: %q", msgs[0])
	}
}

func TestResetConfirm(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	link, err := e.Deps.Auth.CreatePasswordReset(context.Background(), o.Owner.Email)
	if err != nil {
		t.Fatal(err)
	}
	tok := tokenFromLink(t, link)
	requireStatus(t, e.do("POST", "/api/v1/auth/password-reset/confirm", map[string]string{"token": tok, "password": "a brand new password"}), 204)
	requireStatus(t, login(e, o.Owner.Email, "a brand new password"), 200)
}

func TestResetConfirmUsedToken(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	link, _ := e.Deps.Auth.CreatePasswordReset(context.Background(), o.Owner.Email)
	body := map[string]string{"token": tokenFromLink(t, link), "password": "a brand new password"}
	requireStatus(t, e.do("POST", "/api/v1/auth/password-reset/confirm", body), 204)
	requireProblem(t, e.do("POST", "/api/v1/auth/password-reset/confirm", body), 410, "token_invalid")
}

func TestResetConfirmFields(t *testing.T) {
	e := newTestEnv(t)
	requireMissing(t, e.do("POST", "/api/v1/auth/password-reset/confirm", `{"token":"x"}`), "password")
	requireMissing(t, e.do("POST", "/api/v1/auth/password-reset/confirm", `{"password":"x"}`), "token")
	requireFields(t, e.do("POST", "/api/v1/auth/password-reset/confirm", `{"token":"","password":""}`), "token", "password")
}

func inviteClientUser(t *testing.T, e *testEnv, o testOrg, clientID uuid.UUID, email string) string {
	t.Helper()
	res, err := e.Deps.Auth.InviteClientUser(context.Background(), o.Owner.Principal, clientID, email)
	if err != nil {
		t.Fatal(err)
	}
	if res.Link == "" {
		t.Fatal("no link: the mail was sent")
	}
	return tokenFromLink(t, res.Link)
}

func TestAcceptInvitationLogsIn(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	c := e.newClient(o, "Gym", true, 0)
	tok := inviteClientUser(t, e, o, c.ID, "front@gym.example")
	w := e.do("POST", "/api/v1/invitations/accept", map[string]string{"token": tok, "password": "a brand new password"})
	requireStatus(t, w, 200)
	m := decodeMap(t, w)
	if m["role"] != "client" || m["email"] != "front@gym.example" || m["client_id"] != c.ID.String() {
		t.Fatalf("user = %v", m)
	}
	if ck := findCookie(w, "calyx_session"); ck == nil || ck.Value == "" {
		t.Fatal("no session cookie")
	}
}

func TestAcceptInvitationExpired(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	c := e.newClient(o, "Gym", true, 0)
	tok := inviteClientUser(t, e, o, c.ID, "front@gym.example")
	e.Clock.Advance(7*24*time.Hour + time.Minute)
	requireProblem(t, e.do("POST", "/api/v1/invitations/accept", map[string]string{"token": tok, "password": "a brand new password"}), 410, "token_invalid")
}

func TestAcceptShortPassword(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	c := e.newClient(o, "Gym", true, 0)
	tok := inviteClientUser(t, e, o, c.ID, "front@gym.example")
	requireProblem(t, e.do("POST", "/api/v1/invitations/accept", map[string]string{"token": tok, "password": "123456789"}), 400, "invalid_request")
}

func TestMeForAnOwner(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	w := e.asKey(o.Owner, "GET", "/api/v1/me", nil)
	requireStatus(t, w, 200)
	m := decodeMap(t, w)
	user := m["user"].(map[string]any)
	if user["email"] != o.Owner.Email || len(user) != 5 {
		t.Fatalf("user = %v", user)
	}
	org := m["organization"].(map[string]any)
	for _, k := range []string{"id", "name", "max_instances", "instance_count", "whatsapp_web_allowed", "whatsapp_web_acknowledged_at", "meta_webhook", "smtp_configured"} {
		if _, ok := org[k]; !ok {
			t.Fatalf("no %s in %v", k, org)
		}
	}
	if len(org) != 8 || org["name"] != "Org One" {
		t.Fatalf("organization = %v", org)
	}
}

func TestMeForAClientUser(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	c := e.newClient(o, "Gym", true, 0)
	u := e.newClientUser(o, c.ID, "front@gym.example")
	w := e.asKey(u, "GET", "/api/v1/me", nil)
	requireStatus(t, w, 200)
	m := decodeMap(t, w)
	org := m["organization"].(map[string]any)
	if len(org) != 2 || org["id"] != o.ID.String() || org["name"] != "Org One" {
		t.Fatalf("organization = %v", org)
	}
	if m["user"].(map[string]any)["role"] != "client" {
		t.Fatalf("user = %v", m["user"])
	}
}

var (
	uuidPattern  = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	emailPattern = regexp.MustCompile(`"email":"[^"]*"`)
)

// The OpenAPI type of Me.organization changes while the Go field stays any. This test pins the bytes.
func TestMeWire(t *testing.T) {
	withMeta := func(c *config.Config) {
		c.CoreMetaWebhookURL = "https://calyx.example/meta/webhook"
		c.CoreMetaVerifyToken = "verify-token"
	}
	clientUser := func(e *testEnv, o testOrg) testUser {
		c := e.newClient(o, "Gym", true, 0)
		return e.newClientUser(o, c.ID, "front@gym.example")
	}
	owner := func(e *testEnv, o testOrg) testUser { return o.Owner }
	cases := []struct {
		name string
		org  string
		mods []func(*config.Config)
		who  func(*testEnv, testOrg) testUser
		want string
	}{
		{"owner without meta_webhook", "Org One", nil, owner, `{"create_instance_problem":null,"user":{"id":"ID","email":"EMAIL","role":"owner","client_id":null,"organization_id":"ID"},"organization":{"id":"ID","name":"Org One","max_instances":10,"instance_count":0,"whatsapp_web_allowed":true,"whatsapp_web_acknowledged_at":"2026-11-19T09:00:00Z","meta_webhook":null,"smtp_configured":false}}`},
		{"owner with meta_webhook", "Org One", []func(*config.Config){withMeta}, owner, `{"create_instance_problem":null,"user":{"id":"ID","email":"EMAIL","role":"owner","client_id":null,"organization_id":"ID"},"organization":{"id":"ID","name":"Org One","max_instances":10,"instance_count":0,"whatsapp_web_allowed":true,"whatsapp_web_acknowledged_at":"2026-11-19T09:00:00Z","meta_webhook":{"url":"https://calyx.example/meta/webhook","verify_token":"verify-token"},"smtp_configured":false}}`},
		{"client user", "Org One", nil, clientUser, `{"create_instance_problem":null,"user":{"id":"ID","email":"EMAIL","role":"client","client_id":"ID","organization_id":"ID"},"organization":{"id":"ID","name":"Org One"}}`},
		{"name with special characters", "A & <B>", nil, owner, `{"create_instance_problem":null,"user":{"id":"ID","email":"EMAIL","role":"owner","client_id":null,"organization_id":"ID"},"organization":{"id":"ID","name":"A & <B>","max_instances":10,"instance_count":0,"whatsapp_web_allowed":true,"whatsapp_web_acknowledged_at":"2026-11-19T09:00:00Z","meta_webhook":null,"smtp_configured":false}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newTestEnv(t, c.mods...)
			// The owner email comes from the name, so rename after the login.
			o := e.newOrg("Org One", 10, true, true)
			if _, err := e.Deps.Pool.Exec(context.Background(), `UPDATE organizations SET name = $1`, c.org); err != nil {
				t.Fatal(err)
			}
			w := e.asKey(c.who(e, o), "GET", "/api/v1/me", nil)
			requireStatus(t, w, 200)
			got := uuidPattern.ReplaceAllString(w.Body.String(), "ID")
			got = emailPattern.ReplaceAllString(got, `"email":"EMAIL"`)
			if got != c.want+"\n" {
				t.Fatalf("body = %s\nwant   %s", got, c.want)
			}
		})
	}
}

func TestChangePasswordKeepsThisSession(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	s1 := o.Owner.Cookie
	s2 := e.login(o.Owner.Email)
	w := e.do("PUT", "/api/v1/me/password", map[string]string{"current_password": testPassword, "new_password": "another good password"}, withCookie(s1), withOrigin(goodOrigin))
	requireStatus(t, w, 204)
	requireStatus(t, e.do("GET", "/api/v1/me", nil, withCookie(s1)), 200)
	requireProblem(t, e.do("GET", "/api/v1/me", nil, withCookie(s2)), 401, "unauthenticated")
	requireStatus(t, login(e, o.Owner.Email, "another good password"), 200)
}

func TestChangePasswordRefusesAPIKey(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	w := e.asKey(o.Owner, "PUT", "/api/v1/me/password", map[string]string{"current_password": testPassword, "new_password": "another good password"})
	requireProblem(t, w, 403, "api_key_not_allowed")
	requireStatus(t, e.do("GET", "/api/v1/me", nil, withCookie(o.Owner.Cookie)), 200)
	requireStatus(t, login(e, o.Owner.Email, testPassword), 200)
}

func TestChangePasswordWrongCurrent(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	w := e.asCookie(o.Owner, "PUT", "/api/v1/me/password", map[string]string{"current_password": "wrong one wrong one", "new_password": "another good password"})
	requireProblem(t, w, 400, "invalid_request")
	requireStatus(t, login(e, o.Owner.Email, testPassword), 200)
}

func TestChangePasswordFields(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	requireMissing(t, e.asCookie(o.Owner, "PUT", "/api/v1/me/password", `{"current_password":"x"}`), "new_password")
	requireFields(t, e.asCookie(o.Owner, "PUT", "/api/v1/me/password", `{"current_password":"","new_password":""}`), "current_password", "new_password")
}

func TestCreateListRevokeAPIKey(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	w := e.asCookie(o.Owner, "POST", "/api/v1/api-keys", map[string]string{"name": "n8n"})
	requireStatus(t, w, 201)
	m := decodeMap(t, w)
	tok, _ := m["token"].(string)
	if m["name"] != "n8n" || len(tok) != 46 || !strings.HasPrefix(tok, "cx_") || m["last_used_at"] != nil || m["prefix"] == "" || m["created_at"] == nil || len(m) != 6 {
		t.Fatalf("key = %v", m)
	}
	id := m["id"].(string)
	w = e.asKey(o.Owner, "GET", "/api/v1/api-keys", nil)
	requireStatus(t, w, 200)
	found := false
	for _, it := range decodeMap(t, w)["items"].([]any) {
		k := it.(map[string]any)
		if _, has := k["token"]; has {
			t.Fatalf("a listed key has a token: %v", k)
		}
		if k["id"] == id {
			found = true
		}
	}
	if !found {
		t.Fatal("the new key is not in the list")
	}
	requireStatus(t, e.asKey(o.Owner, "DELETE", "/api/v1/api-keys/"+id, nil), 204)
	requireProblem(t, e.do("GET", "/api/v1/me", nil, withKey(tok)), 401, "unauthenticated")
}

func TestAPIKeyNameRules(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	for _, n := range []string{"", "  ", strings.Repeat("é", 101)} {
		requireProblem(t, e.asCookie(o.Owner, "POST", "/api/v1/api-keys", map[string]string{"name": n}), 400, "invalid_request")
	}
	requireMissing(t, e.asCookie(o.Owner, "POST", "/api/v1/api-keys", `{}`), "name")
	requireStatus(t, e.asCookie(o.Owner, "POST", "/api/v1/api-keys", map[string]string{"name": strings.Repeat("é", 100)}), 201)
}

func TestRevokeAnotherUsersKey(t *testing.T) {
	e := newTestEnv(t)
	a := e.newOrg("Org A", 10, true, true)
	b := e.newOrg("Org B", 10, true, true)
	w := e.asCookie(b.Owner, "POST", "/api/v1/api-keys", map[string]string{"name": "b"})
	requireStatus(t, w, 201)
	bk := decodeMap(t, w)
	requireProblem(t, e.asKey(a.Owner, "DELETE", "/api/v1/api-keys/"+bk["id"].(string), nil), 404, "not_found")
	requireProblem(t, e.asKey(a.Owner, "DELETE", "/api/v1/api-keys/"+uuid.NewString(), nil), 404, "not_found")
	requireProblem(t, e.asKey(a.Owner, "DELETE", "/api/v1/api-keys/abc", nil), 404, "not_found")
	requireStatus(t, e.do("GET", "/api/v1/me", nil, withKey(bk["token"].(string))), 200)
}

func TestUnknownFieldIsRefused(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	w := e.do("POST", "/api/v1/auth/login", map[string]any{"email": o.Owner.Email, "password": testPassword, "remember": true})
	requireProblem(t, w, 400, "invalid_request")
	if got := problemErrors(t, w); fmt.Sprint(got) != "[[body.remember unexpected property]]" {
		t.Fatalf("errors = %v", got)
	}
	if findCookie(w, "calyx_session") != nil {
		t.Fatal("a refused login sets a cookie")
	}
}

func TestResetRequestRateLimitIs429(t *testing.T) {
	e := newTestEnv(t)
	for i := 0; i < 10; i++ {
		requireStatus(t, e.do("POST", "/api/v1/auth/password-reset", map[string]string{"email": "n" + string(rune('a'+i)) + "@example.com"}), 202)
	}
	w := e.do("POST", "/api/v1/auth/password-reset", map[string]string{"email": "nz@example.com"})
	requireProblem(t, w, 429, "rate_limited")
	if w.Header().Get("Retry-After") != "60" {
		t.Fatalf("Retry-After = %q", w.Header().Get("Retry-After"))
	}
}

func TestAnEmptyKeyListIsAnArray(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	ids := decodeMap(t, e.asKey(o.Owner, "GET", "/api/v1/api-keys", nil))["items"].([]any)
	if len(ids) != 1 {
		t.Fatalf("items = %v", ids)
	}
	// Revoke the only key with the session, then list with the session.
	id := ids[0].(map[string]any)["id"].(string)
	requireStatus(t, e.asCookie(o.Owner, "DELETE", "/api/v1/api-keys/"+id, nil), 204)
	w := e.do("GET", "/api/v1/api-keys", nil, withCookie(o.Owner.Cookie))
	requireStatus(t, w, 200)
	if body := strings.TrimSpace(w.Body.String()); body != `{"items":[]}` {
		t.Fatalf("body = %s", body)
	}
}

func TestTheAnswersKeepTheirHeaders(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	for _, w := range []*httptest.ResponseRecorder{
		login(e, o.Owner.Email, testPassword),
		e.asKey(o.Owner, "GET", "/api/v1/me", nil),
		e.do("POST", "/api/v1/auth/password-reset", map[string]string{"email": "nobody@example.com"}),
	} {
		requireNoStore(t, w)
		if w.Code == 200 && w.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("Content-Type = %q", w.Header().Get("Content-Type"))
		}
	}
	w := e.asCookie(o.Owner, "POST", "/api/v1/auth/logout", nil)
	requireNoStore(t, w)
	if w.Body.Len() != 0 || len(w.Header().Values("Set-Cookie")) != 1 {
		t.Fatalf("logout: body %q, Set-Cookie %q", w.Body.String(), w.Header().Values("Set-Cookie"))
	}
}

// The limited fields of the area tell their limit in the description, from the service constant,
// and set no maxLength.
func TestTheLimitsAreInTheDescriptions(t *testing.T) {
	_, api := newMux(Deps{Config: testConfigForRoutes()})
	doc := api.OpenAPI()
	field := func(opPath, method, name string) *huma.Schema {
		t.Helper()
		op := doc.Paths[opPath].Post
		if method == "PUT" {
			op = doc.Paths[opPath].Put
		}
		s := op.RequestBody.Content["application/json"].Schema
		if s.Ref != "" {
			s = doc.Components.Schemas.SchemaFromRef(s.Ref)
		}
		return s.Properties[name]
	}
	cases := []struct {
		path, method, field, want string
	}{
		{"/api/v1/api-keys", "POST", "name", "1 to 100 characters"},
		{"/api/v1/auth/password-reset", "POST", "email", "At most 254 bytes"},
		{"/api/v1/auth/password-reset/confirm", "POST", "password", "10 to 128 characters"},
		{"/api/v1/invitations/accept", "POST", "password", "10 to 128 characters"},
		{"/api/v1/me/password", "PUT", "new_password", "10 to 128 characters"},
	}
	for _, c := range cases {
		s := field(c.path, c.method, c.field)
		if s == nil || !strings.Contains(s.Description, c.want) || s.MaxLength != nil || s.MinLength != nil {
			t.Errorf("%s %s %s: schema = %+v", c.method, c.path, c.field, s)
		}
	}
}

// create_instance_problem tells a client user why it cannot create a WhatsApp Web instance. An
// owner always gets null.
func TestMeCreateInstanceProblem(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, false, true)
	open := e.newClient(o, "Open", true, 0)
	closed := e.newClient(o, "Closed", false, 0)
	cases := []struct {
		name string
		who  testUser
		want any
	}{
		{"owner", o.Owner, nil},
		{"client user, WhatsApp Web blocked", e.newClientUser(o, open.ID, "open@example.com"), "whatsapp_web_not_allowed"},
		{"client user, no permission", e.newClientUser(o, closed.ID, "closed@example.com"), "whatsapp_web_not_allowed"},
	}
	for _, c := range cases {
		m := decodeMap(t, e.asKey(c.who, "GET", "/api/v1/me", nil))
		if v, ok := m["create_instance_problem"]; !ok || v != c.want {
			t.Errorf("%s: create_instance_problem = %v, want %v", c.name, v, c.want)
		}
	}
	if _, err := e.Deps.Pool.Exec(context.Background(), `UPDATE organizations SET whatsapp_web_allowed = true`); err != nil {
		t.Fatal(err)
	}
	m := decodeMap(t, e.asKey(cases[2].who, "GET", "/api/v1/me", nil))
	if m["create_instance_problem"] != "instance_creation_not_allowed" {
		t.Errorf("create_instance_problem = %v, want instance_creation_not_allowed", m["create_instance_problem"])
	}
}

func TestLookupInvitation(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	c := e.newClient(o, "Gym", true, 0)
	tok := inviteClientUser(t, e, o, c.ID, "front@gym.example")
	w := e.do("POST", "/api/v1/invitations/lookup", map[string]string{"token": tok})
	requireStatus(t, w, 200)
	requireNoStore(t, w)
	m := decodeMap(t, w)
	if m["email"] != "front@gym.example" || m["organization_name"] != "Org One" || m["expires_at"] != "2026-11-26T09:00:00Z" || len(m) != 3 {
		t.Fatalf("answer = %v", m)
	}
	// The lookup does not use the token.
	requireStatus(t, e.do("POST", "/api/v1/invitations/accept", map[string]string{"token": tok, "password": "a brand new password"}), 200)
	requireProblem(t, e.do("POST", "/api/v1/invitations/lookup", map[string]string{"token": tok}), 410, "token_invalid")
	requireProblem(t, e.do("POST", "/api/v1/invitations/lookup", map[string]string{"token": "unknown"}), 410, "token_invalid")
	requireFields(t, e.do("POST", "/api/v1/invitations/lookup", map[string]string{"token": ""}), "token")
	requireMissing(t, e.do("POST", "/api/v1/invitations/lookup", `{}`), "token")
	requireProblem(t, e.do("POST", "/api/v1/invitations/lookup", map[string]string{"token": tok}, withOrigin("https://evil.example")), 403, "csrf_check_failed")
}

func TestLookupPasswordReset(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	link, err := e.Deps.Auth.CreatePasswordReset(context.Background(), o.Owner.Email)
	if err != nil {
		t.Fatal(err)
	}
	tok := tokenFromLink(t, link)
	w := e.do("POST", "/api/v1/auth/password-reset/lookup", map[string]string{"token": tok})
	requireStatus(t, w, 200)
	requireNoStore(t, w)
	if m := decodeMap(t, w); m["email"] != o.Owner.Email || len(m) != 1 {
		t.Fatalf("answer = %v", m)
	}
	// The lookup does not use the token.
	requireStatus(t, e.do("POST", "/api/v1/auth/password-reset/confirm", map[string]string{"token": tok, "password": "a brand new password"}), 204)
	requireProblem(t, e.do("POST", "/api/v1/auth/password-reset/lookup", map[string]string{"token": tok}), 410, "token_invalid")
	requireFields(t, e.do("POST", "/api/v1/auth/password-reset/lookup", map[string]string{"token": ""}), "token")
	requireMissing(t, e.do("POST", "/api/v1/auth/password-reset/lookup", `{}`), "token")
	requireProblem(t, e.do("POST", "/api/v1/auth/password-reset/lookup", map[string]string{"token": tok}, withOrigin("https://evil.example")), 403, "csrf_check_failed")
}

// The two lookups share one limit per IP: 10 requests per minute.
func TestTheLookupsShareOneRateLimit(t *testing.T) {
	e := newTestEnv(t)
	for i := range 10 {
		path := "/api/v1/invitations/lookup"
		if i%2 == 1 {
			path = "/api/v1/auth/password-reset/lookup"
		}
		requireProblem(t, e.do("POST", path, map[string]string{"token": "unknown"}), 410, "token_invalid")
	}
	for _, path := range []string{"/api/v1/invitations/lookup", "/api/v1/auth/password-reset/lookup"} {
		w := e.do("POST", path, map[string]string{"token": "unknown"})
		requireProblem(t, w, 429, "rate_limited")
		if w.Header().Get("Retry-After") == "" {
			t.Fatalf("%s: no Retry-After", path)
		}
	}
}
