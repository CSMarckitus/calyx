package core

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	"calyx/backend/internal/apierr"
	corev1 "github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

type cell struct {
	nilErr bool
	status int
	code   string
}

func TestMapErrorTable(t *testing.T) {
	kinds := []callKind{callRead, callChange, callRemove, callCreate, callBulk, callSend}
	nf := cell{status: 404, code: "not_found"}
	rows := []struct {
		name string
		code codes.Code
		want [6]cell
	}{
		{"INVALID_ARGUMENT", codes.InvalidArgument, [6]cell{
			{status: 400, code: "invalid_request"},
			{status: 400, code: "invalid_request"},
			{status: 400, code: "invalid_request"},
			{status: 400, code: "invalid_request"},
			{status: 400, code: "invalid_request"},
			{status: 400, code: "invalid_request"},
		}},
		{"NOT_FOUND", codes.NotFound, [6]cell{nf, nf, {nilErr: true}, nf, nf, nf}},
		{"ALREADY_EXISTS", codes.AlreadyExists, [6]cell{
			{status: 409, code: "conflict"},
			{status: 409, code: "conflict"},
			{status: 409, code: "conflict"},
			{status: 409, code: "conflict"},
			{status: 504, code: "bulk_outcome_unknown"},
			{status: 504, code: "send_outcome_unknown"},
		}},
		{"FAILED_PRECONDITION", codes.FailedPrecondition, [6]cell{
			{status: 409, code: "conflict"},
			{status: 409, code: "conflict"},
			{status: 409, code: "conflict"},
			{status: 409, code: "conflict"},
			{status: 409, code: "instance_not_ready"},
			{status: 409, code: "instance_not_ready"},
		}},
		{"RESOURCE_EXHAUSTED", codes.ResourceExhausted, other()},
		{"UNAUTHENTICATED", codes.Unauthenticated, [6]cell{
			{status: 502, code: "core_rejected_token"},
			{status: 502, code: "core_rejected_token"},
			{status: 502, code: "core_rejected_token"},
			{status: 502, code: "core_rejected_token"},
			{status: 502, code: "core_rejected_token"},
			{status: 502, code: "core_rejected_token"},
		}},
		{"UNAVAILABLE", codes.Unavailable, transport()},
		{"DEADLINE_EXCEEDED", codes.DeadlineExceeded, transport()},
		{"CANCELED", codes.Canceled, transport()},
		{"INTERNAL", codes.Internal, other()},
		{"DATA_LOSS", codes.DataLoss, other()},
	}
	for _, row := range rows {
		for i, kind := range kinds {
			err := mapError(kind, "/calyx.core.v1.X/Y", status.Error(row.code, "Instance i_x: '34600111222' is not a valid recipient."))
			checkCell(t, row.name, i, err, row.want[i])
		}
	}
	for i, kind := range kinds {
		err := mapError(kind, "m", errors.New("boom"))
		w := other()[i]
		checkCell(t, "UNKNOWN", i, err, w)
	}
}

func transport() [6]cell {
	return [6]cell{
		{status: 503, code: "core_unavailable"},
		{status: 504, code: "change_outcome_unknown"},
		{status: 503, code: "core_unavailable"},
		{status: 503, code: "core_unavailable"},
		{status: 504, code: "bulk_outcome_unknown"},
		{status: 504, code: "send_outcome_unknown"},
	}
}

func other() [6]cell {
	return [6]cell{
		{status: 500, code: "internal"},
		{status: 500, code: "internal"},
		{status: 500, code: "internal"},
		{status: 500, code: "internal"},
		{status: 504, code: "bulk_outcome_unknown"},
		{status: 504, code: "send_outcome_unknown"},
	}
}

func checkCell(t *testing.T, name string, kind int, err error, want cell) {
	t.Helper()
	if want.nilErr {
		if err != nil {
			t.Errorf("%s kind %d: want nil, got %v", name, kind, err)
		}
		return
	}
	e, ok := apierr.As(err)
	if !ok {
		t.Errorf("%s kind %d: not an apierr error: %v", name, kind, err)
		return
	}
	if e.Status != want.status || e.Code != want.code {
		t.Errorf("%s kind %d: got %d %s, want %d %s", name, kind, e.Status, e.Code, want.status, want.code)
	}
}

func detail(t *testing.T, err error) string {
	t.Helper()
	e, ok := apierr.As(err)
	if !ok {
		t.Fatalf("not an apierr error: %v", err)
	}
	return e.Detail
}

func TestMapErrorPassesTheCoreMessageWhereTheTableSaysSo(t *testing.T) {
	got := detail(t, mapError(callChange, "m", status.Error(codes.FailedPrecondition, "Instance i_x is being paired.")))
	if got != "Instance i_x is being paired." {
		t.Errorf("change: %q", got)
	}
	got = detail(t, mapError(callCreate, "m", status.Error(codes.Unavailable, "Could not reach WhatsApp to pair: x")))
	if got != "The pairing failed. The core did not confirm it. Try again." {
		t.Errorf("create: %q", got)
	}
}

