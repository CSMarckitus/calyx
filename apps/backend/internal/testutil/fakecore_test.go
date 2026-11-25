package testutil

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
	"google.golang.org/grpc/test/bufconn"

	corev1 "github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// rawConn dials the fake core without the token interceptors.
func rawConn(t *testing.T, f *FakeCore) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return f.lis.(*bufconn.Listener).DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestFakeCoreRefusesACallWithoutTheToken(t *testing.T) {
	f, _ := NewFakeCore(t)
	client := corev1.NewInstanceServiceClient(rawConn(t, f))
	_, err := client.ListInstances(context.Background(), &corev1.ListInstancesRequest{})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("no token: code = %v, want Unauthenticated", status.Code(err))
	}
	ctx := metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer wrong")
	_, err = client.ListInstances(ctx, &corev1.ListInstancesRequest{})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("wrong token: code = %v, want Unauthenticated", status.Code(err))
	}
	ctx = metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+FakeCoreToken)
	if _, err := client.ListInstances(ctx, &corev1.ListInstancesRequest{}); err != nil {
		t.Fatalf("right token: %v", err)
	}
}

func TestFakeCoreRefusesAStreamWithoutTheToken(t *testing.T) {
	f, _ := NewFakeCore(t)
	client := corev1.NewInstanceServiceClient(rawConn(t, f))
	stream, err := client.CreateInstance(context.Background(), &corev1.CreateInstanceRequest{})
	if err == nil {
		_, err = stream.Recv()
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("code = %v, want Unauthenticated", status.Code(err))
	}
}

// createAll runs CreateInstance to its end and returns the error of the stream (nil at io.EOF).
func createAll(ctx context.Context, conn *grpc.ClientConn, req *corev1.CreateInstanceRequest) error {
	stream, err := corev1.NewInstanceServiceClient(conn).CreateInstance(ctx, req)
	if err != nil {
		return err
	}
	for {
		if _, err := stream.Recv(); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func webRequest(name string, identity corev1.DeviceIdentity, locale string) *corev1.CreateInstanceRequest {
	return &corev1.CreateInstanceRequest{InstanceName: name, ProviderSettings: &corev1.CreateInstanceRequest_WhatsappWeb{
		WhatsappWeb: &corev1.WhatsAppWebSettings{DeviceIdentity: identity, Locale: &locale},
	}}
}

// As the core, the fake refuses a locale of another form and an identity outside the enum.
func TestFakeCoreRefusesABadLocaleOrIdentity(t *testing.T) {
	f, conn := NewFakeCore(t)
	for name, req := range map[string]*corev1.CreateInstanceRequest{
		"locale":   webRequest("i_a", 0, "es_ES"),
		"identity": webRequest("i_a", corev1.DeviceIdentity(42), "es-ES"),
	} {
		// The default script holds an accepted pairing: the deadline ends it.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := createAll(ctx, conn, req)
		cancel()
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: code = %v, want InvalidArgument", name, status.Code(err))
		}
	}
	if len(f.Instances()) != 0 {
		t.Fatal("a refused request stored an instance")
	}
}

// As the core, a re-pair writes its configuration before the pairing: an abandoned re-pair keeps it.
func TestFakeCoreWritesTheRePairBeforeThePairing(t *testing.T) {
	f, conn := NewFakeCore(t)
	f.PutInstance(FakeInstance{Name: "i_a", Provider: corev1.Provider_PROVIDER_WHATSAPP_WEB,
		Status: corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH, Enabled: true,
		DeviceIdentity: corev1.DeviceIdentity_DEVICE_IDENTITY_EDGE_WINDOWS, Locale: "es-ES",
		Webhooks: []*corev1.WebhookConfig{{Url: "https://a.example/x", Event: "message.received"}}})
	f.ScriptCreate("i_a", CreateStep{Err: status.Error(codes.Canceled, "the caller left")})

	req := webRequest("i_a", corev1.DeviceIdentity_DEVICE_IDENTITY_SAFARI_MAC_OS, "pt-BR")
	req.IsUsedOnlyForSendMessages = new(false)
	if err := createAll(context.Background(), conn, req); status.Code(err) != codes.Canceled {
		t.Fatalf("code = %v", status.Code(err))
	}
	in, _ := f.Instance("i_a")
	if in.Status != corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH || in.DeviceIdentity != corev1.DeviceIdentity_DEVICE_IDENTITY_SAFARI_MAC_OS ||
		in.Locale != "pt-BR" || len(in.Webhooks) != 1 {
		t.Fatalf("instance %+v", in)
	}
}

// As the core: a re-pair of a disabled instance is refused, and nothing changes.
func TestFakeCoreRefusesTheRePairOfADisabledInstance(t *testing.T) {
	f, conn := NewFakeCore(t)
	f.PutInstance(FakeInstance{Name: "i_a", Provider: corev1.Provider_PROVIDER_WHATSAPP_WEB,
		Status: corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH, Enabled: false, Locale: "es-ES"})

	err := createAll(context.Background(), conn, webRequest("i_a", corev1.DeviceIdentity_DEVICE_IDENTITY_SAFARI_MAC_OS, "pt-BR"))
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("code = %v", status.Code(err))
	}
	if in, _ := f.Instance("i_a"); in.Locale != "es-ES" {
		t.Fatalf("a refused re-pair wrote the configuration: %+v", in)
	}
}

