package httpapi

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/instances"
	"calyx/backend/internal/testutil"
)

type sseFixture struct {
	srv      *httptest.Server
	events   chan instances.PairingEvent
	shutdown chan struct{}
	returned chan struct{} // closed when streamPairing returns
	clk      *testutil.FakeClock
	denied   atomic.Bool // the answer of the access check
	checks   atomic.Int32
	hang     atomic.Bool // the access check waits for the end of its context, then keeps the stream
}

func newSSEFixture(t *testing.T) *sseFixture {
	t.Helper()
	return newSSEFixtureWithLimit(t, writeLimit)
}

// newSSEFixtureWithLimit is newSSEFixture with another limit for each write.
func newSSEFixtureWithLimit(t *testing.T, limit time.Duration) *sseFixture {
	t.Helper()
	f := &sseFixture{
		events:   make(chan instances.PairingEvent, 16),
		shutdown: make(chan struct{}),
		returned: make(chan struct{}),
		clk:      testutil.NewFakeClock(testStart),
	}
	f.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(f.returned)
		sse, err := startSSE(w)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		sse.limit = limit
		sse.checkLimit = 100 * time.Millisecond
		allowed := func(ctx context.Context) bool {
			f.checks.Add(1)
			if f.hang.Load() {
				<-ctx.Done()
			}
			return !f.denied.Load()
		}
		streamPairing(r.Context(), sse, f.events, allowed, f.clk)
	}))
	f.srv.Config.BaseContext = func(_ net.Listener) context.Context {
		return WithShutdownSignal(context.Background(), f.shutdown)
	}
	f.srv.Start()
	t.Cleanup(f.srv.Close)
	return f
}

func (f *sseFixture) get(t *testing.T) (*http.Response, *bufio.Reader) {
	t.Helper()
	resp, err := http.Get(f.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp, bufio.NewReader(resp.Body)
}

func readBlock(t *testing.T, br *bufio.Reader) string {
	t.Helper()
	var sb strings.Builder
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v (got %q)", err, sb.String())
		}
		sb.WriteString(line)
		if line == "\n" {
			return sb.String()
		}
	}
}

func wantReturned(t *testing.T, f *sseFixture) {
	t.Helper()
	select {
	case <-f.returned:
	case <-time.After(time.Second):
		t.Fatal("the handler did not return within 1 s")
	}
}

func strp(s string) *string { return &s }

