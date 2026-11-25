package httpapi

import (
	"context"
	"encoding/json"
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

func recipients(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "34600" + strconv.Itoa(100000+i)
	}
	return out
}

func bulkPath(id uuid.UUID) string { return "/api/v1/instances/" + id.String() + "/bulk-jobs" }

func TestCreateSameTextJob(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	w := e.asKey(o.Owner, "POST", bulkPath(id), map[string]any{"recipients": recipients(3), "text": "Reminder: tomorrow 10:00."})
	requireStatus(t, w, 201)
	m := decodeMap(t, w)
	if _, err := uuid.Parse(m["id"].(string)); err != nil {
		t.Fatalf("id = %v", m["id"])
	}
	if m["instance_id"] != id.String() || m["status"] != "QUEUED" || m["total_messages"] != float64(3) || m["sent_messages"] != float64(0) ||
		m["failed_messages"] != float64(0) || m["progress_percent"] != float64(0) || m["created_at"] == nil || len(m) != 10 {
		t.Fatalf("job = %v", m)
	}
	if _, ok := m["estimated_finish_at"]; !ok {
		t.Fatalf("no estimated_finish_at in %v", m)
	}
	reqs := e.Fake.BulkSameRequests()
	if len(reqs) != 1 || reqs[0].GetInstanceName() != tenancy.CoreName(id) || len(reqs[0].GetRecipients()) != 3 {
		t.Fatalf("core requests = %v", reqs)
	}
}

func TestCreateCustomMessagesJob(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	w := e.asKey(o.Owner, "POST", bulkPath(id), map[string]any{"messages": []map[string]any{
		{"to": "34600000001", "text": "Hi Ana", "delay_ms": 20000},
		{"to": "34600000002", "text": "Hi Luis"},
	}})
	requireStatus(t, w, 201)
	reqs := e.Fake.BulkCustomRequests()
	if len(reqs) != 1 || len(reqs[0].GetMessages()) != 2 {
		t.Fatalf("core requests = %v", reqs)
	}
	first, second := reqs[0].GetMessages()[0], reqs[0].GetMessages()[1]
	if first.DelayMs == nil || first.GetDelayMs() != 20000 || second.DelayMs != nil {
		t.Fatalf("delays = %v %v", first.DelayMs, second.DelayMs)
	}
}

func TestBulkPacingIsPassedInMilliseconds(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	w := e.asKey(o.Owner, "POST", bulkPath(id), `{"recipients":["34600000001"],"text":"x",
		"pacing":{"delay":{"min":15000,"max":45000},"pause_every":{"min":10,"max":25},"pause_duration":{"min":60000,"max":300000}}}`)
	requireStatus(t, w, 201)
	pc := e.Fake.BulkSameRequests()[0].GetPacing()
	if pc.GetDelay().GetMin() != 15000 || pc.GetDelay().GetMax() != 45000 ||
		pc.GetPauseEvery().GetMin() != 10 || pc.GetPauseEvery().GetMax() != 25 ||
		pc.GetPauseDuration().GetMin() != 60000 || pc.GetPauseDuration().GetMax() != 300000 {
		t.Fatalf("pacing = %v", pc)
	}
	// No pacing: the backend adds none.
	e.asKey(o.Owner, "POST", bulkPath(id), map[string]any{"recipients": recipients(1), "text": "x"})
	if got := e.Fake.BulkSameRequests()[1].Pacing; got != nil {
		t.Fatalf("pacing = %v, want none", got)
	}
}

