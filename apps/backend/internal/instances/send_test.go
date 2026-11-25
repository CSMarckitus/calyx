package instances_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"calyx/backend/internal/instances"
	"calyx/backend/internal/testutil"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// hookRecorder records the instance ids that the status check hook receives.
func (e *env) hookRecorder() chan uuid.UUID {
	ch := make(chan uuid.UUID, 8)
	e.svc.SetStatusCheck(func(_ context.Context, id uuid.UUID) { ch <- id })
	return ch
}

func expectHook(t *testing.T, ch chan uuid.UUID, id uuid.UUID) {
	t.Helper()
	select {
	case got := <-ch:
		if got != id {
			t.Fatalf("hook got %v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the hook was not called")
	}
	select {
	case <-ch:
		t.Fatal("the hook was called twice")
	case <-time.After(50 * time.Millisecond):
	}
}

func expectNoHook(t *testing.T, ch chan uuid.UUID) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal("the hook was called")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSendReturnsTheMessageID(t *testing.T) {
	e := newEnv(t)
	hook := e.hookRecorder()
	row := e.activeRow(t)
	id, err := e.svc.Send(context.Background(), e.owner, row.ID, "34600111222", "Hello", "")
	if err != nil || id.MessageID != "fake-1" || id.Replayed {
		t.Fatalf("%+v, %v", id, err)
	}
	reqs := e.fc.SendRequests()
	if len(reqs) != 1 || reqs[0].GetInstanceName() != row.CoreName || reqs[0].GetTo() != "34600111222" || reqs[0].GetText() != "Hello" {
		t.Fatalf("requests %v", reqs)
	}
	expectNoHook(t, hook)
}

func TestSendIsRefusedWhenTheInstanceIsNotConnected(t *testing.T) {
	e := newEnv(t)
	hook := e.hookRecorder()
	row := e.activeRow(t)
	e.fc.SetStatus(row.CoreName, needsAuth)
	_, err := e.svc.Send(context.Background(), e.owner, row.ID, "34600111222", "Hello", "")
	requireCode(t, err, 409, "instance_not_connected")
	if len(e.fc.SendRequests()) != 0 {
		t.Fatal("a send request was made")
	}
	expectHook(t, hook, row.ID)
}

func TestSendWhenTheCoreSaysTheInstanceIsNotReady(t *testing.T) {
	e := newEnv(t)
	hook := e.hookRecorder()
	row := e.activeRow(t)
	e.fc.OnSendPlainText(func(context.Context, *corev1.SendPlainTextRequest) (*corev1.SendPlainTextResponse, error) {
		return nil, status.Error(codes.FailedPrecondition, "needs to authenticate again")
	})
	_, err := e.svc.Send(context.Background(), e.owner, row.ID, "34600111222", "Hello", "")
	requireCode(t, err, 409, "instance_not_ready")
	expectHook(t, hook, row.ID)
}

func TestSendOutcomeUnknown(t *testing.T) {
	for _, code := range []codes.Code{codes.Internal, codes.Unavailable, codes.DeadlineExceeded} {
		t.Run(code.String(), func(t *testing.T) {
			e := newEnv(t)
			hook := e.hookRecorder()
			row := e.activeRow(t)
			e.fc.OnSendPlainText(func(context.Context, *corev1.SendPlainTextRequest) (*corev1.SendPlainTextResponse, error) {
				return nil, status.Error(code, "x")
			})
			_, err := e.svc.Send(context.Background(), e.owner, row.ID, "34600111222", "Hello", "")
			requireCode(t, err, 504, "send_outcome_unknown")
			if len(e.fc.SendRequests()) != 1 {
				t.Fatalf("%d send requests", len(e.fc.SendRequests()))
			}
			expectNoHook(t, hook)
		})
	}
}

func TestSendInstanceQueueFull(t *testing.T) {
	e := newEnv(t)
	hook := e.hookRecorder()
	row := e.activeRow(t)
	e.fc.OnSendPlainText(func(context.Context, *corev1.SendPlainTextRequest) (*corev1.SendPlainTextResponse, error) {
		return nil, testutil.ReasonError(codes.ResourceExhausted, corev1.ErrorReason_ERROR_REASON_INSTANCE_QUEUE_FULL, nil, "slow down")
	})
	_, err := e.svc.Send(context.Background(), e.owner, row.ID, "34600111222", "Hello", "")
	requireCode(t, err, 429, "instance_queue_full")
	expectNoHook(t, hook)
}

func TestSendInvalidArgument(t *testing.T) {
	e := newEnv(t)
	hook := e.hookRecorder()
	row := e.activeRow(t)
	e.fc.OnSendPlainText(func(context.Context, *corev1.SendPlainTextRequest) (*corev1.SendPlainTextResponse, error) {
		return nil, status.Error(codes.InvalidArgument, "'x' is not a valid recipient.")
	})
	_, err := e.svc.Send(context.Background(), e.owner, row.ID, "x", "Hello", "")
	requireFields(t, err, "to")
	if ae, _ := apierrAs(err); strings.Contains(ae.Errors[0].Message, "'x'") {
		t.Fatalf("message %q quotes the recipient", ae.Errors[0].Message)
	}
	expectNoHook(t, hook)
}

func TestSendValidatesTheBody(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	_, err := e.svc.Send(context.Background(), e.owner, row.ID, "", "Hello", "")
	requireFields(t, err, "to")
	_, err = e.svc.Send(context.Background(), e.owner, row.ID, "34600111222", "  secret-text ", "")
	if err != nil { // a text with spaces around is fine
		t.Fatal(err)
	}
	_, err = e.svc.Send(context.Background(), e.owner, row.ID, "34600111222", "   ", "")
	requireFields(t, err, "text")
	if len(e.fc.SendRequests()) != 1 {
		t.Fatalf("%d send requests, want 1", len(e.fc.SendRequests()))
	}
}

func TestSendOnARowThatIsNotActive(t *testing.T) {
	e := newEnv(t)
	hook := e.hookRecorder()
	for _, st := range []string{"creating", "removing", "missing"} {
		row := e.seed(t, testutil.InstanceSeed{State: st})
		_, err := e.svc.Send(context.Background(), e.owner, row.ID, "34600111222", "Hello", "")
		requireCode(t, err, 409, "instance_not_connected")
	}
	if len(e.fc.StatusRequests()) != 0 || len(e.fc.SendRequests()) != 0 {
		t.Fatal("a core call was made")
	}
	expectNoHook(t, hook)
}

func TestSendWhenTheStatusCheckCannotReachTheCore(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	e.fc.SetUnavailable(true)
	_, err := e.svc.Send(context.Background(), e.owner, row.ID, "34600111222", "Hello", "")
	requireCode(t, err, 503, "core_unavailable")
	if len(e.fc.SendRequests()) != 0 {
		t.Fatal("a send request was made")
	}
}

func TestSendOutOfScopeIsNotFound(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	_, err := e.svc.Send(context.Background(), e.userA2, row.ID, "34600111222", "Hello", "")
	requireCode(t, err, 404, "not_found")
	if len(e.fc.StatusRequests()) != 0 || len(e.fc.SendRequests()) != 0 {
		t.Fatal("a core call was made")
	}
}

// Each limit is accepted at its value and refused one step past it, with the field path.
func TestSendLimits(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	ctx := context.Background()
	if _, err := e.svc.Send(ctx, e.owner, row.ID, strings.Repeat("1", instances.MaxRecipientLength), strings.Repeat("é", instances.MaxTextLength), ""); err != nil {
		t.Fatalf("limits: %v", err)
	}
	_, err := e.svc.Send(ctx, e.owner, row.ID, "34600111222", strings.Repeat("é", instances.MaxTextLength+1), "")
	requireFields(t, err, "text")
	_, err = e.svc.Send(ctx, e.owner, row.ID, strings.Repeat("1", instances.MaxRecipientLength+1), "Hello", "")
	requireFields(t, err, "to")
	// Both wrong fields at once, and no value in a message.
	_, err = e.svc.Send(ctx, e.owner, row.ID, "", strings.Repeat("secret ", 1000), "")
	requireFields(t, err, "to", "text")
	if ae, _ := apierrAs(err); strings.Contains(ae.Errors[1].Message, "secret") {
		t.Fatalf("the message quotes the text: %q", ae.Errors[1].Message)
	}
	if len(e.fc.SendRequests()) != 1 {
		t.Fatalf("%d send requests, want 1", len(e.fc.SendRequests()))
	}
}

func TestSendPassesTheKeyAndMarksAReplay(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	ctx := context.Background()
	first, err := e.svc.Send(ctx, e.owner, row.ID, "34600111222", "Hello", "key-1")
	if err != nil || first.Replayed {
		t.Fatalf("%+v, %v", first, err)
	}
	second, err := e.svc.Send(ctx, e.owner, row.ID, "34600111222", "Hello", "key-1")
	if err != nil || !second.Replayed || second.MessageID != first.MessageID {
		t.Fatalf("%+v, %v", second, err)
	}
	reqs := e.fc.SendRequests()
	if len(reqs) != 2 || reqs[0].IdempotencyKey == nil || *reqs[0].IdempotencyKey != "key-1" {
		t.Fatalf("requests %v", reqs)
	}
}

func TestSendRefusesAWrongKeyBeforeAnyCoreCall(t *testing.T) {
	e := newEnv(t)
	row := e.activeRow(t)
	_, err := e.svc.Send(context.Background(), e.owner, row.ID, "34600111222", "Hello", "has space")
	requireCode(t, err, 400, "invalid_request")
	if ae, _ := apierrAs(err); strings.Contains(ae.Detail, "has space") {
		t.Fatalf("the detail repeats the key: %q", ae.Detail)
	}
	if len(e.fc.StatusRequests()) != 0 || len(e.fc.SendRequests()) != 0 {
		t.Fatal("a core call was made")
	}
}

// notConnected makes an active instance whose core status is NEEDS_AUTH, with a core that answers
// every SendPlainText with fn.
func (e *env) notConnected(t *testing.T, fn func(context.Context, *corev1.SendPlainTextRequest) (*corev1.SendPlainTextResponse, error)) testutil.SeededInstance {
	t.Helper()
	row := e.activeRow(t)
	e.fc.SetStatus(row.CoreName, needsAuth)
	e.fc.OnSendPlainText(fn)
	return row
}

func TestAKnownKeyGetsItsReplayWhileTheInstanceIsNotConnected(t *testing.T) {
	e := newEnv(t)
	hook := e.hookRecorder()
	row := e.notConnected(t, func(context.Context, *corev1.SendPlainTextRequest) (*corev1.SendPlainTextResponse, error) {
		return &corev1.SendPlainTextResponse{MessageId: "m-1", Replayed: true}, nil
	})
	res, err := e.svc.Send(context.Background(), e.owner, row.ID, "34600111222", "Hello", "key-1")
	if err != nil || res.MessageID != "m-1" || !res.Replayed {
		t.Fatalf("%+v, %v", res, err)
	}
	if reqs := e.fc.SendRequests(); len(reqs) != 1 || reqs[0].GetIdempotencyKey() != "key-1" {
		t.Fatalf("requests %v", reqs)
	}
	expectNoHook(t, hook)
}

func TestAKeyedSendToAnInstanceThatIsNotConnectedKeepsTheAnswerOfTheCore(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"in progress", testutil.ReasonError(codes.Aborted, corev1.ErrorReason_ERROR_REASON_SEND_IN_PROGRESS, nil, "in progress"), 409, "send_in_progress"},
		{"outcome unknown", testutil.ReasonError(codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_SEND_OUTCOME_UNKNOWN, nil, "unknown"), 409, "idempotency_key_outcome_unknown"},
		{"conflict", testutil.ReasonError(codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT, nil, "conflict"), 409, "idempotency_key_conflict"},
		{"deadline", status.Error(codes.DeadlineExceeded, "x"), 504, "send_outcome_unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			hook := e.hookRecorder()
			row := e.notConnected(t, func(context.Context, *corev1.SendPlainTextRequest) (*corev1.SendPlainTextResponse, error) {
				return nil, tc.err
			})
			_, err := e.svc.Send(context.Background(), e.owner, row.ID, "34600111222", "Hello", "key-1")
			requireCode(t, err, tc.status, tc.code)
			expectNoHook(t, hook)
		})
	}
}

