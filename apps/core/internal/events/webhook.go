package events

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// SerializedEvent is an event ready to be delivered: plain strings, so it can travel through Redis.
type SerializedEvent struct {
	ID           string `json:"id"`
	InstanceName string `json:"instanceName"`
	Type         string `json:"type"`
	PayloadJSON  string `json:"payloadJson"`
	OccurredAt   string `json:"occurredAt"` // RFC 3339
}

// Serialize turns the payload into JSON once, however many webhooks receive the event.
func Serialize(e Event) (SerializedEvent, error) {
	payload, err := SerializePayload(e.Payload)
	if err != nil {
		return SerializedEvent{}, err
	}
	return SerializedEvent{
		ID:           e.ID,
		InstanceName: e.InstanceName,
		Type:         e.Type,
		PayloadJSON:  payload,
		OccurredAt:   e.OccurredAt.UTC().Format(time.RFC3339Nano),
	}, nil
}

const requestTimeout = 5 * time.Second

// BuildRequest returns the body and headers of a delivery.
//
// When the webhook has a secret, the receiver can check that the request comes from this core
// and was not altered: X-Calyx-Signature is "sha256=" + HMAC-SHA256(secret, "<timestamp>.<body>"),
// with the timestamp (Unix seconds) in X-Calyx-Timestamp so old requests can be refused.
func BuildRequest(event SerializedEvent, secret string, now time.Time) ([]byte, http.Header) {
	// The payload is already JSON: it is placed as is instead of being parsed and encoded again.
	quote := func(s string) string { encoded, _ := json.Marshal(s); return string(encoded) }
	body := []byte(`{"id":` + quote(event.ID) + `,"event":` + quote(event.Type) + `,"instance":` +
		quote(event.InstanceName) + `,"data":` + event.PayloadJSON + `,"timestamp":` + quote(event.OccurredAt) + `}`)

	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	if secret != "" {
		timestamp := strconv.FormatInt(now.Unix(), 10)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(timestamp + "."))
		mac.Write(body)
		headers.Set("X-Calyx-Timestamp", timestamp)
		headers.Set("X-Calyx-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	return body, headers
}

// HTTPStatusError is an answer that was not 2xx.
type HTTPStatusError struct{ StatusCode int }

func (e *HTTPStatusError) Error() string { return fmt.Sprintf("webhook answered %d", e.StatusCode) }

// Send makes one attempt. Retries are decided by the caller (the dispatcher or the asynq worker).
// The request is signed here, so every retry carries a fresh timestamp.
func Send(ctx context.Context, client *http.Client, url, secret string, event SerializedEvent) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	body, headers := BuildRequest(event, secret, time.Now())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header = headers
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10)) // lets the connection be reused
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &HTTPStatusError{StatusCode: resp.StatusCode}
	}
	return nil
}

// IsWorthRetrying is false for a 4xx: the receiver understood and said no.
func IsWorthRetrying(err error) bool {
	var statusErr *HTTPStatusError
	return !errors.As(err, &statusErr) || statusErr.StatusCode >= 500
}
