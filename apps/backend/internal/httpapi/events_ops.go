package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"calyx/backend/internal/clock"
	"calyx/backend/internal/live"
	"calyx/backend/internal/tenancy"
)

// eventsStreamDescription is the description of the 200 answer of the stream of changes.
const eventsStreamDescription = "Events: changed (data: {}) and replaced (data: {}). The stream sends one changed " +
	"at its start. After changed, read the screen again: getMe, the instances and /healthz. After replaced, " +
	"close the stream and do not connect again immediately: a newer stream of the same user took its place. " +
	"A comment line \": ping\" comes every 15 seconds. The stream ends with no event when the access of the " +
	"login ends or the server stops."

type changedJSON struct{}

type replacedJSON struct{}

// eventsOfStream maps each event name of the stream of changes to the type of its data.
var eventsOfStream = map[string]any{"changed": changedJSON{}, "replaced": replacedJSON{}}

func (s *server) registerEventOps(api huma.API) {
	huma.Register(api, withAccess(withEventStream(api, huma.Operation{
		OperationID: "followEvents", Tags: []string{"Events"}, Method: http.MethodGet, Path: "/api/v1/events",
		Summary: "Follow the changes that the caller can see, as a stream of server-sent events.",
		Description: "Only for a login in a browser. Errors: 401 unauthenticated, 403 api_key_not_allowed. " +
			"The 401 and 403 answers are problems, sent before any stream header.",
	}, eventsStreamDescription, eventsOfStream), accessLoggedInCookie, tenancy.UseInstances), s.followEvents)
}

// GET /api/v1/events. The stream subscribes first and then sends changed, so no change between the first
// read of the browser and the subscription is lost.
func (s *server) followEvents(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
	p, allowed := principalOf(ctx), s.streamAccess(ctx)
	return &huma.StreamResponse{Body: func(hctx huma.Context) {
		sub := s.Live.Subscribe(p)
		defer sub.Close()
		_, w := humago.Unwrap(hctx)
		sse, err := startSSE(w)
		if err != nil {
			s.log.Error("The response writer cannot flush.", "request_id", requestIDOf(hctx.Context()), "error", err)
			return
		}
		streamEvents(hctx.Context(), sse, sub, allowed, s.Clock)
	}}, nil
}

// streamEvents writes changed at the start and after each notification, until the stream is
// replaced, the access ends, the client leaves or the server stops.
func streamEvents(ctx context.Context, sse *sseWriter, sub *live.Subscription, allowed accessCheck, clk clock.Clock) {
	stopping := shutdownSignal(ctx)
	timer := clk.NewTimer(pingInterval) // before the first write, so a reader of changed knows it exists
	defer func() { timer.Stop() }()
	if sse.event("changed", changedJSON{}) != nil {
		return
	}
	for {
		select {
		case <-sub.Changed():
			if sse.event("changed", changedJSON{}) != nil {
				return
			}
		case <-sub.Replaced():
			_ = sse.event("replaced", replacedJSON{})
			return
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
