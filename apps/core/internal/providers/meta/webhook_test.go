package meta_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"calyx/core/internal/engine"
	"calyx/core/internal/providers/meta"
	"calyx/core/internal/testutil"
)

func newWebhookServer(t *testing.T) (*httptest.Server, *collected) {
	t.Helper()
	p, c := newProvider(t, testutil.NewFakeGraphAPI(t))
	handler := meta.NewWebhookServer(func() []engine.Provider { return []engine.Provider{p} }, "verify-me")
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server, c
}

func webhookBody(phoneNumberID string) []byte {
	value := strings.Replace(webhookValue, `"phone_number_id": "123"`, `"phone_number_id": "`+phoneNumberID+`"`, 1)
	return []byte(`{"object":"whatsapp_business_account","entry":[{"id":"1","changes":[{"field":"messages","value":` + value + `}]}]}`)
}

func post(t *testing.T, server *httptest.Server, path string, body []byte, signature string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, server.URL+path, bytes.NewReader(body))
	if signature != "" {
		req.Header.Set("X-Hub-Signature-256", signature)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestTheVerificationChallenge(t *testing.T) {
	server, _ := newWebhookServer(t)
	status, body := get(t, server.URL+"/meta/webhook?hub.mode=subscribe&hub.verify_token=verify-me&hub.challenge=abc")
	if status != http.StatusOK || body != "abc" {
		t.Fatalf("got %d %q", status, body)
	}
	status, _ = get(t, server.URL+"/meta/webhook?hub.mode=subscribe&hub.verify_token=wrong&hub.challenge=abc")
	if status != http.StatusForbidden {
		t.Fatalf("wrong token: %d", status)
	}
}

func TestASignedPostDeliversTheEvents(t *testing.T) {
	server, c := newWebhookServer(t)
	body := webhookBody("123")
	if status := post(t, server, "/meta/webhook", body, sign("app-secret", body)); status != 200 {
		t.Fatalf("status %d", status)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.events) != 3 || c.events[1].Type != "message.received" {
		t.Fatalf("got %+v", c.events)
	}
}

func TestABadOrMissingSignatureIs401AndDeliversNothing(t *testing.T) {
	server, c := newWebhookServer(t)
	body := webhookBody("123")
	for _, signature := range []string{"", sign("other", body)} {
		if status := post(t, server, "/meta/webhook", body, signature); status != 401 {
			t.Fatalf("signature %q: status %d", signature, status)
		}
	}
	if len(c.events) != 0 {
		t.Fatal("delivered")
	}
}

func TestOtherRequestsAreRefused(t *testing.T) {
	server, c := newWebhookServer(t)
	unknown := webhookBody("999")
	if status := post(t, server, "/meta/webhook", unknown, sign("app-secret", unknown)); status != 404 {
		t.Errorf("unknown phone number id: %d", status)
	}
	if status := post(t, server, "/meta/webhook", []byte("{not json"), ""); status != 400 {
		t.Errorf("not JSON: %d", status)
	}
	huge := bytes.Repeat([]byte("a"), 3<<20+1)
	if status := post(t, server, "/meta/webhook", huge, ""); status != 413 {
		t.Errorf("above 3 MB: %d", status)
	}
	if status := post(t, server, "/other", webhookBody("123"), ""); status != 404 {
		t.Errorf("other path: %d", status)
	}
	if len(c.events) != 0 {
		t.Fatal("delivered")
	}
}
