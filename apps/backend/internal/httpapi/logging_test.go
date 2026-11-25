package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"calyx/backend/internal/tenancy"
	"calyx/backend/internal/testutil"
	corev1 "github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// TestNoContentInLogs runs the flows that carry secrets and content through the whole server and
// checks that none of it reaches the log.
func TestNoContentInLogs(t *testing.T) {
	const (
		msgText      = "LOG-TEXT-7f3a"
		msgTo        = "34600999888"
		badTo        = "34600999777"
		bulkText     = "LOG-BULK-9c1d"
		bulkTo1      = "34600999001"
		bulkTo2      = "34600999002"
		customText   = "LOG-CUSTOM-55aa"
		pw1          = "Log-Test-Password-1"
		pw2          = "Log-Test-Password-2"
		loginEmail   = "log-owner@example.com"
		clientEmail  = "log-client@example.com"
		metaToken    = "LOG-META-ACCESS-TOKEN-3b"
		metaSecret   = "LOG-META-APP-SECRET-4c"
		hookSecret   = "LOG-HOOK-SECRET-5d"
		internalText = "LOG-INTERNAL-6e"
		shapeValue   = "LOG-SHAPE-7a"
		badJSONValue = "LOG-BAD-JSON-8b"
		idemKey      = "LOG-IDEM-KEY-9f"
	)
	e := newTestEnv(t)
	ctx := context.Background()
	requests := 0
	do := func(method, path string, body any, opts ...reqOpt) *httptest.ResponseRecorder {
		requests++
		return e.do(method, path, body, opts...)
	}
	var secrets []string
	remember := func(v ...string) { secrets = append(secrets, v...) }
	remember(msgText, msgTo, badTo, bulkText, bulkTo1, bulkTo2, customText, pw1, pw2, loginEmail, clientEmail, metaToken, metaSecret, hookSecret, internalText, shapeValue, badJSONValue, idemKey)

	org, _, err := e.Deps.Tenancy.CreateOrganization(ctx, tenancy.NewOrganization{Name: "Log Org", MaxInstances: 10, WhatsAppWebAllowed: true})
	if err != nil {
		t.Fatal(err)
	}
	// 1. Accept an owner invitation through the API.
	inv, err := e.Deps.Auth.InviteOwner(ctx, org.ID, loginEmail, nil)
	if err != nil {
		t.Fatal(err)
	}
	tok1 := tokenFromLink(t, inv.Link)
	remember(tok1)
	requireStatus(t, do("POST", "/api/v1/invitations/lookup", map[string]string{"token": tok1}), 200)
	w := do("POST", "/api/v1/invitations/accept", map[string]string{"token": tok1, "password": pw1})
	requireStatus(t, w, 200)
	cookie1 := findCookie(w, sessionCookie).Value
	remember(cookie1)
	ownerID := decodeMap(t, w)["id"].(string)
	_ = ownerID
	owner, err := e.Deps.Auth.Authenticate(ctx, cookie1, "")
	if err != nil {
		t.Fatal(err)
	}

	// 2. Accept a client invitation through the API. The page of the link only tells how.
	c, err := e.Deps.Tenancy.CreateClient(ctx, owner, tenancy.ClientInput{Name: "Log Client", CanCreateInstances: true})
	if err != nil {
		t.Fatal(err)
	}
	inv2, err := e.Deps.Auth.InviteClientUser(ctx, owner, c.ID, clientEmail)
	if err != nil {
		t.Fatal(err)
	}
	tok2 := tokenFromLink(t, inv2.Link)
	remember(tok2)
	requireStatus(t, do("GET", "/invite?token="+url.QueryEscape(tok2), nil), 404)
	w = do("POST", "/api/v1/invitations/accept", map[string]string{"token": tok2, "password": pw1})
	requireStatus(t, w, 200)
	remember(findCookie(w, sessionCookie).Value)

	// 3. Log in, then change the password.
	w = do("POST", "/api/v1/auth/login", map[string]string{"email": loginEmail, "password": pw1})
	requireStatus(t, w, 200)
	cookie2 := findCookie(w, sessionCookie).Value
	remember(cookie2)
	requireStatus(t, do("PUT", "/api/v1/me/password", map[string]string{"current_password": pw1, "new_password": pw2}, withCookie(cookie2), withOrigin(goodOrigin)), 204)

	// 4. Reset the password: the request, the page of the link and the API.
	// The invitations sent mails too: wait for the reset mail, so the background reset ends first.
	before := e.Mailer.Attempts()
	requireStatus(t, do("POST", "/api/v1/auth/password-reset", map[string]string{"email": loginEmail}), 202)
	e.Mailer.WaitForAttempts(t, before+1)
	link, err := e.Deps.Auth.CreatePasswordReset(ctx, loginEmail)
	if err != nil {
		t.Fatal(err)
	}
	reset1 := tokenFromLink(t, link)
	remember(reset1)
	requireStatus(t, do("GET", "/reset-password?token="+url.QueryEscape(reset1), nil), 404)
	requireStatus(t, do("POST", "/api/v1/auth/password-reset/confirm", map[string]string{"token": reset1, "password": pw1}), 204)
	link, err = e.Deps.Auth.CreatePasswordReset(ctx, loginEmail)
	if err != nil {
		t.Fatal(err)
	}
	reset2 := tokenFromLink(t, link)
	remember(reset2)
	requireStatus(t, do("POST", "/api/v1/auth/password-reset/lookup", map[string]string{"token": reset2}), 200)
	requireStatus(t, do("POST", "/api/v1/auth/password-reset/confirm", map[string]string{"token": reset2, "password": pw2}), 204)

	// 5. An API key for the rest.
	w = do("POST", "/api/v1/auth/login", map[string]string{"email": loginEmail, "password": pw2})
	requireStatus(t, w, 200)
	cookie3 := findCookie(w, sessionCookie).Value
	remember(cookie3)
	w = do("POST", "/api/v1/api-keys", map[string]string{"name": "log"}, withCookie(cookie3), withOrigin(goodOrigin))
	requireStatus(t, w, 201)
	key := decodeMap(t, w)["token"].(string)
	remember(key)
	bearer := withKey(key)

	// 6. Instance, sends and bulk jobs.
	w = do("POST", "/api/v1/instances", map[string]any{
		"display_name": "Log line", "provider": "META_CLOUD",
		"meta_credentials": map[string]string{"phone_number_id": "9988776655", "access_token": metaToken, "app_secret": metaSecret},
		"webhooks":         []map[string]string{{"url": "https://hooks.example/log", "event": "message.received", "secret": hookSecret}},
	}, bearer)
	requireStatus(t, w, 201)
	id := decodeMap(t, w)["instance"].(map[string]any)["id"].(string)
	e.Fake.OnSendPlainText(func(ctx context.Context, req *corev1.SendPlainTextRequest) (*corev1.SendPlainTextResponse, error) {
		if req.GetTo() == badTo {
			return nil, status.Error(codes.InvalidArgument, "'"+badTo+"' is not a valid recipient.")
		}
		return &corev1.SendPlainTextResponse{MessageId: "wamid.log"}, nil
	})
	requireStatus(t, do("POST", "/api/v1/instances/"+id+"/messages", map[string]string{"to": msgTo, "text": msgText}, bearer), 200)
	requireStatus(t, do("POST", "/api/v1/instances/"+id+"/messages", map[string]string{"to": msgTo, "text": msgText}, bearer, withHeader("Idempotency-Key", idemKey)), 200)
	w = do("POST", "/api/v1/instances/"+id+"/messages", map[string]string{"to": badTo, "text": msgText}, bearer)
	if strings.Contains(w.Body.String(), badTo) {
		t.Fatalf("the answer quotes the recipient: %s", w.Body.String())
	}
	requireFields(t, w, "to")
	requireStatus(t, do("POST", "/api/v1/instances/"+id+"/bulk-jobs", map[string]any{"recipients": []string{bulkTo1, bulkTo2}, "text": bulkText}, bearer), 201)
	w = do("POST", "/api/v1/instances/"+id+"/bulk-jobs", map[string]any{"messages": []map[string]string{{"to": bulkTo1, "text": customText}}}, bearer)
	requireStatus(t, w, 201)
	jobID := decodeMap(t, w)["id"].(string)

	// 7. A 500 from the core.
	e.Fake.SetError(testutil.MethodGetBulkJob, status.Error(codes.Internal, "core failed: "+internalText))
	requireProblem(t, do("GET", "/api/v1/bulk-jobs/"+jobID, nil, bearer), 500, "internal")

	// 8. Huma refuses a field of the wrong type and a malformed body. Huma keeps the rejected
	// value of a field error; neither the answer nor the log holds it.
	for _, body := range []string{
		`{"recipients":["` + bulkTo1 + `"],"text":"x","pacing":{"delay":{"min":"` + shapeValue + `","max":1}}}`,
		`{"recipients":["` + bulkTo1 + `"],"text":` + badJSONValue + `}`,
	} {
		w = do("POST", "/api/v1/instances/"+id+"/bulk-jobs", body, bearer)
		requireProblem(t, w, 400, "invalid_request")
		for _, s := range []string{shapeValue, badJSONValue, bulkTo1} {
			if strings.Contains(w.Body.String(), s) {
				t.Fatalf("the answer holds %q: %s", s, w.Body.String())
			}
		}
	}

	// 9. Remove the access of the client user. The log line names ids, never the email.
	requireStatus(t, do("DELETE", "/api/v1/clients/"+c.ID.String()+"/user", nil, withCookie(cookie3), withOrigin(goodOrigin)), 204)

	// The checks.
	log := e.Logs.String()
	for _, s := range secrets {
		if strings.Contains(log, s) {
			t.Errorf("the log holds %q", s)
		}
	}
	var requestLines, errorLines int
	for _, line := range e.Logs.lines() {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		if rec["level"] == "ERROR" {
			errorLines++
		}
		if rec["msg"] != "HTTP request." {
			continue
		}
		requestLines++
		want := 10
		if rec["auth"] == "api_key" {
			want = 11
		}
		if len(rec) != want {
			t.Errorf("request line keys = %v", rec)
		}
		for _, k := range []string{"time", "level", "msg", "method", "route", "status", "duration_ms", "request_id", "user_agent", "auth"} {
			if _, ok := rec[k]; !ok {
				t.Errorf("request line has no %s: %s", k, line)
			}
		}
	}
	if requestLines != requests {
		t.Errorf("%d request lines for %d requests", requestLines, requests)
	}
	if errorLines == 0 {
		t.Error("the 500 left no error line in the log")
	}
}

