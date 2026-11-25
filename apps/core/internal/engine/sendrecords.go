package engine

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"time"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// SendRecords is the database access of send_records: the idempotency keys and the repeat guard.
// Times come from now, so tests can move the clock. The rule of BulkStore applies: one
// connection, so close rows before the next query and use the transaction while one is open.
type SendRecords struct {
	db  *sql.DB
	now func() time.Time
}

func NewSendRecords(db *sql.DB, now func() time.Time) *SendRecords {
	return &SendRecords{db: db, now: now}
}

// SingleCheck is the answer of BeginSingle: either a new PENDING row, or a replay.
type SingleCheck struct {
	RecordID  int64 // the new PENDING row; 0 on a replay
	Replayed  bool
	MessageID string // on a replay: the stored provider message id
	// PendingRecordID is set with the SEND_IN_PROGRESS refusal: the PENDING row of the key.
	PendingRecordID int64
}

// checkKeyFormat accepts a nil key (no key) or 1 to 255 printable ASCII characters.
func checkKeyFormat(key *string) error {
	if key == nil {
		return nil
	}
	valid := len(*key) >= 1 && len(*key) <= 255
	for i := 0; valid && i < len(*key); i++ {
		valid = (*key)[i] >= 0x21 && (*key)[i] <= 0x7e
	}
	if !valid {
		return calyxerr.New(codes.InvalidArgument, "The idempotency key must have 1 to 255 printable ASCII characters.")
	}
	return nil
}

// BeginSingle applies the rules for a known key, then the repeat guard, then inserts a PENDING
// row. It is one transaction on the one connection, so two equal requests cannot both pass.
// A refusal is a *calyxerr.Error with a reason.
func (s *SendRecords) BeginSingle(ctx context.Context, instanceID string, key *string, fingerprint []byte) (SingleCheck, error) {
	now := s.now()
	var check SingleCheck
	err := inTx(ctx, s.db, func(tx *sql.Tx) error {
		if key != nil {
			row, err := knownKey(ctx, tx, instanceID, *key, now)
			if err != nil {
				return err
			}
			if row != nil {
				check, err = knownSingleAnswer(row, fingerprint)
				return err
			}
		}
		if err := repeatGuard(ctx, tx, instanceID, fingerprint, now); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO send_records (instance_id, kind, idempotency_key, fingerprint, status, created_at)
			VALUES (?, 'SINGLE', ?, ?, 'PENDING', ?)`, instanceID, key, fingerprint, now.UnixMilli())
		if err != nil {
			return err
		}
		check.RecordID, err = result.LastInsertId()
		return err
	})
	if err != nil {
		// Only the SEND_IN_PROGRESS refusal keeps a field: the id of the pending row.
		return SingleCheck{PendingRecordID: check.PendingRecordID}, err
	}
	return check, nil
}

// LookupSingle gives the answer of BeginSingle for a known key. It writes no record. If the key
// is not known, or if err is not nil, known is false. It runs in a transaction, because knownKey
// clears the key of a row that is KeyWindow old.
func (s *SendRecords) LookupSingle(ctx context.Context, instanceID, key string, fingerprint []byte) (check SingleCheck, known bool, err error) {
	now := s.now()
	err = inTx(ctx, s.db, func(tx *sql.Tx) error {
		row, err := knownKey(ctx, tx, instanceID, key, now)
		if err != nil || row == nil {
			return err
		}
		known = true
		check, err = knownSingleAnswer(row, fingerprint)
		return err
	})
	if err != nil {
		// A refusal is an error, so known is false.
		return SingleCheck{PendingRecordID: check.PendingRecordID}, false, err
	}
	return check, known, nil
}

// knownSingleAnswer turns the row of a known key into the answer for a single send.
func knownSingleAnswer(row *keyRow, fingerprint []byte) (SingleCheck, error) {
	if row.kind != "SINGLE" || !bytes.Equal(row.fingerprint, fingerprint) {
		return SingleCheck{}, keyConflict()
	}
	switch row.status {
	case "PENDING":
		return SingleCheck{PendingRecordID: row.id}, calyxerr.WithReason(codes.Aborted, corev1.ErrorReason_ERROR_REASON_SEND_IN_PROGRESS, nil,
			"A send with this idempotency key is in progress.")
	case "UNKNOWN":
		return SingleCheck{}, calyxerr.WithReason(codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_SEND_OUTCOME_UNKNOWN,
			map[string]string{"at": formatAt(row.finishedAt.Int64)},
			"The outcome of the send with this idempotency key is unknown. Check the chat. To send the message again, use a new key after %s.",
			formatAt(row.finishedAt.Int64+RepeatWindow.Milliseconds()))
	default: // SENT
		return SingleCheck{Replayed: true, MessageID: row.resultID.String}, nil
	}
}

// MarkSent records that the provider accepted the message.
func (s *SendRecords) MarkSent(ctx context.Context, recordID int64, messageID string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE send_records SET status = 'SENT', result_id = ?, finished_at = ? WHERE id = ?",
		messageID, s.now().UnixMilli(), recordID)
	return err
}

// MarkUnknown records that nobody knows whether the provider got the message.
func (s *SendRecords) MarkUnknown(ctx context.Context, recordID int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE send_records SET status = 'UNKNOWN', finished_at = ? WHERE id = ?",
		s.now().UnixMilli(), recordID)
	return err
}

// MarkNotSent deletes the row, so a retry with the same key or the same content can send.
func (s *SendRecords) MarkNotSent(ctx context.Context, recordID int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM send_records WHERE id = ?", recordID)
	return err
}

// Recover turns the PENDING rows created before olderThan into UNKNOWN: their send was in
// progress when the process stopped, or its outcome write failed.
func (s *SendRecords) Recover(ctx context.Context, olderThan time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE send_records SET status = 'UNKNOWN', finished_at = ?
		WHERE status = 'PENDING' AND created_at < ?`, s.now().UnixMilli(), olderThan.UnixMilli())
	return err
}

// Sweep deletes the finished rows that are KeyWindow old. Such a row is already ignored by the
// key rules and by the repeat guard.
func (s *SendRecords) Sweep(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM send_records
		WHERE created_at <= ? AND status IN ('SENT', 'UNKNOWN', 'CREATED')`, s.now().Add(-KeyWindow).UnixMilli())
	return err
}

// checkBulkKey runs inside the transaction of BulkStore.Create. It returns the job id of a
// replay, "" when the key is not known, or a conflict.
func checkBulkKey(ctx context.Context, tx *sql.Tx, instanceID, key string, fingerprint []byte, now time.Time) (string, error) {
	row, err := knownKey(ctx, tx, instanceID, key, now)
	if err != nil || row == nil {
		return "", err
	}
	if row.kind != "BULK" || !bytes.Equal(row.fingerprint, fingerprint) {
		return "", keyConflict()
	}
	return row.resultID.String, nil
}

// insertBulkKey stores the key of a new bulk job, in the same transaction as the job.
func insertBulkKey(ctx context.Context, tx *sql.Tx, instanceID, key string, fingerprint []byte, jobID string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO send_records
		(instance_id, kind, idempotency_key, fingerprint, status, result_id, created_at, finished_at)
		VALUES (?, 'BULK', ?, ?, 'CREATED', ?, ?, ?)`, instanceID, key, fingerprint, jobID, now.UnixMilli(), now.UnixMilli())
	return err
}

