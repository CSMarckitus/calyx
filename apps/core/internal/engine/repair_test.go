package engine_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/engine"
	"calyx/core/internal/events"
	"calyx/core/internal/testutil"
)

type storedHook struct {
	ID, URL, Event, Secret string
}

func (f *orchestratorFixture) storedWebhooks(t *testing.T, name string) []storedHook {
	t.Helper()
	rows, err := f.db.Query(`SELECT w.id, w.url, w.event_type, COALESCE(w.secret, '') FROM webhooks w
		JOIN instances i ON i.id = w.instance_id WHERE i.name = ? ORDER BY w.event_type`, name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var found []storedHook
	for rows.Next() {
		var w storedHook
		if err := rows.Scan(&w.ID, &w.URL, &w.Event, &w.Secret); err != nil {
			t.Fatal(err)
		}
		found = append(found, w)
	}
	return found
}

func (f *orchestratorFixture) storedSendOnly(t *testing.T, name string) bool {
	t.Helper()
	var sendOnly bool
	if err := f.db.QueryRow("SELECT is_used_only_for_send_messages FROM instances WHERE name = ?", name).Scan(&sendOnly); err != nil {
		t.Fatal(err)
	}
	return sendOnly
}

// needsAuthWithWebhooks stores the instance "shop" in NEEDS_AUTH with two webhooks. The webhook
// of message.received has the secret "s3cret".
func (f *orchestratorFixture) needsAuthWithWebhooks(t *testing.T, receiverURL string) []storedHook {
	t.Helper()
	f.instance(t, "shop", "connection_status = 'NEEDS_AUTH'")
	hooks := []engine.WebhookInput{
		{URL: receiverURL, Event: "message.received", Secret: "s3cret"},
		{URL: "https://example.test/status", Event: "connection.update"},
	}
	if err := f.o.UpdateInstanceConfig(ctx, "shop", engine.UpdateInstanceParams{Webhooks: &hooks}); err != nil {
		t.Fatal(err)
	}
	stored := f.storedWebhooks(t, "shop")
	if len(stored) != 2 {
		t.Fatalf("setup: %+v", stored)
	}
	return stored
}

// rePair is the request of a pairing that carries no configuration. It carries a locale, which a
// new pairing needs.
func rePair(name string) engine.CreateInstanceParams {
	return engine.CreateInstanceParams{Name: name, Provider: "WHATSAPP_WEB", Pairing: engine.PairingMethod{QR: true}, Locale: "es-ES"}
}

func boolPtr(b bool) *bool { return &b }

func TestNewInstanceStoresTheRequest(t *testing.T) {
	f := newOrchestratorFixture(t)
	params := rePair("shop")
	params.Webhooks = []engine.WebhookInput{{URL: "https://example.test/in", Event: "message.received", Secret: "k"}}
	if err := f.o.CreateInstance(ctx, params, func(engine.CreateInstanceUpdate) {}); err != nil {
		t.Fatal(err)
	}
	if f.storedSendOnly(t, "shop") {
		t.Error("an absent send-only flag must mean false")
	}
	got := f.storedWebhooks(t, "shop")
	if len(got) != 1 || got[0].URL != "https://example.test/in" || got[0].Secret != "k" {
		t.Errorf("webhooks %+v", got)
	}

	sendOnly := rePair("sender")
	sendOnly.SendOnly = boolPtr(true)
	if err := f.o.CreateInstance(ctx, sendOnly, func(engine.CreateInstanceUpdate) {}); err != nil {
		t.Fatal(err)
	}
	if !f.storedSendOnly(t, "sender") {
		t.Error("the send-only flag of the request was not stored")
	}
}

func TestRePairWithNoWebhooksKeepsThem(t *testing.T) {
	f := newOrchestratorFixture(t)
	var mu sync.Mutex
	var deliveries []http.Header
	var bodies []string
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		deliveries, bodies = append(deliveries, r.Header.Clone()), append(bodies, string(body))
		mu.Unlock()
	}))
	t.Cleanup(receiver.Close)
	before := f.needsAuthWithWebhooks(t, receiver.URL)

	if err := f.o.CreateInstance(ctx, rePair("shop"), func(engine.CreateInstanceUpdate) {}); err != nil {
		t.Fatal(err)
	}
	if after := f.storedWebhooks(t, "shop"); !slices.Equal(after, before) {
		t.Fatalf("webhooks changed:\nbefore %+v\nafter  %+v", before, after)
	}

	dispatcher := events.NewDispatcher(f.db, f.bus, events.DispatcherOptions{})
	dispatcher.Start()
	t.Cleanup(dispatcher.Stop)
	f.fake("shop").Emit("message.received", map[string]string{"text": "hi"})
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n := len(deliveries)
		mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no delivery")
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	headers, body := deliveries[0], bodies[0]
	mu.Unlock()
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write([]byte(headers.Get("X-Calyx-Timestamp") + "." + body))
	if want := "sha256=" + hex.EncodeToString(mac.Sum(nil)); headers.Get("X-Calyx-Signature") != want {
		t.Fatalf("signature %q, want %q", headers.Get("X-Calyx-Signature"), want)
	}
}

