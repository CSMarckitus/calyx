// Package grpcserver exposes the orchestrator through the calyx.core.v1 gRPC contract. Handlers
// only translate between protobuf and engine types; the rules live in the engine.
package grpcserver

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net"
	"sync"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/protoadapt"
	"google.golang.org/protobuf/types/known/durationpb"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/engine"
	"calyx/core/internal/events"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// MaxMessageBytes is the largest gRPC message that the server receives: 17 MiB. The backend
// accepts a bulk body of up to 16 MiB, and its protobuf form is not larger than its JSON form.
// The gRPC default is 4 MiB. The fake core of the backend tests uses the same value.
const MaxMessageBytes = 17 << 20

// The server pings idle connections, so a long stream through a proxy stays open. Clients may
// ping at most once a minute.
var (
	keepaliveParams = keepalive.ServerParameters{Time: 2 * time.Minute, Timeout: 20 * time.Second}
	keepalivePolicy = keepalive.EnforcementPolicy{MinTime: time.Minute}
)

// errorDomain is the domain of the google.rpc.ErrorInfo detail.
const errorDomain = "calyx.core.v1"

type Options struct {
	// When set, every call must carry the metadata "authorization: Bearer <token>". It is a shared
	// secret between the core and its clients, not users or tenants.
	AuthToken string
	// Version goes to the clients in the header "calyx-version" of Subscribe. Empty sends no header.
	Version string
}

type Server struct {
	grpc     *grpc.Server
	stopping chan struct{} // closed first by Stop, so the event streams end
	stopOnce sync.Once
}

func New(o *engine.Orchestrator, bus *events.Bus, opts Options) *Server {
	stopping := make(chan struct{})
	check := authChecker(opts.AuthToken)
	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(MaxMessageBytes),
		grpc.KeepaliveParams(keepaliveParams),
		grpc.KeepaliveEnforcementPolicy(keepalivePolicy),
		grpc.ChainUnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			if err := check(ctx); err != nil {
				return nil, err
			}
			resp, err := handler(ctx, req)
			return resp, toStatus(err, info.FullMethod)
		}),
		grpc.ChainStreamInterceptor(func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			if err := check(stream.Context()); err != nil {
				return err
			}
			return toStatus(handler(srv, stream), info.FullMethod)
		}),
	)
	corev1.RegisterInstanceServiceServer(server, &instanceService{o: o})
	corev1.RegisterMessageServiceServer(server, &messageService{o: o})
	corev1.RegisterEventServiceServer(server, &eventService{bus: bus, stopping: stopping, version: opts.Version})
	return &Server{grpc: server, stopping: stopping}
}

// Serve blocks until Stop.
func (s *Server) Serve(listener net.Listener) error {
	return s.grpc.Serve(listener)
}

// Stop ends the event streams with UNAVAILABLE, because they never end by themselves and would
// make each stop wait for the timeout. Then it lets running calls finish for up to timeout and
// cuts the rest. A second call is safe.
func (s *Server) Stop(timeout time.Duration) {
	s.stopOnce.Do(func() { close(s.stopping) })
	done := make(chan struct{})
	go func() {
		s.grpc.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		s.grpc.Stop()
	}
}

func authChecker(token string) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		if token == "" {
			return nil
		}
		md, _ := metadata.FromIncomingContext(ctx)
		for _, value := range md.Get("authorization") {
			if subtle.ConstantTimeCompare([]byte(value), []byte("Bearer "+token)) == 1 {
				return nil
			}
		}
		return status.Error(codes.Unauthenticated, "Missing or wrong authorization token.")
	}
}

// toStatus turns an error into a gRPC status. Domain errors keep their code and message; anything
// else is logged and answered as INTERNAL without its message, which may hold internal details.
func toStatus(err error, method string) error {
	if err == nil {
		return nil
	}
	if _, isStatus := status.FromError(err); isStatus {
		return err
	}
	var domain *calyxerr.Error
	if errors.As(err, &domain) {
		return domainStatus(domain)
	}
	if errors.Is(err, context.Canceled) {
		return status.Error(codes.Canceled, "The call was cancelled.")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.DeadlineExceeded, "The call ran out of time.")
	}
	slog.Error("Unexpected error in a gRPC call.", "method", method, "err", err)
	return status.Error(codes.Internal, "Internal error")
}

// domainStatus adds the ErrorInfo detail when the error has a reason, then the RetryInfo detail
// when it has a wait. If a detail cannot be added, the client still gets the code and the message.
func domainStatus(e *calyxerr.Error) error {
	st := status.New(e.Code, e.Message)
	var details []protoadapt.MessageV1
	if e.Reason != corev1.ErrorReason_ERROR_REASON_UNSPECIFIED {
		details = append(details, &errdetails.ErrorInfo{
			Reason:   e.Reason.String(),
			Domain:   errorDomain,
			Metadata: e.Metadata,
		})
	}
	if e.RetryAfter > 0 {
		details = append(details, &errdetails.RetryInfo{RetryDelay: durationpb.New(e.RetryAfter)})
	}
	if len(details) == 0 {
		return st.Err()
	}
	withDetails, err := st.WithDetails(details...)
	if err != nil {
		slog.Error("Cannot add the error detail to a status.", "err", err)
		return st.Err()
	}
	return withDetails.Err()
}
