package core

import (
	"context"
	"time"

	"google.golang.org/grpc/status"

	"calyx/backend/internal/apierr"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

const (
	methodSendText   = "/calyx.core.v1.MessageService/SendPlainText"
	methodBulkSame   = "/calyx.core.v1.MessageService/SendBulkSameMessage"
	methodBulkCustom = "/calyx.core.v1.MessageService/SendBulkCustomMessages"
	methodGetBulk    = "/calyx.core.v1.MessageService/GetBulkJob"
	methodGetBulks   = "/calyx.core.v1.MessageService/GetBulkJobs"
	methodCancelBulk = "/calyx.core.v1.MessageService/CancelBulkJob"
)

// BulkInput is a bulk job: either Recipients with one Text, or Messages. Not both.
type BulkInput struct {
	Recipients []string
	Text       string
	Messages   []BulkMessage
	Pacing     *BulkPacing
	Key        string // optional dedupe key; empty sends none
}

// BulkMessage is one message of a custom bulk job. DelayMs is in milliseconds.
type BulkMessage struct {
	To, Text string
	DelayMs  *int32
}

// BulkPacing holds the pacing ranges. Delay and PauseDuration are in milliseconds, PauseEvery is in
// messages. A nil range stays absent. The backend never changes these values.
type BulkPacing struct{ Delay, PauseEvery, PauseDuration *Range }

type Range struct{ Min, Max int32 }

type BulkJob struct {
	ID, Status          string
	Total, Sent, Failed int32
	ProgressPercent     float32
	EstimatedFinishAt   *time.Time
	CreatedAt           time.Time
	Replayed            bool
}

// SendResult is the answer of a send. Replayed is true when the core returned the stored answer
// of an earlier call with the same key.
type SendResult struct {
	MessageID string
	Replayed  bool
}

// keyPtr turns an empty key into nil, so the core sees no key at all.
func keyPtr(key string) *string {
	if key == "" {
		return nil
	}
	return &key
}

// SendText sends one text message. It adds no deadline: it uses the context of the caller.
func (c *Client) SendText(ctx context.Context, coreName, to, text, key string) (SendResult, error) {
	resp, err := c.messages.SendPlainText(ctx, &corev1.SendPlainTextRequest{
		InstanceName: coreName, To: to, Text: text, IdempotencyKey: keyPtr(key),
	})
	if err != nil {
		mapped := mapError(callSend, methodSendText, err)
		if ae, ok := apierr.As(mapped); ok {
			return SendResult{}, sendError{mapped: ae, raw: status.Code(err)}
		}
		return SendResult{}, mapped
	}
	return SendResult{MessageID: resp.GetMessageId(), Replayed: resp.GetReplayed()}, nil
}

func (c *Client) CreateBulkJob(ctx context.Context, coreName string, in BulkInput) (BulkJob, error) {
	custom := len(in.Messages) > 0
	same := len(in.Recipients) > 0 || in.Text != ""
	if custom == same {
		return BulkJob{}, apierr.Invalid("Give either recipients and text, or messages.")
	}
	ctx, cancel := context.WithTimeout(ctx, bulkTimeout)
	defer cancel()
	var job *corev1.BulkJob
	var err error
	method := methodBulkSame
	if custom {
		method = methodBulkCustom
		msgs := make([]*corev1.PlainTextPayload, 0, len(in.Messages))
		for _, m := range in.Messages {
			msgs = append(msgs, &corev1.PlainTextPayload{To: m.To, Text: m.Text, DelayMs: m.DelayMs})
		}
		job, err = c.messages.SendBulkCustomMessages(ctx, &corev1.SendBulkCustomMessagesRequest{
			InstanceName: coreName, Messages: msgs, Pacing: bulkPacing(in.Pacing),
			IdempotencyKey: keyPtr(in.Key),
		})
	} else {
		job, err = c.messages.SendBulkSameMessage(ctx, &corev1.SendBulkSameMessageRequest{
			InstanceName: coreName, Recipients: in.Recipients, Text: in.Text, Pacing: bulkPacing(in.Pacing),
			IdempotencyKey: keyPtr(in.Key),
		})
	}
	if err != nil {
		return BulkJob{}, mapError(callBulk, method, err)
	}
	return bulkJob(job), nil
}

func (c *Client) GetBulkJob(ctx context.Context, id string) (BulkJob, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	job, err := c.messages.GetBulkJob(ctx, &corev1.GetBulkJobRequest{JobId: id})
	if err != nil {
		return BulkJob{}, mapError(callRead, methodGetBulk, err)
	}
	return bulkJob(job), nil
}

// GetBulkJobs returns the jobs of the ids in one call. The core answer has no defined order. The
// core leaves a missing id out. The core takes 1 to 50 ids.
func (c *Client) GetBulkJobs(ctx context.Context, ids []string) ([]BulkJob, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	resp, err := c.messages.GetBulkJobs(ctx, &corev1.GetBulkJobsRequest{JobIds: ids})
	if err != nil {
		return nil, mapError(callRead, methodGetBulks, err)
	}
	jobs := make([]BulkJob, 0, len(resp.GetJobs()))
	for _, j := range resp.GetJobs() {
		jobs = append(jobs, bulkJob(j))
	}
	return jobs, nil
}

// CancelBulkJob cancels a bulk job. A finished job comes back unchanged.
func (c *Client) CancelBulkJob(ctx context.Context, id string) (BulkJob, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	job, err := c.messages.CancelBulkJob(ctx, &corev1.CancelBulkJobRequest{JobId: id})
	if err != nil {
		return BulkJob{}, mapError(callRead, methodCancelBulk, err)
	}
	return bulkJob(job), nil
}
