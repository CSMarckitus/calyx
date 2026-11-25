package engine_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/engine"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

func TestTheProviderGetsTheStoredIdentityAndLocale(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "device_identity = 'DEVICE_IDENTITY_FIREFOX_LINUX', locale = 'es-ES'")
	f.instance(t, "old", "")
	f.o.LoadAllActiveInstances(ctx)
	for _, name := range []string{"shop", "old"} {
		if _, err := f.o.SendText(ctx, name, "34600000001", "hi", nil); err != nil {
			t.Fatal(err)
		}
	}
	if row := f.builtRow("shop"); row.DeviceIdentity != "DEVICE_IDENTITY_FIREFOX_LINUX" || row.Locale != "es-ES" {
		t.Errorf("shop: got %q %q", row.DeviceIdentity, row.Locale)
	}
	if row := f.builtRow("old"); row.DeviceIdentity != "" || row.Locale != "" {
		t.Errorf("an instance without values: got %q %q", row.DeviceIdentity, row.Locale)
	}
}

func newPairing(name string) engine.CreateInstanceParams {
	return engine.CreateInstanceParams{Name: name, Provider: "WHATSAPP_WEB", Pairing: engine.PairingMethod{QR: true}, Locale: "es-ES"}
}

// pair runs CreateInstance and returns its last update.
func (f *orchestratorFixture) pair(t *testing.T, params engine.CreateInstanceParams) engine.CreateInstanceUpdate {
	t.Helper()
	var last engine.CreateInstanceUpdate
	if err := f.o.CreateInstance(ctx, params, func(u engine.CreateInstanceUpdate) { last = u }); err != nil {
		t.Fatal(err)
	}
	return last
}

func (f *orchestratorFixture) summaryOf(t *testing.T, name string) engine.InstanceSummary {
	t.Helper()
	summaries, err := f.o.ListInstances(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range summaries {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no instance %s", name)
	return engine.InstanceSummary{}
}

// captureLogs sends the default slog output to a buffer for the time of the test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

func TestRequestedIdentityWins(t *testing.T) {
	f := newOrchestratorFixture(t)
	logs := captureLogs(t)
	params := newPairing("shop")
	params.DeviceIdentity = corev1.DeviceIdentity_DEVICE_IDENTITY_SAFARI_MAC_OS
	last := f.pair(t, params)

	if last.Status != connected || last.DeviceIdentity != corev1.DeviceIdentity_DEVICE_IDENTITY_SAFARI_MAC_OS || last.Locale != "es-ES" {
		t.Errorf("CONNECTED update %+v", last)
	}
	if row := f.builtRow("shop"); row.DeviceIdentity != "DEVICE_IDENTITY_SAFARI_MAC_OS" {
		t.Errorf("row %q", row.DeviceIdentity)
	}
	if s := f.summaryOf(t, "shop"); s.DeviceIdentity != corev1.DeviceIdentity_DEVICE_IDENTITY_SAFARI_MAC_OS || s.Locale != "es-ES" {
		t.Errorf("summary %+v", s)
	}
	if len(f.intnCalls) != 0 {
		t.Error("a requested identity must not use the random source")
	}
	if line := logs.String(); !strings.Contains(line, `msg="Device identity chosen."`) ||
		!strings.Contains(line, "device_identity=DEVICE_IDENTITY_SAFARI_MAC_OS") || !strings.Contains(line, "source=requested") ||
		!strings.Contains(line, "instance=shop") {
		t.Errorf("log %q", line)
	}
}

func TestRandomIdentityUsesIntn(t *testing.T) {
	f := newOrchestratorFixture(t)
	logs := captureLogs(t)
	f.intn = func(int) int { return 3 }
	last := f.pair(t, newPairing("shop"))

	if last.DeviceIdentity != corev1.DeviceIdentity_DEVICE_IDENTITY_FIREFOX_LINUX {
		t.Errorf("got %v", last.DeviceIdentity)
	}
	if len(f.intnCalls) != 1 || f.intnCalls[0] != 5 {
		t.Errorf("Intn calls %v, want one call with 5", f.intnCalls)
	}
	if !strings.Contains(logs.String(), "source=random") {
		t.Errorf("log %q", logs.String())
	}
}

func TestRePairPicksAgain(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "connection_status = 'NEEDS_AUTH', device_identity = 'DEVICE_IDENTITY_FIREFOX_LINUX', locale = 'es-ES'")
	last := f.pair(t, rePair("shop"))
	if last.DeviceIdentity != corev1.DeviceIdentity_DEVICE_IDENTITY_CHROME_WINDOWS { // Intn gives 0
		t.Errorf("got %v", last.DeviceIdentity)
	}
	if len(f.intnCalls) != 1 {
		t.Errorf("Intn calls %v", f.intnCalls)
	}
}

func TestUnknownIdentityIsRefused(t *testing.T) {
	f := newOrchestratorFixture(t)
	params := newPairing("shop")
	params.DeviceIdentity = corev1.DeviceIdentity(42)
	err := f.o.CreateInstance(ctx, params, func(engine.CreateInstanceUpdate) {})
	if calyxerr.CodeOf(err) != codes.InvalidArgument {
		t.Fatalf("got %v", err)
	}
	if _, err := f.o.GetInstanceStatus(ctx, "shop"); calyxerr.CodeOf(err) != codes.NotFound {
		t.Error("a refused request stored the instance")
	}
}

func TestRequestedLocaleWins(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "connection_status = 'NEEDS_AUTH', locale = 'es-ES'")
	params := rePair("shop")
	params.Locale = "pt-BR"
	if last := f.pair(t, params); last.Locale != "pt-BR" {
		t.Errorf("CONNECTED update %q", last.Locale)
	}
	if row := f.builtRow("shop"); row.Locale != "pt-BR" {
		t.Errorf("row %q", row.Locale)
	}
}

