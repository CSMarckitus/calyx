package httpapi

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"calyx/backend/internal/live"
	"calyx/backend/internal/tenancy"
	"calyx/backend/internal/testutil"
)

const (
	eventsPath    = "/api/v1/events"
	changedBlock  = "event: changed\ndata: {}\n\n"
	replacedBlock = "event: replaced\ndata: {}\n\n"
	pingBlock     = ": ping\n\n"
)

// openEvents opens the stream of u with its login cookie and reads the first event.
func openEvents(t *testing.T, e *testEnv, u testUser) *bufio.Reader {
	t.Helper()
	resp, br := openSSE(t, e, eventsPath, withCookie(u.Cookie)) //nolint:bodyclose // openSSE closes the body in t.Cleanup
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := readBlock(t, br); got != changedBlock {
		t.Fatalf("first block = %q, want changed", got)
	}
	return br
}

// The stream subscribes, and then sends one changed: the browser reads its screen at the start.
func TestFollowEventsSendsChangedFirst(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	resp, br := openSSE(t, e, eventsPath, withCookie(o.Owner.Cookie)) //nolint:bodyclose // openSSE closes the body in t.Cleanup
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	for k, v := range map[string]string{
		"Content-Type": "text/event-stream", "Cache-Control": "no-cache", "X-Accel-Buffering": "no",
	} {
		if got := resp.Header.Values(k); len(got) != 1 || got[0] != v {
			t.Fatalf("%s = %q, want %q", k, got, v)
		}
	}
	if got := readBlock(t, br); got != changedBlock {
		t.Fatalf("first block = %q", got)
	}
	// The subscription exists before the first changed, so a change after it is not lost.
	e.Deps.Live.Publish(o.ID, o.DefaultClientID)
	if got := readBlock(t, br); got != changedBlock {
		t.Fatalf("block after a publish = %q", got)
	}
}

func TestFollowEventsNeedsALoginCookie(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	requireProblem(t, e.do("GET", eventsPath, nil), 401, "unauthenticated")
	requireProblem(t, e.asKey(o.Owner, "GET", eventsPath, nil), 403, "api_key_not_allowed")
}

// Each stream gets the changes of its scope only.
func TestFollowEventsFilter(t *testing.T) {
	e := newTestEnv(t)
	a := e.newOrg("Org A", 10, true, true)
	b := e.newOrg("Org B", 10, true, true)
	a1 := e.newClient(a, "A1", true, 1)
	a2 := e.newClient(a, "A2", true, 1)
	streams := map[string]*bufio.Reader{
		"oA":  openEvents(t, e, a.Owner),
		"uA1": openEvents(t, e, e.newClientUser(a, a1.ID, "a1@example.com")),
		"uA2": openEvents(t, e, e.newClientUser(a, a2.ID, "a2@example.com")),
		"oB":  openEvents(t, e, b.Owner),
	}
	e.Deps.Live.Publish(a.ID, a1.ID)
	for _, name := range []string{"oA", "uA1"} {
		if got := readBlock(t, streams[name]); got != changedBlock {
			t.Fatalf("%s: block = %q, want changed", name, got)
		}
	}
	// Two pings in a row prove that no changed waits: a changed that was set before the first ping
	// comes before the second one.
	for i := range 2 {
		e.Clock.Advance(pingInterval)
		for name, br := range streams {
			if got := readBlock(t, br); got != pingBlock {
				t.Fatalf("%s: block %d = %q, want a ping", name, i+1, got)
			}
		}
	}
}

// The stream that comes after the limit of a user replaces the oldest one, which gets replaced and ends.
func TestFollowEventsReplaced(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	oldest := openEvents(t, e, o.Owner)
	for range live.MaxStreamsPerUser {
		openEvents(t, e, o.Owner)
	}
	if got := readBlock(t, oldest); got != replacedBlock {
		t.Fatalf("block = %q, want replaced", got)
	}
	requireEnd(t, oldest)
}

// The access check at each ping keeps the stream of a user whose access did not change.
func TestFollowEventsOfAClientUserSurvivesTwoPings(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	c := e.newClient(o, "Client", true, 1)
	br := openEvents(t, e, e.newClientUser(o, c.ID, "client@example.com"))
	for i := range 2 {
		e.Clock.Advance(pingInterval)
		if got := readBlock(t, br); got != pingBlock {
			t.Fatalf("ping %d = %q", i+1, got)
		}
	}
}