func TestBulkFormRules(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	msg := []map[string]any{{"to": "34600000001", "text": "x"}}
	cases := map[string]any{
		"both forms":                  map[string]any{"recipients": recipients(1), "text": "x", "messages": msg},
		"neither":                     map[string]any{},
		"recipients without text":     map[string]any{"recipients": recipients(1)},
		"empty recipients":            map[string]any{"recipients": []string{}, "text": "x"},
		"text without recipients":     map[string]any{"text": "x"},
		"messages with a text":        map[string]any{"messages": msg, "text": "x"},
		"messages with an empty text": map[string]any{"messages": msg, "text": ""},
		"empty messages":              map[string]any{"messages": []map[string]any{}},
		"blank text":                  map[string]any{"recipients": recipients(1), "text": "  "},
		"long text":                   map[string]any{"recipients": recipients(1), "text": strings.Repeat("é", 4097)},
		"empty recipient":             map[string]any{"recipients": []string{""}, "text": "x"},
		"long recipient":              map[string]any{"recipients": []string{strings.Repeat("1", 65)}, "text": "x"},
		"message without to":          map[string]any{"messages": []map[string]any{{"text": "x"}}},
		"message without text":        map[string]any{"messages": []map[string]any{{"to": "1"}}},
		"message with a blank text":   map[string]any{"messages": []map[string]any{{"to": "1", "text": " "}}},
		"negative delay_ms":           map[string]any{"messages": []map[string]any{{"to": "1", "text": "x", "delay_ms": -1}}},
		"too large delay_ms":          map[string]any{"messages": []map[string]any{{"to": "1", "text": "x", "delay_ms": 2147483648}}},
		"negative pacing":             map[string]any{"recipients": recipients(1), "text": "x", "pacing": map[string]any{"delay": map[string]int{"min": -1, "max": 5}}},
		"too large pacing":            map[string]any{"recipients": recipients(1), "text": "x", "pacing": map[string]any{"delay": map[string]int64{"min": 1, "max": 2147483648}}},
		"pacing without max":          map[string]any{"recipients": recipients(1), "text": "x", "pacing": map[string]any{"delay": map[string]int{"min": 1}}},
		"pacing in seconds as text":   map[string]any{"recipients": recipients(1), "text": "x", "pacing": map[string]any{"delay": map[string]string{"min": "1", "max": "2"}}},
		"unknown pacing field":        map[string]any{"recipients": recipients(1), "text": "x", "pacing": map[string]any{"speed": 1}},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			requireProblem(t, e.asKey(o.Owner, "POST", bulkPath(id), body), 400, "invalid_request")
		})
	}
	if n := len(e.Fake.BulkSameRequests()) + len(e.Fake.BulkCustomRequests()); n != 0 {
		t.Fatalf("the core received %d bulk requests", n)
	}
	requireFields(t, e.asKey(o.Owner, "POST", bulkPath(id), map[string]any{}), "messages")
	requireFields(t, e.asKey(o.Owner, "POST", bulkPath(id), map[string]any{"recipients": []string{"1", strings.Repeat("1", 65)}, "text": " "}),
		"text", "recipients[1]")
	// The service reports every wrong field of the messages at once.
	requireFields(t, e.asKey(o.Owner, "POST", bulkPath(id), map[string]any{"messages": []map[string]any{
		{"to": "", "text": " ", "delay_ms": -1}, {"to": "1", "text": "x"},
	}, "pacing": map[string]any{"delay": map[string]int{"min": -1, "max": -2}}}),
		"messages[0].to", "messages[0].text", "messages[0].delay_ms", "pacing.delay.min", "pacing.delay.max")
	// A missing bound or a missing message field is a shape error at the object that misses it.
	requireFields(t, e.asKey(o.Owner, "POST", bulkPath(id), map[string]any{"recipients": recipients(1), "text": "x",
		"pacing": map[string]any{"delay": map[string]int{"min": 1}}}), "pacing.delay")
	requireFields(t, e.asKey(o.Owner, "POST", bulkPath(id), map[string]any{"messages": []map[string]any{{"text": "x"}}}), "messages[0]")
	// Anti-spam: a repeated recipient of one text, and a third message to one recipient.
	requireFields(t, e.asKey(o.Owner, "POST", bulkPath(id), map[string]any{"recipients": []string{"1", "1"}, "text": "x"}), "recipients[1]")
	requireFields(t, e.asKey(o.Owner, "POST", bulkPath(id), map[string]any{"messages": []map[string]any{
		{"to": "1", "text": "a"}, {"to": "1", "text": "b"}, {"to": "1", "text": "c"},
	}}), "messages[2].to")
	// A number outside the int32 range has the path of its field, like every other field error.
	msgs := requireFields(t, e.asKey(o.Owner, "POST", bulkPath(id), map[string]any{"messages": []map[string]any{
		{"to": "1", "text": "x"}, {"to": "1", "text": "x", "delay_ms": int64(3000000000)},
	}, "pacing": map[string]any{"delay": map[string]int64{"min": -3000000000, "max": 2147483648}}}),
		"messages[1].delay_ms", "pacing.delay.min", "pacing.delay.max")
	if msgs[0] != "must be 0 to 2147483647." {
		t.Fatalf("message = %q", msgs[0])
	}
}

