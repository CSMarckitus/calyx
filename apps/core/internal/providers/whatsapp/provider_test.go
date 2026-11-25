package whatsapp_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/database"
	"calyx/core/internal/engine"
	"calyx/core/internal/providers/whatsapp"
	"calyx/core/internal/testutil"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

func TestBackoffDoublesFrom5SecondsUpTo5Minutes(t *testing.T) {
	want := []time.Duration{
		5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second,
		80 * time.Second, 160 * time.Second, 5 * time.Minute, 5 * time.Minute,
	}
	for attempt, w := range want {
		if got := whatsapp.Backoff(attempt); got != w {
			t.Errorf("attempt %d: got %v, want %v", attempt, got, w)
		}
	}
	if got := whatsapp.Backoff(1000); got != 5*time.Minute {
		t.Errorf("attempt 1000: got %v", got)
	}
}

func TestConnectWithoutAPairedDeviceNeedsAuth(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)
	container, err := database.NewWhatsmeowStore(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	id := testutil.CreateInstance(t, db, "shop")
	var reported []corev1.InstanceStatus
	hooks := engine.ProviderHooks{OnStatus: func(s corev1.InstanceStatus) { reported = append(reported, s) }}

	for _, deviceJID := range []string{"", "34600111222:7@s.whatsapp.net"} { // never paired; paired but its keys are gone
		p := whatsapp.New(engine.InstanceRow{ID: id, Name: "shop", DeviceJID: deviceJID}, hooks, db, container)
		err := p.Connect(ctx)
		if calyxerr.CodeOf(err) != codes.FailedPrecondition {
			t.Fatalf("device %q: got %v", deviceJID, err)
		}
		if p.Status() != corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH {
			t.Fatalf("device %q: status %v", deviceJID, p.Status())
		}
	}
	if len(reported) == 0 || reported[0] != corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH {
		t.Fatalf("reported %v", reported)
	}
}

func TestTheProviderUsesHumanPacingAndRejectsBadRecipients(t *testing.T) {
	p := whatsapp.New(engine.InstanceRow{Name: "shop"}, engine.ProviderHooks{}, nil, nil)
	if p.Pacing().Delay.Min != engine.HumanPacing.Delay.Min || !p.Pacing().SimulateTyping {
		t.Fatalf("pacing %+v", p.Pacing())
	}
	if _, err := p.NormalizeRecipient("hello"); calyxerr.CodeOf(err) != codes.InvalidArgument {
		t.Fatalf("got %v", err)
	}
	if _, err := p.SendText(context.Background(), "34600111222@s.whatsapp.net", "hi"); calyxerr.CodeOf(err) != codes.Unavailable {
		t.Fatalf("sending without a connection: %v", err)
	}
}

// A removal logs out: the phone numbers of the contacts must not stay in the database when no
// device is left (whatsmeow's LID map is shared and has no owner).
func TestLogoutForgetsTheLIDMapWhenNoDeviceIsLeft(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)
	container, err := database.NewWhatsmeowStore(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO whatsmeow_lid_map (lid, pn) VALUES ('111', '34600999888')"); err != nil {
		t.Fatal(err)
	}
	id := testutil.CreateInstance(t, db, "shop")
	p := whatsapp.New(engine.InstanceRow{ID: id, Name: "shop"}, engine.ProviderHooks{}, db, container)
	if err := p.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM whatsmeow_lid_map").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d LID mappings left after the logout", n)
	}
}

// pairedProvider returns a provider whose device is stored, as after a pairing.
func pairedProvider(t *testing.T) *whatsapp.Provider {
	t.Helper()
	return pairedProviderWithHooks(t, engine.ProviderHooks{})
}

func pairedProviderWithHooks(t *testing.T, hooks engine.ProviderHooks) *whatsapp.Provider {
	t.Helper()
	ctx := context.Background()
	db := testutil.NewDB(t)
	container, err := database.NewWhatsmeowStore(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	jid := types.NewADJID("34600111222", 0, 7)
	device := container.NewDevice()
	device.ID = &jid
	// Empty values of the lengths that the store checks: the dial never gets as far as using them.
	device.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte{},
		AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64)}
	if err := container.PutDevice(ctx, device); err != nil {
		t.Fatal(err)
	}
	id := testutil.CreateInstance(t, db, "shop")
	return whatsapp.New(engine.InstanceRow{ID: id, Name: "shop", DeviceJID: jid.String()}, hooks, db, container)
}

