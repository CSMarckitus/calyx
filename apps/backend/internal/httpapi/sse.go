package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/clock"
	"calyx/backend/internal/core"
	"calyx/backend/internal/instances"
)

const pingInterval = 15 * time.Second

type shutdownKey struct{}

// WithShutdownSignal returns a context that carries done. A stream (pairing or events) ends when
// done is closed. cmd/backend uses it in http.Server.BaseContext: http.Server.Shutdown does not
// cancel the contexts of active requests, so a stream would keep the shutdown waiting.
func WithShutdownSignal(ctx context.Context, done <-chan struct{}) context.Context {
	return context.WithValue(ctx, shutdownKey{}, done)
}

func shutdownSignal(ctx context.Context) <-chan struct{} {
	done, _ := ctx.Value(shutdownKey{}).(<-chan struct{})
	return done // nil blocks forever in a select
}

// checkLimit is the time that the access check of a ping can take. A database that does not answer
// must not stop the stream: after this time the check counts as an error that is not 401.
const checkLimit = 5 * time.Second

// writeLimit is the time that one write of a stream can take. A client that does not read fills the
// buffers of the connection, and the write fails after this time.
const writeLimit = 10 * time.Second

// accessCheck runs at each ping of a stream. It answers false when the stream must end.
type accessCheck func(ctx context.Context) bool

type sseWriter struct {
	w          http.ResponseWriter
	rc         *http.ResponseController
	limit      time.Duration
	checkLimit time.Duration
}

// startSSE writes the headers and flushes them. It fails when the writer cannot flush.
func startSSE(w http.ResponseWriter) (*sseWriter, error) {
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if err := rc.Flush(); err != nil {
		return nil, err
	}
	return &sseWriter{w: w, rc: rc, limit: writeLimit, checkLimit: checkLimit}, nil
}

// event writes "event: <name>" and one line of JSON data, then flushes.
func (s *sseWriter) event(name string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return s.write(fmt.Sprintf("event: %s\ndata: %s\n\n", name, raw))
}

// ping writes a comment line that keeps the connection open.
func (s *sseWriter) ping() error { return s.write(": ping\n\n") }

// keepAlive runs the access check, then writes a ping. It answers false when the stream must end.
func (s *sseWriter) keepAlive(ctx context.Context, allowed accessCheck) bool {
	checkCtx, cancel := context.WithTimeout(ctx, s.checkLimit)
	defer cancel()
	return allowed(checkCtx) && s.ping() == nil
}

// write sends text and flushes it within the limit. The deadline is removed after a write that
// succeeded, so a quiet stream and the end of the answer have no limit. After a failed write it
// stays, so the end of the answer fails at once.
func (s *sseWriter) write(text string) error {
	if err := s.deadline(time.Now().Add(s.limit)); err != nil {
		return err
	}
	if _, err := io.WriteString(s.w, text); err != nil {
		return err
	}
	if err := s.rc.Flush(); err != nil {
		return err
	}
	return s.deadline(time.Time{})
}

// deadline sets the write deadline. A writer that has none (a test recorder) is not an error.
func (s *sseWriter) deadline(t time.Time) error {
	if err := s.rc.SetWriteDeadline(t); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	return nil
}

// doneStatus is the only status of the "done" event: the instance is connected.
type doneStatus string

const statusConnected = doneStatus(core.StatusConnected)

func (doneStatus) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return enumOf(s, []string{string(statusConnected)})
}

type doneJSON struct {
	Status doneStatus `json:"status" doc:"Always CONNECTED."`
}

type failedJSON struct {
	Code   pairingFailureCode `json:"code" doc:"pairing_expired (not finished in time), pairing_failed or pairing_cancelled."`
	Detail string             `json:"detail" doc:"A sentence for people that says why the pairing ended."`
}

// streamPairing writes events until the session ends, the client leaves or the server stops. A
// stream that ends because the server stops sends no event: the client reconnects.
func streamPairing(ctx context.Context, sse *sseWriter, events <-chan instances.PairingEvent, allowed accessCheck, clk clock.Clock) {
	stopping := shutdownSignal(ctx)
	timer := clk.NewTimer(pingInterval)
	defer func() { timer.Stop() }()
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return
			}
			var err error
			switch ev.Kind {
			case "update":
				err = sse.event("update", pairingResource(ev.Update))
			case "done":
				_ = sse.event("done", doneJSON{Status: statusConnected})
				return
			case "failed":
				_ = sse.event("failed", failedJSON{Code: pairingFailureCode(ev.FailCode), Detail: ev.Detail})
				return
			}
			if err != nil {
				return
			}
		case <-timer.C():
			timer = clk.NewTimer(pingInterval) // before the write, so a reader of the ping knows it exists
			if !sse.keepAlive(ctx, allowed) {
				return
			}
		case <-ctx.Done():
			return
		case <-stopping:
			return
		}
	}
}

// streamAccess is the access check of a stream. It runs auth.Check with the credentials of the
// request, which does not extend the session. The stream ends on 401, or when the user, the
// organization, the role or the client changed. Another error keeps the stream: a short database
// outage must not close every stream.
func (s *server) streamAccess(ctx context.Context) accessCheck {
	p, token, key := principalOf(ctx), sessionToken(ctx), bearerOf(ctx)
	return func(ctx context.Context) bool {
		now, err := s.Auth.Check(ctx, token, key)
		if err != nil {
			e, ok := apierr.As(err)
			return !ok || e.Status != http.StatusUnauthorized
		}
		return now.UserID == p.UserID && now.OrganizationID == p.OrganizationID && now.Role == p.Role &&
			sameClient(now.ClientID, p.ClientID)
	}
}

func sameClient(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