// An explicit null for an optional field is an absent field, as before.
func TestAnExplicitNullIsAnAbsentBulkField(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	requireStatus(t, e.asKey(o.Owner, "POST", bulkPath(id), `{"recipients":["34600000001"],"text":"x","messages":null,"pacing":null}`), 201)
	requireStatus(t, e.asKey(o.Owner, "POST", bulkPath(id),
		`{"recipients":null,"text":null,"messages":[{"to":"34600000001","text":"x","delay_ms":null}]}`), 201)
	requireStatus(t, e.asKey(o.Owner, "POST", bulkPath(id),
		`{"recipients":["34600000001"],"text":"x","pacing":{"delay":null,"pause_every":null,"pause_duration":null}}`), 201)
	if second := e.Fake.BulkCustomRequests()[0].GetMessages()[0]; second.DelayMs != nil {
		t.Fatalf("delay_ms = %v, want none", second.DelayMs)
	}
	if pc := e.Fake.BulkSameRequests()[1].GetPacing(); pc.Delay != nil || pc.PauseEvery != nil || pc.PauseDuration != nil {
		t.Fatalf("pacing = %v, want no range", pc)
	}
}

// A malformed id cannot exist: 404, as before.
func TestAMalformedBulkIDIs404(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	for _, r := range []struct{ method, path string }{
		{"POST", "/api/v1/instances/abc/bulk-jobs"}, {"GET", "/api/v1/instances/abc/bulk-jobs"},
		{"GET", "/api/v1/bulk-jobs/abc"}, {"POST", "/api/v1/bulk-jobs/abc/cancel"},
	} {
		requireProblem(t, e.asKey(o.Owner, r.method, r.path, `{}`), 404, "not_found")
	}
}

func TestBulkAnswersKeepTheirHeaders(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	w := e.asKey(o.Owner, "POST", bulkPath(id), map[string]any{"recipients": recipients(1), "text": "x"})
	j := decodeMap(t, w)["id"].(string)
	e.Fake.PutBulkJob(&corev1.BulkJob{Id: j, InstanceName: tenancy.CoreName(id), Status: corev1.BulkJobStatus_BULK_JOB_STATUS_RUNNING, TotalMessages: 1})
	for _, c := range []struct {
		w      *httptest.ResponseRecorder
		status int
	}{
		{w, 201},
		{e.asKey(o.Owner, "GET", bulkPath(id), nil), 200},
		{e.asKey(o.Owner, "GET", "/api/v1/bulk-jobs/"+j, nil), 200},
		{e.asKey(o.Owner, "POST", "/api/v1/bulk-jobs/"+j+"/cancel", nil), 200},
	} {
		requireStatus(t, c.w, c.status)
		requireNoStore(t, c.w)
		if ct := c.w.Header().Get("Content-Type"); ct != "application/json" {
			t.Fatalf("Content-Type = %q", ct)
		}
	}
}

