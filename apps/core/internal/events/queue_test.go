package events_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"calyx/core/internal/events"
	"calyx/core/internal/testutil"
)

func webhookTask(t *testing.T, webhookID string) *asynq.Task {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"webhookId": webhookID,
		"event":     events.SerializedEvent{ID: "e1", InstanceName: "shop", Type: "bulk.finished", PayloadJSON: "{}", OccurredAt: "x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return asynq.NewTask(events.WebhookTaskType, payload)
}

func TestTheWorkerReadsURLAndSecretFromTheDatabase(t *testing.T) {
	db := testutil.NewDB(t)
	instanceID := testutil.CreateInstance(t, db, "shop")
	r := newReceiver(t)
	addWebhook(t, db, instanceID, "bulk.finished", r.server.URL, "s3cret")
	handler := events.NewWebhookTaskHandler(db, &http.Client{})

	if err := handler.ProcessTask(context.Background(), webhookTask(t, "bulk.finished"+r.server.URL)); err != nil {
		t.Fatal(err)
	}
	got := r.got()
	if len(got) != 1 || got[0].headers.Get("X-Calyx-Signature") == "" {
		t.Fatalf("got %+v", got)
	}
}

func TestAMissingWebhookOrA4xxIsNotRetriedButA5xxIs(t *testing.T) {
	db := testutil.NewDB(t)
	instanceID := testutil.CreateInstance(t, db, "shop")
	refusing, failing := newReceiver(t, 404), newReceiver(t, 503)
	addWebhook(t, db, instanceID, "bulk.finished", refusing.server.URL, "")
	addWebhook(t, db, instanceID, "message.status", failing.server.URL, "")
	handler := events.NewWebhookTaskHandler(db, &http.Client{})
	ctx := context.Background()

	if err := handler.ProcessTask(ctx, webhookTask(t, "missing")); !errors.Is(err, asynq.SkipRetry) {
		t.Errorf("missing webhook: %v", err)
	}
	if err := handler.ProcessTask(ctx, webhookTask(t, "bulk.finished"+refusing.server.URL)); !errors.Is(err, asynq.SkipRetry) {
		t.Errorf("404: %v", err)
	}
	err := handler.ProcessTask(ctx, webhookTask(t, "message.status"+failing.server.URL))
	if err == nil || errors.Is(err, asynq.SkipRetry) {
		t.Errorf("503 must be retried: %v", err)
	}
}

// Runs only against a throwaway Redis: CALYX_TEST_REDIS_URL=redis://127.0.0.1:6399/0
func TestAQueuedWebhookIsDeliveredByTheWorker(t *testing.T) {
	redisURL := os.Getenv("CALYX_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("CALYX_TEST_REDIS_URL not set")
	}
	db := testutil.NewDB(t)
	bus := events.NewBus()
	instanceID := testutil.CreateInstance(t, db, "shop")
	r := newReceiver(t)
	addWebhook(t, db, instanceID, "bulk.finished", r.server.URL, "s3cret")

	enqueuer, err := events.NewAsynqEnqueuer(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	defer enqueuer.Close()
	d := events.NewDispatcher(db, bus, events.DispatcherOptions{Enqueuer: enqueuer})
	d.Start()
	defer d.Stop()

	worker, err := events.NewWebhookWorker(redisURL, db, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(); err != nil {
		t.Fatal(err)
	}
	defer worker.Shutdown()

	bus.Publish("shop", "bulk.finished", events.BulkFinished{JobID: "j1"})
	deadline := time.Now().Add(10 * time.Second)
	for len(r.got()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("not delivered through Redis")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
