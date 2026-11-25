package whatsapp_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/engine"
	"calyx/core/internal/providers/whatsapp"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// statusLog records what a provider reports. hold, when set, runs inside the report.
type statusLog struct {
	mu       sync.Mutex
	reported []corev1.InstanceStatus
	hold     func(corev1.InstanceStatus)
}

func (l *statusLog) hooks() engine.ProviderHooks {
	return engine.ProviderHooks{OnStatus: func(s corev1.InstanceStatus) {
		l.mu.Lock()
		hold := l.hold
		l.mu.Unlock()
		if hold != nil {
			hold(s)
		}
		l.mu.Lock()
		l.reported = append(l.reported, s)
		l.mu.Unlock()
	}}
}

func (l *statusLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.reported)
}

func (l *statusLog) since(n int) []corev1.InstanceStatus {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]corev1.InstanceStatus(nil), l.reported[n:]...)
}

// failedProvider returns a provider whose first Connect failed, as for a long-lived instance with
// no network. Its later dials wait until p.life ends; dialing is closed when the first one starts.
func failedProvider(t *testing.T, log *statusLog) (p *whatsapp.Provider, dialing chan struct{}) {
	t.Helper()
	p = pairedProviderWithHooks(t, log.hooks())
	hanging, dialing := hangingClient()
	whatsapp.SetConnectClient(p, func(ctx context.Context, client *whatsmeow.Client) error {
		client.SetWebsocketHTTPClient(hanging) // the reconnection uses the same client
		return errors.New("no network")
	})
	if err := p.Connect(context.Background()); calyxerr.CodeOf(err) != codes.Unavailable {
		t.Fatalf("got %v", err)
	}
	return p, dialing
}

// A reconnection that passed its check of closed before Destroy fails when Destroy ends
// p.life. Its DISCONNECTED must not reach the row of the closed instance.
func TestAReconnectionThatFailsBecauseOfDestroyReportsNothing(t *testing.T) {
	log := &statusLog{}
	p, dialing := failedProvider(t, log)
	reconnected := make(chan struct{})
	go func() {
		defer close(reconnected)
		whatsapp.TryReconnect(p)
	}()
	<-dialing // after the check of closed and the CONNECTING report
	// Destroy ends p.life before the disconnect. This wait lets the failed dial report before Destroy
	// sets its own DISCONNECTED, which would hide an equal report.
	whatsapp.SetDisconnectClient(p, func(*whatsmeow.Client) { <-reconnected })

	before := log.count()
	_ = p.Destroy(context.Background())
	<-reconnected
	if late := log.since(before); len(late) > 0 {
		t.Fatalf("the provider reported %v after Destroy", late)
	}
}

// A status report in flight when Destroy starts. Destroy must not return before the report
// ends, or the report writes the row after the close.
func TestDestroyWaitsForAStatusReportInFlight(t *testing.T) {
	log := &statusLog{}
	p, _ := failedProvider(t, log)
	reporting, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	log.mu.Lock()
	log.hold = func(s corev1.InstanceStatus) {
		if s == corev1.InstanceStatus_INSTANCE_STATUS_CONNECTING {
			once.Do(func() { close(reporting) })
			<-release
		}
	}
	log.mu.Unlock()
	reconnected := make(chan struct{})
	go func() {
		defer close(reconnected)
		whatsapp.TryReconnect(p)
	}()
	<-reporting

	destroyed := make(chan struct{})
	go func() {
		defer close(destroyed)
		_ = p.Destroy(context.Background())
	}()
	select {
	case <-destroyed:
		close(release)
		<-reconnected
		t.Fatal("Destroy returned while the CONNECTING report was in flight")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	<-destroyed
	after := log.count()
	<-reconnected
	if late := log.since(after); len(late) > 0 {
		t.Fatalf("the provider reported %v after Destroy returned", late)
	}
}
