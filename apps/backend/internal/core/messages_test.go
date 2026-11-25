package core_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/core"
	"calyx/backend/internal/testutil"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

func TestSendText(t *testing.T) {
	fc, c := newClient(t)
	fc.AddInstance("i_a", whatsappWeb, connected)
	res, err := c.SendText(context.Background(), "i_a", "34600111222", "Hello", "")
	if err != nil || res.MessageID != "fake-1" || res.Replayed {
		t.Fatalf("%+v, %v", res, err)
	}
	r := fc.SendRequests()[0]
	if r.GetInstanceName() != "i_a" || r.GetTo() != "34600111222" || r.GetText() != "Hello" {
		t.Errorf("request %v", r)
	}
}

func TestSendTextAddsNoDeadline(t *testing.T) {
	fc, c := newClient(t)
	fc.AddInstance("i_a", whatsappWeb, connected)
	if _, err := c.SendText(context.Background(), "i_a", "1", "x", ""); err != nil {
		t.Fatal(err)
	}
	if fc.Requests(testutil.MethodSendPlainText)[0].HasDeadline {
		t.Error("deadline added")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.SendText(ctx, "i_a", "1", "x", ""); err != nil {
		t.Fatal(err)
	}
	requireTimeout(t, fc.Requests(testutil.MethodSendPlainText)[1], 5*time.Second)
}

func TestSendTextMapping(t *testing.T) {
	fc, c := newClient(t)
	fc.AddInstance("i_a", whatsappWeb, connected)
	for _, tc := range []struct {
		code codes.Code
		st   int
		name string
	}{
		{codes.InvalidArgument, 400, "invalid_request"},
		{codes.FailedPrecondition, 409, "instance_not_ready"},
		{codes.NotFound, 404, "not_found"},
		{codes.Unauthenticated, 502, "core_rejected_token"},
		{codes.Unavailable, 504, "send_outcome_unknown"},
		{codes.DeadlineExceeded, 504, "send_outcome_unknown"},
		{codes.Internal, 504, "send_outcome_unknown"},
		{codes.AlreadyExists, 504, "send_outcome_unknown"},
	} {
		fc.SetError(testutil.MethodSendPlainText, status.Error(tc.code, "'34600111222' is not a valid recipient."))
		_, err := c.SendText(context.Background(), "i_a", "34600111222", "Hello", "")
		requireCode(t, err, tc.st, tc.name)
		if strings.Contains(err.Error(), "34600111222") || strings.Contains(err.Error(), "Hello") {
			t.Errorf("%v leaks: %v", tc.code, err)
		}
	}
	fc.SetError(testutil.MethodSendPlainText, testutil.ReasonError(codes.ResourceExhausted, corev1.ErrorReason_ERROR_REASON_INSTANCE_QUEUE_FULL, nil, "There are already 20 messages waiting on this instance."))
	_, err := c.SendText(context.Background(), "i_a", "1", "x", "")
	requireCode(t, err, 429, "instance_queue_full")
	if e, _ := apierr.As(err); e.Detail != "There are already 20 messages waiting on this instance." {
		t.Errorf("detail %q", e.Detail)
	}
}

func TestASendToADisabledOrUnpairedInstanceIsNotReady(t *testing.T) {
	fc, c := newClient(t)
	fc.AddInstance("i_a", whatsappWeb, needsAuth)
	_, err := c.SendText(context.Background(), "i_a", "1", "x", "")
	requireCode(t, err, 409, "instance_not_ready")
}

func TestASendThatLosesTheConnectionIsAnUnknownOutcome(t *testing.T) {
	fc, c := newClient(t)
	fc.AddInstance("i_a", whatsappWeb, connected)
	fc.SetDelay(testutil.MethodSendPlainText, 10*time.Second)
	done := make(chan error, 1)
	go func() {
		_, err := c.SendText(context.Background(), "i_a", "34600111222", "Hello", "")
		done <- err
	}()
	fc.WaitForRequests(t, testutil.MethodSendPlainText, 1)
	fc.Kill()
	select {
	case err := <-done:
		requireCode(t, err, 504, "send_outcome_unknown")
		if strings.Contains(err.Error(), "34600111222") || strings.Contains(err.Error(), "Hello") {
			t.Errorf("leak: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SendText did not return")
	}
}

func TestASendWhenTheCoreIsDownIsAnUnknownOutcome(t *testing.T) {
	fc, c := newClient(t)
	fc.SetUnavailable(true)
	_, err := c.SendText(context.Background(), "i_a", "1", "x", "")
	requireCode(t, err, 504, "send_outcome_unknown")
}

func TestCreateBulkJobWithOneText(t *testing.T) {
	fc, c := newClient(t)
	fc.AddInstance("i_a", whatsappWeb, connected)
	job, err := c.CreateBulkJob(context.Background(), "i_a", core.BulkInput{
		Recipients: []string{"1", "2", "3"}, Text: "Hi",
		Pacing: &core.BulkPacing{Delay: &core.Range{Min: 3000, Max: 9000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := fc.BulkSameRequests()[0]
	if r.GetInstanceName() != "i_a" || len(r.GetRecipients()) != 3 || r.GetText() != "Hi" ||
		r.GetPacing().GetDelay().GetMin() != 3000 || r.GetPacing().GetDelay().GetMax() != 9000 ||
		r.GetPacing().PauseEvery != nil || r.GetPacing().PauseDuration != nil {
		t.Errorf("request %v", r)
	}
	if job.Status != "QUEUED" || job.Total != 3 || job.Sent != 0 || job.Failed != 0 || job.EstimatedFinishAt != nil {
		t.Errorf("job %+v", job)
	}
	if _, err := uuid.Parse(job.ID); err != nil {
		t.Errorf("id %q", job.ID)
	}
	if time.Since(job.CreatedAt) > time.Minute {
		t.Errorf("created at %v", job.CreatedAt)
	}
	if len(fc.BulkCustomRequests()) != 0 {
		t.Error("custom request sent")
	}
}

func TestCreateBulkJobWithCustomMessages(t *testing.T) {
	fc, c := newClient(t)
	fc.AddInstance("i_a", whatsappWeb, connected)
	job, err := c.CreateBulkJob(context.Background(), "i_a", core.BulkInput{
		Messages: []core.BulkMessage{{To: "1", Text: "a", DelayMs: ptr(int32(1500))}, {To: "2", Text: "b"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := fc.BulkCustomRequests()[0]
	ms := r.GetMessages()
	if len(ms) != 2 || ms[0].DelayMs == nil || *ms[0].DelayMs != 1500 || ms[1].DelayMs != nil || r.Pacing != nil {
		t.Errorf("request %v", r)
	}
	if job.Total != 2 {
		t.Errorf("job %+v", job)
	}
}

func TestCreateBulkJobRefusesAmbiguousInput(t *testing.T) {
	fc, c := newClient(t)
	fc.AddInstance("i_a", whatsappWeb, connected)
	for _, in := range []core.BulkInput{
		{Messages: []core.BulkMessage{{To: "1", Text: "a"}}, Recipients: []string{"1"}, Text: "x"},
		{Messages: []core.BulkMessage{{To: "1", Text: "a"}}, Text: "x"},
		{},
	} {
		_, err := c.CreateBulkJob(context.Background(), "i_a", in)
		requireCode(t, err, 400, "invalid_request")
	}
	if len(fc.BulkSameRequests())+len(fc.BulkCustomRequests()) != 0 {
		t.Error("the core was called")
	}
}

func TestCreateBulkJobDeadlineIs30s(t *testing.T) {
	fc, c := newClient(t)
	fc.AddInstance("i_a", whatsappWeb, connected)
	if _, err := c.CreateBulkJob(context.Background(), "i_a", core.BulkInput{Recipients: []string{"1"}, Text: "x"}); err != nil {
		t.Fatal(err)
	}
	requireTimeout(t, fc.Requests(testutil.MethodSendBulkSameMessage)[0], 30*time.Second)
}

func TestABulkJobWithoutAnswerIsAnUnknownOutcome(t *testing.T) {
	fc, c := newClient(t)
	in := core.BulkInput{Recipients: []string{"1"}, Text: "x"}
	fc.SetUnavailable(true)
	_, err := c.CreateBulkJob(context.Background(), "i_a", in)
	requireCode(t, err, 504, "bulk_outcome_unknown")
	fc.SetUnavailable(false)
	fc.SetDelay(testutil.MethodSendBulkSameMessage, 2*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = c.CreateBulkJob(ctx, "i_a", in)
	requireCode(t, err, 504, "bulk_outcome_unknown")
}

func TestBulkRefusalsAreMapped(t *testing.T) {
	fc, c := newClient(t)
	in := core.BulkInput{Recipients: []string{"1"}, Text: "x"}
	try := func(code codes.Code, msg string) error {
		fc.SetError(testutil.MethodSendBulkSameMessage, status.Error(code, msg))
		_, err := c.CreateBulkJob(context.Background(), "i_a", in)
		return err
	}
	pacing := "The delay range is too low (minimum allowed: 2000). Use a larger range."
	err := try(codes.InvalidArgument, pacing)
	requireCode(t, err, 400, "invalid_request")
	if e, _ := apierr.As(err); e.Detail == pacing {
		t.Errorf("detail %q is the core message", e.Detail)
	}
	err = try(codes.InvalidArgument, "Message 0: '123' is not a valid recipient.")
	if e, _ := apierr.As(err); strings.Contains(e.Detail, "123") {
		t.Errorf("detail %q", e.Detail)
	}
	requireCode(t, try(codes.FailedPrecondition, "x"), 409, "instance_not_ready")
	requireCode(t, try(codes.NotFound, "x"), 404, "not_found")
	requireCode(t, try(codes.Unauthenticated, "x"), 502, "core_rejected_token")
	requireCode(t, try(codes.ResourceExhausted, "x"), 504, "bulk_outcome_unknown") // no reason: not a full queue
	requireCode(t, try(codes.Internal, "x"), 504, "bulk_outcome_unknown")
	requireCode(t, try(codes.AlreadyExists, "x"), 504, "bulk_outcome_unknown")
}

func TestABulkJobOnAnUnpairedInstanceIsNotReady(t *testing.T) {
	fc, c := newClient(t)
	fc.AddInstance("i_a", whatsappWeb, needsAuth)
	_, err := c.CreateBulkJob(context.Background(), "i_a", core.BulkInput{Recipients: []string{"1"}, Text: "x"})
	requireCode(t, err, 409, "instance_not_ready")
}

func TestGetBulkJob(t *testing.T) {
	fc, c := newClient(t)
	t0 := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	fc.PutBulkJob(&corev1.BulkJob{
		Id: "j1", Status: corev1.BulkJobStatus_BULK_JOB_STATUS_RUNNING,
		TotalMessages: 10, SentMessages: 4, FailedMessages: 1, ProgressPercent: 50,
		EstimatedFinishAt: timestamppb.New(t1), CreatedAt: timestamppb.New(t0),
	})
	got, err := c.GetBulkJob(context.Background(), "j1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "j1" || got.Status != "RUNNING" || got.Total != 10 || got.Sent != 4 || got.Failed != 1 ||
		got.ProgressPercent != 50 || got.EstimatedFinishAt == nil || !got.EstimatedFinishAt.Equal(t1) ||
		got.EstimatedFinishAt.Location() != time.UTC || !got.CreatedAt.Equal(t0) || got.CreatedAt.Location() != time.UTC {
		t.Errorf("job %+v", got)
	}
	requireTimeout(t, fc.Requests(testutil.MethodGetBulkJob)[0], 10*time.Second)
}

func TestGetBulkJobUnknownIsNotFound(t *testing.T) {
	_, c := newClient(t)
	_, err := c.GetBulkJob(context.Background(), "nope")
	requireCode(t, err, 404, "not_found")
}

func TestGetBulkJobWhenTheCoreIsDownIsCoreUnavailable(t *testing.T) {
	fc, c := newClient(t)
	fc.SetUnavailable(true)
	_, err := c.GetBulkJob(context.Background(), "j1")
	requireCode(t, err, 503, "core_unavailable")
}

func TestGetBulkJobs(t *testing.T) {
	fc, c := newClient(t)
	fc.PutBulkJob(&corev1.BulkJob{Id: "j1", Status: corev1.BulkJobStatus_BULK_JOB_STATUS_RUNNING, TotalMessages: 10, SentMessages: 4})
	fc.PutBulkJob(&corev1.BulkJob{Id: "j2", Status: corev1.BulkJobStatus_BULK_JOB_STATUS_COMPLETED, TotalMessages: 2, SentMessages: 2})
	got, err := c.GetBulkJobs(context.Background(), []string{"j2", "gone", "j1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "j1" || got[0].Sent != 4 || got[1].ID != "j2" || got[1].Status != "COMPLETED" {
		t.Errorf("jobs %+v", got)
	}
	reqs := fc.GetBulkJobsRequests()
	if len(reqs) != 1 || len(reqs[0].GetJobIds()) != 3 {
		t.Errorf("requests %v", reqs)
	}
	requireTimeout(t, fc.Requests(testutil.MethodGetBulkJobs)[0], 10*time.Second)
}

func TestGetBulkJobsWhenTheCoreIsDownIsCoreUnavailable(t *testing.T) {
	fc, c := newClient(t)
	fc.SetUnavailable(true)
	_, err := c.GetBulkJobs(context.Background(), []string{"j1"})
	requireCode(t, err, 503, "core_unavailable")
}

func TestGetBulkJobsMapsAnInvalidArgument(t *testing.T) {
	fc, c := newClient(t)
	fc.SetError(testutil.MethodGetBulkJobs, status.Error(codes.InvalidArgument, "too many ids"))
	_, err := c.GetBulkJobs(context.Background(), []string{"j1"})
	requireCode(t, err, 400, apierr.CodeInvalidRequest)
}

func TestCancelBulkJob(t *testing.T) {
	fc, c := newClient(t)
	now := timestamppb.Now()
	fc.PutBulkJob(&corev1.BulkJob{Id: "q", Status: corev1.BulkJobStatus_BULK_JOB_STATUS_QUEUED, CreatedAt: now})
	fc.PutBulkJob(&corev1.BulkJob{Id: "d", Status: corev1.BulkJobStatus_BULK_JOB_STATUS_COMPLETED, CreatedAt: now})
	got, err := c.CancelBulkJob(context.Background(), "q")
	if err != nil || got.Status != "CANCELLED" {
		t.Errorf("%+v, %v", got, err)
	}
	got, err = c.CancelBulkJob(context.Background(), "d")
	if err != nil || got.Status != "COMPLETED" {
		t.Errorf("%+v, %v", got, err)
	}
	requireTimeout(t, fc.Requests(testutil.MethodCancelBulkJob)[0], 10*time.Second)
}

func TestCancelBulkJobUnknownIsNotFound(t *testing.T) {
	_, c := newClient(t)
	_, err := c.CancelBulkJob(context.Background(), "nope")
	requireCode(t, err, 404, "not_found")
}

func TestTheKeyReachesTheCoreOnTheThreeRequests(t *testing.T) {
	fc, c := newClient(t)
	fc.AddInstance("i_a", whatsappWeb, connected)
	ctx := context.Background()
	if _, err := c.SendText(ctx, "i_a", "1", "x", "k-send"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateBulkJob(ctx, "i_a", core.BulkInput{Recipients: []string{"1"}, Text: "x", Key: "k-same"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateBulkJob(ctx, "i_a", core.BulkInput{Messages: []core.BulkMessage{{To: "1", Text: "x"}}, Key: "k-custom"}); err != nil {
		t.Fatal(err)
	}
	if k := fc.SendRequests()[0].IdempotencyKey; k == nil || *k != "k-send" {
		t.Errorf("send key %v", k)
	}
	if k := fc.BulkSameRequests()[0].IdempotencyKey; k == nil || *k != "k-same" {
		t.Errorf("bulk same key %v", k)
	}
	if k := fc.BulkCustomRequests()[0].IdempotencyKey; k == nil || *k != "k-custom" {
		t.Errorf("bulk custom key %v", k)
	}
}

func TestAnEmptyKeyLeavesTheFieldNil(t *testing.T) {
	fc, c := newClient(t)
	fc.AddInstance("i_a", whatsappWeb, connected)
	ctx := context.Background()
	if _, err := c.SendText(ctx, "i_a", "1", "x", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateBulkJob(ctx, "i_a", core.BulkInput{Recipients: []string{"1"}, Text: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateBulkJob(ctx, "i_a", core.BulkInput{Messages: []core.BulkMessage{{To: "1", Text: "x"}}}); err != nil {
		t.Fatal(err)
	}
	if fc.SendRequests()[0].IdempotencyKey != nil || fc.BulkSameRequests()[0].IdempotencyKey != nil ||
		fc.BulkCustomRequests()[0].IdempotencyKey != nil {
		t.Error("an empty key reached the core")
	}
}

func TestAReplayedSendAndBulkJobAreMarked(t *testing.T) {
	fc, c := newClient(t)
	fc.AddInstance("i_a", whatsappWeb, connected)
	ctx := context.Background()
	first, _ := c.SendText(ctx, "i_a", "1", "x", "k1")
	again, err := c.SendText(ctx, "i_a", "1", "x", "k1")
	if err != nil || first.Replayed || !again.Replayed || again.MessageID != first.MessageID {
		t.Fatalf("%+v %+v %v", first, again, err)
	}
	in := core.BulkInput{Recipients: []string{"1"}, Text: "x", Key: "k2"}
	j1, _ := c.CreateBulkJob(ctx, "i_a", in)
	j2, err := c.CreateBulkJob(ctx, "i_a", in)
	if err != nil || j1.Replayed || !j2.Replayed || j2.ID != j1.ID {
		t.Fatalf("%+v %+v %v", j1, j2, err)
	}
}

func TestTheFourConflictReasonsMapOverTheWire(t *testing.T) {
	fc, c := newClient(t)
	fc.AddInstance("i_a", whatsappWeb, connected)
	for _, tc := range []struct {
		code   codes.Code
		reason corev1.ErrorReason
		want   string
	}{
		{codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT, string(apierr.CodeIdempotencyKeyConflict)},
		{codes.Aborted, corev1.ErrorReason_ERROR_REASON_SEND_IN_PROGRESS, string(apierr.CodeSendInProgress)},
		{codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_SEND_OUTCOME_UNKNOWN, string(apierr.CodeIdempotencyKeyOutcomeUnknown)},
		{codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_DUPLICATE_MESSAGE, string(apierr.CodeDuplicateMessage)},
	} {
		fc.SetError(testutil.MethodSendPlainText, testutil.ReasonError(tc.code, tc.reason, nil, "Refused."))
		_, err := c.SendText(context.Background(), "i_a", "1", "x", "k")
		requireCode(t, err, 409, tc.want)
		fc.SetError(testutil.MethodSendBulkSameMessage, testutil.ReasonError(tc.code, tc.reason, nil, "Refused."))
		_, err = c.CreateBulkJob(context.Background(), "i_a", core.BulkInput{Recipients: []string{"1"}, Text: "x", Key: "k"})
		requireCode(t, err, 409, tc.want)
	}
}

func TestASendErrorKeepsTheRawCodeAndTheMappedError(t *testing.T) {
	fc, c := newClient(t)
	fc.AddInstance("i_a", whatsappWeb, connected)
	for _, tc := range []struct {
		code        codes.Code
		unavailable bool
	}{
		{codes.Unavailable, true},
		{codes.DeadlineExceeded, false},
		{codes.FailedPrecondition, false},
	} {
		fc.SetError(testutil.MethodSendPlainText, status.Error(tc.code, "failed for 34600111222"))
		_, err := c.SendText(context.Background(), "i_a", "34600111222", "Hello", "")
		if got := core.IsUnavailable(err); got != tc.unavailable {
			t.Errorf("%v: IsUnavailable = %v", tc.code, got)
		}
		if tc.code == codes.Unavailable {
			if ae, ok := apierr.As(err); !ok || ae.Code != apierr.CodeSendOutcomeUnknown {
				t.Errorf("%v: apierr.As = %v, %v", tc.code, ae, ok)
			}
			if got := core.Loggable(err); fmt.Sprint(got) != fmt.Sprint(err) {
				t.Errorf("Loggable changed: %v", got)
			}
		}
	}
	if core.IsUnavailable(nil) || core.IsUnavailable(errors.New("x")) {
		t.Error("IsUnavailable is true for a plain error")
	}
}