type keyRow struct {
	id           int64
	kind, status string
	fingerprint  []byte
	resultID     sql.NullString
	createdAt    int64
	finishedAt   sql.NullInt64
}

// knownKey returns the row of the key, or nil when the key is not known. A row of KeyWindow
// or more loses its key here, so the unique index accepts a new row before the sweeper runs.
func knownKey(ctx context.Context, tx *sql.Tx, instanceID, key string, now time.Time) (*keyRow, error) {
	var r keyRow
	err := tx.QueryRowContext(ctx, `SELECT id, kind, status, fingerprint, result_id, created_at, finished_at
		FROM send_records WHERE instance_id = ? AND idempotency_key = ?`, instanceID, key).
		Scan(&r.id, &r.kind, &r.status, &r.fingerprint, &r.resultID, &r.createdAt, &r.finishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if r.createdAt <= now.Add(-KeyWindow).UnixMilli() {
		_, err := tx.ExecContext(ctx, "UPDATE send_records SET idempotency_key = NULL WHERE id = ?", r.id)
		return nil, err
	}
	return &r, nil
}

const spamRule = "Calyx does not allow the same message to the same recipient within 10 minutes: it looks like spam."

// repeatGuard refuses a single send when a send with the same fingerprint is PENDING, or went
// out or got an unknown outcome less than RepeatWindow ago.
func repeatGuard(ctx context.Context, tx *sql.Tx, instanceID string, fingerprint []byte, now time.Time) error {
	var status string
	var createdAt int64
	var finishedAt sql.NullInt64
	var resultID sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT status, created_at, finished_at, result_id FROM send_records
		WHERE instance_id = ? AND fingerprint = ? AND kind = 'SINGLE'
			AND (status = 'PENDING' OR finished_at > ?)
		ORDER BY status = 'PENDING' DESC, finished_at DESC LIMIT 1`,
		instanceID, fingerprint, now.Add(-RepeatWindow).UnixMilli()).
		Scan(&status, &createdAt, &finishedAt, &resultID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	reason := corev1.ErrorReason_ERROR_REASON_DUPLICATE_MESSAGE
	switch status {
	case "PENDING":
		return calyxerr.WithReason(codes.AlreadyExists, reason, map[string]string{"at": formatAt(createdAt)},
			"This instance is sending the same message to this recipient now. %s", spamRule)
	case "SENT":
		at := formatAt(finishedAt.Int64)
		return calyxerr.WithReason(codes.AlreadyExists, reason, map[string]string{"at": at, "message_id": resultID.String},
			"This instance sent the same message to this recipient at %s. %s", at, spamRule)
	default: // UNKNOWN
		at := formatAt(finishedAt.Int64)
		return calyxerr.WithReason(codes.AlreadyExists, reason, map[string]string{"at": at},
			"The outcome of the same message to this recipient at %s is unknown. %s", at, spamRule)
	}
}

func keyConflict() error {
	return calyxerr.WithReason(codes.AlreadyExists, corev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT, nil,
		"This idempotency key belongs to another message on this instance. Use a new key for a new message.")
}

// formatAt gives a time of the table (Unix milliseconds) as UTC RFC 3339.
func formatAt(ms int64) string {
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}
