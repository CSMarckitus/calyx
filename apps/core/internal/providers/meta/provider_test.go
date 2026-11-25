package meta_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/engine"
	"calyx/core/internal/events"
	"calyx/core/internal/providers/meta"
	"calyx/core/internal/testutil"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

const credentials = `{"phoneNumberId":"123","accessToken":"good-token","appSecret":"app-secret"}`

type collected struct {
	mu       sync.Mutex
	statuses []corev1.InstanceStatus
	events   []events.Event
}

func (c *collected) hooks() engine.ProviderHooks {
	return engine.ProviderHooks{
		OnStatus: func(s corev1.InstanceStatus) { c.mu.Lock(); c.statuses = append(c.statuses, s); c.mu.Unlock() },
		OnEvent: func(t string, p any) {
			c.mu.Lock()
			c.events = append(c.events, events.Event{Type: t, Payload: p})
			c.mu.Unlock()
		},
	}
}

func newProvider(t *testing.T, graph *testutil.FakeGraphAPI) (*meta.Provider, *collected) {
	t.Helper()
	c := &collected{}
	p, err := meta.New(engine.InstanceRow{Name: "shop", Provider: "META_CLOUD", ProviderConfig: credentials}, c.hooks(),
		meta.Options{GraphURL: graph.URL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Destroy(context.Background()) })
	return p, c
}

var bg = context.Background()

func TestConnectValidatesTheCredentials(t *testing.T) {
	graph := testutil.NewFakeGraphAPI(t)
	p, _ := newProvider(t, graph)
	if err := p.Connect(bg); err != nil {
		t.Fatal(err)
	}
	if p.Status() != corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED {
		t.Fatalf("status %v", p.Status())
	}
	requests := graph.Requests()
	if len(requests) != 1 || requests[0].Method != http.MethodGet || requests[0].Path != "/v23.0/123" ||
		requests[0].Authorization != "Bearer good-token" {
		t.Fatalf("requests %+v", requests)
	}
}

func TestConnectWithABadTokenNeedsAuth(t *testing.T) {
	graph := testutil.NewFakeGraphAPI(t)
	graph.SetValidToken("other")
	p, _ := newProvider(t, graph)
	if err := p.Connect(bg); calyxerr.CodeOf(err) != codes.FailedPrecondition {
		t.Fatalf("got %v", err)
	}
	if p.Status() != corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH {
		t.Fatalf("status %v", p.Status())
	}
}

func TestConnectWhenMetaIsUnreachableIsUnavailable(t *testing.T) {
	graph := testutil.NewFakeGraphAPI(t)
	graph.Close()
	p, _ := newProvider(t, graph)
	if err := p.Connect(bg); calyxerr.CodeOf(err) != codes.Unavailable {
		t.Fatalf("got %v", err)
	}
	if p.Status() != corev1.InstanceStatus_INSTANCE_STATUS_DISCONNECTED {
		t.Fatalf("status %v", p.Status())
	}
}

func TestSendTextPostsTheTextPayloadAndReturnsTheWamid(t *testing.T) {
	graph := testutil.NewFakeGraphAPI(t)
	p, _ := newProvider(t, graph)
	id, err := p.SendText(bg, "34600111222", "hi")
	if err != nil || id != "wamid.1" {
		t.Fatalf("got %q, %v", id, err)
	}
	want := map[string]any{
		"messaging_product": "whatsapp", "recipient_type": "individual", "to": "34600111222",
		"type": "text", "text": map[string]any{"body": "hi"},
	}
	last := graph.Requests()[0]
	if last.Path != "/v23.0/123/messages" || !reflect.DeepEqual(last.Body, want) {
		t.Fatalf("got %+v", last)
	}
}

func TestARefusedMessageIsAPlainErrorWithMetasMessage(t *testing.T) {
	graph := testutil.NewFakeGraphAPI(t)
	p, _ := newProvider(t, graph)
	graph.NextSendResponse(400, map[string]any{"error": map[string]any{"message": "Re-engagement message", "code": 131047}})
	_, err := p.SendText(bg, "34600111222", "hi")
	if err == nil || calyxerr.CodeOf(err) != codes.Unknown || !strings.Contains(err.Error(), "Re-engagement message") {
		t.Fatalf("got %v", err)
	}
}

func TestAnExpiredTokenWhileSendingNeedsAuth(t *testing.T) {
	graph := testutil.NewFakeGraphAPI(t)
	p, _ := newProvider(t, graph)
	_ = p.Connect(bg)
	graph.SetValidToken("other")
	if _, err := p.SendText(bg, "34600111222", "hi"); calyxerr.CodeOf(err) != codes.FailedPrecondition {
		t.Fatalf("got %v", err)
	}
	if p.Status() != corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH {
		t.Fatalf("status %v", p.Status())
	}
}

// A send is UNAVAILABLE only when it never left the core. Any other failure is an unknown outcome.
func TestA500OnASendIsAnUnknownOutcome(t *testing.T) {
	graph := testutil.NewFakeGraphAPI(t)
	p, _ := newProvider(t, graph)
	graph.NextSendResponse(503, map[string]any{"error": map[string]any{"message": "busy"}})
	_, err := p.SendText(bg, "34600111222", "hi")
	if err == nil || calyxerr.CodeOf(err) == codes.Unavailable || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("got %v", err)
	}
}

func TestA400IsAnUnknownOutcome(t *testing.T) {
	graph := testutil.NewFakeGraphAPI(t)
	p, _ := newProvider(t, graph)
	graph.NextSendResponse(400, map[string]any{"error": map[string]any{"message": "bad", "code": 100}})
	if _, err := p.SendText(bg, "34600111222", "hi"); err == nil || calyxerr.CodeOf(err) == codes.Unavailable {
		t.Fatalf("got %v", err)
	}
}

