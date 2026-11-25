package calyx_test

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/calyx"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

var backendCodes = []calyx.Code{
	calyx.CodeAPIKeyNotAllowed, calyx.CodeInstanceQueueFull,
	calyx.CodeBulkOutcomeUnknown, calyx.CodeChangeOutcomeUnknown, calyx.CodeClientHasInstances,
	calyx.CodeConflict, calyx.CodeCoreRejectedToken, calyx.CodeCoreUnavailable,
	calyx.CodeCSRFCheckFailed, calyx.CodeDefaultClient, calyx.CodeDuplicateMessage,
	calyx.CodeEmailInUse, calyx.CodeIdempotencyKeyConflict, calyx.CodeIdempotencyKeyOutcomeUnknown,
	calyx.CodeInstanceConnected, calyx.CodeInstanceCreationNotAllowed, calyx.CodeInstanceInError,
	calyx.CodeInstanceNotConnected, calyx.CodeInstanceNotReady, calyx.CodeInternal,
	calyx.CodeInvalidRequest, calyx.CodeNotFound, calyx.CodeOwnerOnly,
	calyx.CodePairingInProgress, calyx.CodePairingNotSupported, calyx.CodePhoneNumberIDInUse,
	calyx.CodeQuotaExceeded, calyx.CodeRateLimited, calyx.CodeSendInProgress,
	calyx.CodeSendOutcomeUnknown, calyx.CodeTokenInvalid, calyx.CodeTooManyMessages,
	calyx.CodeUnauthenticated, calyx.CodeWhatsAppWebNotAcknowledged, calyx.CodeWhatsAppWebNotAllowed,
}

var coreReasonCodes = []calyx.Code{
	calyx.CodeErrorReasonIdempotencyKeyConflict, calyx.CodeErrorReasonSendInProgress,
	calyx.CodeErrorReasonSendOutcomeUnknown, calyx.CodeErrorReasonDuplicateMessage,
	calyx.CodeErrorReasonInstanceQueueFull,
}

var grpcCodes = []calyx.Code{
	calyx.CodeGRPCCancelled, calyx.CodeGRPCUnknown, calyx.CodeGRPCInvalidArgument,
	calyx.CodeGRPCDeadlineExceeded, calyx.CodeGRPCNotFound, calyx.CodeGRPCAlreadyExists,
	calyx.CodeGRPCPermissionDenied, calyx.CodeGRPCResourceExhausted, calyx.CodeGRPCFailedPrecondition,
	calyx.CodeGRPCAborted, calyx.CodeGRPCOutOfRange, calyx.CodeGRPCUnimplemented,
	calyx.CodeGRPCInternal, calyx.CodeGRPCUnavailable, calyx.CodeGRPCDataLoss,
	calyx.CodeGRPCUnauthenticated,
}

var clientCodes = []calyx.Code{
	calyx.CodeTimeout, calyx.CodeAborted, calyx.CodeNetworkError, calyx.CodeInvalidResponse,
	calyx.CodeStreamFailed, calyx.CodeInvalidSignature, calyx.CodeStaleTimestamp,
	calyx.CodeInvalidEvent, calyx.CodeInvalidConfig,
}

func sortedStrings(codes []calyx.Code) []string {
	out := make([]string, len(codes))
	for i, c := range codes {
		out[i] = string(c)
	}
	slices.Sort(out)
	return out
}

func TestBackendCodesEqualProblemEnum(t *testing.T) {
	raw, err := os.ReadFile("../../../shared-types/openapi/backend.json")
	if err != nil {
		t.Fatalf("read backend.json: %v", err)
	}
	var doc struct {
		Components struct {
			Schemas struct {
				Problem struct {
					Properties struct {
						Code struct {
							Enum []string `json:"enum"`
						} `json:"code"`
					} `json:"properties"`
				} `json:"Problem"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse backend.json: %v", err)
	}
	want := doc.Components.Schemas.Problem.Properties.Code.Enum
	slices.Sort(want)
	if len(want) != 35 {
		t.Fatalf("enum has %d values, want 35", len(want))
	}
	if got := sortedStrings(backendCodes); !slices.Equal(got, want) {
		t.Errorf("backend constants = %v\nwant the enum    = %v", got, want)
	}
}

func TestCoreReasonCodesEqualErrorReason(t *testing.T) {
	var want []string
	for _, name := range corev1.ErrorReason_name {
		if name != "ERROR_REASON_UNSPECIFIED" {
			want = append(want, name)
		}
	}
	slices.Sort(want)
	if got := sortedStrings(coreReasonCodes); !slices.Equal(got, want) {
		t.Errorf("core reason constants = %v, want %v", got, want)
	}
}

func TestGRPCCodesAreTheCanonicalNames(t *testing.T) {
	if len(grpcCodes) != 16 {
		t.Fatalf("%d gRPC constants, want 16", len(grpcCodes))
	}
	// The list is the canonical names of the gRPC specification, in the order of codes 1 to 16.
	names := []string{"CANCELLED", "UNKNOWN", "INVALID_ARGUMENT", "DEADLINE_EXCEEDED", "NOT_FOUND",
		"ALREADY_EXISTS", "PERMISSION_DENIED", "RESOURCE_EXHAUSTED", "FAILED_PRECONDITION",
		"ABORTED", "OUT_OF_RANGE", "UNIMPLEMENTED", "INTERNAL", "UNAVAILABLE", "DATA_LOSS",
		"UNAUTHENTICATED"}
	for i, name := range names {
		if string(grpcCodes[i]) != name {
			t.Errorf("gRPC code %d = %q, want %q", i+1, grpcCodes[i], name)
		}
	}
}