// requestLines returns the decoded "HTTP request." lines of the log.
func requestLines(t *testing.T, e *testEnv) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range e.Logs.lines() {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		if rec["msg"] == "HTTP request." {
			out = append(out, rec)
		}
	}
	return out
}

// A tool call logs two lines. The inner line points to the /mcp line with mcp_request_id, and the
// /mcp line has no such field. A value from the client on /mcp does not reach the log.
func TestMCPToolCallLogLink(t *testing.T) {
	const forged = "forged-mcp-id"
	e := newTestEnv(t, mcpOn)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	before := len(requestLines(t, e))
	w, _ := mcpCall(t, e, o.Owner.Key, "tools/call", map[string]any{"name": "get_instance", "arguments": map[string]any{"id": id.String()}}, withHeader("X-Calyx-Mcp-Request-Id", forged))
	requireStatus(t, w, 200)
	lines := requestLines(t, e)[before:]
	if len(lines) != 2 {
		t.Fatalf("%d request lines, want 2: %v", len(lines), lines)
	}
	var outer, inner map[string]any
	for _, l := range lines {
		if l["route"] == "POST /mcp" {
			outer = l
		} else {
			inner = l
		}
	}
	if outer == nil || inner == nil {
		t.Fatalf("lines = %v", lines)
	}
	if _, ok := outer["mcp_request_id"]; ok {
		t.Errorf("the /mcp line has mcp_request_id: %v", outer)
	}
	if inner["mcp_request_id"] != outer["request_id"] {
		t.Errorf("mcp_request_id = %v, want %v", inner["mcp_request_id"], outer["request_id"])
	}
	if len(inner) != 12 || inner["user_agent"] != "" || inner["auth"] != "api_key" {
		t.Errorf("inner line keys = %v", inner)
	}
	if strings.Contains(e.Logs.String(), forged) {
		t.Error("the client value reached the log")
	}
}

// A network request never passes through outerMux.ServeHTTP, so a forged header has no effect.
func TestForgedMCPRequestIDIsNotLogged(t *testing.T) {
	const forged = "forged-mcp-id"
	e := newTestEnv(t, mcpOn)
	o := webOrg(e, "Org One")
	before := len(requestLines(t, e))
	requireStatus(t, e.do("GET", "/api/v1/instances", nil, withKey(o.Owner.Key), withHeader("X-Calyx-Mcp-Request-Id", forged)), 200)
	lines := requestLines(t, e)[before:]
	if len(lines) != 1 {
		t.Fatalf("%d request lines, want 1", len(lines))
	}
	if _, ok := lines[0]["mcp_request_id"]; ok {
		t.Errorf("the line has mcp_request_id: %v", lines[0])
	}
	if strings.Contains(e.Logs.String(), forged) {
		t.Error("the client value reached the log")
	}
}
