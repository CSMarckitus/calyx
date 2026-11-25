package engine_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/engine"
	"calyx/core/internal/events"
	"calyx/core/internal/testutil"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

const (
	connected  = corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED
	needsAuth  = corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH
	disconnect = corev1.InstanceStatus_INSTANCE_STATUS_DISCONNECTED
)

type orchestratorFixture struct {
	db    *sql.DB
	bus   *events.Bus
	store *engine.BulkStore
	o     *engine.Orchestrator

	mu          sync.Mutex
	builds      map[string]int
	fakes       map[string]*testutil.FakeProvider // the last one built per instance name
	rows        map[string]engine.InstanceRow     // the row of the last build per instance name
	intn        func(n int) int                   // the random choice of a device identity; nil gives 0
	intnCalls   []int                             // the n of every call
	configure   func(f *testutil.FakeProvider)    // applied to every new fake
	published   []events.Event
	invalidated []string
	clockOffset time.Duration // added to time.Now by the clock of the orchestrator
}

func newOrchestratorFixture(t *testing.T) *orchestratorFixture {
	t.Helper()
	f := &orchestratorFixture{
		db:     testutil.NewDB(t),
		bus:    events.NewBus(),
		builds: map[string]int{},
		fakes:  map[string]*testutil.FakeProvider{},
		rows:   map[string]engine.InstanceRow{},
	}
	f.store = engine.NewBulkStore(f.db)
	f.bus.Subscribe(func(e events.Event) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.published = append(f.published, e)
	})
	f.o = engine.NewOrchestrator(f.db, f.store, f.bus, engine.OrchestratorOptions{
		NormalizeRecipient: testutil.NormalizeRecipient,
		BuildProvider: func(row engine.InstanceRow, hooks engine.ProviderHooks) (engine.Provider, error) {
			fake := testutil.NewFakeProvider(row.Name)
			fake.SetHooks(hooks)
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.configure != nil {
				f.configure(fake)
			}
			f.builds[row.Name]++
			f.fakes[row.Name] = fake
			f.rows[row.Name] = row
			return fake, nil
		},
		Intn: func(n int) int {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.intnCalls = append(f.intnCalls, n)
			if f.intn == nil {
				return 0
			}
			return f.intn(n)
		},
		Now: func() time.Time {
			f.mu.Lock()
			defer f.mu.Unlock()
			return time.Now().Add(f.clockOffset)
		},
		SendOnlyIdle: 100 * time.Millisecond,
		Webhooks:     invalidatorFunc(func(name string) { f.mu.Lock(); f.invalidated = append(f.invalidated, name); f.mu.Unlock() }),
	})
	t.Cleanup(f.o.DestroyAllSessions)
	return f
}

// advance moves the clock of the orchestrator forward.
func (f *orchestratorFixture) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clockOffset += d
}

type invalidatorFunc func(string)

func (fn invalidatorFunc) Invalidate(name string) { fn(name) }

func (f *orchestratorFixture) fake(name string) *testutil.FakeProvider {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fakes[name]
}

func (f *orchestratorFixture) builtRow(name string) engine.InstanceRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rows[name]
}

func (f *orchestratorFixture) buildCount(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.builds[name]
}

func (f *orchestratorFixture) eventsOf(eventType string) []events.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	var found []events.Event
	for _, e := range f.published {
		if e.Type == eventType {
			found = append(found, e)
		}
	}
	return found
}