func TestRePairWithWebhooksReplacesThem(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.needsAuthWithWebhooks(t, "https://example.test/in")
	params := rePair("shop")
	params.Webhooks = []engine.WebhookInput{{URL: "https://example.test/new", Event: "message.status"}}
	if err := f.o.CreateInstance(ctx, params, func(engine.CreateInstanceUpdate) {}); err != nil {
		t.Fatal(err)
	}
	got := f.storedWebhooks(t, "shop")
	if len(got) != 1 || got[0].URL != "https://example.test/new" || got[0].Event != "message.status" {
		t.Fatalf("webhooks %+v", got)
	}
}

func TestRePairKeepsOrSetsTheSendOnlyFlag(t *testing.T) {
	cases := []struct {
		name            string
		stored          bool
		request         *bool
		wantAfterRePair bool
	}{
		{"absent keeps true", true, nil, true},
		{"absent keeps false", false, nil, false},
		{"present false sets false", true, boolPtr(false), false},
		{"present true sets true", false, boolPtr(true), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newOrchestratorFixture(t)
			changes := "connection_status = 'NEEDS_AUTH'"
			if c.stored {
				changes += ", is_used_only_for_send_messages = 1"
			}
			f.instance(t, "shop", changes)
			params := rePair("shop")
			params.SendOnly = c.request
			if err := f.o.CreateInstance(ctx, params, func(engine.CreateInstanceUpdate) {}); err != nil {
				t.Fatal(err)
			}
			if got := f.storedSendOnly(t, "shop"); got != c.wantAfterRePair {
				t.Errorf("send-only %v, want %v", got, c.wantAfterRePair)
			}
		})
	}
}

func TestRePairChecksTheMergedConfiguration(t *testing.T) {
	t.Run("webhooks for an instance that stays send-only", func(t *testing.T) {
		f := newOrchestratorFixture(t)
		f.instance(t, "shop", "connection_status = 'NEEDS_AUTH', is_used_only_for_send_messages = 1")
		params := rePair("shop")
		params.Webhooks = []engine.WebhookInput{{URL: "https://example.test/in", Event: "message.received"}}
		err := f.o.CreateInstance(ctx, params, func(engine.CreateInstanceUpdate) {})
		if calyxerr.CodeOf(err) != codes.InvalidArgument {
			t.Fatalf("got %v", err)
		}
		if len(f.storedWebhooks(t, "shop")) != 0 || !f.storedSendOnly(t, "shop") {
			t.Error("a refused request changed the configuration")
		}
		if f.buildCount("shop") != 0 {
			t.Error("a refused request started a pairing")
		}
	})
	t.Run("send-only for an instance that keeps its webhooks", func(t *testing.T) {
		f := newOrchestratorFixture(t)
		before := f.needsAuthWithWebhooks(t, "https://example.test/in")
		params := rePair("shop")
		params.SendOnly = boolPtr(true)
		err := f.o.CreateInstance(ctx, params, func(engine.CreateInstanceUpdate) {})
		if calyxerr.CodeOf(err) != codes.InvalidArgument {
			t.Fatalf("got %v", err)
		}
		if after := f.storedWebhooks(t, "shop"); !slices.Equal(after, before) || f.storedSendOnly(t, "shop") {
			t.Error("a refused request changed the configuration")
		}
	})
}

func TestAbandonedRePairKeepsWhatItDidNotCarry(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.configure = func(fake *testutil.FakeProvider) {
		fake.SetPair(func(ctx context.Context, method engine.PairingMethod, onCode func(string)) error {
			return calyxerr.New(codes.Canceled, "the caller closed the stream")
		})
	}
	before := f.needsAuthWithWebhooks(t, "https://example.test/in")
	params := rePair("shop")
	params.SendOnly = boolPtr(false)
	if err := f.o.CreateInstance(ctx, params, func(engine.CreateInstanceUpdate) {}); calyxerr.CodeOf(err) != codes.Canceled {
		t.Fatalf("got %v", err)
	}
	if f.storedStatus(t, "shop") != "NEEDS_AUTH" {
		t.Errorf("status %s", f.storedStatus(t, "shop"))
	}
	if after := f.storedWebhooks(t, "shop"); !slices.Equal(after, before) {
		t.Errorf("webhooks changed:\nbefore %+v\nafter  %+v", before, after)
	}
	if f.storedSendOnly(t, "shop") {
		t.Error("send-only flag changed")
	}

	// What the request carried stays written after the failure.
	params.Webhooks = []engine.WebhookInput{{URL: "https://example.test/new", Event: "message.status"}}
	if err := f.o.CreateInstance(ctx, params, func(engine.CreateInstanceUpdate) {}); calyxerr.CodeOf(err) != codes.Canceled {
		t.Fatalf("got %v", err)
	}
	if got := f.storedWebhooks(t, "shop"); len(got) != 1 || got[0].URL != "https://example.test/new" {
		t.Errorf("webhooks %+v", got)
	}
}

