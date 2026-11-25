package instances

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/clock"
	"calyx/backend/internal/core"
)

// PairingUpdate is one update of a pairing session. At most one of PairingCode and QRText is set.
type PairingUpdate struct {
	Status      string
	PairingCode *string
	QRText      *string
	Message     string
}

// PairingEvent is what a follower of a session receives. Kind is "update", "done" or "failed".
type PairingEvent struct {
	Kind             string
	Update           PairingUpdate
	FailCode, Detail string
}

// PairingRun is the core call of a session. It must return when ctx ends.
type PairingRun func(ctx context.Context, onUpdate func(core.Update)) error

// PairingResult is how a session ended. Err is the core's error when FailCode is "pairing_failed"
// (nil when the stream ended without CONNECTED and without an error), or when the codes ended in
// the core ("pairing_expired").
type PairingResult struct {
	Connected bool
	FailCode  string // "", "pairing_expired", "pairing_failed", "pairing_cancelled"
	Detail    string
	Err       *apierr.Error
}

var (
	errPairingExpired   = errors.New("pairing expired")
	errPairingCancelled = errors.New("pairing cancelled")
	errShutdown         = errors.New("shutdown")
)

const (
	followerBuffer = 16
	endWait        = 10 * time.Second
	// keptFor is how long the map keeps a failed session, so a client that comes back late
	// still reads the reason.
	keptFor = 5 * time.Minute
)

type follower struct{ ch chan PairingEvent }

type session struct {
	cancel    context.CancelCauseFunc
	latest    *PairingUpdate // guarded by Pairings.mu
	followers map[*follower]struct{}
	done      chan struct{} // closed after the final event went out
	// The organization and the client of the row. A follow uses them when the row is gone.
	organizationID, clientID uuid.UUID
	// final is set when the session failed: it is then an ended entry, kept until endedAt+keptFor.
	// Guarded by Pairings.mu.
	final   *PairingEvent
	endedAt time.Time
}

// Pairings keeps the pairing sessions, one per instance. A session lives in the backend: it does
// not depend on any follower (a browser), and it has its own context and timeout. A failed session
// stays in the map for keptFor as an ended entry. Start and Follow remove the old entries.
type Pairings struct {
	clk      clock.Clock
	timeout  time.Duration
	mu       sync.Mutex
	sessions map[uuid.UUID]*session
}

func NewPairings(clk clock.Clock, timeout time.Duration) *Pairings {
	return &Pairings{clk: clk, timeout: timeout, sessions: map[uuid.UUID]*session{}}
}

// Start runs a new session in its own goroutine and returns the starter's follower channel.
// ctx limits only that follower, never the session. organizationID and clientID are the values of
// the row, not of the caller: an owner has no client. mode is "qr", "code" or "" (Meta).
// onEnd runs in the session goroutine after run returned and before the final event.
func (p *Pairings) Start(ctx context.Context, instanceID, organizationID, clientID uuid.UUID, mode string, run PairingRun, onEnd func(PairingResult)) (<-chan PairingEvent, error) {
	sctx, cancel := context.WithCancelCause(context.Background())
	s := &session{
		cancel: cancel, followers: map[*follower]struct{}{}, done: make(chan struct{}),
		organizationID: organizationID, clientID: clientID,
	}
	p.mu.Lock()
	p.removeOldLocked()
	if p.runningLocked(instanceID) != nil {
		p.mu.Unlock()
		cancel(nil)
		return nil, apierr.Conflict(apierr.CodePairingInProgress, "A pairing is already running for this instance.")
	}
	p.sessions[instanceID] = s
	timer := p.clk.NewTimer(p.timeout)
	f := p.addFollowerLocked(ctx, s)
	p.mu.Unlock()

	go func() {
		select {
		case <-timer.C():
			cancel(errPairingExpired)
		case <-sctx.Done():
		}
	}()
	go p.runSession(sctx, instanceID, s, mode, run, onEnd, timer)
	return f.ch, nil
}

func (p *Pairings) runSession(ctx context.Context, id uuid.UUID, s *session, mode string, run PairingRun, onEnd func(PairingResult), timer clock.Timer) {
	connected := false
	onUpdate := func(u core.Update) {
		upd := PairingUpdate{Status: u.Status, Message: u.Message}
		if u.PairingValue != "" {
			v := u.PairingValue
			switch mode {
			case "qr":
				upd.QRText = &v
			case "code":
				upd.PairingCode = &v
			}
		}
		connected = u.Status == core.StatusConnected
		p.mu.Lock()
		defer p.mu.Unlock()
		s.latest = &upd
		p.sendLocked(s, PairingEvent{Kind: "update", Update: upd})
	}
	err := run(ctx, onUpdate)
	timer.Stop()
	cause := context.Cause(ctx)
	s.cancel(nil)

	var res PairingResult
	switch {
	case err == nil && connected:
		res = PairingResult{Connected: true}
	case errors.Is(cause, errPairingExpired):
		res = PairingResult{FailCode: apierr.CodePairingExpired, Detail: "The pairing was not finished in time."}
	case errors.Is(err, core.ErrPairingExpired):
		ae, _ := apierr.As(err)
		res = PairingResult{FailCode: apierr.CodePairingExpired, Detail: "The pairing was not finished in time.", Err: ae}
	case errors.Is(cause, errPairingCancelled), errors.Is(cause, errShutdown):
		res = PairingResult{FailCode: apierr.CodePairingCancelled, Detail: "The pairing was cancelled."}
	case err != nil && !errors.Is(err, core.ErrEndedWithoutConnected):
		ae, ok := apierr.As(err)
		if !ok {
			ae = apierr.Internal(err)
		}
		res = PairingResult{FailCode: apierr.CodePairingFailed, Detail: ae.Detail, Err: ae}
	default:
		res = PairingResult{FailCode: apierr.CodePairingFailed, Detail: "The core ended the pairing without connecting."}
	}

	onEnd(res)

	final := PairingEvent{Kind: "done", Update: PairingUpdate{Status: core.StatusConnected}}
	if !res.Connected {
		final = PairingEvent{Kind: "failed", FailCode: res.FailCode, Detail: res.Detail}
	}
	p.mu.Lock()
	// One critical section: no reader sees a session that is neither running nor ended.
	if res.Connected {
		delete(p.sessions, id)
	} else {
		s.latest = nil // no QR and no code stay after the end
		s.final, s.endedAt = &final, p.clk.Now()
	}
	p.sendFinalLocked(s, final)
	for f := range s.followers {
		close(f.ch)
	}
	s.followers = map[*follower]struct{}{}
	p.mu.Unlock()
	close(s.done)
}

