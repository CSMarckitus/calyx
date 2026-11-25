package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/tenancy"
)

// committedDocument is the committed copy of the document, relative to this package.
const committedDocument = "../../../../packages/shared-types/openapi/backend.json"

// operationIDs lists the operation id of each route of routeTable, in order.
var operationIDs = []string{
	"login", "logout", "requestPasswordReset", "confirmPasswordReset", "acceptInvitation",
	"getMe", "changePassword", "listAPIKeys", "createAPIKey", "revokeAPIKey",
	"getOrganization", "updateOrganization", "acknowledgeWhatsAppWeb",
	"listClients", "createClient", "getClient", "updateClient", "deleteClient", "inviteClientUser",
	"listInstances", "createInstance", "getInstance", "updateInstance", "removeInstance",
	"pairInstance", "followPairing", "cancelPairing", "sendMessage",
	"createBulkJob", "listBulkJobs", "getBulkJob", "cancelBulkJob",
	"lookupInvitation", "lookupPasswordReset", "cancelInvitation", "removeClientAccess", "followEvents",
}

// unlimitedFields are the request string fields with no length limit to document: an enum, a
// value with a fixed form that the service checks, or a value that the service only compares.
// A key is a property name, or "Schema.property" for one schema only. The login email is only
// compared: an invalid one is an unknown email (401), never a 400.
var unlimitedFields = map[string]bool{
	"event": true, "provider": true, "pairing": true, "phone": true, "client_id": true,
	"token": true, "password": true, "current_password": true, "LoginRequest.email": true,
}

func generatedDocument(t *testing.T) []byte {
	t.Helper()
	b, err := OpenAPIDocument()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func loadOpenAPI(t *testing.T) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(generatedDocument(t), &doc); err != nil {
		t.Fatalf("the document is not valid JSON: %v", err)
	}
	return doc
}

func operations(doc map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for p, item := range doc["paths"].(map[string]any) {
		for m, op := range item.(map[string]any) {
			switch m {
			case "get", "put", "post", "patch", "delete":
				out[p+" "+m] = op.(map[string]any)
			}
		}
	}
	return out
}

func opKey(method, pattern string) string { return pattern + " " + strings.ToLower(method) }

func TestOpenAPIHeader(t *testing.T) {
	doc := loadOpenAPI(t)
	info := doc["info"].(map[string]any)
	if doc["openapi"] != "3.1.0" || info["title"] != "Calyx backend API" || info["version"] != "v1" {
		t.Fatalf("header = %v %v", doc["openapi"], info)
	}
	if license, _ := info["license"].(map[string]any); license["name"] != "Apache-2.0" {
		t.Fatalf("license = %v, want Apache-2.0", info["license"])
	}
	description, _ := info["description"].(string)
	for _, want := range []string{"## Start here", "/api/v1", "Authorization: Bearer", "Idempotency-Key", "application/problem+json", "Retry-After"} {
		if !strings.Contains(description, want) {
			t.Errorf("info.description lacks %q", want)
		}
	}
	if description != startHere {
		t.Errorf("info.description = %q, want %q", description, startHere)
	}
	schemes := doc["components"].(map[string]any)["securitySchemes"].(map[string]any)
	for name, want := range schemeDescriptions {
		if got := schemes[name].(map[string]any)["description"]; got != want {
			t.Errorf("security scheme %s description = %v, want %s", name, got, want)
		}
	}
}

// startHere is the approved "Start here" text of info.description.
const startHere = "## Start here\n\n" +
	"1. Get an API key from the person who manages Calyx, or create one on the Calyx web page. A key starts with `cx_`.\n" +
	"2. Send each request to `/api/v1` on the address of Calyx, with the header `Authorization: Bearer <key>`.\n" +
	"3. Call `GET /api/v1/me` to check the key.\n" +
	"4. Send a message with `POST /api/v1/instances/{id}/messages` and an `Idempotency-Key` header. A second request with the\n" +
	"   same key never sends the message again.\n" +
	"5. An error answer has the type `application/problem+json`. Read its `code`. When the answer has a `Retry-After` header,\n" +
	"   wait that number of seconds before the next request."

// schemeDescriptions are the approved texts of the two security schemes.
var schemeDescriptions = map[string]string{
	"apiKey":  "Send an API key in the header `Authorization: Bearer <key>`. Programs use this scheme.",
	"session": "The browser sends the login cookie of Calyx. Programs cannot use this scheme.",
}

