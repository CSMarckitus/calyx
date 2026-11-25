package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"calyx/backend/internal/config"
)

func TestEveryAnswerHasARequestID(t *testing.T) {
	e := newTestEnv(t)
	a := e.do("GET", "/api/v1/me", nil).Header().Get("X-Request-Id")
	b := e.do("GET", "/api/v1/me", nil).Header().Get("X-Request-Id")
	if _, err := uuid.Parse(a); err != nil {
		t.Fatalf("id %q is not a UUID", a)
	}
	if a == b {
		t.Fatal("two requests have the same id")
	}
}

func TestAnIncomingRequestIDIsIgnored(t *testing.T) {
	e := newTestEnv(t)
	w := e.do("GET", "/api/v1/me", nil, withHeader("X-Request-Id", "evil\nline"))
	id := w.Header().Get("X-Request-Id")
	if _, err := uuid.Parse(id); err != nil {
		t.Fatalf("id %q is not a UUID", id)
	}
	if strings.Contains(e.Logs.String(), "evil") {
		t.Fatalf("the log has client text: %s", e.Logs.String())
	}
}

func TestAPanicIsA500AndLogged(t *testing.T) {
	e := newTestEnv(t)
	s := newServer(e.Deps)
	h := s.chain(Route{Method: "GET", Pattern: "/api/v1/boom", Auth: Public}, func(http.ResponseWriter, *http.Request) {
		panic("kaboom")
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/boom", nil))
	requireProblem(t, w, 500, "internal")
	id := w.Header().Get("X-Request-Id")
	log := e.Logs.String()
	if !strings.Contains(log, "panic") || !strings.Contains(log, id) || !strings.Contains(log, "kaboom") {
		t.Fatalf("log = %s", log)
	}
	if strings.Contains(w.Body.String(), "kaboom") {
		t.Fatal("the answer shows the panic value")
	}
}

func TestAnAbortedHandlerIsNotAnswered(t *testing.T) {
	e := newTestEnv(t)
	s := newServer(e.Deps)
	h := s.chain(Route{Method: "GET", Pattern: "/api/v1/abort", Auth: Public}, func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	})
	defer func() {
		if err, _ := recover().(error); !errors.Is(err, http.ErrAbortHandler) {
			t.Fatalf("recovered %v, want http.ErrAbortHandler", err)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/abort", nil))
}

func TestTheRequestLogLine(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	logOf := func(w *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		requireStatus(t, w, 200)
		lines := e.Logs.lines()
		if len(lines) != 1 {
			t.Fatalf("log lines = %v", lines)
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
			t.Fatal(err)
		}
		if rec["msg"] != "HTTP request." || rec["method"] != "GET" || rec["route"] != "GET /api/v1/me" ||
			rec["status"] != float64(200) || rec["request_id"] != w.Header().Get("X-Request-Id") || rec["level"] != "INFO" {
			t.Fatalf("record = %v", rec)
		}
		for _, k := range []string{"time", "duration_ms"} {
			if _, ok := rec[k]; !ok {
				t.Fatalf("no key %s in %v", k, rec)
			}
		}
		return rec
	}

	e.Logs.Reset()
	rec := logOf(e.asKey(o.Owner, "GET", "/api/v1/me", nil, withHeader("User-Agent", "sdk/1.0")))
	if len(rec) != 11 || rec["user_agent"] != "sdk/1.0" || rec["auth"] != "api_key" {
		t.Fatalf("API key record = %v", rec)
	}
	if id, _ := rec["api_key_id"].(string); id == "" {
		t.Fatalf("api_key_id = %v", rec["api_key_id"])
	}

	e.Logs.Reset()
	rec = logOf(e.do("GET", "/api/v1/me", nil, withCookie(o.Owner.Cookie)))
	if len(rec) != 10 || rec["user_agent"] != "" || rec["auth"] != "cookie" {
		t.Fatalf("cookie record = %v", rec)
	}
	if _, ok := rec["api_key_id"]; ok {
		t.Fatalf("a cookie line has api_key_id: %v", rec)
	}
}

// The log keeps at most 200 bytes of User-Agent and never cuts a UTF-8 character.
func TestTheLoggedUserAgentIsCutAtACharacter(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	// 199 ASCII bytes, then a 2-byte character that spans bytes 199 and 200 (counted from 0).
	ua := strings.Repeat("a", 199) + "é" + strings.Repeat("b", 299)
	if len(ua) != 500 {
		t.Fatalf("len = %d", len(ua))
	}
	e.Logs.Reset()
	requireStatus(t, e.asKey(o.Owner, "GET", "/api/v1/me", nil, withHeader("User-Agent", ua)), 200)
	var rec map[string]any
	if err := json.Unmarshal([]byte(e.Logs.lines()[0]), &rec); err != nil {
		t.Fatal(err)
	}
	got, _ := rec["user_agent"].(string)
	if got != strings.Repeat("a", 199) {
		t.Fatalf("user_agent = %q (%d bytes)", got, len(got))
	}

	// An ASCII value of 500 bytes becomes 200 bytes.
	e.Logs.Reset()
	requireStatus(t, e.asKey(o.Owner, "GET", "/api/v1/me", nil, withHeader("User-Agent", strings.Repeat("c", 500))), 200)
	if err := json.Unmarshal([]byte(e.Logs.lines()[0]), &rec); err != nil {
		t.Fatal(err)
	}
	if got, _ := rec["user_agent"].(string); len(got) != 200 {
		t.Fatalf("user_agent has %d bytes", len(got))
	}
}

func TestHealthzOKIsNotLogged(t *testing.T) {
	e := newTestEnv(t)
	e.Logs.Reset()
	w := e.do("GET", "/healthz", nil)
	requireStatus(t, w, 200)
	if len(e.Logs.lines()) != 0 {
		t.Fatalf("log = %s", e.Logs.String())
	}
}

func TestNoCredentialsIs401(t *testing.T) {
	e := newTestEnv(t)
	d := requireProblem(t, e.do("GET", "/api/v1/me", nil), 401, "unauthenticated")
	if d != "Log in or send an API key." {
		t.Fatalf("detail = %q", d)
	}
}

func TestAWrongSchemeIs401(t *testing.T) {
	e := newTestEnv(t)
	d := requireProblem(t, e.do("GET", "/api/v1/me", nil, withHeader("Authorization", "Basic x")), 401, "unauthenticated")
	if d != "Use Authorization: Bearer <API key>." {
		t.Fatalf("detail = %q", d)
	}
}

func TestAnUnknownKeyIs401(t *testing.T) {
	e := newTestEnv(t)
	requireProblem(t, e.do("GET", "/api/v1/me", nil, withKey("cx_unknown")), 401, "unauthenticated")
}

func TestTheCookieAuthenticates(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	w := e.do("GET", "/api/v1/me", nil, withCookie(o.Owner.Cookie))
	requireStatus(t, w, 200)
}

func TestTheKeyWinsOverTheCookie(t *testing.T) {
	e := newTestEnv(t)
	a := e.newOrg("Org A", 10, true, true)
	b := e.newOrg("Org B", 10, true, true)
	w := e.do("GET", "/api/v1/me", nil, withKey(a.Owner.Key), withCookie(b.Owner.Cookie))
	requireStatus(t, w, 200)
	user := decodeMap(t, w)["user"].(map[string]any)
	if user["email"] != a.Owner.Email {
		t.Fatalf("user = %v, want %s", user, a.Owner.Email)
	}
}

func TestCookiePostWithoutOriginIsRefused(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	w := e.do("POST", "/api/v1/api-keys", map[string]string{"name": "n"}, withCookie(o.Owner.Cookie))
	requireProblem(t, w, 403, "csrf_check_failed")
}

func TestCookiePostWithAnotherOriginIsRefused(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	w := e.do("POST", "/api/v1/api-keys", map[string]string{"name": "n"}, withCookie(o.Owner.Cookie), withOrigin("https://evil.example"))
	requireProblem(t, w, 403, "csrf_check_failed")
}

func TestCookiePostWithTheRightOriginIsAccepted(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	w := e.do("POST", "/api/v1/api-keys", map[string]string{"name": "n"}, withCookie(o.Owner.Cookie), withOrigin("https://calyx.example"))
	requireStatus(t, w, 201)
}

func TestCookieCSRFMethods(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	for _, c := range []struct{ method, path string }{
		{"PUT", "/api/v1/me/password"},
		{"PATCH", "/api/v1/organization"},
		{"DELETE", "/api/v1/api-keys/" + uuid.NewString()},
	} {
		w := e.do(c.method, c.path, `{}`, withCookie(o.Owner.Cookie))
		requireProblem(t, w, 403, "csrf_check_failed")
	}
	requireStatus(t, e.do("GET", "/api/v1/me", nil, withCookie(o.Owner.Cookie)), 200)
}

func TestKeyDeleteWithoutOriginIsAccepted(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	id := decodeMap(t, e.asCookie(o.Owner, "POST", "/api/v1/api-keys", map[string]string{"name": "n"}))["id"].(string)
	requireStatus(t, e.asKey(o.Owner, "DELETE", "/api/v1/api-keys/"+id, nil), 204)
}

func TestCookieOnlyOperationsNeedAnOrigin(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	c := e.newClient(o, "Gym", true, 0)
	for _, r := range []struct{ method, path string }{
		{"POST", "/api/v1/auth/logout"},
		{"PUT", "/api/v1/me/password"},
		{"POST", "/api/v1/api-keys"},
		{"POST", "/api/v1/organization/whatsapp-web-acknowledgement"},
		{"POST", "/api/v1/clients/" + c.ID.String() + "/invitations"},
	} {
		w := e.do(r.method, r.path, `{}`, withCookie(o.Owner.Cookie))
		requireProblem(t, w, 403, "csrf_check_failed")
	}
}

func TestCreateAPIKeyWithAKeyLogsTheRefusal(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	k, err := e.Deps.Auth.CreateAPIKey(context.Background(), o.Owner.Principal, "used")
	if err != nil {
		t.Fatal(err)
	}
	e.Logs.Reset()
	requireProblem(t, e.do("POST", "/api/v1/api-keys", map[string]string{"name": "n"}, withKey(k.Token)), 403, "api_key_not_allowed")
	found := false
	for _, line := range e.Logs.lines() {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		if rec["msg"] != "API key refused." {
			continue
		}
		found = true
		if rec["route"] != "POST /api/v1/api-keys" || rec["api_key_id"] != k.ID.String() {
			t.Fatalf("line = %s, want the route and api_key_id %s", line, k.ID)
		}
	}
	if !found {
		t.Fatalf("no Warn line: %s", e.Logs.String())
	}
}

func TestPublicPostWithAForeignOriginIsRefused(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	w := e.do("POST", "/api/v1/auth/login", map[string]string{"email": o.Owner.Email, "password": testPassword}, withOrigin("https://evil.example"))
	requireProblem(t, w, 403, "csrf_check_failed")
}

func TestPublicPostWithoutOriginIsAccepted(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	w := e.do("POST", "/api/v1/auth/login", map[string]string{"email": o.Owner.Email, "password": testPassword})
	requireStatus(t, w, 200)
}

func TestPublicOriginDropsDefaultPorts(t *testing.T) {
	for in, want := range map[string]string{
		"https://a.example:443":     "https://a.example",
		"http://a.example:80":       "http://a.example",
		"http://127.0.0.1:8080":     "http://127.0.0.1:8080",
		"https://A.Example/base":    "https://a.example",
		"https://a.example:8443/":   "https://a.example:8443",
		"http://a.example:443":      "http://a.example:443",
		"https://calyx.example":     "https://calyx.example",
		"http://localhost:80/x?y=1": "http://localhost",
	} {
		u, err := url.Parse(in)
		if err != nil {
			t.Fatal(err)
		}
		if got := publicOrigin(u); got != want {
			t.Errorf("publicOrigin(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestClientIPWithoutTrustProxy(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.9:5000"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := computeClientIP(r, false); got != "10.0.0.9" {
		t.Fatalf("ip = %s", got)
	}
}

func TestClientIPWithTrustProxy(t *testing.T) {
	for xff, want := range map[string]string{
		"6.6.6.6, 1.2.3.4": "1.2.3.4",
		"garbage":          "10.0.0.9",
		"":                 "10.0.0.9",
		"1.2.3.4":          "1.2.3.4",
		"6.6.6.6, bad":     "10.0.0.9",
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = "10.0.0.9:5000"
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		if got := computeClientIP(r, true); got != want {
			t.Errorf("XFF %q: ip = %s, want %s", xff, got, want)
		}
	}
}

func TestTheLoginRateLimitUsesTheForwardedIP(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) { c.TrustProxy = true })
	login := func(email, xff string) int {
		w := e.do("POST", "/api/v1/auth/login", map[string]string{"email": email, "password": "wrong password"}, withHeader("X-Forwarded-For", xff))
		return w.Code
	}
	for i := 0; i < 10; i++ {
		if got := login("u"+string(rune('a'+i))+"@example.com", "1.2.3.4"); got != 401 {
			t.Fatalf("login %d = %d, want 401", i+1, got)
		}
	}
	if got := login("uk@example.com", "1.2.3.4"); got != 429 {
		t.Fatalf("11th login = %d, want 429", got)
	}
	if got := login("ul@example.com", "5.6.7.8"); got != 401 {
		t.Fatalf("12th login from another IP = %d, want 401", got)
	}
}

func TestClientIPInTheContextUsesTrustProxy(t *testing.T) {
	for _, trust := range []bool{false, true} {
		e := newTestEnv(t, func(c *config.Config) { c.TrustProxy = trust })
		var got string
		h := newServer(e.Deps).chain(Route{Method: "GET", Pattern: "/api/v1/ip", Auth: Public}, func(_ http.ResponseWriter, r *http.Request) {
			got = clientIP(r)
		})
		r := httptest.NewRequest(http.MethodGet, "/api/v1/ip", nil)
		r.RemoteAddr = "10.0.0.9:5000"
		r.Header.Set("X-Forwarded-For", "1.2.3.4")
		h.ServeHTTP(httptest.NewRecorder(), r)
		want := "10.0.0.9"
		if trust {
			want = "1.2.3.4"
		}
		if got != want {
			t.Fatalf("trust %v: ip = %s, want %s", trust, got, want)
		}
	}
}

func TestClientIPUsesTheLastForwardedHeaderLine(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.9:5000"
	r.Header.Add("X-Forwarded-For", "6.6.6.6")
	r.Header.Add("X-Forwarded-For", "7.7.7.7, 1.2.3.4")
	if got := computeClientIP(r, true); got != "1.2.3.4" {
		t.Fatalf("ip = %s, want 1.2.3.4", got)
	}
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	r.Header.Add("X-Forwarded-For", "")
	if got := computeClientIP(r, true); got != "10.0.0.9" {
		t.Fatalf("an empty last line: ip = %s", got)
	}
}

func TestACancelledRequestWithNoAnswerIsLogged499(t *testing.T) {
	e := newTestEnv(t)
	s := newServer(e.Deps)
	h := s.chain(Route{Method: "GET", Pattern: "/api/v1/slow", Auth: Public}, func(http.ResponseWriter, *http.Request) {})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.Logs.Reset()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/slow", nil).WithContext(ctx))
	if log := e.Logs.String(); !strings.Contains(log, `"status":499`) {
		t.Fatalf("log = %s", log)
	}
}

func TestCookieOnlyOperationRefusesAPIKey(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	mux, api := newMux(e.Deps)
	registerTestOps(api)
	url := testOpURL() + "/cookie"
	body := `{"name":"x","kind":"a"}`

	e.Logs.Reset()
	w := send(mux, "POST", url, "application/json", body, withKey(o.Owner.Key))
	d := requireProblem(t, w, 403, "api_key_not_allowed")
	if d != "This operation needs a login in a browser. An API key cannot use it." {
		t.Fatalf("detail = %q", d)
	}
	warned := false
	for _, line := range e.Logs.lines() {
		if strings.Contains(line, `"msg":"API key refused."`) && strings.Contains(line, `"api_key_id"`) && strings.Contains(line, "/cookie") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("no Warn line: %s", e.Logs.String())
	}

	// A cookie POST needs the Origin, as on every logged-in operation.
	requireProblem(t, send(mux, "POST", url, "application/json", body, withCookie(o.Owner.Cookie)), 403, "csrf_check_failed")
	requireStatus(t, send(mux, "POST", url, "application/json", body, withCookie(o.Owner.Cookie), withOrigin(goodOrigin)), 200)
}
