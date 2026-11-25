// Command backend is the Calyx backend: the public API on top of the core.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"calyx/backend/internal/auth"
	"calyx/backend/internal/bulk"
	"calyx/backend/internal/clock"
	"calyx/backend/internal/config"
	"calyx/backend/internal/core"
	"calyx/backend/internal/database"
	"calyx/backend/internal/database/gen"
	"calyx/backend/internal/httpapi"
	"calyx/backend/internal/instances"
	"calyx/backend/internal/live"
	"calyx/backend/internal/mail"
	"calyx/backend/internal/operator"
	"calyx/backend/internal/tenancy"
	"calyx/backend/internal/watcher"
)

// version is set with -ldflags "-X main.version=..." by the Dockerfile.
var version = "dev"

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, operator.Usage)
		return 2
	}
	switch args[0] {
	case "--version":
		// Like help, it needs no configuration.
		fmt.Fprintln(stdout, version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, operator.Usage)
		return 0
	case "openapi":
		// The document needs no configuration: it is built from the types of the operations.
		doc, err := httpapi.OpenAPIDocument()
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		_, _ = stdout.Write(doc)
		return 0
	case "serve", "migrate", "org", "user", "instances", "mail":
		// A known command: the configuration is read below.
	default:
		fmt.Fprintf(stderr, "Error: unknown command %q.\n\n%s", args[0], operator.Usage)
		return 2
	}
	cfg, err := config.Load(getenv)
	if err != nil {
		fmt.Fprintf(stderr, "Error: invalid configuration:\n%s\n", err)
		return 1
	}
	// Logs go to stderr for every command. The core client logs through slog.Default(), so the
	// default must be set before the client is built.
	log := config.NewLogger(cfg.LogFormat, stderr)
	slog.SetDefault(log)
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(stderr, connectError(err, cfg.DatabaseURL))
		return 1
	}
	switch args[0] {
	case "migrate":
		return migrate(ctx, pool, stdout, stderr)
	case "serve":
		signals := make(chan os.Signal, 2)
		signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(signals)
		listen := func() (net.Listener, error) {
			return net.Listen("tcp", net.JoinHostPort(cfg.HTTPHost, strconv.Itoa(cfg.HTTPPort)))
		}
		return serve(ctx, cfg, log, pool, listen, dialCore(cfg), signals)
	default:
		return operatorCommand(ctx, args, cfg, pool, stdout, stderr)
	}
}

// migrate applies the migrations under the advisory lock, then closes the pool.
func migrate(ctx context.Context, pool *pgxpool.Pool, stdout, stderr io.Writer) int {
	defer pool.Close()
	release, err := database.Lock(ctx, pool)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	defer release()
	if err := database.Migrate(ctx, pool); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "Database migrated.")
	return 0
}

// operatorCommand builds the services that the operator commands use and runs one command. It
// neither migrates nor takes the advisory lock: the commands run next to a running server.
func operatorCommand(ctx context.Context, args []string, cfg config.Config, pool *pgxpool.Pool, stdout, stderr io.Writer) int {
	defer pool.Close()
	conn, err := core.Dial(cfg.CoreAddress, cfg.CoreToken)
	if err != nil {
		fmt.Fprintf(stderr, "Error: cannot create the core client: %v\n", err)
		return 1
	}
	defer conn.Close()
	clk := clock.Real()
	mailer := mail.NewSMTPSender(cfg.SMTP, nil)
	return operator.Run(ctx, args, operator.Deps{
		Config:  cfg,
		Pool:    pool,
		Mailer:  mailer,
		Auth:    auth.NewService(pool, clk, mailer, cfg.PublicURL, auth.NewLimiter(clk)),
		Tenancy: tenancy.NewService(pool, clk),
		Core:    core.NewClient(conn),
	}, stdout, stderr)
}

// dialCore gives the core client of the configuration and the connection that serve closes at the end.
func dialCore(cfg config.Config) func() (*core.Client, io.Closer, error) {
	return func() (*core.Client, io.Closer, error) {
		conn, err := core.Dial(cfg.CoreAddress, cfg.CoreToken)
		if err != nil {
			return nil, nil, err
		}
		return core.NewClient(conn), conn, nil
	}
}

