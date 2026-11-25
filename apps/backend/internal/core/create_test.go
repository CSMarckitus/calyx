package core_test

import (
	"context"
	"errors"
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

func collect() (*[]core.Update, func(core.Update)) {
	var us []core.Update
	return &us, func(u core.Update) { us = append(us, u) }
}

func TestCreateWithACodeSendsWhatsAppWebSettings(t *testing.T) {
	fc, c := newClient(t)
	fc.ScriptCreate("i_a",
		testutil.CreateStep{Status: needsAuth, PairingCode: "ABCD1234", Message: "m1"},
		testutil.CreateStep{Status: connected, Message: "m2"})
	us, on := collect()
	err := c.CreateInstance(context.Background(), core.InstanceConfig{
		CoreName: "i_a", Provider: "WHATSAPP_WEB", Pairing: "code", Phone: "34600111222",
		SendOnly: ptr(true), DeviceIdentity: "SAFARI_MAC_OS", Locale: "es-ES",
	}, on)
	if err != nil {
		t.Fatal(err)
	}
	want := []core.Update{
		{Status: "NEEDS_AUTH", PairingValue: "ABCD1234", Message: "m1"},
		{Status: "CONNECTED", Message: "m2", DeviceIdentity: "SAFARI_MAC_OS", Locale: "es-ES"},
	}
	if len(*us) != 2 || (*us)[0] != want[0] || (*us)[1] != want[1] {
		t.Errorf("updates %+v", *us)
	}
	req := fc.CreateRequests()[0]
	if req.GetInstanceName() != "i_a" || !req.GetIsUsedOnlyForSendMessages() ||
		req.GetWhatsappWeb().GetPairingCode().GetPhoneNumber() != "34600111222" {
		t.Errorf("request %v", req)
	}
	if req.GetWhatsappWeb().GetDeviceIdentity() != corev1.DeviceIdentity_DEVICE_IDENTITY_SAFARI_MAC_OS ||
		req.GetWhatsappWeb().GetLocale() != "es-ES" {
		t.Errorf("identity and locale: %v", req.GetWhatsappWeb())
	}
	in, ok := fc.Instance("i_a")
	if !ok || in.Status != connected || in.Provider != whatsappWeb || !in.SendOnly {
		t.Errorf("instance %+v", in)
	}
}

func TestCreateWithAQRSendsPairingQr(t *testing.T) {
	fc, c := newClient(t)
	us, on := collect()
	done := make(chan error, 1)
	go func() {
		done <- c.CreateInstance(context.Background(), core.InstanceConfig{CoreName: "i_a", Provider: "WHATSAPP_WEB", Pairing: "qr", Locale: "es-ES"}, on)
	}()
	fc.WaitForRequests(t, testutil.MethodCreateInstance, 1)
	time.Sleep(50 * time.Millisecond)
	fc.Release("i_a")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !fc.CreateRequests()[0].GetWhatsappWeb().GetPairingQr() {
		t.Error("pairing_qr not set")
	}
	if len(*us) != 2 || (*us)[0].Status != "NEEDS_AUTH" || (*us)[0].PairingValue != "fake-qr-1" || (*us)[1].Status != "CONNECTED" || (*us)[1].PairingValue != "" {
		t.Errorf("updates %+v", *us)
	}
}

func TestCreateMetaSendsTheCredentials(t *testing.T) {
	fc, c := newClient(t)
	us, on := collect()
	err := c.CreateInstance(context.Background(), core.InstanceConfig{
		CoreName: "i_a", Provider: "META_CLOUD",
		Meta: &core.MetaCredentials{PhoneNumberID: "111", AccessToken: "tok", AppSecret: "sec"},
	}, on)
	if err != nil {
		t.Fatal(err)
	}
	req := fc.CreateRequests()[0]
	m := req.GetMetaCloud()
	if m.GetPhoneNumberId() != "111" || m.GetAccessToken() != "tok" || m.GetAppSecret() != "sec" || req.GetWhatsappWeb() != nil {
		t.Errorf("request %v", req)
	}
	if len(*us) != 1 || (*us)[0] != (core.Update{Status: "CONNECTED", Message: "Instance connected successfully."}) {
		t.Errorf("updates %+v", *us)
	}
}

func TestCreateWithSendOnlyNilIsNotSendOnly(t *testing.T) {
	fc, c := newClient(t)
	_, on := collect()
	if err := c.CreateInstance(context.Background(), core.InstanceConfig{
		CoreName: "i_a", Provider: "META_CLOUD",
		Meta: &core.MetaCredentials{PhoneNumberID: "1"},
	}, on); err != nil {
		t.Fatal(err)
	}
	if fc.CreateRequests()[0].GetIsUsedOnlyForSendMessages() {
		t.Error("send-only set")
	}
}

func TestCreateAlwaysSendsProviderSettings(t *testing.T) {
	fc, c := newClient(t)
	_, on := collect()
	_ = c.CreateInstance(context.Background(), core.InstanceConfig{
		CoreName: "i_a", Provider: "META_CLOUD",
		Meta: &core.MetaCredentials{PhoneNumberID: "1"},
	}, on)
	fc.ScriptCreate("i_b", testutil.CreateStep{Status: connected})
	_ = c.CreateInstance(context.Background(), core.InstanceConfig{CoreName: "i_b", Provider: "WHATSAPP_WEB", Pairing: "qr", Locale: "es-ES"}, on)
	for _, r := range fc.CreateRequests() {
		if r.GetProviderSettings() == nil {
			t.Errorf("no provider settings: %v", r)
		}
	}
}

func TestCreateSendsWebhooksWithTheirSecrets(t *testing.T) {
	fc, c := newClient(t)
	_, on := collect()
	err := c.CreateInstance(context.Background(), core.InstanceConfig{
		CoreName: "i_a", Provider: "META_CLOUD",
		Meta: &core.MetaCredentials{PhoneNumberID: "1"},
		Webhooks: []core.Webhook{
			{URL: "https://u1.example", Event: "message.received", Secret: "s1"},
			{URL: "https://u2.example", Event: "connection.update"},
		},
	}, on)
	if err != nil {
		t.Fatal(err)
	}
	ws := fc.CreateRequests()[0].GetWebhooks()
	if len(ws) != 2 || ws[0].GetSecret() != "s1" || ws[1].Secret != nil {
		t.Errorf("webhooks %v", ws)
	}
}

func TestCreateRefusesABadConfigurationWithoutCallingTheCore(t *testing.T) {
	fc, c := newClient(t)
	called := false
	on := func(core.Update) { called = true }
	for _, cfg := range []core.InstanceConfig{
		{CoreName: "i_a", Provider: "X"},
		{CoreName: "i_a", Provider: "WHATSAPP_WEB"},
		{CoreName: "i_a", Provider: "META_CLOUD"},
	} {
		requireCode(t, c.CreateInstance(context.Background(), cfg, on), 400, "invalid_request")
	}
	if n := len(fc.Requests(testutil.MethodCreateInstance)); n != 0 || called {
		t.Errorf("core called %d times, onUpdate %v", n, called)
	}
}

func TestCreateRefusedByTheCoreBeforeAnyUpdate(t *testing.T) {
	fc, c := newClient(t)
	msg := "pairing_code.phone_number must be 7 to 15 digits."
	fc.SetError(testutil.MethodCreateInstance, status.Error(codes.InvalidArgument, msg))
	called := false
	err := c.CreateInstance(context.Background(), core.InstanceConfig{CoreName: "i_a", Provider: "WHATSAPP_WEB", Pairing: "code", Phone: "1", Locale: "es-ES"},
		func(core.Update) { called = true })
	requireCode(t, err, 400, "invalid_request")
	if e, _ := apierr.As(err); e.Detail == msg || called {
		t.Errorf("detail %q, called %v", e.Detail, called)
	}
}

func TestCreateOfANameInUseIsAConflict(t *testing.T) {
	fc, c := newClient(t)
	fc.AddInstance("i_a", whatsappWeb, connected)
	_, on := collect()
	err := c.CreateInstance(context.Background(), core.InstanceConfig{CoreName: "i_a", Provider: "WHATSAPP_WEB", Pairing: "qr", Locale: "es-ES"}, on)
	requireCode(t, err, 409, "conflict")
}

func TestRePairOfAnInstanceInNeedsAuthIsAccepted(t *testing.T) {
	fc, c := newClient(t)
	fc.AddInstance("i_a", whatsappWeb, needsAuth)
	fc.ScriptCreate("i_a", testutil.CreateStep{Status: needsAuth, PairingCode: "c"}, testutil.CreateStep{Status: connected})
	_, on := collect()
	if err := c.CreateInstance(context.Background(), core.InstanceConfig{CoreName: "i_a", Provider: "WHATSAPP_WEB", Pairing: "qr", Locale: "es-ES"}, on); err != nil {
		t.Fatal(err)
	}
	if in, _ := fc.Instance("i_a"); in.Status != connected {
		t.Errorf("status %v", in.Status)
	}
}

func TestAStreamErrorAfterAnUpdate(t *testing.T) {
	fc, c := newClient(t)
	fc.ScriptCreate("i_a", testutil.CreateStep{Status: needsAuth, PairingCode: "c"},
		testutil.CreateStep{Err: status.Error(codes.DeadlineExceeded, "The pairing expired: the code was not used in time.")})
	us, on := collect()
	err := c.CreateInstance(context.Background(), core.InstanceConfig{CoreName: "i_a", Provider: "WHATSAPP_WEB", Pairing: "qr", Locale: "es-ES"}, on)
	requireCode(t, err, 503, "core_unavailable")
	if !errors.Is(err, core.ErrPairingExpired) {
		t.Error("DEADLINE_EXCEEDED does not wrap ErrPairingExpired")
	}
	if e, _ := apierr.As(err); strings.Contains(e.Detail, "expired") || e.Detail == "" {
		t.Errorf("detail %q", e.Detail)
	}
	if len(*us) != 1 {
		t.Errorf("updates %+v", *us)
	}
	if _, ok := fc.Instance("i_a"); ok {
		t.Error("instance stored")
	}
}

func TestAnUnavailableStreamErrorIsNotAnExpiredPairing(t *testing.T) {
	fc, c := newClient(t)
	fc.ScriptCreate("i_a", testutil.CreateStep{Status: needsAuth, PairingCode: "c"},
		testutil.CreateStep{Err: status.Error(codes.Unavailable, "The connection to WhatsApp ended during the pairing.")})
	_, on := collect()
	err := c.CreateInstance(context.Background(), core.InstanceConfig{CoreName: "i_a", Provider: "WHATSAPP_WEB", Pairing: "qr", Locale: "es-ES"}, on)
	requireCode(t, err, 503, "core_unavailable")
	if errors.Is(err, core.ErrPairingExpired) {
		t.Error("UNAVAILABLE wraps ErrPairingExpired")
	}
}

func TestCancellingTheContextEndsThePairing(t *testing.T) {
	fc, c := newClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan struct{}, 1)
	on := func(core.Update) {
		select {
		case first <- struct{}{}:
		default:
		}
	}
	done := make(chan error, 1)
	go func() {
		done <- c.CreateInstance(ctx, core.InstanceConfig{CoreName: "i_a", Provider: "WHATSAPP_WEB", Pairing: "qr", Locale: "es-ES"}, on)
	}()
	<-first
	cancel()
	select {
	case err := <-done:
		requireCode(t, err, 503, "core_unavailable")
	case <-time.After(time.Second):
		t.Fatal("the pairing did not end")
	}
	if _, ok := fc.Instance("i_a"); ok {
		t.Error("a new instance was kept")
	}
}

