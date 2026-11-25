package main

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	id1 = "00000000-0000-4000-8000-000000000001"
	id2 = "00000000-0000-4000-8000-000000000002"
	id3 = "00000000-0000-4000-8000-000000000003"
	id4 = "00000000-0000-4000-8000-000000000004"
	id5 = "00000000-0000-4000-8000-000000000005"
	id6 = "00000000-0000-4000-8000-000000000006"
	id7 = "00000000-0000-4000-8000-000000000007"
	id8 = "00000000-0000-4000-8000-000000000008"
)

// client returns the 302 answer itself, so scenario 8 can be checked.
var client = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func start(t *testing.T) *httptest.Server {
	t.Helper()
	s := newServer()
	s.hold = 100 * time.Millisecond
	ts := httptest.NewServer(s.handler())
	t.Cleanup(ts.Close)
	return ts
}

// answer is what the tests need from a response whose body is already read.
type answer struct {
	StatusCode int
	Header     http.Header
}

func do(t *testing.T, ts *httptest.Server, method, path, body string, headers map[string]string) (answer, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-key")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return answer{resp.StatusCode, resp.Header}, data
}

func decode(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("bad JSON %q: %v", data, err)
	}
	return m
}

func send(t *testing.T, ts *httptest.Server, id, key string) (answer, []byte) {
	t.Helper()
	return do(t, ts, "POST", "/api/v1/instances/"+id+"/messages", `{"to":"x","text":"hi"}`,
		map[string]string{"Idempotency-Key": key, "Content-Type": "application/json"})
}

// sevenRoutes calls the 7 routes with one ID.
func sevenRoutes(id string) [][2]string {
	return [][2]string{
		{"POST", "/api/v1/instances/" + id + "/messages"},
		{"GET", "/api/v1/instances/" + id},
		{"GET", "/api/v1/instances/" + id + "/bulk-jobs"},
		{"POST", "/api/v1/instances/" + id + "/bulk-jobs"},
		{"GET", "/api/v1/bulk-jobs/" + id},
		{"POST", "/api/v1/bulk-jobs/" + id + "/cancel"},
	}
}

func TestScenario1AllRoutes(t *testing.T) {
	ts := start(t)
	resp, data := send(t, ts, id1, "k1")
	m := decode(t, data)
	if resp.StatusCode != http.StatusOK || m["message_id"] != sendMessageID || m["replayed"] != false {
		t.Fatalf("send: %d %s", resp.StatusCode, data)
	}
	resp, data = do(t, ts, "GET", "/api/v1/instances", "", nil)
	list := decode(t, data)
	items, _ := list["items"].([]any)
	if resp.StatusCode != http.StatusOK || len(items) != 1 {
		t.Fatalf("list: %d %s", resp.StatusCode, data)
	}
	inst := items[0].(map[string]any)
	for _, k := range []string{"id", "client_id", "display_name", "provider", "send_only", "enabled", "state",
		"status", "status_changed_at", "status_stale", "device_identity", "locale", "phone_number_id", "webhooks", "alert"} {
		if v, ok := inst[k]; !ok || v == nil {
			t.Errorf("instance field %s is missing or null", k)
		}
	}
	resp, data = do(t, ts, "GET", "/api/v1/instances/"+id1, "", nil)
	if resp.StatusCode != http.StatusOK || decode(t, data)["id"] != inst["id"] {
		t.Fatalf("get instance: %d %s", resp.StatusCode, data)
	}
	for _, r := range sevenRoutes(id1)[2:] {
		resp, data = do(t, ts, r[0], r[1], "{}", nil)
		m = decode(t, data)
		want := 200
		if r[0] == "POST" && strings.HasSuffix(r[1], "/bulk-jobs") {
			want = 201
		}
		if resp.StatusCode != want {
			t.Fatalf("%s %s: %d %s", r[0], r[1], resp.StatusCode, data)
		}
		job := m
		if arr, ok := m["items"].([]any); ok {
			job = arr[0].(map[string]any)
		}
		for _, k := range []string{"id", "instance_id", "status", "total_messages", "sent_messages",
			"failed_messages", "progress_percent", "estimated_finish_at", "created_at", "replayed"} {
			if v, ok := job[k]; !ok || v == nil {
				t.Errorf("%s %s: job field %s is missing or null", r[0], r[1], k)
			}
		}
	}
}

func TestScenario2SendInProgress(t *testing.T) {
	ts := start(t)
	for i := 1; i <= 2; i++ {
		resp, data := send(t, ts, id2, "same")
		m := decode(t, data)
		if resp.StatusCode != http.StatusConflict || m["code"] != "send_in_progress" {
			t.Fatalf("request %d: %d %s", i, resp.StatusCode, data)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
			t.Errorf("content type %q", ct)
		}
		if ra := resp.Header.Get("Retry-After"); ra != "12" {
			t.Errorf("request %d: Retry-After %q, want 12", i, ra)
		}
	}
	resp, data := send(t, ts, id2, "same")
	m := decode(t, data)
	if resp.StatusCode != http.StatusOK || m["replayed"] != true {
		t.Fatalf("third: %d %s", resp.StatusCode, data)
	}
	resp, _ = send(t, ts, id2, "other")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("a new key starts again: %d", resp.StatusCode)
	}
}

func TestScenario3OutcomeUnknown(t *testing.T) {
	resp, data := send(t, start(t), id3, "k")
	if resp.StatusCode != http.StatusGatewayTimeout || decode(t, data)["code"] != "send_outcome_unknown" {
		t.Fatalf("%d %s", resp.StatusCode, data)
	}
}

