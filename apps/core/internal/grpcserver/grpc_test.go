package grpcserver_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"calyx/core/internal/engine"
	"calyx/core/internal/events"
	"calyx/core/internal/grpcserver"
	"calyx/core/internal/testutil"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

type harness struct {
	db        *sql.DB
	bus       *events.Bus
	instances corev1.InstanceServiceClient
	messages  corev1.MessageServiceClient
	events    corev1.EventServiceClient

	clockOffset atomic.Int64 // added to time.Now by the clock of the orchestrator
	mu          sync.Mutex
	fakes       map[string]*testutil.FakeProvider // the last one built per instance name
	o           *engine.Orchestrator
	server      *grpcserver.Server
}

func start(t *testing.T, authToken string) *harness {
	t.Helper()
	return startWith(t, grpcserver.Options{AuthToken: authToken})
}

func startWith(t *testing.T, opts grpcserver.Options) *harness {
	t.Helper()
	h := &harness{db: testutil.NewDB(t), bus: events.NewBus(), fakes: map[string]*testutil.FakeProvider{}}
	o := engine.NewOrchestrator(h.db, engine.NewBulkStore(h.db), h.bus, engine.OrchestratorOptions{
		NormalizeRecipient: testutil.NormalizeRecipient,
		Now:                func() time.Time { return time.Now().Add(time.Duration(h.clockOffset.Load())) },
		BuildProvider: func(row engine.InstanceRow, hooks engine.ProviderHooks) (engine.Provider, error) {
			fake := testutil.NewFakeProvider(row.Name)
			fake.SetHooks(hooks)
			h.mu.Lock()
			h.fakes[row.Name] = fake
			h.mu.Unlock()
			if row.Name == "slow-pairing" {
				fake.SetPair(func(ctx context.Context, method engine.PairingMethod, onCode func(string)) error {
					onCode("QR-1")
					<-ctx.Done()
					return ctx.Err()
				})
			}
			return fake, nil
		},
	})
	h.o = o
	server := grpcserver.New(o, h.bus, opts)
	h.server = server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop(time.Second)
		o.DestroyAllSessions()
	})

	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	h.instances = corev1.NewInstanceServiceClient(conn)
	h.messages = corev1.NewMessageServiceClient(conn)
	h.events = corev1.NewEventServiceClient(conn)
	return h
}

// connectedInstance stores a long-lived instance and loads it, as the core does at start. A send
// does not load a long-lived instance.
func (h *harness) connectedInstance(t *testing.T, name string) {
	t.Helper()
	testutil.CreateInstance(t, h.db, name)
	h.o.LoadAllActiveInstances(bg)
}

// exec runs the SQL of a test setup and stops the test when it fails.
func (h *harness) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := h.db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) fake(name string) *testutil.FakeProvider {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.fakes[name]
}

// advance moves the clock of the orchestrator forward.
func (h *harness) advance(d time.Duration) { h.clockOffset.Add(int64(d)) }

func codeOf(err error) codes.Code { return status.Code(err) }

var bg = context.Background()

