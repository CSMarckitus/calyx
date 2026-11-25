package watcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/clock"
	"calyx/backend/internal/config"
	"calyx/backend/internal/core"
	"calyx/backend/internal/database/gen"
	"calyx/backend/internal/instances"
	"calyx/backend/internal/live"
	"calyx/backend/internal/mail"
)

// knownStatuses are the statuses that the instances table accepts.
var knownStatuses = map[string]bool{
	core.StatusConnected: true, core.StatusConnecting: true, core.StatusDisconnected: true,
	core.StatusNeedsAuth: true, core.StatusError: true,
}

// Watcher compares the state of every instance with the core. It keeps no state in memory: the
// alert fields live on the instances rows and the outage fields on the core_status row.
type Watcher struct {
	pool      *pgxpool.Pool
	core      *core.Client
	instances *instances.Service
	mailer    mail.Sender
	clk       clock.Clock
	cfg       config.Config
	live      *live.Live
	log       *slog.Logger

	// signal asks Run for a round that counts no failure. A signal that waits joins the next one.
	signal chan struct{}

	// mu makes the comparisons of Round and CheckInstance run one at a time. Mails and recovery
	// run after the unlock, so a slow mail server never delays a check.
	mu sync.Mutex
}

// New returns a Watcher. A nil l is allowed: the watcher then reports no health and no changes.
func New(pool *pgxpool.Pool, c *core.Client, inst *instances.Service, mailer mail.Sender, clk clock.Clock, cfg config.Config, l *live.Live, log *slog.Logger) *Watcher {
	if log == nil {
		log = slog.Default()
	}
	return &Watcher{
		pool: pool, core: c, instances: inst, mailer: mailer, clk: clk, cfg: cfg, live: l, log: log,
		signal: make(chan struct{}, 1),
	}
}

// SignalRound asks Run for a round now. The round counts no failure of the core, sends the due
// mails and does not move the timer.
func (w *Watcher) SignalRound() {
	select {
	case w.signal <- struct{}{}:
	default:
	}
}

// Run does one round at once, then one round every StateCheckInterval, and one round for each
// signal, until ctx ends. Only Run starts rounds, so two rounds never send the same mail.
func (w *Watcher) Run(ctx context.Context) {
	w.logRound(ctx, w.Round(ctx))
	t := w.clk.NewTimer(w.cfg.StateCheckInterval)
	defer func() { t.Stop() }()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C():
			w.logRound(ctx, w.Round(ctx))
			t = w.clk.NewTimer(w.cfg.StateCheckInterval)
		case <-w.signal:
			w.logRound(ctx, w.signalRound(ctx))
		}
	}
}

func (w *Watcher) logRound(ctx context.Context, err error) {
	if err != nil && ctx.Err() == nil {
		w.log.Warn("The state check failed.", "error", err)
	}
}

// Round is the round of the timer: a comparison that counts a failure of the core, then the
// owner mails and the recovery. Run calls it; tests call it directly, one at a time. A mail that
// fails never makes it return an error.
func (w *Watcher) Round(ctx context.Context) error {
	list, outage, err := w.compare(ctx, true)
	if outage != nil {
		w.mailOperator(ctx, *outage)
	}
	if err != nil {
		return err
	}
	w.sendMails(ctx)
	// Recover can wait 60 s for one row, so it runs after the unlock.
	if err := w.instances.Recover(ctx, list); err != nil {
		w.log.Warn("Cannot recover interrupted instances.", "error", err)
		return fmt.Errorf("recover: %w", err)
	}
	return nil
}

// signalRound is the round of a signal: a comparison that counts no failure, then the owner mails.
func (w *Watcher) signalRound(ctx context.Context) error {
	if _, _, err := w.compare(ctx, false); err != nil {
		return err
	}
	w.sendMails(ctx)
	return nil
}

// compare asks the core, and stores the statuses and the alerts. When counted is true, a failure
// of the core is counted; the returned row is then the outage for the operator mail.
func (w *Watcher) compare(ctx context.Context, counted bool) ([]core.InstanceInfo, *gen.RecordCoreCheckFailedRow, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	q := gen.New(w.pool)
	now := w.clk.Now()

	// Read the rows before the core call: a row created in between is not in this list, so it is
	// not mistaken for a missing instance.
	rows, err := q.ListWatchedInstances(ctx, w.cfg.CoreID)
	if err != nil {
		return nil, nil, fmt.Errorf("list watched instances: %w", err)
	}

	// Step 1: ask the core.
	link := w.live.LinkNumber()
	list, err := w.core.ListInstances(ctx)
	if err != nil {
		err = fmt.Errorf("list instances: %w", err)
		if ctx.Err() != nil || !counted {
			return nil, nil, err // a stop is not a core failure
		}
		st, dbErr := q.RecordCoreCheckFailed(ctx, gen.RecordCoreCheckFailedParams{CoreID: w.cfg.CoreID, Now: now})
		if dbErr != nil {
			w.log.Warn("Cannot record the failed core check.", "error", dbErr)
			return nil, nil, err
		}
		if st.UnreachableSince != nil {
			w.live.SetUnreachable()
		}
		return nil, &st, err
	}
	if err := q.RecordCoreCheckOK(ctx, gen.RecordCoreCheckOKParams{CoreID: w.cfg.CoreID, Now: now}); err != nil {
		return nil, nil, fmt.Errorf("record core check: %w", err)
	}

	// Step 2: store the observed statuses.
	known := make(map[string]core.InstanceInfo, len(list))
	for _, in := range list {
		known[in.CoreName] = in
	}
	before := make(map[uuid.UUID]gen.Instance, len(rows))
	for _, row := range rows {
		before[row.ID] = row
		info, present := known[row.CoreName]
		switch {
		case row.State == "active" && present:
			err = w.observe(ctx, q, row.ID, info.Status)
			if err == nil {
				err = syncIdentity(ctx, q, row, info)
			}
		case row.State == "active":
			_, err = q.MarkInstanceMissing(ctx, row.ID)
		case row.State == "missing" && present && knownStatuses[info.Status]:
			_, err = q.MarkInstanceReappeared(ctx, gen.MarkInstanceReappearedParams{ID: row.ID, Status: info.Status, Now: now})
		}
		if err != nil {
			return nil, nil, fmt.Errorf("store the status: %w", err)
		}
	}

	// Step 3: apply the alert rules to the new state.
	rows, err = q.ListWatchedInstances(ctx, w.cfg.CoreID)
	if err != nil {
		return nil, nil, fmt.Errorf("list watched instances: %w", err)
	}
	for _, row := range rows {
		old, ok := before[row.ID]
		if !ok {
			old = row
		}
		if _, err := w.evaluate(ctx, q, row, old); err != nil {
			return nil, nil, fmt.Errorf("alert rules: %w", err)
		}
	}
	w.live.RoundOK(link)
	return list, nil, nil
}

