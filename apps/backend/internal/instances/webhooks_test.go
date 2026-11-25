package instances_test

import (
	"bytes"
	"strings"
	"testing"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/instances"
)

func testKey(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func TestSecretRoundTrip(t *testing.T) {
	key := testKey(1)
	sealed, err := instances.EncryptSecret(key, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	got, err := instances.DecryptSecret(key, sealed)
	if err != nil || got != "s3cret" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestSecretWithTheWrongKeyFails(t *testing.T) {
	sealed, _ := instances.EncryptSecret(testKey(1), "s3cret")
	_, err := instances.DecryptSecret(testKey(2), sealed)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("the error holds the secret: %v", err)
	}
}

func TestSecretNonceDiffersEachTime(t *testing.T) {
	a, _ := instances.EncryptSecret(testKey(1), "s3cret")
	b, _ := instances.EncryptSecret(testKey(1), "s3cret")
	if bytes.Equal(a, b) || bytes.Equal(a[:12], b[:12]) {
		t.Fatal("two sealed values share a nonce")
	}
}

func TestSealedSecretLayout(t *testing.T) {
	sealed, _ := instances.EncryptSecret(testKey(1), "s3cret")
	if len(sealed) != 12+len("s3cret")+16 {
		t.Fatalf("length %d", len(sealed))
	}
}

func TestChangedCiphertextFails(t *testing.T) {
	sealed, _ := instances.EncryptSecret(testKey(1), "s3cret")
	sealed[len(sealed)-1] ^= 1
	if _, err := instances.DecryptSecret(testKey(1), sealed); err == nil {
		t.Fatal("expected an error")
	}
}

func TestShortValueAndWrongKeyLengthFail(t *testing.T) {
	if _, err := instances.DecryptSecret(testKey(1), make([]byte, 10)); err == nil {
		t.Fatal("short value accepted")
	}
	if _, err := instances.EncryptSecret(make([]byte, 16), "x"); err == nil {
		t.Fatal("16-byte key accepted")
	}
	if _, err := instances.DecryptSecret(make([]byte, 16), make([]byte, 40)); err == nil {
		t.Fatal("16-byte key accepted")
	}
}

func TestSupportedEventsPerProvider(t *testing.T) {
	web := instances.SupportedEvents("WHATSAPP_WEB")
	if len(web) != 14 {
		t.Fatalf("WHATSAPP_WEB: %d names", len(web))
	}
	has := func(list []string, s string) bool {
		for _, e := range list {
			if e == s {
				return true
			}
		}
		return false
	}
	if !has(web, "whatsmeow.Receipt") || has(web, "meta.webhook") {
		t.Fatalf("WHATSAPP_WEB: %v", web)
	}
	meta := instances.SupportedEvents("META_CLOUD")
	if len(meta) != 5 || !has(meta, "meta.webhook") {
		t.Fatalf("META_CLOUD: %v", meta)
	}
	for _, e := range meta {
		if strings.HasPrefix(e, "whatsmeow.") {
			t.Fatalf("META_CLOUD has %s", e)
		}
	}
}

// validateWebhooks runs the create rules (a secret "" is refused) and returns the error.
func validateWebhooks(provider string, sendOnly bool, hooks []instances.WebhookInput) error {
	var f apierr.Fields
	instances.ValidateWebhooks(&f, provider, sendOnly, hooks, false)
	return f.Err()
}

func TestValidateWebhooksAcceptsAValidList(t *testing.T) {
	err := validateWebhooks("WHATSAPP_WEB", false, []instances.WebhookInput{
		{URL: "https://a.example/h", Event: "connection.update"},
		{URL: "http://b.example:8080/x", Event: "whatsmeow.Message", Secret: strp("s")},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateWebhooksRefusals(t *testing.T) {
	long := strings.Repeat("a", 2049)
	secret := strings.Repeat("S", 257)
	hook := func(url, event string, secret *string) []instances.WebhookInput {
		return []instances.WebhookInput{{URL: url, Event: event, Secret: secret}}
	}
	cases := []struct {
		name     string
		provider string
		sendOnly bool
		hooks    []instances.WebhookInput
	}{
		{"send-only with a webhook", "WHATSAPP_WEB", true, hook("https://a.example/", "connection.update", nil)},
		{"meta with whatsmeow event", "META_CLOUD", false, hook("https://a.example/", "whatsmeow.Message", nil)},
		{"web with meta event", "WHATSAPP_WEB", false, hook("https://a.example/", "meta.webhook", nil)},
		{"unknown whatsmeow event", "WHATSAPP_WEB", false, hook("https://a.example/", "whatsmeow.Foo", nil)},
		{"duplicate event", "WHATSAPP_WEB", false, []instances.WebhookInput{
			{URL: "https://a.example/1", Event: "message.received"}, {URL: "https://a.example/2", Event: "message.received"},
		}},
		{"ftp scheme", "WHATSAPP_WEB", false, hook("ftp://a.example/x", "message.received", nil)},
		{"no host", "WHATSAPP_WEB", false, hook("https://", "message.received", nil)},
		{"no scheme", "WHATSAPP_WEB", false, hook("a.example/x", "message.received", nil)},
		{"long url", "WHATSAPP_WEB", false, hook("https://a.example/"+long, "message.received", nil)},
		{"long secret", "WHATSAPP_WEB", false, hook("https://a.example/", "message.received", &secret)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateWebhooks(c.provider, c.sendOnly, c.hooks)
			ae, ok := apierr.As(err)
			if !ok || ae.Status != 400 || ae.Code != "invalid_request" {
				t.Fatalf("got %v", err)
			}
			for _, fe := range ae.Errors {
				if strings.Contains(fe.Message, secret) || strings.Contains(fe.Message, "a.example") {
					t.Fatalf("the message %q holds the value", fe.Message)
				}
			}
		})
	}
}

func TestValidateWebhooksAcceptsAnEmptyListOnASendOnlyInstance(t *testing.T) {
	if err := validateWebhooks("WHATSAPP_WEB", true, nil); err != nil {
		t.Fatal(err)
	}
}

func TestWebhookMessagesHaveNoURL(t *testing.T) {
	err := validateWebhooks("WHATSAPP_WEB", false, []instances.WebhookInput{
		{URL: "ftp://user:pw-SECRET@a.example/x", Event: "message.received"},
	})
	ae, _ := apierr.As(err)
	if ae == nil || len(ae.Errors) != 1 || ae.Errors[0].Field != "webhooks[0].url" {
		t.Fatalf("err %v", err)
	}
	if m := ae.Errors[0].Message; strings.Contains(m, "pw-SECRET") || strings.Contains(m, "a.example") {
		t.Fatalf("message %q", m)
	}
}
