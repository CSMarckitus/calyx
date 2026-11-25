package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/events"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// Opening and closing a session for every message looks nothing like a person using WhatsApp,
// so a send-only instance stays connected for a while in case more work arrives.
const defaultSendOnlyIdle = 60 * time.Second

const (
	sweepInterval = 10 * time.Minute
	// A PENDING row this old comes from a failed outcome write: no send takes an hour.
	pendingTimeout = time.Hour
)

func newID() string { return uuid.NewString() }

// WebhookInvalidator is told when the webhooks of an instance change (events.Dispatcher).
type WebhookInvalidator interface {
	Invalidate(instanceName string)
}

type OrchestratorOptions struct {
	BuildProvider ProviderFactory
	// How long a send-only instance stays connected after its last task. Default 60 s.
	SendOnlyIdle time.Duration
	// Optional: without it nothing is cached anywhere that needs invalidating.
	Webhooks WebhookInvalidator
	// Picks a random device identity: a number in [0, n). Default rand.IntN; tests fix it.
	Intn func(n int) int
	// The clock of the send records. Default time.Now; tests move it past the time windows.
	Now func() time.Time
	// Gives the recipient rules of a provider by its name. The engine cannot import the providers,
	// so the bootstrap passes them in. Default: refuses every provider.
	NormalizeRecipient func(provider, to string) (string, error)
}

// liveInstance is an instance loaded in memory right now.
type liveInstance struct {
	id, name string
	sendOnly bool
	provider Provider
	outbox   *Outbox
	// Guarded by Orchestrator.mu. Calls using the instance right now; together with the outbox it
	// tells whether it can go to sleep.
	activeTasks int
	sleepTimer  *time.Timer
	// Guarded by Orchestrator.mu. Set by the first close; a second close waits for it instead of
	// closing again.
	closed chan struct{}
}

type loadCall struct {
	done chan struct{}
	err  error
}

// Orchestrator knows every instance and decides when each one is loaded in memory. Everything that
// uses an instance goes through it, so there is a single place where an instance is opened, put
// to sleep, rebuilt or removed.
//
// Lock rule: mu is never held while calling the outbox's Stop or anything of a provider, because
// the outbox goroutine calls back into the orchestrator (OnIdle) and would wait for mu forever.
type Orchestrator struct {
	db      *sql.DB
	store   *BulkStore
	records *SendRecords
	bus     *events.Bus
	opts    OrchestratorOptions
	// Every load uses life, so DestroyAllSessions can end a load that hangs.
	life    context.Context
	endLife context.CancelFunc

	mu      sync.Mutex
	live    map[string]*liveInstance
	loading map[string]*loadCall     // parallel loads of one instance share one load
	closing map[string]chan struct{} // a load waits for the close of the same instance
	pairing map[string]bool
	// Held from the read to the write of an instance configuration (CreateInstance,
	// UpdateInstanceConfig), so the written configuration is always the one that was checked.
	configMu sync.Mutex
	// Last send of each instance id whose outbox was stopped, handed to its next outbox so the
	// minimum gap holds across a rebuild. Lost on a restart of the process, which takes longer
	// than any gap.
	lastSent map[string]time.Time
	// Guarded by mu. Set by StartSendRecords; nil when the sweeper does not run.
	sweeper *sweeper
	// Tests set it to drive the sweeps; nil means a ticker of sweepInterval.
	sweepTick <-chan time.Time
	// The waits of the wake loop. Default time.After; tests replace it.
	wakeAfter func(time.Duration) <-chan time.Time
	// The limit of the connect of a wake. Default wakeLimit; tests make it shorter.
	wakeLimit time.Duration
	// Guarded by mu. Set by StartWakeRetry, closed when its loop ends.
	wakeDone chan struct{}
	// Tests set it to hold a pairing after its CONNECTED write and before its register.
	pairedHook func()
	// Tests set it to hold a removal after its pairing check and before it publishes its close.
	removalHook func()
}