// shutdownGrace is how long http.Server.Shutdown waits for the requests in progress.
const shutdownGrace = 5 * time.Second

// lockCheckInterval is how often serve checks the connection that holds the advisory lock, in
// nanoseconds. It is atomic so a test can shorten it while other servers run.
var lockCheckInterval atomic.Int64

func init() { lockCheckInterval.Store(int64(30 * time.Second)) }

// mailCheckTimeout is the time limit of the SMTP check at start.
const mailCheckTimeout = 10 * time.Second

// checkMail connects to the SMTP server, as the first mail would, and sends nothing. A failure
// is logged and never stops the server: a mail outage must not stop the API.
func checkMail(ctx context.Context, cfg config.SMTP, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(ctx, mailCheckTimeout)
	defer cancel()
	if err := mail.CheckSMTP(ctx, cfg, nil); err != nil {
		log.Error("The SMTP check failed: the alert mails will fail. Run 'backend mail test ADDRESS' to see the error.", "error", err)
		return
	}
	log.Info("SMTP check passed.")
}

// serve runs the server until ctx ends, a first signal arrives or the server fails. It owns pool:
// it closes it before it returns, except after a second signal. listen and signals come from
// outside, so the tests can drive both. dial gives the core client, so a test can give a fake core.
func serve(ctx context.Context, cfg config.Config, log *slog.Logger, pool *pgxpool.Pool,
	listen func() (net.Listener, error), dial func() (*core.Client, io.Closer, error), signals <-chan os.Signal,
) int {
	log.Info("Starting Calyx backend.", "version", version)

	// 1. One backend process at a time, then the schema.
	release, checkLock, err := database.LockWithCheck(ctx, pool)
	if err != nil {
		log.Error("Cannot start.", "error", err)
		pool.Close()
		return 1
	}
	stopEarly := func(msg string, attrs ...any) int {
		log.Error(msg, attrs...)
		release()
		pool.Close()
		return 1
	}
	if err := database.Migrate(ctx, pool); err != nil {
		return stopEarly("Cannot migrate the database.", "error", err)
	}
	// 2. The instances must belong to the core of this configuration.
	other, err := gen.New(pool).OperatorForeignCoreID(ctx, cfg.CoreID)
	switch {
	case err == nil:
		return stopEarly("An instance belongs to another core. The database has instances of a core other than main.",
			"found", other, "core_id", cfg.CoreID)
	case !errors.Is(err, pgx.ErrNoRows):
		return stopEarly("Cannot check the core of the instances.", "error", err)
	}
	// 3. The core client does not connect yet: a core that is down is not a start error.
	coreClient, conn, err := dial()
	if err != nil {
		return stopEarly("Cannot create the core client.", "error", err)
	}

	// 4. The services.
	clk := clock.Real()
	mailer := mail.NewSMTPSender(cfg.SMTP, nil)
	authSvc := auth.NewService(pool, clk, mailer, cfg.PublicURL, auth.NewLimiter(clk))
	tenancySvc := tenancy.NewService(pool, clk)
	lv := live.New()
	// Two failed rounds before the stop left the core unreachable. Keep it so until a round succeeds.
	if since, err := gen.New(pool).GetCoreUnreachableSince(ctx, cfg.CoreID); err == nil && since != nil {
		lv.SetUnreachable()
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		log.Warn("Cannot read the stored health of the core.", "error", err)
	}
	inst := instances.NewService(pool, coreClient, clk, cfg.SecretKey, cfg.CoreID, cfg.PairingTimeout)
	inst.SetLive(lv)
	bulkSvc := bulk.NewService(pool, coreClient, inst)
	w := watcher.New(pool, coreClient, inst, mailer, clk, cfg, lv, log)
	inst.SetStatusCheck(w.CheckInstance)

	// 5. Recovery of rows that a stop left in the state creating or removing.
	if list, err := coreClient.ListInstances(ctx); err != nil {
		log.Warn("The core cannot be reached. Recovery runs again on the next state check.")
	} else if err := inst.Recover(ctx, list); err != nil {
		log.Error("Recovery failed.", "error", err)
	}

	// 6. The watcher.
	watcherCtx, stopWatcher := context.WithCancel(context.Background())
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		w.Run(watcherCtx)
	}()
	// The listener starts after the watcher, because a link that comes up asks the watcher for a round.
	listenerCtx, stopListener := context.WithCancel(context.Background())
	listenerDone := make(chan struct{})
	go func() {
		defer close(listenerDone)
		watcher.NewListener(w, coreClient, lv, clk, log, version).Run(listenerCtx)
	}()

	// A PostgreSQL restart ends the session and drops the lock without a sign. Stop the process:
	// Docker restarts it, and it takes the lock again.
	lockLost := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Duration(lockCheckInterval.Load()))
		defer t.Stop()
		for {
			select {
			case <-watcherCtx.Done():
				return
			case <-t.C:
			}
			cctx, cancel := context.WithTimeout(watcherCtx, 10*time.Second)
			err := checkLock(cctx)
			cancel()
			if err != nil && watcherCtx.Err() == nil {
				log.Error("The advisory lock is lost.", "error", err)
				close(lockLost)
				return
			}
		}
	}()

	// stopServices is the end of the shutdown, after the HTTP server stopped.
	stopServices := func() {
		stopListener()
		<-listenerDone
		stopWatcher()
		<-watcherDone
		inst.CancelAllPairings()
		// A reset mail in progress needs the pool and the mailer.
		wctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		if err := authSvc.WaitBackground(wctx); err != nil {
			log.Warn("Background reset work did not end in time.", "error", err)
		}
		cancel()
		conn.Close()
		release()
		pool.Close()
	}

	// 7. The HTTP server.
	ln, err := listen()
	if err != nil {
		log.Error("Cannot listen.", "error", err)
		stopServices()
		return 1
	}
	streamsDone := make(chan struct{})
	srv := &http.Server{
		Handler: httpapi.NewServer(httpapi.Deps{
			Config: cfg, Auth: authSvc, Tenancy: tenancySvc, Instances: inst, Bulk: bulkSvc,
			Pool: pool, Clock: clk, Logger: log, Live: lv, Version: version,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second, // a 16 MiB bulk body on a slow link
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: a send waits for the pacing of the core, and SSE streams stay open.
		BaseContext: func(net.Listener) context.Context {
			return httpapi.WithShutdownSignal(context.Background(), streamsDone)
		},
		ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	srv.RegisterOnShutdown(func() { close(streamsDone) })
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	log.Info("Calyx backend is ready.", "address", ln.Addr().String(), "public_url", cfg.PublicURL.String())
	if cfg.MailConfigured() {
		// In the background, so a slow mail server never delays the API.
		go checkMail(watcherCtx, cfg.SMTP, log)
	} else {
		log.Warn("Mail is not configured: invitation and reset links are returned in the API answers, and alerts are not emailed.")
	}

	// 8. Wait for the end.
	failed := false
	select {
	case <-ctx.Done():
	case <-signals:
	case <-lockLost:
		failed = true
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("The HTTP server failed.", "error", err)
			failed = true
		}
	}

	// Shutdown order: the HTTP server, the listener, the watcher, the pairing sessions, the reset mails, the core connection,
	// the lock and the pool.
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		sctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := srv.Shutdown(sctx); err != nil {
			log.Warn("The requests did not end in time. Closing the connections.", "error", err)
			_ = srv.Close()
		}
		stopServices()
		log.Info("Shutdown complete.")
	}()
	select {
	case <-shutdownDone:
	case <-signals:
		// The operating system closes the connections, and PostgreSQL frees the advisory lock
		// with the session.
		log.Error("Second signal: stopping now.")
		return 1
	}
	if failed {
		return 1
	}
	return 0
}

// connectError builds the text for a failed connection. It leaves out the cause when the cause
// contains the password of the URL.
func connectError(err error, databaseURL string) string {
	msg := "Error: cannot connect to the database"
	if u, perr := url.Parse(databaseURL); perr == nil && u.User != nil {
		if pw, ok := u.User.Password(); ok && pw != "" && strings.Contains(err.Error(), pw) {
			return msg
		}
	}
	if errors.Is(err, context.Canceled) {
		return msg
	}
	return msg + ": " + err.Error()
}
