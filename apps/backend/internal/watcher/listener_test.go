package watcher_test

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"calyx/backend/internal/core"
	"calyx/backend/internal/instances"
	"calyx/backend/internal/live"
	"calyx/backend/internal/testutil"
	"calyx/backend/internal/watcher"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// testHeaderWait replaces the wait of 10 s, so a test of "no headers" is fast.
const testHeaderWait = 100 * time.Millisecond

// logBuffer collects the log lines of a listener. The listener writes from its own goroutines.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// lines returns the log lines that contain s.
func (b *logBuffer) lines(s string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for _, l := range strings.Split(b.buf.String(), "\n") {
		if strings.Contains(l, s) {
			out = append(out, l)
		}
	}
	return out
}

// startListener runs a listener of w on the client c until the test ends. The returned function
// cancels it and waits until Run returns.
func (e *env) startListener(t *testing.T, w *watcher.Watcher, c *core.Client) (*logBuffer, func()) {
	t.Helper()
	return e.startListenerOf(t, w, c, "dev")
}

// startListenerOf is startListener for a backend of the version backendVersion.
func (e *env) startListenerOf(t *testing.T, w *watcher.Watcher, c *core.Client, backendVersion string) (*logBuffer, func()) {
	t.Helper()
	logs := &logBuffer{}
	l := watcher.NewListener(w, c, e.lv, e.clk, slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})), backendVersion)
	watcher.SetHeaderWait(l, testHeaderWait)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); l.Run(ctx) }()
	stop := func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Run did not return after the end of the context")
		}
	}
	t.Cleanup(func() { cancel(); <-done })
	return logs, stop
}

// waitLinkUp waits until the core link came up n times.
func waitLinkUp(t *testing.T, e *env, n uint64) {
	t.Helper()
	waitFor(t, "the core link", func() bool { return e.lv.LinkNumber() == n })
}

func subscribeCalls(e *env) int { return len(e.fc.SubscribeRequests()) }

func statusCallsOf(e *env, name string) int {
	n := 0
	for _, r := range e.fc.StatusRequests() {
		if r.GetInstanceName() == name {
			n++
		}
	}
	return n
}

// expectWait checks that the listener waits exactly d before its next attempt, and then lets it
// try again. attempts is the number of attempts so far.
func expectWait(t *testing.T, e *env, attempts func() int, n int, d time.Duration) {
	t.Helper()
	waitFor(t, "the wait after the failure", func() bool { return attempts() == n && e.clk.PendingTimers() == 1 })
	e.clk.Advance(d - time.Millisecond)
	if e.clk.PendingTimers() != 1 || attempts() != n {
		t.Fatalf("the listener did not wait %v", d)
	}
	e.clk.Advance(time.Millisecond)
	waitFor(t, "the next attempt", func() bool { return attempts() == n+1 })
}

func receive(t *testing.T, sub *live.Subscription) {
	t.Helper()
	select {
	case <-sub.Changed():
	case <-time.After(2 * time.Second):
		t.Fatal("no publish")
	}
}

func TestAnEventLeadsToAWriteAndThenAPublish(t *testing.T) { // goal 2
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stConnected)
	e.startListener(t, e.mailOff(), e.cl)
	waitLinkUp(t, e, 1)
	reqs := e.fc.SubscribeRequests()
	if len(reqs) != 1 || len(reqs[0].EventTypes) != 1 || reqs[0].EventTypes[0] != "connection.update" {
		t.Fatalf("subscribe requests = %v", reqs)
	}

	sub := e.lv.Subscribe(ownerPrincipal(e))
	defer sub.Close()
	e.fc.SetStatus(in.CoreName, stNeedsAuth)
	e.fc.PublishConnection(in.CoreName, stNeedsAuth)
	receive(t, sub)
	if got := e.row(t, in.ID).LastStatus; got == nil || *got != "NEEDS_AUTH" {
		t.Fatalf("status when the publish came = %v, want NEEDS_AUTH", got)
	}
}

func TestTwentyEventsDuringOneCheckMakeOneMoreCheck(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stConnected)
	e.fc.SetDelay(testutil.MethodGetInstanceStatus, 500*time.Millisecond)
	e.startListener(t, e.mailOff(), e.cl)
	waitLinkUp(t, e, 1)

	e.fc.PublishConnection(in.CoreName, stConnected)
	waitFor(t, "the first check", func() bool { return statusCallsOf(e, in.CoreName) == 1 })
	for range 20 {
		e.fc.PublishConnection(in.CoreName, stConnected)
	}
	waitFor(t, "the second check", func() bool { return statusCallsOf(e, in.CoreName) == 2 })
	time.Sleep(700 * time.Millisecond) // a third check would start in this time
	if n := statusCallsOf(e, in.CoreName); n != 2 {
		t.Fatalf("checks = %d, want 2", n)
	}
}