// schemaProperty returns the property of a named schema of the document.
func schemaProperty(t *testing.T, doc map[string]any, schema, property string) map[string]any {
	t.Helper()
	s, ok := doc["components"].(map[string]any)["schemas"].(map[string]any)[schema].(map[string]any)
	if !ok {
		t.Fatalf("no schema %s", schema)
	}
	p, ok := s["properties"].(map[string]any)[property].(map[string]any)
	if !ok {
		t.Fatalf("no property %s.%s", schema, property)
	}
	return p
}

// requireEnum checks the enum of a property, in order.
func requireEnum(t *testing.T, p map[string]any, name string, want ...any) {
	t.Helper()
	got, _ := p["enum"].([]any)
	if !slices.Equal(got, want) {
		t.Errorf("%s enum = %v, want %v", name, got, want)
	}
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// The answer schemas list the values of their closed fields. The lists of the request schemas
// stay out: TestRequestSchemasUseOnlyTheShapeKeywords.
func TestOpenAPIEnums(t *testing.T) {
	doc := loadOpenAPI(t)
	codes := apierr.Codes()
	if len(codes) != 35 {
		t.Fatalf("%d codes, want 35", len(codes))
	}
	requireEnum(t, schemaProperty(t, doc, "Problem", "code"), "Problem.code", toAny(codes)...)

	// The values are written here on purpose: a change of a list must fail this test.
	statuses := []any{"CONNECTED", "CONNECTING", "NEEDS_AUTH", "DISCONNECTED", "ERROR"}
	requireEnum(t, schemaProperty(t, doc, "Instance", "provider"), "Instance.provider", "WHATSAPP_WEB", "META_CLOUD")
	requireEnum(t, schemaProperty(t, doc, "Instance", "state"), "Instance.state", "creating", "active", "removing", "missing")
	status := schemaProperty(t, doc, "Instance", "status")
	requireEnum(t, status, "Instance.status", append(slices.Clone(statuses), nil)...)
	if typ, _ := status["type"].([]any); !slices.Equal(typ, []any{"string", "null"}) {
		t.Errorf("Instance.status type = %v, want string and null", status["type"])
	}
	requireEnum(t, schemaProperty(t, doc, "Pairing", "status"), "Pairing.status", statuses...)
	requireEnum(t, schemaProperty(t, doc, "Done", "status"), "Done.status", "CONNECTED")
	requireEnum(t, schemaProperty(t, doc, "BulkJob", "status"), "BulkJob.status",
		"QUEUED", "RUNNING", "COMPLETED", "CANCELLED", "FAILED")
	requireEnum(t, schemaProperty(t, doc, "Failed", "code"), "Failed.code",
		"pairing_expired", "pairing_failed", "pairing_cancelled")
}

// The document is indented JSON with one trailing newline, and two builds give the same bytes.
func TestOpenAPIDocumentIsStable(t *testing.T) {
	a, b := generatedDocument(t), generatedDocument(t)
	if !bytes.Equal(a, b) {
		t.Fatal("two builds of the document differ")
	}
	if !bytes.HasPrefix(a, []byte("{\n  \"")) || !bytes.HasSuffix(a, []byte("}\n")) || bytes.HasSuffix(a, []byte("\n\n")) {
		t.Fatalf("the document is not indented JSON with one trailing newline: %q ... %q", a[:10], a[len(a)-10:])
	}
}

// The served document and the committed copy are the generated document. After a change to an
// operation, run "pnpm --filter @calyx/backend openapi:generate".
func TestTheServedDocumentIsTheCommittedCopy(t *testing.T) {
	want := generatedDocument(t)
	committed, err := os.ReadFile(committedDocument)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(committed, want) {
		t.Fatal("the committed copy is out of date: run pnpm --filter @calyx/backend openapi:generate")
	}
	h := NewServer(Deps{Config: testConfigForRoutes(), Logger: slog.New(slog.DiscardHandler)})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	requireStatus(t, w, 200)
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if !bytes.Equal(w.Body.Bytes(), want) {
		t.Fatal("the served document is not the generated document")
	}
}

func TestOpenAPIMatchesTheRoutes(t *testing.T) {
	ops := operations(loadOpenAPI(t))
	if len(ops) != len(routeTable) {
		t.Errorf("%d operations, want %d", len(ops), len(routeTable))
	}
	for i, rk := range routeTable {
		op, ok := ops[opKey(rk.Method, rk.Pattern)]
		if !ok {
			t.Errorf("no operation for %s %s", rk.Method, rk.Pattern)
			continue
		}
		if op["operationId"] != operationIDs[i] {
			t.Errorf("%s %s: operationId = %v, want %s", rk.Method, rk.Pattern, op["operationId"], operationIDs[i])
		}
		if d, _ := op["description"].(string); d == "" {
			t.Errorf("%s %s has no description", rk.Method, rk.Pattern)
		}
	}
}

// Every operation has its access in the Metadata (it is not in the document), and its security
// in the document follows it.
func TestOpenAPISecurityMatchesTheAccess(t *testing.T) {
	ops := operations(loadOpenAPI(t))
	for _, r := range apiRoutes() {
		op := ops[opKey(r.Method, r.Pattern)]
		sec, _ := op["security"].([]any)
		var names []string
		for _, s := range sec {
			for k := range s.(map[string]any) {
				names = append(names, k)
			}
		}
		sort.Strings(names)
		want := "[apiKey session]"
		switch {
		case r.Auth == Public:
			want = "[]"
		case r.access == accessLoggedInCookie:
			want = "[session]"
		}
		if fmt.Sprint(names) != want {
			t.Errorf("%s %s: security = %v, want %s", r.Method, r.Pattern, names, want)
		}
	}
	_, api := newMux(Deps{Config: testConfigForRoutes()})
	for _, item := range api.OpenAPI().Paths {
		for _, op := range []*huma.Operation{item.Get, item.Put, item.Post, item.Patch, item.Delete} {
			if op == nil {
				continue
			}
			if acc, _ := op.Metadata[metaAccess].(access); acc != accessPublic && acc != accessLoggedIn && acc != accessLoggedInCookie {
				t.Errorf("%s has no access", op.OperationID)
			}
		}
	}
}

// The error answer of every operation is the default response, a problem document. No
// operation documents a 422.
func TestOpenAPIErrorsAreTheDefaultProblem(t *testing.T) {
	doc := loadOpenAPI(t)
	for key, op := range operations(doc) {
		responses := op["responses"].(map[string]any)
		if _, ok := responses["422"]; ok {
			t.Errorf("%s documents a 422", key)
		}
		def, ok := responses["default"].(map[string]any)
		if !ok {
			t.Errorf("%s has no default response", key)
			continue
		}
		content, _ := def["content"].(map[string]any)
		mt, _ := content["application/problem+json"].(map[string]any)
		schema, _ := mt["schema"].(map[string]any)
		if schema["$ref"] != "#/components/schemas/Problem" {
			t.Errorf("%s: default response = %v", key, def)
		}
		two := false
		for code := range responses {
			two = two || strings.HasPrefix(code, "2")
		}
		if !two {
			t.Errorf("%s has no 2xx response", key)
		}
	}
	problem := doc["components"].(map[string]any)["schemas"].(map[string]any)["Problem"].(map[string]any)
	props := problem["properties"].(map[string]any)
	for _, k := range []string{"type", "title", "status", "detail", "code", "errors"} {
		if _, ok := props[k]; !ok {
			t.Errorf("Problem has no %s", k)
		}
	}
}

// Every limited field of a request body has its limit, a number, in its description.
func TestOpenAPILimitedFieldsHaveTheirLimit(t *testing.T) {
	doc := loadOpenAPI(t)
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	digit := regexp.MustCompile(`[0-9]`)
	seen := map[string]bool{}
	var walk func(where string, s map[string]any)
	walk = func(where string, s map[string]any) {
		if ref, ok := s["$ref"].(string); ok {
			name := strings.TrimPrefix(ref, "#/components/schemas/")
			if !seen[name] {
				seen[name] = true
				walk(name, schemas[name].(map[string]any))
			}
			return
		}
		if items, ok := s["items"].(map[string]any); ok {
			walk(where+"[]", items)
		}
		props, _ := s["properties"].(map[string]any)
		for name, p := range props {
			ps := p.(map[string]any)
			walk(where+"."+name, ps)
			if isString(ps) && ps["enum"] == nil && !unlimitedFields[name] && !unlimitedFields[where+"."+name] && !digit.MatchString(fmt.Sprint(ps["description"])) {
				t.Errorf("%s.%s has no limit in its description: %v", where, name, ps["description"])
			}
		}
	}
	for key, op := range operations(doc) {
		body, ok := op["requestBody"].(map[string]any)
		if !ok {
			continue
		}
		walk(key, body["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any))
	}
	if len(seen) == 0 {
		t.Fatal("no request schema was read")
	}
}

// isString is true for "type": "string" and for "type": ["string", "null"].
func isString(s map[string]any) bool {
	switch t := s["type"].(type) {
	case string:
		return t == "string"
	case []any:
		for _, v := range t {
			if v == "string" {
				return true
			}
		}
	}
	return false
}

func TestOpenAPIRefsResolve(t *testing.T) {
	doc := loadOpenAPI(t)
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, val := range x {
				if k != "$ref" {
					walk(val)
					continue
				}
				ref := val.(string)
				if !strings.HasPrefix(ref, "#/components/") {
					t.Errorf("ref %q does not point into #/components/", ref)
					continue
				}
				var cur any = doc
				for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
					m, ok := cur.(map[string]any)
					if !ok {
						t.Errorf("ref %q does not resolve", ref)
						break
					}
					if cur, ok = m[part]; !ok {
						t.Errorf("ref %q does not resolve at %q", ref, part)
						break
					}
				}
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(doc)
}

func TestOpenAPIHasNoPagesOrHealthz(t *testing.T) {
	for p := range loadOpenAPI(t)["paths"].(map[string]any) {
		if !strings.HasPrefix(p, "/api/v1/") {
			t.Errorf("the document has the path %s", p)
		}
	}
}

func TestOpenAPIRequiredTexts(t *testing.T) {
	ops := operations(loadOpenAPI(t))
	desc := func(k string) string { return ops[k]["description"].(string) }
	if !strings.Contains(desc("/api/v1/instances/{id}/messages post"), "A 504 send_outcome_unknown means the message may or may not have been sent. Send an Idempotency-Key header. After a 504, retry with the same key: the core sends the message at most once. After 429 instance_queue_full or 409 send_in_progress, wait for the time in Retry-After, then retry with the same key. Without a key, do not retry automatically.") {
		t.Error("sendMessage lacks the retry warning")
	}
	sm := desc("/api/v1/instances/{id}/messages post")
	for _, code := range []string{"idempotency_key_conflict", "send_in_progress", "idempotency_key_outcome_unknown", "duplicate_message"} {
		if !strings.Contains(sm, "409 "+code) {
			t.Errorf("sendMessage description lacks 409 %s", code)
		}
	}
	if !strings.Contains(desc("/api/v1/instances post"), "A client user can create an instance only when the owner allows it for the client. Errors:") {
		t.Error("createInstance lacks the sentence of the owner permission before Errors")
	}
	bj := desc("/api/v1/instances/{id}/bulk-jobs post")
	if !strings.Contains(bj, "milliseconds") || !strings.Contains(bj, "pause_every counts messages") || !strings.Contains(bj, "bulk_outcome_unknown") {
		t.Errorf("createBulkJob description = %q", bj)
	}
}

// The SSE operation documents its stream and one schema per event type.
func TestOpenAPIPairingStreamHasItsEvents(t *testing.T) {
	op := operations(loadOpenAPI(t))["/api/v1/instances/{id}/pairing get"]
	resp := op["responses"].(map[string]any)["200"].(map[string]any)
	stream, ok := resp["content"].(map[string]any)["text/event-stream"].(map[string]any)
	if !ok {
		t.Fatal("followPairing has no text/event-stream content")
	}
	events := stream["schema"].(map[string]any)["items"].(map[string]any)["oneOf"].([]any)
	var titles []string
	for _, e := range events {
		titles = append(titles, e.(map[string]any)["title"].(string))
	}
	if fmt.Sprint(titles) != "[Event done Event failed Event update]" {
		t.Fatalf("events = %v", titles)
	}
	d := resp["description"].(string)
	for _, w := range []string{"update", "done", "failed", ": ping"} {
		if !strings.Contains(d, w) {
			t.Errorf("followPairing does not mention %q", w)
		}
	}
}

func TestOpenAPIRequestSchemasRefuseUnknownFields(t *testing.T) {
	doc := loadOpenAPI(t)
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	n := 0
	for key, op := range operations(doc) {
		body, ok := op["requestBody"].(map[string]any)
		if !ok {
			continue
		}
		ref := body["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)["$ref"].(string)
		s := schemas[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any)
		if s["additionalProperties"] != false {
			t.Errorf("%s: the body schema does not set additionalProperties: false", key)
		}
		n++
	}
	if n == 0 {
		t.Fatal("no request body was read")
	}
}

// The schema names are for SDK authors: no Go suffix (JSON, InputBody) and no generic type name.
func TestOpenAPISchemaNames(t *testing.T) {
	schemas := loadOpenAPI(t)["components"].(map[string]any)["schemas"].(map[string]any)
	for name := range schemas {
		if strings.Contains(name, "JSON") || strings.Contains(name, "InputBody") {
			t.Errorf("schema name %s", name)
		}
	}
	// The whole set is written here on purpose: a generator names its types after these schemas.
	want := []string{
		"AcknowledgementRequest", "Alert", "ApiKey", "ApiKeyList", "BulkJob", "BulkJobList", "BulkMessageInput",
		"BulkPacingInput", "ChangePasswordRequest", "Changed", "Client", "ClientList", "CreateAPIKeyRequest",
		"CreateBulkJobRequest", "CreateClientRequest", "CreateInstanceRequest", "CreateInstanceResult", "Done",
		"EmailRequest", "Failed", "Instance", "InstanceList", "Invitation", "InvitationLookup", "InviteClientUserRequest",
		"LoginRequest", "LookupInvitationRequest", "LookupPasswordResetRequest", "Me", "MeOrganization",
		"MetaCredentialsInput", "MetaWebhook", "Organization", "PairInstanceRequest", "PairResult", "Pairing",
		"PasswordResetLookup", "PendingInvitation", "Problem", "ProblemField", "RangeInput", "Replaced",
		"SendMessageRequest", "SendMessageResult", "TokenPasswordRequest", "UpdateClientRequest",
		"UpdateInstanceRequest", "UpdateOrganizationRequest", "User", "Webhook", "WebhookInput",
	}
	if got := slices.Sorted(maps.Keys(schemas)); !slices.Equal(got, want) {
		t.Errorf("schema names = %q\nwant %q", got, want)
	}
}

// The organization of getMe has the fields of Organization, and a client user gets id and name
// only. A field that is absent for a client user must not be required.
func TestOpenAPIMeOrganization(t *testing.T) {
	doc := loadOpenAPI(t)
	if ref := schemaProperty(t, doc, "Me", "organization")["$ref"]; ref != "#/components/schemas/MeOrganization" {
		t.Errorf("Me.organization $ref = %v", ref)
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	if required := schemas["Me"].(map[string]any)["required"].([]any); !slices.Equal(required, []any{"create_instance_problem", "user", "organization"}) {
		t.Errorf("Me required = %v", required)
	}
	got, ok := schemas["MeOrganization"].(map[string]any)
	if !ok {
		t.Fatal("no schema MeOrganization")
	}
	if required, _ := got["required"].([]any); !slices.Equal(required, []any{"id", "name"}) {
		t.Errorf("MeOrganization required = %v, want id and name", required)
	}
	// The descriptions can differ: MeOrganization says when a field is absent.
	shapes := func(schema string) map[string]any {
		props := schemas[schema].(map[string]any)["properties"].(map[string]any)
		for _, p := range props {
			delete(p.(map[string]any), "description")
		}
		return props
	}
	if props, want := shapes("MeOrganization"), shapes("Organization"); !reflect.DeepEqual(props, want) {
		t.Errorf("MeOrganization properties = %v\nwant those of Organization: %v", props, want)
	}
	if typ, _ := schemaProperty(t, doc, "MeOrganization", "whatsapp_web_acknowledged_at")["type"].([]any); !slices.Equal(typ, []any{"string", "null"}) {
		t.Errorf("whatsapp_web_acknowledged_at type = %v, want string and null", typ)
	}
	oneOf, _ := schemaProperty(t, doc, "MeOrganization", "meta_webhook")["oneOf"].([]any)
	wantOneOf := []any{map[string]any{"$ref": "#/components/schemas/MetaWebhook"}, map[string]any{"type": "null"}}
	if !reflect.DeepEqual(oneOf, wantOneOf) {
		t.Errorf("meta_webhook oneOf = %v, want %v", oneOf, wantOneOf)
	}
}

// excludedOperations are the operations that the client SDKs leave out (CONTRACT.md, part 2).
var excludedOperations = map[string]bool{
	"login": true, "logout": true, "createAPIKey": true, "inviteClientUser": true,
	"requestPasswordReset": true, "confirmPasswordReset": true, "acceptInvitation": true,
	"changePassword": true, "acknowledgeWhatsAppWeb": true,
	"lookupInvitation": true, "lookupPasswordReset": true, "cancelInvitation": true, "removeClientAccess": true,
	"followEvents": true,
}

// The SDKs show the descriptions in the IDE. Each path parameter, each named schema and each of its
// properties has one. A property that refers to a named schema can use the text of that schema.
func TestOpenAPIDescriptions(t *testing.T) {
	doc := loadOpenAPI(t)
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	described := func(s map[string]any) bool { d, _ := s["description"].(string); return d != "" }
	refName := func(s map[string]any) string {
		ref, _ := s["$ref"].(string)
		return strings.TrimPrefix(ref, "#/components/schemas/")
	}
	// refs lists the named schemas that a property refers to, directly or in a oneOf.
	refs := func(s map[string]any) []string {
		names := []string{refName(s)}
		oneOf, _ := s["oneOf"].([]any)
		for _, o := range oneOf {
			names = append(names, refName(o.(map[string]any)))
		}
		return slices.DeleteFunc(names, func(n string) bool { return n == "" })
	}
	hasText := func(s map[string]any) bool {
		return described(s) || slices.ContainsFunc(refs(s), func(n string) bool { return described(schemas[n].(map[string]any)) })
	}

	seen := map[string]bool{}
	var follow func(v any)
	follow = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, e := range x {
				follow(e)
			}
		case map[string]any:
			if name := refName(x); name != "" && !seen[name] {
				seen[name] = true
				s := schemas[name].(map[string]any)
				if !described(s) {
					t.Errorf("schema %s has no description", name)
				}
				props, _ := s["properties"].(map[string]any)
				for p, ps := range props {
					if !hasText(ps.(map[string]any)) {
						t.Errorf("property %s.%s has no description", name, p)
					}
				}
				follow(s)
			}
			for _, e := range x {
				follow(e)
			}
		}
	}
	n := 0
	for key, op := range operations(doc) {
		if excludedOperations[op["operationId"].(string)] {
			continue
		}
		n++
		params, _ := op["parameters"].([]any)
		for _, p := range params {
			if param := p.(map[string]any); param["in"] == "path" && !described(param) {
				t.Errorf("%s: path parameter %s has no description", key, param["name"])
			}
		}
		follow(op["requestBody"])
		follow(op["responses"])
	}
	if want := len(operationIDs) - len(excludedOperations); n != want {
		t.Errorf("%d operations checked, want %d", n, want)
	}
}

// The fields of the clients, the invitations and the lookups are required. create_instance_problem
// is an open string or null, not an enum, and pending_invitation is a PendingInvitation or null.
func TestOpenAPIRequiredFieldsOfClientsAndLookups(t *testing.T) {
	doc := loadOpenAPI(t)
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	required := func(name string) []any {
		s, _ := schemas[name].(map[string]any)
		r, _ := s["required"].([]any)
		return r
	}
	wantRequired := map[string][]any{
		"Client": {"id", "name", "is_default", "can_create_instances", "max_instances", "instance_count",
			"user_email", "pending_invitation"},
		"PendingInvitation":   {"email", "expires_at"},
		"InvitationLookup":    {"email", "organization_name", "expires_at"},
		"PasswordResetLookup": {"email"},
	}
	for name, want := range wantRequired {
		if got := required(name); !slices.Equal(got, want) {
			t.Errorf("%s required = %v, want %v", name, got, want)
		}
	}
	problem := schemaProperty(t, doc, "Me", "create_instance_problem")
	if typ, _ := problem["type"].([]any); !slices.Equal(typ, []any{"string", "null"}) || problem["enum"] != nil {
		t.Errorf("create_instance_problem = %v, want an open string or null", problem)
	}
	oneOf, _ := schemaProperty(t, doc, "Client", "pending_invitation")["oneOf"].([]any)
	wantOneOf := []any{map[string]any{"$ref": "#/components/schemas/PendingInvitation"}, map[string]any{"type": "null"}}
	if !reflect.DeepEqual(oneOf, wantOneOf) {
		t.Errorf("pending_invitation oneOf = %v, want %v", oneOf, wantOneOf)
	}
}

// The descriptions say how invitations, removed users, conflicts and failed pairings behave.
func TestOpenAPIDescriptionsOfEdgeCases(t *testing.T) {
	doc := loadOpenAPI(t)
	byID := map[string]string{}
	for _, op := range operations(doc) {
		byID[op["operationId"].(string)], _ = op["description"].(string)
	}
	cases := map[string][]string{
		"acceptInvitation": {"The user is created, or a user whose access was removed from the same client returns with the new password, and is logged in."},
		"getMe":            {"An owner gets the whole organization, and create_instance_problem is null. A client user gets only the id and the name of its organization, and create_instance_problem."},
		"pairInstance":     {"409 conflict (the instance is disabled or another change of it is running)"},
		"updateInstance":   {"409 conflict (another change of the instance is running)"},
		"followPairing":    {"404 not_found (no session runs and the backend keeps no failure). For 5 minutes after a pairing failed the stream gives the event failed and ends."},
		"inviteClientUser": {"An email whose access was removed from this client can return."},
		"cancelBulkJob":    {"A finished job is returned unchanged."},
	}
	for id, texts := range cases {
		for _, text := range texts {
			if !strings.Contains(byID[id], text) {
				t.Errorf("%s description = %q, want %q in it", id, byID[id], text)
			}
		}
	}
	if strings.Contains(byID["cancelBulkJob"], "409") {
		t.Errorf("cancelBulkJob still documents a 409: %q", byID["cancelBulkJob"])
	}
	secret, _ := schemaProperty(t, doc, "WebhookInput", "secret")["description"].(string)
	if !strings.Contains(secret, "On PATCH: absent or null keeps the stored secret only when the event and the URL do not change, an empty string removes it, another value replaces it.") {
		t.Errorf("WebhookInput.secret = %q", secret)
	}
}

// TestOpenAPIRolesFollowTenancy checks that the role marker comes from tenancy.RolesFor, and that
// a public operation has none.
func TestOpenAPIRolesFollowTenancy(t *testing.T) {
	ops := operations(loadOpenAPI(t))
	routes := apiRoutes()
	if len(routes) == 0 {
		t.Fatal("no routes")
	}
	for _, r := range routes {
		op, ok := ops[opKey(r.Method, r.Pattern)]
		if !ok {
			t.Fatalf("%s %s is not in the document", r.Method, r.Pattern)
		}
		marker, has := op["x-calyx-roles"]
		if r.Action == "" {
			if has {
				t.Errorf("%s: a public operation has x-calyx-roles %v", r.OperationID, marker)
			}
			continue
		}
		var want []any
		for _, role := range tenancy.RolesFor(r.Action) {
			want = append(want, string(role))
		}
		if len(want) == 0 || !reflect.DeepEqual(marker, want) {
			t.Errorf("%s: x-calyx-roles = %v, want %v", r.OperationID, marker, want)
		}
	}
}

// tagDescriptions are the tags of the document, in order, with their approved texts.
var tagDescriptions = []struct{ name, description string }{
	{"Account", "Your user, your password and your API keys."},
	{"Organization", "The organization and its settings. Only the owner can call these operations."},
	{"Clients", "The clients of the organization and their users. Only the owner can call these operations."},
	{"Instances", "The WhatsApp numbers: create, read, change, remove and pair them."},
	{"Messages", "Send one message from an instance."},
	{"Bulk jobs", "Send one text or a list of messages to many recipients, with the pacing of the core."},
	{"Events", "The stream of changes for the Calyx web page. Programs use webhooks instead."},
}

// tagOfOperation is the one tag of each operation.
var tagOfOperation = map[string]string{
	"login": "Account", "logout": "Account", "requestPasswordReset": "Account",
	"confirmPasswordReset": "Account", "acceptInvitation": "Account", "getMe": "Account",
	"changePassword": "Account", "listAPIKeys": "Account", "createAPIKey": "Account",
	"revokeAPIKey": "Account", "lookupInvitation": "Account", "lookupPasswordReset": "Account",
	"getOrganization": "Organization", "updateOrganization": "Organization",
	"acknowledgeWhatsAppWeb": "Organization",
	"listClients":            "Clients", "createClient": "Clients", "getClient": "Clients",
	"updateClient": "Clients", "deleteClient": "Clients", "inviteClientUser": "Clients",
	"cancelInvitation": "Clients", "removeClientAccess": "Clients",
	"listInstances": "Instances", "createInstance": "Instances", "getInstance": "Instances",
	"updateInstance": "Instances", "removeInstance": "Instances", "pairInstance": "Instances",
	"followPairing": "Instances", "cancelPairing": "Instances",
	"sendMessage":   "Messages",
	"createBulkJob": "Bulk jobs",
	"listBulkJobs":  "Bulk jobs",
	"getBulkJob":    "Bulk jobs",
	"cancelBulkJob": "Bulk jobs",
	"followEvents":  "Events",
}

// TestOpenAPITagsOfTheOperations checks that each operation has exactly its one tag, and that the
// tag list of the document has the seven tags with their texts, in order.
func TestOpenAPITagsOfTheOperations(t *testing.T) {
	doc := loadOpenAPI(t)
	if len(tagOfOperation) != len(operationIDs) {
		t.Fatalf("the map has %d operations, want %d", len(tagOfOperation), len(operationIDs))
	}
	for key, op := range operations(doc) {
		id, _ := op["operationId"].(string)
		want, ok := tagOfOperation[id]
		if !ok {
			t.Errorf("%s (%s) is not in the tag map", id, key)
			continue
		}
		if got := op["tags"]; !reflect.DeepEqual(got, []any{want}) {
			t.Errorf("%s: tags = %v, want [%s]", id, got, want)
		}
	}
	var got []map[string]any
	rawTags, _ := doc["tags"].([]any)
	for _, raw := range rawTags {
		got = append(got, raw.(map[string]any))
	}
	if len(got) != len(tagDescriptions) {
		t.Fatalf("the document has %d tags, want %d: %v", len(got), len(tagDescriptions), got)
	}
	for i, want := range tagDescriptions {
		if got[i]["name"] != want.name || got[i]["description"] != want.description {
			t.Errorf("tag %d = %v, want %s: %s", i, got[i], want.name, want.description)
		}
	}
}

// TestOpenAPICreateInstanceExamples checks the two named request examples of createInstance.
// Each one must decode into the input type with unknown fields refused.
func TestOpenAPICreateInstanceExamples(t *testing.T) {
	doc := loadOpenAPI(t)
	op := operations(doc)[opKey(http.MethodPost, "/api/v1/instances")]
	media := op["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)
	if ref := media["schema"].(map[string]any)["$ref"]; ref != "#/components/schemas/CreateInstanceRequest" {
		t.Fatalf("the body schema is %v, want CreateInstanceRequest", ref)
	}
	examples, _ := media["examples"].(map[string]any)
	want := map[string]string{
		"WhatsApp Web": `{"display_name": "Shop", "provider": "WHATSAPP_WEB", "locale": "es-ES", "pairing": "qr"}`,
		"Meta Cloud API": `{"display_name": "Support", "provider": "META_CLOUD", "meta_credentials": ` +
			`{"phone_number_id": "123456789012345", "access_token": "<access token>", "app_secret": "<app secret>"}}`,
	}
	if got := slices.Sorted(maps.Keys(examples)); !reflect.DeepEqual(got, slices.Sorted(maps.Keys(want))) {
		t.Fatalf("the examples are %v, want %v", got, slices.Sorted(maps.Keys(want)))
	}
	for name, body := range want {
		value, err := json.Marshal(examples[name].(map[string]any)["value"])
		if err != nil {
			t.Fatal(err)
		}
		var got, exp any
		if err := json.Unmarshal(value, &got); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(body), &exp); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, exp) {
			t.Errorf("%s: the value is %s, want %s", name, value, body)
		}
		var in createInstanceInput
		dec := json.NewDecoder(bytes.NewReader(value))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in.Body); err != nil {
			t.Errorf("%s: the value does not decode into the input type: %v", name, err)
		}
	}
}
