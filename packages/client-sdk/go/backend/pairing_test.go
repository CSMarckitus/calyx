package backend

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/calyx"
)

const pairingPath = "/api/v1/instances/i1/pairing"

// sseHandler sends the status and the event-stream headers, and then runs body.
func sseHandler(body func(w http.ResponseWriter, r *http.Request, flush func())) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f := w.(http.Flusher)
		f.Flush()
		body(w, r, f.Flush)
	}
}

// collect reads all the items of the iterator. It stops after an error, like a program that
// returns on the first error.
func collect(ctx context.Context, t *testing.T, c *Client) ([]PairingItem, error) {
	t.Helper()
	var items []PairingItem
	for item, err := range c.FollowPairing(ctx, "i1") {
		if err != nil {
			return items, err
		}
		items = append(items, item)
	}
	return items, nil
}

// setIdle sets a short idle time for one test.
func setIdle(t *testing.T, d time.Duration) {
	t.Helper()
	old := idleTimeout
	idleTimeout = d
	t.Cleanup(func() { idleTimeout = old })
}

func TestFollowPairingRequest(t *testing.T) {
	got := make(chan *http.Request, 1)
	c := newTestClient(t, sseHandler(func(w http.ResponseWriter, r *http.Request, flush func()) {
		got <- r
		fmt.Fprint(w, "event: done\ndata: {\"status\":\"connected\"}\n\n")
	}), Config{APIKey: testAPIKey})

	if _, err := collect(context.Background(), t, c); err != nil {
		t.Fatal(err)
	}
	r := <-got
	if r.Method != http.MethodGet || r.URL.Path != pairingPath {
		t.Errorf("request = %s %s", r.Method, r.URL.Path)
	}
	if a := r.Header.Get("Accept"); a != "text/event-stream" {
		t.Errorf("Accept = %q", a)
	}
	if a := r.Header.Get("Authorization"); a != "Bearer "+testAPIKey {
		t.Errorf("Authorization = %q", a)
	}
}

func TestFollowPairingRejectsA2xxThatIsNotAStream(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"connected"}`)
	}, Config{})

	_, err := collect(context.Background(), t, c)
	_ = asCalyxError(t, err, calyx.CodeInvalidResponse)
}

func TestFollowPairingUpdatesThenDone(t *testing.T) {
	c := newTestClient(t, sseHandler(func(w http.ResponseWriter, r *http.Request, flush func()) {
		fmt.Fprint(w, ": ping\n\n")
		fmt.Fprint(w, "event: update\ndata: {\"status\":\"waiting_qr\",\"message\":\"m1\",\"qr_text\":\"q1\",\"pairing_code\":null}\n\n")
		fmt.Fprint(w, "event: other\ndata: {\"x\":1}\n\n")
		fmt.Fprint(w, "event: update\ndata: {\"status\":\"waiting_qr\",\"message\":\"m2\",\"qr_text\":\"q2\",\"pairing_code\":null}\n\n")
		fmt.Fprint(w, "event: done\ndata: {\"status\":\"connected\"}\n\n")
		flush()
		// The stream stays open: the loop must end at done, not at the end of the stream.
		<-r.Context().Done()
	}), Config{})

	items, err := collect(context.Background(), t, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3: %+v", len(items), items)
	}
	for i, want := range []string{"q1", "q2"} {
		u := items[i].Update
		if items[i].Event != "update" || u == nil || u.QrText == nil || *u.QrText != want {
			t.Errorf("item %d = %+v", i, items[i])
		}
	}
	if items[2].Event != "done" || items[2].Done == nil || items[2].Done.Status != "connected" {
		t.Errorf("last item = %+v", items[2])
	}
}

func TestFollowPairingUpdateThenFailed(t *testing.T) {
	c := newTestClient(t, sseHandler(func(w http.ResponseWriter, r *http.Request, flush func()) {
		fmt.Fprint(w, "event: update\ndata: {\"status\":\"waiting_qr\",\"message\":\"m\",\"qr_text\":\"q\",\"pairing_code\":null}\n\n")
		fmt.Fprint(w, "event: failed\ndata: {\"code\":\"pairing_timeout\",\"detail\":\"d\"}\n\n")
		flush()
		<-r.Context().Done()
	}), Config{})

	items, err := collect(context.Background(), t, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Event != "update" {
		t.Fatalf("items = %+v", items)
	}
	f := items[1].Failed
	if items[1].Event != "failed" || f == nil || f.Code != "pairing_timeout" || f.Detail != "d" {
		t.Errorf("last item = %+v", items[1])
	}
}

func TestFollowPairingIdleGivesStreamFailed(t *testing.T) {
	setIdle(t, 100*time.Millisecond)
	c := newTestClient(t, sseHandler(func(w http.ResponseWriter, r *http.Request, flush func()) {
		<-r.Context().Done()
	}), Config{})

	start := time.Now()
	_, err := collect(context.Background(), t, c)
	_ = asCalyxError(t, err, calyx.CodeStreamFailed)
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("the idle rule took %s", d)
	}
}

func TestFollowPairingPingsAreNotIdle(t *testing.T) {
	setIdle(t, 200*time.Millisecond)
	c := newTestClient(t, sseHandler(func(w http.ResponseWriter, r *http.Request, flush func()) {
		for range 8 {
			time.Sleep(40 * time.Millisecond)
			fmt.Fprint(w, ": ping\n\n")
			flush()
		}
		fmt.Fprint(w, "event: done\ndata: {\"status\":\"connected\"}\n\n")
	}), Config{})

	items, err := collect(context.Background(), t, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Event != "done" {
		t.Errorf("items = %+v", items)
	}
}

func TestFollowPairingSlowLoopBodyIsNotIdle(t *testing.T) {
	setIdle(t, 100*time.Millisecond)
	bodyDone := make(chan struct{})
	c := newTestClient(t, sseHandler(func(w http.ResponseWriter, r *http.Request, flush func()) {
		fmt.Fprint(w, "event: update\ndata: {\"status\":\"waiting_qr\",\"message\":\"m\",\"qr_text\":\"q\",\"pairing_code\":null}\n\n")
		flush()
		// done is sent only after the loop body, so it is not in the buffer of the client.
		select {
		case <-bodyDone:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "event: done\ndata: {\"status\":\"connected\"}\n\n")
	}), Config{})

	var events []string
	for item, err := range c.FollowPairing(context.Background(), "i1") {
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, item.Event)
		if item.Event == "update" {
			time.Sleep(300 * time.Millisecond)
			close(bodyDone)
		}
	}
	if strings.Join(events, ",") != "update,done" {
		t.Errorf("events = %v", events)
	}
}

func TestFollowPairingEndBeforeDone(t *testing.T) {
	c := newTestClient(t, sseHandler(func(w http.ResponseWriter, r *http.Request, flush func()) {
		fmt.Fprint(w, "event: update\ndata: {\"status\":\"waiting_qr\",\"message\":\"m\",\"qr_text\":\"q\",\"pairing_code\":null}\n\n")
	}), Config{})

	items, err := collect(context.Background(), t, c)
	_ = asCalyxError(t, err, calyx.CodeStreamFailed)
	if len(items) != 1 {
		t.Errorf("items = %+v", items)
	}
}

func TestFollowPairingDataThatIsNotJSON(t *testing.T) {
	c := newTestClient(t, sseHandler(func(w http.ResponseWriter, r *http.Request, flush func()) {
		fmt.Fprint(w, "event: update\ndata: not json\n\n")
	}), Config{})

	_, err := collect(context.Background(), t, c)
	_ = asCalyxError(t, err, calyx.CodeInvalidResponse)
}

func TestFollowPairing404IsTheFirstItem(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.Header().Set("X-Request-Id", "r1")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"code":"not_found","detail":"No pairing."}`)
	}, Config{})

	n := 0
	for _, err := range c.FollowPairing(context.Background(), "i1") {
		n++
		e := asCalyxError(t, err, calyx.CodeNotFound)
		if e.Source != calyx.SourceBackend || e.Status != http.StatusNotFound || e.RequestID != "r1" {
			t.Errorf("error = %+v", e)
		}
	}
	if n != 1 {
		t.Errorf("got %d items, want 1", n)
	}
}

