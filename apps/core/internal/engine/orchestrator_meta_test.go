package engine_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/engine"
	"calyx/core/internal/events"
	"calyx/core/internal/providers/meta"
	"calyx/core/internal/testutil"
)

type metaFixture struct {
	o     *engine.Orchestrator
	store *engine.BulkStore
	graph *testutil.FakeGraphAPI
	bus   *events.Bus
}

func newMetaFixture(t *testing.T) *metaFixture {
	t.Helper()
	db := testutil.NewDB(t)
	f := &metaFixture{store: engine.NewBulkStore(db), graph: testutil.NewFakeGraphAPI(t), bus: events.NewBus()}
	f.o = engine.NewOrchestrator(db, f.store, f.bus, engine.OrchestratorOptions{
		NormalizeRecipient: testutil.NormalizeRecipient,
		BuildProvider: func(row engine.InstanceRow, hooks engine.ProviderHooks) (engine.Provider, error) {
			return meta.New(row, hooks, meta.Options{GraphURL: f.graph.URL})
		},
	})
	t.Cleanup(f.o.DestroyAllSessions)
	return f
}

func metaParams(name, phoneNumberID, token string) engine.CreateInstanceParams {
	config, _ := json.Marshal(engine.MetaCredentials{PhoneNumberID: phoneNumberID, AccessToken: token, AppSecret: "app-secret"})
	return engine.CreateInstanceParams{Name: name, Provider: "META_CLOUD", ProviderConfig: string(config)}
}

func (f *metaFixture) create(t *testing.T, params engine.CreateInstanceParams) ([]engine.CreateInstanceUpdate, error) {
	var updates []engine.CreateInstanceUpdate
	err := f.o.CreateInstance(ctx, params, func(u engine.CreateInstanceUpdate) { updates = append(updates, u) })
	return updates, err
}

func TestCreatingAMetaInstanceValidatesTheCredentialsAndReportsConnectedOnce(t *testing.T) {
	f := newMetaFixture(t)
	updates, err := f.create(t, metaParams("shop", "123", "good-token"))
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 1 || updates[0].Status != connected {
		t.Fatalf("got %+v", updates)
	}
}

func TestABadTokenCreatesNothing(t *testing.T) {
	f := newMetaFixture(t)
	if _, err := f.create(t, metaParams("shop", "123", "bad-token")); calyxerr.CodeOf(err) != codes.FailedPrecondition {
		t.Fatalf("got %v", err)
	}
	if list, _ := f.o.ListInstances(ctx); len(list) != 0 {
		t.Fatalf("left %+v", list)
	}
}

func TestSendOnlyIsRejectedForMeta(t *testing.T) {
	f := newMetaFixture(t)
	params := metaParams("shop", "123", "good-token")
	yes := true
	params.SendOnly = &yes
	if _, err := f.create(t, params); calyxerr.CodeOf(err) != codes.InvalidArgument {
		t.Fatalf("create: %v", err)
	}
	if _, err := f.create(t, metaParams("shop", "123", "good-token")); err != nil {
		t.Fatal(err)
	}
	if err := f.o.UpdateInstanceConfig(ctx, "shop", engine.UpdateInstanceParams{SendOnly: &yes}); calyxerr.CodeOf(err) != codes.InvalidArgument {
		t.Fatalf("update: %v", err)
	}
}

