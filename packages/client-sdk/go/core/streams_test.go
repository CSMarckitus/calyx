package core

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/calyx"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// serverEnd is how long a test waits for the fake core to see the end of a stream.
const serverEnd = 5 * time.Second

// oneItemThenWait makes a fake stream that sends item, then waits until the call ends. It puts
// the method name in ended when the server context ends.
func oneItemThenWait(item proto.Message, ended chan<- string) func(context.Context, string, proto.Message, func(proto.Message) error) error {
	return func(ctx context.Context, method string, _ proto.Message, send func(proto.Message) error) error {
		if err := send(item); err != nil {
			return err
		}
		<-ctx.Done()
		ended <- method
		return ctx.Err()
	}
}

func validEvent() *corev1.Event {
	return &corev1.Event{
		Id:           "evt-1",
		InstanceName: "sales",
		Type:         "connection.update",
		PayloadJson:  `{"status":"CONNECTED"}`,
		OccurredAt:   timestamppb.New(time.Date(2026, 11, 20, 10, 30, 0, 123456789, time.UTC)),
	}
}

// waitEnd fails the test when the fake core does not see the end of method in time.
func waitEnd(t *testing.T, ended <-chan string, method string) {
	t.Helper()
	select {
	case got := <-ended:
		if got != method {
			t.Fatalf("the stream %s ended, want %s", got, method)
		}
	case <-time.After(serverEnd):
		t.Fatalf("the fake core did not see the end of %s in %s", method, serverEnd)
	}
}

func TestBreakCancelsTheStreamOnTheServer(t *testing.T) {
	f := newFakeCore(t)
	c := f.newClient(t)
	ended := make(chan string, 1)

	f.setStream(oneItemThenWait(&corev1.CreateInstanceResponse{Status: corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH}, ended))
	for res, err := range c.CreateInstance(context.Background(), &corev1.CreateInstanceRequest{InstanceName: "sales"}) {
		if err != nil || res.GetStatus() != corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH {
			t.Fatalf("CreateInstance item = %v, %v", res, err)
		}
		break
	}
	waitEnd(t, ended, "CreateInstance")

	f.setStream(oneItemThenWait(validEvent(), ended))
	for _, err := range c.Subscribe(context.Background(), Filter{}) {
		if err != nil {
			t.Fatalf("Subscribe error = %v", err)
		}
		break
	}
	waitEnd(t, ended, "Subscribe")
}

func TestPanicInTheLoopCancelsTheStream(t *testing.T) {
	f := newFakeCore(t)
	c := f.newClient(t)
	ended := make(chan string, 1)
	f.setStream(oneItemThenWait(validEvent(), ended))

	func() {
		defer func() {
			if r := recover(); r != "stop" {
				t.Fatalf("recover() = %v, want the panic of the loop body", r)
			}
		}()
		for _, err := range c.Subscribe(context.Background(), Filter{}) {
			if err != nil {
				t.Errorf("Subscribe error = %v", err)
			}
			panic("stop")
		}
	}()
	waitEnd(t, ended, "Subscribe")

	f.setStream(oneItemThenWait(&corev1.CreateInstanceResponse{}, ended))
	func() {
		defer func() { _ = recover() }()
		for _, err := range c.CreateInstance(context.Background(), &corev1.CreateInstanceRequest{}) {
			if err != nil {
				t.Errorf("CreateInstance error = %v", err)
			}
			panic("stop")
		}
	}()
	waitEnd(t, ended, "CreateInstance")
}

func TestSubscribeSendsTheFilter(t *testing.T) {
	f := newFakeCore(t)
	c := f.newClient(t)
	filter := Filter{InstanceNames: []string{"sales", "support"}, EventTypes: []string{"message.received"}}

	for _, err := range c.Subscribe(context.Background(), filter) {
		// The fake core ends the stream at once: the only item is stream_failed.
		_ = asCalyxError(t, err, calyx.CodeStreamFailed)
	}
	calls := f.recorded()
	if len(calls) != 1 || calls[0].method != "Subscribe" {
		t.Fatalf("calls = %v, want one Subscribe", calls)
	}
	want := &corev1.SubscribeRequest{InstanceNames: filter.InstanceNames, EventTypes: filter.EventTypes}
	if !proto.Equal(calls[0].req, want) {
		t.Fatalf("request = %v, want %v", calls[0].req, want)
	}
	if got := calls[0].md.Get("authorization"); len(got) != 1 || got[0] != "Bearer "+testToken {
		t.Fatalf("authorization = %v", got)
	}
}

