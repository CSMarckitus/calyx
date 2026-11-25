package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"calyx/backend/internal/live"
)

type healthJSON struct {
	Database    string `json:"database"`
	Core        string `json:"core"`
	Mail        string `json:"mail"`
	Version     string `json:"version"`
	CoreVersion string `json:"core_version"` // empty before the first link, or from an older core
}

// healthz answers 503 when the database fails. It answers 200 with "core":"unreachable" when only
// the core fails, so the container is not restarted for a core outage. "mail":"configured" says
// only that an SMTP host is set, not that the mail server works. Both answers have the versions, so a
// person can report them during an outage.
func (s *server) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	mail := "off"
	if s.Config.MailConfigured() {
		mail = "configured"
	}
	if err := s.Pool.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, healthJSON{
			Database: "unavailable", Core: live.CoreUnknown, Mail: mail, Version: s.Version, CoreVersion: s.Live.CoreVersion(),
		})
		return
	}
	writeJSON(w, http.StatusOK, healthJSON{
		Database: "ok", Core: s.Live.Core(), Mail: mail, Version: s.Version, CoreVersion: s.Live.CoreVersion(),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