// Bulk creation has its own body limits: 16 MiB and 60 s. The other bulk operations keep the
// defaults of withAccess.
func TestBulkCreationHasItsOwnBodyLimits(t *testing.T) {
	_, api := newMux(Deps{Config: testConfigForRoutes()})
	jobs, cancelItem := api.OpenAPI().Paths["/api/v1/instances/{id}/bulk-jobs"], api.OpenAPI().Paths["/api/v1/bulk-jobs/{id}/cancel"]
	if jobs == nil || cancelItem == nil || jobs.Post == nil || cancelItem.Post == nil {
		t.Fatal("an operation is missing")
	}
	create, cancel := jobs.Post, cancelItem.Post
	if create.MaxBodyBytes != 16<<20 || create.BodyReadTimeout != 60*time.Second {
		t.Fatalf("create limits = %d %s", create.MaxBodyBytes, create.BodyReadTimeout)
	}
	if cancel.MaxBodyBytes != 64<<10 || cancel.BodyReadTimeout != 5*time.Second {
		t.Fatalf("cancel limits = %d %s", cancel.MaxBodyBytes, cancel.BodyReadTimeout)
	}
}

// The limited fields of bulk creation tell their limit in the description, from the service
// constant, and set no length, count, range or format.
func TestTheLimitsOfBulkJobsAreInTheDescriptions(t *testing.T) {
	_, api := newMux(Deps{Config: testConfigForRoutes()})
	doc := api.OpenAPI()
	resolve := func(s *huma.Schema) *huma.Schema {
		if s != nil && s.Ref != "" {
			return doc.Components.Schemas.SchemaFromRef(s.Ref)
		}
		return s
	}
	item := doc.Paths["/api/v1/instances/{id}/bulk-jobs"]
	if item == nil || item.Post == nil {
		t.Fatal("no operation")
	}
	body := resolve(item.Post.RequestBody.Content["application/json"].Schema)
	msg := resolve(body.Properties["messages"].Items)
	pacing := resolve(body.Properties["pacing"])
	rng := resolve(pacing.Properties["delay"])
	cases := []struct {
		name string
		s    *huma.Schema
		want string
	}{
		{"recipients", body.Properties["recipients"], "1 to 5000 recipients"},
		{"recipient", body.Properties["recipients"].Items, "At most 64 bytes"},
		{"text", body.Properties["text"], "At most 4096 characters"},
		{"messages", body.Properties["messages"], "1 to 5000 messages"},
		{"message to", msg.Properties["to"], "At most 64 bytes"},
		{"message text", msg.Properties["text"], "At most 4096 characters"},
		{"delay_ms", msg.Properties["delay_ms"], "0 to 2147483647"},
		{"range min", rng.Properties["min"], "0 to 2147483647"},
		{"range max", rng.Properties["max"], "0 to 2147483647"},
	}
	for _, c := range cases {
		s := c.s
		if s == nil || !strings.Contains(s.Description, c.want) || s.MaxLength != nil || s.MinLength != nil ||
			s.MaxItems != nil || s.MinItems != nil || s.Minimum != nil || s.Maximum != nil || s.Pattern != "" || s.Format != "" {
			t.Errorf("%s: schema = %+v", c.name, s)
		}
	}
	// The objects of pacing are not nullable in the schema, but the server takes null as absent.
	for name, p := range map[string]*huma.Schema{
		"pacing": body.Properties["pacing"], "delay": pacing.Properties["delay"],
		"pause_every": pacing.Properties["pause_every"], "pause_duration": pacing.Properties["pause_duration"],
	} {
		if !strings.Contains(p.Description, "null is the same as an absent field") {
			t.Errorf("%s: description = %q", name, p.Description)
		}
	}
	if !slices.Equal(rng.Required, []string{"min", "max"}) || !slices.Equal(msg.Required, []string{"to", "text"}) {
		t.Errorf("required: range %v, message %v", rng.Required, msg.Required)
	}
}

func TestTooManyMessages(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	requireProblem(t, e.asKey(o.Owner, "POST", bulkPath(id), map[string]any{"recipients": recipients(5001), "text": "x"}), 413, "too_many_messages")
	msgs := make([]map[string]string, 5001)
	for i := range msgs {
		msgs[i] = map[string]string{"to": "34600000001", "text": "x"}
	}
	requireProblem(t, e.asKey(o.Owner, "POST", bulkPath(id), map[string]any{"messages": msgs}), 413, "too_many_messages")
	if n := len(e.Fake.BulkSameRequests()) + len(e.Fake.BulkCustomRequests()); n != 0 {
		t.Fatalf("the core received %d bulk requests", n)
	}
}