func TestFollowPairingBreakClosesTheConnection(t *testing.T) {
	closed := make(chan struct{})
	c := newTestClient(t, sseHandler(func(w http.ResponseWriter, r *http.Request, flush func()) {
		fmt.Fprint(w, "event: update\ndata: {\"status\":\"waiting_qr\",\"message\":\"m\",\"qr_text\":\"q\",\"pairing_code\":null}\n\n")
		flush()
		<-r.Context().Done()
		close(closed)
	}), Config{})

	for _, err := range c.FollowPairing(context.Background(), "i1") {
		if err != nil {
			t.Fatal(err)
		}
		break
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the server did not see the end of the request")
	}
}

func TestFollowPairingCancelGivesAborted(t *testing.T) {
	c := newTestClient(t, sseHandler(func(w http.ResponseWriter, r *http.Request, flush func()) {
		<-r.Context().Done()
	}), Config{APIKey: testAPIKey})

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	_, err := collect(ctx, t, c)
	e := asCalyxError(t, err, calyx.CodeAborted)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("the cause of %v is not context.Canceled", err)
	}
	if strings.Contains(e.Error(), testAPIKey) {
		t.Errorf("the error text has the API key: %s", e.Error())
	}
}

func TestFollowPairingNetworkFailure(t *testing.T) {
	c, err := New(Config{BaseURL: "http://127.0.0.1:1", APIKey: testAPIKey})
	if err != nil {
		t.Fatal(err)
	}
	_, err = collect(context.Background(), t, c)
	e := asCalyxError(t, err, calyx.CodeNetworkError)
	if strings.Contains(e.Error(), testAPIKey) {
		t.Errorf("the error text has the API key: %s", e.Error())
	}
}

func TestFollowPairingSendsTheUserAgent(t *testing.T) {
	got := make(chan string, 1)
	c := newTestClient(t, sseHandler(func(w http.ResponseWriter, r *http.Request, flush func()) {
		got <- r.Header.Get("User-Agent")
		fmt.Fprint(w, "event: done\ndata: {\"status\":\"connected\"}\n\n")
	}), Config{})
	if _, err := collect(context.Background(), t, c); err != nil {
		t.Fatal(err)
	}
	if ua, want := <-got, "calyx-go-sdk/"+calyx.Version; ua != want {
		t.Errorf("User-Agent = %q, want %q", ua, want)
	}
}

func TestFollowPairingEventOverTheLimitClosesTheStream(t *testing.T) {
	setMaxEvent(t, 1024)
	c := newTestClient(t, sseHandler(func(w http.ResponseWriter, r *http.Request, flush func()) {
		fmt.Fprint(w, "event: update\n"+strings.Repeat("data: 1\n", 200)+"\n")
		flush()
		<-r.Context().Done() // the server waits until the client closes the connection
	}), Config{})
	_, err := collect(context.Background(), t, c)
	_ = asCalyxError(t, err, calyx.CodeInvalidResponse)
}

func TestFollowPairingErrorBodyOverTheLimit(t *testing.T) {
	setMaxAnswer(t, 1024)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write(make([]byte, 2048))
	}, Config{})
	_, err := collect(context.Background(), t, c)
	_ = asCalyxError(t, err, calyx.CodeInvalidResponse)
}
