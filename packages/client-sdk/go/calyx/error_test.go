// The package is calyx, not calyx_test: the tests set the unexported cause.

package calyx

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestErrorFormat(t *testing.T) {
	cases := []struct {
		name string
		err  *Error
		want string
	}{
		{"minimal", &Error{Source: SourceBackend, Code: CodeNotFound, Status: 404},
			"calyx backend error not_found (status 404)"},
		{"detail", &Error{Source: SourceCore, Code: CodeGRPCUnavailable, Status: 14, Detail: "core is down"},
			"calyx core error UNAVAILABLE (status 14): core is down"},
		{"request id", &Error{Source: SourceBackend, Code: CodeInternal, Status: 500, RequestID: "r1"},
			"calyx backend error internal (status 500), request r1"},
		{"idempotency key", &Error{Source: SourceClient, Code: CodeTimeout, IdempotencyKey: "k1"},
			"calyx client error timeout (status 0), idempotency key k1"},
		{"all", &Error{Source: SourceBackend, Code: CodeInstanceQueueFull, Status: 429, Detail: "wait", RequestID: "r2", IdempotencyKey: "k2"},
			"calyx backend error instance_queue_full (status 429): wait, request r2, idempotency key k2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.err.Error(); got != c.want {
				t.Errorf("Error() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestErrorNeverShowsTheCause(t *testing.T) {
	const token = "secret-core-token"
	cause := errors.New("dial failed with " + token)
	err := &Error{Source: SourceClient, Code: CodeNetworkError, cause: cause}
	if strings.Contains(err.Error(), token) {
		t.Errorf("Error() shows the cause: %q", err.Error())
	}
	if !errors.Is(err, cause) {
		t.Error("Unwrap does not give the cause")
	}
}

func TestErrorUnwrapContext(t *testing.T) {
	err := &Error{Source: SourceClient, Code: CodeTimeout, cause: context.DeadlineExceeded}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Error("errors.Is does not find context.DeadlineExceeded")
	}
	if (&Error{}).Unwrap() != nil {
		t.Error("Unwrap of an error without a cause is not nil")
	}
}

func TestHasCode(t *testing.T) {
	base := &Error{Source: SourceBackend, Code: CodeNotFound}
	wrapped := fmt.Errorf("get instance: %w", base)
	if !HasCode(wrapped, CodeNotFound) {
		t.Error("HasCode does not find a wrapped *Error")
	}
	if HasCode(wrapped, CodeInternal) {
		t.Error("HasCode matches a different code")
	}
	if HasCode(nil, CodeNotFound) || HasCode(errors.New("x"), CodeNotFound) {
		t.Error("HasCode is true for nil or a foreign error")
	}
}

func TestSent(t *testing.T) {
	s := Sent[string]{Data: "ok", IdempotencyKey: "k"}
	if s.Data != "ok" || s.IdempotencyKey != "k" {
		t.Errorf("unexpected %+v", s)
	}
}