func TestExactly5000(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	requireStatus(t, e.asKey(o.Owner, "POST", bulkPath(id), map[string]any{"recipients": recipients(5000), "text": "x"}), 201)
}

func TestABodyOverTheBulkLimitIsRefused(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	big := `{"recipients":["1"],"text":"` + strings.Repeat("a", bulkBodyLimit) + `"}`
	d := requireProblem(t, e.asKey(o.Owner, "POST", bulkPath(id), big), 400, "invalid_request")
	if !strings.Contains(d, "larger than") {
		t.Fatalf("detail = %q", d)
	}
}

// A body near the 16 MiB limit reaches the core: the core receives gRPC messages of up to 17 MiB.
func TestABulkBodyNearTheLimitReachesTheCore(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	msgs := make([]map[string]string, 5000)
	for i := range msgs {
		msgs[i] = map[string]string{"to": "34600" + strconv.Itoa(100000+i), "text": strings.Repeat("a", 3300)}
	}
	b, err := json.Marshal(map[string]any{"messages": msgs})
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 16_000_000 || len(b) > bulkBodyLimit {
		t.Fatalf("body = %d bytes", len(b))
	}
	requireStatus(t, e.asKey(o.Owner, "POST", bulkPath(id), string(b)), 201)
	if reqs := e.Fake.BulkCustomRequests(); len(reqs) != 1 || len(reqs[0].GetMessages()) != 5000 {
		t.Fatalf("the core received %d requests", len(reqs))
	}
}

func TestCorePacingRefusal(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	e.Fake.SetError(testutil.MethodSendBulkSameMessage, status.Error(codes.InvalidArgument, "delay too low"))
	d := requireProblem(t, e.asKey(o.Owner, "POST", bulkPath(id), map[string]any{"recipients": recipients(2), "text": "x"}), 400, "invalid_request")
	if d != "The core refused a recipient or a pacing value of the bulk job." {
		t.Fatalf("detail = %q", d)
	}
}

func TestBulkOutcomeUnknown(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	e.Fake.SetError(testutil.MethodSendBulkSameMessage, status.Error(codes.Unavailable, "down"))
	requireProblem(t, e.asKey(o.Owner, "POST", bulkPath(id), map[string]any{"recipients": recipients(2), "text": "x"}), 504, "bulk_outcome_unknown")
	w := e.asKey(o.Owner, "GET", bulkPath(id), nil)
	requireStatus(t, w, 200)
	if items := decodeMap(t, w)["items"].([]any); len(items) != 0 {
		t.Fatalf("items = %v", items)
	}
}

func TestBulkCoreErrors(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"not ready", status.Error(codes.FailedPrecondition, "login again"), 409, "instance_not_ready"},
		{"not found", status.Error(codes.NotFound, "none"), 404, "not_found"},
		{"token", status.Error(codes.Unauthenticated, "bad"), 502, "core_rejected_token"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newTestEnv(t)
			o := webOrg(e, "Org One")
			id := e.createMeta(o.Owner, "Front")
			e.Fake.SetError(testutil.MethodSendBulkSameMessage, c.err)
			requireProblem(t, e.asKey(o.Owner, "POST", bulkPath(id), map[string]any{"recipients": recipients(1), "text": "x"}), c.status, c.code)
		})
	}
}

func TestBulkOnAnInstanceThatIsNotActive(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id, _ := e.createWeb(o.Owner, "Front", "qr") // still creating
	requireProblem(t, e.asKey(o.Owner, "POST", bulkPath(id), map[string]any{"recipients": recipients(1), "text": "x"}), 409, "instance_not_connected")
}

