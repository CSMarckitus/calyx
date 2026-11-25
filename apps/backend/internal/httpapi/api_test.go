package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/google/uuid"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/config"
	"calyx/backend/internal/tenancy"
)

// testOpInput is the input of the test operations: a path id and a body with one field of each
// kind of the shape rules.
type testOpInput struct {
	ID   uuid.UUID `path:"id" format:"uuid"`
	Body struct {
		Name string  `json:"name"`
		Kind string  `json:"kind" enum:"a,b"`
		Note *string `json:"note,omitempty" nullable:"true"`
		Pad  string  `json:"pad,omitempty"`
	}
}

type testOpOutput struct {
	Body struct {
		Name string `json:"name"`
	}
}

// testOpHandler answers the name of the body, or the error that the name asks for.
func testOpHandler(ctx context.Context, in *testOpInput) (*testOpOutput, error) {
	switch in.Body.Name {
	case "service-fields":
		return nil, serviceError(ctx, apierr.InvalidFields(apierr.FieldError{Field: "name", Message: "is wrong."}))
	case "service-rate-limited":
		return nil, serviceError(ctx, apierr.New(429, apierr.CodeRateLimited, "Too many attempts."))
	case "service-plain":
		return nil, serviceError(ctx, errors.New("db password=service-secret"))
	case "service-canceled":
		return nil, serviceError(ctx, context.Canceled)
	case "huma-plain":
		// Not through the helper: Huma's own error path must still give the problem body.
		return nil, errors.New("db password=huma-secret")
	case "huma-canceled":
		return nil, context.Canceled
	}
	out := &testOpOutput{}
	out.Body.Name = in.Body.Name
	return out, nil
}

const testOpPath = "/api/v1/test-ops/"

// registerTestOps adds a public operation and an owner-only operation to api.
func registerTestOps(api huma.API) {
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "testPublic", Method: http.MethodPost, Path: "/api/v1/test-ops/{id}",
	}, accessPublic, ""), testOpHandler)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "testOwnerOnly", Method: http.MethodPost, Path: "/api/v1/test-ops/{id}/owner",
	}, accessLoggedIn, tenancy.ManageOrganization), testOpHandler)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "testCookieOnly", Method: http.MethodPost, Path: "/api/v1/test-ops/{id}/cookie",
	}, accessLoggedInCookie, tenancy.ManageOwnAccount), testOpHandler)
}

// newTestAPI returns the handler of NewServer with the test operations, and its log. It needs no
// database: the public test operation calls no service.
func newTestAPI(t *testing.T) (http.Handler, *syncBuffer) {
	t.Helper()
	logs := &syncBuffer{}
	mux, api := newMux(Deps{Config: testConfigForRoutes(), Logger: config.NewLogger("json", logs)})
	registerTestOps(api)
	return mux, logs
}