type sweeper struct {
	stop context.CancelFunc
	done chan struct{}
}

func NewOrchestrator(db *sql.DB, store *BulkStore, bus *events.Bus, opts OrchestratorOptions) *Orchestrator {
	if opts.SendOnlyIdle == 0 {
		opts.SendOnlyIdle = defaultSendOnlyIdle
	}
	if opts.Intn == nil {
		opts.Intn = rand.IntN
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.BuildProvider == nil {
		opts.BuildProvider = func(row InstanceRow, hooks ProviderHooks) (Provider, error) {
			return nil, calyxerr.New(codes.FailedPrecondition, "This core cannot run %s instances.", row.Provider)
		}
	}
	if opts.NormalizeRecipient == nil {
		opts.NormalizeRecipient = func(provider, to string) (string, error) {
			return "", calyxerr.New(codes.FailedPrecondition, "This core cannot normalize recipients for %s.", provider)
		}
	}
	life, endLife := context.WithCancel(context.Background())
	return &Orchestrator{
		db: db, store: store, records: NewSendRecords(db, opts.Now), bus: bus, opts: opts,
		life: life, endLife: endLife,
		wakeAfter: time.After, wakeLimit: wakeLimit,
		live:     map[string]*liveInstance{},
		loading:  map[string]*loadCall{},
		closing:  map[string]chan struct{}{},
		pairing:  map[string]bool{},
		lastSent: map[string]time.Time{},
	}
}

// LoadAllActiveInstances connects every enabled, authenticated instance. Send-only instances stay
// asleep unless they have a bulk job to continue. Failures are logged, not returned.
func (o *Orchestrator) LoadAllActiveInstances(ctx context.Context) {
	rows, err := listInstanceRows(ctx, o.db, "WHERE is_enabled = 1 AND connection_status <> 'NEEDS_AUTH'")
	if err != nil {
		slog.Error("Could not read the instances.", "err", err)
		return
	}
	withJobs, err := o.store.InstancesWithUnfinishedJobs(ctx)
	if err != nil {
		slog.Error("Could not read the unfinished bulk jobs.", "err", err)
	}

	var toLoad []string
	for _, row := range rows {
		if !row.IsSendOnly || slices.Contains(withJobs, row.Name) {
			toLoad = append(toLoad, row.Name)
		}
	}
	if len(toLoad) == 0 {
		slog.Info("No instances to load (send-only instances are loaded when used).")
		return
	}

	var wg sync.WaitGroup
	var failed atomic.Int32
	for _, name := range toLoad {
		wg.Add(1)
		go func() {
			defer wg.Done()
			live, err := o.acquire(name)
			if err != nil {
				failed.Add(1)
				slog.Error("Instance could not be loaded.", "instance", name, "err", err)
				return
			}
			o.release(live)
		}()
	}
	wg.Wait()
	slog.Info("Instances loaded.", "online", len(toLoad)-int(failed.Load()), "failed", failed.Load())
}

// StartSendRecords turns every PENDING send record into UNKNOWN, then starts the sweeper. Call it
// once, before any outbox runs: a PENDING row at start is a send the last process left unfinished.
func (o *Orchestrator) StartSendRecords(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.sweeper != nil {
		return errors.New("the send records are already started")
	}
	if err := o.records.Recover(ctx, o.opts.Now()); err != nil {
		return fmt.Errorf("recover the send records: %w", err)
	}
	o.sweep(ctx)

	tick := o.sweepTick
	var ticker *time.Ticker
	if tick == nil {
		ticker = time.NewTicker(sweepInterval)
		tick = ticker.C
	}
	sweepCtx, stop := context.WithCancel(ctx)
	sw := &sweeper{stop: stop, done: make(chan struct{})}
	o.sweeper = sw
	go func() {
		defer close(sw.done)
		if ticker != nil {
			defer ticker.Stop()
		}
		for {
			select {
			case <-sweepCtx.Done():
				return
			case <-tick:
				o.sweep(sweepCtx)
			}
		}
	}()
	return nil
}

// sweep recovers the PENDING rows of failed outcome writes and deletes the rows of KeyWindow.
// An error is only logged: the next sweep tries again.
func (o *Orchestrator) sweep(ctx context.Context) {
	err := o.records.Recover(ctx, o.opts.Now().Add(-pendingTimeout))
	if err == nil {
		err = o.records.Sweep(ctx)
	}
	if err != nil && ctx.Err() == nil {
		slog.Error("Could not sweep the send records.", "err", err)
	}
}

// DestroyAllSessions stops the sweeper and the wake loop, and closes every connection. Credentials
// and unfinished jobs stay in the database. A load, a pairing or a removal that ends after it
// starts registers nothing and deletes nothing. It returns after the removals already in progress.
func (o *Orchestrator) DestroyAllSessions() {
	// First, so the loads that run end soon and a new load fails at its first read.
	o.endLife()

	o.mu.Lock()
	sw := o.sweeper
	o.sweeper = nil
	o.mu.Unlock()
	if sw != nil {
		sw.stop()
		<-sw.done
	}

	// The lifetime ended, so the loop ends after the wake that runs.
	o.mu.Lock()
	wakeDone := o.wakeDone
	o.mu.Unlock()
	if wakeDone != nil {
		<-wakeDone
	}

	o.mu.Lock()
	var loads []*loadCall
	for _, call := range o.loading {
		loads = append(loads, call)
	}
	o.mu.Unlock()
	for _, call := range loads {
		<-call.done
	}

	o.mu.Lock()
	var all []*liveInstance
	for _, live := range o.live {
		all = append(all, live)
	}
	o.mu.Unlock()

	var wg sync.WaitGroup
	for _, live := range all {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o.closeLive(live, false)
		}()
	}
	wg.Wait()

	o.mu.Lock()
	var closings []chan struct{}
	for _, ch := range o.closing {
		closings = append(closings, ch)
	}
	o.mu.Unlock()
	for _, ch := range closings {
		<-ch
	}
}