// As the core: while an instance pairs, every change is refused, as RemoveInstance is.
func TestFakeCoreRefusesAnUpdateDuringAPairing(t *testing.T) {
	f, conn := NewFakeCore(t)
	f.PutInstance(FakeInstance{Name: "i_a", Provider: corev1.Provider_PROVIDER_WHATSAPP_WEB,
		Status: corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH, Enabled: true, Locale: "es-ES"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- createAll(ctx, conn, webRequest("i_a", corev1.DeviceIdentity_DEVICE_IDENTITY_SAFARI_MAC_OS, "es-ES"))
	}()
	f.WaitForRequests(t, MethodCreateInstance, 1)
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.mu.Lock()
		on := f.pairing["i_a"]
		f.mu.Unlock()
		if on {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the pairing did not start")
		}
		time.Sleep(time.Millisecond)
	}

	client := corev1.NewInstanceServiceClient(conn)
	_, err := client.UpdateInstanceConfig(ctx, &corev1.UpdateInstanceConfigRequest{InstanceName: "i_a", IsEnabled: new(false)})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("code = %v", status.Code(err))
	}
	if in, _ := f.Instance("i_a"); !in.Enabled {
		t.Fatal("a refused update changed the instance")
	}
	cancel()
	if err := <-done; status.Code(err) != codes.Canceled {
		t.Fatalf("create code = %v", status.Code(err))
	}
}

func TestReasonErrorCarriesTheReasonTheMetadataAndTheMessage(t *testing.T) {
	err := ReasonError(codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_DUPLICATE_MESSAGE,
		map[string]string{"message_id": "m1"}, "Sent before.")
	st := status.Convert(err)
	if st.Code() != codes.AlreadyExists || st.Message() != "Sent before." {
		t.Fatalf("status = %v %q", st.Code(), st.Message())
	}
	details := st.Details()
	if len(details) != 1 {
		t.Fatalf("details = %d, want 1", len(details))
	}
	info, ok := details[0].(*errdetails.ErrorInfo)
	if !ok {
		t.Fatalf("detail type %T", details[0])
	}
	if info.GetReason() != "ERROR_REASON_DUPLICATE_MESSAGE" || info.GetDomain() != "calyx.core.v1" ||
		info.GetMetadata()["message_id"] != "m1" {
		t.Errorf("info = %v", info)
	}
}

