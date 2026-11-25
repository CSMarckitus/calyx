package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/database/gen"
	"calyx/backend/internal/tenancy"
)

// Session is a new session. ExpiresAt is the absolute expiry (the life of the cookie); the server
// also enforces the sliding expiry.
type Session struct {
	Token     string
	ExpiresAt time.Time
}

// verifyDummy spends the time of one verification, so an unknown email takes as long as a known one.
func (s *Service) verifyDummy(password string) {
	_, _ = VerifyPassword(s.dummyHash, password)
}

// createSession inserts a new session of the user. It runs inside a transaction.
func createSession(ctx context.Context, q *gen.Queries, userID uuid.UUID, now time.Time) (Session, error) {
	plain, hash := NewToken("")
	abs := now.Add(SessionAbsoluteTTL)
	_, err := q.CreateSession(ctx, gen.CreateSessionParams{
		ID:                uuid.New(),
		TokenHash:         hash,
		UserID:            userID,
		ExpiresAt:         now.Add(SessionSlidingTTL),
		AbsoluteExpiresAt: abs,
		LastSeenAt:        now,
	})
	if err != nil {
		return Session{}, err
	}
	return Session{Token: plain, ExpiresAt: abs}, nil
}

func (s *Service) Login(ctx context.Context, email, password, ip string) (Session, gen.User, error) {
	if err := requireAll("email", email, "password", password); err != nil {
		return Session{}, gen.User{}, err
	}
	email, err := NormalizeEmail(email)
	if err != nil {
		email = "" // an invalid email is an unknown email
	}
	if !s.limiter.Allow("login", ip, email) {
		return Session{}, gen.User{}, rateLimited()
	}
	failed := apierr.New(401, apierr.CodeUnauthenticated, detailLoginFailed)
	q := gen.New(s.pool)
	var user gen.User
	if email != "" {
		user, err = q.GetUserByEmail(ctx, email)
	} else {
		err = pgx.ErrNoRows
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			s.verifyDummy(password)
			return Session{}, gen.User{}, failed
		}
		return Session{}, gen.User{}, apierr.Internal(err)
	}
	ok, err := VerifyPassword(user.PasswordHash, password)
	if err != nil {
		return Session{}, gen.User{}, apierr.Internal(err)
	}
	if !ok || user.DisabledAt != nil {
		return Session{}, gen.User{}, failed
	}
	now := s.clk.Now()
	var sess Session
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		qt := gen.New(tx)
		if err := qt.DeleteExpiredSessionsOfUser(ctx, gen.DeleteExpiredSessionsOfUserParams{UserID: user.ID, Now: now}); err != nil {
			return err
		}
		var err error
		sess, err = createSession(ctx, qt, user.ID, now)
		return err
	})
	if err != nil {
		return Session{}, gen.User{}, toAPIError(err)
	}
	return sess, user, nil
}

// Logout deletes the session. An unknown token is not an error.
func (s *Service) Logout(ctx context.Context, sessionToken string) error {
	if err := gen.New(s.pool).DeleteSessionByTokenHash(ctx, HashToken(sessionToken)); err != nil {
		return toAPIError(err)
	}
	return nil
}

// checked is the result of a check: the principal, and what the touch of Authenticate needs.
type checked struct {
	principal tenancy.Principal
	sessionID uuid.UUID
	expiresAt time.Time // the new sliding expiry of the session
	expired   bool      // the session is past its expiry; Authenticate deletes it
}

// Check resolves a bearer API key when bearer is set, else a session token. It never falls back
// from a bad bearer to the session. It writes nothing, so a stream can repeat it without extending
// the session.
func (s *Service) Check(ctx context.Context, sessionToken, bearer string) (tenancy.Principal, error) {
	c, err := s.check(ctx, sessionToken, bearer)
	return c.principal, err
}

