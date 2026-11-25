// Package instances holds the instance lifecycle of the backend: create, pair, change, remove,
// send and the recovery of interrupted rows. It is the only code that changes instance rows
// outside the watcher.
package instances

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"

	"calyx/backend/internal/apierr"
)

const (
	nonceSize       = 12
	secretKeyLength = 32
)

// WebhookInput is one webhook of a request. Secret nil keeps the stored secret, "" removes it.
type WebhookInput struct {
	URL, Event string
	Secret     *string
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != secretKeyLength {
		return nil, errors.New("the secret key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// EncryptSecret seals plain with AES-256-GCM. The result is nonce (12 bytes) followed by the
// ciphertext and the tag. No additional data is used.
func EncryptSecret(key []byte, plain string) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, []byte(plain), nil), nil
}

// DecryptSecret opens a value made by EncryptSecret. Its errors never hold the secret.
func DecryptSecret(key, sealed []byte) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	if len(sealed) < nonceSize+gcm.Overhead() {
		return "", errors.New("the sealed secret is too short")
	}
	plain, err := gcm.Open(nil, sealed[:nonceSize], sealed[nonceSize:], nil)
	if err != nil {
		return "", errors.New("the sealed secret cannot be opened")
	}
	return string(plain), nil
}

// The events below are copied from apps/core/internal/events/types.go (the backend cannot
// import an internal package of another module). Keep both lists the same.
var calyxEvents = []string{"connection.update", "message.received", "message.status", "bulk.finished"}

var whatsmeowEvents = []string{
	"Message", "Receipt", "UndecryptableMessage", "Presence", "ChatPresence",
	"Contact", "PushName", "JoinedGroup", "GroupInfo", "Picture",
}

const metaEvent = "meta.webhook"

// SupportedEvents lists the events a webhook of an instance of this provider can use.
func SupportedEvents(provider string) []string {
	out := append([]string(nil), calyxEvents...)
	if provider == "META_CLOUD" {
		return append(out, metaEvent)
	}
	for _, e := range whatsmeowEvents {
		out = append(out, "whatsmeow."+e)
	}
	return out
}

// ValidateWebhooks adds to f the errors of the webhook list of an instance of this provider:
// the rules of the core, checked before any core call. In a PATCH, a secret "" removes the stored
// secret (allowEmptySecret). No message holds a URL, an event or a secret.
func ValidateWebhooks(f *apierr.Fields, provider string, sendOnly bool, hooks []WebhookInput, allowEmptySecret bool) {
	checkWebhookFields(f, hooks, allowEmptySecret)
	if len(hooks) <= MaxWebhooks {
		checkWebhookEvents(f, provider, sendOnly, hooks)
	}
}

// checkWebhookFields adds the errors that do not depend on the instance: the count, and the URL,
// the presence of the event and the secret of each item.
func checkWebhookFields(f *apierr.Fields, hooks []WebhookInput, allowEmptySecret bool) {
	if len(hooks) > MaxWebhooks {
		f.Add("webhooks", fmt.Sprintf("must have at most %d items.", MaxWebhooks))
		return
	}
	for i, h := range hooks {
		path := fmt.Sprintf("webhooks[%d]", i)
		switch {
		case h.URL == "":
			f.Add(path+".url", "is required.")
		case len(h.URL) > MaxURLLength:
			f.Add(path+".url", fmt.Sprintf("must have at most %d bytes.", MaxURLLength))
		case !validWebhookURL(h.URL):
			f.Add(path+".url", "must start with http:// or https:// and have a host.")
		}
		if h.Event == "" {
			f.Add(path+".event", "is required.")
		}
		if h.Secret != nil {
			n := len(*h.Secret)
			switch {
			case allowEmptySecret && n > MaxSecretLength:
				f.Add(path+".secret", fmt.Sprintf("must have at most %d bytes.", MaxSecretLength))
			case !allowEmptySecret && (n < 1 || n > MaxSecretLength):
				f.Add(path+".secret", fmt.Sprintf("must have 1 to %d bytes.", MaxSecretLength))
			}
		}
	}
}

// checkWebhookEvents adds the errors that depend on the instance: a send-only instance has no
// webhook, and each event is supported by the provider and appears once.
func checkWebhookEvents(f *apierr.Fields, provider string, sendOnly bool, hooks []WebhookInput) {
	if sendOnly && len(hooks) > 0 {
		f.Add("webhooks", "must be empty for an instance used only for sending.")
	}
	supported := map[string]bool{}
	for _, e := range SupportedEvents(provider) {
		supported[e] = true
	}
	seen := map[string]bool{}
	for i, h := range hooks {
		path := fmt.Sprintf("webhooks[%d].event", i)
		switch {
		case h.Event == "":
		case !supported[h.Event]:
			f.Add(path, "is not supported for this provider.")
		case seen[h.Event]:
			f.Add(path, "must not repeat: one webhook per event.")
		}
		seen[h.Event] = true
	}
}

func validWebhookURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
