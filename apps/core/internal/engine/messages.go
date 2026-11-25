package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"time"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/events"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// SendResult is the answer of SendText.
type SendResult struct {
	MessageID string // the provider message id
	Replayed  bool   // the key belongs to an earlier send that went out: nothing was sent now
}

// outcomeWriteTimeout bounds the write of a send outcome. The write does not use the context of
// the caller, because the caller may have stopped waiting.
const outcomeWriteTimeout = 10 * time.Second

// notConnectedWait is the retry advice for a send to an instance that is not connected. A
// reconnection usually takes longer than the wait of a place in the queue.
const notConnectedWait = 60 * time.Second

// SendText sends one message, at most once per idempotency key. The repeat guard refuses the
// same message text to the same recipient within RepeatWindow, with or without a key.
func (o *Orchestrator) SendText(ctx context.Context, name, to, text string, key *string) (SendResult, error) {
	if err := checkKeyFormat(key); err != nil {
		return SendResult{}, err
	}
	if strings.TrimSpace(text) == "" {
		return SendResult{}, calyxerr.New(codes.InvalidArgument, "The text of a message cannot be empty.")
	}
	// The status read loads nothing, so a send to an instance that is not connected never
	// connects it: a check has no side effect.
	row, err := findInstance(ctx, o.db, name)
	if err != nil {
		return SendResult{}, err
	}
	if o.statusOf(row) != corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED {
		return o.sendNotConnected(ctx, row, to, text, key)
	}

	live, err := o.acquire(name)
	if err != nil {
		return SendResult{}, err
	}
	defer o.release(live)

	recipient, err := live.provider.NormalizeRecipient(to)
	if err != nil {
		return SendResult{}, err
	}
	check, err := o.records.BeginSingle(ctx, live.id, key, singleFingerprint(recipient, text))
	if err != nil {
		return SendResult{}, refuseSend(name, err, live, check.PendingRecordID)
	}
	if check.Replayed {
		return replaySend(name, check), nil
	}

	// Only a send with a key can be retried, so only it needs its place in the queue.
	var queueID int64
	var keyHash string
	if key != nil {
		queueID = check.RecordID
		keyHash = hashKey(*key)
	}
	id, err := live.outbox.Send(ctx, recipient, text, queueID, func(outcome SendOutcome) {
		o.recordOutcome(name, check.RecordID, keyHash, outcome)
	})
	if err != nil {
		return SendResult{}, err
	}
	return SendResult{MessageID: id}, nil
}

// sendNotConnected answers a send to an instance that is not connected. A known key gets its
// stored answer. Any other send is refused: nothing is queued and no record is written.
func (o *Orchestrator) sendNotConnected(ctx context.Context, row InstanceRow, to, text string, key *string) (SendResult, error) {
	notConnected := calyxerr.New(codes.Unavailable, "Instance %s is not connected.", row.Name).WithRetryAfter(notConnectedWait)
	if key == nil {
		return SendResult{}, notConnected
	}
	// The normalizer option, not the provider: the instance may have no provider now.
	recipient, err := o.opts.NormalizeRecipient(row.Provider, to)
	if err != nil {
		return SendResult{}, err
	}
	check, known, err := o.records.LookupSingle(ctx, row.ID, *key, singleFingerprint(recipient, text))
	if err != nil {
		o.mu.Lock()
		live := o.live[row.Name] // A closing instance is no longer in o.live.
		o.mu.Unlock()
		return SendResult{}, refuseSend(row.Name, err, live, check.PendingRecordID)
	}
	if !known {
		return SendResult{}, notConnected
	}
	return replaySend(row.Name, check), nil
}