func TestCreateInstanceCannotClearWebhooks(t *testing.T) {
	f := newOrchestratorFixture(t)
	before := f.needsAuthWithWebhooks(t, "https://example.test/in")
	params := rePair("shop")
	params.Webhooks = []engine.WebhookInput{}
	if err := f.o.CreateInstance(ctx, params, func(engine.CreateInstanceUpdate) {}); err != nil {
		t.Fatal(err)
	}
	if after := f.storedWebhooks(t, "shop"); !slices.Equal(after, before) {
		t.Fatalf("an empty list changed the webhooks: %+v", after)
	}

	empty := []engine.WebhookInput{}
	if err := f.o.UpdateInstanceConfig(ctx, "shop", engine.UpdateInstanceParams{Webhooks: &empty}); err != nil {
		t.Fatal(err)
	}
	if got := f.storedWebhooks(t, "shop"); len(got) != 0 {
		t.Fatalf("UpdateInstanceConfig did not clear the webhooks: %+v", got)
	}
}

// An UpdateInstanceConfig during a pairing would write values that the pairing already read, so
// the core refuses every change until the pairing ends.
func TestAnUpdateDuringARePairIsRefused(t *testing.T) {
	f := newOrchestratorFixture(t)
	if err := f.o.CreateInstance(ctx, rePair("shop"), func(engine.CreateInstanceUpdate) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec("UPDATE instances SET connection_status = 'NEEDS_AUTH', is_used_only_for_send_messages = 1 WHERE name = 'shop'"); err != nil {
		t.Fatal(err)
	}
	pairing, release := make(chan struct{}), make(chan struct{})
	f.configure = func(fake *testutil.FakeProvider) {
		fake.SetPair(func(context.Context, engine.PairingMethod, func(string)) error {
			close(pairing)
			<-release
			return nil
		})
	}
	rePaired := make(chan error, 1)
	go func() { rePaired <- f.o.CreateInstance(ctx, rePair("shop"), func(engine.CreateInstanceUpdate) {}) }()
	<-pairing

	hooks := []engine.WebhookInput{{URL: "https://example.test/in", Event: "message.received"}}
	changes := map[string]engine.UpdateInstanceParams{
		"webhooks and send-only": {SendOnly: boolPtr(false), Webhooks: &hooks},
		"disable":                {IsEnabled: boolPtr(false)},
	}
	for name, change := range changes {
		err := f.o.UpdateInstanceConfig(ctx, "shop", change)
		if calyxerr.CodeOf(err) != codes.FailedPrecondition {
			t.Errorf("%s: got %v", name, err)
		}
	}
	close(release)
	if err := <-rePaired; err != nil {
		t.Fatalf("re-pair: %v", err)
	}
	if !f.storedSendOnly(t, "shop") || len(f.storedWebhooks(t, "shop")) != 0 {
		t.Error("a refused update changed the configuration")
	}

	// After the pairing ends, the same change works.
	if err := f.o.UpdateInstanceConfig(ctx, "shop", engine.UpdateInstanceParams{IsEnabled: boolPtr(false)}); err != nil {
		t.Fatalf("update after the pairing: %v", err)
	}
}

// A disabled instance stays closed: a re-pair would open it with the old values.
func TestARePairOfADisabledInstanceIsRefused(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "connection_status = 'NEEDS_AUTH', is_enabled = 0")
	err := f.o.CreateInstance(ctx, rePair("shop"), func(engine.CreateInstanceUpdate) {})
	if calyxerr.CodeOf(err) != codes.FailedPrecondition {
		t.Fatalf("got %v", err)
	}
	if f.buildCount("shop") != 0 {
		t.Error("a refused re-pair started a pairing")
	}

	// The check of an existing instance goes first.
	f.instance(t, "other", "connection_status = 'CONNECTED', is_enabled = 0")
	err = f.o.CreateInstance(ctx, rePair("other"), func(engine.CreateInstanceUpdate) {})
	if calyxerr.CodeOf(err) != codes.AlreadyExists {
		t.Fatalf("got %v", err)
	}
}

// A re-pair that the checks refuse must not close the session that is still loaded.
func TestARefusedRePairDoesNotCloseTheLiveInstance(t *testing.T) {
	f := newOrchestratorFixture(t)
	if err := f.o.CreateInstance(ctx, rePair("shop"), func(engine.CreateInstanceUpdate) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec("UPDATE instances SET connection_status = 'NEEDS_AUTH', is_used_only_for_send_messages = 1 WHERE name = 'shop'"); err != nil {
		t.Fatal(err)
	}
	params := rePair("shop")
	params.Webhooks = []engine.WebhookInput{{URL: "https://example.test/in", Event: "message.received"}}
	if err := f.o.CreateInstance(ctx, params, func(engine.CreateInstanceUpdate) {}); calyxerr.CodeOf(err) != codes.InvalidArgument {
		t.Fatalf("got %v", err)
	}
	if _, destroys, _ := f.fake("shop").Counts(); destroys != 0 {
		t.Fatal("the refused re-pair closed the live instance")
	}
}
