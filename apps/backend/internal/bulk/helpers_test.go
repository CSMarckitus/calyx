package bulk_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/bulk"
	"calyx/backend/internal/core"
	"calyx/backend/internal/instances"
	"calyx/backend/internal/tenancy"
	"calyx/backend/internal/testutil"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

type env struct {
	pool                  *pgxpool.Pool
	fc                    *testutil.FakeCore
	inst                  *instances.Service
	svc                   *bulk.Service
	tn                    testutil.Tenant
	owner, userA1, userA2 tenancy.Principal
	hook                  chan uuid.UUID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := testutil.NewDB(t)
	fc, conn := testutil.NewFakeCore(t)
	c := core.NewClient(conn)
	clk := testutil.NewFakeClock(time.Now())
	inst := instances.NewService(pool, c, clk, make([]byte, 32), "main", 3*time.Minute)
	tn := testutil.SeedTenant(t, pool, testutil.TenantOptions{Name: "Acme", WhatsAppWebAllowed: true, Acknowledged: true})
	a1, a2 := tn.ClientA1, tn.ClientA2
	e := &env{
		pool: pool, fc: fc, inst: inst, svc: bulk.NewService(pool, c, inst), tn: tn,
		owner:  tenancy.Principal{UserID: tn.OwnerID, OrganizationID: tn.OrganizationID, Role: tenancy.RoleOwner},
		userA1: tenancy.Principal{UserID: tn.UserA1, OrganizationID: tn.OrganizationID, Role: tenancy.RoleClient, ClientID: &a1},
		userA2: tenancy.Principal{UserID: tn.UserA2, OrganizationID: tn.OrganizationID, Role: tenancy.RoleClient, ClientID: &a2},
		hook:   make(chan uuid.UUID, 16),
	}
	inst.SetStatusCheck(func(_ context.Context, id uuid.UUID) { e.hook <- id })
	return e
}

// instance seeds an active row of ClientA1 that the fake core knows as CONNECTED.
func (e *env) instance(t *testing.T) testutil.SeededInstance {
	t.Helper()
	return e.instanceOf(t, e.tn.ClientA1)
}

func (e *env) instanceOf(t *testing.T, client uuid.UUID) testutil.SeededInstance {
	t.Helper()
	st := "CONNECTED"
	row := testutil.SeedInstance(t, e.pool, testutil.InstanceSeed{OrganizationID: e.tn.OrganizationID, ClientID: client, LastStatus: &st})
	e.fc.AddInstance(row.CoreName, corev1.Provider_PROVIDER_WHATSAPP_WEB, corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED)
	return row
}

func (e *env) count(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM bulk_jobs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func requireCode(t *testing.T, err error, status int, code string) {
	t.Helper()
	ae, ok := apierr.As(err)
	if !ok || ae.Status != status || ae.Code != code {
		t.Fatalf("want %d %s, got %v", status, code, err)
	}
}

func (e *env) expectHook(t *testing.T, id uuid.UUID) {
	t.Helper()
	select {
	case got := <-e.hook:
		if got != id {
			t.Fatalf("hook got %v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the hook was not called")
	}
	select {
	case <-e.hook:
		t.Fatal("the hook was called twice")
	case <-time.After(50 * time.Millisecond):
	}
}

func (e *env) expectNoHook(t *testing.T) {
	t.Helper()
	select {
	case <-e.hook:
		t.Fatal("the hook was called")
	case <-time.After(100 * time.Millisecond):
	}
}

func recipients(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("34600%06d", i) // unique: a repeated recipient is refused
	}
	return out
}

func apierrAs(err error) (*apierr.Error, bool) { return apierr.As(err) }

func ptr[T any](v T) *T { return &v }

// requireFields checks a 400 invalid_request error and the paths of its field errors, in order.
func requireFields(t *testing.T, err error, fields ...string) {
	t.Helper()
	requireCode(t, err, 400, "invalid_request")
	ae, _ := apierrAs(err)
	got := make([]string, 0, len(ae.Errors))
	for _, fe := range ae.Errors {
		got = append(got, fe.Field)
	}
	if strings.Join(got, ",") != strings.Join(fields, ",") {
		t.Fatalf("fields = %v, want %v (%+v)", got, fields, ae.Errors)
	}
}
