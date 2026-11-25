package instances_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/tenancy"
	"calyx/backend/internal/testutil"
)

func apierrAs(err error) (*apierr.Error, bool) { return apierr.As(err) }

func (e *env) addWebhook(t *testing.T, instanceID uuid.UUID, url, event string, secret *string) {
	t.Helper()
	var sealed []byte
	if secret != nil {
		var err error
		sealed, err = encrypt(e.key, *secret)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := e.pool.Exec(context.Background(),
		`INSERT INTO webhooks (id, instance_id, url, event, secret_ciphertext) VALUES ($1, $2, $3, $4, $5)`,
		uuid.New(), instanceID, url, event, sealed)
	if err != nil {
		t.Fatal(err)
	}
}

func TestGetReturnsTheRowAndItsWebhooks(t *testing.T) {
	e := newEnv(t)
	row := e.seed(t, testutil.InstanceSeed{})
	e.addWebhook(t, row.ID, "https://b.example/m", "message.received", strp("s"))
	e.addWebhook(t, row.ID, "https://a.example/c", "connection.update", nil)
	v, err := e.svc.Get(context.Background(), e.owner, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.ID != row.ID || len(v.Webhooks) != 2 {
		t.Fatalf("got %+v", v)
	}
	if v.Webhooks[0].Event != "connection.update" || v.Webhooks[0].HasSecret ||
		v.Webhooks[1].Event != "message.received" || !v.Webhooks[1].HasSecret || v.Webhooks[1].URL != "https://b.example/m" {
		t.Fatalf("webhooks %+v", v.Webhooks)
	}
}

func TestGetOutOfScopeIsNotFound(t *testing.T) {
	e := newEnv(t)
	row := e.seed(t, testutil.InstanceSeed{ClientID: e.tn.ClientA1})
	other := testutil.SeedTenant(t, e.pool, testutil.TenantOptions{Name: "Other", WhatsAppWebAllowed: true})
	otherOwner := tenancy.Principal{UserID: other.OwnerID, OrganizationID: other.OrganizationID, Role: tenancy.RoleOwner}
	for name, p := range map[string]tenancy.Principal{"userA2": e.userA2, "other owner": otherOwner} {
		_, err := e.svc.Get(context.Background(), p, row.ID)
		if ae, ok := apierr.As(err); !ok || ae.Status != 404 || ae.Code != "not_found" {
			t.Fatalf("%s: got %v", name, err)
		}
	}
	if _, err := e.svc.Get(context.Background(), e.owner, uuid.New()); err == nil {
		t.Fatal("random id found")
	} else {
		requireCode(t, err, 404, "not_found")
	}
}

func TestListIsScoped(t *testing.T) {
	e := newEnv(t)
	e.seed(t, testutil.InstanceSeed{ClientID: e.tn.DefaultClientID})
	e.seed(t, testutil.InstanceSeed{ClientID: e.tn.ClientA1})
	e.seed(t, testutil.InstanceSeed{ClientID: e.tn.ClientA2})
	other := testutil.SeedTenant(t, e.pool, testutil.TenantOptions{Name: "Other"})
	otherOwner := tenancy.Principal{UserID: other.OwnerID, OrganizationID: other.OrganizationID, Role: tenancy.RoleOwner}
	check := func(p tenancy.Principal, want int) {
		t.Helper()
		vs, err := e.svc.List(context.Background(), p)
		if err != nil || len(vs) != want {
			t.Fatalf("want %d, got %d, %v", want, len(vs), err)
		}
	}
	check(e.owner, 3)
	check(e.userA1, 1)
	check(otherOwner, 0)
}

func TestListShowsEveryState(t *testing.T) {
	e := newEnv(t)
	for _, st := range []string{"creating", "active", "removing", "missing"} {
		e.seed(t, testutil.InstanceSeed{State: st})
	}
	vs, err := e.svc.List(context.Background(), e.owner)
	if err != nil || len(vs) != 4 {
		t.Fatalf("got %d, %v", len(vs), err)
	}
	states := map[string]bool{}
	for _, v := range vs {
		states[v.State] = true
		if v.Webhooks == nil {
			t.Fatal("Webhooks is nil")
		}
	}
	if len(states) != 4 {
		t.Fatalf("states %v", states)
	}
}

func TestStatusStaleFollowsCoreStatus(t *testing.T) {
	e := newEnv(t)
	row := e.seed(t, testutil.InstanceSeed{})
	stale := func() (bool, bool) {
		v, err := e.svc.Get(context.Background(), e.owner, row.ID)
		if err != nil {
			t.Fatal(err)
		}
		vs, err := e.svc.List(context.Background(), e.owner)
		if err != nil || len(vs) != 1 {
			t.Fatal(err)
		}
		return v.StatusStale, vs[0].StatusStale
	}
	if g, l := stale(); g || l {
		t.Fatal("stale at the start")
	}
	e.lv.SetCoreLink(false, "")
	if g, l := stale(); !g || !l {
		t.Fatal("not stale with the core link down")
	}
	e.lv.SetCoreLink(true, "")
	if g, l := stale(); !g || !l {
		t.Fatal("stale ended before a round succeeded")
	}
	e.lv.RoundOK(e.lv.LinkNumber())
	if g, l := stale(); g || l {
		t.Fatal("still stale after ok")
	}
}

func TestTriggerStatusCheckRunsTheHookOnce(t *testing.T) {
	e := newEnv(t)
	got := make(chan uuid.UUID, 4)
	e.svc.SetStatusCheck(func(_ context.Context, id uuid.UUID) { got <- id })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	id := uuid.New()
	e.svc.TriggerStatusCheck(ctx, id)
	select {
	case g := <-got:
		if g != id {
			t.Fatal("wrong id")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("hook not called")
	}
	select {
	case <-got:
		t.Fatal("hook called twice")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestTriggerStatusCheckWithoutHookDoesNothing(t *testing.T) {
	e := newEnv(t)
	e.svc.TriggerStatusCheck(context.Background(), uuid.New())
}
