package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/hibiken/asynq"
)

const (
	WebhookTaskType = "webhook:deliver"
	webhookQueue    = "webhooks"
)

// webhookTask carries the webhook id, not its URL or secret: the worker reads them from the
// database when it delivers, so the secret never sits in Redis and a changed webhook applies to
// tasks already queued.
type webhookTask struct {
	WebhookID string          `json:"webhookId"`
	Event     SerializedEvent `json:"event"`
}

// AsynqEnqueuer is the Enqueuer used when REDIS_URL is set.
type AsynqEnqueuer struct {
	client *asynq.Client
}

func NewAsynqEnqueuer(redisURL string) (*AsynqEnqueuer, error) {
	opt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		return nil, fmt.Errorf("REDIS_URL: %w", err)
	}
	return &AsynqEnqueuer{client: asynq.NewClient(opt)}, nil
}

func (q *AsynqEnqueuer) Enqueue(ctx context.Context, webhookID string, event SerializedEvent) error {
	payload, err := json.Marshal(webhookTask{WebhookID: webhookID, Event: event})
	if err != nil {
		return err
	}
	task := asynq.NewTask(WebhookTaskType, payload,
		asynq.Queue(webhookQueue),
		asynq.MaxRetry(3),             // 4 attempts, like the direct path
		asynq.Timeout(10*time.Second), // the request itself times out after 5 s
	)
	_, err = q.client.EnqueueContext(ctx, task)
	return err
}

func (q *AsynqEnqueuer) Close() error { return q.client.Close() }

// NewWebhookTaskHandler delivers one queued webhook. An error makes asynq retry with backoff,
// except when it wraps asynq.SkipRetry: the webhook is gone or the receiver answered 4xx.
func NewWebhookTaskHandler(db *sql.DB, client *http.Client) asynq.Handler {
	return asynq.HandlerFunc(func(ctx context.Context, t *asynq.Task) error {
		var task webhookTask
		if err := json.Unmarshal(t.Payload(), &task); err != nil {
			return fmt.Errorf("%w: bad payload: %w", asynq.SkipRetry, err)
		}

		var url, secret string
		err := db.QueryRowContext(ctx, "SELECT url, COALESCE(secret, '') FROM webhooks WHERE id = ? AND is_active = 1",
			task.WebhookID).Scan(&url, &secret)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: webhook %s was removed or disabled", asynq.SkipRetry, task.WebhookID)
		}
		if err != nil {
			return err
		}

		if err := Send(ctx, client, url, secret, task.Event); err != nil {
			if !IsWorthRetrying(err) {
				return fmt.Errorf("%w: %w", asynq.SkipRetry, err)
			}
			return err
		}
		return nil
	})
}

// WebhookWorker consumes the webhook queue. It runs in its own process (cmd/worker).
type WebhookWorker struct {
	server  *asynq.Server
	handler asynq.Handler
}

func NewWebhookWorker(redisURL string, db *sql.DB, concurrency int) (*WebhookWorker, error) {
	opt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		return nil, fmt.Errorf("REDIS_URL: %w", err)
	}
	server := asynq.NewServer(opt, asynq.Config{
		Concurrency: concurrency,
		Queues:      map[string]int{webhookQueue: 1},
		ErrorHandler: asynq.ErrorHandlerFunc(func(ctx context.Context, task *asynq.Task, err error) {
			slog.Error("Webhook task failed.", "err", err)
		}),
	})
	return &WebhookWorker{server: server, handler: NewWebhookTaskHandler(db, &http.Client{})}, nil
}

func (w *WebhookWorker) Start() error {
	mux := asynq.NewServeMux()
	mux.Handle(WebhookTaskType, w.handler)
	return w.server.Start(mux)
}

// Shutdown waits for the tasks in progress and stops.
func (w *WebhookWorker) Shutdown() { w.server.Shutdown() }
