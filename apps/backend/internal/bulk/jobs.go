// Package bulk holds the bulk jobs of the backend. The backend stores only the ownership of each
// job (which instance, organization and client it belongs to); status and progress come from the
// core. Pacing values go to the core unchanged: the core enforces its minimums.
package bulk

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/core"
	"calyx/backend/internal/database/gen"
	"calyx/backend/internal/instances"
	"calyx/backend/internal/tenancy"
)

// MaxMessages is the largest number of messages in one bulk job.
const MaxMessages = 5000

// dbWriteTimeout limits the write that follows a core answer (the same limit as instances.Remove).
const dbWriteTimeout = 10 * time.Second

// Job is a core bulk job with the id of the backend instance that owns it.
type Job struct {
	core.BulkJob
	InstanceID uuid.UUID
}

type Service struct {
	pool *pgxpool.Pool
	core *core.Client
	inst *instances.Service
	log  *slog.Logger
}

func NewService(pool *pgxpool.Pool, c *core.Client, inst *instances.Service) *Service {
	return &Service{pool: pool, core: c, inst: inst, log: slog.Default()}
}

func dbError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return apierr.NotFound()
	}
	return apierr.Internal(err)
}

func isCode(err error, code string) bool {
	ae, ok := apierr.As(err)
	return ok && ae.Code == code
}

func (s *Service) loadInstance(ctx context.Context, p tenancy.Principal, id uuid.UUID) (gen.Instance, error) {
	sc := tenancy.Scope(p)
	row, err := gen.New(s.pool).GetInstanceInScope(ctx, gen.GetInstanceInScopeParams{
		ID: id, OrganizationID: sc.OrganizationID, ClientID: sc.ClientID,
	})
	if err != nil {
		return gen.Instance{}, dbError(err)
	}
	return row, nil
}

// Create starts a bulk job in the core and stores its ownership. When the core does not confirm
// (504 bulk_outcome_unknown), nothing is stored. A retry with the same key gives the same job,
// and stores the row when it is missing.
func (s *Service) Create(ctx context.Context, p tenancy.Principal, instanceID uuid.UUID, in Input) (Job, error) {
	if err := tenancy.Allow(p, tenancy.UseInstances); err != nil {
		return Job{}, err
	}
	if err := validateInput(in); err != nil {
		return Job{}, err
	}
	row, err := s.loadInstance(ctx, p, instanceID)
	if err != nil {
		return Job{}, err
	}
	if row.State != "active" {
		return Job{}, apierr.Conflict(apierr.CodeInstanceNotConnected, "The instance is not ready to send. Its state is "+row.State+".")
	}
	job, err := s.core.CreateBulkJob(ctx, row.CoreName, in.coreInput())
	if err != nil {
		if isCode(err, apierr.CodeInstanceNotReady) {
			s.inst.TriggerStatusCheck(ctx, row.ID)
		}
		return Job{}, err
	}
	jobID, err := uuid.Parse(job.ID)
	if err != nil {
		s.log.Error("The core gave a bulk job id that is not a UUID.", "job_id", job.ID, "instance_id", row.ID)
		return Job{}, errBulkNotStored
	}
	// The core started the job: the insert must not stop because the request ended.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dbWriteTimeout)
	defer cancel()
	q := gen.New(s.pool)
	n, err := q.InsertBulkJob(wctx, gen.InsertBulkJobParams{
		ID: jobID, InstanceID: row.ID, OrganizationID: row.OrganizationID, ClientID: row.ClientID, CreatedBy: p.UserID,
	})
	if err != nil {
		// The job runs in the core without an ownership row. A retry with the same key stores it.
		s.log.Error("Cannot store the ownership of a bulk job.", "job_id", job.ID, "instance_id", row.ID, "error", err)
		return Job{}, errBulkNotStored
	}
	if n == 0 {
		// The row exists: a replay. It must belong to this instance, or the core gave one id to two.
		sc := tenancy.Scope(p)
		existing, err := q.GetBulkJobInScope(wctx, gen.GetBulkJobInScopeParams{
			ID: jobID, OrganizationID: sc.OrganizationID, ClientID: sc.ClientID,
		})
		switch {
		case errors.Is(err, pgx.ErrNoRows) || (err == nil && existing.InstanceID != row.ID):
			s.log.Error("The core gave a bulk job id that belongs to another instance.", "job_id", job.ID, "instance_id", row.ID)
			return Job{}, apierr.Internal(errors.New("bulk job id of another instance"))
		case err != nil:
			s.log.Error("Cannot read the ownership of a bulk job.", "job_id", job.ID, "instance_id", row.ID, "error", err)
			return Job{}, errBulkNotStored
		}
	}
	return Job{BulkJob: job, InstanceID: row.ID}, nil
}

