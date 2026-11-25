package live_test

import (
	"sync"
	"testing"

	"github.com/google/uuid"

	"calyx/backend/internal/live"
	"calyx/backend/internal/tenancy"
)

var (
	orgID    = uuid.MustParse("00000000-0000-0000-0000-00000000000a")
	clientID = uuid.MustParse("00000000-0000-0000-0000-00000000000c")
	otherID  = uuid.MustParse("00000000-0000-0000-0000-00000000000d")
	foreign  = uuid.MustParse("00000000-0000-0000-0000-0000000000ff")
)

func owner() tenancy.Principal {
	return tenancy.Principal{UserID: uuid.New(), OrganizationID: orgID, Role: tenancy.RoleOwner}
}

func clientUser(c uuid.UUID) tenancy.Principal {
	return tenancy.Principal{UserID: uuid.New(), OrganizationID: orgID, Role: tenancy.RoleClient, ClientID: &c}
}

// pending reports whether a notification waits, and takes it.
func pending(s *live.Subscription) bool {
	select {
	case <-s.Changed():
		return true
	default:
		return false
	}
}

func closed(c <-chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

func TestPublishFiltersByTenant(t *testing.T) {
	l := live.New()
	own := l.Subscribe(owner())
	user := l.Subscribe(clientUser(clientID))
	other := l.Subscribe(clientUser(otherID))
	alien := l.Subscribe(tenancy.Principal{UserID: uuid.New(), OrganizationID: foreign, Role: tenancy.RoleOwner})

	l.Publish(orgID, clientID)

	if !pending(own) {
		t.Fatal("the owner sees every client of its organization")
	}
	if !pending(user) {
		t.Fatal("the client user sees its client")
	}
	if pending(other) {
		t.Fatal("a client user of another client must not see the change")
	}
	if pending(alien) {
		t.Fatal("a foreign tenant must not see the change")
	}
}

func TestTwoChangesJoin(t *testing.T) {
	l := live.New()
	s := l.Subscribe(owner())
	l.Publish(orgID, clientID)
	l.Publish(orgID, otherID)
	if !pending(s) {
		t.Fatal("no notification")
	}
	if pending(s) {
		t.Fatal("two changes gave two notifications")
	}
	if cap(s.Changed()) != 1 {
		t.Fatalf("capacity %d, want 1", cap(s.Changed()))
	}
}

func TestEleventhStreamReplacesTheOldest(t *testing.T) {
	l := live.New()
	p := owner()
	subs := make([]*live.Subscription, 10)
	for i := range subs {
		subs[i] = l.Subscribe(p)
	}
	for _, s := range subs {
		if closed(s.Replaced()) {
			t.Fatal("ten streams must stay open")
		}
	}
	newest := l.Subscribe(p)
	if !closed(subs[0].Replaced()) {
		t.Fatal("the oldest stream did not get replaced")
	}
	for _, s := range subs[1:] {
		if closed(s.Replaced()) {
			t.Fatal("only the oldest stream gets replaced")
		}
	}
	l.Publish(orgID, clientID)
	if pending(subs[0]) {
		t.Fatal("the replaced stream still gets notifications")
	}
	if !pending(newest) {
		t.Fatal("the newest stream gets no notification")
	}
	subs[0].Close() // after replaced: no panic

	// Another user has its own count.
	other := l.Subscribe(owner())
	if closed(subs[1].Replaced()) || closed(other.Replaced()) {
		t.Fatal("the stream of another user replaced a stream")
	}
}

func TestCloseStopsNotificationsAndFreesASlot(t *testing.T) {
	l := live.New()
	p := owner()
	first := l.Subscribe(p)
	first.Close()
	first.Close()
	l.Publish(orgID, clientID)
	if pending(first) {
		t.Fatal("a closed stream gets notifications")
	}
	subs := make([]*live.Subscription, 10)
	for i := range subs {
		subs[i] = l.Subscribe(p)
	}
	if closed(subs[0].Replaced()) {
		t.Fatal("a closed stream still counts")
	}
}

func TestHealthTable(t *testing.T) {
	cases := []struct {
		name  string
		steps func(l *live.Live)
		core  string
		stale bool
	}{
		{"start", func(l *live.Live) {}, "unknown", false},
		{"first attempt failed", func(l *live.Live) { l.SetCoreLink(false, "") }, "unreachable", true},
		{"link up, no round yet", func(l *live.Live) { l.SetCoreLink(true, "") }, "unknown", false},
		{"link up and a round of its number", func(l *live.Live) {
			l.SetCoreLink(true, "")
			l.RoundOK(l.LinkNumber())
		}, "ok", false},
		{"link lost after ok", func(l *live.Live) {
			l.SetCoreLink(true, "")
			l.RoundOK(l.LinkNumber())
			l.SetCoreLink(false, "")
		}, "unreachable", true},
		{"rounds unreachable with the link up", func(l *live.Live) {
			l.SetCoreLink(true, "")
			l.RoundOK(l.LinkNumber())
			l.SetUnreachable()
		}, "unreachable", true},
		{"rounds unreachable at the start", func(l *live.Live) { l.SetUnreachable() }, "unreachable", true},
		{"the link does not clear the rounds", func(l *live.Live) {
			l.SetUnreachable()
			l.SetCoreLink(true, "")
		}, "unreachable", true},
		{"a round clears the rounds", func(l *live.Live) {
			l.SetUnreachable()
			l.SetCoreLink(true, "")
			l.RoundOK(l.LinkNumber())
		}, "ok", false},
		{"a round of an older link clears the rounds, but gives no ok", func(l *live.Live) {
			n := l.LinkNumber()
			l.SetUnreachable()
			l.SetCoreLink(true, "")
			l.RoundOK(n)
		}, "unknown", true},
		{"a round does not bring the link up", func(l *live.Live) {
			l.SetCoreLink(false, "")
			l.RoundOK(l.LinkNumber())
		}, "unreachable", true},
		{"stale stays until ok", func(l *live.Live) {
			l.SetCoreLink(false, "")
			l.SetCoreLink(true, "")
		}, "unknown", true},
		{"a new link needs a new round", func(l *live.Live) {
			l.SetCoreLink(true, "")
			l.RoundOK(l.LinkNumber())
			l.SetCoreLink(false, "")
			l.SetCoreLink(true, "")
		}, "unknown", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := live.New()
			c.steps(l)
			if got := l.Core(); got != c.core {
				t.Fatalf("Core() = %q, want %q", got, c.core)
			}
			if got := l.Stale(); got != c.stale {
				t.Fatalf("Stale() = %v, want %v", got, c.stale)
			}
		})
	}
}

