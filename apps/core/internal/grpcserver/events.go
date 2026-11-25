package grpcserver

import (
	"log/slog"
	"slices"
	"sync/atomic"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"calyx/core/internal/events"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// How many events a slow client may fall behind before new ones are dropped for it.
const subscriberBuffer = 256

type eventService struct {
	corev1.UnimplementedEventServiceServer
	bus      *events.Bus
	stopping <-chan struct{}
	version  string
}

// Subscribe streams events from the moment of the call, at most once: a client that cannot keep
// up loses events instead of slowing down the core.
func (s *eventService) Subscribe(req *corev1.SubscribeRequest, stream grpc.ServerStreamingServer[corev1.Event]) error {
	instances, types := req.GetInstanceNames(), req.GetEventTypes()
	queue := make(chan events.Event, subscriberBuffer)
	var dropped atomic.Int64

	unsubscribe := s.bus.Subscribe(func(e events.Event) {
		if (len(instances) > 0 && !slices.Contains(instances, e.InstanceName)) ||
			(len(types) > 0 && !slices.Contains(types, e.Type)) {
			return
		}
		select {
		case queue <- e:
		default:
			dropped.Add(1)
		}
	})
	defer unsubscribe()

	// The headers tell the client that the subscription listens, so it loses no event after them.
	var header metadata.MD
	if s.version != "" {
		header = metadata.Pairs("calyx-version", s.version)
	}
	if err := stream.SendHeader(header); err != nil {
		return nil // the client left
	}

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case <-s.stopping:
			return status.Error(codes.Unavailable, "The core stops.")
		case e := <-queue:
			if lost := dropped.Swap(0); lost > 0 {
				slog.Warn("A slow event subscriber lost events.", "dropped", lost)
			}
			payload, err := events.SerializePayload(e.Payload)
			if err != nil {
				slog.Error("Could not serialize an event.", "type", e.Type, "err", err)
				continue
			}
			err = stream.Send(&corev1.Event{
				Id: e.ID, InstanceName: e.InstanceName, Type: e.Type,
				PayloadJson: payload, OccurredAt: timestamppb.New(e.OccurredAt),
			})
			if err != nil {
				return nil // the client left
			}
		}
	}
}