// refuseSend logs a refusal of the send records and returns it. A SEND_IN_PROGRESS refusal gets
// the wait of its place in the queue of live, or notConnectedWait when live is nil.
func refuseSend(name string, err error, live *liveInstance, pendingRecordID int64) error {
	var refusal *calyxerr.Error
	errors.As(err, &refusal)
	if refusal != nil && refusal.Reason != corev1.ErrorReason_ERROR_REASON_UNSPECIFIED {
		slog.Info("Send refused.", "instance", name, "reason", refusal.Reason.String())
	}
	if refusal != nil && refusal.Reason == corev1.ErrorReason_ERROR_REASON_SEND_IN_PROGRESS {
		wait := notConnectedWait
		if live != nil {
			// Place is false when the loop already took the message: it leaves within one message.
			wait = live.provider.Pacing().QueueWait()
			if place, queued := live.outbox.Place(pendingRecordID); queued {
				wait *= time.Duration(place)
			}
		}
		refusal.RetryAfter = wait
	}
	return err
}

func replaySend(name string, check SingleCheck) SendResult {
	slog.Info("Send replayed: the idempotency key belongs to a message already sent.", "instance", name)
	return SendResult{MessageID: check.MessageID, Replayed: true}
}

// hashKey is the first 16 hex characters of the SHA-256 of an idempotency key. Logs use it
// instead of the key, so an operator can match a line to a request without seeing the key.
func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:16]
}

// recordOutcome writes the outcome of a single send to its row. A failed write leaves the row
// PENDING: the sweeper makes it UNKNOWN after one hour. A send with a key (keyHash not empty)
// also logs its outcome.
func (o *Orchestrator) recordOutcome(name string, recordID int64, keyHash string, outcome SendOutcome) {
	ctx, cancel := context.WithTimeout(context.Background(), outcomeWriteTimeout)
	defer cancel()
	var err error
	result := "UNKNOWN"
	switch outcome.Kind {
	case OutcomeSent:
		result = "SENT"
		err = o.records.MarkSent(ctx, recordID, outcome.MessageID)
	case OutcomeNotSent:
		result = "NOT_SENT"
		err = o.records.MarkNotSent(ctx, recordID)
	default:
		err = o.records.MarkUnknown(ctx, recordID)
	}
	if keyHash != "" {
		slog.Info("Send finished.", "instance", name, "outcome", result, "message_id", outcome.MessageID, "key_hash", keyHash)
	}
	if err != nil {
		slog.Error("Could not record the outcome of a send.", "instance", name, "err", err)
	}
}

// BulkKind is the gRPC call that created a bulk job. It is part of the fingerprint of the job.
type BulkKind int

const (
	BulkSame   BulkKind = iota + 1 // SendBulkSameMessage
	BulkCustom                     // SendBulkCustomMessages
)

// The anti-spam limits of one bulk job: the most messages that one recipient (after
// normalization) gets. More messages to one person in one job look like spam. The backend
// repeats these values to give a field error first.
const (
	MaxSameTextPerRecipient = 1 // SendBulkSameMessage: the same text twice to one person is never useful
	MaxCustomPerRecipient   = 2 // SendBulkCustomMessages
)

// maxPerRecipient is the anti-spam limit of one job of the kind.
func (k BulkKind) maxPerRecipient() int {
	if k == BulkSame {
		return MaxSameTextPerRecipient
	}
	return MaxCustomPerRecipient
}

