package testutil_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/engine"
	"calyx/core/internal/providers/meta"
	"calyx/core/internal/testutil"
)

var recipients = []string{"+34 (600) 111 222", "34600111222", "34600111222@s.whatsapp.net", "hello"}

func TestNormalizeRecipientGivesTheResultOfTheFakeProvider(t *testing.T) {
	fake := testutil.NewFakeProvider("shop")
	for _, in := range recipients {
		want, wantErr := fake.NormalizeRecipient(in)
		got, gotErr := testutil.NormalizeRecipient("WHATSAPP_WEB", in)
		if got != want || calyxerr.CodeOf(gotErr) != calyxerr.CodeOf(wantErr) {
			t.Errorf("%q: got %q, %v; want %q, %v", in, got, gotErr, want, wantErr)
		}
	}
}

func TestNormalizeRecipientGivesTheResultOfMetaForMetaCloud(t *testing.T) {
	p, err := meta.New(engine.InstanceRow{Name: "shop", Provider: "META_CLOUD",
		ProviderConfig: `{"phoneNumberId":"123","accessToken":"t","appSecret":"s"}`}, engine.ProviderHooks{}, meta.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range recipients {
		want, wantErr := p.NormalizeRecipient(in)
		got, gotErr := testutil.NormalizeRecipient("META_CLOUD", in)
		if got != want || calyxerr.CodeOf(gotErr) != calyxerr.CodeOf(wantErr) {
			t.Errorf("%q: got %q, %v; want %q, %v", in, got, gotErr, want, wantErr)
		}
	}
}

func TestConnectEndsWithItsContext(t *testing.T) {
	fake := testutil.NewFakeProvider("shop")
	counter := testutil.NewConnectionCounter()
	fake.CountConnections(counter)
	fake.BeforeConnect(func(ctx context.Context) { <-ctx.Done() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if err := fake.Connect(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	if counter.Open("shop") != 0 {
		t.Fatal("a connect that ended with its context left a connection open")
	}
}
