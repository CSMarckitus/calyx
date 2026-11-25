package core_test

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"calyx/backend/internal/core"
	"calyx/backend/internal/testutil"
	corev1 "github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// runSubscribe starts SubscribeConnections in a goroutine. It sends the names of the events to
// events, and the core version of onReady to ready.
func runSubscribe(ctx context.Context, c *core.Client, wait time.Duration) (<-chan string, <-chan string, <-chan error) {
	events := make(chan string, 16)
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- c.SubscribeConnections(ctx, wait, func(coreVersion string) { ready <- coreVersion }, func(n string) { events <- n })
	}()
	return events, ready, done
}

func waitFor[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout: %s", what)
		panic("unreachable")
	}
}

func TestSubscribeConnectionsReportsReadyAndEvents(t *testing.T) {
	fc, c := newClient(t)
	events, ready, done := runSubscribe(context.Background(), c, 5*time.Second)
	waitFor(t, ready, "ready")
	fc.PublishConnection("i_a", corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED)
	fc.PublishConnection("i_b", corev1.InstanceStatus_INSTANCE_STATUS_DISCONNECTED)
	if got := waitFor(t, events, "event 1"); got != "i_a" {
		t.Errorf("event 1 = %q", got)
	}
	if got := waitFor(t, events, "event 2"); got != "i_b" {
		t.Errorf("event 2 = %q", got)
	}
	reqs := fc.SubscribeRequests()
	if len(reqs) != 1 || len(reqs[0].EventTypes) != 1 || reqs[0].EventTypes[0] != "connection.update" || len(reqs[0].InstanceNames) != 0 {
		t.Errorf("request = %v", reqs)
	}
	fc.EndSubscriptions()
	if err := waitFor(t, done, "end"); status.Code(err) != codes.Unavailable {
		t.Errorf("end = %v, want UNAVAILABLE", err)
	}
}

func TestSubscribeConnectionsReadyComesBeforeAnyEventAndOnce(t *testing.T) {
	fc, c := newClient(t)
	var ready atomic.Int32
	events := make(chan string, 4)
	go func() {
		_ = c.SubscribeConnections(context.Background(), 5*time.Second,
			func(string) { ready.Add(1) }, func(n string) { events <- n })
	}()
	fc.WaitForRequests(t, testutil.MethodSubscribe, 1)
	waitUntil(t, func() bool { return ready.Load() == 1 })
	fc.PublishConnection("i_a", corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED)
	fc.PublishConnection("i_a", corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED)
	waitFor(t, events, "event")
	waitFor(t, events, "event")
	if ready.Load() != 1 {
		t.Errorf("onReady ran %d times", ready.Load())
	}
}

func waitUntil(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSubscribeConnectionsGivesErrNoHeaders(t *testing.T) {
	fc, c := newClient(t)
	fc.SetSubscribeHeaders(false)
	start := time.Now()
	_, ready, done := runSubscribe(context.Background(), c, 100*time.Millisecond)
	err := waitFor(t, done, "return")
	if !errors.Is(err, core.ErrNoHeaders) {
		t.Fatalf("err = %v, want ErrNoHeaders", err)
	}
	if d := time.Since(start); d < 90*time.Millisecond || d > 2*time.Second {
		t.Errorf("waited %v", d)
	}
	select {
	case <-ready:
		t.Error("onReady ran")
	default:
	}
}

func TestSubscribeConnectionsReturnsWhenTheCoreIsUnavailable(t *testing.T) {
	fc, c := newClient(t)
	fc.SetUnavailable(true)
	_, ready, done := runSubscribe(context.Background(), c, 5*time.Second)
	if err := waitFor(t, done, "return"); status.Code(err) != codes.Unavailable {
		t.Errorf("err = %v, want UNAVAILABLE", err)
	}
	select {
	case <-ready:
		t.Error("onReady ran")
	default:
	}
}

func TestSubscribeConnectionsReturnsWhenTheContextEnds(t *testing.T) {
	_, c := newClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	_, ready, done := runSubscribe(ctx, c, 5*time.Second)
	waitFor(t, ready, "ready")
	cancel()
	if err := waitFor(t, done, "return"); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// refusingEvents answers Subscribe with UNAUTHENTICATED, as a core does with a wrong token.
type refusingEvents struct {
	corev1.UnimplementedEventServiceServer
}

func (refusingEvents) Subscribe(*corev1.SubscribeRequest, grpc.ServerStreamingServer[corev1.Event]) error {
	return status.Error(codes.Unauthenticated, "no")
}

func TestSubscribeConnectionsTreatsARefusedTokenAsAFailure(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	corev1.RegisterEventServiceServer(srv, refusingEvents{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := core.Dial(lis.Addr().String(), "wrong")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	_, ready, done := runSubscribe(context.Background(), core.NewClient(conn), 5*time.Second)
	if err := waitFor(t, done, "return"); status.Code(err) != codes.Unauthenticated {
		t.Errorf("err = %v, want UNAUTHENTICATED", err)
	}
	select {
	case <-ready:
		t.Error("onReady ran")
	default:
	}
}

func TestSubscribeConnectionsPassesTheCoreVersionToOnReady(t *testing.T) {
	fc, c := newClient(t)
	fc.SetVersion("1.2.3")
	_, ready, _ := runSubscribe(context.Background(), c, 5*time.Second)
	if got := waitFor(t, ready, "ready"); got != "1.2.3" {
		t.Errorf("core version = %q, want 1.2.3", got)
	}
}

func TestSubscribeConnectionsGivesAnEmptyVersionWithoutTheHeader(t *testing.T) {
	_, c := newClient(t) // an older core sends no calyx-version header
	_, ready, _ := runSubscribe(context.Background(), c, 5*time.Second)
	if got := waitFor(t, ready, "ready"); got != "" {
		t.Errorf("core version = %q, want empty", got)
	}
}
