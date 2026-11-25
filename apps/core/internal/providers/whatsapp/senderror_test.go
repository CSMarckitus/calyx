package whatsapp

import (
	"errors"
	"fmt"
	"testing"

	"go.mau.fi/whatsmeow"
	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
)

func TestSendErrorMapsNotConnectedAndNotLoggedInToUnavailable(t *testing.T) {
	for _, base := range []error{whatsmeow.ErrNotConnected, whatsmeow.ErrNotLoggedIn} {
		for _, err := range []error{base, fmt.Errorf("send: %w", base)} {
			if got := sendError("shop", err); calyxerr.CodeOf(got) != codes.Unavailable {
				t.Errorf("%v: got %v", err, got)
			}
		}
	}
}

func TestSendErrorKeepsOtherErrors(t *testing.T) {
	plain := errors.New("server error 500")
	if got := sendError("shop", plain); !errors.Is(got, plain) {
		t.Fatalf("got %v", got)
	}
	if got := sendError("shop", nil); got != nil {
		t.Fatalf("got %v", got)
	}
}