// RemoveInstance logs the instance out and deletes it with its webhooks and bulk jobs. It goes
// ahead even when the device cannot be unlinked (for example a banned account).
func (o *Orchestrator) RemoveInstance(ctx context.Context, name string) error {
	row, err := findInstance(ctx, o.db, name)
	if err != nil {
		return err
	}
	o.mu.Lock()
	if o.pairing[name] {
		o.mu.Unlock()
		return calyxerr.New(codes.FailedPrecondition, "Instance %s is being paired. Cancel the pairing first.", name)
	}
	live := o.live[name]
	o.mu.Unlock()

	// Unlinking the device needs a connection, so a sleeping instance is woken up.
	if live == nil && row.IsEnabled && row.Status != corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH {
		woken, err := o.acquire(name)
		if err != nil {
			slog.Warn("Could not connect to unlink the session, removing it anyway.", "instance", name, "err", err)
		} else {
			// The task ends on every return: after an early return the instance must still sleep.
			defer o.release(woken)
		}
	}

	if o.removalHook != nil {
		o.removalHook()
	}

	// A rename that committed since the first read gave the row to another name: this removal must
	// not delete it. Under configMu a rename either committed before this read, or it starts after
	// the close below is published and waits for it.
	o.configMu.Lock()
	current, err := findInstance(ctx, o.db, name)
	if err == nil && current.ID != row.ID {
		err = calyxerr.New(codes.NotFound, "Instance with name %s not found.", name)
	}
	if err != nil {
		o.configMu.Unlock()
		return err
	}

	// From here until the row is deleted, a new load of this name waits (as it waits for a
	// close), so no call can open the instance again in the middle of its removal.
	o.mu.Lock()
	// A pairing that started since the first check reads o.closing before it writes the row: it
	// either sees this removal and waits, or this check sees its flag.
	if o.pairing[name] {
		o.mu.Unlock()
		o.configMu.Unlock()
		return calyxerr.New(codes.FailedPrecondition, "Instance %s is being paired. Cancel the pairing first.", name)
	}
	// DestroyAllSessions ends the lifetime before it reads o.closing under mu: it either waits for
	// this removal, or this removal stops before its logout and its DELETE.
	if o.life.Err() != nil {
		o.mu.Unlock()
		o.configMu.Unlock()
		return calyxerr.New(codes.Unavailable, "The core is stopping.")
	}
	previousClose := o.closing[name]
	removed := make(chan struct{})
	o.closing[name] = removed
	// Only a load started before this point is waited for: a later one waits for this removal.
	inFlight := o.loading[name]
	o.mu.Unlock()
	o.configMu.Unlock()
	defer func() {
		o.mu.Lock()
		if o.closing[name] == removed {
			delete(o.closing, name)
		}
		o.mu.Unlock()
		close(removed)
	}()
	if previousClose != nil {
		<-previousClose
	}
	// A load that started before (and may have read the row before a disable) registers what it
	// opened; whatever is live now is what this removal closes.
	if inFlight != nil {
		<-inFlight.done
	}
	o.mu.Lock()
	live = o.live[name]
	if live != nil {
		o.forgetLocked(live)
		live.closed = removed // a close arriving now waits for the removal
	}
	o.mu.Unlock()

	if live != nil {
		live.outbox.Stop()
		if err := live.provider.Logout(ctx); err != nil {
			slog.Warn("Logout failed, removing it anyway.", "instance", name, "err", err)
		}
		_ = live.provider.Destroy(ctx)
	}

	// Credentials, webhooks and bulk jobs go with the instance (ON DELETE CASCADE).
	if _, err := o.db.ExecContext(context.Background(), "DELETE FROM instances WHERE id = ?", row.ID); err != nil {
		return err
	}
	o.mu.Lock()
	delete(o.lastSent, row.ID)
	o.mu.Unlock()
	o.invalidateWebhooks(name)
	slog.Info("Instance removed.", "instance", name)
	return nil
}

