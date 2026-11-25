package auth

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/database"
	"calyx/backend/internal/database/gen"
	"calyx/backend/internal/tenancy"
)

// disableUser disables the user, deletes its login sessions and revokes its API keys. The caller
// runs it inside a transaction.
func disableUser(ctx context.Context, q *gen.Queries, userID uuid.UUID, now time.Time) error {
	if _, err := q.DisableUser(ctx, gen.DisableUserParams{DisabledAt: now, ID: userID}); err != nil {
		return err
	}
	if err := q.DeleteSessionsOfUser(ctx, userID); err != nil {
		return err
	}
	return q.RevokeAPIKeysOfUser(ctx, gen.RevokeAPIKeysOfUserParams{RevokedAt: now, UserID: userID})
}

// SetUserDisabled disables or enables a user (operator command). Disabling deletes the sessions
// and revokes the API keys of the user. Enabling does not bring the keys back.
func (s *Service) SetUserDisabled(ctx context.Context, email string, disabled bool) error {
	email, err := NormalizeEmail(email)
	if err != nil {
		return err
	}
	user, err := gen.New(s.pool).GetUserByEmail(ctx, email)
	if err != nil {
		return toAPIError(err)
	}
	if disabled {
		now := s.clk.Now()
		err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
			return disableUser(ctx, gen.New(tx), user.ID, now)
		})
		return toAPIError(err)
	}
	if _, err := gen.New(s.pool).EnableUser(ctx, user.ID); err != nil {
		if c, ok := database.UniqueViolation(err); ok &&
			(c == "users_one_active_owner_per_org" || c == "users_one_active_user_per_client") {
			return apierr.Conflict(apierr.CodeConflict, "Another active user has this role. Disable it first.")
		}
		return toAPIError(err)
	}
	return nil
}

// RemoveClientAccess disables the active user of a client (owner only). The instances, the bulk
// jobs and the webhooks of the client do not change. A new invitation to the same email in the
// same client lets the user return. 404 when the client has no active user.
func (s *Service) RemoveClientAccess(ctx context.Context, p tenancy.Principal, clientID uuid.UUID) error {
	if err := tenancy.Allow(p, tenancy.ManageClients); err != nil {
		return err
	}
	now := s.clk.Now()
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := gen.New(tx)
		// The lock makes two removals of one client run one after the other.
		if _, err := q.LockClient(ctx, gen.LockClientParams{ID: clientID, OrganizationID: p.OrganizationID}); err != nil {
			return err
		}
		user, err := q.GetActiveUserOfClient(ctx, clientID)
		if err != nil {
			return err
		}
		return disableUser(ctx, q, user.ID, now)
	})
	if err != nil {
		return toAPIError(err)
	}
	s.log.Info("The access of a client user was removed.", "actor_id", p.UserID, "client_id", clientID)
	return nil
}
