package core

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/metadata"

	"calyx/backend/internal/apierr"
)

func TestUnaryInterceptorAddsTheToken(t *testing.T) {
	unary, _ := TokenInterceptors("abc")
	ctx := metadata.AppendToOutgoingContext(context.Background(), "x-request-id", "1")
	var md metadata.MD
	invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		md, _ = metadata.FromOutgoingContext(ctx)
		return nil
	}
	if err := unary(ctx, "/m", nil, nil, nil, invoker); err != nil {
		t.Fatal(err)
	}
	if got := md.Get("authorization"); len(got) != 1 || got[0] != "Bearer abc" {
		t.Errorf("authorization = %v", got)
	}
	if got := md.Get("x-request-id"); len(got) != 1 || got[0] != "1" {
		t.Errorf("x-request-id = %v", got)
	}
}

func TestStreamInterceptorAddsTheToken(t *testing.T) {
	_, stream := TokenInterceptors("abc")
	ctx := metadata.AppendToOutgoingContext(context.Background(), "x-request-id", "1")
	var md metadata.MD
	streamer := func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		md, _ = metadata.FromOutgoingContext(ctx)
		return nil, nil
	}
	if _, err := stream(ctx, &grpc.StreamDesc{}, nil, "/m", streamer); err != nil {
		t.Fatal(err)
	}
	if got := md.Get("authorization"); len(got) != 1 || got[0] != "Bearer abc" {
		t.Errorf("authorization = %v", got)
	}
	if got := md.Get("x-request-id"); len(got) != 1 || got[0] != "1" {
		t.Errorf("x-request-id = %v", got)
	}
}

func TestDialDoesNoIOAndFailsTheFirstCallFast(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	conn, err := Dial(addr, "t")
	if err != nil || conn == nil {
		t.Fatalf("Dial: %v, %v", conn, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, err = NewClient(conn).ListInstances(ctx)
	requireAPIError(t, err, 503, "core_unavailable")
	if time.Since(start) > 5*time.Second {
		t.Error("the first call was slow")
	}
	if err := conn.Close(); err != nil {
		t.Error(err)
	}
}

func requireAPIError(t *testing.T, err error, st int, code string) {
	t.Helper()
	e, ok := apierr.As(err)
	if !ok || e.Status != st || e.Code != code {
		t.Fatalf("want %d %s, got %v", st, code, err)
	}
}

func TestCreateOnAClosedPortHidesTheAddress(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	port := addr[strings.LastIndex(addr, ":")+1:]
	l.Close()
	conn, err := Dial(addr, "t")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = NewClient(conn).CreateInstance(ctx, InstanceConfig{
		CoreName: "i_a", Provider: "META_CLOUD",
		Meta: &MetaCredentials{PhoneNumberID: "1"},
	}, func(Update) {})
	requireAPIError(t, err, 503, "core_unavailable")
	e, _ := apierr.As(err)
	if strings.Contains(e.Detail, "127.0.0.1") || strings.Contains(e.Detail, port) || strings.Contains(e.Detail, "dial") {
		t.Errorf("detail leaks the address: %q", e.Detail)
	}
}

func TestConnectParamsCapTheWaitAt5Seconds(t *testing.T) {
	want := backoff.DefaultConfig
	want.MaxDelay = 5 * time.Second
	if connectParams.Backoff != want {
		t.Errorf("backoff = %+v, want %+v", connectParams.Backoff, want)
	}
	if connectParams.MinConnectTimeout != 20*time.Second {
		t.Errorf("MinConnectTimeout = %v", connectParams.MinConnectTimeout)
	}
}