// LiveProviders returns the providers loaded right now (the Meta webhook server looks for the
// one of a phone number among them).
func (o *Orchestrator) LiveProviders() []Provider {
	o.mu.Lock()
	defer o.mu.Unlock()
	providers := make([]Provider, 0, len(o.live))
	for _, live := range o.live {
		providers = append(providers, live.provider)
	}
	return providers
}

// acquire returns the instance ready to be used, loading it first if needed, and counts the
// caller as an active task until release. Parallel calls for one instance share one load.
func (o *Orchestrator) acquire(name string) (*liveInstance, error) {
	return o.acquireWithin(name, 0)
}

// acquireWithin is acquire with a limit for the connect of a send-only row (a wake). With no load
// to join, it starts one with this limit. 0 means no limit.
func (o *Orchestrator) acquireWithin(name string, limit time.Duration) (*liveInstance, error) {
	for {
		o.mu.Lock()
		if live, ok := o.live[name]; ok {
			live.activeTasks++
			o.cancelSleepLocked(live)
			o.mu.Unlock()
			return live, nil
		}
		// The pairing registers its own provider: a load now would open a second session of the device.
		if o.pairing[name] {
			o.mu.Unlock()
			return nil, calyxerr.New(codes.FailedPrecondition, "Instance %s is being paired. Try again when the pairing ends.", name)
		}
		call, loading := o.loading[name]
		if !loading {
			call = &loadCall{done: make(chan struct{})}
			o.loading[name] = call
			closing := o.closing[name]
			o.mu.Unlock()
			o.runLoad(call, name, closing, limit)
		} else {
			o.mu.Unlock()
		}
		<-call.done
		if call.err != nil {
			return nil, call.err
		}
		// Loaded and registered: the next turn of the loop takes it (or loads it again in the
		// unlikely case it was closed in between).
	}
}

