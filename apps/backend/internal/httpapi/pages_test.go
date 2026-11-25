package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

const pageToken = "Page-Token-0123456789abcdefghijklmnopqrstuvwxyz"

// The frontend serves these paths. Without it, the backend tells how to do the same with the API.
var pageTexts = map[string]string{
	"/invite":         "The Calyx frontend is not installed at this address. To accept an invitation with the API, send the token of the link to POST /api/v1/invitations/accept (acceptInvitation).",
	"/reset-password": "The Calyx frontend is not installed at this address. To set a new password with the API, send the token of the link to POST /api/v1/auth/password-reset/confirm (confirmPasswordReset).",
	"/instances/0b6f3e3c-8c1e-4b5a-9d55-0f6e2c9a1b11": "The Calyx frontend is not installed at this address. To read an instance with the API, call GET /api/v1/instances/{id} (getInstance).",
}

func TestPagesTellHowToUseTheAPI(t *testing.T) {
	e := newTestEnv(t)
	for path, text := range pageTexts {
		e.Logs.Reset()
		w := e.do("GET", path+"?token="+pageToken, nil)
		requireStatus(t, w, http.StatusNotFound)
		if got := w.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
			t.Errorf("%s: Content-Type = %q", path, got)
		}
		if got := w.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q", path, got)
		}
		if got := w.Body.String(); got != text+"\n" {
			t.Errorf("%s: body = %q, want %q", path, got, text+"\n")
		}
		if strings.Contains(e.Logs.String(), pageToken) {
			t.Errorf("%s: the log holds the token: %s", path, e.Logs.String())
		}
		if lines := requestLines(t, e); len(lines) != 1 || lines[0]["status"] != float64(404) {
			t.Errorf("%s: request lines = %v", path, lines)
		}
	}
}

func TestPagesRefuseAPost(t *testing.T) {
	e := newTestEnv(t)
	for path := range pageTexts {
		w := e.do("POST", path, map[string]string{"token": pageToken, "password": "a brand new password"})
		requireStatus(t, w, http.StatusMethodNotAllowed)
		if strings.Contains(w.Body.String(), pageToken) {
			t.Errorf("%s: the body holds the token: %s", path, w.Body.String())
		}
	}
	if strings.Contains(e.Logs.String(), pageToken) {
		t.Fatalf("the log holds the token: %s", e.Logs.String())
	}
}

func TestPageHeadRequest(t *testing.T) {
	e := newTestEnv(t)
	requireStatus(t, e.do("HEAD", "/invite?token="+pageToken, nil), http.StatusNotFound)
}
