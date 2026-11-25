package meta

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"calyx/core/internal/engine"
)

// Meta's webhooks are small; anything bigger is not from Meta.
const maxWebhookBody = 3 << 20

// WebhookServer receives Meta's webhooks on /meta/webhook and hands each change to the provider
// of its phone number, after checking the signature with that instance's app secret.
type WebhookServer struct {
	providers   func() []engine.Provider
	verifyToken string
}

// NewWebhookServer: providers returns the live providers (Orchestrator.LiveProviders).
func NewWebhookServer(providers func() []engine.Provider, verifyToken string) *WebhookServer {
	return &WebhookServer{providers: providers, verifyToken: verifyToken}
}

func (s *WebhookServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/meta/webhook" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.verify(w, r)
	case http.MethodPost:
		s.receive(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// verify answers the challenge Meta sends when the webhook URL is configured in the app.
func (s *WebhookServer) verify(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	token := query.Get("hub.verify_token")
	if query.Get("hub.mode") != "subscribe" || subtle.ConstantTimeCompare([]byte(token), []byte(s.verifyToken)) != 1 {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = io.WriteString(w, query.Get("hub.challenge"))
}

type webhookChange struct {
	Field string          `json:"field"`
	Value json.RawMessage `json:"value"`
}

func (s *WebhookServer) receive(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var payload struct {
		Entry []struct {
			Changes []webhookChange `json:"changes"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Every change is checked before any is handed over: a request is delivered whole or not at all.
	type delivery struct {
		provider *Provider
		value    json.RawMessage
	}
	var deliveries []delivery
	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			if change.Field != "messages" {
				continue
			}
			var value struct {
				Metadata struct {
					PhoneNumberID string `json:"phone_number_id"`
				} `json:"metadata"`
			}
			_ = json.Unmarshal(change.Value, &value)
			provider := s.find(value.Metadata.PhoneNumberID)
			if provider == nil {
				slog.Warn("Meta webhook for an unknown phone number id.", "phoneNumberId", value.Metadata.PhoneNumberID)
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if !provider.IsValidSignature(body, r.Header.Get("X-Hub-Signature-256")) {
				slog.Warn("Meta webhook with a wrong signature.", "instance", provider.InstanceName())
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			deliveries = append(deliveries, delivery{provider: provider, value: change.Value})
		}
	}
	for _, d := range deliveries {
		d.provider.HandleWebhookValue(d.value)
	}
	w.WriteHeader(http.StatusOK)
}

func (s *WebhookServer) find(phoneNumberID string) *Provider {
	if phoneNumberID == "" {
		return nil
	}
	for _, p := range s.providers() {
		if metaProvider, ok := p.(*Provider); ok && metaProvider.PhoneNumberID() == phoneNumberID {
			return metaProvider
		}
	}
	return nil
}