func TestAPhoneNumberIDBelongsToOneInstanceOnly(t *testing.T) {
	f := newMetaFixture(t)
	if _, err := f.create(t, metaParams("shop", "123", "good-token")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.create(t, metaParams("store", "123", "good-token")); calyxerr.CodeOf(err) != codes.AlreadyExists {
		t.Fatalf("create: %v", err)
	}
	if _, err := f.create(t, metaParams("store", "456", "good-token")); err != nil {
		t.Fatal(err)
	}
	taken := metaParams("", "123", "good-token").ProviderConfig
	if err := f.o.UpdateInstanceConfig(ctx, "store", engine.UpdateInstanceParams{ProviderConfig: &taken}); calyxerr.CodeOf(err) != codes.AlreadyExists {
		t.Fatalf("update: %v", err)
	}
}

func TestMetaSendsAndRunsBulkJobsWithoutLongPauses(t *testing.T) {
	f := newMetaFixture(t)
	if _, err := f.create(t, metaParams("shop", "123", "good-token")); err != nil {
		t.Fatal(err)
	}
	result, err := f.o.SendText(ctx, "shop", "+34 600 111 222", "hi", nil)
	if err != nil || !strings.HasPrefix(result.MessageID, "wamid.") {
		t.Fatalf("got %+v, %v", result, err)
	}

	job, _, err := f.o.CreateBulkJob(ctx, "shop", engine.BulkCustom, bulkOf("34600000001", "34600000002", "34600000003", "34600000004", "34600000005"),
		engine.BulkPacingOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "job completed", func() bool {
		j, _ := f.store.Get(ctx, job.ID)
		return j.Status == engine.JobCompleted && j.SentMessages == 5
	}, 10*time.Second)
}

func TestABulkMessageThatGetsAMeta5xxFailsAndTheJobContinues(t *testing.T) {
	f := newMetaFixture(t)
	if _, err := f.create(t, metaParams("shop", "123", "good-token")); err != nil {
		t.Fatal(err)
	}
	f.graph.NextSendResponse(500, map[string]any{"error": map[string]any{"message": "busy"}})
	job, _, err := f.o.CreateBulkJob(ctx, "shop", engine.BulkCustom, bulkOf("34600000001", "34600000002", "34600000003"),
		engine.BulkPacingOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "job completed", func() bool { j, _ := f.store.Get(ctx, job.ID); return j.Status == engine.JobCompleted }, 10*time.Second)
	if j, _ := f.store.Get(ctx, job.ID); j.SentMessages != 2 || j.FailedMessages != 1 {
		t.Fatalf("sent %d, failed %d", j.SentMessages, j.FailedMessages)
	}
	if n := len(f.graph.Requests()); n != 4 { // the connect check and one POST per message, no resend
		t.Fatalf("%d requests", n)
	}
}

func TestAnExpiredTokenFailsTheRunningJobInsteadOfEveryMessage(t *testing.T) {
	f := newMetaFixture(t)
	if _, err := f.create(t, metaParams("shop", "123", "good-token")); err != nil {
		t.Fatal(err)
	}
	recipients := make([]string, 10)
	for i := range recipients {
		recipients[i] = "3460000000" + string(rune('0'+i))
	}
	job, _, err := f.o.CreateBulkJob(ctx, "shop", engine.BulkCustom, bulkOf(recipients...), engine.BulkPacingOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "first message", func() bool { j, _ := f.store.Get(ctx, job.ID); return j.SentMessages >= 1 }, 5*time.Second)
	f.graph.SetValidToken("other")

	waitFor(t, "job failed", func() bool { j, _ := f.store.Get(ctx, job.ID); return j.Status == engine.JobFailed }, 5*time.Second)
	if j, _ := f.store.Get(ctx, job.ID); j.FailedMessages > 2 {
		t.Fatalf("%d messages failed one by one", j.FailedMessages)
	}
	if status, _ := f.o.GetInstanceStatus(ctx, "shop"); status != needsAuth {
		t.Fatalf("status %v", status)
	}
}

func TestNewCredentialsBringANeedsAuthInstanceBack(t *testing.T) {
	f := newMetaFixture(t)
	if _, err := f.create(t, metaParams("shop", "123", "good-token")); err != nil {
		t.Fatal(err)
	}
	f.graph.SetValidToken("rotated")
	if _, err := f.o.SendText(ctx, "shop", "34600111222", "hi", nil); calyxerr.CodeOf(err) != codes.FailedPrecondition {
		t.Fatalf("send with an expired token: %v", err)
	}
	waitFor(t, "needs auth", func() bool { s, _ := f.o.GetInstanceStatus(ctx, "shop"); return s == needsAuth }, time.Second)

	rotated := metaParams("", "123", "rotated").ProviderConfig
	if err := f.o.UpdateInstanceConfig(ctx, "shop", engine.UpdateInstanceParams{ProviderConfig: &rotated}); err != nil {
		t.Fatal(err)
	}
	if status, _ := f.o.GetInstanceStatus(ctx, "shop"); status != connected {
		t.Fatalf("status %v", status)
	}
}

func TestLiveProvidersFindsAMetaInstanceAndListNeverShowsCredentials(t *testing.T) {
	f := newMetaFixture(t)
	if _, err := f.create(t, metaParams("shop", "123", "good-token")); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range f.o.LiveProviders() {
		if m, ok := p.(*meta.Provider); ok && m.PhoneNumberID() == "123" {
			found = true
		}
	}
	if !found {
		t.Fatal("not found")
	}
	list, _ := f.o.ListInstances(ctx)
	encoded, _ := json.Marshal(list)
	if strings.Contains(string(encoded), "good-token") || strings.Contains(string(encoded), "app-secret") {
		t.Fatalf("credentials exposed: %s", encoded)
	}
	if list[0].Provider != "META_CLOUD" {
		t.Fatalf("got %+v", list[0])
	}
}