func send(h http.Handler, method, path, contentType string, body string, opts ...reqOpt) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	for _, o := range opts {
		o(r)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func testOpURL() string { return testOpPath + uuid.NewString() }

func postTestOp(h http.Handler, body string) *httptest.ResponseRecorder {
	return send(h, "POST", testOpURL(), "application/json", body)
}

// problemErrors returns the location and the message of each item of errors[].
func problemErrors(t *testing.T, w *httptest.ResponseRecorder) [][2]string {
	t.Helper()
	var p struct {
		Errors []struct{ Location, Message string } `json:"errors"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	var out [][2]string
	for _, e := range p.Errors {
		out = append(out, [2]string{e.Location, e.Message})
	}
	return out
}

func requireNoStore(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if got := w.Header().Values("Cache-Control"); len(got) != 1 || got[0] != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

func TestATestOperationAnswers(t *testing.T) {
	h, logs := newTestAPI(t)
	w := postTestOp(h, `{"name":"ok","kind":"a"}`)
	requireStatus(t, w, 200)
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
	requireNoStore(t, w)
	if w.Header().Get("Link") != "" {
		t.Fatalf("Link = %q", w.Header().Get("Link"))
	}
	m := decodeMap(t, w)
	if _, ok := m["$schema"]; ok || len(m) != 1 || m["name"] != "ok" {
		t.Fatalf("body = %v", m)
	}
	// The outer wrapper runs: a request id and one request log line with the route pattern.
	if _, err := uuid.Parse(w.Header().Get("X-Request-Id")); err != nil {
		t.Fatalf("X-Request-Id = %q", w.Header().Get("X-Request-Id"))
	}
	if l := logs.String(); !contains(l, `"route":"POST /api/v1/test-ops/{id}"`) || !contains(l, `"status":200`) {
		t.Fatalf("log = %s", l)
	}
}

func TestAnExplicitNullIsAccepted(t *testing.T) {
	h, _ := newTestAPI(t)
	requireStatus(t, postTestOp(h, `{"name":"ok","kind":"a","note":null}`), 200)
}

func TestTheOtherOperationsAndTheCatchAllStay(t *testing.T) {
	h, _ := newTestAPI(t)
	requireProblem(t, send(h, "GET", "/api/v1/organization", "", ""), 401, "unauthenticated")
	requireProblem(t, send(h, "GET", "/api/v1/nothing", "", ""), 404, "not_found")
	// A known Huma path with another method is not a route either.
	requireProblem(t, send(h, "GET", testOpURL(), "", ""), 404, "not_found")
}

func TestABodyWithoutJSONContentTypeIsRefused(t *testing.T) {
	h, _ := newTestAPI(t)
	for _, ct := range []string{"", "text/plain", "application/jsonx"} {
		w := send(h, "POST", testOpURL(), ct, `{"name":"ok","kind":"a"}`)
		if d := requireProblem(t, w, 400, "invalid_request"); d != "Content-Type must be application/json." {
			t.Fatalf("%q: detail = %q", ct, d)
		}
		requireNoStore(t, w)
	}
	requireStatus(t, send(h, "POST", testOpURL(), "application/json; charset=utf-8", `{"name":"ok","kind":"a"}`), 200)
}

// Bridge rows 2 and 3, through HTTP: each class of error that Huma creates.
func TestHumaErrorClassesBecomeTheProblemBody(t *testing.T) {
	h, _ := newTestAPI(t)
	cases := []struct {
		name, path, body string
		status           int
		code             string
		detail           string
		errors           [][2]string
	}{
		{"malformed id", testOpPath + "abc", `{"name":"ok","kind":"a"}`, 404, "not_found", "The resource does not exist.", nil},
		{"malformed id and body", testOpPath + "abc", `{"kind":5}`, 404, "not_found", "The resource does not exist.", nil},
		{"missing field", "", `{"kind":"a"}`, 400, "invalid_request", "The request has invalid fields.",
			[][2]string{{"body", "expected required property name to be present"}}},
		{"wrong type", "", `{"name":98765,"kind":"a"}`, 400, "invalid_request", "The request has invalid fields.",
			[][2]string{{"body.name", "expected string"}}},
		{"enum", "", `{"name":"ok","kind":"zzz-value"}`, 400, "invalid_request", "The request has invalid fields.",
			[][2]string{{"body.kind", `expected value to be one of "a, b"`}}},
		{"unknown field", "", `{"name":"ok","kind":"a","extra":1}`, 400, "invalid_request", "The request has invalid fields.",
			[][2]string{{"body.extra", "unexpected property"}}},
		{"malformed JSON", "", `{"name": s3cret}`, 400, "invalid_request", "The request has invalid fields.",
			[][2]string{{"body", "The body is not valid JSON."}}},
		{"empty body", "", ``, 400, "invalid_request", "The body is empty.", nil},
		{"data after the object", "", `{"name":"ok","kind":"a"} {}`, 400, "invalid_request", "The request has invalid fields.",
			[][2]string{{"body", "The body is not valid JSON."}}},
		{"not an object", "", `[1]`, 400, "invalid_request", "The request has invalid fields.",
			[][2]string{{"body", "expected object"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := c.path
			if path == "" {
				path = testOpURL()
			}
			w := send(h, "POST", path, "application/json", c.body)
			if d := requireProblem(t, w, c.status, c.code); d != c.detail {
				t.Fatalf("detail = %q, want %q", d, c.detail)
			}
			requireNoStore(t, w)
			if got := problemErrors(t, w); fmt.Sprint(got) != fmt.Sprint(c.errors) {
				t.Fatalf("errors = %v, want %v", got, c.errors)
			}
			// Huma's Value holds the rejected input; the bridge never copies it.
			for _, s := range []string{"98765", "zzz-value", "s3cret", `"value"`} {
				if strings.Contains(w.Body.String(), s) {
					t.Fatalf("the answer holds %q: %s", s, w.Body.String())
				}
			}
		})
	}
}

func TestTheBodyLimitIs64KiB(t *testing.T) {
	h, _ := newTestAPI(t)
	body := func(size int) string {
		head := `{"name":"ok","kind":"a","pad":"`
		return head + strings.Repeat("x", size-len(head)-2) + `"}`
	}
	if b := body(smallBodyLimit); len(b) != 64<<10 {
		t.Fatalf("len = %d", len(b))
	}
	requireStatus(t, postTestOp(h, body(smallBodyLimit)), 200)
	w := postTestOp(h, body(smallBodyLimit+1))
	if d := requireProblem(t, w, 400, "invalid_request"); d != "The body is larger than 65536 bytes." {
		t.Fatalf("detail = %q", d)
	}
}

// timeoutReader fails like a body read that passed its deadline.
type timeoutReader struct{}

func (timeoutReader) Read([]byte) (int, error) { return 0, timeoutError{} }

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestASlowBodyIs400(t *testing.T) {
	h, _ := newTestAPI(t)
	r := httptest.NewRequest(http.MethodPost, testOpURL(), timeoutReader{})
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if d := requireProblem(t, w, 400, "invalid_request"); d != "The body did not arrive in time." {
		t.Fatalf("detail = %q", d)
	}
}

func TestAServiceErrorGoesThroughTheHelper(t *testing.T) {
	h, logs := newTestAPI(t)
	w := postTestOp(h, `{"name":"service-fields","kind":"a"}`)
	if d := requireProblem(t, w, 400, "invalid_request"); d != "The request has invalid fields." {
		t.Fatalf("detail = %q", d)
	}
	requireNoStore(t, w)
	if got := problemErrors(t, w); fmt.Sprint(got) != "[[body.name is wrong.]]" {
		t.Fatalf("errors = %v", got)
	}
	m := decodeMap(t, w)
	if m["type"] != "https://calyx.dev/errors/invalid_request" || m["title"] != "Bad Request" || m["status"] != float64(400) {
		t.Fatalf("body = %v", m)
	}

	w = postTestOp(h, `{"name":"service-rate-limited","kind":"a"}`)
	requireProblem(t, w, 429, "rate_limited")
	if got := w.Header().Values("Retry-After"); len(got) != 1 || got[0] != "60" {
		t.Fatalf("Retry-After = %q", got)
	}

	for _, name := range []string{"service-plain", "huma-plain"} {
		logs.Reset()
		w = postTestOp(h, `{"name":"`+name+`","kind":"a"}`)
		d := requireProblem(t, w, 500, "internal")
		id := w.Header().Get("X-Request-Id")
		if d != "Internal error. Request id: "+id+"." || strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("%s: body = %s", name, w.Body.String())
		}
		if l := logs.String(); !contains(l, `"msg":"Internal error."`) || !contains(l, id) || !contains(l, "password=") {
			t.Fatalf("%s: log = %s", name, l)
		}
	}
}

// Bridge row 1, and the same rule for a service error: a client that left gets no body and no
// Error log, and the request log says 499.
func TestAClientThatLeftGetsNoBody(t *testing.T) {
	h, logs := newTestAPI(t)
	for _, name := range []string{"service-canceled", "huma-canceled"} {
		logs.Reset()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		r := httptest.NewRequest(http.MethodPost, testOpURL(), strings.NewReader(`{"name":"`+name+`","kind":"a"}`)).WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Body.Len() != 0 {
			t.Fatalf("%s: body = %s", name, w.Body.String())
		}
		l := logs.String()
		if contains(l, `"level":"ERROR"`) || !contains(l, `"status":499`) {
			t.Fatalf("%s: log = %s", name, l)
		}
	}
}

// humaContext returns a Huma context for a request with a request id and a logger.
func humaContext(ctx context.Context, logs *syncBuffer) huma.Context {
	ctx = withRequestID(ctx, "rid-1")
	ctx = withLogger(ctx, config.NewLogger("json", logs))
	r := httptest.NewRequest(http.MethodPost, "/x", nil).WithContext(ctx)
	return humago.NewContext(&huma.Operation{MaxBodyBytes: smallBodyLimit}, r, httptest.NewRecorder())
}

// Each row of the bridge table, also for the classes that the API cannot reach today.
func TestTheBridgeTable(t *testing.T) {
	detail := func(loc, msg string) error {
		return &huma.ErrorDetail{Location: loc, Message: msg, Value: "rejected-value"}
	}
	cases := []struct {
		name     string
		canceled bool
		status   int
		errs     []error
		want     int
		code     string
		logged   bool
	}{
		{"1 client left", true, 500, []error{context.Canceled}, statusClientClosed, "", false},
		{"1 client left, 4xx", true, 422, []error{detail("body.name", "expected string")}, statusClientClosed, "", false},
		{"2 path id", false, 422, []error{detail("body.name", "expected string"), detail("path.id", "expected uuid")}, 404, "not_found", false},
		{"3 shape", false, 422, []error{detail("body.name", "expected string")}, 400, "invalid_request", false},
		{"3 malformed", false, 400, []error{detail("body", "The body is not valid JSON.")}, 400, "invalid_request", false},
		{"3 timeout", false, 408, nil, 400, "invalid_request", false},
		{"3 too large", false, 413, nil, 400, "invalid_request", false},
		{"3 content type", false, 415, []error{detail("body", "unknown content type")}, 400, "invalid_request", false},
		{"4 status 0", false, 0, nil, 500, "internal", true},
		{"4 500", false, 500, []error{errors.New("read failed")}, 500, "internal", true},
		{"4 503", false, 503, nil, 500, "internal", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			logs := &syncBuffer{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if c.canceled {
				cancel()
			}
			se := huma.NewErrorWithContext(humaContext(ctx, logs), c.status, "msg", c.errs...)
			p, ok := se.(*problem)
			if !ok {
				t.Fatalf("type = %T", se)
			}
			if p.GetStatus() != c.want || string(p.Code) != c.code {
				t.Fatalf("problem = %+v", p)
			}
			if c.code != "" && (p.Type != problemTypeBase+c.code || p.Title != http.StatusText(c.want)) {
				t.Fatalf("problem = %+v", p)
			}
			raw, _ := json.Marshal(p)
			if strings.Contains(string(raw), "rejected-value") {
				t.Fatalf("the problem holds the value: %s", raw)
			}
			if got := contains(logs.String(), `"level":"ERROR"`); got != c.logged {
				t.Fatalf("logged = %v, log = %s", got, logs.String())
			}
		})
	}
}

func TestTheBridgeCopiesLocationAndMessage(t *testing.T) {
	errs := []error{
		&huma.ErrorDetail{Location: "body.name", Message: "expected string", Value: 5},
		&huma.ErrorDetail{Location: "body", Message: "unexpected property", Value: "x"},
	}
	p := huma.NewErrorWithContext(humaContext(context.Background(), &syncBuffer{}), 422, "validation failed", errs...).(*problem)
	if fmt.Sprint(p.Errors) != "[{body.name expected string} {body unexpected property}]" {
		t.Fatalf("errors = %v", p.Errors)
	}
	if p.Detail != "The request has invalid fields." {
		t.Fatalf("detail = %q", p.Detail)
	}
	// The 413 detail names the limit of the operation.
	p = huma.NewErrorWithContext(humaContext(context.Background(), &syncBuffer{}), 413, "too large").(*problem)
	if p.Detail != "The body is larger than 65536 bytes." || len(p.Errors) != 0 {
		t.Fatalf("problem = %+v", p)
	}
}

func TestTheBridgeKeepsTwentyFieldErrors(t *testing.T) {
	errs := make([]error, 22)
	for i := range errs {
		errs[i] = &huma.ErrorDetail{Location: "body.f", Message: "bad"}
	}
	p := huma.NewErrorWithContext(humaContext(context.Background(), &syncBuffer{}), 422, "v", errs...).(*problem)
	if len(p.Errors) != 21 || p.Errors[20] != (problemField{Location: "body", Message: "2 more field errors."}) {
		t.Fatalf("errors = %v", p.Errors)
	}
}

func TestTheDocumentedErrorIsTheProblem(t *testing.T) {
	// defineErrors builds the error schema from NewError(0, ""): it must be the problem type,
	// built without a request and without a log line.
	if _, ok := huma.NewError(0, "").(*problem); !ok {
		t.Fatal("huma.NewError does not return *problem")
	}
	if p := (&problem{}); p.ContentType("application/json") != "application/problem+json" {
		t.Fatal("the content type of a problem is not application/problem+json")
	}
}

func TestEveryOperationNeedsItsAccess(t *testing.T) {
	cases := map[string]huma.Operation{
		"no access":              {OperationID: "a", Method: http.MethodGet, Path: "/api/v1/a"},
		"logged_in, no action":   withAccess(huma.Operation{OperationID: "b", Method: http.MethodGet, Path: "/api/v1/b"}, accessLoggedIn, ""),
		"cookie only, no action": withAccess(huma.Operation{OperationID: "c", Method: http.MethodGet, Path: "/api/v1/c"}, accessLoggedInCookie, ""),
	}
	for name, op := range cases {
		t.Run(name, func(t *testing.T) {
			_, api := newMux(Deps{Config: testConfigForRoutes()})
			defer func() {
				if recover() == nil {
					t.Fatal("registration did not panic")
				}
			}()
			huma.Register(api, op, func(context.Context, *struct{}) (*struct{}, error) { return nil, nil })
		})
	}
}

func TestTheAccessHelperSetsTheLimits(t *testing.T) {
	op := withAccess(huma.Operation{}, accessLoggedIn, tenancy.UseInstances)
	if op.MaxBodyBytes != smallBodyLimit || op.BodyReadTimeout.Seconds() != 5 {
		t.Fatalf("limits = %d %v", op.MaxBodyBytes, op.BodyReadTimeout)
	}
	if op.Metadata["access"] != accessLoggedIn || op.Metadata["action"] != tenancy.UseInstances {
		t.Fatalf("metadata = %v", op.Metadata)
	}
	if len(op.Security) != 2 {
		t.Fatalf("security = %v", op.Security)
	}
	op = withAccess(huma.Operation{}, accessLoggedInCookie, tenancy.ManageOwnAccount)
	if op.Metadata["access"] != accessLoggedInCookie || op.Metadata["action"] != tenancy.ManageOwnAccount {
		t.Fatalf("metadata = %v", op.Metadata)
	}
	if len(op.Security) != 1 {
		t.Fatalf("security = %v", op.Security)
	}
	if _, ok := op.Security[0]["session"]; !ok {
		t.Fatalf("security = %v, want session only", op.Security)
	}
	op = withAccess(huma.Operation{MaxBodyBytes: bulkBodyLimit}, accessPublic, "")
	if op.MaxBodyBytes != bulkBodyLimit || op.Security != nil {
		t.Fatalf("op = %+v", op)
	}
}

func TestTheAPIConfiguration(t *testing.T) {
	_, api := newMux(Deps{Config: testConfigForRoutes()})
	doc := api.OpenAPI()
	if doc.OpenAPI != "3.1.0" || doc.Info.Version != "v1" {
		t.Fatalf("header = %s %s", doc.OpenAPI, doc.Info.Version)
	}
	s := doc.Components.SecuritySchemes
	if s["session"] == nil || s["session"].Type != "apiKey" || s["session"].In != "cookie" || s["session"].Name != sessionCookie {
		t.Fatalf("session = %+v", s["session"])
	}
	if s["apiKey"] == nil || s["apiKey"].Type != "http" || s["apiKey"].Scheme != "bearer" {
		t.Fatalf("apiKey = %+v", s["apiKey"])
	}
	// No /openapi, /docs or /schemas route of Huma.
	h, _ := newTestAPI(t)
	for _, p := range []string{"/openapi.yaml", "/openapi-3.0.json", "/docs", "/schemas/Problem.json"} {
		if w := send(h, "GET", p, "", ""); w.Code != 404 {
			t.Fatalf("%s: status = %d", p, w.Code)
		}
	}
}

// Authentication, CSRF and authorization run before Huma reads the body.
func TestTheMiddlewareRunsBeforeTheInput(t *testing.T) {
	e := newTestEnv(t)
	mux, api := newMux(e.Deps)
	registerTestOps(api)
	e.Handler = mux
	o := e.newOrg("Org One", 10, true, true)
	c := e.newClient(o, "Client", true, 1)
	cu := e.newClientUser(o, c.ID, "client@example.com")
	path := testOpURL() + "/owner"
	bad := `{"name": 5, "extra": true}`

	requireProblem(t, e.do("POST", path, bad), 401, "unauthenticated")
	requireProblem(t, e.do("POST", path, bad, withCookie(o.Owner.Cookie)), 403, "csrf_check_failed")
	requireProblem(t, e.asKey(cu, "POST", path, bad), 403, "owner_only")
	requireProblem(t, e.asCookie(cu, "POST", path, bad), 403, "owner_only")
	requireProblem(t, e.asKey(o.Owner, "POST", path, bad), 400, "invalid_request")
	requireStatus(t, e.asKey(o.Owner, "POST", path, map[string]string{"name": "ok", "kind": "a"}), 200)
	requireStatus(t, e.asCookie(o.Owner, "POST", path, map[string]string{"name": "ok", "kind": "a"}), 200)
	// Without Content-Type, authentication, CSRF and authorization still answer first, as on the
	// old routes.
	noCT := func(opts ...reqOpt) *httptest.ResponseRecorder {
		return send(e.Handler, "POST", path, "", bad, opts...)
	}
	requireProblem(t, noCT(), 401, "unauthenticated")
	requireProblem(t, noCT(withCookie(o.Owner.Cookie), withOrigin("https://evil.example")), 403, "csrf_check_failed")
	requireProblem(t, noCT(withKey(cu.Key)), 403, "owner_only")
	requireProblem(t, noCT(withKey(o.Owner.Key)), 400, "invalid_request")
	// The public operation checks Origin only when the browser sends it.
	requireProblem(t, e.do("POST", testOpURL(), `{"name":"ok","kind":"a"}`, withOrigin("https://evil.example")), 403, "csrf_check_failed")
}

// allowedSchemaKeys are the keywords that a request schema may use: the shape rules (type,
// required, enum, additionalProperties false) and words that check nothing.
var allowedSchemaKeys = map[string]bool{
	"type": true, "required": true, "enum": true, "properties": true, "items": true,
	"additionalProperties": true, "$ref": true, "description": true, "title": true,
	"examples": true, "deprecated": true, "readOnly": true, "writeOnly": true,
}

// schemaKeywordViolations walks the request schemas of doc (parameters and bodies) and returns
// each keyword that the rule of Component 3 does not allow.
func schemaKeywordViolations(t *testing.T, doc *huma.OpenAPI) []string {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Paths      map[string]map[string]json.RawMessage
		Components struct{ Schemas map[string]map[string]any }
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	var out []string
	seen := map[string]bool{}
	var walk func(where string, s map[string]any, pathParam bool)
	walk = func(where string, s map[string]any, pathParam bool) {
		for k, v := range s {
			switch {
			case k == "format" && pathParam && v == "uuid":
			case k == "additionalProperties" && v != false:
				out = append(out, where+": additionalProperties is not false")
			case !allowedSchemaKeys[k]:
				out = append(out, where+": "+k)
			}
		}
		if ref, ok := s["$ref"].(string); ok {
			name := strings.TrimPrefix(ref, "#/components/schemas/")
			if !seen[name] {
				seen[name] = true
				walk(name, d.Components.Schemas[name], false)
			}
		}
		if props, ok := s["properties"].(map[string]any); ok {
			for name, p := range props {
				walk(where+"."+name, p.(map[string]any), false)
			}
		}
		if items, ok := s["items"].(map[string]any); ok {
			walk(where+"[]", items, false)
		}
	}
	for path, item := range d.Paths {
		for method, rawOp := range item {
			var op struct {
				Parameters []struct {
					In     string
					Schema map[string]any
				}
				RequestBody struct {
					Content map[string]struct{ Schema map[string]any }
				}
			}
			if err := json.Unmarshal(rawOp, &op); err != nil {
				continue // not an operation
			}
			for _, p := range op.Parameters {
				walk(method+" "+path+" "+p.In, p.Schema, p.In == "path")
			}
			for ct, mt := range op.RequestBody.Content {
				walk(method+" "+path+" "+ct, mt.Schema, false)
			}
		}
	}
	return out
}

func TestRequestSchemasUseOnlyTheShapeKeywords(t *testing.T) {
	_, api := newMux(Deps{Config: testConfigForRoutes()})
	registerTestOps(api)
	if v := schemaKeywordViolations(t, api.OpenAPI()); len(v) != 0 {
		t.Fatalf("forbidden keywords: %v", v)
	}
	// The walk finds a limit as a schema rule.
	type limited struct {
		Body struct {
			Name string `json:"name" maxLength:"5"`
		}
	}
	huma.Register(api, withAccess(huma.Operation{OperationID: "limited", Method: http.MethodPost, Path: "/api/v1/limited"}, accessPublic, ""),
		func(context.Context, *limited) (*struct{}, error) { return nil, nil })
	if v := schemaKeywordViolations(t, api.OpenAPI()); len(v) != 1 || !strings.HasSuffix(v[0], ": maxLength") {
		t.Fatalf("violations = %v", v)
	}
}

func TestTheJSONFormatHidesTheInputInDecodeErrors(t *testing.T) {
	var into struct {
		N int8 `json:"n"`
	}
	cases := []struct {
		data string
		v    any
		want string
	}{
		{`{"n": s3cret}`, new(any), "The body is not valid JSON."},
		{`{"n": 98765}`, &into, "Field n has the wrong type."},
		{`[1]`, &into, "The body must hold one JSON object."},
	}
	for _, c := range cases {
		if err := jsonFormat.Unmarshal([]byte(c.data), c.v); err == nil || err.Error() != c.want {
			t.Fatalf("%s: err = %v, want %q", c.data, err, c.want)
		}
	}
	var w strings.Builder
	if err := jsonFormat.Marshal(&w, &problem{Status: statusClientClosed}); err != nil || w.Len() != 0 {
		t.Fatalf("a 499 problem was written: %q %v", w.String(), err)
	}
}

type routeKey struct{ Method, Pattern string }

// routeTable lists every route of the API, in order. A route number is its position, from 1.
var routeTable = []routeKey{
	{"POST", "/api/v1/auth/login"},
	{"POST", "/api/v1/auth/logout"},
	{"POST", "/api/v1/auth/password-reset"},
	{"POST", "/api/v1/auth/password-reset/confirm"},
	{"POST", "/api/v1/invitations/accept"},
	{"GET", "/api/v1/me"},
	{"PUT", "/api/v1/me/password"},
	{"GET", "/api/v1/api-keys"},
	{"POST", "/api/v1/api-keys"},
	{"DELETE", "/api/v1/api-keys/{id}"},
	{"GET", "/api/v1/organization"},
	{"PATCH", "/api/v1/organization"},
	{"POST", "/api/v1/organization/whatsapp-web-acknowledgement"},
	{"GET", "/api/v1/clients"},
	{"POST", "/api/v1/clients"},
	{"GET", "/api/v1/clients/{id}"},
	{"PATCH", "/api/v1/clients/{id}"},
	{"DELETE", "/api/v1/clients/{id}"},
	{"POST", "/api/v1/clients/{id}/invitations"},
	{"GET", "/api/v1/instances"},
	{"POST", "/api/v1/instances"},
	{"GET", "/api/v1/instances/{id}"},
	{"PATCH", "/api/v1/instances/{id}"},
	{"DELETE", "/api/v1/instances/{id}"},
	{"POST", "/api/v1/instances/{id}/pair"},
	{"GET", "/api/v1/instances/{id}/pairing"},
	{"DELETE", "/api/v1/instances/{id}/pairing"},
	{"POST", "/api/v1/instances/{id}/messages"},
	{"POST", "/api/v1/instances/{id}/bulk-jobs"},
	{"GET", "/api/v1/instances/{id}/bulk-jobs"},
	{"GET", "/api/v1/bulk-jobs/{id}"},
	{"POST", "/api/v1/bulk-jobs/{id}/cancel"},
	{"POST", "/api/v1/invitations/lookup"},
	{"POST", "/api/v1/auth/password-reset/lookup"},
	{"DELETE", "/api/v1/clients/{id}/invitations"},
	{"DELETE", "/api/v1/clients/{id}/user"},
	{"GET", "/api/v1/events"},
}

// The Huma operations are the 37 routes of routeTable.
func TestThereAre37Routes(t *testing.T) {
	routes := apiRoutes()
	if len(routes) != 37 {
		t.Fatalf("len(apiRoutes) = %d, want 37", len(routes))
	}
	for i, r := range routes {
		if (routeKey{r.Method, r.Pattern}) != routeTable[i] {
			t.Errorf("route %d = %s %s, want %s %s", i+1, r.Method, r.Pattern, routeTable[i].Method, routeTable[i].Pattern)
		}
	}
}

func TestRoutesAreUnique(t *testing.T) {
	seen := map[routeKey]bool{}
	for _, r := range apiRoutes() {
		k := routeKey{r.Method, r.Pattern}
		if seen[k] {
			t.Errorf("duplicate route %s %s", r.Method, r.Pattern)
		}
		seen[k] = true
	}
}

func TestEveryRouteIsUnderAPIV1(t *testing.T) {
	for _, r := range apiRoutes() {
		if !strings.HasPrefix(r.Pattern, "/api/v1/") {
			t.Errorf("%s %s is not under /api/v1/", r.Method, r.Pattern)
		}
	}
}

func TestPublicRoutes(t *testing.T) {
	for i, r := range apiRoutes() {
		n := i + 1
		wantPublic := n == 1 || n == 3 || n == 4 || n == 5 || n == 33 || n == 34
		if (r.Auth == Public) != wantPublic {
			t.Errorf("route %d %s %s: Public = %v, want %v", n, r.Method, r.Pattern, r.Auth == Public, wantPublic)
		}
	}
}

func TestNewServerDoesNotPanic(t *testing.T) {
	NewServer(Deps{Config: testConfigForRoutes()})
}

func TestUnknownAPIPathIs404Problem(t *testing.T) {
	e := newTestEnv(t)
	requireProblem(t, e.do("GET", "/api/v1/nothing", nil), 404, "not_found")
}

func TestUnknownMethodIs404Problem(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	requireProblem(t, e.asKey(o.Owner, "PUT", "/api/v1/me", `{}`), 404, "not_found")
	e.Logs.Reset()
	e.do("GET", "/api/v1/nothing", nil)
	if l := e.Logs.String(); !contains(l, `"route":"unmatched"`) {
		t.Fatalf("the catch-all is not named unmatched in the log: %s", l)
	}
}

func TestANonUUIDIdIs404(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	requireProblem(t, e.asKey(o.Owner, "GET", "/api/v1/instances/abc", nil), 404, "not_found")
	requireProblem(t, e.asKey(o.Owner, "GET", "/api/v1/bulk-jobs/abc", nil), 404, "not_found")
}

func TestNewServerRefusesANilPublicURL(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewServer accepted a nil PublicURL")
		}
	}()
	NewServer(Deps{})
}
