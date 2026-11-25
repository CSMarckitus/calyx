// Package e2e runs the whole core in one process, with fakes only at its edges: a fake Graph API
// instead of Meta, and a local HTTP server as the customer's webhook receiver.
package e2e_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"calyx/core/internal/engine"
	"calyx/core/internal/events"
	"calyx/core/internal/grpcserver"
	"calyx/core/internal/providers/meta"
	"calyx/core/internal/testutil"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

func hmacHex(secret string, parts ...string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	for _, part := range parts {
		mac.Write([]byte(part))
	}
	return hex.EncodeToString(mac.Sum(nil))
}

type customerReceiver struct {
	mu       sync.Mutex
	verified int
	got      int
}

func TestAMetaInstanceWorksEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Edges: Meta and the customer's webhook receiver.
	graph := testutil.NewFakeGraphAPI(t)
	receiver := &customerReceiver{}
	receiverServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receiver.mu.Lock()
		defer receiver.mu.Unlock()
		receiver.got++
		want := "sha256=" + hmacHex("s3cret", r.Header.Get("X-Calyx-Timestamp"), ".", string(body))
		if r.Header.Get("X-Calyx-Signature") == want {
			receiver.verified++
		}
	}))
	defer receiverServer.Close()

	// The core, wired like cmd/core.
	db := testutil.NewDB(t)
	bus := events.NewBus()
	dispatcher := events.NewDispatcher(db, bus, events.DispatcherOptions{})
	dispatcher.Start()
	defer dispatcher.Stop()
	orchestrator := engine.NewOrchestrator(db, engine.NewBulkStore(db), bus, engine.OrchestratorOptions{
		NormalizeRecipient: testutil.NormalizeRecipient,
		BuildProvider: func(row engine.InstanceRow, hooks engine.ProviderHooks) (engine.Provider, error) {
			return meta.New(row, hooks, meta.Options{GraphURL: graph.URL})
		},
		Webhooks: dispatcher,
	})
	defer orchestrator.DestroyAllSessions()
	if err := orchestrator.StartSendRecords(context.Background()); err != nil {
		t.Fatal(err)
	}
	metaWebhooks := httptest.NewServer(meta.NewWebhookServer(orchestrator.LiveProviders, "verify-me"))
	defer metaWebhooks.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpcserver.New(orchestrator, bus, grpcserver.Options{})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop(time.Second)

	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	instances := corev1.NewInstanceServiceClient(conn)
	messages := corev1.NewMessageServiceClient(conn)
	eventStream := corev1.NewEventServiceClient(conn)

	// 1. Create the instance: one CONNECTED update.
	secret := "s3cret"
	create, err := instances.CreateInstance(ctx, &corev1.CreateInstanceRequest{
		InstanceName: "shop",
		Webhooks:     []*corev1.WebhookConfig{{Url: receiverServer.URL, Event: "message.received", Secret: &secret}},
		ProviderSettings: &corev1.CreateInstanceRequest_MetaCloud{MetaCloud: &corev1.MetaCloudSettings{
			PhoneNumberId: "123", AccessToken: "good-token", AppSecret: "app-secret",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var updates []*corev1.CreateInstanceResponse
	for {
		update, err := create.Recv()
		if err != nil {
			break
		}
		updates = append(updates, update)
	}
	if len(updates) != 1 || updates[0].GetStatus() != corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED {
		t.Fatalf("1. create: %v", updates)
	}

	// 2. Subscribe.
	stream, err := eventStream.Subscribe(ctx, &corev1.SubscribeRequest{EventTypes: []string{"message.received", "bulk.finished"}})
	if err != nil {
		t.Fatal(err)
	}
	received := make(chan *corev1.Event, 10)
	go func() {
		for {
			e, err := stream.Recv()
			if err != nil {
				return
			}
			received <- e
		}
	}()
	time.Sleep(100 * time.Millisecond) // let the server register the subscription

	// 3. Send one message.
	sent, err := messages.SendPlainText(ctx, &corev1.SendPlainTextRequest{InstanceName: "shop", To: "+34 600 111 222", Text: "hi"})
	if err != nil || !strings.HasPrefix(sent.GetMessageId(), "wamid.") {
		t.Fatalf("3. send: %v, %v", sent, err)
	}
	if last := graph.Requests()[len(graph.Requests())-1]; last.Body["to"] != "34600111222" {
		t.Fatalf("3. Meta got %+v", last.Body)
	}

	// 4. A bulk job completes and bulk.finished arrives on the stream.
	job, err := messages.SendBulkSameMessage(ctx, &corev1.SendBulkSameMessageRequest{
		InstanceName: "shop", Recipients: []string{"34600000001", "34600000002", "34600000003"}, Text: "Reminder",
	})
	if err != nil {
		t.Fatal(err)
	}
	for job.GetStatus() != corev1.BulkJobStatus_BULK_JOB_STATUS_COMPLETED {
		time.Sleep(50 * time.Millisecond)
		if job, err = messages.GetBulkJob(ctx, &corev1.GetBulkJobRequest{JobId: job.GetId()}); err != nil {
			t.Fatal(err)
		}
	}
	if job.GetProgressPercent() != 100 {
		t.Fatalf("4. progress %v", job.GetProgressPercent())
	}
	if e := waitEvent(t, received, "bulk.finished"); !strings.Contains(e.GetPayloadJson(), `"sentMessages":3`) {
		t.Fatalf("4. bulk.finished %s", e.GetPayloadJson())
	}

	// 5. Meta posts an incoming message: it reaches the stream and the signed customer webhook.
	body := []byte(`{"object":"whatsapp_business_account","entry":[{"changes":[{"field":"messages","value":{` +
		`"metadata":{"phone_number_id":"123"},"messages":[{"id":"wamid.IN","from":"34600111222","timestamp":"1700000000","type":"text","text":{"body":"hi"}}]}}]}]}`)
	req, _ := http.NewRequest(http.MethodPost, metaWebhooks.URL+"/meta/webhook", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256="+hmacHex("app-secret", string(body)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("5. Meta webhook: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("5. Meta webhook: status %d", resp.StatusCode)
	}
	if e := waitEvent(t, received, "message.received"); !strings.Contains(e.GetPayloadJson(), `"text":"hi"`) {
		t.Fatalf("5. message.received %s", e.GetPayloadJson())
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		receiver.mu.Lock()
		got, verified := receiver.got, receiver.verified
		receiver.mu.Unlock()
		if got == 1 && verified == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("5. customer webhook: got %d, verified %d", got, verified)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 6. Remove it.
	if _, err := instances.RemoveInstance(ctx, &corev1.RemoveInstanceRequest{InstanceName: "shop"}); err != nil {
		t.Fatal(err)
	}
	list, err := instances.ListInstances(ctx, &corev1.ListInstancesRequest{})
	if err != nil || len(list.GetInstances()) != 0 {
		t.Fatalf("6. list: %v, %v", list, err)
	}
}

func waitEvent(t *testing.T, received chan *corev1.Event, eventType string) *corev1.Event {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e := <-received:
			if e.GetType() == eventType {
				return e
			}
		case <-timeout:
			t.Fatalf("no %s event", eventType)
		}
	}
}
