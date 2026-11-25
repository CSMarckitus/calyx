package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
)

type JobStatus string

const (
	JobQueued    JobStatus = "QUEUED"
	JobRunning   JobStatus = "RUNNING"
	JobCompleted JobStatus = "COMPLETED"
	JobCancelled JobStatus = "CANCELLED"
	JobFailed    JobStatus = "FAILED"
)

type MessageStatus string

const (
	MessagePending   MessageStatus = "PENDING"
	MessageSending   MessageStatus = "SENDING"
	MessageSent      MessageStatus = "SENT"
	MessageFailed    MessageStatus = "FAILED"
	MessageCancelled MessageStatus = "CANCELLED"
)

type BulkJob struct {
	ID, InstanceID, InstanceName                             string
	Status                                                   JobStatus
	TotalMessages, SentMessages, FailedMessages, EstimatedMs int
	Error                                                    string
	CreatedAt                                                time.Time
	StartedAt, FinishedAt                                    *time.Time
}

type BulkMessage struct {
	ID                    int64
	JobID                 string
	Position              int
	Recipient, Text       string
	DelayMs, PauseAfterMs int
	Status                MessageStatus
}

const insertChunkSize = 500

// BulkStore is all the database access of bulk jobs. Keeping every step in the database instead
// of in memory is what lets a job report its progress, be cancelled and survive a restart.
//
// The database has a single connection: a method must close its rows before running another
// query, and must use the transaction (not s.db) while one is open, or it waits forever.
type BulkStore struct {
	db *sql.DB
}

func NewBulkStore(db *sql.DB) *BulkStore {
	return &BulkStore{db: db}
}

// BulkKey is the idempotency key of a new job. Now comes from the clock of the orchestrator.
type BulkKey struct {
	Key         string
	Fingerprint []byte
	Now         time.Time
}