func createJob(t *testing.T, e *testEnv, u testUser, instanceID uuid.UUID) string {
	t.Helper()
	w := e.asKey(u, "POST", bulkPath(instanceID), map[string]any{"recipients": recipients(1), "text": "x"})
	requireStatus(t, w, 201)
	return decodeMap(t, w)["id"].(string)
}

func TestListBulkJobs(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	j1 := createJob(t, e, o.Owner, id)
	j2 := createJob(t, e, o.Owner, id)
	e.Fake.PutBulkJob(&corev1.BulkJob{Id: j1, InstanceName: tenancy.CoreName(id), Status: corev1.BulkJobStatus_BULK_JOB_STATUS_COMPLETED, TotalMessages: 1, SentMessages: 1, ProgressPercent: 100})
	e.Fake.PutBulkJob(&corev1.BulkJob{Id: j2, InstanceName: tenancy.CoreName(id), Status: corev1.BulkJobStatus_BULK_JOB_STATUS_RUNNING, TotalMessages: 1})
	w := e.asKey(o.Owner, "GET", bulkPath(id), nil)
	requireStatus(t, w, 200)
	items := decodeMap(t, w)["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %v", items)
	}
	first, second := items[0].(map[string]any), items[1].(map[string]any)
	if first["id"] != j2 || first["status"] != "RUNNING" || second["id"] != j1 || second["status"] != "COMPLETED" || first["instance_id"] != id.String() {
		t.Fatalf("items = %v", items)
	}
}

func TestGetAndCancelBulkJob(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	j := createJob(t, e, o.Owner, id)
	e.Fake.PutBulkJob(&corev1.BulkJob{Id: j, InstanceName: tenancy.CoreName(id), Status: corev1.BulkJobStatus_BULK_JOB_STATUS_RUNNING, TotalMessages: 1})
	w := e.asKey(o.Owner, "GET", "/api/v1/bulk-jobs/"+j, nil)
	requireStatus(t, w, 200)
	if m := decodeMap(t, w); m["status"] != "RUNNING" || m["instance_id"] != id.String() {
		t.Fatalf("job = %v", m)
	}
	w = e.asKey(o.Owner, "POST", "/api/v1/bulk-jobs/"+j+"/cancel", nil)
	requireStatus(t, w, 200)
	if m := decodeMap(t, w); m["status"] != "CANCELLED" {
		t.Fatalf("job = %v", m)
	}
	// An unknown job is 404.
	requireProblem(t, e.asKey(o.Owner, "GET", "/api/v1/bulk-jobs/"+uuid.NewString(), nil), 404, "not_found")
}

func TestBulkJobOfAnotherClient(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	a1 := e.newClient(o, "A1", true, 0)
	a2 := e.newClient(o, "A2", true, 0)
	u1 := e.newClientUser(o, a1.ID, "a1@example.com")
	u2 := e.newClientUser(o, a2.ID, "a2@example.com")
	id := e.createMeta(u2, "A2 line")
	j := createJob(t, e, u2, id)
	requireProblem(t, e.asKey(u1, "GET", "/api/v1/bulk-jobs/"+j, nil), 404, "not_found")
	requireProblem(t, e.asKey(u1, "POST", "/api/v1/bulk-jobs/"+j+"/cancel", nil), 404, "not_found")
	requireProblem(t, e.asKey(u1, "GET", bulkPath(id), nil), 404, "not_found")
	requireProblem(t, e.asKey(u1, "POST", bulkPath(id), map[string]any{"recipients": recipients(1), "text": "x"}), 404, "not_found")
	if n := len(e.Fake.GetBulkRequests()) + len(e.Fake.GetBulkJobsRequests()) + len(e.Fake.CancelBulkRequests()); n != 0 {
		t.Fatalf("the core received %d job reads or cancels", n)
	}
	if n := len(e.Fake.BulkSameRequests()); n != 1 {
		t.Fatalf("bulk creates = %d, want only the first", n)
	}
	requireStatus(t, e.asKey(u2, "GET", "/api/v1/bulk-jobs/"+j, nil), 200)
}

