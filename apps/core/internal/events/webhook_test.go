package events_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"calyx/core/internal/events"
	"calyx/core/internal/testutil"
)

type received struct {
	body    []byte
	headers http.Header
}

// receiver is a webhook endpoint that answers each request with the next status of statuses
// (200 once they run out) and remembers what it got.
type receiver struct {
	mu       sync.Mutex
	requests []received
	statuses []int
	server   *httptest.Server
}

func newReceiver(t *testing.T, statuses ...int) *receiver {
	r := &receiver{statuses: statuses}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.requests = append(r.requests, received{body: body, headers: req.Header.Clone()})
		status := http.StatusOK
		if len(r.statuses) > 0 {
			status, r.statuses = r.statuses[0], r.statuses[1:]
		}
		r.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(r.server.Close)
	return r
}

func (r *receiver) got() []received {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]received(nil), r.requests...)
}

func addWebhook(t *testing.T, db *sql.DB, instanceID, eventType, url, secret string) {
	t.Helper()
	var secretValue any
	if secret != "" {
		secretValue = secret
	}
	_, err := db.Exec("INSERT INTO webhooks (id, instance_id, event_type, url, secret) VALUES (?, ?, ?, ?, ?)",
		eventType+url, instanceID, eventType, url, secretValue)
	if err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func newDispatcher(t *testing.T, db *sql.DB, bus *events.Bus) *events.Dispatcher {
	d := events.NewDispatcher(db, bus, events.DispatcherOptions{
		RetryDelays: []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond},
	})
	d.Start()
	t.Cleanup(d.Stop)
	return d
}

func TestASignedRequestCanBeVerifiedWithTheSecret(t *testing.T) {
	event := events.SerializedEvent{
		ID: "e1", InstanceName: "shop", Type: "message.received",
		PayloadJSON: `{"text":"hi"}`, OccurredAt: "2026-11-17T10:00:00Z",
	}
	body, headers := events.BuildRequest(event, "s3cret", time.Unix(1700000000, 0))

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["id"] != "e1" || parsed["event"] != "message.received" || parsed["instance"] != "shop" ||
		parsed["timestamp"] != "2026-11-17T10:00:00Z" || parsed["data"].(map[string]any)["text"] != "hi" {
		t.Fatalf("body %s", body)
	}

	if headers.Get("X-Calyx-Timestamp") != "1700000000" {
		t.Fatalf("timestamp %q", headers.Get("X-Calyx-Timestamp"))
	}
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write([]byte("1700000000." + string(body)))
	if want := "sha256=" + hex.EncodeToString(mac.Sum(nil)); headers.Get("X-Calyx-Signature") != want {
		t.Fatalf("signature %q, want %q", headers.Get("X-Calyx-Signature"), want)
	}
}

func TestWithoutSecretThereIsNoSignature(t *testing.T) {
	_, headers := events.BuildRequest(events.SerializedEvent{PayloadJSON: "null"}, "", time.Now())
	if headers.Get("X-Calyx-Signature") != "" || headers.Get("X-Calyx-Timestamp") != "" {
		t.Fatalf("headers %v", headers)
	}
}

func TestOnlyTheWebhookOfTheEventIsCalled(t *testing.T) {
	db := testutil.NewDB(t)
	bus := events.NewBus()
	instanceID := testutil.CreateInstance(t, db, "shop")
	received, status := newReceiver(t), newReceiver(t)
	addWebhook(t, db, instanceID, "message.received", received.server.URL, "s3cret")
	addWebhook(t, db, instanceID, "message.status", status.server.URL, "")
	newDispatcher(t, db, bus)

	bus.Publish("shop", "message.received", events.MessageReceived{MessageID: "m1"})
	waitFor(t, "delivery", func() bool { return len(received.got()) == 1 })
	time.Sleep(50 * time.Millisecond)
	if len(status.got()) != 0 {
		t.Fatal("the message.status webhook was called")
	}
	if received.got()[0].headers.Get("X-Calyx-Signature") == "" {
		t.Fatal("not signed")
	}
}