func (o *Orchestrator) runLoad(call *loadCall, name string, closing chan struct{}, limit time.Duration) {
	// Two connections of the same session at once is exactly what must never happen.
	if closing != nil {
		<-closing
	}
	call.err = o.load(name, limit)
	o.mu.Lock()
	delete(o.loading, name)
	o.mu.Unlock()
	close(call.done)
}

func (o *Orchestrator) waitForLoad(name string) {
	o.mu.Lock()
	call := o.loading[name]
	o.mu.Unlock()
	if call != nil {
		<-call.done
	}
}

func (o *Orchestrator) release(live *liveInstance) {
	o.mu.Lock()
	live.activeTasks--
	o.mu.Unlock()
	o.scheduleSleep(live)
}

// load opens the instance. With a limit, the connect of a send-only row ends after limit. A long-lived
// row keeps the 60 s of its provider, so a change of send_only during a wake cannot leave it closed.
func (o *Orchestrator) load(name string, limit time.Duration) error {
	ctx := o.life // shared by every caller waiting for this load
	row, err := findInstance(ctx, o.db, name)
	if err != nil {
		return err
	}
	if !row.IsEnabled {
		return calyxerr.New(codes.FailedPrecondition, "Instance %s is disabled.", name)
	}
	if row.Status == corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH {
		return calyxerr.New(codes.FailedPrecondition, "Instance %s needs to authenticate again.", name)
	}

	wired := &atomic.Bool{}
	wired.Store(true)
	provider, err := o.opts.BuildProvider(row, o.hooksFor(row, wired))
	if err != nil {
		return err
	}
	if limit > 0 && row.IsSendOnly {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, limit)
		defer cancel()
	}
	if err := provider.Connect(ctx); err != nil {
		switch {
		case o.life.Err() != nil:
			err = calyxerr.New(codes.Unavailable, "The core is stopping.")
		case ctx.Err() != nil:
			err = calyxerr.New(codes.Unavailable, "Instance %s could not connect within %v.", name, limit)
		}
		// A long-lived instance keeps trying to reconnect by itself. Anything else is over.
		keepsTrying := calyxerr.CodeOf(err) == codes.Unavailable && !row.IsSendOnly && o.life.Err() == nil
		if !keepsTrying {
			wired.Store(false)
			_ = provider.Destroy(context.Background())
			return err
		}
		slog.Warn("Not connected yet, it will keep trying.", "instance", name, "err", err)
	}
	return o.register(row, provider)
}

// hooksFor connects what a provider reports to the database and the event bus. Nothing is
// reported until wired is true: during a pairing the stored status must stay NEEDS_AUTH, so a
// crash in the middle does not leave an instance that looks usable.
func (o *Orchestrator) hooksFor(row InstanceRow, wired *atomic.Bool) ProviderHooks {
	return ProviderHooks{
		OnStatus: func(status corev1.InstanceStatus) {
			if !wired.Load() {
				return
			}
			name := StatusName(status)
			_, err := o.db.Exec("UPDATE instances SET connection_status = ?, updated_at = ? WHERE id = ?",
				name, time.Now().UnixMilli(), row.ID)
			if err != nil {
				slog.Debug("Could not save the status.", "instance", row.Name, "status", name, "err", err)
			}
			o.bus.Publish(row.Name, "connection.update", events.ConnectionUpdate{Status: name})
		},
		OnEvent: func(eventType string, payload any) {
			if wired.Load() {
				o.bus.Publish(row.Name, eventType, payload)
			}
		},
	}
}

