package meta_test

import (
	"context"
	"net/http"
	"testing"

	"calyx/core/internal/engine"
	"calyx/core/internal/providers/meta"
	"calyx/core/internal/testutil"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// holdingTransport sends each request to the fake Graph API only after release is closed.
// calling is closed when the first request starts.
type holdingTransport struct {
	calling, release chan struct{}
}

func (h holdingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	select {
	case <-h.calling:
	default:
		close(h.calling)
	}
	<-h.release
	return http.DefaultTransport.RoundTrip(r)
}

func (c *collected) since(n int) []corev1.InstanceStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]corev1.InstanceStatus(nil), c.statuses[n:]...)
}

// A Connect of the retry timer that waits for Meta when Destroy runs must not write the status of
// the closed instance when Meta answers.
func TestAConnectThatEndsAfterDestroyReportsNothing(t *testing.T) {
	graph := testutil.NewFakeGraphAPI(t)
	hold := holdingTransport{calling: make(chan struct{}), release: make(chan struct{})}
	c := &collected{}
	p, err := meta.New(engine.InstanceRow{Name: "shop", Provider: "META_CLOUD", ProviderConfig: credentials}, c.hooks(),
		meta.Options{GraphURL: graph.URL, Client: &http.Client{Transport: hold}})
	if err != nil {
		t.Fatal(err)
	}
	connected := make(chan struct{})
	go func() {
		defer close(connected)
		_ = p.Connect(context.Background())
	}()
	<-hold.calling

	_ = p.Destroy(context.Background())
	c.mu.Lock()
	before := len(c.statuses)
	c.mu.Unlock()
	close(hold.release)
	<-connected
	if late := c.since(before); len(late) > 0 {
		t.Fatalf("the provider reported %v after Destroy", late)
	}
}

// Destroy can run just before the retry timer calls Connect. That Connect must not bring the
// closed provider back: no report, and no new retry every 30 s.
func TestAConnectAfterDestroyDoesNotReviveTheProvider(t *testing.T) {
	graph := testutil.NewFakeGraphAPI(t)
	graph.Close() // Meta unreachable: a live provider would retry
	p, c := newProvider(t, graph)

	_ = p.Destroy(context.Background())
	_ = p.Connect(context.Background())
	if late := c.since(0); len(late) > 0 {
		t.Errorf("the provider reported %v after Destroy", late)
	}
	if meta.RetryArmed(p) {
		t.Error("the closed provider scheduled a new Connect")
	}
}
