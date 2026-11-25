package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/core"
	"calyx/backend/internal/testutil"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

const (
	connected    = corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED
	needsAuth    = corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH
	disconnected = corev1.InstanceStatus_INSTANCE_STATUS_DISCONNECTED
	whatsappWeb  = corev1.Provider_PROVIDER_WHATSAPP_WEB
	metaCloud    = corev1.Provider_PROVIDER_META_CLOUD
)

func newClient(t *testing.T) (*testutil.FakeCore, *core.Client) {
	t.Helper()
	fc, conn := testutil.NewFakeCore(t)
	return fc, core.NewClient(conn)
}

func requireCode(t *testing.T, err error, st int, code string) {
	t.Helper()
	e, ok := apierr.As(err)
	if !ok {
		t.Fatalf("want %d %s, got %v", st, code, err)
	}
	if e.Status != st || e.Code != code {
		t.Fatalf("want %d %s, got %d %s (%s)", st, code, e.Status, e.Code, e.Detail)
	}
}

func requireTimeout(t *testing.T, r testutil.Request, want time.Duration) {
	t.Helper()
	if !r.HasDeadline {
		t.Fatal("the request has no deadline")
	}
	if r.Timeout > want || r.Timeout <= want-time.Second {
		t.Fatalf("timeout %v, want just under %v", r.Timeout, want)
	}
}

func ptr[T any](v T) *T { return &v }