// Create stores a job and its plan. With a key that is already known, it stores nothing and
// returns the earlier job in its current state, with replayed true.
func (s *BulkStore) Create(ctx context.Context, instanceID string, plan []PlannedMessage, estimatedMs int, key *BulkKey) (*BulkJob, bool, error) {
	id := newID()
	replayedID := ""
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if key != nil {
			jobID, err := checkBulkKey(ctx, tx, instanceID, key.Key, key.Fingerprint, key.Now)
			if err != nil || jobID != "" {
				replayedID = jobID
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO bulk_jobs (id, instance_id, total_messages, estimated_ms, created_at)
			VALUES (?, ?, ?, ?, ?)`, id, instanceID, len(plan), estimatedMs, time.Now().UnixMilli())
		if err != nil {
			return err
		}
		for start := 0; start < len(plan); start += insertChunkSize {
			end := min(start+insertChunkSize, len(plan))
			if err := insertChunk(ctx, tx, id, start, plan[start:end]); err != nil {
				return err
			}
		}
		if key != nil {
			return insertBulkKey(ctx, tx, instanceID, key.Key, key.Fingerprint, id, key.Now)
		}
		return nil
	})
	if err != nil {
		return nil, false, fmt.Errorf("create bulk job: %w", err)
	}
	if replayedID != "" {
		job, err := s.Get(ctx, replayedID) // after the transaction: it holds the one connection
		return job, true, err
	}
	job, err := s.Get(ctx, id)
	return job, false, err
}

// insertChunk inserts the messages with one statement, far faster than one INSERT each.
func insertChunk(ctx context.Context, tx *sql.Tx, jobID string, firstPosition int, chunk []PlannedMessage) error {
	placeholders := make([]string, len(chunk))
	args := make([]any, 0, len(chunk)*6)
	for i, m := range chunk {
		placeholders[i] = "(?, ?, ?, ?, ?, ?)"
		args = append(args, jobID, firstPosition+i, m.Recipient, m.Text, m.DelayMs, m.PauseAfterMs)
	}
	query := "INSERT INTO bulk_job_messages (job_id, position, recipient, text, delay_ms, pause_after_ms) VALUES " +
		strings.Join(placeholders, ", ")
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

// jobSelect joins the instance to return its name, which the gRPC BulkJob message carries.
const jobSelect = `SELECT j.id, j.instance_id, i.name, j.status, j.total_messages, j.sent_messages,
	j.failed_messages, j.estimated_ms, j.error, j.created_at, j.started_at, j.finished_at
	FROM bulk_jobs j JOIN instances i ON i.id = j.instance_id`

func scanJob(row interface{ Scan(...any) error }) (*BulkJob, error) {
	var job BulkJob
	var jobError sql.NullString
	var createdAt int64
	var startedAt, finishedAt sql.NullInt64
	err := row.Scan(&job.ID, &job.InstanceID, &job.InstanceName, &job.Status, &job.TotalMessages, &job.SentMessages,
		&job.FailedMessages, &job.EstimatedMs, &jobError, &createdAt, &startedAt, &finishedAt)
	if err != nil {
		return nil, err
	}
	job.Error = jobError.String
	job.CreatedAt = time.UnixMilli(createdAt)
	job.StartedAt = optionalTime(startedAt)
	job.FinishedAt = optionalTime(finishedAt)
	return &job, nil
}

func optionalTime(ms sql.NullInt64) *time.Time {
	if !ms.Valid {
		return nil
	}
	t := time.UnixMilli(ms.Int64)
	return &t
}

func (s *BulkStore) Get(ctx context.Context, jobID string) (*BulkJob, error) {
	job, err := scanJob(s.db.QueryRowContext(ctx, jobSelect+" WHERE j.id = ?", jobID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, calyxerr.New(codes.NotFound, "Bulk job %s not found.", jobID)
	}
	return job, err
}

// GetMany returns the jobs found for the ids, in no defined order. A missing id is left out
// and a repeated id returns its job once. One query serves the whole list.
func (s *BulkStore) GetMany(ctx context.Context, ids []string) ([]*BulkJob, error) {
	seen := make(map[string]bool, len(ids))
	placeholders := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			placeholders = append(placeholders, "?")
			args = append(args, id)
		}
	}
	if len(args) == 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, jobSelect+" WHERE j.id IN ("+strings.Join(placeholders, ", ")+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []*BulkJob
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// NextUnfinished returns the oldest job of the instance that still has work to do, or nil.
func (s *BulkStore) NextUnfinished(ctx context.Context, instanceID string) (*BulkJob, error) {
	job, err := scanJob(s.db.QueryRowContext(ctx, jobSelect+`
		WHERE j.instance_id = ? AND j.status IN ('QUEUED', 'RUNNING')
		ORDER BY j.created_at, j.rowid LIMIT 1`, instanceID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return job, err
}

// InstancesWithUnfinishedJobs returns the names of the enabled instances that must be awake
// because they have jobs to continue.
func (s *BulkStore) InstancesWithUnfinishedJobs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT i.name FROM bulk_jobs j
		JOIN instances i ON i.id = j.instance_id
		WHERE j.status IN ('QUEUED', 'RUNNING') AND i.is_enabled = 1
		ORDER BY i.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// RecoverInterrupted fails the messages that were being sent when the core stopped: nobody knows
// whether WhatsApp received them, and a duplicate is worse than a gap, so they are never resent.
func (s *BulkStore) RecoverInterrupted(ctx context.Context, instanceID string) error {
	type interrupted struct {
		jobID     string
		messageID int64
	}
	rows, err := s.db.QueryContext(ctx, `SELECT m.job_id, m.id FROM bulk_job_messages m
		JOIN bulk_jobs j ON j.id = m.job_id
		WHERE m.status = 'SENDING' AND j.instance_id = ?`, instanceID)
	if err != nil {
		return err
	}
	var found []interrupted
	for rows.Next() {
		var m interrupted
		if err := rows.Scan(&m.jobID, &m.messageID); err != nil {
			rows.Close()
			return err
		}
		found = append(found, m)
	}
	rows.Close() // before MarkFailed: there is only one connection
	if err := rows.Err(); err != nil {
		return err
	}

	for _, m := range found {
		if err := s.MarkFailed(ctx, m.jobID, m.messageID, "Interrupted by a restart, the result is unknown."); err != nil {
			return err
		}
	}
	return nil
}

// MarkRunning starts a queued job. A job cancelled since it was read stays cancelled.
func (s *BulkStore) MarkRunning(ctx context.Context, jobID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE bulk_jobs SET status = 'RUNNING', started_at = ?
		WHERE id = ? AND status = 'QUEUED'`, time.Now().UnixMilli(), jobID)
	return err
}

// NextPending returns the pending message with the lowest position, or nil when none is left.
func (s *BulkStore) NextPending(ctx context.Context, jobID string) (*BulkMessage, error) {
	var m BulkMessage
	err := s.db.QueryRowContext(ctx, `SELECT id, job_id, position, recipient, text, delay_ms, pause_after_ms, status
		FROM bulk_job_messages WHERE job_id = ? AND status = 'PENDING' ORDER BY position LIMIT 1`, jobID).
		Scan(&m.ID, &m.JobID, &m.Position, &m.Recipient, &m.Text, &m.DelayMs, &m.PauseAfterMs, &m.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *BulkStore) MarkSending(ctx context.Context, messageID int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE bulk_job_messages SET status = 'SENDING' WHERE id = ?", messageID)
	return err
}

// MarkPending puts a message back in the queue. Only for messages that were surely not sent.
// When the job finished in the meantime (a cancel), the message is cancelled instead.
func (s *BulkStore) MarkPending(ctx context.Context, messageID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE bulk_job_messages SET
		status = CASE WHEN (SELECT status FROM bulk_jobs WHERE id = job_id) IN ('QUEUED', 'RUNNING')
			THEN 'PENDING' ELSE 'CANCELLED' END,
		text = CASE WHEN (SELECT status FROM bulk_jobs WHERE id = job_id) IN ('QUEUED', 'RUNNING')
			THEN text ELSE '' END
		WHERE id = ?`, messageID)
	return err
}

func (s *BulkStore) MarkSent(ctx context.Context, jobID string, messageID int64, providerMessageID string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE bulk_job_messages
			SET status = 'SENT', provider_message_id = ?, sent_at = ?, text = '' WHERE id = ?`,
			providerMessageID, time.Now().UnixMilli(), messageID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE bulk_jobs SET sent_messages = sent_messages + 1 WHERE id = ?", jobID)
		return err
	})
}

