package httpapi

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/bulk"
	"calyx/backend/internal/core"
	"calyx/backend/internal/tenancy"
)

// bulkCreateDeadline bounds the whole create call (the core client adds its own 30 s).
const bulkCreateDeadline = 60 * time.Second

// bulkBodyLimit is 16 MiB. 5,000 messages reach it at about 3.3 KB per message, so a job of
// 5,000 messages with texts near the 4,096-character limit (more with multi-byte characters) can
// be refused with 400 "larger than".
const bulkBodyLimit = 16 << 20

// bulkBodyReadTimeout is the time a client has to send the body of a bulk job: up to 16 MiB on a
// slow link.
const bulkBodyReadTimeout = 60 * time.Second

type bulkMessageInput struct {
	To      recipient   `json:"to"`
	Text    messageText `json:"text"`
	DelayMs *delayMs    `json:"delay_ms,omitempty" nullable:"true"`
}

type rangeInput struct {
	Min pacingBound `json:"min"`
	Max pacingBound `json:"max"`
}

// core returns the core range, or nil when the range is absent.
func (r *rangeInput) core(f *apierr.Fields, field string) *core.Range {
	if r == nil {
		return nil
	}
	return &core.Range{Min: toInt32(f, field+".min", int64(r.Min)), Max: toInt32(f, field+".max", int64(r.Max))}
}

// toInt32 converts a number of the request. A value outside the int32 range is a field error, in
// the same place as a type error: the service does not run. The service refuses a negative value.
func toInt32(f *apierr.Fields, field string, v int64) int32 {
	if v < math.MinInt32 || v > math.MaxInt32 {
		f.Add(field, fmt.Sprintf("must be 0 to %d.", math.MaxInt32))
		return 0
	}
	return int32(v)
}

// The objects of pacing are optional. Their schema does not allow null (Huma cannot make a $ref
// nullable), but the server skips a null optional property, so the descriptions say it.
type bulkPacingInput struct {
	Delay         *rangeInput `json:"delay,omitempty" doc:"The delay between two messages, in milliseconds. Optional. An explicit null is the same as an absent field."`
	PauseEvery    *rangeInput `json:"pause_every,omitempty" doc:"The number of messages between two pauses. Optional. An explicit null is the same as an absent field."`
	PauseDuration *rangeInput `json:"pause_duration,omitempty" doc:"The length of a pause, in milliseconds. Optional. An explicit null is the same as an absent field."`
}

type createBulkJobInput struct {
	ID             uuid.UUID      `path:"id" format:"uuid"`
	IdempotencyKey idempotencyKey `header:"Idempotency-Key"`
	Body           struct {
		Recipients recipientList    `json:"recipients,omitempty" nullable:"true"`
		Text       *messageText     `json:"text,omitempty" nullable:"true"`
		Messages   bulkMessageList  `json:"messages,omitempty" nullable:"true"`
		Pacing     *bulkPacingInput `json:"pacing,omitempty" doc:"The pacing of the job. The core keeps its minimums. Optional. An explicit null is the same as an absent field."`
	}
}

// input builds the service input. An absent or null list stays nil: the bulk service tells it
// from an empty list. The service checks every rule, the count of messages included. The error
// holds the numbers outside the int32 range.
func (in *createBulkJobInput) input() (bulk.Input, error) {
	b := in.Body
	out := bulk.Input{Key: string(in.IdempotencyKey)}
	var f apierr.Fields
	if b.Recipients != nil {
		out.Recipients = make([]string, 0, len(b.Recipients))
		for _, to := range b.Recipients {
			out.Recipients = append(out.Recipients, string(to))
		}
	}
	if b.Text != nil {
		text := string(*b.Text)
		out.Text = &text
	}
	if b.Messages != nil {
		out.Messages = make([]core.BulkMessage, 0, len(b.Messages))
		for i, m := range b.Messages {
			cm := core.BulkMessage{To: string(m.To), Text: string(m.Text)}
			if m.DelayMs != nil {
				d := toInt32(&f, fmt.Sprintf("messages[%d].delay_ms", i), int64(*m.DelayMs))
				cm.DelayMs = &d
			}
			out.Messages = append(out.Messages, cm)
		}
	}
	if p := b.Pacing; p != nil {
		out.Pacing = &core.BulkPacing{
			Delay:         p.Delay.core(&f, "pacing.delay"),
			PauseEvery:    p.PauseEvery.core(&f, "pacing.pause_every"),
			PauseDuration: p.PauseDuration.core(&f, "pacing.pause_duration"),
		}
	}
	return out, f.Err()
}

type bulkJobOutput struct{ Body bulkJobJSON }

type bulkJobListOutput struct{ Body itemsJSON[bulkJobJSON] }