// sendLocked sends ev to every follower without blocking. A follower with a full buffer is
// dropped: its channel is closed and it reconnects to read the latest update.
func (p *Pairings) sendLocked(s *session, ev PairingEvent) {
	for f := range s.followers {
		select {
		case f.ch <- ev:
		default:
			delete(s.followers, f)
			close(f.ch)
		}
	}
}

func (p *Pairings) addFollowerLocked(ctx context.Context, s *session) *follower {
	f := &follower{ch: make(chan PairingEvent, followerBuffer)}
	if s.latest != nil {
		f.ch <- PairingEvent{Kind: "update", Update: *s.latest}
	}
	s.followers[f] = struct{}{}
	go func() {
		select {
		case <-ctx.Done():
		case <-s.done:
			return
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		if _, ok := s.followers[f]; ok {
			delete(s.followers, f)
			close(f.ch)
		}
	}()
	return f
}

// Follow subscribes to a running session: the first event is the latest update (when there is
// one), then each new one. The channel closes after the final event, when ctx ends, or when the
// follower is too slow. For a kept failure, ended is true and the channel holds only the final
// event and is closed. ok is false when the map has neither.
func (p *Pairings) Follow(ctx context.Context, instanceID uuid.UUID) (events <-chan PairingEvent, ended, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.removeOldLocked()
	s, found := p.sessions[instanceID]
	if !found {
		return nil, false, false
	}
	if s.final != nil {
		ch := make(chan PairingEvent, 1)
		ch <- *s.final
		close(ch)
		return ch, true, true
	}
	return p.addFollowerLocked(ctx, s).ch, false, true
}

// Owner returns the organization and the client of the row of a running session or a kept failure.
func (p *Pairings) Owner(instanceID uuid.UUID) (organizationID, clientID uuid.UUID, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, found := p.sessions[instanceID]
	if !found || p.oldLocked(s) {
		return uuid.Nil, uuid.Nil, false
	}
	return s.organizationID, s.clientID, true
}

// runningLocked returns the session of the instance when it runs, and nil for none or an ended one.
func (p *Pairings) runningLocked(instanceID uuid.UUID) *session {
	s, ok := p.sessions[instanceID]
	if !ok || s.final != nil {
		return nil
	}
	return s
}

func (p *Pairings) oldLocked(s *session) bool {
	return s.final != nil && !p.clk.Now().Before(s.endedAt.Add(keptFor))
}

// removeOldLocked removes the kept failures older than keptFor. Start and Follow call it, so the
// map needs no goroutine to stay small.
func (p *Pairings) removeOldLocked() {
	for id, s := range p.sessions {
		if p.oldLocked(s) {
			delete(p.sessions, id)
		}
	}
}

// Cancel ends the session with "pairing_cancelled" and waits until it ended (10 s at most).
// It returns false when no session runs.
func (p *Pairings) Cancel(instanceID uuid.UUID) bool {
	p.mu.Lock()
	s := p.runningLocked(instanceID)
	p.mu.Unlock()
	if s == nil {
		return false
	}
	s.cancel(errPairingCancelled)
	waitDone(s.done, time.After(endWait))
	return true
}

func waitDone(done <-chan struct{}, limit <-chan time.Time) {
	select {
	case <-done:
	case <-limit:
	}
}

func (p *Pairings) Latest(instanceID uuid.UUID) (PairingUpdate, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.runningLocked(instanceID)
	if s == nil || s.latest == nil {
		return PairingUpdate{}, false
	}
	return *s.latest, true
}

func (p *Pairings) Running(instanceID uuid.UUID) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.runningLocked(instanceID) != nil
}

// CancelAll cancels every session (shutdown) and waits until all ended (10 s at most).
func (p *Pairings) CancelAll() {
	p.mu.Lock()
	all := make([]*session, 0, len(p.sessions))
	for _, s := range p.sessions {
		if s.final == nil {
			all = append(all, s)
		}
	}
	p.mu.Unlock()
	for _, s := range all {
		s.cancel(errShutdown)
	}
	limit := time.After(endWait)
	for _, s := range all {
		waitDone(s.done, limit)
	}
}

// sendFinalLocked sends the final event to every follower. When a buffer is full, it drops the
// oldest buffered event to make room: the final event must always arrive. Only this goroutine
// sends, so the room stays free.
func (p *Pairings) sendFinalLocked(s *session, ev PairingEvent) {
	for f := range s.followers {
		select {
		case f.ch <- ev:
		default:
			select {
			case <-f.ch:
			default:
			}
			select {
			case f.ch <- ev:
			default:
			}
		}
	}
}
