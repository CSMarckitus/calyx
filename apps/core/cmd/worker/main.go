// Command worker delivers the webhooks the core puts in Redis (when the core runs with REDIS_URL).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"calyx/core/internal/config"
	"calyx/core/internal/database"
	"calyx/core/internal/events"
)

// Webhooks delivered in parallel by one worker process.
const concurrency = 50

func main() {
	cfg, err := config.LoadWorker(os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid configuration:\n%s\n", err)
		os.Exit(1)
	}
	config.SetupLogging(cfg.LogFormat)
	if err := run(cfg); err != nil {
		slog.Error("The worker stopped because of an error.", "err", err)
		os.Exit(1)
	}
}

func run(cfg config.WorkerConfig) error {
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	// The worker reads the URL and secret of each webhook when it delivers it.
	db, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	worker, err := events.NewWebhookWorker(cfg.RedisURL, db, concurrency)
	if err != nil {
		return err
	}
	if err := worker.Start(); err != nil {
		return err
	}
	slog.Info("Webhook worker running.", "concurrency", concurrency)

	<-ctx.Done()
	// From here a second Ctrl+C or SIGTERM ends the process at once, if the shutdown hangs.
	stopSignals()
	slog.Info("Shutting down: finishing the webhooks in progress.")
	worker.Shutdown()
	return nil
}