func TestSSEHeaders(t *testing.T) {
	f := newSSEFixture(t)
	resp, _ := f.get(t) //nolint:bodyclose // get closes the body in t.Cleanup
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	for k, v := range map[string]string{
		"Content-Type": "text/event-stream", "Cache-Control": "no-cache", "X-Accel-Buffering": "no",
	} {
		if got := resp.Header.Get(k); got != v {
			t.Fatalf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestUpdateThenDone(t *testing.T) {
	f := newSSEFixture(t)
	f.events <- instances.PairingEvent{Kind: "update", Update: instances.PairingUpdate{Status: "NEEDS_AUTH", PairingCode: strp("ABCD1234")}}
	f.events <- instances.PairingEvent{Kind: "done"}
	resp, _ := f.get(t) //nolint:bodyclose // get closes the body in t.Cleanup
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	want := "event: update\ndata: {\"status\":\"NEEDS_AUTH\",\"pairing_code\":\"ABCD1234\",\"qr_text\":null,\"message\":\"\"}\n\n" +
		"event: done\ndata: {\"status\":\"CONNECTED\"}\n\n"
	if string(body) != want {
		t.Fatalf("body = %q\nwant   %q", body, want)
	}
}

func TestFailedEndsTheStream(t *testing.T) {
	f := newSSEFixture(t)
	f.events <- instances.PairingEvent{Kind: "failed", FailCode: apierr.CodePairingExpired, Detail: "x"}
	resp, _ := f.get(t) //nolint:bodyclose // get closes the body in t.Cleanup
	body, _ := io.ReadAll(resp.Body)
	want := "event: failed\ndata: {\"code\":\"pairing_expired\",\"detail\":\"x\"}\n\n"
	if string(body) != want {
		t.Fatalf("body = %q", body)
	}
}

func waitTimers(t *testing.T, f *sseFixture, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for f.clk.PendingTimers() != n {
		if time.Now().After(deadline) {
			t.Fatalf("pending timers = %d, want %d", f.clk.PendingTimers(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAPingEvery15Seconds(t *testing.T) {
	f := newSSEFixture(t)
	f.events <- instances.PairingEvent{Kind: "update", Update: instances.PairingUpdate{Status: "NEEDS_AUTH", QRText: strp("qr")}}
	_, br := f.get(t) //nolint:bodyclose // get closes the body in t.Cleanup
	readBlock(t, br)
	waitTimers(t, f, 1)
	f.clk.Advance(15 * time.Second)
	if got := readBlock(t, br); got != ": ping\n\n" {
		t.Fatalf("first ping = %q", got)
	}
	waitTimers(t, f, 1)
	f.clk.Advance(15 * time.Second)
	if got := readBlock(t, br); got != ": ping\n\n" {
		t.Fatalf("second ping = %q", got)
	}
}

func TestNoPingBefore15Seconds(t *testing.T) {
	f := newSSEFixture(t)
	f.events <- instances.PairingEvent{Kind: "update", Update: instances.PairingUpdate{Status: "NEEDS_AUTH", QRText: strp("qr")}}
	_, br := f.get(t) //nolint:bodyclose // get closes the body in t.Cleanup
	readBlock(t, br)
	waitTimers(t, f, 1)
	f.clk.Advance(14 * time.Second)
	f.events <- instances.PairingEvent{Kind: "done"}
	if got := readBlock(t, br); !strings.HasPrefix(got, "event: done\n") {
		t.Fatalf("next block = %q, want the done event", got)
	}
}

func TestTheClientLeaving(t *testing.T) {
	f := newSSEFixture(t)
	f.events <- instances.PairingEvent{Kind: "update", Update: instances.PairingUpdate{Status: "NEEDS_AUTH", QRText: strp("qr")}}
	resp, br := f.get(t)
	readBlock(t, br)
	resp.Body.Close()
	wantReturned(t, f)
}

func TestShutdownEndsTheStream(t *testing.T) {
	f := newSSEFixture(t)
	resp, _ := f.get(t) //nolint:bodyclose // get closes the body in t.Cleanup
	close(f.shutdown)
	done := make(chan []byte, 1)
	go func() { b, _ := io.ReadAll(resp.Body); done <- b }()
	select {
	case b := <-done:
		if len(b) != 0 {
			t.Fatalf("body = %q, want nothing", b)
		}
	case <-time.After(time.Second):
		t.Fatal("the stream did not end within 1 s")
	}
	wantReturned(t, f)
}

func TestAClosedChannelEndsTheStream(t *testing.T) {
	f := newSSEFixture(t)
	close(f.events)
	resp, _ := f.get(t) //nolint:bodyclose // get closes the body in t.Cleanup
	body, _ := io.ReadAll(resp.Body)
	if len(body) != 0 {
		t.Fatalf("body = %q", body)
	}
}

func TestStartSSEWithoutFlush(t *testing.T) {
	if _, err := startSSE(noFlushWriter{httptest.NewRecorder()}); err == nil {
		t.Fatal("startSSE accepted a writer that cannot flush")
	}
}

// noFlushWriter hides the Flush method of the recorder.
type noFlushWriter struct{ w *httptest.ResponseRecorder }

func (n noFlushWriter) Header() http.Header         { return n.w.Header() }
func (n noFlushWriter) Write(b []byte) (int, error) { return n.w.Write(b) }
func (n noFlushWriter) WriteHeader(c int)           { n.w.WriteHeader(c) }

// The access check runs at each ping, before the ping. A refused check ends the stream with no
// event, so the client connects again and gets 401.
func TestTheAccessCheckRunsAtEachPing(t *testing.T) {
	f := newSSEFixture(t)
	f.events <- instances.PairingEvent{Kind: "update", Update: instances.PairingUpdate{Status: "NEEDS_AUTH", QRText: strp("qr")}}
	resp, br := f.get(t) //nolint:bodyclose // get closes the body in t.Cleanup
	readBlock(t, br)
	waitTimers(t, f, 1)
	f.clk.Advance(15 * time.Second)
	if got := readBlock(t, br); got != ": ping\n\n" {
		t.Fatalf("first ping = %q", got)
	}
	if n := f.checks.Load(); n != 1 {
		t.Fatalf("checks = %d, want 1", n)
	}
	f.denied.Store(true)
	waitTimers(t, f, 1)
	f.clk.Advance(15 * time.Second)
	rest, err := io.ReadAll(resp.Body)
	if err != nil || len(rest) != 0 {
		t.Fatalf("rest = %q, %v; want a clean end with nothing", rest, err)
	}
	wantReturned(t, f)
}

// Each write sets a new deadline: a write after a quiet time longer than the limit still works,
// and the stream still ends cleanly.
func TestEachWriteSetsANewDeadline(t *testing.T) {
	f := newSSEFixtureWithLimit(t, 100*time.Millisecond)
	f.events <- instances.PairingEvent{Kind: "update", Update: instances.PairingUpdate{Status: "NEEDS_AUTH", QRText: strp("qr")}}
	resp, br := f.get(t) //nolint:bodyclose // get closes the body in t.Cleanup
	readBlock(t, br)
	time.Sleep(300 * time.Millisecond)
	f.events <- instances.PairingEvent{Kind: "done"}
	rest, err := io.ReadAll(resp.Body)
	if err != nil || string(rest) != "event: done\ndata: {\"status\":\"CONNECTED\"}\n\n" {
		t.Fatalf("rest = %q, %v", rest, err)
	}
}

// A client that does not read fills the buffers of the connection. The write that cannot go on
// fails after the limit, and the stream ends.
func TestAWriteHasALimit(t *testing.T) {
	f := newSSEFixtureWithLimit(t, 200*time.Millisecond)
	conn := dialWithoutReading(t, f.srv.URL, "/")
	defer conn.Close()
	big := strings.Repeat("q", 64<<10)
	go func() {
		for {
			select {
			case f.events <- instances.PairingEvent{Kind: "update", Update: instances.PairingUpdate{Status: "NEEDS_AUTH", QRText: &big}}:
			case <-f.returned:
				return
			}
		}
	}()
	select {
	case <-f.returned:
	case <-time.After(10 * time.Second):
		t.Fatal("the stream did not end while the client did not read")
	}
}

// dialWithoutReading sends a GET with a small receive buffer and never reads the answer.
func dialWithoutReading(t *testing.T, base, path string, header ...string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(base, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetReadBuffer(4096)
	}
	req := "GET " + path + " HTTP/1.1\r\nHost: test\r\n" + strings.Join(header, "") + "\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		t.Fatal(err)
	}
	return conn
}

// A writer without SetWriteDeadline (httptest.ResponseRecorder) still gets the events.
func TestAWriterWithoutDeadlinesWorks(t *testing.T) {
	rec := httptest.NewRecorder()
	sse, err := startSSE(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := sse.event("done", doneJSON{Status: statusConnected}); err != nil {
		t.Fatalf("event: %v", err)
	}
	if err := sse.ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

// A check that hangs (a database that does not answer) has a time limit. After it, the stream
// goes on as after any error that is not 401, and it still reacts to the stop of the server.
func TestAHungAccessCheckHasALimit(t *testing.T) {
	f := newSSEFixture(t)
	f.hang.Store(true)
	f.events <- instances.PairingEvent{Kind: "update", Update: instances.PairingUpdate{Status: "NEEDS_AUTH", QRText: strp("qr")}}
	resp, br := f.get(t) //nolint:bodyclose // get closes the body in t.Cleanup
	readBlock(t, br)
	waitTimers(t, f, 1)
	f.clk.Advance(15 * time.Second)
	if got := readBlock(t, br); got != ": ping\n\n" {
		t.Fatalf("block after a hung check = %q, want a ping", got)
	}
	waitTimers(t, f, 1)
	f.clk.Advance(15 * time.Second) // the next check hangs too
	close(f.shutdown)
	done := make(chan struct{})
	go func() { _, _ = io.ReadAll(resp.Body); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the stream did not end within 1 s of the stop")
	}
	wantReturned(t, f)
}