func TestScenario4HTML(t *testing.T) {
	ts := start(t)
	for _, r := range sevenRoutes(id4) {
		resp, data := do(t, ts, r[0], r[1], "{}", nil)
		if resp.StatusCode != http.StatusBadGateway || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") ||
			!strings.Contains(string(data), "<html>") {
			t.Fatalf("%s %s: %d %q", r[0], r[1], resp.StatusCode, data)
		}
	}
	resp, _ := do(t, ts, "GET", "/api/v1/instances", "", map[string]string{"X-Fake-Scenario": "4"})
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("list with header: %d", resp.StatusCode)
	}
}

func TestScenario5NoAnswerThenClose(t *testing.T) {
	ts := start(t)
	for _, r := range sevenRoutes(id5) {
		begin := time.Now()
		req, _ := http.NewRequest(r[0], ts.URL+r[1], strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer test-key")
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			t.Fatalf("%s %s: got an answer %d", r[0], r[1], resp.StatusCode)
		}
		if time.Since(begin) < 90*time.Millisecond {
			t.Errorf("%s %s: closed after %v, before the hold time", r[0], r[1], time.Since(begin))
		}
	}
}

func TestScenario6Nulls(t *testing.T) {
	ts := start(t)
	_, data := do(t, ts, "GET", "/api/v1/instances/"+id6, "", nil)
	inst := decode(t, data)
	for _, k := range []string{"status", "status_changed_at", "device_identity", "locale", "phone_number_id", "alert"} {
		if v, ok := inst[k]; !ok || v != nil {
			t.Errorf("instance %s = %v, want null", k, v)
		}
	}
	if w, ok := inst["webhooks"].([]any); !ok || len(w) != 0 {
		t.Errorf("webhooks = %v, want []", inst["webhooks"])
	}
	_, data = do(t, ts, "GET", "/api/v1/bulk-jobs/"+id6, "", nil)
	if v, ok := decode(t, data)["estimated_finish_at"]; !ok || v != nil {
		t.Errorf("estimated_finish_at = %v, want null", v)
	}
	_, data = do(t, ts, "GET", "/api/v1/instances/"+id6+"/bulk-jobs", "", nil)
	items := decode(t, data)["items"].([]any)
	if items[0].(map[string]any)["estimated_finish_at"] != nil {
		t.Errorf("list job is not null: %s", data)
	}
}

func TestDefaultHoldIs20Seconds(t *testing.T) {
	if got := newServer().hold; got != 20*time.Second {
		t.Errorf("hold = %v, want 20s", got)
	}
}

func TestScenario7Utf8(t *testing.T) {
	ts := start(t)
	body := `{"to":"34600000000","text":"café ñ 😀"}`
	resp, data := do(t, ts, "POST", "/api/v1/instances/"+id7+"/messages", body,
		map[string]string{"Idempotency-Key": "utf8-key"})
	if resp.StatusCode != http.StatusOK || decode(t, data)["replayed"] != false {
		t.Fatalf("%d %s", resp.StatusCode, data)
	}
	_, data = do(t, ts, "GET", "/_fake/last", "", nil)
	last := decode(t, data)
	if last["method"] != "POST" || last["path"] != "/api/v1/instances/"+id7+"/messages" {
		t.Errorf("last = %s", data)
	}
	if last["bodyHex"] != hex.EncodeToString([]byte(body)) {
		t.Errorf("bodyHex = %v", last["bodyHex"])
	}
	h := last["headers"].(map[string]any)
	if h["Idempotency-Key"] != "utf8-key" || h["Authorization"] != "Bearer test-key" {
		t.Errorf("headers = %v", h)
	}
}

func TestLastIsNotOverwrittenByItself(t *testing.T) {
	ts := start(t)
	do(t, ts, "GET", "/api/v1/instances/"+id1, "", nil)
	do(t, ts, "GET", "/_fake/last", "", nil)
	_, data := do(t, ts, "GET", "/_fake/last", "", nil)
	if decode(t, data)["path"] != "/api/v1/instances/"+id1 {
		t.Fatalf("last = %s", data)
	}
}

func TestScenario8Redirect(t *testing.T) {
	ts := start(t)
	for _, r := range sevenRoutes(id8) {
		resp, _ := do(t, ts, r[0], r[1], "{}", nil)
		if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "https://example.invalid/" {
			t.Fatalf("%s %s: %d %v", r[0], r[1], resp.StatusCode, resp.Header)
		}
	}
}

func TestUnauthenticated(t *testing.T) {
	ts := start(t)
	for _, auth := range []string{"", "Bearer wrong", "bearer test-key", "test-key"} {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/instances/"+id1, nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		m := decode(t, data)
		if resp.StatusCode != http.StatusUnauthorized || m["code"] != "unauthenticated" || m["status"] != float64(401) ||
			resp.Header.Get("Content-Type") != "application/problem+json" {
			t.Errorf("auth %q: %d %s", auth, resp.StatusCode, data)
		}
	}
}

func TestScenarioHeaderOnList(t *testing.T) {
	ts := start(t)
	resp, _ := do(t, ts, "GET", "/api/v1/instances", "", map[string]string{"X-Fake-Scenario": "8"})
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("scenario 8: %d", resp.StatusCode)
	}
	resp, data := do(t, ts, "GET", "/api/v1/instances", "", map[string]string{"X-Fake-Scenario": "6"})
	items := decode(t, data)["items"].([]any)
	if resp.StatusCode != http.StatusOK || items[0].(map[string]any)["locale"] != nil {
		t.Fatalf("scenario 6: %d %s", resp.StatusCode, data)
	}
}

func TestUnknownRoute(t *testing.T) {
	resp, _ := do(t, start(t), "GET", "/nope", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("%d", resp.StatusCode)
	}
}
