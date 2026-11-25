package whatsapp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	"go.mau.fi/whatsmeow/proto/waWa6"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/database"
	"calyx/core/internal/engine"
	"calyx/core/internal/locale"
	"calyx/core/internal/testutil"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// These tests fail when a whatsmeow update changes the parts of the payload that the core
// relies on.

var (
	firefoxLinux = deviceEntry{Text: "Firefox (Linux)", PairType: whatsmeow.PairClientFirefox,
		Platform: waCompanionReg.DeviceProps_FIREFOX, OS: "Linux"}
	safariMac = deviceEntry{Text: "Safari (Mac OS)", PairType: whatsmeow.PairClientSafari,
		Platform: waCompanionReg.DeviceProps_SAFARI, OS: "Mac OS"}
	spain = locale.Locale{Language: "es", Country: "ES"}
)

func newDevice(t *testing.T) *store.Device {
	t.Helper()
	container, err := database.NewWhatsmeowStore(context.Background(), testutil.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	return container.NewDevice()
}

func decodeDeviceProps(t *testing.T, payload *waWa6.ClientPayload) *waCompanionReg.DeviceProps {
	t.Helper()
	props := &waCompanionReg.DeviceProps{}
	if err := proto.Unmarshal(payload.GetDevicePairingData().GetDeviceProps(), props); err != nil {
		t.Fatalf("decode DeviceProps: %v", err)
	}
	return props
}

func checkLocale(t *testing.T, payload *waWa6.ClientPayload, want locale.Locale) {
	t.Helper()
	agent := payload.GetUserAgent()
	if agent.GetLocaleLanguageIso6391() != want.Language || agent.GetLocaleCountryIso31661Alpha2() != want.Country {
		t.Errorf("locale %s-%s, want %s", agent.GetLocaleLanguageIso6391(), agent.GetLocaleCountryIso31661Alpha2(), want)
	}
}

// withoutOurChanges undoes the changes of the hook, so the rest can be compared with whatsmeow's payload.
func withoutOurChanges(t *testing.T, payload, original *waWa6.ClientPayload) *waWa6.ClientPayload {
	t.Helper()
	copied := proto.Clone(payload).(*waWa6.ClientPayload)
	copied.UserAgent.LocaleLanguageIso6391 = original.GetUserAgent().LocaleLanguageIso6391
	copied.UserAgent.LocaleCountryIso31661Alpha2 = original.GetUserAgent().LocaleCountryIso31661Alpha2
	if copied.DevicePairingData != nil {
		copied.DevicePairingData.DeviceProps = original.GetDevicePairingData().GetDeviceProps()
	}
	return copied
}

func TestTheRegistrationPayloadChangesOnlyTheLabelAndTheLocale(t *testing.T) {
	device := newDevice(t)
	original := device.GetClientPayload()
	if original.GetDevicePairingData() == nil {
		t.Fatal("a new device must give a registration payload")
	}
	originalProps := decodeDeviceProps(t, original) // whatsmeow's own bytes decode

	got := clientPayload(device, firefoxLinux, spain, slog.Default())

	props := decodeDeviceProps(t, got)
	if props.GetOs() != "Linux" || props.GetPlatformType() != waCompanionReg.DeviceProps_FIREFOX {
		t.Errorf("DeviceProps Os %q, PlatformType %v", props.GetOs(), props.GetPlatformType())
	}
	props.Os, props.PlatformType = originalProps.Os, originalProps.PlatformType
	if !proto.Equal(props, originalProps) {
		t.Errorf("other fields of DeviceProps changed:\n got %v\nwant %v", props, originalProps)
	}
	checkLocale(t, got, spain)
	if rest := withoutOurChanges(t, got, original); !proto.Equal(rest, original) {
		t.Errorf("other fields of the payload changed")
	}
}

func TestTheLoginPayloadChangesOnlyTheLocale(t *testing.T) {
	device := newDevice(t)
	device.ID = &types.JID{User: "34600111222", Device: 7, Server: types.DefaultUserServer}
	original := device.GetClientPayload()

	got := clientPayload(device, firefoxLinux, spain, slog.Default())

	if got.GetDevicePairingData() != nil {
		t.Fatal("a login payload has no DevicePairingData")
	}
	checkLocale(t, got, spain)
	if rest := withoutOurChanges(t, got, original); !proto.Equal(rest, original) {
		t.Errorf("other fields of the payload changed:\n got %v\nwant %v", rest, original)
	}
}

func TestTwoInstancesBuildDifferentPayloadsAtTheSameTime(t *testing.T) {
	brazil := locale.Locale{Language: "pt", Country: "BR"}
	cases := []struct {
		entry deviceEntry
		loc   locale.Locale
	}{{firefoxLinux, spain}, {safariMac, brazil}}

	var wg sync.WaitGroup
	for _, c := range cases {
		device := newDevice(t)
		wg.Go(func() {
			for range 50 {
				got := clientPayload(device, c.entry, c.loc, slog.Default())
				props := decodeDeviceProps(t, got)
				if props.GetOs() != c.entry.OS || props.GetPlatformType() != c.entry.Platform {
					t.Errorf("%s: got %q %v", c.entry.Text, props.GetOs(), props.GetPlatformType())
					return
				}
				checkLocale(t, got, c.loc)
			}
		})
	}
	wg.Wait()
	if store.DeviceProps.GetOs() != "whatsmeow" {
		t.Errorf("the package variable of whatsmeow changed: %q", store.DeviceProps.GetOs())
	}
}

func newProvider(t *testing.T, identity, loc string) *Provider {
	t.Helper()
	db := testutil.NewDB(t)
	container, err := database.NewWhatsmeowStore(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	row := engine.InstanceRow{ID: testutil.CreateInstance(t, db, "shop"), Name: "shop", DeviceIdentity: identity, Locale: loc}
	return New(row, engine.ProviderHooks{}, db, container)
}

func TestEveryEntryOfTheListHasARowAndEveryRowAnEntry(t *testing.T) {
	for number, name := range corev1.DeviceIdentity_name {
		_, ok := deviceEntries[corev1.DeviceIdentity(number)]
		if number == 0 && ok {
			t.Errorf("%s must have no row", name)
		}
		if number != 0 && !ok {
			t.Errorf("%s has no row", name)
		}
	}
	for identity, entry := range deviceEntries {
		if _, ok := corev1.DeviceIdentity_name[int32(identity)]; !ok {
			t.Errorf("the row %q has no enum value", entry.Text)
		}
		if entry.Text == "" || entry.PairType == "" || entry.OS == "" || entry.Platform == waCompanionReg.DeviceProps_UNKNOWN {
			t.Errorf("incomplete row %+v", entry)
		}
	}
}

func TestEachEntrySetsTheQRClientTypeAndThePayloadHook(t *testing.T) {
	for identity, entry := range deviceEntries {
		p := newProvider(t, identity.String(), "es-ES")
		client, err := p.newClient(context.Background(), types.EmptyJID)
		if err != nil {
			t.Fatal(err)
		}
		if client.QRClientType == "" || client.QRClientType != entry.PairType {
			t.Errorf("%s: QRClientType %q", identity, client.QRClientType)
		}
		if client.GetClientPayload == nil {
			t.Fatalf("%s: no payload hook", identity)
		}
		props := decodeDeviceProps(t, client.GetClientPayload())
		if props.GetOs() != entry.OS {
			t.Errorf("%s: Os %q", identity, props.GetOs())
		}
	}
}

func TestAnInstanceWithoutIdentityKeepsThePayloadOfWhatsmeow(t *testing.T) {
	p := newProvider(t, "", "")
	client, err := p.newClient(context.Background(), types.EmptyJID)
	if err != nil {
		t.Fatal(err)
	}
	if client.QRClientType != "" || client.GetClientPayload != nil {
		t.Errorf("QRClientType %q, hook set: %v", client.QRClientType, client.GetClientPayload != nil)
	}
}

func TestThePairingCodeUsesTheEntry(t *testing.T) {
	p := newProvider(t, "DEVICE_IDENTITY_FIREFOX_LINUX", "es-ES")
	var gotType whatsmeow.PairClientType
	var gotText string
	p.pairPhone = func(ctx context.Context, phone string, clientType whatsmeow.PairClientType, text string) (string, error) {
		gotType, gotText = clientType, text
		return "ABCD-EFGH", nil
	}
	code, err := p.requestPairingCode(context.Background(), "34600111222")
	if err != nil || code != "ABCD-EFGH" {
		t.Fatalf("got %q %v", code, err)
	}
	if gotType != whatsmeow.PairClientFirefox || gotText != "Firefox (Linux)" {
		t.Errorf("PairPhone got %q %q", gotType, gotText)
	}

	p.pairPhone = func(context.Context, string, whatsmeow.PairClientType, string) (string, error) {
		// A new error, as WhatsApp's answer gives: errors.Is compares the code and the text.
		return "", fmt.Errorf("companion_hello: %w", &whatsmeow.IQError{Code: 400, Text: "bad-request"})
	}
	_, err = p.requestPairingCode(context.Background(), "34600111222")
	if calyxerr.CodeOf(err) != codes.FailedPrecondition ||
		!strings.Contains(err.Error(), "WhatsApp refused the request for a pairing code. Check the phone number. "+
			"If it is correct, choose another device identity than Firefox (Linux).") {
		t.Errorf("got %v", err)
	}
}
