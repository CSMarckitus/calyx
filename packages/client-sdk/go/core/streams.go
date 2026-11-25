package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"iter"

	"google.golang.org/grpc"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/calyx"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// CreateInstance creates an instance, or pairs it again, and gives each pairing update. The
// call starts when the loop starts, and each loop is a new call. The stream has no deadline.
// The end of the loop (break, return or panic) cancels the call: the pairing stops and the
// instance stays NEEDS_AUTH. An error is the last item. The SDK never opens the stream again.
func (c *Client) CreateInstance(ctx context.Context, req *corev1.CreateInstanceRequest) iter.Seq2[*corev1.CreateInstanceResponse, error] {
	return func(yield func(*corev1.CreateInstanceResponse, error) bool) {
		_, err := stream(ctx, c, req, c.instances.CreateInstance, func(res *corev1.CreateInstanceResponse) bool {
			return yield(res, nil)
		})
		if err != nil {
			yield(nil, err)
		}
	}
}

// Subscribe gives the events of the core that match filter, from the moment of the call. The
// call starts when the loop starts, and each loop is a new call. The stream has no deadline.
// The end of the loop (break, return or panic) cancels the call. An error is the last item:
// the core never ends the stream by itself, so an end with status OK gives stream_failed. An
// event that is not valid gives invalid_event. The SDK never opens the stream again.
func (c *Client) Subscribe(ctx context.Context, filter Filter) iter.Seq2[calyx.Event, error] {
	return func(yield func(calyx.Event, error) bool) {
		req := &corev1.SubscribeRequest{InstanceNames: filter.InstanceNames, EventTypes: filter.EventTypes}
		var invalid error
		endedOK, err := stream(ctx, c, req, c.events.Subscribe, func(e *corev1.Event) bool {
			ev, err := toEvent(e)
			if err != nil {
				invalid = err
				return false
			}
			return yield(ev, nil)
		})
		switch {
		case invalid != nil:
			yield(calyx.Event{}, invalid)
		case err != nil:
			yield(calyx.Event{}, err)
		case endedOK:
			yield(calyx.Event{}, clientError(calyx.CodeStreamFailed, "The core ended the event stream.", "", nil))
		}
	}
}

// stream runs one server stream and gives each item to each until each returns false. It
// returns true when the core ended the stream with status OK. The stream runs on a context
// derived from ctx, so the return, also by a panic in each, cancels the call on the core.
func stream[Req, Res any](ctx context.Context, c *Client, req Req,
	open func(context.Context, Req, ...grpc.CallOption) (grpc.ServerStreamingClient[Res], error),
	each func(*Res) bool,
) (endedOK bool, err error) {
	ctx, cerr := c.outgoing(ctx, "")
	if cerr != nil {
		return false, cerr
	}
	// No default limit: a stream has no deadline.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	s, err := open(ctx, req)
	for err == nil {
		var item *Res
		item, err = s.Recv()
		// An abort has priority over a buffered item and over the end of the stream.
		if ctx.Err() != nil {
			err = ctx.Err()
			break
		}
		if errors.Is(err, io.EOF) {
			return true, nil
		}
		if err == nil && !each(item) {
			return false, nil
		}
	}
	// Close during the stream makes grpc-go give CANCELLED; the table says aborted.
	if c.closed.Load() {
		return false, closedError("")
	}
	return false, convertError(ctx, err, "")
}

// toEvent converts an event of the stream to the envelope of a webhook (CONTRACT.md part 10,
// rule 5). It refuses the same events as the webhook check.
func toEvent(e *corev1.Event) (calyx.Event, error) {
	switch {
	case e.GetId() == "" || e.GetType() == "" || e.GetInstanceName() == "":
		return calyx.Event{}, clientError(calyx.CodeInvalidEvent, "The event has no id, name or instance.", "", nil)
	case e.GetOccurredAt() == nil || e.GetOccurredAt().CheckValid() != nil:
		return calyx.Event{}, clientError(calyx.CodeInvalidEvent, "The event has no valid time.", "", nil)
	case !json.Valid([]byte(e.GetPayloadJson())):
		return calyx.Event{}, clientError(calyx.CodeInvalidEvent, "The payload of the event is not valid JSON.", "", nil)
	}
	return calyx.Event{
		ID:        e.GetId(),
		Event:     e.GetType(),
		Instance:  e.GetInstanceName(),
		Timestamp: e.GetOccurredAt().AsTime(),
		Data:      json.RawMessage(e.GetPayloadJson()),
	}, nil
}
