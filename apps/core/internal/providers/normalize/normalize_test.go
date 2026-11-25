package normalize_test

import (
	"testing"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/engine"
	"calyx/core/internal/providers/meta"
	"calyx/core/internal/providers/normalize"
	"calyx/core/internal/providers/whatsapp"
)

// The option of the orchestrator and Provider.NormalizeRecipient must never differ.
func TestRecipientGivesTheResultOfTheProvider(t *testing.T) {
	whatsappProvider := whatsapp.New(engine.InstanceRow{Name: "shop"}, engine.ProviderHooks{}, nil, nil)
	metaProvider, err := meta.New(engine.InstanceRow{Name: "shop", Provider: "META_CLOUD",
		ProviderConfig: `{"phoneNumberId":"123","accessToken":"t","appSecret":"s"}`}, engine.ProviderHooks{}, meta.Options{})
	if err != nil {
		t.Fatal(err)
	}
	providers := map[string]engine.Provider{"WHATSAPP_WEB": whatsappProvider, "META_CLOUD": metaProvider}
	inputs := []string{"+34 (600) 111 222", "34600111222@s.whatsapp.net", "hello"}
	for name, provider := range providers {
		for _, in := range inputs {
			want, wantErr := provider.NormalizeRecipient(in)
			got, gotErr := normalize.Recipient(name, in)
			if got != want || calyxerr.CodeOf(gotErr) != calyxerr.CodeOf(wantErr) {
				t.Errorf("%s %q: got %q, %v; want %q, %v", name, in, got, gotErr, want, wantErr)
			}
		}
	}
}

func TestRecipientRefusesAnUnknownProvider(t *testing.T) {
	if _, err := normalize.Recipient("OTHER", "34600111222"); calyxerr.CodeOf(err) != codes.FailedPrecondition {
		t.Fatalf("got %v", err)
	}
}
