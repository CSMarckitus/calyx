package core

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/calyx"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

func TestEveryRPCHasAClientMethod(t *testing.T) {
	services := []protoreflect.ServiceDescriptor{
		corev1.File_calyx_core_v1_instance_proto.Services().Get(0),
		corev1.File_calyx_core_v1_message_proto.Services().Get(0),
		corev1.File_calyx_core_v1_event_proto.Services().Get(0),
	}
	typ := reflect.TypeOf(&Client{})
	count := 0
	for _, svc := range services {
		for i := 0; i < svc.Methods().Len(); i++ {
			name := string(svc.Methods().Get(i).Name())
			count++
			if _, ok := typ.MethodByName(name); !ok {
				t.Errorf("core.Client has no method %s", name)
			}
		}
	}
	if count != 12 {
		t.Errorf("the services have %d RPCs, want 12", count)
	}
}

func TestSendCallsKeepTheKeyOfTheProgram(t *testing.T) {
	f := newFakeCore(t)
	c := f.newClient(t)
	ctx := context.Background()
	key := "my-key"

	p, err := c.SendPlainText(ctx, &corev1.SendPlainTextRequest{InstanceName: "a", IdempotencyKey: &key})
	if err != nil || p.IdempotencyKey != key {
		t.Fatalf("SendPlainText key = %q, err %v", p.IdempotencyKey, err)
	}
	s, err := c.SendBulkSameMessage(ctx, &corev1.SendBulkSameMessageRequest{InstanceName: "a", IdempotencyKey: &key})
	if err != nil || s.IdempotencyKey != key {
		t.Fatalf("SendBulkSameMessage key = %q, err %v", s.IdempotencyKey, err)
	}
	u, err := c.SendBulkCustomMessages(ctx, &corev1.SendBulkCustomMessagesRequest{InstanceName: "a", IdempotencyKey: &key})
	if err != nil || u.IdempotencyKey != key {
		t.Fatalf("SendBulkCustomMessages key = %q, err %v", u.IdempotencyKey, err)
	}
	for _, call := range f.recorded() {
		var got string
		switch r := call.req.(type) {
		case *corev1.SendPlainTextRequest:
			got = r.GetIdempotencyKey()
		case *corev1.SendBulkSameMessageRequest:
			got = r.GetIdempotencyKey()
		case *corev1.SendBulkCustomMessagesRequest:
			got = r.GetIdempotencyKey()
		}
		if got != key {
			t.Errorf("%s: the core got key %q, want %q", call.method, got, key)
		}
	}
}

func TestSendCallsMakeAKeyAndDoNotChangeTheRequest(t *testing.T) {
	f := newFakeCore(t)
	c := f.newClient(t)
	ctx := context.Background()

	plain := &corev1.SendPlainTextRequest{InstanceName: "a", To: "1"}
	same := &corev1.SendBulkSameMessageRequest{InstanceName: "a"}
	custom := &corev1.SendBulkCustomMessagesRequest{InstanceName: "a"}
	clones := []proto.Message{proto.Clone(plain), proto.Clone(same), proto.Clone(custom)}

	p, err := c.SendPlainText(ctx, plain)
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.SendBulkSameMessage(ctx, same)
	if err != nil {
		t.Fatal(err)
	}
	u, err := c.SendBulkCustomMessages(ctx, custom)
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range []proto.Message{plain, same, custom} {
		if !proto.Equal(m, clones[i]) {
			t.Errorf("request %d changed: %v", i, m)
		}
	}
	keys := []string{p.IdempotencyKey, s.IdempotencyKey, u.IdempotencyKey}
	calls := f.recorded()
	for i, k := range keys {
		if k == "" {
			t.Errorf("call %d: empty key in Sent", i)
		}
		var got string
		switch r := calls[i].req.(type) {
		case *corev1.SendPlainTextRequest:
			got = r.GetIdempotencyKey()
		case *corev1.SendBulkSameMessageRequest:
			got = r.GetIdempotencyKey()
		case *corev1.SendBulkCustomMessagesRequest:
			got = r.GetIdempotencyKey()
		}
		if got != k {
			t.Errorf("call %d: the core got %q, Sent has %q", i, got, k)
		}
	}
	if keys[0] == keys[1] || keys[1] == keys[2] {
		t.Errorf("keys are not new each time: %v", keys)
	}
}

func TestSendCallsPutTheKeyInEveryError(t *testing.T) {
	f := newFakeCore(t)
	f.setUnary(func(context.Context, string, proto.Message) (proto.Message, error) {
		return nil, grpcstatus.Error(codes.Unavailable, "down")
	})
	c := f.newClient(t)
	ctx := context.Background()

	_, err := c.SendPlainText(ctx, &corev1.SendPlainTextRequest{InstanceName: "a"})
	e := asCalyxError(t, err, calyx.CodeGRPCUnavailable)
	if e.IdempotencyKey == "" {
		t.Error("SendPlainText: no key in the error")
	}
	key := "k1"
	_, err = c.SendBulkSameMessage(ctx, &corev1.SendBulkSameMessageRequest{IdempotencyKey: &key})
	if e := asCalyxError(t, err, calyx.CodeGRPCUnavailable); e.IdempotencyKey != key {
		t.Errorf("key = %q, want %q", e.IdempotencyKey, key)
	}
	_, err = c.SendBulkCustomMessages(ctx, &corev1.SendBulkCustomMessagesRequest{})
	if e := asCalyxError(t, err, calyx.CodeGRPCUnavailable); e.IdempotencyKey == "" {
		t.Error("SendBulkCustomMessages: no key in the error")
	}
	// An error before the call (a closed client) carries the key too.
	_ = c.Close()
	_, err = c.SendPlainText(ctx, &corev1.SendPlainTextRequest{IdempotencyKey: &key})
	if e := asCalyxError(t, err, calyx.CodeAborted); e.IdempotencyKey != key {
		t.Errorf("closed: key = %q, want %q", e.IdempotencyKey, key)
	}
}