func TestBulkJobsWhenTheCoreIsDown(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	j := createJob(t, e, o.Owner, id)
	e.Fake.SetUnavailable(true)
	requireProblem(t, e.asKey(o.Owner, "GET", "/api/v1/bulk-jobs/"+j, nil), 503, "core_unavailable")
	requireProblem(t, e.asKey(o.Owner, "POST", "/api/v1/bulk-jobs/"+j+"/cancel", nil), 503, "core_unavailable")
	requireProblem(t, e.asKey(o.Owner, "GET", bulkPath(id), nil), 503, "core_unavailable")
}

func TestBulkKeyReachesTheCoreAndAReplayIsMarked(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	body := map[string]any{"recipients": recipients(2), "text": "x"}
	w := e.asKey(o.Owner, "POST", bulkPath(id), body, idempotencyKeyHeader("bulk-1"))
	requireStatus(t, w, 201)
	first := decodeMap(t, w)
	if first["replayed"] != false {
		t.Fatalf("first answer = %v", first)
	}
	w = e.asKey(o.Owner, "POST", bulkPath(id), body, idempotencyKeyHeader("bulk-1"))
	requireStatus(t, w, 201)
	if m := decodeMap(t, w); m["replayed"] != true || m["id"] != first["id"] {
		t.Fatalf("replay answer = %v", m)
	}
	for _, r := range e.Fake.BulkSameRequests() {
		if r.GetIdempotencyKey() != "bulk-1" {
			t.Fatalf("the core got the key %q", r.GetIdempotencyKey())
		}
	}
}

func TestBulkKeyConflictOverHTTP(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	e.Fake.SetError(testutil.MethodSendBulkSameMessage,
		testutil.ReasonError(codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT, nil, "Use a new key."))
	w := e.asKey(o.Owner, "POST", bulkPath(id), map[string]any{"recipients": recipients(1), "text": "x"}, idempotencyKeyHeader("k"))
	if d := requireProblem(t, w, 409, "idempotency_key_conflict"); d != "Use a new key." {
		t.Fatalf("detail = %q", d)
	}
}

func TestBulkWrongKeyIsRefusedWithoutTheValue(t *testing.T) {
	e := newTestEnv(t)
	o := webOrg(e, "Org One")
	id := e.createMeta(o.Owner, "Front")
	w := e.asKey(o.Owner, "POST", bulkPath(id), map[string]any{"recipients": recipients(1), "text": "x"}, idempotencyKeyHeader("bad key SECRET"))
	requireProblem(t, w, 400, "invalid_request")
	if strings.Contains(w.Body.String(), "SECRET") || len(e.Fake.BulkSameRequests()) != 0 {
		t.Fatalf("body = %s", w.Body.String())
	}
}

func TestBulkContinuesAfterTheClientLeavesOnlyWithAKey(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []reqOpt
		rows int
	}{
		{"with a key", []reqOpt{idempotencyKeyHeader("k")}, 1},
		{"without a key", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			o := webOrg(e, "Org One")
			id := e.createMeta(o.Owner, "Front")
			e.Fake.SetDelay(testutil.MethodSendBulkSameMessage, 300*time.Millisecond)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				opts := append([]reqOpt{withContext(ctx)}, tc.opts...)
				done <- e.asKey(o.Owner, "POST", bulkPath(id), map[string]any{"recipients": recipients(1), "text": "x"}, opts...)
			}()
			// The fake core records the call before its delay, so the call is in flight.
			deadline := time.Now().Add(5 * time.Second)
			for len(e.Fake.BulkSameRequests()) == 0 {
				if time.Now().After(deadline) {
					t.Fatal("the core call did not start")
				}
				time.Sleep(5 * time.Millisecond)
			}
			cancel()
			<-done
			var n int
			if err := e.Deps.Pool.QueryRow(context.Background(), `SELECT count(*) FROM bulk_jobs`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != tc.rows {
				t.Fatalf("%d bulk_jobs rows, want %d", n, tc.rows)
			}
		})
	}
}
