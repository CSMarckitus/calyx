package bulk_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/bulk"
	"calyx/backend/internal/core"
	"calyx/backend/internal/tenancy"
	"calyx/backend/internal/testutil"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

func TestCreateSameMessageStoresOwnership(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	job, err := e.svc.Create(context.Background(), e.userA1, row.ID, bulk.Input{Recipients: recipients(3), Text: ptr("Reminder")})
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "QUEUED" || job.Total != 3 || job.InstanceID != row.ID || job.ID == "" {
		t.Fatalf("job %+v", job)
	}
	var inst, org, client, by uuid.UUID
	err = e.pool.QueryRow(context.Background(),
		`SELECT instance_id, organization_id, client_id, created_by FROM bulk_jobs WHERE id = $1`, job.ID).Scan(&inst, &org, &client, &by)
	if err != nil {
		t.Fatal(err)
	}
	if inst != row.ID || org != e.tn.OrganizationID || client != e.tn.ClientA1 || by != e.tn.UserA1 {
		t.Fatalf("ownership %v %v %v %v", inst, org, client, by)
	}
	req := e.fc.BulkSameRequests()[0]
	if req.GetInstanceName() != row.CoreName || len(req.GetRecipients()) != 3 || req.GetText() != "Reminder" {
		t.Fatalf("request %v", req)
	}
}

func TestCreateCustomMessagesPassesDelaysAndPacing(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	delay := int32(3000)
	in := bulk.Input{
		Messages: []core.BulkMessage{{To: "34600111222", Text: "a", DelayMs: &delay}, {To: "34600111333", Text: "b"}},
		Pacing:   &core.BulkPacing{Delay: &core.Range{Min: 4000, Max: 9000}},
	}
	if _, err := e.svc.Create(context.Background(), e.owner, row.ID, in); err != nil {
		t.Fatal(err)
	}
	req := e.fc.BulkCustomRequests()[0]
	if req.GetMessages()[0].DelayMs == nil || req.GetMessages()[0].GetDelayMs() != 3000 || req.GetMessages()[1].DelayMs != nil {
		t.Fatalf("messages %v", req.GetMessages())
	}
	d := req.GetPacing().GetDelay()
	if d.GetMin() != 4000 || d.GetMax() != 9000 || req.GetPacing().GetPauseEvery() != nil {
		t.Fatalf("pacing %v", req.GetPacing())
	}
	if e.count(t) != 1 {
		t.Fatal("no bulk_jobs row")
	}
}

func TestCreateAcceptsExactly5000(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	if _, err := e.svc.Create(context.Background(), e.owner, row.ID, bulk.Input{Recipients: recipients(bulk.MaxMessages), Text: ptr("x")}); err != nil {
		t.Fatal(err)
	}
	if len(e.fc.BulkSameRequests()) != 1 {
		t.Fatal("no core request")
	}
}

func TestCreateRefusesMoreThan5000(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	_, err := e.svc.Create(context.Background(), e.owner, row.ID, bulk.Input{Recipients: recipients(bulk.MaxMessages + 1), Text: ptr("x")})
	requireCode(t, err, 413, "too_many_messages")
	_, err = e.svc.Create(context.Background(), e.owner, row.ID, bulk.Input{Messages: make([]core.BulkMessage, bulk.MaxMessages+1)})
	requireCode(t, err, 413, "too_many_messages")
	if len(e.fc.BulkSameRequests())+len(e.fc.BulkCustomRequests()) != 0 || e.count(t) != 0 {
		t.Fatal("a core request or a row exists")
	}
}

func TestCreateNeedsExactlyOneForm(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	inputs := []bulk.Input{
		{Recipients: recipients(1), Text: ptr("x"), Messages: []core.BulkMessage{{To: "1", Text: "x"}}},
		{},
		{Text: ptr("x")}, // recipients 0
	}
	for i, in := range inputs {
		_, err := e.svc.Create(context.Background(), e.owner, row.ID, in)
		if ae, ok := apierrAs(err); !ok || ae.Status != 400 || ae.Code != "invalid_request" {
			t.Fatalf("input %d: %v", i, err)
		}
	}
	if len(e.fc.BulkSameRequests())+len(e.fc.BulkCustomRequests()) != 0 {
		t.Fatal("a core request was sent")
	}
}