// hangingClient returns an HTTP client whose dial waits until its context ends, with no network.
// dialing is closed when the first dial starts.
func hangingClient() (hanging *http.Client, dialing chan struct{}) {
	dialing = make(chan struct{})
	var once sync.Once
	hanging = &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			once.Do(func() { close(dialing) })
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}}
	return hanging, dialing
}

// hangTheDial makes the real dial of whatsmeow wait until its context ends, with no network.
// dialing is closed when the dial starts.
func hangTheDial(p *whatsapp.Provider) (dialing chan struct{}) {
	hanging, dialing := hangingClient()
	whatsapp.SetConnectClient(p, func(ctx context.Context, client *whatsmeow.Client) error {
		client.SetWebsocketHTTPClient(hanging)
		return client.ConnectContext(ctx)
	})
	return dialing
}

func returnsWithin(t *testing.T, what string, fn func(), timeout time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("%s did not return within %v", what, timeout)
	}
}

func TestADialThatHangsEndsWithTheContextOfConnect(t *testing.T) {
	p := pairedProvider(t)
	hangTheDial(p)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	var err error
	returnsWithin(t, "Connect", func() { err = p.Connect(ctx) }, 5*time.Second)
	if calyxerr.CodeOf(err) != codes.Unavailable {
		t.Fatalf("got %v", err)
	}
	_ = p.Destroy(context.Background())
}

func TestDestroyEndsADialThatHangs(t *testing.T) {
	p := pairedProvider(t)
	dialing := hangTheDial(p)
	connected := make(chan error, 1)
	go func() { connected <- p.Connect(context.Background()) }()
	<-dialing

	returnsWithin(t, "Destroy", func() { _ = p.Destroy(context.Background()) }, 5*time.Second)
	if err := <-connected; err == nil {
		t.Fatal("Connect worked after Destroy")
	}
}

func TestAnAcceptedSessionSurvivesTheEndOfTheContextOfConnect(t *testing.T) {
	p := pairedProvider(t)
	var socket context.Context
	whatsapp.SetConnectClient(p, func(ctx context.Context, client *whatsmeow.Client) error {
		socket = ctx
		whatsapp.Accept(p)
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())

	if err := p.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	time.Sleep(100 * time.Millisecond) // a forgotten AfterFunc runs in its own goroutine
	if socket.Err() != nil {
		t.Fatal("the end of the context of Connect closed an accepted session")
	}
	_ = p.Destroy(context.Background())
	if socket.Err() == nil {
		t.Fatal("Destroy did not close the session")
	}
}

func TestDestroyOfAnAcceptedSessionDisconnectsBeforeItEndsTheSession(t *testing.T) {
	p := pairedProvider(t)
	var socket context.Context
	whatsapp.SetConnectClient(p, func(ctx context.Context, client *whatsmeow.Client) error {
		socket = ctx
		whatsapp.Accept(p)
		return nil
	})
	aliveAtDisconnect := false
	whatsapp.SetDisconnectClient(p, func(*whatsmeow.Client) { aliveAtDisconnect = socket.Err() == nil })
	if err := p.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}

	_ = p.Destroy(context.Background())
	if !aliveAtDisconnect {
		t.Fatal("Destroy ended the session before the disconnect: the socket closes with no close frame")
	}
	if socket.Err() == nil {
		t.Fatal("Destroy did not end the session")
	}
}

// A long-lived instance whose first connect failed connects later through its own retry.
func TestASessionAcceptedAfterAFailedConnectIsUnlinkedAndClosesInOrder(t *testing.T) {
	p := pairedProvider(t)
	t.Cleanup(func() { _ = p.Destroy(context.Background()) }) // stops the retry before it runs
	var socket context.Context
	whatsapp.SetConnectClient(p, func(ctx context.Context, client *whatsmeow.Client) error {
		socket = ctx
		return errors.New("no network")
	})
	aliveAtDisconnect := false
	whatsapp.SetDisconnectClient(p, func(*whatsmeow.Client) { aliveAtDisconnect = socket.Err() == nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := p.Connect(ctx); calyxerr.CodeOf(err) != codes.Unavailable {
		t.Fatalf("got %v", err)
	}

	whatsapp.Accept(p) // what the retry gives when WhatsApp accepts the session
	cancel()
	time.Sleep(100 * time.Millisecond) // a link that is still armed runs in its own goroutine
	if socket.Err() != nil {
		t.Fatal("the end of the context of the failed Connect closed the accepted session")
	}
	_ = p.Destroy(context.Background())
	if !aliveAtDisconnect {
		t.Fatal("Destroy ended the accepted session before the disconnect")
	}
}
