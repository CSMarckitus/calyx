// Command core runs the Calyx core: the WhatsApp instances, their outbound queues and the gRPC API.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"go.mau.fi/whatsmeow/store/sqlstore"
	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/config"
	"calyx/core/internal/database"
	"calyx/core/internal/engine"
	"calyx/core/internal/events"
	"calyx/core/internal/grpcserver"
	"calyx/core/internal/providers/meta"
	"calyx/core/internal/providers/normalize"
	"calyx/core/internal/providers/whatsapp"
)

// Set at build time: go build -ldflags "-X main.version=1.0.0".
var version = "dev"

// graphURL is the address of the Meta Graph API. The end-to-end test sets it at build time with
// -ldflags "-X main.graphURL=...". Empty means the real Graph API.
var graphURL string

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout))
}

// run handles --version, which needs no configuration, then loads the configuration and serves.
// It returns the exit status.
func run(args []string, getenv func(string) string, stdout io.Writer) int {
	if len(args) == 1 && args[0] == "--version" {
		fmt.Fprintln(stdout, version)
		return 0
	}
	cfg, err := config.Load(getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid configuration:\n%s\n", err)
		return 1
	}
	config.SetupLogging(cfg.LogFormat)
	if err := serve(cfg); err != nil {
		slog.Error("The core stopped because of an error.", "err", err)
		return 1
	}
	return 0
}

func serve(cfg config.Config) error {
	slog.Info("Starting Calyx Core.", "version", version)

	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	db, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	whatsmeowStore, err := database.NewWhatsmeowStore(ctx, db)
	if err != nil {
		return err
	}
	// Before any pairing: keys of removed instances and the data they left behind.
	if err := database.DeleteOrphanedDevices(ctx, db); err != nil {
		return err
	}
	if err := database.ForgetUnusedWhatsmeowData(ctx, db); err != nil {
		return err
	}

	bus := events.NewBus()
	dispatcherOptions := events.DispatcherOptions{}
	if cfg.RedisURL != "" {
		enqueuer, err := events.NewAsynqEnqueuer(cfg.RedisURL)
		if err != nil {
			return err
		}
		defer enqueuer.Close()
		dispatcherOptions.Enqueuer = enqueuer
		slog.Info("Webhooks go through Redis: run the worker to deliver them.")
	}
	dispatcher := events.NewDispatcher(db, bus, dispatcherOptions)
	dispatcher.Start()

	metaOptions := meta.Options{GraphURL: graphURL, Version: cfg.MetaGraphVersion}
	orchestrator := engine.NewOrchestrator(db, engine.NewBulkStore(db), bus, engine.OrchestratorOptions{
		BuildProvider:      providerFactory(db, whatsmeowStore, metaOptions),
		Webhooks:           dispatcher,
		NormalizeRecipient: normalize.Recipient,
	})
	// Before any outbox runs: a send the last process left unfinished becomes UNKNOWN.
	if err := orchestrator.StartSendRecords(ctx); err != nil {
		return err
	}
	orchestrator.LoadAllActiveInstances(ctx)
	orchestrator.StartWakeRetry()

	metaWebhooks, err := startMetaWebhookServer(orchestrator, cfg.MetaWebhookPort, cfg.MetaVerifyToken)
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", net.JoinHostPort(cfg.GRPCHost, strconv.Itoa(cfg.GRPCPort)))
	if err != nil {
		return fmt.Errorf("listen for gRPC: %w", err)
	}
	server := grpcserver.New(orchestrator, bus, grpcserver.Options{AuthToken: cfg.AuthToken, Version: version})
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	slog.Info("Calyx Core is fully operational.", "grpc", listener.Addr().String())

	select {
	case <-ctx.Done():
		slog.Info("Shutting down.")
	case err := <-serveErr:
		slog.Error("The gRPC server stopped.", "err", err)
	}
	// From here a second Ctrl+C or SIGTERM ends the process at once, if the shutdown hangs.
	stopSignals()

	// New calls stop first, then the sessions close (unfinished bulk jobs stay in the database),
	// then the last webhooks are delivered.
	server.Stop(5 * time.Second)
	if metaWebhooks != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = metaWebhooks.Shutdown(shutdownCtx)
		cancel()
	}
	orchestrator.DestroyAllSessions()
	dispatcher.Stop()
	slog.Info("Shutdown complete.")
	return nil
}

// startMetaWebhookServer opens the HTTP endpoint Meta posts incoming messages to, only when
// META_WEBHOOK_PORT is set. It listens on every interface: Meta must be able to reach it.
func startMetaWebhookServer(orchestrator *engine.Orchestrator, port, verifyToken string) (*http.Server, error) {
	if port == "" {
		return nil, nil
	}
	listener, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return nil, fmt.Errorf("listen for Meta webhooks: %w", err)
	}
	server := &http.Server{
		Handler:           meta.NewWebhookServer(orchestrator.LiveProviders, verifyToken),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("The Meta webhook server stopped.", "err", err)
		}
	}()
	slog.Info("Meta webhook endpoint ready.", "address", listener.Addr().String(), "path", "/meta/webhook")
	return server, nil
}

// providerFactory builds the provider of each instance.
func providerFactory(db *sql.DB, whatsmeowStore *sqlstore.Container, metaOptions meta.Options) engine.ProviderFactory {
	return func(row engine.InstanceRow, hooks engine.ProviderHooks) (engine.Provider, error) {
		switch row.Provider {
		case "WHATSAPP_WEB":
			return whatsapp.New(row, hooks, db, whatsmeowStore), nil
		case "META_CLOUD":
			return meta.New(row, hooks, metaOptions)
		default:
			return nil, calyxerr.New(codes.FailedPrecondition, "This core cannot run %s instances yet.", row.Provider)
		}
	}
}