// collect reads every item of a Subscribe loop.
func collect(t *testing.T, c *Client) ([]calyx.Event, []error) {
	t.Helper()
	var events []calyx.Event
	var errs []error
	for ev, err := range c.Subscribe(context.Background(), Filter{}) {
		if err != nil {
			errs = append(errs, err)
			continue
		}
		events = append(events, ev)
	}
	return events, errs
}

func TestSubscribeEndedByTheServer(t *testing.T) {
	cases := []struct {
		name string
		end  error
		code calyx.Code
	}{
		{"UNAVAILABLE", grpcstatus.Error(codes.Unavailable, "the core stops"), calyx.CodeGRPCUnavailable},
		{"status OK", nil, calyx.CodeStreamFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeCore(t)
			c := f.newClient(t)
			f.setStream(func(_ context.Context, _ string, _ proto.Message, send func(proto.Message) error) error {
				if err := send(validEvent()); err != nil {
					return err
				}
				return tc.end
			})
			events, errs := collect(t, c)
			if len(events) != 1 || len(errs) != 1 {
				t.Fatalf("got %d events and %d errors, want 1 and 1", len(events), len(errs))
			}
			e := asCalyxError(t, errs[0], tc.code)
			if strings.Contains(e.Error(), testToken) {
				t.Fatalf("the error text has the core token: %q", e.Error())
			}
		})
	}
}

func TestSubscribeItemsAreCalyxEvents(t *testing.T) {
	f := newFakeCore(t)
	c := f.newClient(t)
	second := validEvent()
	second.Id = "evt-2"
	second.Type = "whatsmeow.Receipt"
	second.PayloadJson = `{"any":[1,2]}`
	f.setStream(func(_ context.Context, _ string, _ proto.Message, send func(proto.Message) error) error {
		for _, ev := range []*corev1.Event{validEvent(), second} {
			if err := send(ev); err != nil {
				return err
			}
		}
		return nil
	})

	events, errs := collect(t, c)
	at := time.Date(2026, 11, 20, 10, 30, 0, 123456789, time.UTC)
	want := []calyx.Event{
		{ID: "evt-1", Event: "connection.update", Instance: "sales", Timestamp: at, Data: json.RawMessage(`{"status":"CONNECTED"}`)},
		{ID: "evt-2", Event: "whatsmeow.Receipt", Instance: "sales", Timestamp: at, Data: json.RawMessage(`{"any":[1,2]}`)},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %+v, want %+v", events, want)
	}
	if len(errs) != 1 {
		t.Fatalf("errors = %v, want only stream_failed", errs)
	}
	upd, err := events[0].ConnectionUpdate()
	if err != nil || upd.Status != calyx.ConnectionConnected {
		t.Fatalf("ConnectionUpdate() = %v, %v", upd, err)
	}
}

func TestSubscribeRefusesAnInvalidEvent(t *testing.T) {
	cases := map[string]func(*corev1.Event){
		"payload not JSON": func(e *corev1.Event) { e.PayloadJson = `{"status":` },
		"empty payload":    func(e *corev1.Event) { e.PayloadJson = "" },
		"no time":          func(e *corev1.Event) { e.OccurredAt = nil },
		"no id":            func(e *corev1.Event) { e.Id = "" },
		"no type":          func(e *corev1.Event) { e.Type = "" },
		"no instance":      func(e *corev1.Event) { e.InstanceName = "" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakeCore(t)
			c := f.newClient(t)
			ended := make(chan string, 1)
			bad := validEvent()
			change(bad)
			f.setStream(oneItemThenWait(bad, ended))

			events, errs := collect(t, c)
			if len(events) != 0 || len(errs) != 1 {
				t.Fatalf("got %d events and %d errors, want 0 and 1", len(events), len(errs))
			}
			if e := asCalyxError(t, errs[0], calyx.CodeInvalidEvent); e.Source != calyx.SourceClient {
				t.Fatalf("source = %q, want client", e.Source)
			}
			// The invalid event ends the loop, so the call must end on the server too.
			waitEnd(t, ended, "Subscribe")
		})
	}
}

func TestCreateInstanceEndedWithStatusOK(t *testing.T) {
	f := newFakeCore(t)
	c := f.newClient(t)
	f.setStream(func(_ context.Context, _ string, _ proto.Message, send func(proto.Message) error) error {
		for _, s := range []corev1.InstanceStatus{corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH, corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED} {
			if err := send(&corev1.CreateInstanceResponse{Status: s}); err != nil {
				return err
			}
		}
		return nil
	})

	var got []corev1.InstanceStatus
	for res, err := range c.CreateInstance(context.Background(), &corev1.CreateInstanceRequest{InstanceName: "sales"}) {
		if err != nil {
			t.Fatalf("CreateInstance error = %v", err)
		}
		got = append(got, res.GetStatus())
	}
	if len(got) != 2 || got[1] != corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED {
		t.Fatalf("statuses = %v", got)
	}
}

