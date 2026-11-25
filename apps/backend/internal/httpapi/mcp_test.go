package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	"calyx/backend/internal/auth"
	"calyx/backend/internal/config"
	"calyx/backend/internal/tenancy"
	"calyx/backend/internal/testutil"
	corev1 "github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

func mcpOn(c *config.Config) { c.MCPEnabled = true }

// mcpMethods are the MCP methods that the access tests send. Each one needs an API key.
var mcpMethods = []struct {
	method string
	params any
}{
	{"initialize", map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"}}},
	{"tools/list", nil},
	{"tools/call", map[string]any{"name": "list_instances", "arguments": map[string]any{}}},
}

// No MCP method answers without a valid API key, also with a valid session cookie.
func TestMCPNeedsAnAPIKey(t *testing.T) {
	e := newTestEnv(t, mcpOn)
	o := e.newOrg("Org One", 10, true, true)
	for _, m := range mcpMethods {
		t.Run(m.method, func(t *testing.T) {
			w, _ := mcpCall(t, e, "", m.method, m.params)
			d := requireProblem(t, w, 401, "unauthenticated")
			if d != "This endpoint needs an API key in the Authorization header." {
				t.Fatalf("detail = %q", d)
			}
			if w.Header().Get("WWW-Authenticate") != "" {
				t.Fatalf("WWW-Authenticate = %q", w.Header().Get("WWW-Authenticate"))
			}

			w, _ = mcpCall(t, e, "", m.method, m.params, withCookie(o.Owner.Cookie), withOrigin(goodOrigin))
			d = requireProblem(t, w, 401, "unauthenticated")
			if d != "This endpoint needs an API key in the Authorization header." {
				t.Fatalf("cookie only: detail = %q", d)
			}

			w, _ = mcpCall(t, e, auth.APIKeyPrefix+"wrong", m.method, m.params)
			requireProblem(t, w, 401, "unauthenticated")

			w, _ = mcpCall(t, e, "", m.method, m.params, withHeader("Authorization", "Basic dXNlcjpwdw=="))
			d = requireProblem(t, w, 401, "unauthenticated")
			if d != "Use Authorization: Bearer <API key>." {
				t.Fatalf("Basic: detail = %q", d)
			}

			w, _ = mcpCall(t, e, "", m.method, m.params, withHeader("Authorization", "Bearer"))
			d = requireProblem(t, w, 401, "unauthenticated")
			if d != "Use Authorization: Bearer <API key>." {
				t.Fatalf("empty Bearer: detail = %q", d)
			}
		})
	}
}

func TestMCPGetNeedsAnAPIKeyThenGives405(t *testing.T) {
	e := newTestEnv(t, mcpOn)
	o := e.newOrg("Org One", 10, true, true)
	requireProblem(t, e.do("GET", mcpPath, nil), 401, "unauthenticated")
	requireStatus(t, e.asKey(o.Owner, "GET", mcpPath, nil), 405)
}

func TestMCPInitializeWithAValidAPIKey(t *testing.T) {
	e := newTestEnv(t, mcpOn)
	o := e.newOrg("Org One", 10, true, true)
	c := e.newClient(o, "Client", true, 1)
	cu := e.newClientUser(o, c.ID, "client@example.com")
	for _, u := range []testUser{o.Owner, cu} {
		w, rpc := mcpCall(t, e, u.Key, "initialize", mcpMethods[0].params)
		requireStatus(t, w, 200)
		res, _ := rpc["result"].(map[string]any)
		info, _ := res["serverInfo"].(map[string]any)
		if info["name"] != "calyx" || info["version"] != "v1" {
			t.Fatalf("serverInfo = %v", info)
		}
		if s, _ := res["instructions"].(string); s != mcpInstructions {
			t.Fatalf("instructions = %q", s)
		}
	}
}

// With MCP off, /mcp is an unknown route.
func TestMCPDisabledIsNotFound(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	w, _ := mcpCall(t, e, o.Owner.Key, "initialize", mcpMethods[0].params)
	requireStatus(t, w, 404)
}

// An operation on the adapter that runs the API middleware, as Huma's MCP transport does, and has
// no access mark, must never count as public.
func TestAnUnmarkedOperationGives500(t *testing.T) {
	logs := &syncBuffer{}
	mux, api := newMux(Deps{Config: testConfigForRoutes(), Logger: config.NewLogger("json", logs)})
	op := &huma.Operation{Method: http.MethodGet, Path: "/test-unmarked"}
	api.Adapter().Handle(op, func(ctx huma.Context) {
		api.Middlewares().Handler(func(ctx huma.Context) { ctx.SetStatus(http.StatusOK) })(ctx)
	})
	w := send(mux, "GET", "/test-unmarked", "", "")
	requireProblem(t, w, 500, "internal")
	if !contains(logs.String(), "Internal error.") {
		t.Fatalf("no error in the log: %s", logs.String())
	}
}

// An operation at the MCP path that has an access mark keeps its normal checks. Only the
// unmarked transport gets the API-key-only path.
func TestMarkedOperationAtMCPPathKeepsItsChecks(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	cl := e.newClient(o, "Client", true, 2)
	u := e.newClientUser(o, cl.ID, "client@example.test")
	mux, api := newMux(e.Deps)
	op := withAccess(huma.Operation{Method: http.MethodPost, Path: mcpPath}, accessLoggedIn, tenancy.ManageOrganization)
	api.Adapter().Handle(&op, func(ctx huma.Context) {
		api.Middlewares().Handler(func(ctx huma.Context) { ctx.SetStatus(http.StatusOK) })(ctx)
	})
	w := send(mux, "POST", mcpPath, "application/json", "{}", withKey(u.Key))
	requireProblem(t, w, 403, "owner_only")
}

// The MCP transport is on the adapter, not in the document: the document does not change with MCP
// on. openapi:check builds the document with MCP off.
func TestMCPDoesNotChangeTheDocument(t *testing.T) {
	off, err := OpenAPIDocument()
	if err != nil {
		t.Fatal(err)
	}
	on, err := marshalDocument(newAPI(http.NewServeMux(), Deps{Config: config.Config{MCPEnabled: true}}))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(on, off) {
		t.Fatal("the document with MCP on differs from the document with MCP off")
	}
}

// toolNames has no stream: followPairing and followEvents are not tools.
var toolNames = []string{"list_instances", "get_instance", "send_message", "create_bulk_job", "get_bulk_job", "cancel_bulk_job"}

// toolCodes lists the error codes that each tool can give.
var toolCodes = map[string][]string{
	"list_instances": nil,
	"get_instance":   {"not_found"},
	"send_message": {"not_found", "instance_not_connected", "instance_not_ready", "instance_queue_full",
		"send_in_progress", "duplicate_message", "idempotency_key_conflict",
		"idempotency_key_outcome_unknown", "send_outcome_unknown"},
	"create_bulk_job": {"not_found", "instance_not_connected", "instance_not_ready",
		"idempotency_key_conflict", "bulk_outcome_unknown"},
	"get_bulk_job":    {"not_found", "core_unavailable"},
	"cancel_bulk_job": {"not_found", "core_unavailable"},
}

// listTools returns the tools of tools/list by name.
func listTools(t *testing.T, e *testEnv, key string) map[string]map[string]any {
	t.Helper()
	w, rpc := mcpCall(t, e, key, "tools/list", nil)
	requireStatus(t, w, 200)
	res, _ := rpc["result"].(map[string]any)
	list, _ := res["tools"].([]any)
	out := map[string]map[string]any{}
	for _, x := range list {
		tool := x.(map[string]any)
		out[tool["name"].(string)] = tool
	}
	return out
}

// callTool runs tools/call and returns the result.
func callTool(t *testing.T, e *testEnv, key, name string, args map[string]any) map[string]any {
	t.Helper()
	w, rpc := mcpCall(t, e, key, "tools/call", map[string]any{"name": name, "arguments": args})
	requireStatus(t, w, 200)
	res, _ := rpc["result"].(map[string]any)
	if res == nil {
		t.Fatalf("no result: %s", w.Body.String())
	}
	return res
}

// toolText is the text of the first content block of a tool result.
func toolText(t *testing.T, res map[string]any) string {
	t.Helper()
	c, _ := res["content"].([]any)
	if len(c) == 0 {
		t.Fatalf("no content: %v", res)
	}
	s, _ := c[0].(map[string]any)["text"].(string)
	return s
}

func requireToolProblem(t *testing.T, res map[string]any, status int, code string) {
	t.Helper()
	if res["isError"] != true {
		t.Fatalf("isError = %v, result %v", res["isError"], res)
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(toolText(t, res)), &p); err != nil {
		t.Fatalf("the text is not a problem: %v", err)
	}
	if p["status"] != float64(status) || p["code"] != code {
		t.Fatalf("problem = %v, want %d %s", p, status, code)
	}
}

func TestMCPToolSet(t *testing.T) {
	e := newTestEnv(t, mcpOn)
	o := e.newOrg("Org One", 10, true, true)
	c := e.newClient(o, "Client", true, 1)
	cu := e.newClientUser(o, c.ID, "client@example.com")
	for _, u := range []testUser{o.Owner, cu} {
		tools := listTools(t, e, u.Key)
		if len(tools) != len(toolNames) {
			t.Fatalf("tools = %d, want %d", len(tools), len(toolNames))
		}
		for _, n := range toolNames {
			if _, ok := tools[n]; !ok {
				t.Fatalf("missing tool %s", n)
			}
		}
	}
}

func TestMCPToolDescriptionsNameTheirCodes(t *testing.T) {
	e := newTestEnv(t, mcpOn)
	o := e.newOrg("Org One", 10, true, true)
	tools := listTools(t, e, o.Owner.Key)
	all := map[string]bool{}
	for _, cs := range toolCodes {
		for _, code := range cs {
			all[code] = true
		}
	}
	// No tool gives conflict: cancel_bulk_job returns a finished job unchanged.
	all["conflict"] = true
	for _, n := range toolNames {
		d, _ := tools[n]["description"].(string)
		if d == "" {
			t.Fatalf("%s has no description", n)
		}
		want := map[string]bool{}
		for _, code := range toolCodes[n] {
			want[code] = true
		}
		for code := range all {
			has := regexp.MustCompile(`\b` + code + `\b`).MatchString(d)
			if has != want[code] {
				t.Errorf("%s: code %s present = %v, want %v", n, code, has, want[code])
			}
		}
	}
}

func TestMCPKeyArgument(t *testing.T) {
	e := newTestEnv(t, mcpOn)
	o := e.newOrg("Org One", 10, true, true)
	tools := listTools(t, e, o.Owner.Key)
	for _, n := range []string{"send_message", "create_bulk_job"} {
		schema, _ := tools[n]["inputSchema"].(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		arg, _ := props["Idempotency-Key"].(map[string]any)
		if arg == nil {
			t.Fatalf("%s has no Idempotency-Key argument: %v", n, props)
		}
		if d, _ := arg["description"].(string); !strings.HasPrefix(d, "1 to 255 printable ASCII characters. Make a new key") ||
			!strings.Contains(d, "All callers of one instance share its keys.") {
			t.Fatalf("%s: description = %q", n, d)
		}
	}
}

func TestMCPPathTexts(t *testing.T) {
	e := newTestEnv(t, mcpOn)
	o := e.newOrg("Org One", 10, true, true)
	n := 0
	for name, tool := range listTools(t, e, o.Owner.Key) {
		schema, _ := tool["inputSchema"].(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		arg, _ := props["id"].(map[string]any)
		if arg == nil {
			continue
		}
		n++
		d, _ := arg["description"].(string)
		if d != pathTexts["instances"] && d != pathTexts["bulk-jobs"] {
			t.Errorf("%s: id description = %q", name, d)
		}
	}
	if n != 5 {
		t.Errorf("%d tools have an id argument, want 5", n)
	}
}

func TestMCPToolsDoNotReachAnotherClient(t *testing.T) {
	e := newTestEnv(t, mcpOn)
	o := webOrg(e, "Org One")
	c1 := e.newClient(o, "One", true, 2)
	c2 := e.newClient(o, "Two", true, 2)
	u1 := e.newClientUser(o, c1.ID, "one@example.com")
	u2 := e.newClientUser(o, c2.ID, "two@example.com")
	id := e.createMeta(u1, "Front")
	args := map[string]any{"id": id.String()}
	requireToolProblem(t, callTool(t, e, u2.Key, "get_instance", args), 404, "not_found")
	args = map[string]any{"id": id.String(), "to": "34600111222", "text": "hi"}
	requireToolProblem(t, callTool(t, e, u2.Key, "send_message", args), 404, "not_found")
	if res := callTool(t, e, u1.Key, "get_instance", map[string]any{"id": id.String()}); res["isError"] == true {
		t.Fatalf("the owner client got an error: %v", res)
	}
}

func TestMCPSendMessageKeyAndReplay(t *testing.T) {
	e := newTestEnv(t, mcpOn)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	// Huma flattens the fields of an object body into top-level arguments.
	args := map[string]any{"id": id.String(), "Idempotency-Key": "key-1", "to": "34600111222", "text": "hi"}
	var first map[string]any
	if err := json.Unmarshal([]byte(toolText(t, callTool(t, e, o.Owner.Key, "send_message", args))), &first); err != nil {
		t.Fatal(err)
	}
	if first["replayed"] != false {
		t.Fatalf("first = %v", first)
	}
	res := callTool(t, e, o.Owner.Key, "send_message", args)
	var second map[string]any
	if err := json.Unmarshal([]byte(toolText(t, res)), &second); err != nil {
		t.Fatal(err)
	}
	if second["replayed"] != true {
		t.Fatalf("second = %v", second)
	}
	for _, r := range e.Fake.SendRequests() {
		if r.IdempotencyKey == nil || *r.IdempotencyKey != "key-1" {
			t.Fatalf("the core got the key %v", r.IdempotencyKey)
		}
	}
}

// The three bulk tools: create, read and cancel one job.
func TestMCPBulkJobTools(t *testing.T) {
	e := newTestEnv(t, mcpOn)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	jobOf := func(name string, args map[string]any) map[string]any {
		t.Helper()
		res := callTool(t, e, o.Owner.Key, name, args)
		if res["isError"] == true {
			t.Fatalf("%s gave a tool error", name)
		}
		var job map[string]any
		if err := json.Unmarshal([]byte(toolText(t, res)), &job); err != nil {
			t.Fatalf("%s: the text is not a job: %v", name, err)
		}
		return job
	}
	created := jobOf("create_bulk_job", map[string]any{"id": id.String(), "recipients": recipients(2), "text": "x"})
	job, _ := created["id"].(string)
	if _, err := uuid.Parse(job); err != nil || created["status"] != "QUEUED" || created["total_messages"] != float64(2) {
		t.Fatalf("created job = %v", created)
	}
	e.Fake.PutBulkJob(&corev1.BulkJob{Id: job, InstanceName: tenancy.CoreName(id), Status: corev1.BulkJobStatus_BULK_JOB_STATUS_RUNNING, TotalMessages: 2})
	got := jobOf("get_bulk_job", map[string]any{"id": job})
	if got["id"] != job || got["status"] != "RUNNING" {
		t.Fatalf("read job = %v", got)
	}
	cancelled := jobOf("cancel_bulk_job", map[string]any{"id": job})
	if cancelled["id"] != job || cancelled["status"] != "CANCELLED" {
		t.Fatalf("cancelled job = %v", cancelled)
	}
}

func TestMCPSendMessageDuplicate(t *testing.T) {
	e := newTestEnv(t, mcpOn)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	e.Fake.SetError(testutil.MethodSendPlainText, testutil.ReasonError(codes.AlreadyExists,
		corev1.ErrorReason_ERROR_REASON_DUPLICATE_MESSAGE, nil, "Sent before."))
	args := map[string]any{"id": id.String(), "to": "34600111222", "text": "hi"}
	requireToolProblem(t, callTool(t, e, o.Owner.Key, "send_message", args), 409, "duplicate_message")
}

// withAccess must keep a mark that is already on the operation, and accept nil metadata.
func TestWithAccessKeepsTheToolMark(t *testing.T) {
	op := asTool(huma.Operation{OperationID: "x"}, "x_tool", "text")
	if len(op.Metadata) == 0 {
		t.Fatal("asTool wrote no mark")
	}
	marked := withAccess(op, accessPublic, "")
	if marked.Metadata[metaAccess] != accessPublic {
		t.Fatalf("metadata = %v", marked.Metadata)
	}
	if len(marked.Metadata) != len(op.Metadata)+1 {
		t.Fatalf("the tool mark is lost: %v", marked.Metadata)
	}
	if _, ok := op.Metadata[metaAccess]; ok {
		t.Fatal("withAccess changed the metadata of the original operation")
	}
	_ = withAccess(huma.Operation{}, accessPublic, "")
}

func TestMCPSendMessageGivesNoFixedWait(t *testing.T) {
	e := newTestEnv(t, mcpOn)
	o := e.newOrg("Org One", 10, true, true)
	d, _ := listTools(t, e, o.Owner.Key)["send_message"]["description"].(string)
	if strings.Contains(d, "few seconds") || strings.Contains(d, "Retry later") {
		t.Errorf("description has a fixed wait: %s", d)
	}
	if !strings.Contains(d, "the seconds that the error text gives") {
		t.Errorf("description does not name the wait of the error text: %s", d)
	}
}