// The check does not extend the session: only Authenticate touches it.
func TestFollowEventsDoesNotExtendTheSession(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	br := openEvents(t, e, o.Owner)
	expires := func() time.Time {
		var at time.Time
		if err := e.Deps.Pool.QueryRow(context.Background(), `SELECT expires_at FROM sessions WHERE user_id = $1`, o.Owner.ID).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}
	before := expires()
	e.Clock.Advance(pingInterval)
	if got := readBlock(t, br); got != pingBlock {
		t.Fatalf("block = %q", got)
	}
	if after := expires(); !after.Equal(before) {
		t.Fatalf("expires_at moved from %s to %s", before, after)
	}
}

// The stream ends within one ping after the access ends, with no event: the browser connects
// again and gets 401.
func TestFollowEventsEndsWhenTheAccessEnds(t *testing.T) {
	ctx := context.Background()
	cases := map[string]func(e *testEnv, o testOrg, c uuid.UUID, u testUser){
		"logout": func(e *testEnv, _ testOrg, _ uuid.UUID, u testUser) {
			if err := e.Deps.Auth.Logout(ctx, u.Cookie); err != nil {
				t.Fatal(err)
			}
		},
		"removal": func(e *testEnv, o testOrg, c uuid.UUID, _ testUser) {
			if err := e.Deps.Auth.RemoveClientAccess(ctx, o.Owner.Principal, c); err != nil {
				t.Fatal(err)
			}
		},
		"password change in another session": func(e *testEnv, _ testOrg, _ uuid.UUID, u testUser) {
			other := e.login(u.Email)
			if err := e.Deps.Auth.ChangePassword(ctx, u.Principal, other, testPassword, "another horse battery"); err != nil {
				t.Fatal(err)
			}
		},
		"disabled user": func(e *testEnv, _ testOrg, _ uuid.UUID, u testUser) {
			if _, err := e.Deps.Pool.Exec(ctx, `UPDATE users SET disabled_at = now() WHERE id = $1`, u.ID); err != nil {
				t.Fatal(err)
			}
		},
		"other client": func(e *testEnv, o testOrg, _ uuid.UUID, u testUser) {
			other := e.newClient(o, "Other", true, 1)
			if _, err := e.Deps.Pool.Exec(ctx, `UPDATE users SET client_id = $1 WHERE id = $2`, other.ID, u.ID); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, end := range cases {
		t.Run(name, func(t *testing.T) {
			e := newTestEnv(t)
			o := e.newOrg("Org One", 10, true, true)
			c := e.newClient(o, "Client", true, 1)
			u := e.newClientUser(o, c.ID, "client@example.com")
			br := openEvents(t, e, u)
			end(e, o, c.ID, u)
			e.Clock.Advance(pingInterval)
			requireEnd(t, br)
		})
	}
}

// A database error at a ping does not end the stream: a short outage must not close every browser.
func TestFollowEventsSurvivesADatabaseError(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	br := openEvents(t, e, o.Owner)
	e.Deps.Pool.Close()
	for i := range 2 {
		e.Clock.Advance(pingInterval)
		if got := readBlock(t, br); got != pingBlock {
			t.Fatalf("ping %d = %q", i+1, got)
		}
	}
}

// The stop of the server ends the stream with no event, as followPairing.
func TestFollowEventsEndsWhenTheServerStops(t *testing.T) {
	e := newTestEnv(t)
	o := e.newOrg("Org One", 10, true, true)
	stopping := make(chan struct{})
	srv := httptest.NewUnstartedServer(e.Handler)
	srv.Config.BaseContext = func(net.Listener) context.Context {
		return WithShutdownSignal(context.Background(), stopping)
	}
	srv.Start()
	t.Cleanup(srv.Close)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+eventsPath, nil)
	withCookie(o.Owner.Cookie)(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	if got := readBlock(t, br); got != changedBlock {
		t.Fatalf("first block = %q", got)
	}
	close(stopping)
	requireEnd(t, br)
}

// A client that does not read fills the buffers of the connection. The write limit ends the stream.
func TestFollowEventsOfAClientThatDoesNotRead(t *testing.T) {
	l := live.New()
	org, client := uuid.New(), uuid.New()
	returned := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(returned)
		sub := l.Subscribe(tenancy.Principal{UserID: uuid.New(), OrganizationID: org, Role: tenancy.RoleClient, ClientID: &client})
		defer sub.Close()
		sse, err := startSSE(w)
		if err != nil {
			return
		}
		sse.limit = 200 * time.Millisecond
		streamEvents(r.Context(), sse, sub, func(context.Context) bool { return true }, testutil.NewFakeClock(testStart))
	}))
	t.Cleanup(srv.Close)
	conn := dialWithoutReading(t, srv.URL, "/")
	defer conn.Close()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-returned:
			return
		case <-deadline:
			t.Fatal("the stream did not end while the client did not read")
		default:
			l.Publish(org, client)
		}
	}
}