// CheckInstance checks one instance at once: after a send that failed because of its status,
// or after an event of the core. It does nothing for a row that is not active or missing
// (a row in creating belongs to its pairing session), and when the core cannot be reached (the
// next round tries again). It never touches core_status and sends no mail: when it opens an alert
// that is due, it signals Run.
func (w *Watcher) CheckInstance(ctx context.Context, instanceID uuid.UUID) {
	if w.checkInstance(ctx, instanceID) && w.cfg.MailConfigured() {
		w.SignalRound()
	}
}

// checkInstance returns true when the alert of the row is due.
func (w *Watcher) checkInstance(ctx context.Context, instanceID uuid.UUID) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	q := gen.New(w.pool)
	old, err := q.GetInstanceByID(ctx, instanceID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			w.log.Warn("Cannot read the instance to check.", "instance_id", instanceID, "error", err)
		}
		return false
	}
	switch old.State {
	case "active":
		status, err := w.core.GetInstanceStatus(ctx, old.CoreName)
		if ae, ok := apierr.As(err); ok && ae.Code == apierr.CodeNotFound {
			_, err = q.MarkInstanceMissing(ctx, old.ID)
		} else if err != nil {
			w.log.Debug("Cannot check the instance now.", "instance_id", instanceID, "error", err)
			return false
		} else {
			err = w.observe(ctx, q, old.ID, status)
		}
		if err != nil {
			w.log.Warn("Cannot store the status of the instance.", "instance_id", instanceID, "error", err)
			return false
		}
	case "missing":
	default:
		return false
	}
	row, err := q.GetInstanceByID(ctx, instanceID)
	if err != nil {
		return false
	}
	due, err := w.evaluate(ctx, q, row, old)
	if err != nil {
		w.log.Warn("Cannot apply the alert rules.", "instance_id", instanceID, "error", err)
		return false
	}
	return due
}

// observe stores a status that the core reported. An unknown status is logged and not stored.
func (w *Watcher) observe(ctx context.Context, q *gen.Queries, id uuid.UUID, status string) error {
	if !knownStatuses[status] {
		w.log.Warn("The core reported an unknown status.", "instance_id", id, "status", status)
		return nil
	}
	return q.SetObservedStatus(ctx, gen.SetObservedStatusParams{ID: id, Status: status, Now: w.clk.Now()})
}

// syncIdentity copies the device identity and the locale of a WhatsApp Web instance from the core,
// which is their source. It repairs a write that the end of a pairing missed, and the new identity
// that the core stores at the start of a re-pair that was then abandoned.
func syncIdentity(ctx context.Context, q *gen.Queries, row gen.Instance, info core.InstanceInfo) error {
	if row.Provider != "WHATSAPP_WEB" || (text(row.DeviceIdentity) == info.DeviceIdentity && text(row.Locale) == info.Locale) {
		return nil
	}
	return q.SetInstanceIdentity(ctx, gen.SetInstanceIdentityParams{
		ID: row.ID, DeviceIdentity: nilIfEmpty(info.DeviceIdentity), Locale: nilIfEmpty(info.Locale),
	})
}

func text(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// mailOperator mails the operator once per outage, from the core_status row of a counted
// round that failed.
func (w *Watcher) mailOperator(ctx context.Context, st gen.RecordCoreCheckFailedRow) {
	if st.UnreachableSince == nil || st.AlertedAt != nil || !w.cfg.MailConfigured() {
		return
	}
	now := w.clk.Now()
	msg, err := mail.Render("alert_core_unreachable", mail.TemplateData{
		To: w.cfg.OperatorEmail, CoreID: w.cfg.CoreID, Since: *st.UnreachableSince, PublicURL: w.cfg.PublicURL.String(),
	})
	if err == nil {
		err = w.mailer.Send(ctx, msg)
	}
	if err != nil {
		w.log.Warn("Cannot send the core outage mail.", "error", err)
		return
	}
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), saveTimeout)
	defer cancel()
	if err := gen.New(w.pool).MarkCoreAlertSent(saveCtx, gen.MarkCoreAlertSentParams{CoreID: w.cfg.CoreID, Now: now}); err != nil {
		w.log.Warn("Cannot record the core outage mail.", "error", err)
	}
}
