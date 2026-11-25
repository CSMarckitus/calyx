package watcher

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"calyx/backend/internal/clock"
	"calyx/backend/internal/core"
	"calyx/backend/internal/database/gen"
	"calyx/backend/internal/live"
)

// headerWait is the longest wait for the headers of the core, which confirm the subscription.
const headerWait = 10 * time.Second

// retryWaits are the waits after the first, the second and each later failure of the core link.
var retryWaits = []time.Duration{time.Second, 2 * time.Second, 5 * time.Second}

var errStreamEnded = errors.New("the core ended the event stream")

// Listener keeps the core link: it subscribes to the connection.update events of the core and
// checks each named instance at once. The events carry only the name; the check asks the core.
type Listener struct {
	w          *Watcher
	core       *core.Client
	live       *live.Live
	clk        clock.Clock
	log        *slog.Logger
	headerWait time.Duration

	backendVersion string
	coreVersion    string // the core version of the last link; only Run uses it

	// pending holds the names that wait for a check. Many events of one instance make one check.
	mu      sync.Mutex
	pending map[string]struct{}
	wake    chan struct{}
}

func NewListener(w *Watcher, c *core.Client, l *live.Live, clk clock.Clock, log *slog.Logger, backendVersion string) *Listener {
	if log == nil {
		log = slog.Default()
	}
	return &Listener{
		w: w, core: c, live: l, clk: clk, log: log, headerWait: headerWait, backendVersion: backendVersion,
		pending: map[string]struct{}{}, wake: make(chan struct{}, 1),
	}
}

// Run keeps the core link until ctx ends. It returns after the check in progress returns.
func (l *Listener) Run(ctx context.Context) {
	done := make(chan struct{})
	go func() { defer close(done); l.work(ctx) }()
	defer func() { <-done }()

	downSince := l.clk.Now() // the start counts as an outage, so the first INFO line has its length
	var lastEvent time.Time
	up, down, failures := false, false, 0
	for {
		err := l.core.SubscribeConnections(ctx, l.headerWait, func(version string) {
			l.logCoreVersion(version)
			l.live.SetCoreLink(true, version)
			l.w.SignalRound() // a round finds the events that the outage lost
			l.logUp(downSince, lastEvent)
			up, down, failures = true, false, 0
		}, func(name string) {
			lastEvent = l.clk.Now()
			l.add(name)
		})
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			err = errStreamEnded
		}
		l.live.SetCoreLink(false, "")
		if !down {
			l.log.Warn("The core link is down.", "error", err)
			if up {
				downSince = l.clk.Now()
			}
		} else {
			l.log.Debug("Cannot open the core link.", "error", err)
		}
		up, down = false, true
		failures++
		t := l.clk.NewTimer(retryWaits[min(failures, len(retryWaits))-1])
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C():
		}
	}
}

// logCoreVersion logs a new core version. The warning comes one time for each change, and never for a
// development build or an unknown version (an empty one), which have no real version.
func (l *Listener) logCoreVersion(version string) {
	if version == l.coreVersion {
		return
	}
	l.log.Info("The core version changed.", "old_version", l.coreVersion, "new_version", version)
	l.coreVersion = version
	if version != l.backendVersion && version != "" && version != "dev" && l.backendVersion != "dev" {
		l.log.Warn("The core version differs from the backend version.", "core_version", version, "backend_version", l.backendVersion)
	}
}

func (l *Listener) logUp(downSince, lastEvent time.Time) {
	now := l.clk.Now()
	attrs := []any{"outage_seconds", seconds(now.Sub(downSince))}
	if !lastEvent.IsZero() {
		attrs = append(attrs, "last_event_age_seconds", seconds(now.Sub(lastEvent)))
	}
	l.log.Info("The core link is up.", attrs...)
}

func seconds(d time.Duration) int64 { return int64(d.Round(time.Second) / time.Second) }

func (l *Listener) add(name string) {
	l.mu.Lock()
	l.pending[name] = struct{}{}
	l.mu.Unlock()
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

// take removes one name from the set. The removal comes before the check, so an event that
// arrives during the check adds the name again and makes one more check.
func (l *Listener) take() (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for name := range l.pending {
		delete(l.pending, name)
		return name, true
	}
	return "", false
}

// work is the one worker: the checks run one at a time, so a burst of events cannot flood the core.
func (l *Listener) work(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-l.wake:
		}
		for ctx.Err() == nil {
			name, ok := l.take()
			if !ok {
				break
			}
			l.check(ctx, name)
		}
	}
}

// check ignores a name that the backend does not know, for example an instance that a client of
// the core created directly.
func (l *Listener) check(ctx context.Context, name string) {
	row, err := gen.New(l.w.pool).GetInstanceByCoreName(ctx, gen.GetInstanceByCoreNameParams{CoreID: l.w.cfg.CoreID, CoreName: name})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) && ctx.Err() == nil {
			l.log.Warn("Cannot read the instance of a core event.", "core_name", name, "error", err)
		}
		return
	}
	l.w.CheckInstance(ctx, row.ID)
}