// An INVALID_ARGUMENT message of the core can quote a recipient, so it never reaches the error.
func TestMapErrorNeverCopiesAnInvalidArgumentMessage(t *testing.T) {
	core := status.Error(codes.InvalidArgument, "'34600111222' is not a valid recipient. Use a phone number with country code (34600111222) or a JID.")
	for _, kind := range []callKind{callRead, callChange, callRemove, callCreate, callBulk, callSend} {
		e, ok := apierr.As(mapError(kind, "m", core))
		if !ok || e.Status != 400 || e.Code != apierr.CodeInvalidRequest {
			t.Fatalf("kind %d: %v", kind, e)
		}
		text := e.Detail
		for _, fe := range e.Errors {
			text += " " + fe.Field + " " + fe.Message
		}
		if strings.Contains(text, "34600111222") || strings.Contains(text, "is not a valid recipient") {
			t.Errorf("kind %d copies the core message: %q", kind, text)
		}
		if kind == callSend && (len(e.Errors) != 1 || e.Errors[0].Field != "to") {
			t.Errorf("send: errors %+v, want one field error at to", e.Errors)
		}
		if kind != callSend && len(e.Errors) != 0 {
			t.Errorf("kind %d: errors %+v", kind, e.Errors)
		}
	}
}

func TestMapErrorNeverPutsTheCoreMessageInTransportDetails(t *testing.T) {
	got := detail(t, mapError(callSend, "m", status.Error(codes.Unavailable, "secret-ish detail")))
	if strings.Contains(got, "secret-ish") {
		t.Errorf("detail leaks the core message: %q", got)
	}
}

func TestMapErrorReturnsAnUntypedNil(t *testing.T) {
	err := mapError(callRead, "m", nil)
	if err != nil {
		t.Errorf("got %v", err)
	}
}

func TestUnauthenticatedIsLogged(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	_ = mapError(callRead, "/calyx.core.v1.InstanceService/ListInstances", status.Error(codes.Unauthenticated, "x"))
	out := buf.String()
	if !strings.Contains(out, "The core refused CORE_TOKEN.") || !strings.Contains(out, "ListInstances") {
		t.Errorf("log: %q", out)
	}
}

func TestLoggableKeepsOnlyTheCodeOfAGRPCError(t *testing.T) {
	st := status.Error(codes.Internal, "failed for 34600111222")
	for _, err := range []error{st, fmt.Errorf("get bulk job: %w", st)} {
		if got := Loggable(err); got != "core call failed: Internal" {
			t.Fatalf("Loggable(%v) = %v", err, got)
		}
	}
	if got := Loggable(errors.New("db down")); fmt.Sprint(got) != "db down" {
		t.Fatalf("Loggable(plain) = %v", got)
	}
}

// withReason builds a status with an ErrorInfo detail. It cannot use testutil: testutil imports core.
func withReason(code codes.Code, domain, reason string, meta map[string]string, message string) error {
	st, err := status.New(code, message).WithDetails(&errdetails.ErrorInfo{Reason: reason, Domain: domain, Metadata: meta})
	if err != nil {
		panic(err)
	}
	return st.Err()
}

const coreDomain = "calyx.core.v1"

func TestMapErrorMapsEveryCoreReason(t *testing.T) {
	rows := []struct {
		name   string
		code   codes.Code
		reason corev1.ErrorReason
		want   string
	}{
		{"conflict", codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT, "idempotency_key_conflict"},
		{"in progress", codes.Aborted, corev1.ErrorReason_ERROR_REASON_SEND_IN_PROGRESS, "send_in_progress"},
		{"outcome unknown", codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_SEND_OUTCOME_UNKNOWN, "idempotency_key_outcome_unknown"},
		{"duplicate", codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_DUPLICATE_MESSAGE, "duplicate_message"},
	}
	for _, row := range rows {
		for _, kind := range []callKind{callSend, callBulk} {
			err := withReason(row.code, coreDomain, row.reason.String(), nil, "Core text.")
			e, ok := apierr.As(mapError(kind, "m", err))
			if !ok || e.Status != 409 || e.Code != row.want || e.Detail != "Core text." {
				t.Errorf("%s kind %d: %+v", row.name, kind, e)
			}
		}
	}
}

func TestMapErrorAddsTheEarlierMessageID(t *testing.T) {
	err := withReason(codes.AlreadyExists, coreDomain, corev1.ErrorReason_ERROR_REASON_DUPLICATE_MESSAGE.String(),
		map[string]string{"message_id": "3EB0ABC"}, "Sent 2 minutes ago.")
	if got := detail(t, mapError(callSend, "m", err)); got != "Sent 2 minutes ago. Earlier message id: 3EB0ABC." {
		t.Errorf("detail %q", got)
	}
	err = withReason(codes.AlreadyExists, coreDomain, corev1.ErrorReason_ERROR_REASON_DUPLICATE_MESSAGE.String(), nil, "Sent before.")
	if got := detail(t, mapError(callSend, "m", err)); got != "Sent before." {
		t.Errorf("detail %q", got)
	}
}