func TestTheStreamEndsAndTheListenerSubscribesAgain(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stConnected)
	e.startListener(t, e.mailOff(), e.cl)
	waitLinkUp(t, e, 1)

	e.fc.EndSubscriptions()
	waitFor(t, "the link down", func() bool { return e.lv.Core() == live.CoreUnreachable })
	expectWait(t, e, func() int { return subscribeCalls(e) }, 1, time.Second)
	waitLinkUp(t, e, 2)

	// The new stream delivers events.
	e.fc.SetStatus(in.CoreName, stNeedsAuth)
	e.fc.PublishConnection(in.CoreName, stNeedsAuth)
	waitFor(t, "the check of the event", func() bool {
		s := e.row(t, in.ID).LastStatus
		return s != nil && *s == "NEEDS_AUTH"
	})
}

func TestNoHeadersIsAFailureAndTheWaitGrows(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	e.fc.SetSubscribeHeaders(false)
	e.startListener(t, e.mailOff(), e.cl)
	attempts := func() int { return subscribeCalls(e) }

	expectWait(t, e, attempts, 1, time.Second)
	if e.lv.Core() != live.CoreUnreachable || e.lv.LinkNumber() != 0 {
		t.Fatalf("core = %s, link = %d; want unreachable and no link", e.lv.Core(), e.lv.LinkNumber())
	}
	expectWait(t, e, attempts, 2, 2*time.Second)
	expectWait(t, e, attempts, 3, 5*time.Second)
	e.fc.SetSubscribeHeaders(true)
	expectWait(t, e, attempts, 4, 5*time.Second)
	waitLinkUp(t, e, 1)

	// Headers start the waits again at 1 s.
	e.fc.EndSubscriptions()
	expectWait(t, e, attempts, 5, time.Second)
	waitLinkUp(t, e, 2)
}

// refusingCore answers every call with UNAUTHENTICATED, as the real core does with a wrong token.
func refusingCore(t *testing.T) (*core.Client, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(grpc.StreamInterceptor(func(any, grpc.ServerStream, *grpc.StreamServerInfo, grpc.StreamHandler) error {
		calls.Add(1)
		return status.Error(codes.Unauthenticated, "Missing or wrong authorization token.")
	}))
	corev1.RegisterEventServiceServer(srv, corev1.UnimplementedEventServiceServer{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := core.Dial(lis.Addr().String(), "wrong")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return core.NewClient(conn), &calls
}

func TestARefusedTokenIsAFailure(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	c, calls := refusingCore(t)
	logs, _ := e.startListener(t, e.mailOff(), c)
	attempts := func() int { return int(calls.Load()) }

	expectWait(t, e, attempts, 1, time.Second)
	if e.lv.Core() != live.CoreUnreachable || e.lv.LinkNumber() != 0 {
		t.Fatalf("core = %s, link = %d; want unreachable and no link", e.lv.Core(), e.lv.LinkNumber())
	}
	expectWait(t, e, attempts, 2, 2*time.Second)
	if n := len(logs.lines("level=WARN")); n != 1 {
		t.Fatalf("WARN lines = %d, want 1", n)
	}
}

func TestAnUnknownNameIsIgnored(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stConnected)
	e.startListener(t, e.mailOff(), e.cl)
	waitLinkUp(t, e, 1)

	e.fc.PublishConnection("i_00000000000000000000000000000000", stConnected)
	e.fc.PublishConnection(in.CoreName, stConnected)
	waitFor(t, "the check of the known name", func() bool { return statusCallsOf(e, in.CoreName) == 1 })
	time.Sleep(100 * time.Millisecond)
	if n := len(e.fc.StatusRequests()); n != 1 {
		t.Fatalf("status requests = %d, want 1", n)
	}
	if e.lv.LinkNumber() != 1 || subscribeCalls(e) != 1 {
		t.Fatal("an unknown name ended the stream")
	}
}

func TestAnEventDoesNotChangeARowInCreating(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{State: "creating"}, stConnected)
	other := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stConnected)
	e.startListener(t, e.mailOff(), e.cl)
	waitLinkUp(t, e, 1)

	e.fc.PublishConnection(in.CoreName, stConnected)
	time.Sleep(100 * time.Millisecond)
	e.fc.PublishConnection(other.CoreName, stConnected)
	waitFor(t, "the check of the other row", func() bool { return statusCallsOf(e, other.CoreName) == 1 })
	if n := statusCallsOf(e, in.CoreName); n != 0 {
		t.Fatalf("status requests of the creating row = %d, want 0", n)
	}
	if r := e.row(t, in.ID); r.State != "creating" || r.LastStatus != nil {
		t.Fatalf("row = %+v", r)
	}
}

