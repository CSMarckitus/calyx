// Package contract only checks that the core sees the generated gRPC code with the names
// of its public API (PROVIDER_WHATSAPP_WEB, WhatsAppWebSettings).
package contract

import (
	"testing"

	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

func TestRenamedProviderIsWhatsAppWeb(t *testing.T) {
	// The number is part of the wire format: renaming the enum must not change it.
	if corev1.Provider_PROVIDER_WHATSAPP_WEB != 1 {
		t.Fatal("PROVIDER_WHATSAPP_WEB must keep number 1")
	}
}

func TestCreateInstanceCarriesWhatsAppWebSettings(t *testing.T) {
	req := &corev1.CreateInstanceRequest{
		ProviderSettings: &corev1.CreateInstanceRequest_WhatsappWeb{WhatsappWeb: &corev1.WhatsAppWebSettings{}},
	}
	if req.GetWhatsappWeb() == nil {
		t.Fatal("whatsapp_web settings lost")
	}
}