func TestLowPacingIsRefusedByTheCore(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	e.fc.SetError("SendBulkSameMessage", status.Error(codes.InvalidArgument, "The delay range is too low (minimum allowed: 2000)."))
	_, err := e.svc.Create(context.Background(), e.owner, row.ID, bulk.Input{
		Recipients: recipients(2), Text: ptr("x"),
		Pacing: &core.BulkPacing{Delay: &core.Range{Min: 1, Max: 2}},
	})
	requireCode(t, err, 400, "invalid_request")
	if ae, _ := apierrAs(err); ae.Detail != "The core refused a recipient or a pacing value of the bulk job." {
		t.Fatalf("detail %q", ae.Detail)
	}
	// The backend passed the pacing unchanged.
	if d := e.fc.BulkSameRequests()[0].GetPacing().GetDelay(); d.GetMin() != 1 || d.GetMax() != 2 {
		t.Fatalf("pacing %v", d)
	}
	if e.count(t) != 0 {
		t.Fatal("a row exists")
	}
}

func TestBulkOutcomeUnknownStoresNothing(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	e.fc.SetUnavailable(true)
	_, err := e.svc.Create(context.Background(), e.owner, row.ID, bulk.Input{Recipients: recipients(2), Text: ptr("x")})
	requireCode(t, err, 504, "bulk_outcome_unknown")
	if e.count(t) != 0 {
		t.Fatal("a row exists")
	}
}

func TestCreateOnAnInstanceThatNeedsAuth(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	e.fc.SetStatus(row.CoreName, corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH)
	_, err := e.svc.Create(context.Background(), e.owner, row.ID, bulk.Input{Recipients: recipients(2), Text: ptr("x")})
	requireCode(t, err, 409, "instance_not_ready")
	e.expectHook(t, row.ID)
	if e.count(t) != 0 {
		t.Fatal("a row exists")
	}
}

func TestCreateWhenTheCoreLostTheInstance(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	e.fc.DropInstance(row.CoreName)
	_, err := e.svc.Create(context.Background(), e.owner, row.ID, bulk.Input{Recipients: recipients(2), Text: ptr("x")})
	requireCode(t, err, 404, "not_found")
	e.expectNoHook(t)
	if e.count(t) != 0 {
		t.Fatal("a row exists")
	}
}

func TestOtherCoreErrorsAreBulkOutcomeUnknown(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	e.fc.SetError("SendBulkSameMessage", status.Error(codes.Internal, "x"))
	_, err := e.svc.Create(context.Background(), e.owner, row.ID, bulk.Input{Recipients: recipients(2), Text: ptr("x")})
	requireCode(t, err, 504, "bulk_outcome_unknown")
	if e.count(t) != 0 {
		t.Fatal("a row exists")
	}
}

func TestCreateOnARowThatIsNotActive(t *testing.T) {
	e := newEnv(t)
	row := testutil.SeedInstance(t, e.pool, testutil.InstanceSeed{OrganizationID: e.tn.OrganizationID, ClientID: e.tn.ClientA1, State: "missing"})
	_, err := e.svc.Create(context.Background(), e.owner, row.ID, bulk.Input{Recipients: recipients(2), Text: ptr("x")})
	requireCode(t, err, 409, "instance_not_connected")
	if len(e.fc.BulkSameRequests()) != 0 {
		t.Fatal("a core request was sent")
	}
}

func TestCreateOfAnotherClientsInstanceIsNotFound(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	_, err := e.svc.Create(context.Background(), e.userA2, row.ID, bulk.Input{Recipients: recipients(2), Text: ptr("x")})
	requireCode(t, err, 404, "not_found")
	if len(e.fc.BulkSameRequests()) != 0 {
		t.Fatal("a core request was sent")
	}
}

