package engine

import (
	"context"
	"database/sql"
	"errors"

	"github.com/mattn/go-sqlite3"
	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
)

// Database access of instances and webhooks, used only by the Orchestrator.

const instanceSelect = `SELECT id, name, provider, is_enabled, is_used_only_for_send_messages,
	connection_status, COALESCE(device_jid, ''), COALESCE(provider_config, ''),
	COALESCE(device_identity, ''), COALESCE(locale, '') FROM instances`

func scanInstance(row interface{ Scan(...any) error }) (InstanceRow, error) {
	var r InstanceRow
	var status string
	err := row.Scan(&r.ID, &r.Name, &r.Provider, &r.IsEnabled, &r.IsSendOnly, &status, &r.DeviceJID, &r.ProviderConfig,
		&r.DeviceIdentity, &r.Locale)
	r.Status = StatusFromName(status)
	return r, err
}

// findInstance returns NotFound when there is no instance with that name.
func findInstance(ctx context.Context, db *sql.DB, name string) (InstanceRow, error) {
	row, err := scanInstance(db.QueryRowContext(ctx, instanceSelect+" WHERE name = ?", name))
	if errors.Is(err, sql.ErrNoRows) {
		return row, calyxerr.New(codes.NotFound, "Instance with name %s not found.", name)
	}
	return row, err
}

// lookupInstance is findInstance without the error when it does not exist (found is false).
func lookupInstance(ctx context.Context, db *sql.DB, name string) (row InstanceRow, found bool, err error) {
	row, err = scanInstance(db.QueryRowContext(ctx, instanceSelect+" WHERE name = ?", name))
	if errors.Is(err, sql.ErrNoRows) {
		return row, false, nil
	}
	return row, err == nil, err
}

func listInstanceRows(ctx context.Context, db *sql.DB, where string, args ...any) ([]InstanceRow, error) {
	rows, err := db.QueryContext(ctx, instanceSelect+" "+where+" ORDER BY created_at, rowid", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var found []InstanceRow
	for rows.Next() {
		r, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		found = append(found, r)
	}
	return found, rows.Err()
}

type storedWebhook struct {
	InstanceID string
	WebhookSummary
}

func listWebhookRows(ctx context.Context, db *sql.DB) ([]storedWebhook, error) {
	rows, err := db.QueryContext(ctx, "SELECT instance_id, url, event_type, is_active, secret IS NOT NULL AND secret <> '' FROM webhooks ORDER BY rowid")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var found []storedWebhook
	for rows.Next() {
		var w storedWebhook
		if err := rows.Scan(&w.InstanceID, &w.URL, &w.Event, &w.IsActive, &w.HasSecret); err != nil {
			return nil, err
		}
		found = append(found, w)
	}
	return found, rows.Err()
}

// storedWebhookInputs returns the URL and the event of each webhook of an instance, for the checks.
// It never reads a secret.
func storedWebhookInputs(ctx context.Context, db *sql.DB, instanceID string) ([]WebhookInput, error) {
	stored, err := listWebhookRows(ctx, db)
	if err != nil {
		return nil, err
	}
	var found []WebhookInput
	for _, w := range stored {
		if w.InstanceID == instanceID {
			found = append(found, WebhookInput{URL: w.URL, Event: w.Event})
		}
	}
	return found, nil
}

func replaceWebhooks(ctx context.Context, tx *sql.Tx, instanceID string, webhooks []WebhookInput) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM webhooks WHERE instance_id = ?", instanceID); err != nil {
		return err
	}
	for _, w := range webhooks {
		var secret any
		if w.Secret != "" {
			secret = w.Secret
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO webhooks (id, instance_id, event_type, url, secret) VALUES (?, ?, ?, ?, ?)",
			newID(), instanceID, w.Event, w.URL, secret)
		if err != nil {
			return err
		}
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var sqliteErr sqlite3.Error
	return errors.As(err, &sqliteErr) && sqliteErr.ExtendedCode == sqlite3.ErrConstraintUnique
}

func inTx(ctx context.Context, db *sql.DB, fn func(tx *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no effect after Commit
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// listInstancesToWake returns the send-only instances that should be connected but are not.
// NEEDS_AUTH and ERROR need a person, so a wake never tries them.
func listInstancesToWake(ctx context.Context, db *sql.DB) ([]InstanceRow, error) {
	return listInstanceRows(ctx, db, `WHERE is_enabled = 1 AND is_used_only_for_send_messages = 1
		AND connection_status IN ('CONNECTING', 'DISCONNECTED')`)
}