func TestA500IsRetriedAndA400IsNot(t *testing.T) {
	db := testutil.NewDB(t)
	bus := events.NewBus()
	instanceID := testutil.CreateInstance(t, db, "shop")
	flaky := newReceiver(t, 500, 500)
	refusing := newReceiver(t, 400, 400, 400)
	addWebhook(t, db, instanceID, "message.received", flaky.server.URL, "")
	addWebhook(t, db, instanceID, "message.status", refusing.server.URL, "")
	newDispatcher(t, db, bus)

	bus.Publish("shop", "message.received", nil)
	bus.Publish("shop", "message.status", nil)
	waitFor(t, "third attempt", func() bool { return len(flaky.got()) == 3 })
	time.Sleep(100 * time.Millisecond)
	if n := len(refusing.got()); n != 1 {
		t.Fatalf("a 400 was tried %d times", n)
	}
}

func TestInvalidateMakesANewWebhookVisibleAndInactiveOnesAreSkipped(t *testing.T) {
	db := testutil.NewDB(t)
	bus := events.NewBus()
	instanceID := testutil.CreateInstance(t, db, "shop")
	r := newReceiver(t)
	d := newDispatcher(t, db, bus)

	bus.Publish("shop", "bulk.finished", nil) // caches "no webhooks"
	time.Sleep(50 * time.Millisecond)         // the cache is loaded in another goroutine
	addWebhook(t, db, instanceID, "bulk.finished", r.server.URL, "")
	time.Sleep(50 * time.Millisecond)
	bus.Publish("shop", "bulk.finished", nil)
	time.Sleep(100 * time.Millisecond)
	if len(r.got()) != 0 {
		t.Fatal("the cache was not used")
	}

	d.Invalidate("shop")
	bus.Publish("shop", "bulk.finished", nil)
	waitFor(t, "delivery after invalidate", func() bool { return len(r.got()) == 1 })

	if _, err := db.Exec("UPDATE webhooks SET is_active = 0"); err != nil {
		t.Fatal(err)
	}
	d.Invalidate("shop")
	bus.Publish("shop", "bulk.finished", nil)
	time.Sleep(100 * time.Millisecond)
	if len(r.got()) != 1 {
		t.Fatal("an inactive webhook was called")
	}
}

type fakeEnqueuer struct {
	mu    sync.Mutex
	tasks []string
}

func (f *fakeEnqueuer) Enqueue(ctx context.Context, webhookID string, event events.SerializedEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks = append(f.tasks, webhookID+" "+event.Type)
	return nil
}

func TestWithAnEnqueuerNothingIsSentDirectly(t *testing.T) {
	db := testutil.NewDB(t)
	bus := events.NewBus()
	instanceID := testutil.CreateInstance(t, db, "shop")
	r := newReceiver(t)
	addWebhook(t, db, instanceID, "bulk.finished", r.server.URL, "")
	queue := &fakeEnqueuer{}
	d := events.NewDispatcher(db, bus, events.DispatcherOptions{Enqueuer: queue})
	d.Start()
	t.Cleanup(d.Stop)

	bus.Publish("shop", "bulk.finished", nil)
	waitFor(t, "enqueue", func() bool { queue.mu.Lock(); defer queue.mu.Unlock(); return len(queue.tasks) == 1 })
	if len(r.got()) != 0 {
		t.Fatal("sent directly although a queue is configured")
	}
}

// The SDK test reads the same file. Nothing writes it: change it by hand only with a contract change.
func TestBuildRequestMatchesSharedVector(t *testing.T) {
	raw, err := os.ReadFile("../../../../packages/shared-types/testdata/webhook-signature.json")
	if err != nil {
		t.Fatal(err)
	}
	var vector struct {
		Event   events.SerializedEvent `json:"event"`
		Secret  string                 `json:"secret"`
		Now     int64                  `json:"now"`
		Body    string                 `json:"body"`
		Headers map[string]string      `json:"headers"`
	}
	if err := json.Unmarshal(raw, &vector); err != nil {
		t.Fatal(err)
	}

	body, headers := events.BuildRequest(vector.Event, vector.Secret, time.Unix(vector.Now, 0))

	if string(body) != vector.Body {
		t.Fatalf("body = %s, want %s", body, vector.Body)
	}
	if len(vector.Headers) != 3 {
		t.Fatalf("vector has %d headers, want 3", len(vector.Headers))
	}
	for name, want := range vector.Headers {
		if got := headers.Get(name); got != want {
			t.Errorf("header %s = %q, want %q", name, got, want)
		}
	}
}
