package httpapi

import (
	"context"
	"testing"

	"calyx/backend/internal/config"
	"calyx/backend/internal/live"
)

// coreOK makes the health of the core ok: the link is up and a round of it succeeded.
func coreOK(l *live.Live) {
	l.SetCoreLink(true, "1.2.3")
	l.RoundOK(l.LinkNumber())
}

func mailOn(c *config.Config) { c.SMTP.Host = "smtp.calyx.test" }

// withVersion builds the handler again for a backend of the version v.
func withVersion(e *testEnv, v string) {
	e.Deps.Version = v
	e.Handler = NewServer(e.Deps)
}

func TestHealthzOK(t *testing.T) {
	e := newTestEnv(t)
	withVersion(e, "1.0.0")
	coreOK(e.Deps.Live)
	w := e.do("GET", "/healthz", nil)
	requireStatus(t, w, 200)
	m := decodeMap(t, w)
	if len(m) != 5 || m["database"] != "ok" || m["core"] != "ok" || m["mail"] != "off" ||
		m["version"] != "1.0.0" || m["core_version"] != "1.2.3" {
		t.Fatalf("body = %s", w.Body.String())
	}
	if w.Header().Get("Content-Type") != "application/json" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers = %v", w.Header())
	}
}

func TestHealthzMailConfigured(t *testing.T) {
	e := newTestEnv(t, mailOn)
	if m := decodeMap(t, e.do("GET", "/healthz", nil)); m["mail"] != "configured" {
		t.Fatalf("mail = %v", m["mail"])
	}
}

func TestHealthzCoreUnreachable(t *testing.T) {
	e := newTestEnv(t)
	coreOK(e.Deps.Live)
	e.Deps.Live.SetCoreLink(false, "")
	w := e.do("GET", "/healthz", nil)
	requireStatus(t, w, 200)
	m := decodeMap(t, w)
	if m["database"] != "ok" || m["core"] != "unreachable" || m["core_version"] != "1.2.3" {
		t.Fatalf("body = %s", w.Body.String())
	}
}

// Before the first link and the first round, the health and the version of the core are not known.
func TestHealthzCoreUnknownAtTheStart(t *testing.T) {
	e := newTestEnv(t)
	if m := decodeMap(t, e.do("GET", "/healthz", nil)); m["core"] != "unknown" || m["core_version"] != "" {
		t.Fatalf("core = %v, core_version = %v", m["core"], m["core_version"])
	}
	d := e.Deps
	d.Live = nil
	e.Handler = NewServer(d)
	if m := decodeMap(t, e.do("GET", "/healthz", nil)); m["core"] != "unknown" || m["core_version"] != "" {
		t.Fatalf("core without Live = %v, core_version = %v", m["core"], m["core_version"])
	}
}

// The stored unreachable_since does not decide the answer: Live does.
func TestHealthzDoesNotReadTheStoredCoreStatus(t *testing.T) {
	e := newTestEnv(t)
	coreOK(e.Deps.Live)
	if _, err := e.Deps.Pool.Exec(context.Background(), `INSERT INTO core_status (core_id, failed_checks, unreachable_since) VALUES ('main', 2, now())`); err != nil {
		t.Fatal(err)
	}
	if m := decodeMap(t, e.do("GET", "/healthz", nil)); m["core"] != "ok" {
		t.Fatalf("core = %v", m["core"])
	}
}

func TestHealthzDatabaseDown(t *testing.T) {
	e := newTestEnv(t, mailOn)
	withVersion(e, "1.0.0")
	coreOK(e.Deps.Live)
	e.Deps.Pool.Close()
	w := e.do("GET", "/healthz", nil)
	requireStatus(t, w, 503)
	m := decodeMap(t, w)
	if len(m) != 5 || m["database"] != "unavailable" || m["core"] != "unknown" || m["mail"] != "configured" ||
		m["version"] != "1.0.0" || m["core_version"] != "1.2.3" {
		t.Fatalf("body = %s", w.Body.String())
	}
}
