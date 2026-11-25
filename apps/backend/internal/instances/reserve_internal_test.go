package instances

import (
	"context"
	"testing"
	"time"

	"calyx/backend/internal/core"
	"calyx/backend/internal/tenancy"
	"calyx/backend/internal/testutil"
)

// failingCtx ends after limit calls of Done, so a test can fail the second read of loadReserved.
type failingCtx struct {
	context.Context
	calls, limit int
}

func (c *failingCtx) Done() <-chan struct{} {
	c.calls++
	if c.calls > c.limit {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return c.Context.Done()
}

func (c *failingCtx) Err() error {
	if c.calls > c.limit {
		return context.Canceled
	}
	return nil
}

func TestAFailedSecondReadReleasesTheReservation(t *testing.T) {
	pool := testutil.NewDB(t)
	_, conn := testutil.NewFakeCore(t)
	s := NewService(pool, core.NewClient(conn), testutil.NewFakeClock(time.Now()), make([]byte, 32), "main", time.Minute)
	tn := testutil.SeedTenant(t, pool, testutil.TenantOptions{Name: "Acme"})
	row := testutil.SeedInstance(t, pool, testutil.InstanceSeed{OrganizationID: tn.OrganizationID, ClientID: tn.ClientA1})
	owner := tenancy.Principal{UserID: tn.OwnerID, OrganizationID: tn.OrganizationID, Role: tenancy.RoleOwner}

	counter := &failingCtx{Context: context.Background(), limit: 1 << 30}
	if _, err := s.load(counter, owner, row.ID); err != nil {
		t.Fatal(err)
	}
	// The first read uses counter.calls calls of Done, so the second one fails.
	failing := &failingCtx{Context: context.Background(), limit: counter.calls}
	if _, _, err := s.loadReserved(failing, owner, row.ID); err == nil {
		t.Fatal("the second read did not fail")
	}
	if _, _, err := s.loadReserved(context.Background(), owner, row.ID); err != nil {
		t.Fatalf("the reservation leaked: %v", err)
	}
}
