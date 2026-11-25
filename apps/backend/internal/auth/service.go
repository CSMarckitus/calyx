package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/clock"
	"calyx/backend/internal/mail"
)

const (
	SessionSlidingTTL  = 30 * 24 * time.Hour
	SessionAbsoluteTTL = 90 * 24 * time.Hour
	MailTimeout        = 15 * time.Second
)

const (
	detailLoginFailed     = "The email or the password is wrong."
	detailUnauthenticated = "Log in or send a valid API key."
	detailRateLimited     = "Too many attempts. Wait one minute and try again."
)

// Service holds the identity rules. Its log lines are the WARN lines of a failed mail, and one INFO
// line for a removed access, a cancelled invitation and a return. They hold ids, a kind and an
// error class, never a secret or an address. The logger is slog.Default(), as in
// instances.Service.
type Service struct {
	log       *slog.Logger
	pool      *pgxpool.Pool
	clk       clock.Clock
	mailer    mail.Sender
	publicURL *url.URL
	limiter   *Limiter
	// dummyHash is computed once. A login with an unknown email verifies against it.
	dummyHash string
	// bg tracks the reset work that runs after the answer. See WaitBackground.
	bg sync.WaitGroup
}

func NewService(pool *pgxpool.Pool, clk clock.Clock, mailer mail.Sender, publicURL *url.URL, limiter *Limiter) *Service {
	dummy, _ := HashPassword("a fixed text for unknown emails")
	return &Service{log: slog.Default(), pool: pool, clk: clk, mailer: mailer, publicURL: publicURL, limiter: limiter, dummyHash: dummy}
}

// WaitBackground waits until the background reset work ends or ctx ends. The shutdown calls it,
// so a reset mail that is in progress is not lost.
func (s *Service) WaitBackground(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.bg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// toAPIError keeps an *apierr.Error, maps pgx.ErrNoRows to 404 and any other error to 500.
func toAPIError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := apierr.As(err); ok {
		return err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return apierr.NotFound()
	}
	return apierr.Internal(err)
}

func rateLimited() error { return apierr.New(429, apierr.CodeRateLimited, detailRateLimited) }

func unauthenticated() error {
	return apierr.New(401, apierr.CodeUnauthenticated, detailUnauthenticated)
}

func (s *Service) link(path, token string) string {
	return s.publicURL.String() + path + "?token=" + token
}
