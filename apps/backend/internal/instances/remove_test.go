package instances_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"calyx/backend/internal/instances"
	"calyx/backend/internal/testutil"
)

func TestRemoveActiveInstance(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	e.addWebhook(t, row.ID, "https://a.example/1", "connection.update", nil)
	e.exec(t, `INSERT INTO bulk_jobs (id, instance_id, organization_id, client_id, created_by) VALUES ($1, $2, $3, $4, $5)`,
		uuid.New(), row.ID, e.tn.OrganizationID, e.tn.ClientA1, e.tn.OwnerID)
	removed, err := e.svc.Remove(context.Background(), e.owner, row.ID)
	if err != nil || !removed {
		t.Fatalf("removed %v, %v", removed, err)
	}
	reqs := e.fc.RemoveRequests()
	if len(reqs) != 1 || reqs[0].GetInstanceName() != row.CoreName {
		t.Fatalf("requests %v", reqs)
	}
	if e.rowExists(t, row.ID) || e.count(t, "webhooks") != 0 || e.count(t, "bulk_jobs") != 0 {
		t.Fatal("rows remain")
	}
}

func TestRemoveWhenTheCoreNoLongerHasIt(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	e.fc.SetError("RemoveInstance", status.Error(codes.NotFound, "gone"))
	removed, err := e.svc.Remove(context.Background(), e.owner, row.ID)
	if err != nil || !removed || e.rowExists(t, row.ID) {
		t.Fatalf("removed %v, %v", removed, err)
	}
}

func TestRemoveWhenTheCoreIsUnreachable(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	e.fc.SetUnavailable(true)
	removed, err := e.svc.Remove(context.Background(), e.owner, row.ID)
	if err != nil || removed {
		t.Fatalf("removed %v, %v", removed, err)
	}
	if st := e.row(t, row.ID).State; st != "removing" {
		t.Fatalf("state %q", st)
	}
}

func TestRemoveWhenTheCoreRefuses(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	e.fc.SetError("RemoveInstance", status.Error(codes.FailedPrecondition, "being paired"))
	_, err := e.svc.Remove(context.Background(), e.owner, row.ID)
	requireCode(t, err, 409, "conflict")
	if st := e.row(t, row.ID).State; st != "active" {
		t.Fatalf("state %q", st)
	}
}

func TestRemoveMissingInstanceDoesNotCallTheCore(t *testing.T) {
	e := newEnv(t)
	row := e.seed(t, testutil.InstanceSeed{State: "missing"})
	removed, err := e.svc.Remove(context.Background(), e.owner, row.ID)
	if err != nil || !removed || e.rowExists(t, row.ID) {
		t.Fatalf("removed %v, %v", removed, err)
	}
	if len(e.fc.RemoveRequests()) != 0 {
		t.Fatal("a core request was sent")
	}
}

func TestRemoveCreatingInstanceIsRefused(t *testing.T) {
	e := newEnv(t)
	row := e.seed(t, testutil.InstanceSeed{State: "creating"})
	_, err := e.svc.Remove(context.Background(), e.owner, row.ID)
	requireCode(t, err, 409, "pairing_in_progress")
	if st := e.row(t, row.ID).State; st != "creating" || len(e.fc.RemoveRequests()) != 0 {
		t.Fatal("the row changed or the core was called")
	}
}

func TestRemoveDuringARePairIsRefused(t *testing.T) {
	e := newEnv(t)
	v := e.createPaired(t, webInput())
	sc := newPairingScript(e.fc)
	sc.send(needsAuth, "QR9", "scan")
	if _, err := e.svc.Pair(context.Background(), e.owner, v.ID, instances.PairInput{Pairing: "qr"}); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Remove(context.Background(), e.owner, v.ID)
	requireCode(t, err, 409, "pairing_in_progress")
	if len(e.fc.RemoveRequests()) != 0 {
		t.Fatal("the core was called")
	}
}

func TestRemoveARemovingRowAgain(t *testing.T) {
	e := newEnv(t)
	row := e.seed(t, testutil.InstanceSeed{State: "removing"})
	e.fc.AddInstance(row.CoreName, web, connected)
	removed, err := e.svc.Remove(context.Background(), e.owner, row.ID)
	if err != nil || !removed || e.rowExists(t, row.ID) {
		t.Fatalf("removed %v, %v", removed, err)
	}
}

func TestRemoveOutOfScopeIsNotFound(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	_, err := e.svc.Remove(context.Background(), e.userA2, row.ID)
	requireCode(t, err, 404, "not_found")
	if len(e.fc.RemoveRequests()) != 0 || e.row(t, row.ID).State != "active" {
		t.Fatal("the core was called or the row changed")
	}
}
