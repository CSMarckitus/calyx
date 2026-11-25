package apierr_test

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	"calyx/backend/internal/apierr"
)

func TestErrorText(t *testing.T) {
	e := apierr.New(409, "quota_exceeded", "The organization is full.")
	if got, want := e.Error(), "quota_exceeded: The organization is full."; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestAsFindsAWrappedError(t *testing.T) {
	err := fmt.Errorf("reserve: %w", apierr.Conflict(apierr.CodeQuotaExceeded, "x"))
	e, ok := apierr.As(err)
	if !ok || e.Status != 409 || e.Code != "quota_exceeded" {
		t.Fatalf("As = %+v, %v", e, ok)
	}
	if e, ok := apierr.As(errors.New("plain")); ok || e != nil {
		t.Fatalf("As(plain) = %v, %v", e, ok)
	}
	if e, ok := apierr.As(nil); ok || e != nil {
		t.Fatalf("As(nil) = %v, %v", e, ok)
	}
}

func TestInternalHidesTheCause(t *testing.T) {
	cause := errors.New("pq: secret detail")
	e := apierr.Internal(cause)
	if e.Status != 500 || e.Code != "internal" || e.Detail != "An internal error occurred." {
		t.Fatalf("Internal = %+v", e)
	}
	if strings.Contains(e.Error(), "secret") {
		t.Fatalf("Error() leaks the cause: %q", e.Error())
	}
	if !errors.Is(e, cause) {
		t.Fatal("errors.Is(e, cause) = false")
	}
	if apierr.Internal(nil).Unwrap() != nil {
		t.Fatal("Internal(nil).Unwrap() != nil")
	}
}

func TestHelpers(t *testing.T) {
	tests := []struct {
		name   string
		e      *apierr.Error
		status int
		code   string
	}{
		{"not found", apierr.NotFound(), 404, "not_found"},
		{"invalid", apierr.Invalid("name is required."), 400, "invalid_request"},
		{"conflict", apierr.Conflict("email_in_use", "d"), 409, "email_in_use"},
		{"forbidden", apierr.Forbidden("owner_only", "d"), 403, "owner_only"},
	}
	for _, tc := range tests {
		if tc.e.Status != tc.status || tc.e.Code != tc.code {
			t.Errorf("%s: got %d %q", tc.name, tc.e.Status, tc.e.Code)
		}
	}
	if d := apierr.NotFound().Detail; d != "The resource does not exist." {
		t.Errorf("NotFound detail = %q", d)
	}
	if d := apierr.Invalid("name is required.").Detail; d != "name is required." {
		t.Errorf("Invalid detail = %q", d)
	}
}

func TestStatusOfEveryCode(t *testing.T) {
	want := map[string]int{
		"invalid_request": 400, "unauthenticated": 401, "csrf_check_failed": 403, "owner_only": 403,
		"instance_creation_not_allowed": 403, "whatsapp_web_not_allowed": 403,
		"whatsapp_web_not_acknowledged": 403, "not_found": 404, "quota_exceeded": 409,
		"phone_number_id_in_use": 409, "pairing_in_progress": 409, "pairing_not_supported": 409,
		"instance_in_error": 409, "instance_connected": 409, "instance_not_connected": 409,
		"instance_not_ready": 409, "client_has_instances": 409, "default_client": 409,
		"email_in_use": 409, "conflict": 409, "token_invalid": 410, "too_many_messages": 413,
		"rate_limited": 429, "instance_queue_full": 429, "api_key_not_allowed": 403, "internal": 500, "core_rejected_token": 502,
		"core_unavailable": 503, "send_outcome_unknown": 504, "bulk_outcome_unknown": 504,
		"change_outcome_unknown":   504,
		"idempotency_key_conflict": 409, "send_in_progress": 409,
		"idempotency_key_outcome_unknown": 409, "duplicate_message": 409,
	}
	if len(want) != 35 {
		t.Fatalf("table has %d codes, want 35", len(want))
	}
	wantCodes := make([]string, 0, len(want))
	for code := range want {
		wantCodes = append(wantCodes, code)
	}
	sort.Strings(wantCodes)
	if got := apierr.Codes(); !slices.Equal(got, wantCodes) {
		t.Errorf("Codes() = %v, want %v", got, wantCodes)
	}
	for code, status := range want {
		if got, ok := apierr.StatusOf(code); !ok || got != status {
			t.Errorf("StatusOf(%q) = %d, %v; want %d, true", code, got, ok, status)
		}
	}
	for _, code := range []string{"pairing_expired", "nope"} {
		if got, ok := apierr.StatusOf(code); ok || got != 0 {
			t.Errorf("StatusOf(%q) = %d, %v; want 0, false", code, got, ok)
		}
	}
}

func TestPairingFailureCodes(t *testing.T) {
	want := []string{"pairing_expired", "pairing_failed", "pairing_cancelled"}
	if got := apierr.PairingFailureCodes(); !slices.Equal(got, want) {
		t.Fatalf("PairingFailureCodes() = %v, want %v", got, want)
	}
}

func TestFieldsNoControl(t *testing.T) {
	for _, s := range []string{"a\nb", "a\x00b", "tab\there", "a\u2028b", "a\u2029b", "\x7f", "a\u0085b"} {
		var f apierr.Fields
		f.NoControl("name", s)
		if len(f) != 1 || f[0] != (apierr.FieldError{Field: "name", Message: "must not have control characters."}) {
			t.Errorf("%q: fields = %+v", s, f)
		}
	}
	for _, s := range []string{"", "Gym Centre", "Café ñ 日本", "a b"} {
		var f apierr.Fields
		if f.NoControl("name", s); len(f) != 0 {
			t.Errorf("%q: %+v", s, f)
		}
	}
}

func TestFieldsErr(t *testing.T) {
	var f apierr.Fields
	if err := f.Err(); err != nil {
		t.Fatalf("empty: %v", err)
	}
	f.Add("text", "is required.")
	f.Add("to", "is required.")
	e, ok := apierr.As(f.Err())
	if !ok || e.Status != 400 || e.Code != apierr.CodeInvalidRequest || len(e.Errors) != 2 || e.Errors[1].Field != "to" {
		t.Fatalf("err = %+v", e)
	}
}

func TestInvalidFields(t *testing.T) {
	e := apierr.InvalidFields(
		apierr.FieldError{Field: "text", Message: "text is required."},
		apierr.FieldError{Field: "messages[3].recipient", Message: "recipient is not valid."},
	)
	if e.Status != 400 || e.Code != apierr.CodeInvalidRequest || e.Detail != "The request has invalid fields." {
		t.Fatalf("e = %+v", e)
	}
	if len(e.Errors) != 2 || e.Errors[1].Field != "messages[3].recipient" {
		t.Fatalf("Errors = %+v", e.Errors)
	}
	if got := apierr.Invalid("x").Errors; len(got) != 0 {
		t.Fatalf("Invalid has Errors %+v", got)
	}
}
