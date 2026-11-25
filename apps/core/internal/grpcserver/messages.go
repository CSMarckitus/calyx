package grpcserver

import (
	"context"
	"fmt"
	"math"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"calyx/core/internal/engine"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

type messageService struct {
	corev1.UnimplementedMessageServiceServer
	o *engine.Orchestrator
}

func (s *messageService) SendPlainText(ctx context.Context, req *corev1.SendPlainTextRequest) (*corev1.SendPlainTextResponse, error) {
	result, err := s.o.SendText(ctx, req.GetInstanceName(), req.GetTo(), req.GetText(), req.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	return &corev1.SendPlainTextResponse{MessageId: result.MessageID, Replayed: result.Replayed}, nil
}

func (s *messageService) SendBulkSameMessage(ctx context.Context, req *corev1.SendBulkSameMessageRequest) (*corev1.BulkJob, error) {
	messages := make([]engine.BulkMessageInput, 0, len(req.GetRecipients()))
	for _, recipient := range req.GetRecipients() {
		messages = append(messages, engine.BulkMessageInput{To: recipient, Text: req.GetText()})
	}
	// BulkSame allows one message per recipient: the same text twice to one person is never wanted.
	job, replayed, err := s.o.CreateBulkJob(ctx, req.GetInstanceName(), engine.BulkSame, messages, pacingOptions(req.GetPacing()), req.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	msg := bulkJobMessage(job)
	msg.Replayed = replayed
	return msg, nil
}

func (s *messageService) SendBulkCustomMessages(ctx context.Context, req *corev1.SendBulkCustomMessagesRequest) (*corev1.BulkJob, error) {
	messages := make([]engine.BulkMessageInput, 0, len(req.GetMessages()))
	for _, m := range req.GetMessages() {
		input := engine.BulkMessageInput{To: m.GetTo(), Text: m.GetText()}
		if m.DelayMs != nil {
			delay := int(*m.DelayMs)
			input.DelayMs = &delay
		}
		messages = append(messages, input)
	}
	job, replayed, err := s.o.CreateBulkJob(ctx, req.GetInstanceName(), engine.BulkCustom, messages, pacingOptions(req.GetPacing()), req.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	msg := bulkJobMessage(job)
	msg.Replayed = replayed
	return msg, nil
}

func (s *messageService) GetBulkJob(ctx context.Context, req *corev1.GetBulkJobRequest) (*corev1.BulkJob, error) {
	job, err := s.o.GetBulkJob(ctx, req.GetJobId())
	if err != nil {
		return nil, err
	}
	return bulkJobMessage(job), nil
}

func (s *messageService) GetBulkJobs(ctx context.Context, req *corev1.GetBulkJobsRequest) (*corev1.GetBulkJobsResponse, error) {
	jobs, err := s.o.GetBulkJobs(ctx, req.GetJobIds())
	if err != nil {
		return nil, err
	}
	msgs := make([]*corev1.BulkJob, 0, len(jobs))
	for _, job := range jobs {
		msgs = append(msgs, bulkJobMessage(job))
	}
	return &corev1.GetBulkJobsResponse{Jobs: msgs}, nil
}

func (s *messageService) CancelBulkJob(ctx context.Context, req *corev1.CancelBulkJobRequest) (*corev1.BulkJob, error) {
	job, err := s.o.CancelBulkJob(ctx, req.GetJobId())
	if err != nil {
		return nil, err
	}
	return bulkJobMessage(job), nil
}

func pacingOptions(p *corev1.BulkPacing) engine.BulkPacingOptions {
	toRange := func(r *corev1.IntRange) *engine.Range {
		if r == nil {
			return nil
		}
		return &engine.Range{Min: int(r.GetMin()), Max: int(r.GetMax())}
	}
	return engine.BulkPacingOptions{
		Delay:         toRange(p.GetDelay()),
		PauseEvery:    toRange(p.GetPauseEvery()),
		PauseDuration: toRange(p.GetPauseDuration()),
	}
}

func bulkJobMessage(job *engine.BulkJob) *corev1.BulkJob {
	msg := &corev1.BulkJob{
		Id:               job.ID,
		InstanceName:     job.InstanceName,
		Status:           corev1.BulkJobStatus(corev1.BulkJobStatus_value["BULK_JOB_STATUS_"+string(job.Status)]),
		TotalMessages:    int32(job.TotalMessages),
		SentMessages:     int32(job.SentMessages),
		FailedMessages:   int32(job.FailedMessages),
		EstimatedMinutes: float32(math.Round(float64(job.EstimatedMs)/60000*100) / 100),
		DisplayTime:      formatDuration(job.EstimatedMs),
		CreatedAt:        timestamppb.New(job.CreatedAt),
	}
	if job.TotalMessages > 0 {
		msg.ProgressPercent = float32(job.SentMessages+job.FailedMessages) / float32(job.TotalMessages) * 100
	}
	if job.StartedAt != nil {
		msg.StartedAt = timestamppb.New(*job.StartedAt)
		if job.Status == engine.JobRunning {
			msg.EstimatedFinishAt = timestamppb.New(job.StartedAt.Add(time.Duration(job.EstimatedMs) * time.Millisecond))
		}
	}
	if job.FinishedAt != nil {
		msg.FinishedAt = timestamppb.New(*job.FinishedAt)
	}
	if job.Error != "" {
		msg.Error = &job.Error
	}
	return msg
}

// formatDuration writes a duration as "12m 5s".
func formatDuration(ms int) string {
	totalSeconds := int(math.Round(float64(ms) / 1000))
	return fmt.Sprintf("%dm %ds", totalSeconds/60, totalSeconds%60)
}
