package whatsapp

import (
	"testing"

	"go.mau.fi/whatsmeow"
	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
)

func TestPairingEndTimeoutIsDeadlineExceeded(t *testing.T) {
	err := pairingEnd(whatsmeow.QRChannelTimeout, false)
	if got := calyxerr.CodeOf(err); got != codes.DeadlineExceeded {
		t.Fatalf("got %v, want DeadlineExceeded", got)
	}
}

func TestPairingEndTimeoutAfterADisconnectionIsUnavailable(t *testing.T) {
	err := pairingEnd(whatsmeow.QRChannelTimeout, true)
	if got := calyxerr.CodeOf(err); got != codes.Unavailable {
		t.Fatalf("got %v, want Unavailable", got)
	}
}

func TestPairingEndNoOtherItemIsDeadlineExceeded(t *testing.T) {
	others := []whatsmeow.QRChannelItem{
		whatsmeow.QRChannelScannedWithoutMultidevice,
		whatsmeow.QRChannelClientOutdated,
		whatsmeow.QRChannelErrUnexpectedEvent,
		{Event: whatsmeow.QRChannelEventError},
	}
	for _, item := range others {
		for _, disconnected := range []bool{false, true} {
			if got := calyxerr.CodeOf(pairingEnd(item, disconnected)); got == codes.DeadlineExceeded {
				t.Errorf("%q (disconnected %v) gave DeadlineExceeded", item.Event, disconnected)
			}
		}
	}
}
