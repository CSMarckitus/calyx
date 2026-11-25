package events_test

import (
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"calyx/core/internal/events"
)

func TestPublishReachesEverySubscriberUntilItUnsubscribes(t *testing.T) {
	bus := events.NewBus()
	var a, b atomic.Int32
	unsubscribeA := bus.Subscribe(func(e events.Event) { a.Add(1) })
	bus.Subscribe(func(e events.Event) {
		b.Add(1)
		if e.InstanceName != "shop" || e.Type != "connection.update" || e.ID == "" || e.OccurredAt.IsZero() {
			t.Errorf("got %+v", e)
		}
	})

	bus.Publish("shop", "connection.update", events.ConnectionUpdate{Status: "CONNECTED"})
	unsubscribeA()
	bus.Publish("shop", "connection.update", events.ConnectionUpdate{Status: "CONNECTED"})

	if a.Load() != 1 || b.Load() != 2 {
		t.Fatalf("a %d, b %d", a.Load(), b.Load())
	}
}

func TestSubscribingFromInsideAListenerDoesNotDeadlock(t *testing.T) {
	bus := events.NewBus()
	bus.Subscribe(func(e events.Event) { bus.Subscribe(func(events.Event) {}) })
	bus.Publish("shop", "bulk.finished", nil)
}

func TestSupportedEventTypesDependOnTheProvider(t *testing.T) {
	web := events.SupportedEventTypes("WHATSAPP_WEB")
	meta := events.SupportedEventTypes("META_CLOUD")
	for _, common := range events.CalyxEventTypes {
		if !slices.Contains(web, common) || !slices.Contains(meta, common) {
			t.Errorf("%s missing", common)
		}
	}
	if !slices.Contains(web, "whatsmeow.Message") || slices.Contains(web, "meta.webhook") {
		t.Errorf("WhatsApp Web: %v", web)
	}
	if !slices.Contains(meta, "meta.webhook") || slices.Contains(meta, "whatsmeow.Message") {
		t.Errorf("Meta: %v", meta)
	}
}

func TestPayloadsUseCamelCaseJSONNames(t *testing.T) {
	json, err := events.SerializePayload(events.MessageReceived{MessageID: "m1", ChatID: "c", From: "f", ChatAltID: "34600111222@s.whatsapp.net"})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"messageId":"m1"`, `"chatId":"c"`, `"chatAltId":"34600111222@s.whatsapp.net"`, `"isGroup":false`} {
		if !contains(json, field) {
			t.Errorf("%s not in %s", field, json)
		}
	}
	if contains(json, "fromAltId") || contains(json, "pushName") {
		t.Errorf("empty optional fields must be left out: %s", json)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