// instance stores an instance; changes is an optional SQL SET clause ("is_used_only_for_send_messages = 1").
func (f *orchestratorFixture) instance(t *testing.T, name, changes string) string {
	t.Helper()
	id := testutil.CreateInstance(t, f.db, name)
	if changes != "" {
		if _, err := f.db.Exec("UPDATE instances SET "+changes+" WHERE id = ?", id); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func (f *orchestratorFixture) storedStatus(t *testing.T, name string) string {
	t.Helper()
	var status string
	if err := f.db.QueryRow("SELECT connection_status FROM instances WHERE name = ?", name).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func bulkOf(recipients ...string) []engine.BulkMessageInput {
	messages := make([]engine.BulkMessageInput, len(recipients))
	for i, r := range recipients {
		messages[i] = engine.BulkMessageInput{To: r, Text: "Reminder"}
	}
	return messages
}

var ctx = context.Background()

func TestSendTextReturnsTheIdGivenByTheProvider(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.o.LoadAllActiveInstances(ctx)
	result, err := f.o.SendText(ctx, "shop", "34600111222", "hi", nil)
	if err != nil || result.MessageID != "fake-1" || result.Replayed {
		t.Fatalf("got %+v, %v", result, err)
	}
}

func TestSendTextOnAnUnknownInstanceIsNotFound(t *testing.T) {
	f := newOrchestratorFixture(t)
	if _, err := f.o.SendText(ctx, "nope", "34600111222", "hi", nil); calyxerr.CodeOf(err) != codes.NotFound {
		t.Fatalf("got %v", err)
	}
}

func TestABadRecipientOrAnEmptyTextIsInvalidAndNothingIsSent(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.o.LoadAllActiveInstances(ctx)
	if _, err := f.o.SendText(ctx, "shop", "not-a-number", "hi", nil); calyxerr.CodeOf(err) != codes.InvalidArgument {
		t.Errorf("bad recipient: %v", err)
	}
	if _, err := f.o.SendText(ctx, "shop", "34600111222", "  ", nil); calyxerr.CodeOf(err) != codes.InvalidArgument {
		t.Errorf("empty text: %v", err)
	}
	if fake := f.fake("shop"); fake != nil && len(fake.Sent()) != 0 {
		t.Fatal("something was sent")
	}
}

func TestAnInstanceThatNeedsAuthOrIsDisabledCannotBeUsed(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "lost", "connection_status = 'NEEDS_AUTH'")
	f.instance(t, "off", "is_enabled = 0")
	for _, name := range []string{"lost", "off"} {
		if _, err := f.o.SendText(ctx, name, "34600111222", "hi", nil); calyxerr.CodeOf(err) != codes.Unavailable {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestTwoParallelCallsOnASleepingSendOnlyInstanceBuildOneProvider(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "sender", "is_used_only_for_send_messages = 1")
	var wg sync.WaitGroup
	for _, r := range []string{"34600000001", "34600000002"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.o.SendText(ctx, "sender", r, "hi", nil); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := f.buildCount("sender"); n != 1 {
		t.Fatalf("%d providers built", n)
	}
}

func TestASendOnlyInstanceSleepsWhenIdleAndWakesForTheNextCall(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "sender", "is_used_only_for_send_messages = 1")
	if _, err := f.o.SendText(ctx, "sender", "34600000001", "hi", nil); err != nil {
		t.Fatal(err)
	}
	first := f.fake("sender")
	waitFor(t, "sleep", func() bool { _, destroys, _ := first.Counts(); return destroys == 1 }, 3*time.Second)

	if _, err := f.o.SendText(ctx, "sender", "34600000001", "hi again", nil); err != nil {
		t.Fatal(err)
	}
	if n := f.buildCount("sender"); n != 2 {
		t.Fatalf("%d providers built, want 2", n)
	}
}

func TestASendOnlyInstanceStaysAwakeWhileItsBulkJobRuns(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "sender", "is_used_only_for_send_messages = 1")
	delay := 150
	messages := bulkOf("34600000001", "34600000002", "34600000003")
	for i := range messages {
		messages[i].DelayMs = &delay
	}
	job, _, err := f.o.CreateBulkJob(ctx, "sender", engine.BulkCustom, messages, engine.BulkPacingOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "job completed", func() bool {
		j, _ := f.store.Get(ctx, job.ID)
		return j.Status == engine.JobCompleted
	}, 3*time.Second)
	if _, destroys, _ := f.fake("sender").Counts(); destroys != 0 {
		// 3 x 150 ms > 100 ms idle time: it would have slept in the middle of the job
		t.Fatalf("closed %d times while the job ran", destroys)
	}
}

func TestANormalInstanceIsNotClosedWhenIdle(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.o.LoadAllActiveInstances(ctx)
	if _, err := f.o.SendText(ctx, "shop", "34600000001", "hi", nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, destroys, _ := f.fake("shop").Counts(); destroys != 0 {
		t.Fatal("a normal instance was closed")
	}
}

func TestStatusAndListNeverBuildAProvider(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.instance(t, "sender", "is_used_only_for_send_messages = 1")

	status, err := f.o.GetInstanceStatus(ctx, "sender")
	if err != nil || status != connected {
		t.Fatalf("sleeping send-only: %v, %v", status, err)
	}
	if status, _ := f.o.GetInstanceStatus(ctx, "shop"); status != disconnect {
		t.Fatalf("a normal instance that is not loaded: %v", status)
	}
	if _, err := f.o.ListInstances(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.o.GetInstanceStatus(ctx, "nope"); calyxerr.CodeOf(err) != codes.NotFound {
		t.Fatalf("unknown: %v", err)
	}
	if f.buildCount("shop")+f.buildCount("sender") != 0 {
		t.Fatal("a provider was built")
	}
}

func TestListInstancesReportsWebhooksWithoutSecrets(t *testing.T) {
	f := newOrchestratorFixture(t)
	id := f.instance(t, "shop", "")
	if _, err := f.db.Exec(`INSERT INTO webhooks (id, instance_id, event_type, url, secret)
		VALUES ('w1', ?, 'message.received', 'https://example.test/hook', 's3cret')`, id); err != nil {
		t.Fatal(err)
	}
	list, err := f.o.ListInstances(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || len(list[0].Webhooks) != 1 || !list[0].Webhooks[0].HasSecret ||
		list[0].Webhooks[0].URL != "https://example.test/hook" || list[0].Provider != "WHATSAPP_WEB" {
		t.Fatalf("got %+v", list)
	}
}

func TestLoadAllActiveInstancesLeavesSendOnlyOnesAsleep(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.instance(t, "sender", "is_used_only_for_send_messages = 1")
	f.instance(t, "lost", "connection_status = 'NEEDS_AUTH'")
	f.instance(t, "off", "is_enabled = 0")
	busy := f.instance(t, "busy-sender", "is_used_only_for_send_messages = 1")
	plan, ms, _ := engine.PlanBulk(bulkOf("34600000001"), engine.BulkPacingOptions{}, testutil.NewFakeProvider("x").Pacing())
	if _, _, err := f.store.Create(ctx, busy, plan, ms, nil); err != nil {
		t.Fatal(err)
	}

	f.o.LoadAllActiveInstances(ctx)
	for name, want := range map[string]int{"shop": 1, "busy-sender": 1, "sender": 0, "lost": 0, "off": 0} {
		if got := f.buildCount(name); got != want {
			t.Errorf("%s built %d times, want %d", name, got, want)
		}
	}
}

func TestABulkJobCompletesAndPublishesBulkFinished(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	job, _, err := f.o.CreateBulkJob(ctx, "shop", engine.BulkCustom, bulkOf("34600000001", "34600000002"), engine.BulkPacingOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if job.InstanceName != "shop" || job.TotalMessages != 2 {
		t.Fatalf("got %+v", job)
	}
	waitFor(t, "bulk.finished", func() bool { return len(f.eventsOf("bulk.finished")) == 1 }, 3*time.Second)
	payload := f.eventsOf("bulk.finished")[0].Payload.(events.BulkFinished)
	if payload.JobID != job.ID || payload.Status != "COMPLETED" || payload.SentMessages != 2 {
		t.Fatalf("got %+v", payload)
	}
}

// Guard, not a red test: bulk jobs use their own queue, so a full direct queue must not block them.
func TestABulkJobIsCreatedWhenTheDirectQueueIsFull(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.o.LoadAllActiveInstances(ctx)
	if _, err := f.o.SendText(ctx, "shop", "34600000009", "load the instance", nil); err != nil {
		t.Fatal(err)
	}
	_, release := holdSends(t, f.fake("shop"))
	runCtx, stop := context.WithCancel(ctx)
	t.Cleanup(stop)

	// The fake allows 3 waiting messages. Five at once leave at least one refused.
	refused := make(chan error, 5)
	for i := range 5 {
		go func() {
			_, err := f.o.SendText(runCtx, "shop", "3460011122"+string(rune('0'+i)), "text", nil)
			refused <- err
		}()
	}
	select {
	case err := <-refused:
		wantReason(t, err, codes.ResourceExhausted, corev1.ErrorReason_ERROR_REASON_INSTANCE_QUEUE_FULL)
	case <-time.After(3 * time.Second):
		t.Fatal("no send was refused")
	}

	job, _, err := f.o.CreateBulkJob(ctx, "shop", engine.BulkCustom, bulkOf("34600000001"), engine.BulkPacingOptions{}, nil)
	if err != nil {
		t.Fatalf("the bulk job was refused: %v", err)
	}
	if job.InstanceName != "shop" {
		t.Fatalf("got %+v", job)
	}
	release()
}

// Anti-spam: in one job, a recipient gets at most one text of a same-text job and at most two
// messages of a custom job. The whole job is refused.
func TestARecipientWithTooManyMessagesIsRefused(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	_, _, err := f.o.CreateBulkJob(ctx, "shop", engine.BulkSame, bulkOf("34600000002", "34600000001", "34600000001"), engine.BulkPacingOptions{}, nil)
	if calyxerr.CodeOf(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "Message 2:") || strings.Contains(err.Error(), "34600000001") {
		t.Fatalf("same text, repeated recipient: %v", err)
	}
	two, _, err := f.o.CreateBulkJob(ctx, "shop", engine.BulkCustom, bulkOf("34600000001", "34600000001"), engine.BulkPacingOptions{}, nil)
	if err != nil || two.TotalMessages != 2 {
		t.Fatalf("custom, two messages: %+v, %v", two, err)
	}
	_, _, err = f.o.CreateBulkJob(ctx, "shop", engine.BulkCustom, bulkOf("34600000001", "34600000002", "34600000001", "34600000001"), engine.BulkPacingOptions{}, nil)
	if calyxerr.CodeOf(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "Message 3:") || !strings.Contains(err.Error(), "spam") {
		t.Fatalf("custom, three messages: %v", err)
	}
}

func TestInvalidBulkJobsAreRejected(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	cases := map[string][]engine.BulkMessageInput{
		"no messages":   nil,
		"empty text":    {{To: "34600000001", Text: " "}},
		"bad recipient": {{To: "34600000001", Text: "hi"}, {To: "x", Text: "hi"}},
	}
	for name, messages := range cases {
		_, _, err := f.o.CreateBulkJob(ctx, "shop", engine.BulkCustom, messages, engine.BulkPacingOptions{}, nil)
		if calyxerr.CodeOf(err) != codes.InvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
		if name == "bad recipient" && !strings.Contains(err.Error(), "Message 1") {
			t.Errorf("the error does not say which message: %v", err)
		}
	}
	tooFast := &engine.Range{Min: 1, Max: 5}
	if _, _, err := f.o.CreateBulkJob(ctx, "shop", engine.BulkCustom, bulkOf("34600000001"), engine.BulkPacingOptions{Delay: tooFast}, nil); calyxerr.CodeOf(err) != codes.InvalidArgument {
		t.Errorf("pacing below the limits: %v", err)
	}
}

func TestCancelBulkJobReturnsCancelledAndCancellingAgainChangesNothing(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	delay := 5000
	messages := bulkOf("34600000001", "34600000002")
	for i := range messages {
		messages[i].DelayMs = &delay
	}
	job, _, err := f.o.CreateBulkJob(ctx, "shop", engine.BulkCustom, messages, engine.BulkPacingOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := f.o.CancelBulkJob(ctx, job.ID)
	if err != nil || cancelled.Status != engine.JobCancelled {
		t.Fatalf("got %+v, %v", cancelled, err)
	}
	again, err := f.o.CancelBulkJob(ctx, job.ID)
	if err != nil || again.Status != engine.JobCancelled {
		t.Fatalf("second cancel: %+v, %v", again, err)
	}
	if n := len(f.eventsOf("bulk.finished")); n != 1 {
		t.Fatalf("bulk.finished published %d times", n)
	}
	if _, err := f.o.CancelBulkJob(ctx, "missing"); calyxerr.CodeOf(err) != codes.NotFound {
		t.Fatalf("unknown job: %v", err)
	}
	if _, err := f.o.GetBulkJob(ctx, "missing"); calyxerr.CodeOf(err) != codes.NotFound {
		t.Fatalf("unknown job: %v", err)
	}
}

func TestUnfinishedJobsContinueAfterARestartWithoutSendingTwice(t *testing.T) {
	f := newOrchestratorFixture(t)
	id := f.instance(t, "shop", "")
	plan, ms, _ := engine.PlanBulk(bulkOf("34600000001", "34600000002", "34600000003"), engine.BulkPacingOptions{}, testutil.NewFakeProvider("x").Pacing())
	job, _, err := f.store.Create(ctx, id, plan, ms, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The previous run sent the first message and was killed while sending the second.
	first, _ := f.store.NextPending(ctx, job.ID)
	_ = f.store.MarkSending(ctx, first.ID)
	_ = f.store.MarkSent(ctx, job.ID, first.ID, "old-1")
	second, _ := f.store.NextPending(ctx, job.ID)
	_ = f.store.MarkSending(ctx, second.ID)

	f.o.LoadAllActiveInstances(ctx)
	waitFor(t, "job completed", func() bool {
		j, _ := f.store.Get(ctx, job.ID)
		return j.Status == engine.JobCompleted
	}, 3*time.Second)
	sent := f.fake("shop").Sent()
	if len(sent) != 1 || sent[0].Recipient != "34600000003" {
		t.Fatalf("sent after the restart: %+v", sent)
	}
}

func TestRemovingAnInstanceStopsItsJobAndLogsOut(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	delay := 5000
	messages := bulkOf("34600000001", "34600000002")
	for i := range messages {
		messages[i].DelayMs = &delay
	}
	job, _, err := f.o.CreateBulkJob(ctx, "shop", engine.BulkCustom, messages, engine.BulkPacingOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.o.RemoveInstance(ctx, "shop"); err != nil {
		t.Fatal(err)
	}
	if _, _, logouts := f.fake("shop").Counts(); logouts != 1 {
		t.Fatal("no logout")
	}
	if _, err := f.store.Get(ctx, job.ID); calyxerr.CodeOf(err) != codes.NotFound {
		t.Fatal("the job survived its instance")
	}
	if err := f.o.RemoveInstance(ctx, "shop"); calyxerr.CodeOf(err) != codes.NotFound {
		t.Fatalf("removing again: %v", err)
	}
}

func TestRemovingASleepingInstanceWakesItToLogOut(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "sender", "is_used_only_for_send_messages = 1")
	if err := f.o.RemoveInstance(ctx, "sender"); err != nil {
		t.Fatal(err)
	}
	if f.buildCount("sender") != 1 {
		t.Fatal("not woken")
	}
	if _, _, logouts := f.fake("sender").Counts(); logouts != 1 {
		t.Fatal("no logout")
	}
}

func TestRemovingGoesAheadWhenTheInstanceCannotConnect(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.configure = func(fake *testutil.FakeProvider) { fake.FailConnect(errors.New("banned")) }
	f.instance(t, "sender", "is_used_only_for_send_messages = 1")
	if err := f.o.RemoveInstance(ctx, "sender"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.o.GetInstanceStatus(ctx, "sender"); calyxerr.CodeOf(err) != codes.NotFound {
		t.Fatal("still there")
	}
}

func TestAStatusChangeIsSavedAndPublished(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.o.LoadAllActiveInstances(ctx)
	f.fake("shop").ForceStatus(disconnect)

	waitFor(t, "status saved", func() bool { return f.storedStatus(t, "shop") == "DISCONNECTED" }, time.Second)
	updates := f.eventsOf("connection.update")
	if len(updates) != 1 || updates[0].Payload.(events.ConnectionUpdate).Status != "DISCONNECTED" {
		t.Fatalf("got %+v", updates)
	}
}

func TestProviderEventsArePublished(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.o.LoadAllActiveInstances(ctx)
	f.fake("shop").Emit("message.received", events.MessageReceived{MessageID: "m1"})
	if got := f.eventsOf("message.received"); len(got) != 1 || got[0].InstanceName != "shop" {
		t.Fatalf("got %+v", got)
	}
}

func TestUpdatingOnlyWebhooksDoesNotRebuildTheProviderRenamingDoes(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.o.LoadAllActiveInstances(ctx)

	hooks := []engine.WebhookInput{{URL: "https://example.test/a", Event: "message.received"}}
	if err := f.o.UpdateInstanceConfig(ctx, "shop", engine.UpdateInstanceParams{Webhooks: &hooks}); err != nil {
		t.Fatal(err)
	}
	if f.buildCount("shop") != 1 {
		t.Fatal("rebuilt for a webhook change")
	}

	newName := "store"
	if err := f.o.UpdateInstanceConfig(ctx, "shop", engine.UpdateInstanceParams{NewName: &newName}); err != nil {
		t.Fatal(err)
	}
	if f.buildCount("store") != 1 {
		t.Fatal("not rebuilt under the new name")
	}
	f.mu.Lock()
	invalidated := slices.Clone(f.invalidated)
	f.mu.Unlock()
	if !slices.Contains(invalidated, "shop") || !slices.Contains(invalidated, "store") {
		t.Fatalf("invalidated %v", invalidated)
	}
}

func TestReplacingWebhooksRemovesThePreviousOnes(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	first := []engine.WebhookInput{{URL: "https://example.test/a", Event: "message.received", Secret: "x"}}
	second := []engine.WebhookInput{{URL: "https://example.test/b", Event: "message.status"}}
	for _, hooks := range [][]engine.WebhookInput{first, second} {
		if err := f.o.UpdateInstanceConfig(ctx, "shop", engine.UpdateInstanceParams{Webhooks: &hooks}); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := f.o.ListInstances(ctx)
	if len(list[0].Webhooks) != 1 || list[0].Webhooks[0].URL != "https://example.test/b" || list[0].Webhooks[0].HasSecret {
		t.Fatalf("got %+v", list[0].Webhooks)
	}
}

func TestDisablingAnInstanceClosesItAndEnablingBringsItBack(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.o.LoadAllActiveInstances(ctx)
	off, on := false, true
	if err := f.o.UpdateInstanceConfig(ctx, "shop", engine.UpdateInstanceParams{IsEnabled: &off}); err != nil {
		t.Fatal(err)
	}
	if _, destroys, _ := f.fake("shop").Counts(); destroys != 1 {
		t.Fatal("not closed")
	}
	if _, err := f.o.SendText(ctx, "shop", "34600000001", "hi", nil); calyxerr.CodeOf(err) != codes.Unavailable {
		t.Fatalf("disabled: %v", err)
	}
	if err := f.o.UpdateInstanceConfig(ctx, "shop", engine.UpdateInstanceParams{IsEnabled: &on}); err != nil {
		t.Fatal(err)
	}
	if f.buildCount("shop") != 2 {
		t.Fatal("not loaded again")
	}
}

func TestWebhookValidation(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.instance(t, "sender", "is_used_only_for_send_messages = 1")
	cases := map[string][]engine.WebhookInput{
		"ftp url":           {{URL: "ftp://example.test", Event: "message.received"}},
		"no url":            {{URL: "", Event: "message.received"}},
		"unsupported event": {{URL: "https://example.test", Event: "meta.webhook"}},
		"twice the same":    {{URL: "https://a.test", Event: "message.received"}, {URL: "https://b.test", Event: "message.received"}},
	}
	for name, hooks := range cases {
		err := f.o.UpdateInstanceConfig(ctx, "shop", engine.UpdateInstanceParams{Webhooks: &hooks})
		if calyxerr.CodeOf(err) != codes.InvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
	hooks := []engine.WebhookInput{{URL: "https://example.test", Event: "whatsmeow.Message"}}
	if err := f.o.UpdateInstanceConfig(ctx, "sender", engine.UpdateInstanceParams{Webhooks: &hooks}); calyxerr.CodeOf(err) != codes.InvalidArgument {
		t.Errorf("send-only with webhooks: %v", err)
	}
	if err := f.o.UpdateInstanceConfig(ctx, "shop", engine.UpdateInstanceParams{Webhooks: &hooks}); err != nil {
		t.Errorf("raw whatsmeow event: %v", err)
	}
}

func TestNameConflictsAreAlreadyExists(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	f.instance(t, "store", "")
	name := "store"
	if err := f.o.UpdateInstanceConfig(ctx, "shop", engine.UpdateInstanceParams{NewName: &name}); calyxerr.CodeOf(err) != codes.AlreadyExists {
		t.Errorf("rename: %v", err)
	}
	err := f.o.CreateInstance(ctx, engine.CreateInstanceParams{Name: "shop", Provider: "WHATSAPP_WEB", Pairing: engine.PairingMethod{QR: true}, Locale: "es-ES"},
		func(engine.CreateInstanceUpdate) {})
	if calyxerr.CodeOf(err) != codes.AlreadyExists {
		t.Errorf("create: %v", err)
	}
}

func TestCreateInstanceSendsEveryCodeAndEndsConnected(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.configure = func(fake *testutil.FakeProvider) {
		fake.SetPair(func(ctx context.Context, method engine.PairingMethod, onCode func(string)) error {
			if !method.QR {
				t.Error("QR expected")
			}
			onCode("QR-1")
			onCode("QR-2")
			return nil
		})
	}
	var updates []engine.CreateInstanceUpdate
	params := engine.CreateInstanceParams{
		Name: "shop", Provider: "WHATSAPP_WEB", Pairing: engine.PairingMethod{QR: true}, Locale: "es-ES",
		Webhooks: []engine.WebhookInput{{URL: "https://example.test", Event: "message.received"}},
	}
	if err := f.o.CreateInstance(ctx, params, func(u engine.CreateInstanceUpdate) { updates = append(updates, u) }); err != nil {
		t.Fatal(err)
	}
	if len(updates) != 3 || updates[0].PairingCode != "QR-1" || updates[1].PairingCode != "QR-2" ||
		updates[0].Status != needsAuth || updates[2].Status != connected {
		t.Fatalf("got %+v", updates)
	}
	if f.storedStatus(t, "shop") != "CONNECTED" {
		t.Fatal("status not saved")
	}
	if _, err := f.o.SendText(ctx, "shop", "34600000001", "hi", nil); err != nil {
		t.Fatalf("not usable after pairing: %v", err)
	}
	if f.buildCount("shop") != 1 {
		t.Fatal("the paired provider was not reused")
	}
}

func TestAFailedPairingLeavesNoInstanceBehind(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.configure = func(fake *testutil.FakeProvider) {
		fake.SetPair(func(ctx context.Context, method engine.PairingMethod, onCode func(string)) error {
			return calyxerr.New(codes.DeadlineExceeded, "nobody scanned")
		})
	}
	err := f.o.CreateInstance(ctx, engine.CreateInstanceParams{Name: "shop", Provider: "WHATSAPP_WEB", Pairing: engine.PairingMethod{QR: true}, Locale: "es-ES"},
		func(engine.CreateInstanceUpdate) {})
	if calyxerr.CodeOf(err) != codes.DeadlineExceeded {
		t.Fatalf("got %v", err)
	}
	if _, err := f.o.GetInstanceStatus(ctx, "shop"); calyxerr.CodeOf(err) != codes.NotFound {
		t.Fatal("the unpaired instance was kept")
	}
	if _, destroys, _ := f.fake("shop").Counts(); destroys != 1 {
		t.Fatal("the provider was not destroyed")
	}
}

func TestAnInstanceThatNeedsAuthCanBePairedAgain(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "connection_status = 'NEEDS_AUTH'")
	err := f.o.CreateInstance(ctx, engine.CreateInstanceParams{Name: "shop", Provider: "WHATSAPP_WEB", Pairing: engine.PairingMethod{PhoneNumber: "34600111222"}, Locale: "es-ES"},
		func(engine.CreateInstanceUpdate) {})
	if err != nil {
		t.Fatal(err)
	}
	if f.storedStatus(t, "shop") != "CONNECTED" {
		t.Fatal("not connected")
	}
}

func TestOnlyOnePairingPerNameAtATime(t *testing.T) {
	f := newOrchestratorFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	f.configure = func(fake *testutil.FakeProvider) {
		fake.SetPair(func(ctx context.Context, method engine.PairingMethod, onCode func(string)) error {
			close(started)
			<-release
			return nil
		})
	}
	params := engine.CreateInstanceParams{Name: "shop", Provider: "WHATSAPP_WEB", Pairing: engine.PairingMethod{QR: true}, Locale: "es-ES"}
	done := make(chan error, 1)
	go func() { done <- f.o.CreateInstance(ctx, params, func(engine.CreateInstanceUpdate) {}) }()
	<-started
	if err := f.o.CreateInstance(ctx, params, func(engine.CreateInstanceUpdate) {}); calyxerr.CodeOf(err) != codes.AlreadyExists {
		t.Errorf("second pairing: %v", err)
	}
	if err := f.o.RemoveInstance(ctx, "shop"); calyxerr.CodeOf(err) != codes.FailedPrecondition {
		t.Errorf("remove while pairing: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPacingSurvivesARebuildOfTheInstance(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.configure = func(fake *testutil.FakeProvider) {
		p := fake.Pacing()
		p.MinGapMs = 300
		fake.SetPacing(p)
	}
	f.instance(t, "shop", "")
	f.o.LoadAllActiveInstances(ctx)
	if _, err := f.o.SendText(ctx, "shop", "34600000001", "first", nil); err != nil {
		t.Fatal(err)
	}
	first := f.fake("shop")
	off, on := false, true
	_ = f.o.UpdateInstanceConfig(ctx, "shop", engine.UpdateInstanceParams{IsEnabled: &off})
	_ = f.o.UpdateInstanceConfig(ctx, "shop", engine.UpdateInstanceParams{IsEnabled: &on})
	if _, err := f.o.SendText(ctx, "shop", "34600000002", "second", nil); err != nil {
		t.Fatal(err)
	}
	second := f.fake("shop")
	if first == second {
		t.Fatal("the instance was not rebuilt")
	}
	if gap := second.Sent()[0].At.Sub(first.Sent()[0].At); gap < 300*time.Millisecond {
		t.Fatalf("a rebuild let the next message out %v after the previous one", gap)
	}
}

// blockFirstConnect makes the first Connect of the fixture wait until the returned proceed is
// closed; connecting is closed when it starts waiting.
func (f *orchestratorFixture) blockFirstConnect() (connecting, proceed chan struct{}) {
	connecting, proceed = make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.configure = func(fake *testutil.FakeProvider) {
		fake.BeforeConnect(func(context.Context) {
			once.Do(func() {
				close(connecting)
				<-proceed
			})
		})
	}
	return connecting, proceed
}

func (f *orchestratorFixture) waitForRow(t *testing.T, query string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := f.db.QueryRow(query).Scan(&n); err == nil && n == 1 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q", query)
}

// Formally checks that a disabled instance stays closed: a load reads the row
// while it is enabled, the instance is disabled, and the load registers it afterwards.
func TestDisablingAnInstanceWhileItLoadsLeavesItClosed(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	connecting, proceed := f.blockFirstConnect()
	loaded := make(chan struct{})
	go func() {
		defer close(loaded)
		f.o.LoadAllActiveInstances(ctx)
	}()
	<-connecting

	updated := make(chan error, 1)
	off := false
	go func() { updated <- f.o.UpdateInstanceConfig(ctx, "shop", engine.UpdateInstanceParams{IsEnabled: &off}) }()
	f.waitForRow(t, "SELECT COUNT(*) FROM instances WHERE name = 'shop' AND is_enabled = 0")
	close(proceed)
	if err := <-updated; err != nil {
		t.Fatal(err)
	}
	<-loaded

	if n := len(f.o.LiveProviders()); n != 0 {
		t.Fatalf("%d live providers for a disabled instance", n)
	}
}

// Formally checks that a removed instance leaves nothing live: a load reads the row,
// the row is disabled (an update in progress), the removal does not wake the instance and deletes
// the row, and the load registers it afterwards.
func TestRemovingAnInstanceWhileItLoadsLeavesNothingLive(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "")
	connecting, proceed := f.blockFirstConnect()
	loaded := make(chan struct{})
	go func() {
		defer close(loaded)
		f.o.LoadAllActiveInstances(ctx)
	}()
	<-connecting
	// What an UpdateInstanceConfig disabling it has written at this point.
	if _, err := f.db.Exec("UPDATE instances SET is_enabled = 0 WHERE name = 'shop'"); err != nil {
		t.Fatal(err)
	}

	removed := make(chan error, 1)
	go func() { removed <- f.o.RemoveInstance(ctx, "shop") }()
	select { // let the removal reach the point where it waits, or finish
	case err := <-removed:
		removed <- err
	case <-time.After(100 * time.Millisecond):
	}
	close(proceed)
	if err := <-removed; err != nil {
		t.Fatal(err)
	}
	<-loaded

	if n := len(f.o.LiveProviders()); n != 0 {
		t.Fatalf("%d live providers after the removal", n)
	}
	if _, destroys, logouts := f.fake("shop").Counts(); destroys+logouts == 0 {
		t.Fatal("the connection opened by the load was never closed")
	}
}

func TestGetBulkJobsRefusesZeroOrTooManyIds(t *testing.T) {
	f := newOrchestratorFixture(t)
	for _, n := range []int{0, 51} {
		ids := make([]string, n)
		for i := range ids {
			ids[i] = "job"
		}
		if _, err := f.o.GetBulkJobs(ctx, ids); calyxerr.CodeOf(err) != codes.InvalidArgument {
			t.Fatalf("%d ids: %v", n, err)
		}
	}
}

func TestWithoutTheNormalizerOptionTheOrchestratorRefusesEveryProvider(t *testing.T) {
	o := engine.NewOrchestrator(testutil.NewDB(t), nil, events.NewBus(), engine.OrchestratorOptions{})
	_, err := engine.NormalizeRecipientOf(o, "META_CLOUD", "34600111222")
	if calyxerr.CodeOf(err) != codes.FailedPrecondition {
		t.Fatalf("got %v", err)
	}
}
