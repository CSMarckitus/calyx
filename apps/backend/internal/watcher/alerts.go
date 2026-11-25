package watcher

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"calyx/backend/internal/database/gen"
	"calyx/backend/internal/mail"
)

// saveTimeout limits a database write that runs after a mail was sent, even when the round's
// context ended in the meantime. Without it a stop could send a mail and forget it (a second mail
// after the restart).
const saveTimeout = 10 * time.Second

func factsOf(r gen.Instance) Facts {
	return Facts{State: r.State, Enabled: r.Enabled, LastStatus: r.LastStatus}
}

func alertOf(r gen.Instance) Alert {
	a := Alert{OpenedAt: r.AlertOpenedAt, AlertedAt: r.AlertedAt, Attempts: r.AlertAttempts, NextAttemptAt: r.AlertNextAttemptAt}
	if r.AlertKind != nil {
		a.Kind = *r.AlertKind
	}
	return a
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

func sameAlert(a, b Alert) bool {
	return a.Kind == b.Kind && a.Attempts == b.Attempts &&
		sameTime(a.OpenedAt, b.OpenedAt) && sameTime(a.AlertedAt, b.AlertedAt) && sameTime(a.NextAttemptAt, b.NextAttemptAt)
}

func saveParams(row gen.Instance, a Alert) gen.SaveAlertStateParams {
	p := gen.SaveAlertStateParams{
		ID: row.ID, AlertOpenedAt: a.OpenedAt, AlertedAt: a.AlertedAt,
		AlertAttempts: a.Attempts, AlertNextAttemptAt: a.NextAttemptAt,
	}
	if a.Kind != "" {
		k := a.Kind
		p.AlertKind = &k
	}
	return p
}

// evaluate applies the alert rules to one row and saves the alert when it changed. It publishes
// when the state, the status or the alert differs from old, the row read before the writes. It
// says whether a mail is due. Only a database failure is returned.
func (w *Watcher) evaluate(ctx context.Context, q *gen.Queries, row, old gen.Instance) (bool, error) {
	cur := alertOf(row)
	next, due := Evaluate(factsOf(row), cur, w.clk.Now(), w.cfg.AlertGrace)
	if !sameAlert(cur, next) {
		if err := q.SaveAlertState(ctx, saveParams(row, next)); err != nil {
			return false, err
		}
	}
	if row.State != old.State || text(row.LastStatus) != text(old.LastStatus) || !sameAlert(alertOf(old), next) {
		w.live.Publish(row.OrganizationID, row.ClientID)
	}
	return due, nil
}

// sendMails mails the owners of the due alerts, one at a time. It stops at the first failed send
// or failed save: the next round tries again. With mail off, the alerts stay open and visible,
// and no attempt is counted.
func (w *Watcher) sendMails(ctx context.Context) {
	if !w.cfg.MailConfigured() {
		return
	}
	q := gen.New(w.pool)
	for ctx.Err() == nil {
		now := w.clk.Now()
		row, err := q.GetNextDueAlert(ctx, gen.GetNextDueAlertParams{
			CoreID: w.cfg.CoreID, Now: now, DisconnectedBefore: now.Add(-w.cfg.AlertGrace),
		})
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				w.log.Warn("Cannot read the next alert to mail.", "error", err)
			}
			return
		}
		a := alertOf(row)
		res := gen.SaveAlertMailResultParams{
			ID: row.ID, AlertKind: a.Kind, AlertOpenedAt: a.OpenedAt,
			AlertAttempts: a.Attempts, AlertNextAttemptAt: a.NextAttemptAt,
		}
		sendErr := w.mailOwner(ctx, row, a)
		if sendErr != nil {
			w.log.Warn("Cannot send the alert mail.", "instance_id", row.ID, "kind", a.Kind, "error", sendErr)
			res.AlertAttempts++
			retry := now.Add(Backoff(res.AlertAttempts))
			res.AlertNextAttemptAt = &retry
		} else {
			res.AlertedAt = &now
		}
		// A stop cancels the mail, but the save of its result still runs.
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), saveTimeout)
		saved, err := q.SaveAlertMailResult(saveCtx, res)
		cancel()
		if err != nil {
			w.log.Warn("Cannot save the result of the alert mail.", "instance_id", row.ID, "error", err)
			return
		}
		var se sendError
		if errors.As(sendErr, &se) {
			return
		}
		if sendErr == nil && saved == 1 {
			w.live.Publish(row.OrganizationID, row.ClientID)
		}
	}
}

// sendError marks an error of the mail sender, as opposed to an error while the mail is prepared.
type sendError struct{ error }

func (e sendError) Unwrap() error { return e.error }

// mailOwner renders and sends the alert mail of the row's open alert. No owner counts as a
// failed mail.
func (w *Watcher) mailOwner(ctx context.Context, row gen.Instance, a Alert) error {
	rcpt, err := gen.New(w.pool).GetAlertRecipient(ctx, row.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		w.log.Warn("The organization has no active owner to mail.", "instance_id", row.ID)
		return errors.New("no active owner")
	}
	if err != nil {
		return err
	}
	name := ""
	status := "MISSING"
	switch a.Kind {
	case "needs_auth":
		name = "alert_needs_auth_whatsapp_web"
		if row.Provider == "META_CLOUD" {
			name = "alert_needs_auth_meta"
		}
	case "error":
		name = "alert_error"
	case "disconnected":
		name = "alert_disconnected"
	case "missing":
		name = "alert_missing"
	default:
		return errors.New("unknown alert kind")
	}
	if a.Kind != "missing" && row.LastStatus != nil {
		status = *row.LastStatus
	}
	since := w.clk.Now()
	if a.OpenedAt != nil {
		since = *a.OpenedAt
	}
	public := w.cfg.PublicURL.String()
	msg, err := mail.Render(name, mail.TemplateData{
		To: rcpt.OwnerEmail, OrganizationName: rcpt.OrganizationName, ClientName: rcpt.ClientName,
		InstanceName: row.DisplayName, Status: status, Since: since,
		Link: public + "/instances/" + row.ID.String(), APILink: public + "/api/v1/instances/" + row.ID.String(), PublicURL: public,
	})
	if err != nil {
		return err
	}
	if err := w.mailer.Send(ctx, msg); err != nil {
		return sendError{err}
	}
	return nil
}
