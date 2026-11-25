package calyxerr_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

func TestWithReasonKeepsCodeAndMessage(t *testing.T) {
	meta := map[string]string{"at": "2026-11-19T10:00:00Z"}
	err := calyxerr.WithReason(codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_DUPLICATE_MESSAGE, meta, "Sent %d minutes ago.", 3)
	wrapped := fmt.Errorf("wrap: %w", err)

	if got := calyxerr.CodeOf(wrapped); got != codes.AlreadyExists {
		t.Errorf("CodeOf = %v, want AlreadyExists", got)
	}
	if err.Message != "Sent 3 minutes ago." {
		t.Errorf("Message = %q", err.Message)
	}
	if err.Reason != corev1.ErrorReason_ERROR_REASON_DUPLICATE_MESSAGE {
		t.Errorf("Reason = %v", err.Reason)
	}
	if err.Metadata["at"] != "2026-11-19T10:00:00Z" {
		t.Errorf("Metadata = %v", err.Metadata)
	}
	var target *calyxerr.Error
	if !errors.As(wrapped, &target) {
		t.Error("errors.As does not find the error")
	}
}

func TestNewHasNoReason(t *testing.T) {
	err := calyxerr.New(codes.NotFound, "Missing.")
	if err.Reason != corev1.ErrorReason_ERROR_REASON_UNSPECIFIED || err.Metadata != nil {
		t.Errorf("New set a reason or metadata: %+v", err)
	}
}

func TestWithRetryAfterSetsTheWait(t *testing.T) {
	err := calyxerr.New(codes.ResourceExhausted, "Full.").WithRetryAfter(12 * time.Second)
	if err.RetryAfter != 12*time.Second {
		t.Errorf("RetryAfter = %v", err.RetryAfter)
	}
}
