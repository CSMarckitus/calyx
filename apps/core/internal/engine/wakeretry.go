package engine

import (
	"log/slog"
	"time"

	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

const (
	// A failed wake is tried again at each run, with no growing waits, so an unfinished bulk job
	// can go on later.
	wakeInterval = time.Minute
	// The handshake of whatsmeow has its own timer of 20 s, so one wake takes 40 s at most.
	wakeLimit = 20 * time.Second
)

// StartWakeRetry starts the loop that connects the send-only instances whose last connection
// failed. Without it, nothing wakes them, and the core refuses their sends. Call it once, after
// LoadAllActiveInstances. DestroyAllSessions stops it.
func (o *Orchestrator) StartWakeRetry() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.wakeDone != nil {
		return
	}
	done := make(chan struct{})
	o.wakeDone = done
	go func() {
		defer close(done)
		failed := map[string]bool{}
		for {
			// The wait starts after the end of the last run, so a slow run never overlaps the next.
			select {
			case <-o.life.Done():
				return
			case <-o.wakeAfter(wakeInterval):
			}
			failed = o.wakeAll(failed)
		}
	}()
}

// wakeAll wakes each instance to wake, one after the other. It returns the names whose wake
// failed, so the next run logs only a change.
func (o *Orchestrator) wakeAll(failedBefore map[string]bool) map[string]bool {
	rows, err := listInstancesToWake(o.life, o.db)
	if err != nil {
		if o.life.Err() == nil {
			slog.Error("Could not read the instances to wake.", "err", err)
		}
		return failedBefore
	}
	failed := map[string]bool{}
	for _, row := range rows {
		if !o.stillToWake(row.Name) {
			continue
		}
		// The wake only connects. The outbox continues an unfinished bulk job, with its pacing.
		live, err := o.acquireWithin(row.Name, o.wakeLimit)
		if o.life.Err() != nil {
			if err == nil {
				o.release(live)
			}
			return failed
		}
		if err != nil {
			failed[row.Name] = true
			if !failedBefore[row.Name] {
				slog.Warn("The wake of the instance failed. The core tries again each minute.", "instance", row.Name, "err", err)
			}
			continue
		}
		o.release(live)
		if failedBefore[row.Name] {
			slog.Info("The wake of the instance worked after a failure.", "instance", row.Name)
		}
	}
	return failed
}

// stillToWake reads the row again, because a run can last N times 40 s and a row can change in
// that time (for example a temporary ban gives ERROR). A loaded instance is not asleep: a wake
// would only keep it loaded during an outage. An instance that is being paired gets its session
// from the pairing.
func (o *Orchestrator) stillToWake(name string) bool {
	o.mu.Lock()
	skip := o.live[name] != nil || o.pairing[name]
	o.mu.Unlock()
	if skip {
		return false
	}
	row, found, err := lookupInstance(o.life, o.db, name)
	if err != nil || !found {
		return false
	}
	return row.IsEnabled && row.IsSendOnly && (row.Status == corev1.InstanceStatus_INSTANCE_STATUS_CONNECTING ||
		row.Status == corev1.InstanceStatus_INSTANCE_STATUS_DISCONNECTED)
}
