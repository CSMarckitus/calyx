package main

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/protoadapt"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

const (
	token      = "test-token"
	caseHeader = "x-fake-case"
)

// newServer makes the gRPC server with the three services and the two interceptors.
func newServer() *grpc.Server {
	srv := grpc.NewServer(
		grpc.UnaryInterceptor(unaryGate),
		grpc.StreamInterceptor(streamGate),
		// The short pings of the keepalive tests are no reason to close a connection.
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: time.Millisecond}),
	)
	f := &fakeCore{}
	corev1.RegisterInstanceServiceServer(srv, f)
	corev1.RegisterMessageServiceServer(srv, f)
	corev1.RegisterEventServiceServer(srv, f)
	return srv
}

// gate checks the token, then gives the error of the error cases. The two stream cases are not
// errors of the call, so the handlers answer them.
func gate(ctx context.Context) error {
	md, _ := metadata.FromIncomingContext(ctx)
	if v := md.Get("authorization"); len(v) != 1 || v[0] != "Bearer "+token {
		return status.Error(codes.Unauthenticated, "missing or wrong token")
	}
	switch fakeCase(ctx) {
	case "error-info":
		return withDetails(codes.FailedPrecondition, "outcome of the send is unknown", &errdetails.ErrorInfo{
			Domain:   "calyx.core.v1",
			Reason:   corev1.ErrorReason_ERROR_REASON_SEND_OUTCOME_UNKNOWN.String(),
			Metadata: map[string]string{"at": "2026-01-01T00:00:00Z"},
		})
	case "retry-info":
		return withDetails(codes.ResourceExhausted, "too many messages", &errdetails.RetryInfo{RetryDelay: durationpb.New(7 * time.Second)})
	}
	return nil
}

func fakeCase(ctx context.Context) string {
	md, _ := metadata.FromIncomingContext(ctx)
	if v := md.Get(caseHeader); len(v) > 0 {
		return v[0]
	}
	return ""
}

func withDetails(code codes.Code, msg string, detail protoadapt.MessageV1) error {
	st, err := status.New(code, msg).WithDetails(detail)
	if err != nil {
		// A test fake fails fast: a status without its details would hide the error.
		panic(err)
	}
	return st.Err()
}

func unaryGate(ctx context.Context, req any, _ *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
	if err := gate(ctx); err != nil {
		return nil, err
	}
	return next(ctx, req)
}

func streamGate(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, next grpc.StreamHandler) error {
	if err := gate(ss.Context()); err != nil {
		return err
	}
	return next(srv, ss)
}

// fakeCore answers every RPC of the three services.
type fakeCore struct {
	corev1.UnimplementedInstanceServiceServer
	corev1.UnimplementedMessageServiceServer
	corev1.UnimplementedEventServiceServer
}

func (*fakeCore) CreateInstance(_ *corev1.CreateInstanceRequest, ss grpc.ServerStreamingServer[corev1.CreateInstanceResponse]) error {
	if fakeCase(ss.Context()) != "create-stream" {
		return ss.Send(&corev1.CreateInstanceResponse{Status: corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED})
	}
	for _, code := range []string{"QR-1", "QR-2"} {
		res := &corev1.CreateInstanceResponse{Status: corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH, PairingCode: &code}
		if err := ss.Send(res); err != nil {
			return err
		}
	}
	return ss.Send(&corev1.CreateInstanceResponse{Status: corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED})
}

func (*fakeCore) RemoveInstance(context.Context, *corev1.RemoveInstanceRequest) (*corev1.RemoveInstanceResponse, error) {
	return &corev1.RemoveInstanceResponse{}, nil
}

func (*fakeCore) UpdateInstanceConfig(context.Context, *corev1.UpdateInstanceConfigRequest) (*corev1.UpdateInstanceConfigResponse, error) {
	return &corev1.UpdateInstanceConfigResponse{}, nil
}

func (*fakeCore) ListInstances(context.Context, *corev1.ListInstancesRequest) (*corev1.ListInstancesResponse, error) {
	return &corev1.ListInstancesResponse{}, nil
}

func (*fakeCore) GetInstanceStatus(context.Context, *corev1.GetInstanceStatusRequest) (*corev1.GetInstanceStatusResponse, error) {
	return &corev1.GetInstanceStatusResponse{}, nil
}

func needKey(key string) error {
	if key == "" {
		return status.Error(codes.InvalidArgument, "idempotency_key is required")
	}
	return nil
}

func (*fakeCore) SendPlainText(_ context.Context, req *corev1.SendPlainTextRequest) (*corev1.SendPlainTextResponse, error) {
	if err := needKey(req.GetIdempotencyKey()); err != nil {
		return nil, err
	}
	return &corev1.SendPlainTextResponse{MessageId: "fake-message-1"}, nil
}

func (*fakeCore) SendBulkSameMessage(_ context.Context, req *corev1.SendBulkSameMessageRequest) (*corev1.BulkJob, error) {
	if err := needKey(req.GetIdempotencyKey()); err != nil {
		return nil, err
	}
	return &corev1.BulkJob{}, nil
}

func (*fakeCore) SendBulkCustomMessages(_ context.Context, req *corev1.SendBulkCustomMessagesRequest) (*corev1.BulkJob, error) {
	if err := needKey(req.GetIdempotencyKey()); err != nil {
		return nil, err
	}
	return &corev1.BulkJob{}, nil
}

func (*fakeCore) GetBulkJob(context.Context, *corev1.GetBulkJobRequest) (*corev1.BulkJob, error) {
	return &corev1.BulkJob{}, nil
}

func (*fakeCore) GetBulkJobs(context.Context, *corev1.GetBulkJobsRequest) (*corev1.GetBulkJobsResponse, error) {
	return &corev1.GetBulkJobsResponse{}, nil
}

func (*fakeCore) CancelBulkJob(context.Context, *corev1.CancelBulkJobRequest) (*corev1.BulkJob, error) {
	return &corev1.BulkJob{}, nil
}

func (*fakeCore) Subscribe(_ *corev1.SubscribeRequest, ss grpc.ServerStreamingServer[corev1.Event]) error {
	c := fakeCase(ss.Context())
	n := 1
	if c == "subscribe-break" || c == "subscribe-end" {
		n = 2
	}
	for i := range n {
		ev := &corev1.Event{Id: fmt.Sprintf("event-%d", i+1), InstanceName: "main", Type: "connection.update", PayloadJson: `{"status":"CONNECTED"}`, OccurredAt: timestamppb.Now()}
		if err := ss.Send(ev); err != nil {
			return err
		}
	}
	if c == "subscribe-end" {
		return nil
	}
	if c == "subscribe-break" {
		return status.Error(codes.Unavailable, "stream broken")
	}
	<-ss.Context().Done()
	return nil
}