func TestTheTokenIsSentOnUnaryCalls(t *testing.T) {
	fc, c := newClient(t)
	if _, err := c.ListInstances(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fc.Requests(testutil.MethodListInstances)[0].Token; got != "Bearer "+testutil.FakeCoreToken {
		t.Errorf("token %q", got)
	}
}

func TestListInstances(t *testing.T) {
	fc, c := newClient(t)
	fc.PutInstance(testutil.FakeInstance{Name: "i_b", Provider: metaCloud, Status: connected, Enabled: true})
	fc.PutInstance(testutil.FakeInstance{Name: "i_a", Provider: whatsappWeb, Status: needsAuth, SendOnly: true,
		DeviceIdentity: corev1.DeviceIdentity_DEVICE_IDENTITY_EDGE_WINDOWS, Locale: "pt-BR"})
	got, err := c.ListInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []core.InstanceInfo{
		{CoreName: "i_a", Provider: "WHATSAPP_WEB", Status: "NEEDS_AUTH", Enabled: false, SendOnly: true,
			DeviceIdentity: "EDGE_WINDOWS", Locale: "pt-BR"},
		{CoreName: "i_b", Provider: "META_CLOUD", Status: "CONNECTED", Enabled: true, SendOnly: false},
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %+v", got)
	}
}

func TestListInstancesEmpty(t *testing.T) {
	_, c := newClient(t)
	got, err := c.ListInstances(context.Background())
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestListInstancesDeadlineIs10s(t *testing.T) {
	fc, c := newClient(t)
	if _, err := c.ListInstances(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireTimeout(t, fc.Requests(testutil.MethodListInstances)[0], 10*time.Second)
}

func TestReadsAreCoreUnavailableWhenTheCoreIsDown(t *testing.T) {
	fc, c := newClient(t)
	fc.SetUnavailable(true)
	_, err := c.ListInstances(context.Background())
	requireCode(t, err, 503, "core_unavailable")
	_, err = c.GetInstanceStatus(context.Background(), "i_a")
	requireCode(t, err, 503, "core_unavailable")
}

func TestAReadThatRunsOutOfTimeIsCoreUnavailable(t *testing.T) {
	fc, c := newClient(t)
	fc.SetDelay(testutil.MethodListInstances, 2*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.ListInstances(ctx)
	requireCode(t, err, 503, "core_unavailable")
	if time.Since(start) > time.Second {
		t.Error("too slow")
	}
}

func TestGetInstanceStatus(t *testing.T) {
	fc, c := newClient(t)
	fc.AddInstance("i_a", whatsappWeb, disconnected)
	got, err := c.GetInstanceStatus(context.Background(), "i_a")
	if err != nil || got != "DISCONNECTED" {
		t.Errorf("got %q, %v", got, err)
	}
	requireTimeout(t, fc.Requests(testutil.MethodGetInstanceStatus)[0], 10*time.Second)
}

func TestGetInstanceStatusOfAnUnknownInstanceIsNotFound(t *testing.T) {
	_, c := newClient(t)
	_, err := c.GetInstanceStatus(context.Background(), "i_x")
	requireCode(t, err, 404, "not_found")
}

func TestTheCoreRejectingTheTokenIs502(t *testing.T) {
	fc, c := newClient(t)
	fc.SetError(testutil.MethodListInstances, status.Error(codes.Unauthenticated, "x"))
	_, err := c.ListInstances(context.Background())
	requireCode(t, err, 502, "core_rejected_token")
}

func TestUpdateInstanceConfigSendsTheFullConfiguration(t *testing.T) {
	fc, c := newClient(t)
	fc.AddInstance("i_a", whatsappWeb, connected)
	err := c.UpdateInstanceConfig(context.Background(), core.InstanceConfig{
		CoreName: "i_a", Provider: "WHATSAPP_WEB", SendOnly: ptr(false), Enabled: ptr(false),
		Webhooks: []core.Webhook{
			{URL: "https://a.example/x", Event: "message.received", Secret: "s1"},
			{URL: "https://a.example/y", Event: "connection.update"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := fc.UpdateRequests()[0]
	if req.GetInstanceName() != "i_a" || req.NewInstanceName != nil {
		t.Errorf("names: %v", req)
	}
	if req.IsEnabled == nil || *req.IsEnabled || req.IsUsedOnlyForSendMessages == nil || *req.IsUsedOnlyForSendMessages {
		t.Errorf("flags: %v", req)
	}
	ws := req.GetWebhooks().GetWebhooks()
	if len(ws) != 2 || ws[0].GetSecret() != "s1" || ws[1].Secret != nil {
		t.Errorf("webhooks: %v", ws)
	}
	if req.MetaCloud != nil {
		t.Error("meta_cloud set")
	}

	in, _ := fc.Instance("i_a")
	if in.Enabled || len(in.Webhooks) != 2 {
		t.Errorf("fake state: %+v", in)
	}
}

func TestUpdateWithEnabledNilLeavesItOut(t *testing.T) {
	fc, c := newClient(t)
	fc.AddInstance("i_a", whatsappWeb, connected)
	if err := c.UpdateInstanceConfig(context.Background(), core.InstanceConfig{CoreName: "i_a"}); err != nil {
		t.Fatal(err)
	}
	req := fc.UpdateRequests()[0]
	if req.IsEnabled != nil || req.Webhooks == nil || len(req.Webhooks.Webhooks) != 0 {
		t.Errorf("request: %v", req)
	}
}

func TestUpdateWithoutSendOnlyLeavesTheFieldUnset(t *testing.T) {
	fc, c := newClient(t)
	fc.PutInstance(testutil.FakeInstance{Name: "i_a", Provider: whatsappWeb, Status: connected, Enabled: true, SendOnly: true})
	if err := c.UpdateInstanceConfig(context.Background(), core.InstanceConfig{CoreName: "i_a"}); err != nil {
		t.Fatal(err)
	}
	if fc.UpdateRequests()[0].IsUsedOnlyForSendMessages != nil {
		t.Error("field set")
	}
	if in, _ := fc.Instance("i_a"); !in.SendOnly {
		t.Error("send-only changed")
	}
	if err := c.UpdateInstanceConfig(context.Background(), core.InstanceConfig{CoreName: "i_a", SendOnly: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	if r := fc.UpdateRequests()[1]; r.IsUsedOnlyForSendMessages == nil || !*r.IsUsedOnlyForSendMessages {
		t.Error("field not true")
	}
}

func TestUpdateSendsMetaCredentialsOnlyWhenGiven(t *testing.T) {
	fc, c := newClient(t)
	fc.AddInstance("i_a", metaCloud, connected)
	err := c.UpdateInstanceConfig(context.Background(), core.InstanceConfig{
		CoreName: "i_a", Meta: &core.MetaCredentials{PhoneNumberID: "111", AccessToken: "token", AppSecret: "secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := fc.UpdateRequests()[0].GetMetaCloud()
	if m.GetPhoneNumberId() != "111" || m.GetAccessToken() != "token" || m.GetAppSecret() != "secret" {
		t.Errorf("meta: %v", m)
	}
}

func TestUpdateDeadlineIs30s(t *testing.T) {
	fc, c := newClient(t)
	fc.AddInstance("i_a", whatsappWeb, connected)
	if err := c.UpdateInstanceConfig(context.Background(), core.InstanceConfig{CoreName: "i_a"}); err != nil {
		t.Fatal(err)
	}
	requireTimeout(t, fc.Requests(testutil.MethodUpdateInstanceConfig)[0], 30*time.Second)
}

func TestAnUpdateWithoutAnswerIsChangeOutcomeUnknown(t *testing.T) {
	fc, c := newClient(t)
	fc.AddInstance("i_a", whatsappWeb, connected)
	fc.SetUnavailable(true)
	err := c.UpdateInstanceConfig(context.Background(), core.InstanceConfig{CoreName: "i_a"})
	requireCode(t, err, 504, "change_outcome_unknown")
	fc.SetUnavailable(false)
	fc.SetDelay(testutil.MethodUpdateInstanceConfig, 2*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err = c.UpdateInstanceConfig(ctx, core.InstanceConfig{CoreName: "i_a"})
	requireCode(t, err, 504, "change_outcome_unknown")
}

func TestUpdateRefusalsAreMapped(t *testing.T) {
	fc, c := newClient(t)
	cfg := core.InstanceConfig{CoreName: "i_a"}
	fc.SetError(testutil.MethodUpdateInstanceConfig, status.Error(codes.InvalidArgument, "Event 'x' is not supported."))
	err := c.UpdateInstanceConfig(context.Background(), cfg)
	requireCode(t, err, 400, "invalid_request")
	if e, _ := apierr.As(err); strings.Contains(e.Detail, "'x'") {
		t.Errorf("detail %q", e.Detail)
	}
	for _, tc := range []struct {
		code codes.Code
		st   int
		name string
	}{
		{codes.NotFound, 404, "not_found"},
		{codes.FailedPrecondition, 409, "conflict"},
		{codes.Internal, 500, "internal"},
	} {
		fc.SetError(testutil.MethodUpdateInstanceConfig, status.Error(tc.code, "being paired"))
		requireCode(t, c.UpdateInstanceConfig(context.Background(), cfg), tc.st, tc.name)
	}
}

func TestRemoveInstance(t *testing.T) {
	fc, c := newClient(t)
	fc.AddInstance("i_a", whatsappWeb, connected)
	if err := c.RemoveInstance(context.Background(), "i_a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := fc.Instance("i_a"); ok {
		t.Error("instance still there")
	}
	requireTimeout(t, fc.Requests(testutil.MethodRemoveInstance)[0], 60*time.Second)
}

func TestRemovingAnUnknownInstanceCountsAsDone(t *testing.T) {
	_, c := newClient(t)
	if err := c.RemoveInstance(context.Background(), "i_x"); err != nil {
		t.Error(err)
	}
}

func TestARemoveWithoutAnswerIsCoreUnavailable(t *testing.T) {
	fc, c := newClient(t)
	fc.SetUnavailable(true)
	requireCode(t, c.RemoveInstance(context.Background(), "i_a"), 503, "core_unavailable")
}

func TestRemovingAnInstanceBeingPairedIsAConflict(t *testing.T) {
	fc, c := newClient(t)
	fc.SetError(testutil.MethodRemoveInstance, status.Error(codes.FailedPrecondition, "Instance i_a is being paired."))
	requireCode(t, c.RemoveInstance(context.Background(), "i_a"), 409, "conflict")
}