func TestTheListenerStopsWhenTheContextEnds(t *testing.T) {
	t.Run("while it listens", func(t *testing.T) {
		e := newEnv(t, testutil.FakeSMTPOptions{})
		_, stop := e.startListener(t, e.mailOff(), e.cl)
		waitLinkUp(t, e, 1)
		stop()
	})
	t.Run("while it waits", func(t *testing.T) {
		e := newEnv(t, testutil.FakeSMTPOptions{})
		e.fc.SetSubscribeHeaders(false)
		_, stop := e.startListener(t, e.mailOff(), e.cl)
		waitFor(t, "the wait", func() bool { return e.clk.PendingTimers() == 1 })
		stop()
	})
	t.Run("while it checks", func(t *testing.T) {
		e := newEnv(t, testutil.FakeSMTPOptions{})
		in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stConnected)
		e.fc.SetDelay(testutil.MethodGetInstanceStatus, time.Minute)
		_, stop := e.startListener(t, e.mailOff(), e.cl)
		waitLinkUp(t, e, 1)
		e.fc.PublishConnection(in.CoreName, stConnected)
		waitFor(t, "the check", func() bool { return statusCallsOf(e, in.CoreName) == 1 })
		stop()
	})
}

func TestALostEventShowsAfterTheNextRound(t *testing.T) { // goal 3
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stConnected)
	w := e.mailOff()
	e.startRun(t, w)
	e.startListener(t, w, e.cl)
	waitFor(t, "core ok", func() bool { return e.lv.Core() == live.CoreOK })

	e.fc.EndSubscriptions()
	waitFor(t, "the link down", func() bool { return e.lv.Core() == live.CoreUnreachable })
	e.fc.SetStatus(in.CoreName, stNeedsAuth) // the core sends no event: the link is down
	waitFor(t, "the wait and the timer of Run", func() bool { return e.clk.PendingTimers() == 2 })
	e.clk.Advance(time.Second)
	waitFor(t, "the status from the round", func() bool {
		s := e.row(t, in.ID).LastStatus
		return s != nil && *s == "NEEDS_AUTH"
	})
	waitFor(t, "core ok again", func() bool { return e.lv.Core() == live.CoreOK })
	if n := len(e.fc.StatusRequests()); n != 0 {
		t.Fatalf("status requests = %d: the round, not a check, must find the change", n)
	}
}

func TestPairInstanceAfterChanged(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stConnected)
	e.startListener(t, e.mailOff(), e.cl)
	waitLinkUp(t, e, 1)
	pair := func() error {
		_, err := e.inst.Pair(context.Background(), ownerPrincipal(e), in.ID, instances.PairInput{Pairing: "qr"})
		return err
	}
	if pair() == nil {
		t.Fatal("Pair of a connected instance succeeded")
	}

	sub := e.lv.Subscribe(ownerPrincipal(e))
	defer sub.Close()
	e.fc.SetStatus(in.CoreName, stNeedsAuth)
	e.fc.PublishConnection(in.CoreName, stNeedsAuth)
	receive(t, sub)
	if err := pair(); err != nil {
		t.Fatalf("Pair after changed: %v", err)
	}
}

func TestTheLogLinesOfTheCoreLink(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	in := e.addInstance(t, testutil.InstanceSeed{LastStatus: strp("CONNECTED")}, stConnected)
	e.fc.SetSubscribeHeaders(false)
	logs, _ := e.startListener(t, e.mailOff(), e.cl)
	attempts := func() int { return subscribeCalls(e) }

	expectWait(t, e, attempts, 1, time.Second)
	e.fc.SetSubscribeHeaders(true)
	expectWait(t, e, attempts, 2, 2*time.Second)
	waitLinkUp(t, e, 1)
	if n := len(logs.lines("level=WARN")); n != 1 {
		t.Fatalf("WARN lines after two failures = %d, want 1", n)
	}
	up := logs.lines("The core link is up.")
	if len(up) != 1 || !strings.Contains(up[0], "level=INFO") || !strings.Contains(up[0], "outage_seconds=3") ||
		strings.Contains(up[0], "last_event_age_seconds") {
		t.Fatalf("first INFO lines = %q", up)
	}

	e.fc.PublishConnection(in.CoreName, stConnected)
	waitFor(t, "the check", func() bool { return statusCallsOf(e, in.CoreName) == 1 })
	e.clk.Advance(10 * time.Second)
	e.fc.EndSubscriptions()
	expectWait(t, e, attempts, 3, time.Second)
	waitLinkUp(t, e, 2)
	down := logs.lines("level=WARN")
	if len(down) != 2 || !strings.Contains(down[1], "The core link is down.") {
		t.Fatalf("WARN lines = %q", down)
	}
	up = logs.lines("The core link is up.")
	if len(up) != 2 || !strings.Contains(up[1], "outage_seconds=1") || !strings.Contains(up[1], "last_event_age_seconds=11") {
		t.Fatalf("INFO lines = %q", up)
	}
}

