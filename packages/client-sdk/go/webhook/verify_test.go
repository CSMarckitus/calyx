package webhook_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/calyx"
	"github.com/csmarckitus/calyx/packages/client-sdk/go/webhook"
)

type sharedCase struct {
	Body    string            `json:"body"`
	Headers map[string]string `json:"headers"`
	Now     int64             `json:"now"`
	Secret  string            `json:"secret"`
}

func loadCase(t *testing.T) sharedCase {
	t.Helper()
	raw, err := os.ReadFile("../../../shared-types/testdata/webhook-signature.json")
	if err != nil {
		t.Fatal(err)
	}
	var c sharedCase
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func (c sharedCase) header() http.Header {
	h := http.Header{}
	for k, v := range c.Headers {
		h.Set(k, v)
	}
	return h
}

func (c sharedCase) opts() *webhook.VerifyOptions {
	return &webhook.VerifyOptions{Now: time.Unix(c.Now, 0)}
}

func sign(secret string, ts int64, body string) http.Header {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(strconv.FormatInt(ts, 10) + "." + body))
	h := http.Header{}
	h.Set("X-Calyx-Timestamp", strconv.FormatInt(ts, 10))
	h.Set("X-Calyx-Signature", "sha256="+hex.EncodeToString(m.Sum(nil)))
	return h
}

func wantError(t *testing.T, err error, code calyx.Code) {
	t.Helper()
	var e *calyx.Error
	if !errors.As(err, &e) {
		t.Fatalf("want *calyx.Error %s, got %v", code, err)
	}
	if e.Source != calyx.SourceClient || e.Code != code || e.Status != 0 ||
		e.Retry != calyx.AdviceFor(calyx.SourceClient, code) || e.Retry != calyx.RetryDoNotRetry {
		t.Fatalf("got %+v", e)
	}
	if d := e.Detail; d == "" || d[0] < 'A' || d[0] > 'Z' || d[len(d)-1] != '.' {
		t.Fatalf("the detail %q is not a full sentence", d)
	}
}

func TestVerifySharedCase(t *testing.T) {
	c := loadCase(t)
	ev, err := webhook.Verify([]byte(c.Body), c.header(), c.Secret, c.opts())
	if err != nil {
		t.Fatal(err)
	}
	if ev.ID != "evt_0001" || ev.Event != "message.received" {
		t.Fatalf("got %+v", ev)
	}
	m, err := ev.MessageReceived()
	if err != nil || m.Text == nil || *m.Text != "Hello, this is a test" {
		t.Fatalf("got %+v, %v", m, err)
	}
}

func TestVerifyHeaderLetterCase(t *testing.T) {
	c := loadCase(t)
	// Header.Set stores the canonical name, and Header.Get reads it with any letter case.
	h := http.Header{}
	h.Set("x-calyx-signature", c.Headers["X-Calyx-Signature"])
	h.Set("X-CALYX-TIMESTAMP", c.Headers["X-Calyx-Timestamp"])
	if _, err := webhook.Verify([]byte(c.Body), h, c.Secret, c.opts()); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyErrors(t *testing.T) {
	c := loadCase(t)
	now := time.Unix(c.Now, 0)
	good := []byte(c.Body)

	t.Run("empty secret", func(t *testing.T) {
		_, err := webhook.Verify(good, sign("", c.Now, c.Body), "", c.opts())
		wantError(t, err, calyx.CodeInvalidSignature)
	})
	t.Run("missing signature", func(t *testing.T) {
		h := c.header()
		h.Del("X-Calyx-Signature")
		_, err := webhook.Verify(good, h, c.Secret, c.opts())
		wantError(t, err, calyx.CodeInvalidSignature)
	})
	t.Run("missing timestamp", func(t *testing.T) {
		h := c.header()
		h.Del("X-Calyx-Timestamp")
		_, err := webhook.Verify(good, h, c.Secret, c.opts())
		wantError(t, err, calyx.CodeInvalidSignature)
	})
	t.Run("timestamp is not a number", func(t *testing.T) {
		h := c.header()
		h.Set("X-Calyx-Timestamp", "abc")
		_, err := webhook.Verify(good, h, c.Secret, c.opts())
		wantError(t, err, calyx.CodeInvalidSignature)
	})
	t.Run("too old", func(t *testing.T) {
		ts := now.Add(-6 * time.Minute).Unix()
		_, err := webhook.Verify(good, sign(c.Secret, ts, c.Body), c.Secret, c.opts())
		wantError(t, err, calyx.CodeStaleTimestamp)
	})
	t.Run("too new", func(t *testing.T) {
		ts := now.Add(6 * time.Minute).Unix()
		_, err := webhook.Verify(good, sign(c.Secret, ts, c.Body), c.Secret, c.opts())
		wantError(t, err, calyx.CodeStaleTimestamp)
	})
	t.Run("custom tolerance", func(t *testing.T) {
		ts := now.Add(-30 * time.Second).Unix()
		o := &webhook.VerifyOptions{Now: now, Tolerance: 10 * time.Second}
		_, err := webhook.Verify(good, sign(c.Secret, ts, c.Body), c.Secret, o)
		wantError(t, err, calyx.CodeStaleTimestamp)
	})
	t.Run("inside the default tolerance", func(t *testing.T) {
		ts := now.Add(-4 * time.Minute).Unix()
		if _, err := webhook.Verify(good, sign(c.Secret, ts, c.Body), c.Secret, c.opts()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("wrong signature", func(t *testing.T) {
		_, err := webhook.Verify(good, c.header(), "other-secret", c.opts())
		wantError(t, err, calyx.CodeInvalidSignature)
	})
	t.Run("changed body", func(t *testing.T) {
		_, err := webhook.Verify([]byte(c.Body+" "), c.header(), c.Secret, c.opts())
		wantError(t, err, calyx.CodeInvalidSignature)
	})
	t.Run("signature without prefix", func(t *testing.T) {
		h := c.header()
		h.Set("X-Calyx-Signature", h.Get("X-Calyx-Signature")[len("sha256="):])
		_, err := webhook.Verify(good, h, c.Secret, c.opts())
		wantError(t, err, calyx.CodeInvalidSignature)
	})
	for name, body := range map[string]string{
		"not JSON":      "not json",
		"not an event":  `{"hello":"world"}`,
		"array":         `[]`,
		"no id":         `{"event":"x","instance":"i","data":{},"timestamp":"2026-11-20T10:00:00Z"}`,
		"no data":       `{"id":"e","event":"message.received","instance":"i","timestamp":"2026-11-20T10:00:00Z"}`,
		"bad timestamp": `{"id":"e","event":"x","instance":"i","data":{},"timestamp":"nope"}`,
	} {
		t.Run("invalid body "+name, func(t *testing.T) {
			ts := now.Unix()
			_, err := webhook.Verify([]byte(body), sign(c.Secret, ts, body), c.Secret, c.opts())
			wantError(t, err, calyx.CodeInvalidEvent)
		})
	}
	t.Run("data null is valid", func(t *testing.T) {
		body := `{"id":"e","event":"x","instance":"i","data":null,"timestamp":"2026-11-20T10:00:00Z"}`
		if _, err := webhook.Verify([]byte(body), sign(c.Secret, c.Now, body), c.Secret, c.opts()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("nil options", func(t *testing.T) {
		_, err := webhook.Verify(good, c.header(), c.Secret, nil)
		wantError(t, err, calyx.CodeStaleTimestamp)
	})
}
