package instances_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/status"

	"calyx/backend/internal/core"
	"calyx/backend/internal/database/gen"
	"calyx/backend/internal/instances"
	"calyx/backend/internal/live"
	"calyx/backend/internal/tenancy"
	"calyx/backend/internal/testutil"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

type env struct {
	pool                  *pgxpool.Pool
	fc                    *testutil.FakeCore
	clk                   *testutil.FakeClock
	svc                   *instances.Service
	lv                    *live.Live
	tn                    testutil.Tenant // WhatsApp Web allowed and acknowledged, max 10
	owner, userA1, userA2 tenancy.Principal
	key                   []byte // 32 bytes
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := testutil.NewDB(t)
	fc, conn := testutil.NewFakeCore(t)
	clk := testutil.NewFakeClock(time.Now())
	key := testKey(7)
	svc := instances.NewService(pool, core.NewClient(conn), clk, key, "main", 3*time.Minute)
	lv := live.New()
	svc.SetLive(lv)
	tn := testutil.SeedTenant(t, pool, testutil.TenantOptions{Name: "Acme", WhatsAppWebAllowed: true, Acknowledged: true})
	a1, a2 := tn.ClientA1, tn.ClientA2
	e := &env{
		pool: pool, fc: fc, clk: clk, svc: svc, lv: lv, tn: tn, key: key,
		owner:  tenancy.Principal{UserID: tn.OwnerID, OrganizationID: tn.OrganizationID, Role: tenancy.RoleOwner},
		userA1: tenancy.Principal{UserID: tn.UserA1, OrganizationID: tn.OrganizationID, Role: tenancy.RoleClient, ClientID: &a1},
		userA2: tenancy.Principal{UserID: tn.UserA2, OrganizationID: tn.OrganizationID, Role: tenancy.RoleClient, ClientID: &a2},
	}
	t.Cleanup(svc.CancelAllPairings)
	return e
}

// pairingScript makes the next CreateInstance calls of the fake core wait for the test.
// send pushes one update to the open stream; finish ends the stream with err (nil = success).
// ctxDone is closed when the core's stream context ends (the backend cancelled the session).
type pairingScript struct {
	updates chan *corev1.CreateInstanceResponse
	end     chan error
	ctxDone chan struct{}
	once    sync.Once
}

func newPairingScript(fc *testutil.FakeCore) *pairingScript {
	p := &pairingScript{
		updates: make(chan *corev1.CreateInstanceResponse, 64),
		end:     make(chan error, 4),
		ctxDone: make(chan struct{}),
	}
	fc.OnCreateInstance(func(ctx context.Context, _ *corev1.CreateInstanceRequest, send func(*corev1.CreateInstanceResponse) error) error {
		for {
			select {
			case u := <-p.updates:
				if err := send(u); err != nil {
					return err
				}
			case err := <-p.end:
				for {
					select {
					case u := <-p.updates:
						if err := send(u); err != nil {
							return err
						}
					default:
						return err
					}
				}
			case <-ctx.Done():
				p.once.Do(func() { close(p.ctxDone) })
				return status.FromContextError(ctx.Err()).Err()
			}
		}
	})
	return p
}

func (p *pairingScript) send(st corev1.InstanceStatus, pairingCode, message string) {
	r := &corev1.CreateInstanceResponse{Status: st, Message: message}
	if pairingCode != "" {
		r.PairingCode = &pairingCode
	}
	p.updates <- r
}

func (p *pairingScript) finish(err error) { p.end <- err }

const (
	needsAuth = corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH
	connected = corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED
)

func (e *env) row(t *testing.T, id uuid.UUID) gen.Instance {
	t.Helper()
	r, err := gen.New(e.pool).GetInstanceByID(context.Background(), id)
	if err != nil {
		t.Fatalf("read the row: %v", err)
	}
	return r
}

func (e *env) rowExists(t *testing.T, id uuid.UUID) bool {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM instances WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func (e *env) count(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *env) seed(t *testing.T, in testutil.InstanceSeed) testutil.SeededInstance {
	t.Helper()
	if in.OrganizationID == uuid.Nil {
		in.OrganizationID = e.tn.OrganizationID
	}
	if in.ClientID == uuid.Nil {
		in.ClientID = e.tn.ClientA1
	}
	return testutil.SeedInstance(t, e.pool, in)
}

func requireCode(t *testing.T, err error, status int, code string) {
	t.Helper()
	ae, ok := apierrAs(err)
	if !ok || ae.Status != status || ae.Code != code {
		t.Fatalf("want %d %s, got %v", status, code, err)
	}
}

func strp(s string) *string { return &s }

func encrypt(key []byte, plain string) ([]byte, error) { return instances.EncryptSecret(key, plain) }

// holdOpenScript makes every CreateInstance send one QR update and then wait until the core
// stream context ends.
func holdOpenScript(fc *testutil.FakeCore) {
	fc.OnCreateInstance(func(ctx context.Context, _ *corev1.CreateInstanceRequest, send func(*corev1.CreateInstanceResponse) error) error {
		qr := "QR"
		if err := send(&corev1.CreateInstanceResponse{Status: needsAuth, PairingCode: &qr}); err != nil {
			return err
		}
		<-ctx.Done()
		return status.FromContextError(ctx.Err()).Err()
	})
}

// createPaired creates a WhatsApp Web instance that ends connected, then makes the row and the
// fake core say NEEDS_AUTH, so the instance can be paired again.
func (e *env) createPaired(t *testing.T, in instances.CreateInput) instances.View {
	t.Helper()
	sc := newPairingScript(e.fc)
	sc.send(needsAuth, "QR1", "scan")
	sc.send(connected, "", "ok")
	sc.finish(nil)
	v, _, err := e.svc.Create(context.Background(), e.owner, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for e.row(t, v.ID).State != "active" {
		if time.Now().After(deadline) {
			t.Fatal("the instance did not become active")
		}
		time.Sleep(5 * time.Millisecond)
	}
	e.fc.SetStatus(v.CoreName, needsAuth)
	if _, err := e.pool.Exec(context.Background(), `UPDATE instances SET last_status = 'NEEDS_AUTH' WHERE id = $1`, v.ID); err != nil {
		t.Fatal(err)
	}
	return v
}

// running tells whether a pairing session runs for the instance. A follow does not tell it: a
// follow also works for a kept failure.
func (e *env) running(id uuid.UUID) bool { return instances.PairingsOf(e.svc).Running(id) }

// requireFields checks a 400 invalid_request error and the paths of its field errors, in order.
func requireFields(t *testing.T, err error, fields ...string) {
	t.Helper()
	requireCode(t, err, 400, "invalid_request")
	ae, _ := apierrAs(err)
	got := make([]string, 0, len(ae.Errors))
	for _, fe := range ae.Errors {
		got = append(got, fe.Field)
	}
	if fmt.Sprint(got) != fmt.Sprint(fields) {
		t.Fatalf("fields = %v, want %v (%+v)", got, fields, ae.Errors)
	}
}
