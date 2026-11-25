package instances

import (
	"context"

	"github.com/google/uuid"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/database/gen"
	"calyx/backend/internal/tenancy"
)

// Remove removes an instance from the core and then from the database. removed is false when the
// core did not answer: the row stays "removing" and the watcher retries (HTTP 202).
func (s *Service) Remove(ctx context.Context, p tenancy.Principal, id uuid.UUID) (removed bool, err error) {
	if err := tenancy.Allow(p, tenancy.UseInstances); err != nil {
		return false, err
	}
	row, err := s.load(ctx, p, id)
	if err != nil {
		return false, err
	}
	q := gen.New(s.pool)
	switch row.State {
	case "creating":
		return false, apierr.Conflict(apierr.CodePairingInProgress, "Cancel the pairing first.")
	case "missing":
		if err := q.DeleteInstance(ctx, row.ID); err != nil {
			return false, dbError(err)
		}
		s.published(row)
		return true, nil
	case "active":
		if s.pairings.Running(row.ID) {
			return false, apierr.Conflict(apierr.CodePairingInProgress, "A pairing is running for this instance. Cancel it first.")
		}
		n, err := q.SetInstanceState(ctx, gen.SetInstanceStateParams{ID: row.ID, FromState: "active", ToState: "removing"})
		if err != nil {
			return false, dbError(err)
		}
		if n == 0 {
			return false, apierr.Conflict(apierr.CodeConflict, "The instance changed. Read it and try again.")
		}
		s.published(row)
		// A pairing can start between the first check and the state change, so check once more.
		// The race is closed from both sides. The state "removing" is already saved here. A Pair
		// that put its session in the map before this check is seen by it. A Pair that does it
		// later reads the row again inside its session, sees "removing" and ends before the
		// core call.
		if s.pairings.Running(row.ID) {
			if _, err := q.SetInstanceState(ctx, gen.SetInstanceStateParams{ID: row.ID, FromState: "removing", ToState: "active"}); err != nil {
				return false, dbError(err)
			}
			s.published(row)
			return false, apierr.Conflict(apierr.CodePairingInProgress, "A pairing is running for this instance. Cancel it first.")
		}
	}

	coreErr := s.core.RemoveInstance(ctx, row.CoreName)
	// The writes after the core call must not stop because the request ended.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dbWriteTimeout)
	defer cancel()
	wq := gen.New(s.pool)
	if coreErr == nil || isCode(coreErr, apierr.CodeNotFound) {
		if err := wq.DeleteInstance(wctx, row.ID); err != nil {
			return false, dbError(err)
		}
		s.published(row)
		return true, nil
	}
	if ae, ok := apierr.As(coreErr); !ok || ae.Status == 503 || ae.Status == 504 {
		return false, nil // the row stays "removing"; the watcher retries
	}
	if _, err := wq.SetInstanceState(wctx, gen.SetInstanceStateParams{ID: row.ID, FromState: "removing", ToState: "active"}); err != nil {
		return false, dbError(err)
	}
	s.published(row)
	return false, coreErr
}

func isCode(err error, code string) bool {
	ae, ok := apierr.As(err)
	return ok && ae.Code == code
}