func (s *BulkStore) MarkFailed(ctx context.Context, jobID string, messageID int64, reason string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE bulk_job_messages SET status = 'FAILED', error = ?, text = '' WHERE id = ?",
			reason, messageID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE bulk_jobs SET failed_messages = failed_messages + 1 WHERE id = ?", jobID)
		return err
	})
}

// Finish closes a job that is still open and cancels its pending messages. It returns nil, nil
// when the job was already finished, so the caller knows nothing changed: a cancel and the last
// message can race to finish the same job.
func (s *BulkStore) Finish(ctx context.Context, jobID string, status JobStatus, reason string) (*BulkJob, error) {
	changed := false
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var jobError sql.NullString
		if reason != "" {
			jobError = sql.NullString{String: reason, Valid: true}
		}
		result, err := tx.ExecContext(ctx, `UPDATE bulk_jobs SET status = ?, error = ?, finished_at = ?
			WHERE id = ? AND status IN ('QUEUED', 'RUNNING')`, status, jobError, time.Now().UnixMilli(), jobID)
		if err != nil {
			return err
		}
		if rows, _ := result.RowsAffected(); rows == 0 {
			return nil
		}
		changed = true
		// Whatever was not sent will never be sent.
		_, err = tx.ExecContext(ctx, `UPDATE bulk_job_messages SET status = 'CANCELLED', text = ''
			WHERE job_id = ? AND status = 'PENDING'`, jobID)
		return err
	})
	if err != nil || !changed {
		return nil, err
	}
	return s.Get(ctx, jobID)
}

func (s *BulkStore) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	return inTx(ctx, s.db, fn)
}