// errBulkNotStored is the answer when the job exists in the core but the backend has no row.
var errBulkNotStored = apierr.New(504, apierr.CodeBulkOutcomeUnknown,
	"The core started the job, but the backend could not store it. If you sent an Idempotency-Key, "+
		"retry with the same key: the retry stores the job. Without a key, do not start it again.")

// owned reads the ownership row with the tenant scope. A job of another tenant is 404 before any
// core call.
func (s *Service) owned(ctx context.Context, p tenancy.Principal, id uuid.UUID) (gen.BulkJob, error) {
	if err := tenancy.Allow(p, tenancy.UseInstances); err != nil {
		return gen.BulkJob{}, err
	}
	sc := tenancy.Scope(p)
	row, err := gen.New(s.pool).GetBulkJobInScope(ctx, gen.GetBulkJobInScopeParams{
		ID: id, OrganizationID: sc.OrganizationID, ClientID: sc.ClientID,
	})
	if err != nil {
		return gen.BulkJob{}, dbError(err)
	}
	return row, nil
}

// Get returns a job with its status and progress from the core.
func (s *Service) Get(ctx context.Context, p tenancy.Principal, id uuid.UUID) (Job, error) {
	if err := tenancy.Allow(p, tenancy.UseInstances); err != nil {
		return Job{}, err
	}
	row, err := s.owned(ctx, p, id)
	if err != nil {
		return Job{}, err
	}
	job, err := s.core.GetBulkJob(ctx, id.String())
	if err != nil {
		return Job{}, err
	}
	return s.finish(ctx, row, job), nil
}

// Cancel cancels a job. A job that already ended comes back unchanged.
func (s *Service) Cancel(ctx context.Context, p tenancy.Principal, id uuid.UUID) (Job, error) {
	if err := tenancy.Allow(p, tenancy.UseInstances); err != nil {
		return Job{}, err
	}
	row, err := s.owned(ctx, p, id)
	if err != nil {
		return Job{}, err
	}
	job, err := s.core.CancelBulkJob(ctx, id.String())
	if err != nil {
		return Job{}, err
	}
	return s.finish(ctx, row, job), nil
}

// finish adds the instance id and starts the status check after a FAILED job.
func (s *Service) finish(ctx context.Context, row gen.BulkJob, job core.BulkJob) Job {
	if job.Status == core.JobFailed {
		s.inst.TriggerStatusCheck(ctx, row.InstanceID)
	}
	return Job{BulkJob: job, InstanceID: row.InstanceID}
}

// listTimeout is the total time limit of ListForInstance, the database read and the one core call.
var listTimeout = 30 * time.Second

// ListForInstance returns the 50 newest jobs of an instance, with one core call. A job that the
// core no longer has is left out. A core error stops the list.
func (s *Service) ListForInstance(ctx context.Context, p tenancy.Principal, instanceID uuid.UUID) ([]Job, error) {
	if err := tenancy.Allow(p, tenancy.UseInstances); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	inst, err := s.loadInstance(ctx, p, instanceID)
	if err != nil {
		return nil, err
	}
	rows, err := gen.New(s.pool).ListBulkJobsOfInstance(ctx, inst.ID)
	if err != nil {
		return nil, dbError(err)
	}
	if len(rows) == 0 {
		return []Job{}, nil
	}
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID.String()
	}
	found, err := s.core.GetBulkJobs(ctx, ids)
	if err != nil {
		return nil, err
	}
	// The core gives no defined order: put the jobs in the order of the rows.
	byID := make(map[string]core.BulkJob, len(found))
	for _, j := range found {
		byID[j.ID] = j
	}
	jobs := make([]Job, 0, len(found))
	failed := false
	for _, r := range rows {
		j, ok := byID[r.ID.String()]
		if !ok {
			continue
		}
		failed = failed || j.Status == core.JobFailed
		jobs = append(jobs, Job{BulkJob: j, InstanceID: r.InstanceID})
	}
	if failed {
		s.inst.TriggerStatusCheck(ctx, inst.ID)
	}
	return jobs, nil
}