func TestARoundOfAnOlderLinkDoesNotGiveOK(t *testing.T) {
	l := live.New()
	l.SetCoreLink(false, "")
	n := l.LinkNumber() // the round starts before the link comes up
	l.SetCoreLink(true, "")
	l.RoundOK(n)
	if got := l.Core(); got != "unknown" {
		t.Fatalf("Core() = %q, want unknown", got)
	}
	l.RoundOK(l.LinkNumber())
	if got := l.Core(); got != "ok" {
		t.Fatalf("Core() = %q, want ok", got)
	}
}

func TestEachLinkUpGivesANewNumber(t *testing.T) {
	l := live.New()
	a := l.LinkNumber()
	l.SetCoreLink(true, "")
	b := l.LinkNumber()
	l.SetCoreLink(false, "")
	c := l.LinkNumber()
	l.SetCoreLink(true, "")
	d := l.LinkNumber()
	if b == a || c != b || d == c {
		t.Fatalf("numbers %d %d %d %d", a, b, c, d)
	}
}

// The watcher does not call live for a first failure or a failed round of the signal. A round
// that reads the number and then fails must leave the health as it was.
func TestAFailedRoundThatIsNotCountedChangesNothing(t *testing.T) {
	l := live.New()
	l.SetCoreLink(true, "")
	l.RoundOK(l.LinkNumber())
	s := l.Subscribe(owner())
	_ = l.LinkNumber()
	if l.Core() != "ok" || l.Stale() || pending(s) {
		t.Fatalf("Core() = %q, Stale() = %v", l.Core(), l.Stale())
	}

	l.SetUnreachable()
	pending(s)
	_ = l.LinkNumber()
	if l.Core() != "unreachable" || !l.Stale() || pending(s) {
		t.Fatalf("Core() = %q, Stale() = %v", l.Core(), l.Stale())
	}
}