func TestAKeyedSendRefusedByTheCoreAsUnavailableIsNotConnected(t *testing.T) {
	e := newEnv(t)
	hook := e.hookRecorder()
	row := e.notConnected(t, func(context.Context, *corev1.SendPlainTextRequest) (*corev1.SendPlainTextResponse, error) {
		return nil, status.Error(codes.Unavailable, "not connected")
	})
	_, err := e.svc.Send(context.Background(), e.owner, row.ID, "34600111222", "Hello", "key-1")
	requireCode(t, err, 409, "instance_not_connected")
	if ae, _ := apierrAs(err); ae.Detail != "The instance is not connected." {
		t.Fatalf("detail %q", ae.Detail)
	}
	expectHook(t, hook, row.ID)
}

func TestASendWithoutAKeyToAnInstanceThatIsNotConnectedMakesNoCoreCall(t *testing.T) {
	e := newEnv(t)
	hook := e.hookRecorder()
	row := e.notConnected(t, func(context.Context, *corev1.SendPlainTextRequest) (*corev1.SendPlainTextResponse, error) {
		return &corev1.SendPlainTextResponse{MessageId: "m-1"}, nil
	})
	_, err := e.svc.Send(context.Background(), e.owner, row.ID, "34600111222", "Hello", "")
	requireCode(t, err, 409, "instance_not_connected")
	if len(e.fc.SendRequests()) != 0 {
		t.Fatal("a send request was made")
	}
	expectHook(t, hook, row.ID)
}
