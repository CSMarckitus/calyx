package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/config"
)

func testLogger(buf *bytes.Buffer) *slog.Logger {
	return config.NewLogger("json", buf)
}

func requestWithID(id string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	return r.WithContext(withRequestID(r.Context(), id))
}

func TestEveryCodeHasItsStatus(t *testing.T) {
	cases := map[string]int{
		"invalid_request": 400, "unauthenticated": 401,
		"csrf_check_failed": 403, "owner_only": 403, "instance_creation_not_allowed": 403,
		"whatsapp_web_not_allowed": 403, "whatsapp_web_not_acknowledged": 403,
		"not_found":      404,
		"quota_exceeded": 409, "phone_number_id_in_use": 409, "pairing_in_progress": 409,
		"pairing_not_supported": 409, "instance_in_error": 409, "instance_connected": 409,
		"instance_not_connected": 409, "instance_not_ready": 409, "client_has_instances": 409,
		"default_client": 409, "email_in_use": 409, "conflict": 409,
		"token_invalid":     410,
		"too_many_messages": 413,
		"rate_limited":      429, "instance_queue_full": 429, "api_key_not_allowed": 403,
		"internal":             500,
		"core_rejected_token":  502,
		"core_unavailable":     503,
		"send_outcome_unknown": 504, "bulk_outcome_unknown": 504, "change_outcome_unknown": 504,
		"idempotency_key_conflict": 409, "send_in_progress": 409,
		"idempotency_key_outcome_unknown": 409, "duplicate_message": 409,
	}
	if len(cases) != 35 {
		t.Fatalf("the table has %d codes, want 35", len(cases))
	}
	for code, status := range cases {
		t.Run(code, func(t *testing.T) {
			var buf bytes.Buffer
			w := httptest.NewRecorder()
			writeError(w, requestWithID("rid"), testLogger(&buf), apierr.New(status, code, "d"))
			if w.Code != status {
				t.Fatalf("status = %d, want %d", w.Code, status)
			}
			if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
				t.Fatalf("Content-Type = %q", ct)
			}
			if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
				t.Fatalf("Cache-Control = %q", cc)
			}
			var got map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			detail := "d"
			if code == "internal" {
				detail = "Internal error. Request id: rid." // the detail of "internal" is replaced
			}
			want := map[string]any{
				"type": "https://calyx.dev/errors/" + code, "title": http.StatusText(status),
				"status": float64(status), "detail": detail, "code": code,
			}
			if len(got) != len(want) {
				t.Fatalf("body = %v", got)
			}
			for k, v := range want {
				if got[k] != v {
					t.Fatalf("%s = %v, want %v", k, got[k], v)
				}
			}
		})
	}
}