func TestAStreamThatEndsWithoutConnectedIsInternal(t *testing.T) {
	fc, c := newClient(t)
	fc.ScriptCreate("i_a", testutil.CreateStep{Status: needsAuth, PairingCode: "c"})
	us, on := collect()
	err := c.CreateInstance(context.Background(), core.InstanceConfig{CoreName: "i_a", Provider: "WHATSAPP_WEB", Pairing: "qr", Locale: "es-ES"}, on)
	requireCode(t, err, 500, "internal")
	if len(*us) != 1 {
		t.Errorf("updates %+v", *us)
	}
}

func TestCreateWhenTheCoreIsDown(t *testing.T) {
	fc, c := newClient(t)
	fc.SetUnavailable(true)
	called := false
	err := c.CreateInstance(context.Background(), core.InstanceConfig{CoreName: "i_a", Provider: "WHATSAPP_WEB", Pairing: "qr", Locale: "es-ES"},
		func(core.Update) { called = true })
	requireCode(t, err, 503, "core_unavailable")
	if called {
		t.Error("onUpdate called")
	}
}

func TestCreateAddsNoDeadlineOfItsOwn(t *testing.T) {
	fc, c := newClient(t)
	_, on := collect()
	cfg := core.InstanceConfig{CoreName: "i_a", Provider: "META_CLOUD", Meta: &core.MetaCredentials{PhoneNumberID: "1"}}
	if err := c.CreateInstance(context.Background(), cfg, on); err != nil {
		t.Fatal(err)
	}
	if fc.Requests(testutil.MethodCreateInstance)[0].HasDeadline {
		t.Error("deadline added")
	}
	fc.DropInstance("i_a")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := c.CreateInstance(ctx, cfg, on); err != nil {
		t.Fatal(err)
	}
	requireTimeout(t, fc.Requests(testutil.MethodCreateInstance)[1], 3*time.Minute)
}

func TestTheTokenIsSentOnTheStream(t *testing.T) {
	fc, c := newClient(t)
	_, on := collect()
	_ = c.CreateInstance(context.Background(), core.InstanceConfig{
		CoreName: "i_a", Provider: "META_CLOUD",
		Meta: &core.MetaCredentials{PhoneNumberID: "1"},
	}, on)
	if got := fc.Requests(testutil.MethodCreateInstance)[0].Token; got != "Bearer "+testutil.FakeCoreToken {
		t.Errorf("token %q", got)
	}
}