func TestGetAndCancelLoadTheRowWithTheScopeFirst(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	job, err := e.svc.Create(context.Background(), e.userA1, row.ID, bulk.Input{Recipients: recipients(2), Text: ptr("x")})
	if err != nil {
		t.Fatal(err)
	}
	other := testutil.SeedTenant(t, e.pool, testutil.TenantOptions{Name: "Other"})
	otherOwner := tenancy.Principal{UserID: other.OwnerID, OrganizationID: other.OrganizationID, Role: tenancy.RoleOwner}
	id := uuid.MustParse(job.ID)
	for name, p := range map[string]tenancy.Principal{"userA2": e.userA2, "other owner": otherOwner} {
		_, err := e.svc.Get(context.Background(), p, id)
		if ae, ok := apierrAs(err); !ok || ae.Status != 404 {
			t.Fatalf("%s Get: %v", name, err)
		}
		_, err = e.svc.Cancel(context.Background(), p, id)
		if ae, ok := apierrAs(err); !ok || ae.Status != 404 {
			t.Fatalf("%s Cancel: %v", name, err)
		}
	}
	if len(e.fc.GetBulkRequests()) != 0 || len(e.fc.GetBulkJobsRequests()) != 0 || len(e.fc.CancelBulkRequests()) != 0 {
		t.Fatal("the core was called")
	}
	got, err := e.svc.Get(context.Background(), e.owner, id)
	if err != nil || got.ID != job.ID || got.InstanceID != row.ID {
		t.Fatalf("Get: %+v, %v", got, err)
	}
	cancelled, err := e.svc.Cancel(context.Background(), e.userA1, id)
	if err != nil || cancelled.Status != "CANCELLED" || len(e.fc.CancelBulkRequests()) != 1 {
		t.Fatalf("Cancel: %+v, %v", cancelled, err)
	}
}

func TestGetOfAnUnknownJobIsNotFound(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.Get(context.Background(), e.owner, uuid.New())
	requireCode(t, err, 404, "not_found")
	if len(e.fc.GetBulkRequests()) != 0 || len(e.fc.GetBulkJobsRequests()) != 0 {
		t.Fatal("the core was called")
	}
}

// putJob adds a bulk_jobs row for an instance and, when status is not UNSPECIFIED, the core's job.
func (e *env) putJob(t *testing.T, instanceID, client uuid.UUID, status corev1.BulkJobStatus) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := e.pool.Exec(context.Background(),
		`INSERT INTO bulk_jobs (id, instance_id, organization_id, client_id, created_by) VALUES ($1, $2, $3, $4, $5)`,
		id, instanceID, e.tn.OrganizationID, client, e.tn.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if status != corev1.BulkJobStatus_BULK_JOB_STATUS_UNSPECIFIED {
		e.fc.PutBulkJob(&corev1.BulkJob{Id: id.String(), Status: status, TotalMessages: 1, CreatedAt: timestamppb.Now()})
	}
	return id
}

func TestGetOfAFailedJobStartsTheStatusCheck(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	id := e.putJob(t, row.ID, e.tn.ClientA1, corev1.BulkJobStatus_BULK_JOB_STATUS_FAILED)
	job, err := e.svc.Get(context.Background(), e.owner, id)
	if err != nil || job.Status != "FAILED" {
		t.Fatalf("%+v, %v", job, err)
	}
	e.expectHook(t, row.ID)
}

func TestGetOfAJobTheCoreNoLongerHasIsNotFound(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	id := e.putJob(t, row.ID, e.tn.ClientA1, corev1.BulkJobStatus_BULK_JOB_STATUS_UNSPECIFIED)
	_, err := e.svc.Get(context.Background(), e.owner, id)
	requireCode(t, err, 404, "not_found")
	_, err = e.svc.Cancel(context.Background(), e.owner, id)
	requireCode(t, err, 404, "not_found")
}

func TestListIsNewestFirstAndAtMost50(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	var last string
	for i := 0; i < 55; i++ {
		job, err := e.svc.Create(context.Background(), e.owner, row.ID, bulk.Input{Recipients: recipients(1), Text: ptr("x")})
		if err != nil {
			t.Fatal(err)
		}
		last = job.ID
	}
	jobs, err := e.svc.ListForInstance(context.Background(), e.owner, row.ID)
	if err != nil || len(jobs) != 50 {
		t.Fatalf("%d jobs, %v", len(jobs), err)
	}
	if jobs[0].ID != last {
		t.Fatal("the first job is not the last created")
	}
	for _, j := range jobs {
		if j.InstanceID != row.ID {
			t.Fatal("a job has no instance id")
		}
	}
}

func TestListLeavesOutJobsTheCoreNoLongerHas(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	e.putJob(t, row.ID, e.tn.ClientA1, corev1.BulkJobStatus_BULK_JOB_STATUS_QUEUED)
	e.putJob(t, row.ID, e.tn.ClientA1, corev1.BulkJobStatus_BULK_JOB_STATUS_UNSPECIFIED)
	e.putJob(t, row.ID, e.tn.ClientA1, corev1.BulkJobStatus_BULK_JOB_STATUS_RUNNING)
	jobs, err := e.svc.ListForInstance(context.Background(), e.owner, row.ID)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("%d jobs, %v", len(jobs), err)
	}
}