func TestMapErrorIgnoresOtherDomainsAndWrongPairs(t *testing.T) {
	dup := corev1.ErrorReason_ERROR_REASON_DUPLICATE_MESSAGE.String()
	cases := map[string]error{
		"other domain":         withReason(codes.AlreadyExists, "other.v1", dup, nil, "x"),
		"already exists, none": status.Error(codes.AlreadyExists, "x"),
		"aborted, none":        status.Error(codes.Aborted, "x"),
		"wrong pair":           withReason(codes.Aborted, coreDomain, dup, nil, "x"),
	}
	for name, err := range cases {
		for kind, want := range map[callKind]string{callSend: "send_outcome_unknown", callBulk: "bulk_outcome_unknown"} {
			e, ok := apierr.As(mapError(kind, "m", err))
			if !ok || e.Status != 504 || e.Code != want {
				t.Errorf("%s kind %d: %+v", name, kind, e)
			}
		}
	}
}

func TestMapErrorOutcomeUnknownTexts(t *testing.T) {
	send := "The core did not confirm the send. The message may have been sent. If you sent an Idempotency-Key, retry with the same key. Without a key, do not retry automatically."
	bulk := "The core did not confirm the bulk job. It may be running. If you sent an Idempotency-Key, retry with the same key. Without a key, do not start it again automatically."
	if got := detail(t, mapError(callSend, "m", status.Error(codes.Unavailable, "x"))); got != send {
		t.Errorf("send: %q", got)
	}
	if got := detail(t, mapError(callBulk, "m", status.Error(codes.Unavailable, "x"))); got != bulk {
		t.Errorf("bulk: %q", got)
	}
}

func TestMapErrorReasonDetailsHoldNoKeyTextOrRecipient(t *testing.T) {
	err := withReason(codes.AlreadyExists, coreDomain, corev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT.String(),
		nil, "The key 'secret-key' was used for '34600111222'.")
	got := detail(t, mapError(callSend, "m", err))
	if strings.Contains(got, "secret-key") || strings.Contains(got, "34600111222") {
		t.Errorf("detail %q", got)
	}
}

func withRetry(code codes.Code, reason corev1.ErrorReason, wait time.Duration, message string) error {
	st, err := status.New(code, message).WithDetails(
		&errdetails.ErrorInfo{Reason: reason.String(), Domain: coreDomain},
		&errdetails.RetryInfo{RetryDelay: durationpb.New(wait)},
	)
	if err != nil {
		panic(err)
	}
	return st.Err()
}

func TestMapErrorInstanceQueueFull(t *testing.T) {
	full := corev1.ErrorReason_ERROR_REASON_INSTANCE_QUEUE_FULL
	for _, kind := range []callKind{callSend, callBulk} {
		e, _ := apierr.As(mapError(kind, "m", withRetry(codes.ResourceExhausted, full, 12*time.Second, "Queue full.")))
		if e == nil || e.Status != 429 || e.Code != "instance_queue_full" || e.RetryAfter != 12*time.Second ||
			e.Detail != "Queue full. Retry with the same idempotency key in 12 seconds." {
			t.Errorf("kind %d with RetryInfo: %+v", kind, e)
		}
		// An older core sends no RetryInfo: no wait and no sentence.
		e, _ = apierr.As(mapError(kind, "m", withReason(codes.ResourceExhausted, coreDomain, full.String(), nil, "Queue full.")))
		if e == nil || e.Status != 429 || e.Code != "instance_queue_full" || e.RetryAfter != 0 || e.Detail != "Queue full." {
			t.Errorf("kind %d without RetryInfo: %+v", kind, e)
		}
	}
}

func TestMapErrorSendInProgressKeepsTheWait(t *testing.T) {
	e, _ := apierr.As(mapError(callSend, "m", withRetry(codes.Aborted,
		corev1.ErrorReason_ERROR_REASON_SEND_IN_PROGRESS, 3*time.Second, "Busy.")))
	if e == nil || e.Status != 409 || e.Code != "send_in_progress" || e.RetryAfter != 3*time.Second ||
		e.Detail != "Busy. Retry with the same idempotency key in 3 seconds." {
		t.Errorf("%+v", e)
	}
}

func TestCreateErrorWrapsErrPairingExpiredOnlyForDeadlineExceeded(t *testing.T) {
	err := createError(status.Error(codes.DeadlineExceeded, "x"))
	if !errors.Is(err, ErrPairingExpired) {
		t.Error("DEADLINE_EXCEEDED does not wrap ErrPairingExpired")
	}
	if detail(t, err) != detail(t, mapError(callCreate, "m", status.Error(codes.DeadlineExceeded, "x"))) {
		t.Error("the mapped error changed")
	}
	for _, code := range []codes.Code{codes.Unavailable, codes.Canceled, codes.Internal} {
		if errors.Is(createError(status.Error(code, "x")), ErrPairingExpired) {
			t.Errorf("%v wraps ErrPairingExpired", code)
		}
	}
}