func TestCreateInstanceGivesTheCoreError(t *testing.T) {
	f := newFakeCore(t)
	c := f.newClient(t)
	f.setStream(func(context.Context, string, proto.Message, func(proto.Message) error) error {
		return grpcstatus.Error(codes.AlreadyExists, "the instance exists")
	})
	var errs []error
	for _, err := range c.CreateInstance(context.Background(), &corev1.CreateInstanceRequest{InstanceName: "sales"}) {
		errs = append(errs, err)
	}
	if len(errs) != 1 {
		t.Fatalf("items = %d, want one error", len(errs))
	}
	_ = asCalyxError(t, errs[0], calyx.CodeGRPCAlreadyExists)
}

func TestStreamsHaveNoDeadline(t *testing.T) {
	f := newFakeCore(t)
	c := f.newClient(t)
	for _, err := range c.CreateInstance(context.Background(), &corev1.CreateInstanceRequest{}) {
		t.Fatalf("CreateInstance item, error %v; the fake core ends the stream at once", err)
	}
	for _, err := range c.Subscribe(context.Background(), Filter{}) {
		// The only item is stream_failed.
		_ = asCalyxError(t, err, calyx.CodeStreamFailed)
	}
	calls := f.recorded()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	for _, call := range calls {
		if !call.deadline.IsZero() {
			t.Errorf("%s had the deadline %v, want none", call.method, call.deadline)
		}
	}
}

func TestStreamsCancelledByTheProgram(t *testing.T) {
	f := newFakeCore(t)
	c := f.newClient(t)
	ended := make(chan string, 1)
	f.setStream(oneItemThenWait(validEvent(), ended))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var errs []error
	for _, err := range c.Subscribe(ctx, Filter{}) {
		if err != nil {
			errs = append(errs, err)
			continue
		}
		cancel()
	}
	if len(errs) != 1 {
		t.Fatalf("errors = %v, want one", errs)
	}
	_ = asCalyxError(t, errs[0], calyx.CodeAborted)
	waitEnd(t, ended, "Subscribe")
}

func TestStreamsAfterClose(t *testing.T) {
	f := newFakeCore(t)
	c := f.newClient(t)
	_ = c.Close()
	items := 0
	for _, err := range c.CreateInstance(context.Background(), &corev1.CreateInstanceRequest{}) {
		_ = asCalyxError(t, err, calyx.CodeAborted)
		items++
	}
	for _, err := range c.Subscribe(context.Background(), Filter{}) {
		_ = asCalyxError(t, err, calyx.CodeAborted)
		items++
	}
	if items != 2 {
		t.Fatalf("items = %d, want one error of each stream", items)
	}
	if n := len(f.recorded()); n != 0 {
		t.Fatalf("the fake core saw %d calls, want 0", n)
	}
}

// A cancel in the loop body must win over the items that the stream already holds.
func TestCancelInTheLoopStopsBeforeTheNextItem(t *testing.T) {
	f := newFakeCore(t)
	c := f.newClient(t)
	ended := make(chan string, 1)
	f.setStream(func(ctx context.Context, method string, _ proto.Message, send func(proto.Message) error) error {
		defer func() { ended <- method }()
		for range 50 {
			if err := send(validEvent()); err != nil {
				return err
			}
		}
		<-ctx.Done()
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var events int
	var errs []error
	for _, err := range c.Subscribe(ctx, Filter{}) {
		if err != nil {
			errs = append(errs, err)
			continue
		}
		events++
		cancel()
		// Let the other items reach the buffer of the client.
		time.Sleep(50 * time.Millisecond)
	}
	if events != 1 {
		t.Fatalf("got %d events after the cancel, want 1", events)
	}
	if len(errs) != 1 {
		t.Fatalf("errors = %v, want one", errs)
	}
	_ = asCalyxError(t, errs[0], calyx.CodeAborted)
	waitEnd(t, ended, "Subscribe")
}

// A cancel before the end of the stream has priority over the end with status OK.
func TestCancelBeforeTheEndGivesAborted(t *testing.T) {
	f := newFakeCore(t)
	c := f.newClient(t)
	f.setStream(func(_ context.Context, _ string, _ proto.Message, send func(proto.Message) error) error {
		return send(validEvent())
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var errs []error
	for _, err := range c.Subscribe(ctx, Filter{}) {
		if err != nil {
			errs = append(errs, err)
			continue
		}
		cancel()
		// Let the end of the stream reach the client.
		time.Sleep(50 * time.Millisecond)
	}
	if len(errs) != 1 {
		t.Fatalf("errors = %v, want one", errs)
	}
	_ = asCalyxError(t, errs[0], calyx.CodeAborted)
}