// Authenticate is Check, then the touch: it slides the session or updates the last use of the key.
// It also deletes an expired session.
func (s *Service) Authenticate(ctx context.Context, sessionToken, bearer string) (tenancy.Principal, error) {
	c, err := s.check(ctx, sessionToken, bearer)
	if c.expired {
		if derr := gen.New(s.pool).DeleteSession(ctx, c.sessionID); derr != nil {
			return tenancy.Principal{}, apierr.Internal(derr)
		}
	}
	if err != nil {
		return tenancy.Principal{}, err
	}
	q := gen.New(s.pool)
	now := s.clk.Now()
	if c.principal.APIKeyID != nil {
		if err := q.TouchAPIKey(ctx, gen.TouchAPIKeyParams{LastUsedAt: now, ID: *c.principal.APIKeyID}); err != nil {
			return tenancy.Principal{}, apierr.Internal(err)
		}
		return c.principal, nil
	}
	if err := q.TouchSession(ctx, gen.TouchSessionParams{ExpiresAt: c.expiresAt, LastSeenAt: now, ID: c.sessionID}); err != nil {
		return tenancy.Principal{}, apierr.Internal(err)
	}
	return c.principal, nil
}

func (s *Service) check(ctx context.Context, sessionToken, bearer string) (checked, error) {
	switch {
	case bearer != "":
		return s.checkKey(ctx, bearer)
	case sessionToken != "":
		return s.checkSession(ctx, sessionToken)
	}
	return checked{}, unauthenticated()
}

func (s *Service) checkKey(ctx context.Context, bearer string) (checked, error) {
	if !strings.HasPrefix(bearer, APIKeyPrefix) {
		return checked{}, unauthenticated()
	}
	row, err := gen.New(s.pool).GetAPIKeyForAuth(ctx, HashToken(bearer))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return checked{}, unauthenticated()
		}
		return checked{}, apierr.Internal(err)
	}
	if row.DisabledAt != nil {
		return checked{}, unauthenticated()
	}
	keyID := row.ID
	return checked{principal: tenancy.Principal{UserID: row.UserID, OrganizationID: row.OrganizationID, Role: tenancy.Role(row.Role), ClientID: row.ClientID, APIKeyID: &keyID}}, nil
}

func (s *Service) checkSession(ctx context.Context, token string) (checked, error) {
	row, err := gen.New(s.pool).GetSessionForAuth(ctx, HashToken(token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return checked{}, unauthenticated()
		}
		return checked{}, apierr.Internal(err)
	}
	now := s.clk.Now()
	if !now.Before(row.ExpiresAt) || !now.Before(row.AbsoluteExpiresAt) {
		return checked{sessionID: row.ID, expired: true}, unauthenticated()
	}
	if row.DisabledAt != nil {
		return checked{}, unauthenticated()
	}
	expires := now.Add(SessionSlidingTTL)
	if expires.After(row.AbsoluteExpiresAt) {
		expires = row.AbsoluteExpiresAt
	}
	return checked{
		principal: tenancy.Principal{UserID: row.UserID, OrganizationID: row.OrganizationID, Role: tenancy.Role(row.Role), ClientID: row.ClientID},
		sessionID: row.ID,
		expiresAt: expires,
	}, nil
}

// ChangePassword sets a new password. It deletes the other sessions of the user (all of them when
// the call came with an API key). API keys stay.
func (s *Service) ChangePassword(ctx context.Context, p tenancy.Principal, currentSessionToken, current, next string) error {
	if err := tenancy.Allow(p, tenancy.ManageOwnAccount); err != nil {
		return err
	}
	if err := requireAll("current_password", current, "new_password", next); err != nil {
		return err
	}
	if err := ValidatePassword("new_password", next); err != nil {
		return err
	}
	user, err := gen.New(s.pool).GetUserByID(ctx, p.UserID)
	if err != nil {
		return toAPIError(err)
	}
	ok, err := VerifyPassword(user.PasswordHash, current)
	if err != nil {
		return apierr.Internal(err)
	}
	if !ok {
		return apierr.InvalidFields(apierr.FieldError{Field: "current_password", Message: "is wrong."})
	}
	hash, err := HashPassword(next)
	if err != nil {
		return apierr.Internal(err)
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := gen.New(tx)
		if err := q.UpdateUserPassword(ctx, gen.UpdateUserPasswordParams{PasswordHash: hash, ID: user.ID}); err != nil {
			return err
		}
		if err := q.DeletePendingPasswordResets(ctx, user.ID); err != nil {
			return err
		}
		if currentSessionToken != "" {
			return q.DeleteOtherSessionsOfUser(ctx, gen.DeleteOtherSessionsOfUserParams{UserID: user.ID, KeepTokenHash: HashToken(currentSessionToken)})
		}
		return q.DeleteSessionsOfUser(ctx, user.ID)
	})
	return toAPIError(err)
}