func TestAPlainErrorIsInternalAndLoggedNotShown(t *testing.T) {
	var buf bytes.Buffer
	w := httptest.NewRecorder()
	writeError(w, requestWithID("rid-123"), testLogger(&buf), errors.New("db password=x"))
	if w.Code != 500 {
		t.Fatalf("status = %d", w.Code)
	}
	var p problem
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Code != "internal" || !strings.Contains(p.Detail, "rid-123") || strings.Contains(p.Detail, "password=x") {
		t.Fatalf("problem = %+v", p)
	}
	if strings.Contains(w.Body.String(), "password=x") {
		t.Fatal("the body shows the cause")
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("log lines = %d: %s", len(lines), buf.String())
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["level"] != "ERROR" || !strings.Contains(lines[0], "password=x") || !strings.Contains(lines[0], "rid-123") {
		t.Fatalf("log = %s", lines[0])
	}
}

func TestAnInternalAPIErrorIsLoggedWithItsCause(t *testing.T) {
	var buf bytes.Buffer
	w := httptest.NewRecorder()
	writeError(w, requestWithID("rid-9"), testLogger(&buf), apierr.Internal(errors.New("boom-cause")))
	if w.Code != 500 || strings.Contains(w.Body.String(), "boom-cause") || !strings.Contains(w.Body.String(), "rid-9") {
		t.Fatalf("answer = %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(buf.String(), "boom-cause") {
		t.Fatalf("log = %s", buf.String())
	}
}

func TestRateLimitedHasRetryAfter(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, requestWithID("r"), testLogger(&bytes.Buffer{}), apierr.New(429, "rate_limited", "d"))
	if got := w.Header().Get("Retry-After"); got != "60" {
		t.Fatalf("Retry-After = %q", got)
	}
	w = httptest.NewRecorder()
	writeError(w, requestWithID("r"), testLogger(&bytes.Buffer{}), apierr.New(429, "instance_queue_full", "d"))
	if got := w.Header().Get("Retry-After"); got != "" {
		t.Fatalf("instance_queue_full without a wait has Retry-After %q", got)
	}
}

func TestInstanceQueueFullHasTheWaitOfTheCore(t *testing.T) {
	for _, tc := range []struct {
		wait time.Duration
		want string
	}{{12 * time.Second, "12"}, {1200 * time.Millisecond, "2"}} {
		w := httptest.NewRecorder()
		ae := apierr.New(429, "instance_queue_full", "Full.")
		ae.RetryAfter = tc.wait
		writeError(w, requestWithID("r"), testLogger(&bytes.Buffer{}), ae)
		if got := w.Header().Get("Retry-After"); got != tc.want {
			t.Errorf("wait %v: Retry-After = %q, want %q", tc.wait, got, tc.want)
		}
		if strings.Contains(w.Body.String(), "RetryAfter") || strings.Contains(w.Body.String(), "retry_after") {
			t.Errorf("the body shows the wait: %s", w.Body.String())
		}
	}
}

func TestAGRPCCauseIsLoggedWithoutItsMessage(t *testing.T) {
	var buf bytes.Buffer
	w := httptest.NewRecorder()
	cause := status.Error(codes.Internal, "failed for 34600111222")
	writeError(w, requestWithID("rid-7"), testLogger(&buf), apierr.Internal(cause))
	if w.Code != 500 {
		t.Fatalf("status = %d", w.Code)
	}
	log := buf.String()
	if strings.Contains(log, "34600111222") {
		t.Fatalf("the log holds the message of the core: %s", log)
	}
	if !strings.Contains(log, "Internal") || !strings.Contains(log, "rid-7") {
		t.Fatalf("the log has no code and no request id: %s", log)
	}
}

func TestWriteErrorSkipsAnEndedRequest(t *testing.T) {
	var buf bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := requestWithID("rid").WithContext(withRequestID(ctx, "rid"))
	w := httptest.NewRecorder()
	writeError(w, r, testLogger(&buf), apierr.Internal(context.Canceled))
	if buf.Len() != 0 {
		t.Fatalf("a client that left was logged as an error: %s", buf.String())
	}
	if w.Body.Len() != 0 {
		t.Fatalf("an answer was written to a client that left: %s", w.Body.String())
	}
	// A real internal error is still logged when the request is alive.
	w = httptest.NewRecorder()
	writeError(w, requestWithID("rid"), testLogger(&buf), apierr.Internal(context.Canceled))
	if w.Code != 500 || buf.Len() == 0 {
		t.Fatal("a live request must still get the logged 500")
	}
}

func writeToMap(t *testing.T, ae *apierr.Error) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	writeProblem(w, toProblem(context.Background(), ae))
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestProblemOmitsErrorsWhenEmpty(t *testing.T) {
	m := writeToMap(t, apierr.Invalid("bad"))
	if _, ok := m["errors"]; ok {
		t.Fatalf("errors present: %v", m)
	}
	if m["type"] != "https://calyx.dev/errors/invalid_request" {
		t.Fatalf("type = %v", m["type"])
	}
}

func TestProblemCopiesFieldErrorsWithBodyPrefix(t *testing.T) {
	m := writeToMap(t, apierr.InvalidFields(
		apierr.FieldError{Field: "text", Message: "text is required."},
		apierr.FieldError{Field: "messages[3].recipient", Message: "recipient is not valid."},
	))
	if m["type"] != "https://calyx.dev/errors/invalid_request" {
		t.Fatalf("type = %v", m["type"])
	}
	errs := m["errors"].([]any)
	if len(errs) != 2 {
		t.Fatalf("errors = %v", errs)
	}
	first := errs[0].(map[string]any)
	second := errs[1].(map[string]any)
	if first["location"] != "body.text" || first["message"] != "text is required." {
		t.Fatalf("first = %v", first)
	}
	if second["location"] != "body.messages[3].recipient" {
		t.Fatalf("second = %v", second)
	}
}

func TestProblemKeepsTwentyFieldErrorsAndCountsTheRest(t *testing.T) {
	fields := make([]apierr.FieldError, 25)
	for i := range fields {
		fields[i] = apierr.FieldError{Field: "f", Message: "bad."}
	}
	errs := writeToMap(t, apierr.InvalidFields(fields...))["errors"].([]any)
	if len(errs) != 21 {
		t.Fatalf("len = %d, want 21", len(errs))
	}
	last := errs[20].(map[string]any)
	if last["message"] != "5 more field errors." || last["location"] != "body" {
		t.Fatalf("last = %v", last)
	}
	// Exactly 20 items: no summary item.
	errs = writeToMap(t, apierr.InvalidFields(fields[:20]...))["errors"].([]any)
	if len(errs) != 20 {
		t.Fatalf("len = %d, want 20", len(errs))
	}
	// One more item: the summary uses the singular.
	errs = writeToMap(t, apierr.InvalidFields(fields[:21]...))["errors"].([]any)
	if last := errs[20].(map[string]any); len(errs) != 21 || last["message"] != "1 more field error." {
		t.Fatalf("errors = %v", errs)
	}
}