func TestA401OnASendStillNeedsAuth(t *testing.T) {
	graph := testutil.NewFakeGraphAPI(t)
	p, _ := newProvider(t, graph)
	graph.SetValidToken("other")
	if _, err := p.SendText(bg, "34600111222", "hi"); calyxerr.CodeOf(err) != codes.FailedPrecondition {
		t.Fatalf("got %v", err)
	}
	if p.Status() != corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH {
		t.Fatalf("status %v", p.Status())
	}
}

func TestASendThatCannotDialIsUnavailable(t *testing.T) {
	graph := testutil.NewFakeGraphAPI(t)
	graph.Close()
	p, _ := newProvider(t, graph)
	_, err := p.SendText(bg, "34600111222", "hi")
	if calyxerr.CodeOf(err) != codes.Unavailable {
		t.Fatalf("got %v", err)
	}
}

func TestA2xxThatDoesNotDecodeIsAnUnknownOutcome(t *testing.T) {
	graph := testutil.NewFakeGraphAPI(t)
	p, _ := newProvider(t, graph)
	graph.NextSendResponse(200, "not an object")
	if _, err := p.SendText(bg, "34600111222", "hi"); err == nil || calyxerr.CodeOf(err) == codes.Unavailable {
		t.Fatalf("got %v", err)
	}
}

func TestASendTimeoutIsAnUnknownOutcome(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) }) // runs first, so Close never waits for the handler
	c := &collected{}
	p, err := meta.New(engine.InstanceRow{Name: "shop", Provider: "META_CLOUD", ProviderConfig: credentials}, c.hooks(),
		meta.Options{GraphURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Destroy(bg) })
	ctx, cancel := context.WithTimeout(bg, 50*time.Millisecond)
	defer cancel()
	if _, err := p.SendText(ctx, "34600111222", "hi"); err == nil || calyxerr.CodeOf(err) == codes.Unavailable {
		t.Fatalf("got %v", err)
	}
}

func TestErrorsNeverContainTheAccessToken(t *testing.T) {
	graph := testutil.NewFakeGraphAPI(t)
	graph.SetValidToken("other")
	p, _ := newProvider(t, graph)
	errs := []error{p.Connect(bg)}
	_, err := p.SendText(bg, "34600111222", "hi")
	errs = append(errs, err)
	graph.Close()
	_, err = p.SendText(bg, "34600111222", "hi")
	errs = append(errs, err)
	for _, err := range errs {
		if err == nil || strings.Contains(err.Error(), "good-token") || strings.Contains(err.Error(), "app-secret") {
			t.Fatalf("got %v", err)
		}
	}
}

func TestNormalizeRecipientKeepsOnlyDigitsAndRejectsGroups(t *testing.T) {
	p, _ := newProvider(t, testutil.NewFakeGraphAPI(t))
	for _, in := range []string{"+34 600 111 222", "34600111222@s.whatsapp.net", "(34) 600-111-222"} {
		if got, err := p.NormalizeRecipient(in); err != nil || got != "34600111222" {
			t.Errorf("%q: got %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"1203@g.us", "123@lid", "abc"} {
		if _, err := p.NormalizeRecipient(in); calyxerr.CodeOf(err) != codes.InvalidArgument {
			t.Errorf("%q: got %v", in, err)
		}
	}
}

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestIsValidSignature(t *testing.T) {
	p, _ := newProvider(t, testutil.NewFakeGraphAPI(t))
	body := []byte(`{"object":"whatsapp_business_account"}`)
	if !p.IsValidSignature(body, sign("app-secret", body)) {
		t.Fatal("a good signature was refused")
	}
	for _, header := range []string{"", sign("other", body), strings.TrimPrefix(sign("app-secret", body), "sha256="), "sha256=zz"} {
		if p.IsValidSignature(body, header) {
			t.Errorf("accepted %q", header)
		}
	}
}

const webhookValue = `{
	"messaging_product": "whatsapp",
	"metadata": {"phone_number_id": "123"},
	"contacts": [{"wa_id": "34600111222", "profile": {"name": "Ana"}}],
	"messages": [{"id": "wamid.A", "from": "34600111222", "timestamp": "1700000000", "type": "text", "text": {"body": "hi"}}],
	"statuses": [{"id": "wamid.B", "recipient_id": "34600111222", "status": "delivered"}, {"id": "wamid.C", "recipient_id": "1", "status": "weird"}]
}`

func TestHandleWebhookValueEmitsRawReceivedAndStatus(t *testing.T) {
	p, c := newProvider(t, testutil.NewFakeGraphAPI(t))
	p.HandleWebhookValue(json.RawMessage(webhookValue))

	if len(c.events) != 3 {
		t.Fatalf("got %+v", c.events)
	}
	if c.events[0].Type != "meta.webhook" {
		t.Fatalf("first event %s", c.events[0].Type)
	}
	received := c.events[1].Payload.(events.MessageReceived)
	want := events.MessageReceived{
		MessageID: "wamid.A", ChatID: "34600111222", From: "34600111222",
		PushName: "Ana", Timestamp: 1700000000, Type: "text", Text: "hi",
	}
	if received != want {
		t.Fatalf("got %+v", received)
	}
	status := c.events[2].Payload.(events.MessageStatus)
	if status != (events.MessageStatus{MessageID: "wamid.B", ChatID: "34600111222", Status: "delivered"}) {
		t.Fatalf("got %+v", status)
	}
}
