package instances_test

import (
	"context"
	"testing"
	"time"

	"calyx/backend/internal/core"
	"calyx/backend/internal/instances"
	"calyx/backend/internal/testutil"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

func TestRecoverDeletesAnOldCreatingRowTheCoreDoesNotHave(t *testing.T) {
	e := newEnv(t)
	row := e.seed(t, testutil.InstanceSeed{State: "creating", CreatedAt: e.clk.Now().Add(-5 * time.Minute)})
	if err := e.svc.Recover(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if e.rowExists(t, row.ID) {
		t.Fatal("the row remains")
	}
}

func TestRecoverActivatesAnOldCreatingRowTheCoreHas(t *testing.T) {
	e := newEnv(t)
	row := e.seed(t, testutil.InstanceSeed{State: "creating", CreatedAt: e.clk.Now().Add(-5 * time.Minute)})
	infos := []core.InstanceInfo{{CoreName: row.CoreName, Provider: "WHATSAPP_WEB", Status: "NEEDS_AUTH", Enabled: true,
		DeviceIdentity: "EDGE_WINDOWS", Locale: "pt-BR"}}
	if err := e.svc.Recover(context.Background(), infos); err != nil {
		t.Fatal(err)
	}
	r := e.row(t, row.ID)
	if r.State != "active" || r.LastStatus == nil || *r.LastStatus != "NEEDS_AUTH" || r.LastStatusAt == nil {
		t.Fatalf("row %+v", r)
	}
	if d := r.LastStatusAt.Sub(e.clk.Now()); d > time.Millisecond || d < -time.Millisecond {
		t.Fatalf("last_status_at %v", r.LastStatusAt)
	}
	if r.DeviceIdentity == nil || *r.DeviceIdentity != "EDGE_WINDOWS" || r.Locale == nil || *r.Locale != "pt-BR" {
		t.Fatalf("identity %v, locale %v", r.DeviceIdentity, r.Locale)
	}
	e.fc.AddInstance(row.CoreName, corev1.Provider_PROVIDER_WHATSAPP_WEB, needsAuth) // the core has it (infos)
	sc := newPairingScript(e.fc)
	sc.send(needsAuth, "QR9", "scan")
	if _, err := e.svc.Pair(context.Background(), e.owner, row.ID, instances.PairInput{Pairing: "qr"}); err != nil {
		t.Fatalf("Pair: %v", err)
	}
}

func TestRecoverLeavesAYoungCreatingRow(t *testing.T) {
	e := newEnv(t)
	row := e.seed(t, testutil.InstanceSeed{State: "creating", CreatedAt: e.clk.Now().Add(-3 * time.Minute)})
	if err := e.svc.Recover(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if st := e.row(t, row.ID).State; st != "creating" {
		t.Fatalf("state %q", st)
	}
}

func TestRecoverLeavesACreatingRowWithARunningSession(t *testing.T) {
	e := newEnv(t)
	holdOpenScript(e.fc)
	v, _, err := e.svc.Create(context.Background(), e.owner, webInput())
	if err != nil {
		t.Fatal(err)
	}
	e.exec(t, `UPDATE instances SET created_at = $2 WHERE id = $1`, v.ID, e.clk.Now().Add(-5*time.Minute))
	if err := e.svc.Recover(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if st := e.row(t, v.ID).State; st != "creating" {
		t.Fatalf("state %q", st)
	}
}

func TestRecoverRetriesARemovingRow(t *testing.T) {
	e := newEnv(t)
	row := e.seed(t, testutil.InstanceSeed{State: "removing"})
	e.fc.AddInstance(row.CoreName, web, connected)
	if err := e.svc.Recover(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if e.rowExists(t, row.ID) {
		t.Fatal("the row remains")
	}
	if reqs := e.fc.RemoveRequests(); len(reqs) != 1 || reqs[0].GetInstanceName() != row.CoreName {
		t.Fatalf("requests %v", reqs)
	}
}

func TestRecoverKeepsARemovingRowWhenTheCoreFails(t *testing.T) {
	e := newEnv(t)
	row := e.seed(t, testutil.InstanceSeed{State: "removing"})
	e.fc.SetUnavailable(true)
	if err := e.svc.Recover(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if st := e.row(t, row.ID).State; st != "removing" {
		t.Fatalf("state %q", st)
	}
}

func TestRecoverIgnoresRowsOfAnotherCore(t *testing.T) {
	e := newEnv(t)
	row := e.seed(t, testutil.InstanceSeed{State: "creating", CoreID: "other", CreatedAt: e.clk.Now().Add(-5 * time.Minute)})
	if err := e.svc.Recover(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if st := e.row(t, row.ID).State; st != "creating" {
		t.Fatalf("state %q", st)
	}
}

func TestRecoverWaitsForThePairingTimeoutPlusOneMinute(t *testing.T) {
	e := newEnv(t)
	svc := instances.NewService(e.pool, nil, e.clk, e.key, "main", 10*time.Minute)
	young := e.seed(t, testutil.InstanceSeed{State: "creating", CreatedAt: e.clk.Now().Add(-10 * time.Minute)})
	old := e.seed(t, testutil.InstanceSeed{State: "creating", CreatedAt: e.clk.Now().Add(-12 * time.Minute)})
	if err := svc.Recover(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if !e.rowExists(t, young.ID) {
		t.Fatal("a row younger than timeout + 1 minute was recovered")
	}
	if e.rowExists(t, old.ID) {
		t.Fatal("a row older than timeout + 1 minute remains")
	}
}
