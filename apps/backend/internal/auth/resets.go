package auth

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/database/gen"
	"calyx/backend/internal/mail"
)

const ResetTTL = time.Hour

// resetWorkTimeout limits the whole background work of one reset request.
const resetWorkTimeout = 30 * time.Second

const detailResetInvalid = "The reset link is invalid or has expired."

func resetGone() error { return apierr.New(410, apierr.CodeTokenInvalid, detailResetInvalid) }

// newReset replaces the pending reset of the user and returns the link of the new one.
func (s *Service) newReset(ctx context.Context, userID uuid.UUID) (string, error) {
	plain, hash := NewToken("")
	expires := s.clk.Now().Add(ResetTTL)
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := gen.New(tx)
		if err := q.DeletePendingPasswordResets(ctx, userID); err != nil {
			return err
		}
		_, err := q.CreatePasswordReset(ctx, gen.CreatePasswordResetParams{
			ID: uuid.New(), UserID: userID, TokenHash: hash, ExpiresAt: expires,
		})
		return err
	})
	if err != nil {
		return "", toAPIError(err)
	}
	return s.link("/reset-password", plain), nil
}

// RequestPasswordReset is the self-service request. It answers nil for an unknown or disabled
// email, and ignores a failed mail, so the answer shows nothing about the account.
func (s *Service) RequestPasswordReset(ctx context.Context, email, ip string) error {
	email, err := NormalizeEmail(email)
	if err != nil {
		return err // the syntax only: the answer still shows nothing about the account
	}
	if !s.limiter.Allow("reset", ip, email) {
		return rateLimited()
	}
	// All the work runs in a goroutine: the answer must take the same time for a known and for an
	// unknown email. Errors are ignored: without mail, nothing visible happens.
	workCtx := context.WithoutCancel(ctx)
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		s.sendReset(workCtx, email)
	}()
	return nil
}

// sendReset creates the reset of the user and sends the mail. It does nothing for an unknown or
// disabled email.
func (s *Service) sendReset(ctx context.Context, email string) {
	ctx, cancel := context.WithTimeout(ctx, resetWorkTimeout)
	defer cancel()
	user, err := gen.New(s.pool).GetUserByEmail(ctx, email)
	if err != nil || user.DisabledAt != nil {
		return
	}
	link, err := s.newReset(ctx, user.ID)
	if err != nil {
		return
	}
	data := mail.TemplateData{To: email, Link: link, PublicURL: s.publicURL.String()}
	if err := s.sendMail(ctx, "password_reset", data); err != nil {
		s.logMailFailure("password_reset", err, "user_id", user.ID)
	}
}

// CreatePasswordReset is the operator command. It sends no mail and returns the link. A disabled
// user also gets a link: a new password does not enable the user.
func (s *Service) CreatePasswordReset(ctx context.Context, email string) (link string, err error) {
	email, err = NormalizeEmail(email)
	if err != nil {
		return "", err
	}
	user, err := gen.New(s.pool).GetUserByEmail(ctx, email)
	if err != nil {
		return "", toAPIError(err)
	}
	return s.newReset(ctx, user.ID)
}

// ConfirmPasswordReset sets the new password and deletes every session of the user. API keys stay.
func (s *Service) ConfirmPasswordReset(ctx context.Context, token, password, ip string) error {
	if err := requireAll("token", token, "password", password); err != nil {
		return err
	}
	if !s.limiter.Allow("accept", ip, "") {
		return rateLimited()
	}
	now := s.clk.Now()
	q := gen.New(s.pool)
	reset, err := q.GetPasswordResetByTokenHash(ctx, HashToken(token))
	if err != nil {
		if isNoRows(err) {
			return resetGone()
		}
		return apierr.Internal(err)
	}
	if reset.UsedAt != nil || !now.Before(reset.ExpiresAt) {
		return resetGone()
	}
	user, err := q.GetUserByID(ctx, reset.UserID)
	if err != nil {
		return toAPIError(err)
	}
	if !s.limiter.Allow("accept", "", user.Email) {
		return rateLimited()
	}
	if err := ValidatePassword("password", password); err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return apierr.Internal(err)
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		qt := gen.New(tx)
		// The user first, then the reset: the order of the return in AcceptInvitation. With
		// another order, the two can block each other.
		if _, err := qt.LockUserByEmail(ctx, user.Email); err != nil {
			return err
		}
		locked, err := qt.LockPasswordReset(ctx, reset.ID)
		if err != nil {
			if isNoRows(err) {
				return resetGone() // replaced or deleted since the lookup
			}
			return err
		}
		if locked.UsedAt != nil || !now.Before(locked.ExpiresAt) {
			return resetGone()
		}
		if err := qt.UpdateUserPassword(ctx, gen.UpdateUserPasswordParams{PasswordHash: hash, ID: user.ID}); err != nil {
			return err
		}
		if err := qt.MarkPasswordResetUsed(ctx, gen.MarkPasswordResetUsedParams{UsedAt: now, ID: locked.ID}); err != nil {
			return err
		}
		return qt.DeleteSessionsOfUser(ctx, user.ID)
	})
	return toAPIError(err)
}

// LookupPasswordReset returns the email of a reset token and does not use it. Unknown, used and
// expired tokens, and a disabled user, give the same 410.
func (s *Service) LookupPasswordReset(ctx context.Context, token, ip string) (email string, err error) {
	if err := requireAll("token", token); err != nil {
		return "", err
	}
	if !s.limiter.Allow("lookup", ip, "") {
		return "", rateLimited()
	}
	q := gen.New(s.pool)
	reset, err := q.GetPasswordResetByTokenHash(ctx, HashToken(token))
	if err != nil {
		if isNoRows(err) {
			return "", resetGone()
		}
		return "", apierr.Internal(err)
	}
	if reset.UsedAt != nil || !s.clk.Now().Before(reset.ExpiresAt) {
		return "", resetGone()
	}
	user, err := q.GetUserByID(ctx, reset.UserID)
	if err != nil {
		return "", apierr.Internal(err)
	}
	if user.DisabledAt != nil {
		return "", resetGone()
	}
	return user.Email, nil
}