func TestListInstancesReturnsTheStoredInstancesWithoutSecrets(t *testing.T) {
	h := start(t, "")
	id := testutil.CreateInstance(t, h.db, "shop")
	h.exec(t, `INSERT INTO webhooks (id, instance_id, event_type, url, secret) VALUES ('w', ?, 'message.received', 'https://example.test', 'x')`, id)

	resp, err := h.instances.ListInstances(bg, &corev1.ListInstancesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	info := resp.GetInstances()[0]
	if info.GetInstanceName() != "shop" || info.GetProvider() != corev1.Provider_PROVIDER_WHATSAPP_WEB ||
		!info.GetWebhooks()[0].GetHasSecret() {
		t.Fatalf("got %v", info)
	}
}

func TestGetInstanceStatusOfAnUnknownInstanceIsNotFound(t *testing.T) {
	h := start(t, "")
	_, err := h.instances.GetInstanceStatus(bg, &corev1.GetInstanceStatusRequest{InstanceName: "nope"})
	if codeOf(err) != codes.NotFound {
		t.Fatalf("got %v", err)
	}
}

func TestSendPlainTextReturnsTheProviderMessageIdAndRejectsBadRecipients(t *testing.T) {
	h := start(t, "")
	h.connectedInstance(t, "shop")
	resp, err := h.messages.SendPlainText(bg, &corev1.SendPlainTextRequest{InstanceName: "shop", To: "34600111222", Text: "hi"})
	if err != nil || resp.GetMessageId() != "fake-1" {
		t.Fatalf("got %v, %v", resp, err)
	}
	_, err = h.messages.SendPlainText(bg, &corev1.SendPlainTextRequest{InstanceName: "shop", To: "abc", Text: "hi"})
	if codeOf(err) != codes.InvalidArgument {
		t.Fatalf("bad recipient: %v", err)
	}
}

func TestABulkJobReaches100PercentAndCanBeCancelled(t *testing.T) {
	h := start(t, "")
	testutil.CreateInstance(t, h.db, "shop")
	job, err := h.messages.SendBulkSameMessage(bg, &corev1.SendBulkSameMessageRequest{
		InstanceName: "shop", Recipients: []string{"34600000001", "34600000002", "34600000003"}, Text: "Reminder",
	})
	if err != nil {
		t.Fatal(err)
	}
	if job.GetTotalMessages() != 3 || job.GetInstanceName() != "shop" ||
		!regexp.MustCompile(`^\d+m \d+s$`).MatchString(job.GetDisplayTime()) {
		t.Fatalf("got %v", job)
	}

	// 100 % is reached when the last message is sent; the job is finished on the next turn of the
	// outbox, so wait for that.
	deadline := time.Now().Add(3 * time.Second)
	for job.GetFinishedAt() == nil {
		if time.Now().After(deadline) {
			t.Fatalf("stuck at %v", job)
		}
		time.Sleep(20 * time.Millisecond)
		if job, err = h.messages.GetBulkJob(bg, &corev1.GetBulkJobRequest{JobId: job.GetId()}); err != nil {
			t.Fatal(err)
		}
	}
	if job.GetStatus() != corev1.BulkJobStatus_BULK_JOB_STATUS_COMPLETED || job.GetProgressPercent() != 100 {
		t.Fatalf("got %v", job)
	}

	delay := int32(60000)
	long, err := h.messages.SendBulkCustomMessages(bg, &corev1.SendBulkCustomMessagesRequest{
		InstanceName: "shop", Messages: []*corev1.PlainTextPayload{{To: "34600000001", Text: "x", DelayMs: &delay}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := h.messages.CancelBulkJob(bg, &corev1.CancelBulkJobRequest{JobId: long.GetId()})
	if err != nil || cancelled.GetStatus() != corev1.BulkJobStatus_BULK_JOB_STATUS_CANCELLED {
		t.Fatalf("got %v, %v", cancelled, err)
	}
}

// The backend accepts a bulk body of up to 16 MiB, so the core must accept a request of that size.
// gRPC refuses a message above 4 MiB by default.
func TestABulkRequestOf16MiBIsAccepted(t *testing.T) {
	h := start(t, "")
	testutil.CreateInstance(t, h.db, "shop")
	delay := int32(60000)
	text := strings.Repeat("a", 3350)
	msgs := make([]*corev1.PlainTextPayload, 5000)
	for i := range msgs {
		msgs[i] = &corev1.PlainTextPayload{To: fmt.Sprintf("34600%06d", i), Text: text, DelayMs: &delay}
	}
	req := &corev1.SendBulkCustomMessagesRequest{InstanceName: "shop", Messages: msgs}
	if n := proto.Size(req); n < 16<<20 || n > grpcserver.MaxMessageBytes {
		t.Fatalf("request size = %d", n)
	}
	job, err := h.messages.SendBulkCustomMessages(bg, req)
	if err != nil {
		t.Fatal(err)
	}
	if job.GetTotalMessages() != 5000 {
		t.Fatalf("got %v", job)
	}
	if _, err := h.messages.CancelBulkJob(bg, &corev1.CancelBulkJobRequest{JobId: job.GetId()}); err != nil {
		t.Fatal(err)
	}
}

// The anti-spam limit per recipient: 1 for one text, 2 for custom messages.
func TestARepeatedRecipientIsInvalidArgument(t *testing.T) {
	h := start(t, "")
	testutil.CreateInstance(t, h.db, "shop")
	_, err := h.messages.SendBulkSameMessage(bg, &corev1.SendBulkSameMessageRequest{
		InstanceName: "shop", Recipients: []string{"34600000001", "34600000001"}, Text: "x",
	})
	if codeOf(err) != codes.InvalidArgument {
		t.Fatalf("same text: %v", err)
	}
	two := []*corev1.PlainTextPayload{{To: "34600000001", Text: "a"}, {To: "34600000001", Text: "b"}}
	if _, err := h.messages.SendBulkCustomMessages(bg, &corev1.SendBulkCustomMessagesRequest{InstanceName: "shop", Messages: two}); err != nil {
		t.Fatalf("custom, two: %v", err)
	}
	three := slices.Concat(two, []*corev1.PlainTextPayload{{To: "34600000001", Text: "c"}})
	_, err = h.messages.SendBulkCustomMessages(bg, &corev1.SendBulkCustomMessagesRequest{InstanceName: "shop", Messages: three})
	if codeOf(err) != codes.InvalidArgument {
		t.Fatalf("custom, three: %v", err)
	}
}

func TestSubscribeStreamsEventsAndHonoursTheTypeFilter(t *testing.T) {
	h := start(t, "")
	ctx, cancel := context.WithCancel(bg)
	defer cancel()
	stream, err := h.events.Subscribe(ctx, &corev1.SubscribeRequest{EventTypes: []string{"bulk.finished"}})
	if err != nil {
		t.Fatal(err)
	}
	// The subscription is registered when the server handles the call: publish until it arrives.
	go func() {
		for ctx.Err() == nil {
			h.bus.Publish("shop", "connection.update", events.ConnectionUpdate{Status: "CONNECTED"})
			h.bus.Publish("shop", "bulk.finished", events.BulkFinished{JobID: "j1", Status: "COMPLETED"})
			time.Sleep(20 * time.Millisecond)
		}
	}()
	event, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if event.GetType() != "bulk.finished" || !strings.Contains(event.GetPayloadJson(), `"jobId":"j1"`) {
		t.Fatalf("got %v", event)
	}
}

func TestSubscribeSendsItsHeadersBeforeAnyEvent(t *testing.T) {
	h := start(t, "")
	ctx, cancel := context.WithTimeout(bg, 2*time.Second)
	defer cancel()
	stream, err := h.events.Subscribe(ctx, &corev1.SubscribeRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Header(); err != nil {
		t.Fatalf("no headers without an event: %v", err)
	}
	// The headers mean that the subscription listens: this event must arrive with no retry.
	h.bus.Publish("shop", "bulk.finished", events.BulkFinished{JobID: "j2", Status: "COMPLETED"})
	event, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if event.GetType() != "bulk.finished" {
		t.Fatalf("got %v", event)
	}
}

func TestSubscribeSendsTheCoreVersionInItsHeader(t *testing.T) {
	h := startWith(t, grpcserver.Options{Version: "1.2.3"})
	ctx, cancel := context.WithTimeout(bg, 2*time.Second)
	defer cancel()
	stream, err := h.events.Subscribe(ctx, &corev1.SubscribeRequest{})
	if err != nil {
		t.Fatal(err)
	}
	header, err := stream.Header()
	if err != nil {
		t.Fatal(err)
	}
	if got := header.Get("calyx-version"); len(got) != 1 || got[0] != "1.2.3" {
		t.Fatalf("calyx-version = %v, want [1.2.3]", got)
	}
}

func TestStopEndsAnOpenSubscriptionWithUnavailable(t *testing.T) {
	h := start(t, "")
	ctx, cancel := context.WithTimeout(bg, 5*time.Second)
	defer cancel()
	stream, err := h.events.Subscribe(ctx, &corev1.SubscribeRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Header(); err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	go h.server.Stop(5 * time.Second)
	_, err = stream.Recv()
	if codeOf(err) != codes.Unavailable {
		t.Fatalf("got %v, want UNAVAILABLE", err)
	}
	if took := time.Since(began); took > time.Second {
		t.Fatalf("the stream ended after %v", took)
	}
	h.server.Stop(time.Second) // a second call must not panic
}

func TestCreateInstanceStreamsTheCodesAndEndsConnected(t *testing.T) {
	h := start(t, "")
	stream, err := h.instances.CreateInstance(bg, &corev1.CreateInstanceRequest{InstanceName: "shop", ProviderSettings: whatsappWeb("es-ES")})
	if err != nil {
		t.Fatal(err)
	}
	var updates []*corev1.CreateInstanceResponse
	for {
		update, err := stream.Recv()
		if err != nil {
			break
		}
		updates = append(updates, update)
	}
	if len(updates) != 2 || updates[0].GetPairingCode() != "CODE-1" ||
		updates[1].GetStatus() != corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED {
		t.Fatalf("got %v", updates)
	}
}

// whatsappWeb is the provider settings of a QR pairing with a locale.
func whatsappWeb(locale string) *corev1.CreateInstanceRequest_WhatsappWeb {
	return &corev1.CreateInstanceRequest_WhatsappWeb{WhatsappWeb: &corev1.WhatsAppWebSettings{Locale: &locale}}
}

func TestANewPairingWithoutLocaleIsInvalidArgument(t *testing.T) {
	h := start(t, "")
	err := rePair(h, &corev1.CreateInstanceRequest{InstanceName: "shop"})
	if codeOf(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "locale is required for a new pairing") {
		t.Fatalf("got %v", err)
	}
}

func TestAnUnknownDeviceIdentityIsInvalidArgument(t *testing.T) {
	h := start(t, "")
	settings := whatsappWeb("es-ES")
	settings.WhatsappWeb.DeviceIdentity = corev1.DeviceIdentity(42)
	err := rePair(h, &corev1.CreateInstanceRequest{InstanceName: "shop", ProviderSettings: settings})
	if codeOf(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "device_identity 42") {
		t.Fatalf("got %v", err)
	}
}

func TestTheConnectedUpdateAndListInstancesCarryTheIdentityAndLocale(t *testing.T) {
	h := start(t, "")
	settings := whatsappWeb("es-ES")
	settings.WhatsappWeb.DeviceIdentity = corev1.DeviceIdentity_DEVICE_IDENTITY_FIREFOX_LINUX
	stream, err := h.instances.CreateInstance(bg, &corev1.CreateInstanceRequest{InstanceName: "shop", ProviderSettings: settings})
	if err != nil {
		t.Fatal(err)
	}
	var last *corev1.CreateInstanceResponse
	for {
		update, err := stream.Recv()
		if err != nil {
			break
		}
		last = update
	}
	if last.GetStatus() != corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED ||
		last.GetDeviceIdentity() != corev1.DeviceIdentity_DEVICE_IDENTITY_FIREFOX_LINUX || last.GetLocale() != "es-ES" {
		t.Fatalf("last update %v", last)
	}
	resp, err := h.instances.ListInstances(bg, &corev1.ListInstancesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	info := resp.GetInstances()[0]
	if info.GetDeviceIdentity() != corev1.DeviceIdentity_DEVICE_IDENTITY_FIREFOX_LINUX || info.GetLocale() != "es-ES" {
		t.Fatalf("got %v", info)
	}
}

// rePair runs CreateInstance to the end and returns the error of the stream (nil on success).
func rePair(h *harness, req *corev1.CreateInstanceRequest) error {
	stream, err := h.instances.CreateInstance(bg, req)
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

func TestARePairWithNoWebhooksKeepsThem(t *testing.T) {
	h := start(t, "")
	id := testutil.CreateInstance(t, h.db, "shop")
	h.exec(t, `UPDATE instances SET connection_status = 'NEEDS_AUTH', locale = 'es-ES' WHERE id = ?`, id)
	h.exec(t, `INSERT INTO webhooks (id, instance_id, event_type, url, secret) VALUES ('w', ?, 'message.received', 'https://example.test', 's3cret')`, id)

	if err := rePair(h, &corev1.CreateInstanceRequest{InstanceName: "shop"}); err != nil {
		t.Fatal(err)
	}
	var webhookID, secret string
	if err := h.db.QueryRow(`SELECT id, secret FROM webhooks WHERE instance_id = ?`, id).Scan(&webhookID, &secret); err != nil {
		t.Fatalf("the webhook is gone: %v", err)
	}
	if webhookID != "w" || secret != "s3cret" {
		t.Fatalf("got the webhook %q with the secret %q", webhookID, secret)
	}
}

func TestARePairWithNoSendOnlyFieldKeepsTheFlag(t *testing.T) {
	h := start(t, "")
	id := testutil.CreateInstance(t, h.db, "shop")
	h.exec(t, `UPDATE instances SET connection_status = 'NEEDS_AUTH', is_used_only_for_send_messages = 1, locale = 'es-ES' WHERE id = ?`, id)

	if err := rePair(h, &corev1.CreateInstanceRequest{InstanceName: "shop"}); err != nil {
		t.Fatal(err)
	}
	resp, err := h.instances.ListInstances(bg, &corev1.ListInstancesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.GetInstances()[0].GetIsUsedOnlyForSendMessages() {
		t.Fatal("the re-pair turned the send-only flag off")
	}

	h.exec(t, `UPDATE instances SET connection_status = 'NEEDS_AUTH' WHERE id = ?`, id)
	if err := rePair(h, &corev1.CreateInstanceRequest{InstanceName: "shop", IsUsedOnlyForSendMessages: proto.Bool(false)}); err != nil {
		t.Fatal(err)
	}
	resp, err = h.instances.ListInstances(bg, &corev1.ListInstancesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetInstances()[0].GetIsUsedOnlyForSendMessages() {
		t.Fatal("a present false did not turn the send-only flag off")
	}
}

func TestBadPairingSettingsAreInvalidArgument(t *testing.T) {
	h := start(t, "")
	cases := map[string]*corev1.WhatsAppWebSettings{
		"pairing_qr false": {PairingMethod: &corev1.WhatsAppWebSettings_PairingQr{PairingQr: false}},
		"bad phone":        {PairingMethod: &corev1.WhatsAppWebSettings_PairingCode{PairingCode: &corev1.PairingCodeSettings{PhoneNumber: "+34 600"}}},
	}
	for name, settings := range cases {
		stream, err := h.instances.CreateInstance(bg, &corev1.CreateInstanceRequest{
			InstanceName: "shop", ProviderSettings: &corev1.CreateInstanceRequest_WhatsappWeb{WhatsappWeb: settings},
		})
		if err == nil {
			_, err = stream.Recv()
		}
		if codeOf(err) != codes.InvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestAClientThatLeavesCancelsThePairing(t *testing.T) {
	h := start(t, "")
	ctx, cancel := context.WithCancel(bg)
	stream, err := h.instances.CreateInstance(ctx, &corev1.CreateInstanceRequest{InstanceName: "slow-pairing", ProviderSettings: whatsappWeb("es-ES")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	cancel()

	deadline := time.Now().Add(3 * time.Second)
	for {
		_, err := h.instances.GetInstanceStatus(bg, &corev1.GetInstanceStatusRequest{InstanceName: "slow-pairing"})
		if codeOf(err) == codes.NotFound {
			return // the unpaired instance was removed
		}
		if time.Now().After(deadline) {
			t.Fatalf("the abandoned pairing left the instance: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestWithAnAuthTokenOnlyCallsCarryingItWork(t *testing.T) {
	h := start(t, "s3cret")
	if _, err := h.instances.ListInstances(bg, &corev1.ListInstancesRequest{}); codeOf(err) != codes.Unauthenticated {
		t.Fatalf("without token: %v", err)
	}
	wrong := metadata.AppendToOutgoingContext(bg, "authorization", "Bearer nope")
	if _, err := h.instances.ListInstances(wrong, &corev1.ListInstancesRequest{}); codeOf(err) != codes.Unauthenticated {
		t.Fatalf("wrong token: %v", err)
	}
	right := metadata.AppendToOutgoingContext(bg, "authorization", "Bearer s3cret")
	if _, err := h.instances.ListInstances(right, &corev1.ListInstancesRequest{}); err != nil {
		t.Fatalf("right token: %v", err)
	}
	stream, err := h.events.Subscribe(bg, &corev1.SubscribeRequest{})
	if err == nil {
		_, err = stream.Recv()
	}
	if codeOf(err) != codes.Unauthenticated {
		t.Fatalf("stream without token: %v", err)
	}
}

func TestAnUnexpectedErrorIsInternalAndDoesNotLeakItsMessage(t *testing.T) {
	h := start(t, "")
	h.db.Close() // every query now fails with "sql: database is closed"
	_, err := h.instances.ListInstances(bg, &corev1.ListInstancesRequest{})
	if codeOf(err) != codes.Internal || strings.Contains(err.Error(), "sql") {
		t.Fatalf("got %v", err)
	}
}

const shipped = "Your order has shipped"

func sendRequest(to, text string, key *string) *corev1.SendPlainTextRequest {
	return &corev1.SendPlainTextRequest{InstanceName: "shop", To: to, Text: text, IdempotencyKey: key}
}

// refusedWith checks the code and the ErrorInfo detail of a refusal: its reason, its domain and
// its metadata keys. The error message must not repeat a key, a recipient or a message text.
func refusedWith(t *testing.T, err error, code codes.Code, reason corev1.ErrorReason, metadataKeys []string, secrets ...string) map[string]string {
	t.Helper()
	st := status.Convert(err)
	if st.Code() != code {
		t.Fatalf("code %v, want %v (%s)", st.Code(), code, st.Message())
	}
	var info *errdetails.ErrorInfo
	for _, d := range st.Details() {
		if i, ok := d.(*errdetails.ErrorInfo); ok {
			info = i
		}
	}
	if info == nil || info.GetReason() != reason.String() || info.GetDomain() != "calyx.core.v1" {
		t.Fatalf("ErrorInfo %v, want reason %s in domain calyx.core.v1", info, reason)
	}
	keys := slices.Sorted(maps.Keys(info.GetMetadata()))
	if !slices.Equal(keys, metadataKeys) {
		t.Fatalf("metadata keys %v, want %v", keys, metadataKeys)
	}
	for i, s := range secrets {
		if strings.Contains(st.Message(), s) {
			t.Fatalf("the error message repeats a key, a recipient or a message text (forbidden value %d)", i)
		}
	}
	return info.GetMetadata()
}

// holdSends makes every send of the fake wait until release; sending closes when one waits.
func holdSends(t *testing.T, fake *testutil.FakeProvider) (sending chan struct{}, release func()) {
	sending, proceed := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	fake.BeforeSend(func(string) {
		once.Do(func() { close(sending) })
		<-proceed
	})
	release = func() { releaseOnce.Do(func() { close(proceed) }) }
	t.Cleanup(release)
	return sending, release
}

func k(s string) *string { return &s }

func TestARetryDuringTheSendIsAbortedThenReplayed(t *testing.T) {
	h := start(t, "")
	h.connectedInstance(t, "shop")
	if _, err := h.messages.SendPlainText(bg, sendRequest("34600000009", "load the instance", nil)); err != nil {
		t.Fatal(err)
	}
	sending, release := holdSends(t, h.fake("shop"))

	callerCtx, stopWaiting := context.WithCancel(bg)
	first := make(chan error, 1)
	go func() {
		_, err := h.messages.SendPlainText(callerCtx, sendRequest("34600111222", shipped, k("k1")))
		first <- err
	}()
	<-sending
	stopWaiting()
	if err := <-first; codeOf(err) != codes.Canceled {
		t.Fatalf("first caller: %v", err)
	}

	retryCtx, cancelRetry := context.WithTimeout(bg, 5*time.Second) // a regression fails fast
	defer cancelRetry()
	_, err := h.messages.SendPlainText(retryCtx, sendRequest("34600111222", shipped, k("k1")))
	refusedWith(t, err, codes.Aborted, corev1.ErrorReason_ERROR_REASON_SEND_IN_PROGRESS, nil, "k1", "34600111222", shipped)

	release()
	var resp *corev1.SendPlainTextResponse
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err = h.messages.SendPlainText(retryCtx, sendRequest("34600111222", shipped, k("k1")))
		if codeOf(err) != codes.Aborted || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || !resp.GetReplayed() || resp.GetMessageId() != "fake-2" {
		t.Fatalf("replay: got %v, %v", resp, err)
	}
	if n := len(h.fake("shop").Sent()); n != 2 {
		t.Fatalf("the provider got %d messages", n)
	}
}

func TestTheSameMessageWithANewKeyOrNoKeyIsADuplicateFor10Minutes(t *testing.T) {
	h := start(t, "")
	h.connectedInstance(t, "shop")
	first, err := h.messages.SendPlainText(bg, sendRequest("34600111222", shipped, k("k1")))
	if err != nil || first.GetReplayed() {
		t.Fatalf("first: %v, %v", first, err)
	}

	h.advance(3 * time.Minute)
	for _, key := range []*string{k("k2"), nil} {
		_, err := h.messages.SendPlainText(bg, sendRequest("34600111222", shipped, key))
		metadata := refusedWith(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_DUPLICATE_MESSAGE,
			[]string{"at", "message_id"}, "k2", "34600111222", shipped)
		if metadata["message_id"] != first.GetMessageId() {
			t.Fatalf("metadata %v", metadata)
		}
		if _, err := time.Parse(time.RFC3339, metadata["at"]); err != nil {
			t.Fatalf("at: %v", err)
		}
	}

	h.advance(8 * time.Minute)
	if _, err := h.messages.SendPlainText(bg, sendRequest("34600111222", shipped, k("k2"))); err != nil {
		t.Fatalf("11 minutes later: %v", err)
	}
}

func TestAKeyReusedForAnotherMessageIsAConflictFor24Hours(t *testing.T) {
	h := start(t, "")
	h.connectedInstance(t, "shop")
	if _, err := h.messages.SendPlainText(bg, sendRequest("34600111222", shipped, k("k1"))); err != nil {
		t.Fatal(err)
	}
	_, err := h.messages.SendPlainText(bg, sendRequest("34600111222", "Another text", k("k1")))
	refusedWith(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT, nil,
		"k1", "34600111222", "Another text")
	_, err = h.messages.SendPlainText(bg, sendRequest("34600333444", shipped, k("k1")))
	refusedWith(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT, nil,
		"k1", "34600333444", shipped)

	h.advance(24 * time.Hour)
	resp, err := h.messages.SendPlainText(bg, sendRequest("34600111222", "Another text", k("k1")))
	if err != nil || resp.GetReplayed() {
		t.Fatalf("24 hours later: %v, %v", resp, err)
	}
}

// The restart itself is in the engine tests; here the PENDING row is recovered the way the core
// does at start, and the test checks the answer on the wire.
func TestARecoveredSendIsAnUnknownOutcome(t *testing.T) {
	h := start(t, "")
	h.connectedInstance(t, "shop")
	if _, err := h.messages.SendPlainText(bg, sendRequest("34600000009", "load the instance", nil)); err != nil {
		t.Fatal(err)
	}
	sending, _ := holdSends(t, h.fake("shop"))
	callerCtx, stopWaiting := context.WithCancel(bg)
	defer stopWaiting()
	go func() { _, _ = h.messages.SendPlainText(callerCtx, sendRequest("34600111222", shipped, k("k1"))) }()
	<-sending
	if err := engine.NewSendRecords(h.db, time.Now).Recover(bg, time.Now().Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}

	retryCtx, cancelRetry := context.WithTimeout(bg, 5*time.Second) // a regression fails fast
	defer cancelRetry()
	_, err := h.messages.SendPlainText(retryCtx, sendRequest("34600111222", shipped, k("k1")))
	refusedWith(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_SEND_OUTCOME_UNKNOWN, []string{"at"},
		"k1", "34600111222", shipped)
	_, err = h.messages.SendPlainText(retryCtx, sendRequest("34600111222", shipped, nil))
	refusedWith(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_DUPLICATE_MESSAGE, []string{"at"},
		"34600111222", shipped)
}

func TestANotSentErrorFreesTheKey(t *testing.T) {
	h := start(t, "")
	h.connectedInstance(t, "shop")
	if _, err := h.messages.SendPlainText(bg, sendRequest("34600000009", "load the instance", nil)); err != nil {
		t.Fatal(err)
	}
	h.fake("shop").ForceStatus(corev1.InstanceStatus_INSTANCE_STATUS_DISCONNECTED)
	if _, err := h.messages.SendPlainText(bg, sendRequest("34600111222", shipped, k("k1"))); codeOf(err) != codes.Unavailable {
		t.Fatalf("disconnected: %v", err)
	}
	h.fake("shop").ForceStatus(corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED)
	resp, err := h.messages.SendPlainText(bg, sendRequest("34600111222", shipped, k("k1")))
	if err != nil || resp.GetReplayed() || resp.GetMessageId() != "fake-2" {
		t.Fatalf("retry: %v, %v", resp, err)
	}
}

func TestAnInvalidIdempotencyKeyIsInvalidArgument(t *testing.T) {
	h := start(t, "")
	for _, key := range []string{"", strings.Repeat("x", 256), "my key"} {
		_, err := h.messages.SendPlainText(bg, sendRequest("34600111222", shipped, k(key)))
		if codeOf(err) != codes.InvalidArgument {
			t.Fatalf("key of %d characters: %v", len(key), err)
		}
		if key != "" && strings.Contains(status.Convert(err).Message(), key) {
			t.Fatal("the error message repeats the key")
		}
	}
}

func sameRequest(key *string, recipients ...string) *corev1.SendBulkSameMessageRequest {
	return &corev1.SendBulkSameMessageRequest{InstanceName: "shop", Recipients: recipients, Text: "Reminder", IdempotencyKey: key}
}

func customRequest(key *string, recipients ...string) *corev1.SendBulkCustomMessagesRequest {
	delay := int32(60000) // the job stays open, so the replay shows its current state
	msgs := make([]*corev1.PlainTextPayload, len(recipients))
	for i, r := range recipients {
		msgs[i] = &corev1.PlainTextPayload{To: r, Text: fmt.Sprintf("Reminder %d", i), DelayMs: &delay}
	}
	return &corev1.SendBulkCustomMessagesRequest{InstanceName: "shop", Messages: msgs, IdempotencyKey: key}
}

func (h *harness) jobCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := h.db.QueryRow("SELECT COUNT(*) FROM bulk_jobs").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestARetriedBulkRequestReturnsTheSameJob(t *testing.T) {
	calls := map[string]func(h *harness) (*corev1.BulkJob, error){
		"same": func(h *harness) (*corev1.BulkJob, error) {
			return h.messages.SendBulkSameMessage(bg, sameRequest(k("b1"), "34600000001", "34600000002"))
		},
		"custom": func(h *harness) (*corev1.BulkJob, error) {
			return h.messages.SendBulkCustomMessages(bg, customRequest(k("b1"), "34600000001", "34600000002"))
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			h := start(t, "")
			testutil.CreateInstance(t, h.db, "shop")
			first, err := call(h)
			if err != nil || first.GetReplayed() {
				t.Fatalf("first: %v, %v", first, err)
			}
			again, err := call(h)
			if err != nil || !again.GetReplayed() || again.GetId() != first.GetId() || again.GetTotalMessages() != 2 {
				t.Fatalf("retry: %v, %v", again, err)
			}
			if n := h.jobCount(t); n != 1 {
				t.Fatalf("%d jobs, want 1", n)
			}

			got, err := h.messages.GetBulkJob(bg, &corev1.GetBulkJobRequest{JobId: first.GetId()})
			if err != nil || got.GetReplayed() {
				t.Fatalf("GetBulkJob: %v, %v", got, err)
			}
			cancelled, err := h.messages.CancelBulkJob(bg, &corev1.CancelBulkJobRequest{JobId: first.GetId()})
			if err != nil || cancelled.GetReplayed() {
				t.Fatalf("CancelBulkJob: %v, %v", cancelled, err)
			}
			again, err = call(h)
			if err != nil || !again.GetReplayed() || again.GetStatus() != corev1.BulkJobStatus_BULK_JOB_STATUS_CANCELLED {
				t.Fatalf("retry after the cancel: %v, %v", again, err)
			}
		})
	}
}

func TestABulkKeyReusedForOtherRecipientsOrASingleSendIsAConflict(t *testing.T) {
	h := start(t, "")
	testutil.CreateInstance(t, h.db, "shop")
	if _, err := h.messages.SendBulkCustomMessages(bg, customRequest(k("b1"), "34600000001")); err != nil {
		t.Fatal(err)
	}
	_, err := h.messages.SendBulkCustomMessages(bg, customRequest(k("b1"), "34600000003"))
	refusedWith(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT, nil, "b1", "34600000003")
	_, err = h.messages.SendBulkSameMessage(bg, sameRequest(k("b1"), "34600000001"))
	refusedWith(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT, nil, "b1", "34600000001")
	_, err = h.messages.SendPlainText(bg, sendRequest("34600000001", "Reminder 0", k("b1")))
	refusedWith(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT, nil, "b1", "34600000001")

	if _, err := h.messages.SendPlainText(bg, sendRequest("34600111222", shipped, k("k1"))); err != nil {
		t.Fatal(err)
	}
	_, err = h.messages.SendBulkSameMessage(bg, &corev1.SendBulkSameMessageRequest{
		InstanceName: "shop", Recipients: []string{"34600111222"}, Text: shipped, IdempotencyKey: k("k1"),
	})
	refusedWith(t, err, codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT, nil, "k1", "34600111222", shipped)
	if n := h.jobCount(t); n != 1 {
		t.Fatalf("%d jobs, want 1", n)
	}
}

func TestAnInvalidBulkIdempotencyKeyIsInvalidArgument(t *testing.T) {
	h := start(t, "")
	testutil.CreateInstance(t, h.db, "shop")
	for _, key := range []string{"", strings.Repeat("x", 256), "my key"} {
		if _, err := h.messages.SendBulkSameMessage(bg, sameRequest(k(key), "34600000001")); codeOf(err) != codes.InvalidArgument {
			t.Fatalf("same, key of %d characters: %v", len(key), err)
		}
		if _, err := h.messages.SendBulkCustomMessages(bg, customRequest(k(key), "34600000001")); codeOf(err) != codes.InvalidArgument {
			t.Fatalf("custom, key of %d characters: %v", len(key), err)
		}
	}
}

func TestGetBulkJobsReturnsTheJobsAndRefusesBadCounts(t *testing.T) {
	h := start(t, "")
	testutil.CreateInstance(t, h.db, "shop")
	first, err := h.messages.SendBulkCustomMessages(bg, customRequest(k("g1"), "34600000001"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.messages.SendBulkCustomMessages(bg, customRequest(k("g2"), "34600000002", "34600000003"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := h.messages.GetBulkJobs(bg, &corev1.GetBulkJobsRequest{JobIds: []string{first.GetId(), "missing", second.GetId()}})
	if err != nil || len(got.GetJobs()) != 2 {
		t.Fatalf("got %v, %v", got, err)
	}
	total := map[string]int32{}
	for _, job := range got.GetJobs() {
		total[job.GetId()] = job.GetTotalMessages()
	}
	if total[first.GetId()] != 1 || total[second.GetId()] != 2 {
		t.Fatalf("totals %v", total)
	}
	_, err = h.messages.GetBulkJobs(bg, &corev1.GetBulkJobsRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("no ids: %v", err)
	}
}

func TestASendToAnInstanceThatIsNotConnectedIsRefusedAndAKnownKeyIsReplayed(t *testing.T) {
	h := start(t, "")
	h.connectedInstance(t, "shop")
	first, err := h.messages.SendPlainText(bg, sendRequest("34600111222", shipped, k("k1")))
	if err != nil {
		t.Fatal(err)
	}
	enabled := false
	if _, err := h.instances.UpdateInstanceConfig(bg, &corev1.UpdateInstanceConfigRequest{InstanceName: "shop", IsEnabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	h.exec(t, `UPDATE instances SET connection_status = 'NEEDS_AUTH' WHERE name = 'shop'`)

	_, err = h.messages.SendPlainText(bg, sendRequest("34600111222", "Another text", k("k2")))
	if codeOf(err) != codes.Unavailable {
		t.Fatalf("code %v, want UNAVAILABLE", codeOf(err))
	}
	var retry *errdetails.RetryInfo
	for _, d := range status.Convert(err).Details() {
		if r, ok := d.(*errdetails.RetryInfo); ok {
			retry = r
		}
	}
	if retry == nil || retry.GetRetryDelay().AsDuration() != 60*time.Second {
		t.Fatalf("RetryInfo %v, want 60 s", retry)
	}

	resp, err := h.messages.SendPlainText(bg, sendRequest("34600111222", shipped, k("k1")))
	if err != nil || !resp.GetReplayed() || resp.GetMessageId() != first.GetMessageId() {
		t.Fatalf("replay: got %v, %v", resp, err)
	}
}