// CreateBulkJob validates, plans and stores a job; the outbox of the instance runs it. A job that
// gives one recipient more messages than its kind allows is refused as a whole. With a key that
// is already known, it creates nothing and returns the earlier job with replayed true.
func (o *Orchestrator) CreateBulkJob(ctx context.Context, name string, kind BulkKind, messages []BulkMessageInput, pacing BulkPacingOptions, key *string) (job *BulkJob, replayed bool, err error) {
	if err := checkKeyFormat(key); err != nil {
		return nil, false, err
	}
	if len(messages) == 0 {
		return nil, false, calyxerr.New(codes.InvalidArgument, "A bulk job needs at least one message.")
	}
	live, err := o.acquire(name)
	if err != nil {
		return nil, false, err
	}
	defer o.release(live)

	maxPerRecipient := kind.maxPerRecipient()
	normalized := make([]BulkMessageInput, 0, len(messages))
	count := map[string]int{}
	for i, message := range messages {
		recipient, err := live.provider.NormalizeRecipient(message.To)
		if err != nil {
			return nil, false, calyxerr.New(codes.InvalidArgument, "Message %d: %s", i, err.Error())
		}
		// The message does not hold the recipient: errors reach logs.
		if count[recipient]++; count[recipient] > maxPerRecipient {
			return nil, false, calyxerr.New(codes.InvalidArgument,
				"Message %d: its recipient has more than %d messages in this job. Calyx does not allow it: it looks like spam.",
				i, maxPerRecipient)
		}
		message.To = recipient
		normalized = append(normalized, message)
	}

	// The key is checked in the transaction that stores the job, so two equal requests cannot
	// both create one. Planning first is safe: with the same fingerprint, the plan passes again.
	plan, estimatedMs, err := PlanBulk(normalized, pacing, live.provider.Pacing())
	if err != nil {
		return nil, false, err
	}
	var bulkKey *BulkKey
	if key != nil {
		bulkKey = &BulkKey{Key: *key, Fingerprint: bulkFingerprint(kind, normalized, pacing), Now: o.opts.Now()}
	}
	job, replayed, err = o.store.Create(ctx, live.id, plan, estimatedMs, bulkKey)
	if err != nil {
		var refusal *calyxerr.Error
		if errors.As(err, &refusal) && refusal.Reason != corev1.ErrorReason_ERROR_REASON_UNSPECIFIED {
			slog.Info("Bulk job refused.", "instance", name, "reason", refusal.Reason.String())
		}
		return nil, false, err
	}
	if replayed {
		slog.Info("Bulk job replayed: the idempotency key belongs to a job already created.", "instance", name)
		return job, true, nil
	}
	live.outbox.NotifyNewJob()
	slog.Info("Bulk job planned.", "instance", name, "job", job.ID, "messages", len(plan))
	return job, false, nil
}

func (o *Orchestrator) GetBulkJob(ctx context.Context, jobID string) (*BulkJob, error) {
	return o.store.Get(ctx, jobID)
}

// maxBulkJobIDs is the most jobs one GetBulkJobs call can ask for.
const maxBulkJobIDs = 50

// GetBulkJobs returns the jobs found for the ids. A missing id is left out, with no error.
func (o *Orchestrator) GetBulkJobs(ctx context.Context, jobIDs []string) ([]*BulkJob, error) {
	if len(jobIDs) == 0 || len(jobIDs) > maxBulkJobIDs {
		return nil, calyxerr.New(codes.InvalidArgument, "Ask for 1 to %d bulk jobs at a time.", maxBulkJobIDs)
	}
	return o.store.GetMany(ctx, jobIDs)
}

// CancelBulkJob stops a job. Messages already sent are not affected. Cancelling a finished job
// returns it unchanged.
func (o *Orchestrator) CancelBulkJob(ctx context.Context, jobID string) (*BulkJob, error) {
	job, err := o.store.Get(ctx, jobID)
	if err != nil {
		return nil, err
	}
	cancelled, err := o.store.Finish(ctx, jobID, JobCancelled, "")
	if err != nil {
		return nil, err
	}
	if cancelled == nil {
		return job, nil // it had already finished
	}
	o.mu.Lock()
	live := o.live[job.InstanceName]
	o.mu.Unlock()
	if live != nil {
		live.outbox.NotifyJobCancelled(jobID)
	}
	o.publishJobFinished(cancelled)
	return cancelled, nil
}

func (o *Orchestrator) publishJobFinished(job *BulkJob) {
	o.bus.Publish(job.InstanceName, "bulk.finished", events.BulkFinished{
		JobID: job.ID, Status: string(job.Status), TotalMessages: job.TotalMessages,
		SentMessages: job.SentMessages, FailedMessages: job.FailedMessages, Error: job.Error,
	})
}