func (s *server) registerBulkOps(api huma.API) {
	const v1 = "/api/v1"
	huma.Register(api, withAccess(asTool(huma.Operation{
		OperationID: "createBulkJob", Tags: []string{"Bulk jobs"}, Method: http.MethodPost, Path: v1 + "/instances/{id}/bulk-jobs",
		Summary: "Start a bulk job: one text for many recipients, or custom messages.", DefaultStatus: http.StatusCreated,
		Description: "Errors: 400 invalid_request (also the core's pacing refusal), 401 unauthenticated, " +
			"403 csrf_check_failed, 404 not_found, 409 instance_not_connected, 409 instance_not_ready, " +
			"409 idempotency_key_conflict, 413 too_many_messages, 502 core_rejected_token, 504 bulk_outcome_unknown. " +
			"Send either recipients with text, or messages. Anti-spam: with text, each recipient appears once; " +
			"with messages, a recipient gets at most 2 messages. Pacing units: delay and pause_duration are in " +
			"milliseconds, pause_every counts messages. The backend adds no pacing and never lowers it. " +
			"A 504 bulk_outcome_unknown means that the job may run in the core without a backend record. " +
			"With an Idempotency-Key, retry with the same key: the retry gives the job and stores it. " +
			"Without a key, the job cannot be read or cancelled through the backend, and the core's " +
			"bulk.finished webhook still reports it. Do not start it again automatically.",
		Responses:       map[string]*huma.Response{"201": {Description: "The job."}},
		MaxBodyBytes:    bulkBodyLimit,
		BodyReadTimeout: bulkBodyReadTimeout,
	}, "create_bulk_job", toolCreateBulkJob), accessLoggedIn, tenancy.UseInstances), s.createBulkJob)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "listBulkJobs", Tags: []string{"Bulk jobs"}, Method: http.MethodGet, Path: v1 + "/instances/{id}/bulk-jobs",
		Summary:     "List the latest bulk jobs of an instance, newest first, at most 50.",
		Description: "Errors: 401 unauthenticated, 404 not_found, 503 core_unavailable.",
		Responses:   map[string]*huma.Response{"200": {Description: "The jobs, with their status from the core."}},
	}, accessLoggedIn, tenancy.UseInstances), s.listBulkJobs)
	huma.Register(api, withAccess(asTool(huma.Operation{
		OperationID: "getBulkJob", Tags: []string{"Bulk jobs"}, Method: http.MethodGet, Path: v1 + "/bulk-jobs/{id}",
		Summary:     "Read a bulk job.",
		Description: "Errors: 401 unauthenticated, 404 not_found, 503 core_unavailable.",
		Responses:   map[string]*huma.Response{"200": {Description: "The job."}},
	}, "get_bulk_job", toolGetBulkJob), accessLoggedIn, tenancy.UseInstances), s.getBulkJob)
	huma.Register(api, withAccess(asTool(huma.Operation{
		OperationID: "cancelBulkJob", Tags: []string{"Bulk jobs"}, Method: http.MethodPost, Path: v1 + "/bulk-jobs/{id}/cancel",
		Summary: "Cancel a bulk job.",
		Description: "A finished job is returned unchanged. Errors: 401 unauthenticated, 403 csrf_check_failed, " +
			"404 not_found, 503 core_unavailable.",
		Responses: map[string]*huma.Response{"200": {Description: "The job."}},
	}, "cancel_bulk_job", toolCancelBulkJob), accessLoggedIn, tenancy.UseInstances), s.cancelBulkJob)
}

// POST /instances/{id}/bulk-jobs. Recipients and texts are neither stored nor logged.
func (s *server) createBulkJob(ctx context.Context, in *createBulkJobInput) (*bulkJobOutput, error) {
	bi, err := in.input()
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	parent := ctx
	if bi.Key != "" {
		// A client with a key can learn the result of a job that it left, so the core call goes on
		// when the client stops waiting. The core client keeps its own bulk timeout.
		parent = context.WithoutCancel(ctx)
	}
	createCtx, cancel := context.WithTimeout(parent, bulkCreateDeadline)
	defer cancel()
	job, err := s.Bulk.Create(createCtx, principalOf(ctx), in.ID, bi)
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	return &bulkJobOutput{Body: bulkJobResource(job)}, nil
}

// GET /instances/{id}/bulk-jobs
func (s *server) listBulkJobs(ctx context.Context, in *idInput) (*bulkJobListOutput, error) {
	jobs, err := s.Bulk.ListForInstance(ctx, principalOf(ctx), in.ID)
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	items := make([]bulkJobJSON, 0, len(jobs))
	for _, j := range jobs {
		items = append(items, bulkJobResource(j))
	}
	return &bulkJobListOutput{Body: itemsJSON[bulkJobJSON]{Items: items}}, nil
}

// GET /bulk-jobs/{id}
func (s *server) getBulkJob(ctx context.Context, in *idInput) (*bulkJobOutput, error) {
	job, err := s.Bulk.Get(ctx, principalOf(ctx), in.ID)
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	return &bulkJobOutput{Body: bulkJobResource(job)}, nil
}

// POST /bulk-jobs/{id}/cancel
func (s *server) cancelBulkJob(ctx context.Context, in *idInput) (*bulkJobOutput, error) {
	job, err := s.Bulk.Cancel(ctx, principalOf(ctx), in.ID)
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	return &bulkJobOutput{Body: bulkJobResource(job)}, nil
}
