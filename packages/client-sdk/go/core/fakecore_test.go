package core

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"

	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

const testToken = "calyx_secret_core_token_456"

// fakeCall is what the fake core saw of one call.
type fakeCall struct {
	method   string
	md       metadata.MD
	req      proto.Message
	deadline time.Time // zero when the call had no deadline
}

// fakeCore is a small core in the test process. It records every call. The test sets unary
// with setUnary to control the answers of the unary calls.
type fakeCore struct {
	corev1.UnimplementedInstanceServiceServer
	corev1.UnimplementedMessageServiceServer
	corev1.UnimplementedEventServiceServer

	addr string

	mu    sync.Mutex
	calls []fakeCall
	// unary answers the unary calls. A nil answer with a nil error gives an empty response.
	unary func(ctx context.Context, method string, req proto.Message) (proto.Message, error)
	// stream answers the two streams. send gives one item to the client. A nil error ends the
	// stream with status OK. A nil stream ends every stream with status OK at once.
	stream func(ctx context.Context, method string, req proto.Message, send func(proto.Message) error) error
}

// newFakeCore starts a fake core on a 127.0.0.1 port. The test stops it at the end.
func newFakeCore(t *testing.T) *fakeCore {
	t.Helper()
	return newFakeCoreWith(t)
}

// newFakeCoreWith starts a fake core with extra server options, for example TLS credentials.
func newFakeCoreWith(t *testing.T, opts ...grpc.ServerOption) *fakeCore {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeCore{addr: lis.Addr().String()}
	srv := grpc.NewServer(opts...)
	corev1.RegisterInstanceServiceServer(srv, f)
	corev1.RegisterMessageServiceServer(srv, f)
	corev1.RegisterEventServiceServer(srv, f)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return f
}

// dial makes a connection of the program to the fake core, for NewFromConn.
func (f *fakeCore) dial(t *testing.T) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient(f.addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// newClient makes a client of the fake core with New and Insecure.
func (f *fakeCore) newClient(t *testing.T) *Client {
	t.Helper()
	c, err := New(Config{Address: f.addr, Token: testToken, Security: Insecure()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// setUnary sets the answers of the unary calls.
func (f *fakeCore) setUnary(fn func(ctx context.Context, method string, req proto.Message) (proto.Message, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unary = fn
}

// setStream sets the answers of the two streams.
func (f *fakeCore) setStream(fn func(ctx context.Context, method string, req proto.Message, send func(proto.Message) error) error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stream = fn
}

// recorded gives a copy of the calls that the fake core saw.
func (f *fakeCore) recorded() []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeCall(nil), f.calls...)
}

func (f *fakeCore) record(ctx context.Context, method string, req proto.Message) {
	md, _ := metadata.FromIncomingContext(ctx)
	deadline, _ := ctx.Deadline()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeCall{method: method, md: md, req: proto.Clone(req), deadline: deadline})
}

// handle records a unary call and gives the answer of f.unary.
func handle[Req, Res proto.Message](ctx context.Context, f *fakeCore, method string, req Req, empty Res) (Res, error) {
	f.record(ctx, method, req)
	f.mu.Lock()
	unary := f.unary
	f.mu.Unlock()
	if unary == nil {
		return empty, nil
	}
	res, err := unary(ctx, method, req)
	if err != nil {
		var zero Res
		return zero, err
	}
	if res == nil {
		return empty, nil
	}
	return res.(Res), nil
}

func (f *fakeCore) ListInstances(ctx context.Context, req *corev1.ListInstancesRequest) (*corev1.ListInstancesResponse, error) {
	return handle(ctx, f, "ListInstances", req, &corev1.ListInstancesResponse{})
}

func (f *fakeCore) GetInstanceStatus(ctx context.Context, req *corev1.GetInstanceStatusRequest) (*corev1.GetInstanceStatusResponse, error) {
	return handle(ctx, f, "GetInstanceStatus", req, &corev1.GetInstanceStatusResponse{})
}

func (f *fakeCore) RemoveInstance(ctx context.Context, req *corev1.RemoveInstanceRequest) (*corev1.RemoveInstanceResponse, error) {
	return handle(ctx, f, "RemoveInstance", req, &corev1.RemoveInstanceResponse{})
}

func (f *fakeCore) UpdateInstanceConfig(ctx context.Context, req *corev1.UpdateInstanceConfigRequest) (*corev1.UpdateInstanceConfigResponse, error) {
	return handle(ctx, f, "UpdateInstanceConfig", req, &corev1.UpdateInstanceConfigResponse{})
}

func (f *fakeCore) SendPlainText(ctx context.Context, req *corev1.SendPlainTextRequest) (*corev1.SendPlainTextResponse, error) {
	return handle(ctx, f, "SendPlainText", req, &corev1.SendPlainTextResponse{})
}

func (f *fakeCore) SendBulkSameMessage(ctx context.Context, req *corev1.SendBulkSameMessageRequest) (*corev1.BulkJob, error) {
	return handle(ctx, f, "SendBulkSameMessage", req, &corev1.BulkJob{})
}

func (f *fakeCore) SendBulkCustomMessages(ctx context.Context, req *corev1.SendBulkCustomMessagesRequest) (*corev1.BulkJob, error) {
	return handle(ctx, f, "SendBulkCustomMessages", req, &corev1.BulkJob{})
}

func (f *fakeCore) GetBulkJob(ctx context.Context, req *corev1.GetBulkJobRequest) (*corev1.BulkJob, error) {
	return handle(ctx, f, "GetBulkJob", req, &corev1.BulkJob{})
}

func (f *fakeCore) GetBulkJobs(ctx context.Context, req *corev1.GetBulkJobsRequest) (*corev1.GetBulkJobsResponse, error) {
	return handle(ctx, f, "GetBulkJobs", req, &corev1.GetBulkJobsResponse{})
}

func (f *fakeCore) CancelBulkJob(ctx context.Context, req *corev1.CancelBulkJobRequest) (*corev1.BulkJob, error) {
	return handle(ctx, f, "CancelBulkJob", req, &corev1.BulkJob{})
}

// handleStream records a stream call and runs f.stream.
func handleStream[Res any](f *fakeCore, method string, req proto.Message, ss grpc.ServerStreamingServer[Res]) error {
	ctx := ss.Context()
	f.record(ctx, method, req)
	f.mu.Lock()
	stream := f.stream
	f.mu.Unlock()
	if stream == nil {
		return nil
	}
	return stream(ctx, method, req, func(m proto.Message) error { return ss.Send(any(m).(*Res)) })
}

func (f *fakeCore) CreateInstance(req *corev1.CreateInstanceRequest, ss grpc.ServerStreamingServer[corev1.CreateInstanceResponse]) error {
	return handleStream(f, "CreateInstance", req, ss)
}

func (f *fakeCore) Subscribe(req *corev1.SubscribeRequest, ss grpc.ServerStreamingServer[corev1.Event]) error {
	return handleStream(f, "Subscribe", req, ss)
}
