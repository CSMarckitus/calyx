package httpapi

import "net/http"

// The frontend serves the paths of the mail links. A request reaches the backend only when no
// frontend is installed, so the answer tells how to do the same with the API. It never repeats
// the token of the link.
const (
	inviteText = "The Calyx frontend is not installed at this address. To accept an invitation with the API, send the token of the link to POST /api/v1/invitations/accept (acceptInvitation)."
	resetText  = "The Calyx frontend is not installed at this address. To set a new password with the API, send the token of the link to POST /api/v1/auth/password-reset/confirm (confirmPasswordReset)."
	// The text keeps "{id}": it does not repeat a value of the request.
	instanceText = "The Calyx frontend is not installed at this address. To read an instance with the API, call GET /api/v1/instances/{id} (getInstance)."
)

func pageText(text string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "text/plain; charset=utf-8")
		h.Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(text + "\n"))
	}
}