// relink ends the stream and waits until the core link came up for the nth time.
func relink(t *testing.T, e *env, n uint64) {
	t.Helper()
	e.fc.EndSubscriptions()
	waitFor(t, "the link down", func() bool { return e.lv.Core() == live.CoreUnreachable })
	expectWait(t, e, func() int { return subscribeCalls(e) }, int(n)-1, time.Second)
	waitLinkUp(t, e, n)
}

func TestTheListenerLogsTheCoreVersionAndWarnsOnceForEachChange(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	e.fc.SetVersion("1.2.3")
	logs, _ := e.startListenerOf(t, e.mailOff(), e.cl, "1.0.0")
	waitLinkUp(t, e, 1)
	if got := e.lv.CoreVersion(); got != "1.2.3" {
		t.Fatalf("live core version = %q, want 1.2.3", got)
	}
	changed := logs.lines("The core version changed.")
	if len(changed) != 1 || !strings.Contains(changed[0], `old_version=""`) || !strings.Contains(changed[0], "new_version=1.2.3") {
		t.Fatalf("changed lines = %q", changed)
	}
	differs := logs.lines("The core version differs from the backend version.")
	if len(differs) != 1 || !strings.Contains(differs[0], "level=WARN") ||
		!strings.Contains(differs[0], "core_version=1.2.3") || !strings.Contains(differs[0], "backend_version=1.0.0") {
		t.Fatalf("differs lines = %q", differs)
	}

	relink(t, e, 2) // the same version: no new line
	if n := len(logs.lines("The core version changed.")); n != 1 {
		t.Fatalf("changed lines after the same version = %d, want 1", n)
	}
	if n := len(logs.lines("The core version differs")); n != 1 {
		t.Fatalf("differs lines after the same version = %d, want 1", n)
	}

	e.fc.SetVersion("1.0.0")
	relink(t, e, 3) // now the versions agree
	changed = logs.lines("The core version changed.")
	if len(changed) != 2 || !strings.Contains(changed[1], "old_version=1.2.3") || !strings.Contains(changed[1], "new_version=1.0.0") {
		t.Fatalf("changed lines = %q", changed)
	}
	if n := len(logs.lines("The core version differs")); n != 1 {
		t.Fatalf("differs lines after equal versions = %d, want 1", n)
	}

	e.fc.SetVersion("1.3.0")
	relink(t, e, 4) // a second difference warns again
	if n := len(logs.lines("The core version differs")); n != 2 {
		t.Fatalf("differs lines after a new difference = %d, want 2", n)
	}
}

func TestTheListenerDoesNotWarnWhenAVersionIsDev(t *testing.T) {
	for _, c := range []struct{ name, core, backend string }{
		{"dev backend", "1.2.3", "dev"},
		{"dev core", "dev", "1.0.0"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, testutil.FakeSMTPOptions{})
			e.fc.SetVersion(c.core)
			logs, _ := e.startListenerOf(t, e.mailOff(), e.cl, c.backend)
			waitLinkUp(t, e, 1)
			if n := len(logs.lines("The core version changed.")); n != 1 {
				t.Fatalf("changed lines = %d, want 1", n)
			}
			if n := len(logs.lines("The core version differs")); n != 0 {
				t.Fatalf("differs lines = %d, want 0", n)
			}
		})
	}
}

func TestTheListenerDoesNotWarnWhenTheCoreVersionIsUnknown(t *testing.T) {
	e := newEnv(t, testutil.FakeSMTPOptions{})
	e.fc.SetVersion("1.2.3")
	logs, _ := e.startListenerOf(t, e.mailOff(), e.cl, "1.0.0")
	waitLinkUp(t, e, 1)
	if n := len(logs.lines("The core version differs")); n != 1 {
		t.Fatalf("differs lines = %d, want 1", n)
	}

	e.fc.SetVersion("") // a core with no version header
	relink(t, e, 2)
	if n := len(logs.lines("The core version changed.")); n != 2 {
		t.Fatalf("changed lines = %d, want 2", n)
	}
	if n := len(logs.lines("The core version differs")); n != 1 {
		t.Fatalf("differs lines after an unknown version = %d, want 1", n)
	}
}
