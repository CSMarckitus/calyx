// Package live holds the health of the core and a hub that tells each stream "changed". It has no
// database and no HTTP, so instances, watcher and httpapi can all import it.
package live

import (
	"sync"

	"github.com/google/uuid"

	"calyx/backend/internal/tenancy"
)

// MaxStreamsPerUser is the number of streams that one user can keep open.
const MaxStreamsPerUser = 10

// The values of the core health that the backend reports.
const (
	CoreOK          = "ok"          // the last round of the current link succeeded
	CoreUnreachable = "unreachable" // the link is down or a round failed
	CoreUnknown     = "unknown"     // no round of the current link finished yet
)

type linkState int

const (
	linkNone linkState = iota // no attempt yet
	linkUp
	linkDown
)

// Live is safe for concurrent use. A nil *Live does nothing, so the tests of other packages need none.
type Live struct {
	mu sync.Mutex

	link              linkState
	linkNumber        uint64
	okNumber          uint64 // the link number of the last round that succeeded; 0: none
	roundsUnreachable bool
	stale             bool
	coreVersion       string // from the last link that came up

	subs map[uuid.UUID][]*Subscription // by user, the oldest first
}

func New() *Live {
	return &Live{subs: map[uuid.UUID][]*Subscription{}}
}

// SetCoreLink records the state of the core link. Each time the link comes up, it gets a new number,
// so that a round that started before cannot give ok. coreVersion counts only when the link comes up:
// while the link is down, the version of the last link stays.
func (l *Live) SetCoreLink(up bool, coreVersion string) {
	if l == nil {
		return
	}
	l.change(func() {
		if up {
			l.link = linkUp
			l.linkNumber++
			l.coreVersion = coreVersion
		} else {
			l.link = linkDown
		}
	})
}

// LinkNumber is read directly before ListInstances, and the round gives it back to RoundOK.
func (l *Live) LinkNumber() uint64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.linkNumber
}

// RoundOK records a round that succeeded. Any success clears the rounds fact, but only a round of
// the current link gives ok, so a round that started before the link came up cannot give ok.
func (l *Live) RoundOK(link uint64) {
	if l == nil {
		return
	}
	l.change(func() {
		l.roundsUnreachable = false
		if link == l.linkNumber {
			l.okNumber = link
		}
	})
}

// SetUnreachable records a counted round that left unreachable_since set.
func (l *Live) SetUnreachable() {
	if l == nil {
		return
	}
	l.change(func() { l.roundsUnreachable = true })
}

// Core is the health of the core: CoreOK, CoreUnreachable or CoreUnknown.
func (l *Live) Core() string {
	if l == nil {
		return CoreUnknown
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.core()
}

// CoreVersion is the version that the core sent with the last link: empty before the first link, and
// empty for an older core that sends no version.
func (l *Live) CoreVersion() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.coreVersion
}

// Stale is status_stale: the stored statuses can be old.
func (l *Live) Stale() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stale
}

func (l *Live) core() string {
	switch {
	case l.link == linkDown || l.roundsUnreachable:
		return CoreUnreachable
	case l.link == linkUp && l.okNumber == l.linkNumber:
		return CoreOK
	default:
		return CoreUnknown
	}
}

// change applies f and publishes to all subscribers once when core or status_stale changed.
func (l *Live) change(f func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	core, stale := l.core(), l.stale
	f()
	switch l.core() {
	case CoreUnreachable:
		l.stale = true
	case CoreOK:
		// Only ok ends stale, so old states never look current.
		l.stale = false
	}
	if l.core() != core || l.stale != stale {
		l.publish(func(tenancy.ScopeFilter) bool { return true })
	}
}

// Publish tells each subscriber that may see the client of the organization that something changed.
func (l *Live) Publish(org, client uuid.UUID) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.publish(func(f tenancy.ScopeFilter) bool {
		return f.Covers(org, client)
	})
}

// PublishAll tells every subscriber that something changed, for example the health.
func (l *Live) PublishAll() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.publish(func(tenancy.ScopeFilter) bool { return true })
}

func (l *Live) publish(sees func(tenancy.ScopeFilter) bool) {
	for _, list := range l.subs {
		for _, s := range list {
			if !sees(s.scope) {
				continue
			}
			// A notification that waits joins this one, so the send never blocks.
			select {
			case s.changed <- struct{}{}:
			default:
			}
		}
	}
}

// Subscription is one stream. Changed has capacity 1. Replaced closes when a newer stream of the same
// user takes its place.
type Subscription struct {
	live     *Live
	user     uuid.UUID
	scope    tenancy.ScopeFilter
	changed  chan struct{}
	replaced chan struct{}
}

// Subscribe adds a stream for p. The stream that comes after the limit replaces the oldest one.
func (l *Live) Subscribe(p tenancy.Principal) *Subscription {
	s := &Subscription{
		live:     l,
		user:     p.UserID,
		scope:    tenancy.Scope(p),
		changed:  make(chan struct{}, 1),
		replaced: make(chan struct{}),
	}
	if l == nil {
		return s
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	list := l.subs[p.UserID]
	list = append(list, s)
	if len(list) > MaxStreamsPerUser {
		close(list[0].replaced)
		list[0] = nil // the replaced stream must not stay reachable
		list = list[1:]
	}
	l.subs[p.UserID] = list
	return s
}

func (s *Subscription) Changed() <-chan struct{}  { return s.changed }
func (s *Subscription) Replaced() <-chan struct{} { return s.replaced }

// Close removes the stream. A second call, or a call after replaced, does nothing.
func (s *Subscription) Close() {
	l := s.live
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	list := l.subs[s.user]
	for i, x := range list {
		if x == s {
			list = append(list[:i:i], list[i+1:]...)
			break
		}
	}
	if len(list) == 0 {
		delete(l.subs, s.user)
	} else {
		l.subs[s.user] = list
	}
}