func TestListStartsOneStatusCheckForFailedJobs(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	e.putJob(t, row.ID, e.tn.ClientA1, corev1.BulkJobStatus_BULK_JOB_STATUS_FAILED)
	e.putJob(t, row.ID, e.tn.ClientA1, corev1.BulkJobStatus_BULK_JOB_STATUS_FAILED)
	if _, err := e.svc.ListForInstance(context.Background(), e.owner, row.ID); err != nil {
		t.Fatal(err)
	}
	e.expectHook(t, row.ID)
}

func TestListOfAnotherClientsInstanceIsNotFound(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	_, err := e.svc.ListForInstance(context.Background(), e.userA2, row.ID)
	requireCode(t, err, 404, "not_found")
	if len(e.fc.GetBulkRequests()) != 0 || len(e.fc.GetBulkJobsRequests()) != 0 {
		t.Fatal("the core was called")
	}
}

func TestListWithNoRowsMakesNoCoreCall(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	jobs, err := e.svc.ListForInstance(context.Background(), e.owner, row.ID)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("%d jobs, %v", len(jobs), err)
	}
	if len(e.fc.GetBulkRequests()) != 0 || len(e.fc.GetBulkJobsRequests()) != 0 {
		t.Fatal("the core was called")
	}
}

func TestListWithAnOlderCoreIsAnInternalError(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	e.putJob(t, row.ID, e.tn.ClientA1, corev1.BulkJobStatus_BULK_JOB_STATUS_QUEUED)
	e.fc.SetError(testutil.MethodGetBulkJobs, status.Error(codes.Unimplemented, "unknown method GetBulkJobs"))
	_, err := e.svc.ListForInstance(context.Background(), e.owner, row.ID)
	requireCode(t, err, 500, "internal")
}

func TestListWhenTheCoreIsUnreachable(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	e.putJob(t, row.ID, e.tn.ClientA1, corev1.BulkJobStatus_BULK_JOB_STATUS_QUEUED)
	e.fc.SetUnavailable(true)
	_, err := e.svc.ListForInstance(context.Background(), e.owner, row.ID)
	requireCode(t, err, 503, "core_unavailable")
}

func TestListStopsAfterTheTotalLimit(t *testing.T) {
	bulk.SetListTimeout(t, 500*time.Millisecond)
	e := newEnv(t)
	row := e.instance(t)
	e.putJob(t, row.ID, e.tn.ClientA1, corev1.BulkJobStatus_BULK_JOB_STATUS_QUEUED)
	e.fc.SetDelay(testutil.MethodGetBulkJobs, 2*time.Second)
	start := time.Now()
	_, err := e.svc.ListForInstance(context.Background(), e.owner, row.ID)
	requireCode(t, err, 503, "core_unavailable")
	if time.Since(start) > time.Second {
		t.Fatalf("the list took %v", time.Since(start))
	}
}

func TestCreateStoresOwnershipAfterTheRequestContextEnds(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	tx, err := e.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), `SELECT 1 FROM instances WHERE id = $1 FOR UPDATE`, row.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		job bulk.Job
		err error
	}
	done := make(chan result, 1)
	go func() {
		j, err := e.svc.Create(ctx, e.owner, row.ID, bulk.Input{Recipients: recipients(2), Text: ptr("x")})
		done <- result{j, err}
	}()
	e.fc.WaitForRequests(t, "SendBulkSameMessage", 1)
	time.Sleep(200 * time.Millisecond) // the core has answered; the insert waits for the lock
	cancel()
	time.Sleep(50 * time.Millisecond)
	_ = tx.Commit(context.Background())
	r := <-done
	if r.err != nil {
		t.Fatalf("Create: %v", r.err)
	}
	if e.count(t) != 1 {
		t.Fatal("the core started the job but no ownership row was stored")
	}
}

