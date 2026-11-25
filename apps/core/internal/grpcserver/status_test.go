package grpcserver

import (
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"calyx/core/internal/calyxerr"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// This test is in package grpcserver (not grpcserver_test) because toStatus is not exported.
func TestStatusCarriesErrorInfo(t *testing.T) {
	err := calyxerr.WithReason(codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_DUPLICATE_MESSAGE,
		map[string]string{"at": "2026-11-19T10:00:00Z"}, "Repeated message.")

	st, _ := status.FromError(toStatus(err, "/test"))
	if st.Code() != codes.AlreadyExists || st.Message() != "Repeated message." {
		t.Fatalf("status = %v %q", st.Code(), st.Message())
	}
	details := st.Details()
	if len(details) != 1 {
		t.Fatalf("got %d details, want 1", len(details))
	}
	info, ok := details[0].(*errdetails.ErrorInfo)
	if !ok {
		t.Fatalf("detail is %T, want *errdetails.ErrorInfo", details[0])
	}
	if info.Reason != "ERROR_REASON_DUPLICATE_MESSAGE" || info.Domain != "calyx.core.v1" {
		t.Errorf("reason %q, domain %q", info.Reason, info.Domain)
	}
	if info.Metadata["at"] != "2026-11-19T10:00:00Z" {
		t.Errorf("metadata = %v", info.Metadata)
	}
}

func TestStatusCarriesRetryInfoAfterErrorInfo(t *testing.T) {
	err := calyxerr.WithReason(codes.ResourceExhausted, corev1.ErrorReason_ERROR_REASON_INSTANCE_QUEUE_FULL,
		nil, "Full.").WithRetryAfter(12 * time.Second)

	st, _ := status.FromError(toStatus(err, "/test"))
	details := st.Details()
	if len(details) != 2 {
		t.Fatalf("got %d details, want 2", len(details))
	}
	if _, ok := details[0].(*errdetails.ErrorInfo); !ok {
		t.Fatalf("detail 0 is %T, want *errdetails.ErrorInfo", details[0])
	}
	retry, ok := details[1].(*errdetails.RetryInfo)
	if !ok {
		t.Fatalf("detail 1 is %T, want *errdetails.RetryInfo", details[1])
	}
	if got := retry.RetryDelay.AsDuration(); got != 12*time.Second {
		t.Errorf("RetryDelay = %v, want 12s", got)
	}
}

func TestStatusWithoutAWaitHasNoRetryInfo(t *testing.T) {
	err := calyxerr.WithReason(codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_DUPLICATE_MESSAGE, nil, "Repeated.")
	st, _ := status.FromError(toStatus(err, "/test"))
	if len(st.Details()) != 1 {
		t.Errorf("got %d details, want 1", len(st.Details()))
	}
}

func TestStatusWithoutReasonHasNoDetails(t *testing.T) {
	st, _ := status.FromError(toStatus(calyxerr.New(codes.NotFound, "Missing."), "/test"))
	if st.Code() != codes.NotFound || len(st.Details()) != 0 {
		t.Errorf("code %v, %d details", st.Code(), len(st.Details()))
	}
}

func TestKeepaliveValues(t *testing.T) {
	if keepaliveParams.Time != 2*time.Minute {
		t.Errorf("ping interval = %v, want 2m", keepaliveParams.Time)
	}
	if keepaliveParams.Timeout != 20*time.Second {
		t.Errorf("ping timeout = %v, want 20s", keepaliveParams.Timeout)
	}
	if keepalivePolicy.MinTime != time.Minute {
		t.Errorf("minimum client ping interval = %v, want 1m", keepalivePolicy.MinTime)
	}
	if keepalivePolicy.MinTime >= keepaliveParams.Time {
		t.Error("the client minimum must be shorter than the server interval")
	}
}