// A shared request must not cause a data race (run with -race).
func TestSendCallsShareARequestWithoutARace(t *testing.T) {
	f := newFakeCore(t)
	c := f.newClient(t)
	req := &corev1.SendPlainTextRequest{InstanceName: "a", To: "1"}
	var wg sync.WaitGroup
	keys := make([]string, 8)
	for i := range keys {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := c.SendPlainText(context.Background(), req)
			if err != nil {
				t.Error(err)
				return
			}
			keys[i] = s.IdempotencyKey
		}()
	}
	wg.Wait()
	seen := map[string]bool{}
	for _, k := range keys {
		if k == "" || seen[k] {
			t.Errorf("keys are empty or equal: %v", keys)
			break
		}
		seen[k] = true
	}
	if req.IdempotencyKey != nil {
		t.Error("the shared request changed")
	}
}

func TestDefaultLimitsOfTheUnaryCalls(t *testing.T) {
	f := newFakeCore(t)
	c := f.newClient(t)
	ctx := context.Background()

	_, _ = c.ListInstances(ctx, &corev1.ListInstancesRequest{})
	_, _ = c.GetInstanceStatus(ctx, &corev1.GetInstanceStatusRequest{})
	_, _ = c.RemoveInstance(ctx, &corev1.RemoveInstanceRequest{})
	_, _ = c.UpdateInstanceConfig(ctx, &corev1.UpdateInstanceConfigRequest{})
	_, _ = c.SendPlainText(ctx, &corev1.SendPlainTextRequest{})
	_, _ = c.SendBulkSameMessage(ctx, &corev1.SendBulkSameMessageRequest{})
	_, _ = c.SendBulkCustomMessages(ctx, &corev1.SendBulkCustomMessagesRequest{})
	_, _ = c.GetBulkJob(ctx, &corev1.GetBulkJobRequest{})
	_, _ = c.GetBulkJobs(ctx, &corev1.GetBulkJobsRequest{})
	_, _ = c.CancelBulkJob(ctx, &corev1.CancelBulkJobRequest{})

	calls := f.recorded()
	if len(calls) != 10 {
		t.Fatalf("the core saw %d calls, want 10", len(calls))
	}
	for _, call := range calls {
		want := otherLimit
		if call.method == "SendPlainText" {
			want = sendLimit
		}
		left := time.Until(call.deadline)
		if left > want || left < want-30*time.Second {
			t.Errorf("%s: time left %v, want about %v", call.method, left, want)
		}
	}
	if sendLimit != 6*time.Minute || otherLimit != 2*time.Minute {
		t.Error("the default limits changed")
	}
}

func TestACallKeepsTheDeadlineOfTheProgram(t *testing.T) {
	f := newFakeCore(t)
	c := f.newClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	if _, err := c.SendPlainText(ctx, &corev1.SendPlainTextRequest{}); err != nil {
		t.Fatal(err)
	}
	left := time.Until(f.recorded()[0].deadline)
	if left < 50*time.Minute || left > time.Hour {
		t.Errorf("time left %v, want about 1 hour", left)
	}
}

func TestCancellationCodesOfTheCalls(t *testing.T) {
	f := newFakeCore(t)
	c := f.newClient(t)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.GetBulkJob(canceled, &corev1.GetBulkJobRequest{})
	_ = asCalyxError(t, err, calyx.CodeAborted)

	passed, cancel2 := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel2()
	_, err = c.CancelBulkJob(passed, &corev1.CancelBulkJobRequest{})
	_ = asCalyxError(t, err, calyx.CodeTimeout)
}

func TestSendCallsAcceptANilRequest(t *testing.T) {
	f := newFakeCore(t)
	c := f.newClient(t)
	ctx := context.Background()

	p, err := c.SendPlainText(ctx, nil)
	if err != nil || p.IdempotencyKey == "" {
		t.Errorf("SendPlainText(nil) key = %q, err %v", p.IdempotencyKey, err)
	}
	s, err := c.SendBulkSameMessage(ctx, nil)
	if err != nil || s.IdempotencyKey == "" {
		t.Errorf("SendBulkSameMessage(nil) key = %q, err %v", s.IdempotencyKey, err)
	}
	u, err := c.SendBulkCustomMessages(ctx, nil)
	if err != nil || u.IdempotencyKey == "" {
		t.Errorf("SendBulkCustomMessages(nil) key = %q, err %v", u.IdempotencyKey, err)
	}
}
