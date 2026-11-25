package main

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// start runs the fake core on a free loopback port and gives a client connection to it.
func start(t *testing.T) *grpc.ClientConn {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := newServer()
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// ctxWith adds the valid token and, when fakeCase is not empty, the x-fake-case metadata.
func ctxWith(t *testing.T, fakeCase string) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	md := metadata.Pairs("authorization", "Bearer test-token")
	if fakeCase != "" {
		md.Set("x-fake-case", fakeCase)
	}
	return metadata.NewOutgoingContext(ctx, md)
}

func send(idempotencyKey *string) *corev1.SendPlainTextRequest {
	return &corev1.SendPlainTextRequest{InstanceName: "main", To: "34600111222", Text: "hi", IdempotencyKey: idempotencyKey}
}

func key(s string) *string { return &s }

func TestNoCaseGivesNormalAnswers(t *testing.T) {
	conn := start(t)
	ctx := ctxWith(t, "")
	instances := corev1.NewInstanceServiceClient(conn)
	messages := corev1.NewMessageServiceClient(conn)

	if _, err := instances.ListInstances(ctx, &corev1.ListInstancesRequest{}); err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if _, err := instances.GetInstanceStatus(ctx, &corev1.GetInstanceStatusRequest{InstanceName: "main"}); err != nil {
		t.Fatalf("GetInstanceStatus: %v", err)
	}
	if _, err := instances.RemoveInstance(ctx, &corev1.RemoveInstanceRequest{InstanceName: "main"}); err != nil {
		t.Fatalf("RemoveInstance: %v", err)
	}
	if _, err := instances.UpdateInstanceConfig(ctx, &corev1.UpdateInstanceConfigRequest{InstanceName: "main"}); err != nil {
		t.Fatalf("UpdateInstanceConfig: %v", err)
	}
	res, err := messages.SendPlainText(ctx, send(key("k1")))
	if err != nil || res.GetMessageId() == "" {
		t.Fatalf("SendPlainText: %v, %v", res, err)
	}
	if _, err := messages.SendBulkSameMessage(ctx, &corev1.SendBulkSameMessageRequest{IdempotencyKey: key("k2")}); err != nil {
		t.Fatalf("SendBulkSameMessage: %v", err)
	}
	if _, err := messages.SendBulkCustomMessages(ctx, &corev1.SendBulkCustomMessagesRequest{IdempotencyKey: key("k3")}); err != nil {
		t.Fatalf("SendBulkCustomMessages: %v", err)
	}
	if _, err := messages.GetBulkJob(ctx, &corev1.GetBulkJobRequest{}); err != nil {
		t.Fatalf("GetBulkJob: %v", err)
	}
	if _, err := messages.GetBulkJobs(ctx, &corev1.GetBulkJobsRequest{}); err != nil {
		t.Fatalf("GetBulkJobs: %v", err)
	}
	if _, err := messages.CancelBulkJob(ctx, &corev1.CancelBulkJobRequest{}); err != nil {
		t.Fatalf("CancelBulkJob: %v", err)
	}
}

func TestAuthAndIdempotencyKey(t *testing.T) {
	conn := start(t)
	messages := corev1.NewMessageServiceClient(conn)

	_, err := messages.SendPlainText(context.Background(), send(key("k")))
	if got := status.Code(err); got != codes.Unauthenticated {
		t.Fatalf("no token: got %v, want UNAUTHENTICATED", got)
	}
	wrong := metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer other")
	_, err = messages.SendPlainText(wrong, send(key("k")))
	if got := status.Code(err); got != codes.Unauthenticated {
		t.Fatalf("wrong token: got %v, want UNAUTHENTICATED", got)
	}
	_, err = messages.SendPlainText(ctxWith(t, ""), send(nil))
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Fatalf("no key: got %v, want INVALID_ARGUMENT", got)
	}
}

func TestErrorInfoCase(t *testing.T) {
	conn := start(t)
	_, err := corev1.NewMessageServiceClient(conn).SendPlainText(ctxWith(t, "error-info"), send(key("k")))
	st := status.Convert(err)
	if st.Code() != codes.FailedPrecondition {
		t.Fatalf("got %v, want FAILED_PRECONDITION", st.Code())
	}
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok {
			if info.GetDomain() != "calyx.core.v1" || info.GetReason() == "" || info.GetMetadata()["at"] == "" {
				t.Fatalf("bad ErrorInfo: %v", info)
			}
			return
		}
	}
	t.Fatal("no ErrorInfo detail")
}

func TestRetryInfoCase(t *testing.T) {
	conn := start(t)
	_, err := corev1.NewMessageServiceClient(conn).SendPlainText(ctxWith(t, "retry-info"), send(key("k")))
	st := status.Convert(err)
	if st.Code() != codes.ResourceExhausted {
		t.Fatalf("got %v, want RESOURCE_EXHAUSTED", st.Code())
	}
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.RetryInfo); ok {
			if got := info.GetRetryDelay().AsDuration(); got != 7*time.Second {
				t.Fatalf("retry delay %v, want 7s", got)
			}
			return
		}
	}
	t.Fatal("no RetryInfo detail")
}

func TestCreateStreamCase(t *testing.T) {
	conn := start(t)
	stream, err := corev1.NewInstanceServiceClient(conn).CreateInstance(ctxWith(t, "create-stream"), &corev1.CreateInstanceRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var got []corev1.InstanceStatus
	for {
		msg, err := stream.Recv()
		if err != nil {
			break
		}
		got = append(got, msg.GetStatus())
	}
	want := []corev1.InstanceStatus{
		corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH,
		corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH,
		corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestSubscribeBreakCase(t *testing.T) {
	conn := start(t)
	stream, err := corev1.NewEventServiceClient(conn).Subscribe(ctxWith(t, "subscribe-break"), &corev1.SubscribeRequest{})
	if err != nil {
		t.Fatal(err)
	}
	events := 0
	for {
		_, err := stream.Recv()
		if err != nil {
			if status.Code(err) != codes.Unavailable {
				t.Fatalf("got %v, want UNAVAILABLE", err)
			}
			break
		}
		events++
	}
	if events != 2 {
		t.Fatalf("got %d events, want 2", events)
	}
}

func TestSubscribeEndCase(t *testing.T) {
	conn := start(t)
	stream, err := corev1.NewEventServiceClient(conn).Subscribe(ctxWith(t, "subscribe-end"), &corev1.SubscribeRequest{})
	if err != nil {
		t.Fatal(err)
	}
	events := 0
	for {
		_, err := stream.Recv()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatalf("got %v, want io.EOF (status OK)", err)
			}
			break
		}
		events++
	}
	if events != 2 {
		t.Fatalf("got %d events, want 2", events)
	}
}
