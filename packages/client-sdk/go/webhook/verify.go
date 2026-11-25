package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/calyx"
)

const (
	signatureHeader = "X-Calyx-Signature"
	timestampHeader = "X-Calyx-Timestamp"
	signaturePrefix = "sha256="
	defaultWindow   = 5 * time.Minute
)

// VerifyOptions changes the checks of Verify. A nil value gives the defaults.
type VerifyOptions struct {
	// Tolerance is the largest distance between the timestamp and Now. A value of 0 or less means the default of 5 minutes.
	Tolerance time.Duration
	// Now is the time of the check. The default is time.Now(). Tests set it.
	Now time.Time
}

// Verify checks the signature and the time of a webhook, then decodes the body to a
// calyx.Event. Give it the raw body, as the program received it. A failure gives a
// *calyx.Error with the code invalid_signature, stale_timestamp or invalid_event.
func Verify(body []byte, h http.Header, secret string, opts *VerifyOptions) (calyx.Event, error) {
	var o VerifyOptions
	if opts != nil {
		o = *opts
	}
	if o.Tolerance <= 0 {
		o.Tolerance = defaultWindow
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}

	if secret == "" {
		return calyx.Event{}, reject(calyx.CodeInvalidSignature, "The webhook secret is empty.")
	}
	tsText, signature := h.Get(timestampHeader), h.Get(signatureHeader)
	if tsText == "" || signature == "" {
		return calyx.Event{}, reject(calyx.CodeInvalidSignature, "The signature or the timestamp header is missing.")
	}
	seconds, err := strconv.ParseInt(tsText, 10, 64)
	if err != nil {
		return calyx.Event{}, reject(calyx.CodeInvalidSignature, "The timestamp header is not a number.")
	}
	age := o.Now.Sub(time.Unix(seconds, 0))
	if age > o.Tolerance || age < -o.Tolerance {
		return calyx.Event{}, reject(calyx.CodeStaleTimestamp, "The timestamp is outside the tolerance.")
	}

	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(tsText + "."))
	m.Write(body)
	want := signaturePrefix + hex.EncodeToString(m.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(strings.ToLower(signature))) {
		return calyx.Event{}, reject(calyx.CodeInvalidSignature, "The signature does not match.")
	}

	var ev calyx.Event
	if err := json.Unmarshal(body, &ev); err != nil ||
		ev.ID == "" || ev.Event == "" || ev.Instance == "" || ev.Timestamp.IsZero() || len(ev.Data) == 0 {
		return calyx.Event{}, reject(calyx.CodeInvalidEvent, "The body is not a Calyx event.")
	}
	return ev, nil
}

// reject builds the error of a failed check. It has no cause, so no body text reaches it.
func reject(code calyx.Code, detail string) *calyx.Error {
	return &calyx.Error{
		Source: calyx.SourceClient,
		Code:   code,
		Retry:  calyx.AdviceFor(calyx.SourceClient, code),
		Detail: detail,
	}
}