func TestStaleEndsOnlyWithOK(t *testing.T) {
	l := live.New()
	l.SetUnreachable()
	if !l.Stale() {
		t.Fatal("unreachable must set stale")
	}
	l.SetCoreLink(true, "")
	l.RoundOK(0) // an older number
	if !l.Stale() {
		t.Fatal("stale ended without ok")
	}
	l.RoundOK(l.LinkNumber())
	if l.Core() != "ok" || l.Stale() {
		t.Fatalf("Core() = %q, Stale() = %v", l.Core(), l.Stale())
	}
}

func TestOnePublishForEachHealthChange(t *testing.T) {
	l := live.New()
	own := l.Subscribe(owner())
	user := l.Subscribe(clientUser(clientID))
	alien := l.Subscribe(tenancy.Principal{UserID: uuid.New(), OrganizationID: foreign, Role: tenancy.RoleOwner})
	all := []*live.Subscription{own, user, alien}
	expect := func(step string, want bool) {
		t.Helper()
		for i, s := range all {
			if got := pending(s); got != want {
				t.Fatalf("%s: subscriber %d notified = %v, want %v", step, i, got, want)
			}
		}
	}

	l.SetCoreLink(true, "") // unknown to unknown
	expect("link up from the start", false)
	l.RoundOK(l.LinkNumber())
	expect("ok", true)
	l.RoundOK(l.LinkNumber())
	expect("ok again", false)
	l.SetUnreachable()
	expect("unreachable", true)
	l.SetUnreachable()
	expect("unreachable again", false)
	l.SetCoreLink(false, "")
	expect("link down while unreachable", false)
	l.SetCoreLink(true, "")
	expect("link up while the rounds are unreachable", false)
	l.RoundOK(l.LinkNumber())
	expect("ok after the outage", true)
	l.SetCoreLink(true, "") // a new link: ok becomes unknown, stale does not change
	expect("new link", true)
}

func TestNilLiveDoesNothing(t *testing.T) {
	var l *live.Live
	l.SetCoreLink(true, "")
	l.RoundOK(l.LinkNumber())
	l.SetUnreachable()
	l.Publish(orgID, clientID)
	l.PublishAll()
	if l.Core() != "unknown" || l.Stale() {
		t.Fatalf("Core() = %q, Stale() = %v", l.Core(), l.Stale())
	}
	s := l.Subscribe(owner())
	if pending(s) || closed(s.Replaced()) {
		t.Fatal("a nil Live sent something")
	}
	s.Close()
}

func TestConcurrentUse(t *testing.T) {
	l := live.New()
	var wg sync.WaitGroup
	p := owner()
	for i := range 20 {
		wg.Go(func() {
			for j := range 50 {
				s := l.Subscribe(p)
				l.Publish(orgID, clientID)
				switch (i + j) % 4 {
				case 0:
					l.SetCoreLink(j%2 == 0, "")
				case 1:
					l.RoundOK(l.LinkNumber())
				case 2:
					l.SetUnreachable()
				default:
					l.PublishAll()
				}
				_ = l.Core()
				_ = l.Stale()
				pending(s)
				s.Close()
			}
		})
	}
	wg.Wait()
}

func TestTheCoreVersionComesWithTheLinkAndStaysWhileItIsDown(t *testing.T) {
	l := live.New()
	if got := l.CoreVersion(); got != "" {
		t.Fatalf("CoreVersion() before the first link = %q, want empty", got)
	}
	l.SetCoreLink(true, "1.2.3")
	if got := l.CoreVersion(); got != "1.2.3" {
		t.Fatalf("CoreVersion() = %q, want 1.2.3", got)
	}
	l.SetCoreLink(false, "")
	if got := l.CoreVersion(); got != "1.2.3" {
		t.Fatalf("CoreVersion() while the link is down = %q, want 1.2.3", got)
	}
	l.SetCoreLink(true, "") // an older core sends no version
	if got := l.CoreVersion(); got != "" {
		t.Fatalf("CoreVersion() of a core with no version = %q, want empty", got)
	}
}

func TestANilLiveHasNoCoreVersion(t *testing.T) {
	var l *live.Live
	l.SetCoreLink(true, "1.2.3")
	if got := l.CoreVersion(); got != "" {
		t.Fatalf("CoreVersion() = %q, want empty", got)
	}
}