func TestFakeCoreReplaysAKeyedSend(t *testing.T) {
	f, conn := NewFakeCore(t)
	f.AddInstance("i_a", corev1.Provider_PROVIDER_WHATSAPP_WEB, corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED)
	f.AddInstance("i_b", corev1.Provider_PROVIDER_WHATSAPP_WEB, corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED)
	client := corev1.NewMessageServiceClient(conn)
	send := func(name string, key *string) *corev1.SendPlainTextResponse {
		t.Helper()
		resp, err := client.SendPlainText(context.Background(),
			&corev1.SendPlainTextRequest{InstanceName: name, To: "1", Text: "x", IdempotencyKey: key})
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	key := "k1"
	first := send("i_a", &key)
	again := send("i_a", &key)
	if first.GetReplayed() || !again.GetReplayed() || again.GetMessageId() != first.GetMessageId() {
		t.Fatalf("first %v, again %v", first, again)
	}
	if other := send("i_b", &key); other.GetReplayed() || other.GetMessageId() == first.GetMessageId() {
		t.Errorf("the same key on another instance: %v", other)
	}
	a, b := send("i_a", nil), send("i_a", nil)
	if a.GetReplayed() || b.GetReplayed() || a.GetMessageId() == b.GetMessageId() {
		t.Errorf("calls without a key: %v %v", a, b)
	}
}

func TestFakeCoreStoresNothingWhenBeginFails(t *testing.T) {
	f, conn := NewFakeCore(t)
	f.AddInstance("i_a", corev1.Provider_PROVIDER_WHATSAPP_WEB, corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED)
	client := corev1.NewMessageServiceClient(conn)
	key := "k1"
	req := &corev1.SendPlainTextRequest{InstanceName: "i_a", To: "1", Text: "x", IdempotencyKey: &key}
	f.SetError(MethodSendPlainText, status.Error(codes.Internal, "boom"))
	if _, err := client.SendPlainText(context.Background(), req); status.Code(err) != codes.Internal {
		t.Fatalf("code = %v", status.Code(err))
	}
	f.SetError(MethodSendPlainText, nil)
	resp, err := client.SendPlainText(context.Background(), req)
	if err != nil || resp.GetReplayed() {
		t.Fatalf("%v, %v", resp, err)
	}
}

func TestFakeCoreReplaysAKeyedBulkJob(t *testing.T) {
	f, conn := NewFakeCore(t)
	f.AddInstance("i_a", corev1.Provider_PROVIDER_WHATSAPP_WEB, corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED)
	f.AddInstance("i_b", corev1.Provider_PROVIDER_WHATSAPP_WEB, corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED)
	client := corev1.NewMessageServiceClient(conn)
	key := "k1"
	bulk := func(name string) *corev1.BulkJob {
		t.Helper()
		job, err := client.SendBulkSameMessage(context.Background(),
			&corev1.SendBulkSameMessageRequest{InstanceName: name, Recipients: []string{"1"}, Text: "x", IdempotencyKey: &key})
		if err != nil {
			t.Fatal(err)
		}
		return job
	}
	first, again, other := bulk("i_a"), bulk("i_a"), bulk("i_b")
	if first.GetReplayed() || !again.GetReplayed() || again.GetId() != first.GetId() {
		t.Fatalf("first %v, again %v", first, again)
	}
	if other.GetReplayed() || other.GetId() == first.GetId() {
		t.Errorf("the same key on another instance: %v", other)
	}
}

func TestFakeCoreServesGetBulkJobsInReverseOrderAndSkipsMissingIds(t *testing.T) {
	f, conn := NewFakeCore(t)
	f.PutBulkJob(&corev1.BulkJob{Id: "j1"})
	f.PutBulkJob(&corev1.BulkJob{Id: "j2"})
	client := corev1.NewMessageServiceClient(conn)
	resp, err := client.GetBulkJobs(context.Background(), &corev1.GetBulkJobsRequest{JobIds: []string{"j2", "none", "j1"}})
	if err != nil {
		t.Fatal(err)
	}
	jobs := resp.GetJobs()
	if len(jobs) != 2 || jobs[0].GetId() != "j1" || jobs[1].GetId() != "j2" {
		t.Fatalf("jobs %v", jobs)
	}
	if got := f.GetBulkJobsRequests(); len(got) != 1 || len(got[0].GetJobIds()) != 3 {
		t.Fatalf("requests %v", got)
	}
}

func subscribeWith(t *testing.T, f *FakeCore, names, types []string) corev1.EventService_SubscribeClient {
	t.Helper()
	ctx, cancel := context.WithCancel(metadata.AppendToOutgoingContext(context.Background(),
		"authorization", "Bearer "+FakeCoreToken))
	t.Cleanup(cancel)
	stream, err := corev1.NewEventServiceClient(rawConn(t, f)).Subscribe(ctx,
		&corev1.SubscribeRequest{InstanceNames: names, EventTypes: types})
	if err != nil {
		t.Fatal(err)
	}
	return stream
}

func TestFakeCoreTokenHas32CharactersOrMore(t *testing.T) {
	if len(FakeCoreToken) < 32 {
		t.Errorf("len = %d", len(FakeCoreToken))
	}
}

func TestFakeCoreSubscribeSendsHeadersAndFiltersEvents(t *testing.T) {
	f, _ := NewFakeCore(t)
	stream := subscribeWith(t, f, []string{"i_a"}, []string{"connection.update"})
	if md, err := stream.Header(); err != nil || md == nil {
		t.Fatalf("Header = %v, %v", md, err)
	}
	f.PublishConnection("i_b", corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED) // other instance
	f.PublishConnection("i_a", corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH)
	ev, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if ev.InstanceName != "i_a" || ev.Type != "connection.update" || ev.PayloadJson != `{"status":"NEEDS_AUTH"}` || ev.Id == "" {
		t.Errorf("event = %v", ev)
	}
}

func TestFakeCoreSubscribeFiltersByEventType(t *testing.T) {
	f, _ := NewFakeCore(t)
	stream := subscribeWith(t, f, nil, []string{"message.received"})
	if _, err := stream.Header(); err != nil {
		t.Fatal(err)
	}
	f.PublishConnection("i_a", corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED)
	f.EndSubscriptions()
	if _, err := stream.Recv(); status.Code(err) != codes.Unavailable {
		t.Errorf("Recv = %v, want UNAVAILABLE and no event", err)
	}
}

func TestFakeCoreEndSubscriptionsEndsWithUnavailable(t *testing.T) {
	f, _ := NewFakeCore(t)
	stream := subscribeWith(t, f, nil, nil)
	if _, err := stream.Header(); err != nil {
		t.Fatal(err)
	}
	f.EndSubscriptions()
	_, err := stream.Recv()
	if status.Code(err) != codes.Unavailable || status.Convert(err).Message() != "The core stops." {
		t.Errorf("Recv = %v", err)
	}
}

func TestFakeCoreSubscribeWithoutHeadersSendsNone(t *testing.T) {
	f, _ := NewFakeCore(t)
	f.SetSubscribeHeaders(false)
	stream := subscribeWith(t, f, nil, nil)
	got := make(chan struct{})
	go func() { _, _ = stream.Header(); close(got) }()
	select {
	case <-got:
		t.Fatal("headers arrived")
	case <-time.After(100 * time.Millisecond):
	}
	f.SetSubscribeHeaders(true)
	f.EndSubscriptions() // frees the handler
}

func TestFakeCoreSubscribeObeysSetUnavailable(t *testing.T) {
	f, _ := NewFakeCore(t)
	f.SetUnavailable(true)
	stream := subscribeWith(t, f, nil, nil)
	if _, err := stream.Recv(); status.Code(err) != codes.Unavailable {
		t.Errorf("Recv = %v, want UNAVAILABLE", err)
	}
	if n := len(f.SubscribeRequests()); n != 1 {
		t.Errorf("recorded %d requests", n)
	}
}

func TestNewFakeCoreOnServesOnTheGivenListener(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := NewFakeCoreOn(t, lis)
	f.AddInstance("i_tcp", corev1.Provider_PROVIDER_WHATSAPP_WEB, corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	client := corev1.NewInstanceServiceClient(conn)
	ctx := metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+FakeCoreToken)
	resp, err := client.ListInstances(ctx, &corev1.ListInstancesRequest{})
	if err != nil || len(resp.GetInstances()) != 1 {
		t.Fatalf("ListInstances = %v, %v; want one instance", resp, err)
	}
	if _, err := client.ListInstances(context.Background(), &corev1.ListInstancesRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("no token: code = %v, want Unauthenticated", status.Code(err))
	}
}
