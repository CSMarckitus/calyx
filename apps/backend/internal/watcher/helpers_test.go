package watcher_test

import (
	"context"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"calyx/backend/internal/config"
	"calyx/backend/internal/core"
	"calyx/backend/internal/database/gen"
	"calyx/backend/internal/instances"
	"calyx/backend/internal/live"
	"calyx/backend/internal/mail"
	"calyx/backend/internal/testutil"
	"calyx/backend/internal/watcher"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

const (
	stConnected    = corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED
	stConnecting   = corev1.InstanceStatus_INSTANCE_STATUS_CONNECTING
	stDisconnected = corev1.InstanceStatus_INSTANCE_STATUS_DISCONNECTED
	stNeedsAuth    = corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH
	stError        = corev1.InstanceStatus_INSTANCE_STATUS_ERROR
)

type env struct {
	pool *pgxpool.Pool
	fc   *testutil.FakeCore
	clk  *testutil.FakeClock
	smtp *testutil.FakeSMTP
	cfg  config.Config
	inst *instances.Service
	cl   *core.Client
	lv   *live.Live
	tn   testutil.Tenant // OwnerEmail is the alert recipient
}

func newEnv(t *testing.T, smtpOpts testutil.FakeSMTPOptions) *env {
	t.Helper()
	pool := testutil.NewDB(t)
	fc, conn := testutil.NewFakeCore(t)
	clk := testutil.NewFakeClock(time.Now().UTC().Truncate(time.Second))
	smtp := testutil.NewFakeSMTP(t, smtpOpts)
	pub, _ := url.Parse("https://calyx.test")
	cfg := config.Config{
		CoreID: "main", AlertGrace: 5 * time.Minute, StateCheckInterval: 30 * time.Second,
		PairingTimeout: 3 * time.Minute, PublicURL: pub, OperatorEmail: "ops@test.example",
		SecretKey: make([]byte, 32),
		SMTP:      config.SMTP{Host: smtp.Host(), Port: smtp.Port(), From: "Calyx <calyx@test.example>"},
	}
	cl := core.NewClient(conn)
	inst := instances.NewService(pool, cl, clk, cfg.SecretKey, cfg.CoreID, cfg.PairingTimeout)
	t.Cleanup(inst.CancelAllPairings)
	lv := live.New()
	inst.SetLive(lv) // status_stale comes from Live
	tn := testutil.SeedTenant(t, pool, testutil.TenantOptions{Name: "Acme", WhatsAppWebAllowed: true, Acknowledged: true})
	return &env{pool: pool, fc: fc, clk: clk, smtp: smtp, cfg: cfg, inst: inst, cl: cl, lv: lv, tn: tn}
}

// newWatcher returns a new Watcher on the same database: a "restart".
func (e *env) newWatcher() *watcher.Watcher {
	return e.watcherWith(e.smtpSender())
}

// mailOff returns a Watcher for a backend without SMTP.
func (e *env) mailOff() *watcher.Watcher {
	cfg := e.cfg
	cfg.SMTP = config.SMTP{}
	return watcher.New(e.pool, e.cl, e.inst, mail.NewSMTPSender(cfg.SMTP, nil), e.clk, cfg, e.lv, nil)
}

func (e *env) row(t *testing.T, id uuid.UUID) gen.Instance {
	t.Helper()
	r, err := gen.New(e.pool).GetInstanceByID(context.Background(), id)
	if err != nil {
		t.Fatalf("read the row: %v", err)
	}
	return r
}

func (e *env) mailsTo(addr string) []testutil.ReceivedMail {
	var out []testutil.ReceivedMail
	for _, m := range e.smtp.Messages() {
		for _, to := range m.To {
			if to == addr {
				out = append(out, m)
			}
		}
	}
	return out
}

func (e *env) coreStatus(t *testing.T) gen.GetCoreStatusRow {
	t.Helper()
	r, err := gen.New(e.pool).GetCoreStatus(context.Background(), e.cfg.CoreID)
	if err != nil {
		t.Fatalf("read core_status: %v", err)
	}
	return r
}

// addInstance writes an active row named "Front desk" and makes the fake core know it.
func (e *env) addInstance(t *testing.T, in testutil.InstanceSeed, coreStatus corev1.InstanceStatus) testutil.SeededInstance {
	t.Helper()
	if in.OrganizationID == uuid.Nil {
		in.OrganizationID = e.tn.OrganizationID
	}
	if in.ClientID == uuid.Nil {
		in.ClientID = e.tn.ClientA1
	}
	seeded := testutil.SeedInstance(t, e.pool, in)
	if _, err := e.pool.Exec(context.Background(), `UPDATE instances SET display_name = 'Front desk' WHERE id = $1`, seeded.ID); err != nil {
		t.Fatal(err)
	}
	provider := corev1.Provider_PROVIDER_WHATSAPP_WEB
	if in.Provider == "META_CLOUD" {
		provider = corev1.Provider_PROVIDER_META_CLOUD
	}
	e.fc.AddInstance(seeded.CoreName, provider, coreStatus)
	return seeded
}

func strp(s string) *string { return &s }

// round runs one round and fails the test on an error.
func round(t *testing.T, w *watcher.Watcher) {
	t.Helper()
	if err := w.Round(context.Background()); err != nil {
		t.Fatalf("Round: %v", err)
	}
}

func (e *env) smtpSender() mail.Sender {
	return mail.NewSMTPSender(e.cfg.SMTP, e.smtp.ClientTLS())
}

// watcherWith returns a Watcher that sends its mails through sender.
func (e *env) watcherWith(sender mail.Sender) *watcher.Watcher {
	return watcher.New(e.pool, e.cl, e.inst, sender, e.clk, e.cfg, e.lv, nil)
}

// hookSender calls hook before each mail, with the number of the mail (1 for the first). An error
// of hook is the result of the send.
type hookSender struct {
	next mail.Sender
	hook func(ctx context.Context, n int) error
	mu   sync.Mutex
	n    int
}

func (h *hookSender) Send(ctx context.Context, m mail.Message) error {
	h.mu.Lock()
	h.n++
	n := h.n
	h.mu.Unlock()
	if err := h.hook(ctx, n); err != nil {
		return err
	}
	return h.next.Send(ctx, m)
}

// gate makes each mail wait until the test closes release. Each mail that waits puts a value in
// entered.
type gate struct {
	entered chan struct{}
	release chan struct{}
}

func newGate() *gate {
	return &gate{entered: make(chan struct{}, 10), release: make(chan struct{})}
}

func (g *gate) wait(ctx context.Context, _ int) error {
	g.entered <- struct{}{}
	select {
	case <-g.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// waitEntered fails the test when no mail starts within 2 seconds.
func (g *gate) waitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-g.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("no mail started")
	}
}

// startRun starts Run and waits until its first round ended and its timer waits.
func (e *env) startRun(t *testing.T, w *watcher.Watcher) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, "the timer of the first round", func() bool { return e.clk.PendingTimers() >= 1 })
}