func TestRePairUsesTheStoredLocale(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.instance(t, "shop", "connection_status = 'NEEDS_AUTH', locale = 'es-ES'")
	params := rePair("shop")
	params.Locale = ""
	if last := f.pair(t, params); last.Locale != "es-ES" {
		t.Errorf("CONNECTED update %q", last.Locale)
	}
	if s := f.summaryOf(t, "shop"); s.Locale != "es-ES" {
		t.Errorf("summary %q", s.Locale)
	}
}

func TestNewPairingWithoutLocaleIsRefused(t *testing.T) {
	cases := map[string]func(f *orchestratorFixture){
		"new instance":                          func(*orchestratorFixture) {},
		"re-pair of an instance with no locale": func(f *orchestratorFixture) { f.instance(t, "shop", "connection_status = 'NEEDS_AUTH'") },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			f := newOrchestratorFixture(t)
			setup(f)
			params := newPairing("shop")
			params.Locale = ""
			err := f.o.CreateInstance(ctx, params, func(engine.CreateInstanceUpdate) {})
			if calyxerr.CodeOf(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "locale is required for a new pairing, for example es-ES") {
				t.Fatalf("got %v", err)
			}
			if f.buildCount("shop") != 0 {
				t.Error("a refused request started a pairing")
			}
		})
	}
}

func TestBadLocaleIsRefused(t *testing.T) {
	f := newOrchestratorFixture(t)
	params := newPairing("shop")
	params.Locale = "es_ES"
	err := f.o.CreateInstance(ctx, params, func(engine.CreateInstanceUpdate) {})
	if calyxerr.CodeOf(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "must be a locale like es-ES") {
		t.Fatalf("got %v", err)
	}
	if _, err := f.o.GetInstanceStatus(ctx, "shop"); calyxerr.CodeOf(err) != codes.NotFound {
		t.Error("a refused request stored the instance")
	}
}

func TestMetaHasNoIdentityOrLocale(t *testing.T) {
	f := newMetaFixture(t)
	updates, err := f.create(t, metaParams("shop", "123", "good-token"))
	if err != nil {
		t.Fatal(err)
	}
	last := updates[len(updates)-1]
	if last.DeviceIdentity != corev1.DeviceIdentity_DEVICE_IDENTITY_UNSPECIFIED || last.Locale != "" {
		t.Errorf("CONNECTED update %+v", last)
	}
	summaries, err := f.o.ListInstances(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summaries[0].DeviceIdentity != corev1.DeviceIdentity_DEVICE_IDENTITY_UNSPECIFIED || summaries[0].Locale != "" {
		t.Errorf("summary %+v", summaries[0])
	}

	withLocale := metaParams("other", "456", "good-token")
	withLocale.Locale = "es-ES"
	if _, err := f.create(t, withLocale); calyxerr.CodeOf(err) != codes.InvalidArgument {
		t.Errorf("a Meta request with a locale: %v", err)
	}
}