// register makes provider the live instance of row. It never replaces a live instance: the lost one
// would never be closed. After DestroyAllSessions started, it closes provider and returns
// Unavailable: nothing would close the instance.
func (o *Orchestrator) register(row InstanceRow, provider Provider) error {
	o.mu.Lock()
	lastSent := o.lastSent[row.ID]
	o.mu.Unlock()
	live := &liveInstance{id: row.ID, name: row.Name, sendOnly: row.IsSendOnly, provider: provider}
	live.outbox = NewOutbox(provider, o.store, row.ID, OutboxOptions{
		OnIdle:        func() { o.scheduleSleep(live) },
		OnJobFinished: o.publishJobFinished,
		LastSentAt:    lastSent,
	})
	o.mu.Lock()
	// Under mu: DestroyAllSessions ends the lifetime before it reads o.live under mu, so it either
	// sees this instance or this check sees the end. The outbox did not start, so the provider is
	// all there is to close.
	if o.life.Err() != nil {
		o.mu.Unlock()
		_ = provider.Destroy(context.Background())
		return calyxerr.New(codes.Unavailable, "The core is stopping.")
	}
	if _, taken := o.live[row.Name]; taken {
		o.mu.Unlock()
		// This must never happen.
		slog.Error("A second session of a live instance was opened. The core closes it.", "instance", row.Name)
		_ = provider.Destroy(context.Background())
		return nil
	}
	o.live[row.Name] = live
	o.mu.Unlock()
	live.outbox.Start()
	return nil
}

// reloadIfNeeded brings an instance back after a config change, if it should be in memory.
func (o *Orchestrator) reloadIfNeeded(ctx context.Context, name string) {
	row, err := findInstance(ctx, o.db, name)
	if err != nil || !row.IsEnabled || row.Status == corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH {
		return
	}
	if row.IsSendOnly {
		job, err := o.store.NextUnfinished(ctx, row.ID)
		if err != nil || job == nil {
			return
		}
	}
	live, err := o.acquire(name)
	if err != nil {
		slog.Error("Could not be loaded with its new configuration.", "instance", name, "err", err)
		return
	}
	o.release(live)
}

func (o *Orchestrator) canSleepLocked(live *liveInstance) bool {
	return live.sendOnly && live.activeTasks == 0 && live.outbox.IsIdle() && o.live[live.name] == live
}

func (o *Orchestrator) scheduleSleep(live *liveInstance) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.cancelSleepLocked(live)
	if !o.canSleepLocked(live) {
		return
	}
	live.sleepTimer = time.AfterFunc(o.opts.SendOnlyIdle, func() {
		slog.Debug("Idle send-only instance, closing its connection.", "instance", live.name)
		o.closeLive(live, true)
	})
}

func (o *Orchestrator) cancelSleepLocked(live *liveInstance) {
	if live.sleepTimer != nil {
		live.sleepTimer.Stop()
		live.sleepTimer = nil
	}
}

// forgetLocked takes the instance out of the registry, so nobody else starts using it.
func (o *Orchestrator) forgetLocked(live *liveInstance) {
	o.cancelSleepLocked(live)
	if o.live[live.name] == live {
		delete(o.live, live.name)
	}
}

// closeLive closes the connection but keeps the credentials, so the instance can be loaded
// again later. With onlyIfIdle it does nothing if work arrived since the sleep was scheduled.
func (o *Orchestrator) closeLive(live *liveInstance, onlyIfIdle bool) {
	o.mu.Lock()
	if live.closed != nil {
		// Already being closed by someone else: wait for that close instead.
		closed := live.closed
		o.mu.Unlock()
		if !onlyIfIdle {
			<-closed
		}
		return
	}
	if onlyIfIdle && !o.canSleepLocked(live) {
		o.mu.Unlock()
		return
	}
	o.forgetLocked(live)
	done := make(chan struct{})
	live.closed = done
	previousClose := o.closing[live.name]
	o.closing[live.name] = done
	o.mu.Unlock()
	if previousClose != nil {
		<-previousClose // a removal in progress for the same name
	}

	live.outbox.Stop()
	if err := live.provider.Destroy(context.Background()); err != nil {
		slog.Warn("Error while closing the connection.", "instance", live.name, "err", err)
	}

	o.mu.Lock()
	if last := live.outbox.LastSentAt(); last.After(o.lastSent[live.id]) {
		o.lastSent[live.id] = last
	}
	if o.closing[live.name] == done {
		delete(o.closing, live.name)
	}
	o.mu.Unlock()
	close(done)
}
