package auth_test

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"calyx/backend/internal/auth"
	"calyx/backend/internal/mail"
)

const resetLinkPrefix = "http://localhost:8080/reset-password?token="

func TestRequestPasswordResetSendsTheMail(t *testing.T) {
	e, _, _ := setup(t)
	if err := e.svc.RequestPasswordReset(context.Background(), "ANA@x.example", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	e.mailer.WaitForAttempts(t, 1)
	sent := e.mailer.Sent()
	if len(sent) != 1 || sent[0].To != "ana@x.example" || sent[0].Subject != "Reset your Calyx password" ||
		!strings.Contains(sent[0].Body, resetLinkPrefix) {
		t.Fatalf("sent = %+v", sent)
	}
	var exp time.Time
	if err := e.pool.QueryRow(context.Background(), `SELECT expires_at FROM password_resets`).Scan(&exp); err != nil || !exp.Equal(start.Add(time.Hour)) {
		t.Fatalf("expires_at = %v, %v", exp, err)
	}
}

func TestResetForAnUnknownEmail(t *testing.T) {
	e, _, _ := setup(t)
	if err := e.svc.RequestPasswordReset(context.Background(), "nobody@x.example", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.WaitBackground(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.mailer.Attempts() != 0 || countRows(t, e.pool, `SELECT count(*) FROM password_resets`) != 0 {
		t.Fatal("a mail or a row was created")
	}
}

func TestResetForADisabledUser(t *testing.T) {
	e, _, _ := setup(t)
	if err := e.svc.SetUserDisabled(context.Background(), "ana@x.example", true); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RequestPasswordReset(context.Background(), "ana@x.example", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.WaitBackground(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.mailer.Attempts() != 0 || countRows(t, e.pool, `SELECT count(*) FROM password_resets`) != 0 {
		t.Fatal("a mail or a row was created")
	}
}

func TestResetWithoutMail(t *testing.T) {
	e, _, _ := setup(t)
	e.mailer.FailWith(mail.ErrNotConfigured)
	if err := e.svc.RequestPasswordReset(context.Background(), "ana@x.example", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	e.mailer.WaitForAttempts(t, 1)
	if len(e.mailer.Sent()) != 0 {
		t.Fatal("a mail was recorded")
	}
}

func TestResetRequestsAreRateLimited(t *testing.T) {
	e, _, _ := setup(t)
	ctx := context.Background()
	for _, email := range []string{"ana@x.example", "nobody@x.example"} {
		for i := 0; i < 5; i++ {
			if err := e.svc.RequestPasswordReset(ctx, email, "10.0.0."+string(rune('1'+i))); err != nil {
				t.Fatal(err)
			}
		}
		requireCode(t, e.svc.RequestPasswordReset(ctx, email, "10.0.0.9"), 429, "rate_limited")
	}
}

func TestConfirmSetsThePasswordAndDeletesEverySession(t *testing.T) {
	e, _, owner := setup(t)
	ctx := context.Background()
	a, b := login(t, e, "ana@x.example"), login(t, e, "ana@x.example")
	key := insertKey(t, e.pool, owner.ID, "k")
	if err := e.svc.RequestPasswordReset(ctx, "ana@x.example", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	e.mailer.WaitForAttempts(t, 1)
	token := tokenOf(t, lastLink(t, e.mailer))
	if err := e.svc.ConfirmPasswordReset(ctx, token, "new password 1", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{a.Token, b.Token} {
		_, err := e.svc.Authenticate(ctx, s, "")
		requireCode(t, err, 401, "unauthenticated")
	}
	if _, err := e.svc.Authenticate(ctx, "", key); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.Login(ctx, "ana@x.example", "new password 1", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	_, _, err := e.svc.Login(ctx, "ana@x.example", password, "1.2.3.4")
	requireCode(t, err, 401, "unauthenticated")
	var used time.Time
	if err := e.pool.QueryRow(ctx, `SELECT used_at FROM password_resets`).Scan(&used); err != nil || !used.Equal(start) {
		t.Fatalf("used_at = %v, %v", used, err)
	}
}

func requestToken(t *testing.T, e *env) string {
	t.Helper()
	before := e.mailer.Attempts()
	if err := e.svc.RequestPasswordReset(context.Background(), "ana@x.example", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	e.mailer.WaitForAttempts(t, before+1)
	return tokenOf(t, lastLink(t, e.mailer))
}

func TestConfirmAnExpiredReset(t *testing.T) {
	e, _, _ := setup(t)
	token := requestToken(t, e)
	e.clk.Advance(time.Hour)
	requireCode(t, e.svc.ConfirmPasswordReset(context.Background(), token, "new password 1", "1.2.3.4"), 410, "token_invalid")
	if _, _, err := e.svc.Login(context.Background(), "ana@x.example", password, "1.2.3.4"); err != nil {
		t.Fatalf("the password changed: %v", err)
	}

	e2, _, _ := setup(t)
	token2 := requestToken(t, e2)
	e2.clk.Advance(time.Hour - time.Second)
	if err := e2.svc.ConfirmPasswordReset(context.Background(), token2, "new password 1", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
}

func TestConfirmTwice(t *testing.T) {
	e, _, _ := setup(t)
	token := requestToken(t, e)
	ctx := context.Background()
	if err := e.svc.ConfirmPasswordReset(ctx, token, "new password 1", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	requireCode(t, e.svc.ConfirmPasswordReset(ctx, token, "new password 2", "1.2.3.4"), 410, "token_invalid")
}

func TestConfirmAWrongToken(t *testing.T) {
	e, _, _ := setup(t)
	requireCode(t, e.svc.ConfirmPasswordReset(context.Background(), "nope", "new password 1", "1.2.3.4"), 410, "token_invalid")
}

func TestConfirmWithAShortPassword(t *testing.T) {
	e, _, _ := setup(t)
	token := requestToken(t, e)
	ctx := context.Background()
	requireCode(t, e.svc.ConfirmPasswordReset(ctx, token, "short", "1.2.3.4"), 400, "invalid_request")
	if err := e.svc.ConfirmPasswordReset(ctx, token, "new password 1", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
}

func TestANewResetReplacesThePendingOne(t *testing.T) {
	e, _, _ := setup(t)
	first := requestToken(t, e)
	second := requestToken(t, e)
	ctx := context.Background()
	requireCode(t, e.svc.ConfirmPasswordReset(ctx, first, "new password 1", "1.2.3.4"), 410, "token_invalid")
	if err := e.svc.ConfirmPasswordReset(ctx, second, "new password 1", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
}

func TestCreatePasswordResetForTheOperator(t *testing.T) {
	e, _, _ := setup(t)
	ctx := context.Background()
	link, err := e.svc.CreatePasswordReset(ctx, "ana@x.example")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(link, resetLinkPrefix) || e.mailer.Attempts() != 0 {
		t.Fatalf("link %q, attempts %d", link, e.mailer.Attempts())
	}
	if err := e.svc.ConfirmPasswordReset(ctx, tokenOf(t, link), "new password 1", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.CreatePasswordReset(ctx, "nobody@x.example")
	requireCode(t, err, 404, "not_found")
}

// waitForLockWaiter blocks until one database session waits for a row lock.
func waitForLockWaiter(t *testing.T, e *env) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if countRows(t, e.pool, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND datname = current_database()`) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no session waits for a lock")
}

func TestConfirmWhenTheResetIsDeletedBeforeTheLock(t *testing.T) {
	e, _, _ := setup(t)
	token := requestToken(t, e)
	ctx := context.Background()
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM password_resets FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- e.svc.ConfirmPasswordReset(ctx, token, "new password 1", "1.2.3.4") }()
	waitForLockWaiter(t, e)
	if _, err := tx.Exec(ctx, `DELETE FROM password_resets`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	requireCode(t, <-done, 410, "token_invalid")
}

func TestChangePasswordDeletesPendingResets(t *testing.T) {
	e, _, owner := setup(t)
	ctx := context.Background()
	link, err := e.svc.CreatePasswordReset(ctx, "ana@x.example")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ChangePassword(ctx, principalOf(owner), "", password, "new password 1"); err != nil {
		t.Fatal(err)
	}
	requireCode(t, e.svc.ConfirmPasswordReset(ctx, tokenOf(t, link), "other password 1", "1.2.3.4"), 410, "token_invalid")
}

// TestResetRequestDoesNoDatabaseWorkBeforeItAnswers closes the pool: a request that touched the
// database before it answered would fail.
func TestResetRequestDoesNoDatabaseWorkBeforeItAnswers(t *testing.T) {
	e, _, _ := setup(t)
	e.pool.Close()
	if err := e.svc.RequestPasswordReset(context.Background(), "ana@x.example", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.WaitBackground(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWaitBackgroundWaitsForTheResetMail(t *testing.T) {
	e, _, _ := setup(t)
	if err := e.svc.RequestPasswordReset(context.Background(), "ana@x.example", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.WaitBackground(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := e.mailer.Attempts(); got != 1 {
		t.Fatalf("mail attempts = %d, want 1", got)
	}
}

// blockingMailer holds every Send until release closes.
type blockingMailer struct{ release chan struct{} }

func (b blockingMailer) Send(ctx context.Context, _ mail.Message) error {
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	return nil
}

func TestWaitBackgroundStopsAtTheDeadline(t *testing.T) {
	e, _, _ := setup(t)
	bm := blockingMailer{release: make(chan struct{})}
	defer close(bm.release)
	pub, _ := url.Parse("http://localhost:8080")
	svc := auth.NewService(e.pool, e.clk, bm, pub, auth.NewLimiter(e.clk))
	if err := svc.RequestPasswordReset(context.Background(), "ana@x.example", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := svc.WaitBackground(ctx); err == nil {
		t.Fatal("WaitBackground returned nil while the mail was blocked")
	}
}

func TestAFailedResetMailWritesOneWarnLine(t *testing.T) {
	buf := captureLog(t)
	e, _, user := setup(t)
	e.mailer.FailWith(fmt.Errorf("send mail: %w", context.DeadlineExceeded))
	if err := e.svc.RequestPasswordReset(context.Background(), "ana@x.example", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.WaitBackground(context.Background()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Count(out, "\n") != 1 || !strings.Contains(out, "level=WARN") ||
		!strings.Contains(out, "kind=password_reset") || !strings.Contains(out, "user_id="+user.ID.String()) ||
		!strings.Contains(out, "error_class=timeout") {
		t.Fatalf("log = %q", out)
	}
	if strings.Contains(out, "ana@x.example") || strings.Contains(out, "token") {
		t.Fatalf("the line holds an address or a token: %q", out)
	}
}

func TestAResetWithoutMailConfiguredWritesNoLine(t *testing.T) {
	buf := captureLog(t)
	e, _, _ := setup(t)
	e.mailer.FailWith(mail.ErrNotConfigured)
	if err := e.svc.RequestPasswordReset(context.Background(), "ana@x.example", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.WaitBackground(context.Background()); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("log = %q", buf.String())
	}
}

// The return locks the user, then deletes the resets. The confirmation must lock in the same
// order, so the two cannot block each other.
func TestConfirmLocksTheUserBeforeTheReset(t *testing.T) {
	e, _, owner := setup(t)
	token := requestToken(t, e)
	ctx := context.Background()
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM users WHERE id=$1 FOR UPDATE`, owner.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- e.svc.ConfirmPasswordReset(ctx, token, "new password 1", "1.2.3.4") }()
	waitForLockWaiter(t, e)
	if _, err := tx.Exec(ctx, `SELECT 1 FROM password_resets FOR UPDATE NOWAIT`); err != nil {
		t.Fatalf("the confirmation locked the reset first: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestLookupPasswordReset(t *testing.T) {
	e, _, _ := setup(t)
	ctx := context.Background()
	token := requestToken(t, e)
	email, err := e.svc.LookupPasswordReset(ctx, token, "1.2.3.4")
	if err != nil || email != "ana@x.example" {
		t.Fatalf("email %q, err %v", email, err)
	}
	// The lookup does not use the token.
	if err := e.svc.ConfirmPasswordReset(ctx, token, "new password 1", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.LookupPasswordReset(ctx, token, "1.2.3.4")
	requireCode(t, err, 410, "token_invalid")
	_, err = e.svc.LookupPasswordReset(ctx, "nope", "1.2.3.4")
	requireCode(t, err, 410, "token_invalid")

	expired := requestToken(t, e)
	e.clk.Advance(time.Hour)
	_, err = e.svc.LookupPasswordReset(ctx, expired, "1.2.3.4")
	requireCode(t, err, 410, "token_invalid")
}

func TestLookupPasswordResetOfADisabledUser(t *testing.T) {
	e, _, _ := setup(t)
	ctx := context.Background()
	link, err := e.svc.CreatePasswordReset(ctx, "ana@x.example")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetUserDisabled(ctx, "ana@x.example", true); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.LookupPasswordReset(ctx, tokenOf(t, link), "1.2.3.4")
	requireCode(t, err, 410, "token_invalid")
}