func TestOwnershipInsertFailureIsBulkOutcomeUnknown(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	// The core accepts the job, then the ownership insert fails.
	if _, err := e.pool.Exec(context.Background(), `ALTER TABLE bulk_jobs RENAME TO bulk_jobs_gone`); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Create(context.Background(), e.owner, row.ID, bulk.Input{Recipients: recipients(2), Text: ptr("x")})
	requireCode(t, err, 504, "bulk_outcome_unknown")
	ae, _ := apierr.As(err)
	want := "The core started the job, but the backend could not store it. If you sent an Idempotency-Key, " +
		"retry with the same key: the retry stores the job. Without a key, do not start it again."
	if ae.Detail != want {
		t.Fatalf("detail = %q", ae.Detail)
	}
}

func keyed(key string) bulk.Input {
	return bulk.Input{Recipients: recipients(2), Text: ptr("x"), Key: key}
}

func TestAReplayWithAnExistingRowIsMarkedAndKeepsOneRow(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	first, err := e.svc.Create(context.Background(), e.owner, row.ID, keyed("k1"))
	if err != nil || first.Replayed {
		t.Fatalf("first: %+v, %v", first, err)
	}
	again, err := e.svc.Create(context.Background(), e.owner, row.ID, keyed("k1"))
	if err != nil || !again.Replayed || again.ID != first.ID {
		t.Fatalf("replay: %+v, %v", again, err)
	}
	if e.count(t) != 1 {
		t.Fatalf("%d rows, want 1", e.count(t))
	}
}

func TestAReplayWithoutARowStoresTheRow(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `ALTER TABLE bulk_jobs RENAME TO bulk_jobs_gone`); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Create(ctx, e.owner, row.ID, keyed("k1"))
	requireCode(t, err, 504, "bulk_outcome_unknown")
	if _, err := e.pool.Exec(ctx, `ALTER TABLE bulk_jobs_gone RENAME TO bulk_jobs`); err != nil {
		t.Fatal(err)
	}
	job, err := e.svc.Create(ctx, e.owner, row.ID, keyed("k1"))
	if err != nil || !job.Replayed {
		t.Fatalf("replay: %+v, %v", job, err)
	}
	if e.count(t) != 1 {
		t.Fatalf("%d rows, want 1", e.count(t))
	}
}

func TestAReplayThatFindsARowOfAnotherInstanceIsInternal(t *testing.T) {
	e := newEnv(t)
	x, y := e.instance(t), e.instance(t)
	ctx := context.Background()
	job, err := e.svc.Create(ctx, e.owner, x.ID, keyed("k1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE bulk_jobs SET instance_id = $1 WHERE id = $2`, y.ID, job.ID); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Create(ctx, e.owner, x.ID, keyed("k1"))
	ae, ok := apierr.As(err)
	if !ok || ae.Status != 500 {
		t.Fatalf("err = %v, want 500", err)
	}
}

func TestTheKeyReachesTheCore(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	if _, err := e.svc.Create(context.Background(), e.owner, row.ID, keyed("k-core")); err != nil {
		t.Fatal(err)
	}
	if got := e.fc.BulkSameRequests()[0].GetIdempotencyKey(); got != "k-core" {
		t.Fatalf("the core got the key %q", got)
	}
}

func TestAWrongKeyIsRefusedBeforeTheCore(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	_, err := e.svc.Create(context.Background(), e.owner, row.ID, keyed("bad key"))
	requireCode(t, err, 400, "invalid_request")
	if n := len(e.fc.BulkSameRequests()); n != 0 {
		t.Fatalf("the core received %d requests", n)
	}
}

func TestListMakesOneCoreCallAndKeepsTheOrder(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	const n = 20
	var ids []string
	for i := 0; i < n; i++ {
		job, err := e.svc.Create(context.Background(), e.owner, row.ID, bulk.Input{Recipients: recipients(1), Text: ptr("x")})
		if err != nil {
			t.Fatal(err)
		}
		ids = append([]string{job.ID}, ids...) // newest first
	}
	jobs, err := e.svc.ListForInstance(context.Background(), e.owner, row.ID)
	if err != nil || len(jobs) != n {
		t.Fatalf("%d jobs, %v", len(jobs), err)
	}
	if got := len(e.fc.GetBulkJobsRequests()); got != 1 {
		t.Fatalf("%d GetBulkJobs calls, want 1", got)
	}
	if got := len(e.fc.GetBulkRequests()); got != 0 {
		t.Fatalf("%d GetBulkJob calls, want 0", got)
	}
	for i, j := range jobs {
		if j.ID != ids[i] {
			t.Fatalf("job %d is %s, want %s", i, j.ID, ids[i])
		}
	}
}
