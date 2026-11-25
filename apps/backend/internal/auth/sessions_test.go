package auth_test

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"

	"calyx/backend/internal/auth"
	"calyx/backend/internal/tenancy"
)

const day = 24 * time.Hour

func login(t *testing.T, e *env, email string) auth.Session {
	t.Helper()
	s, _, err := e.svc.Login(context.Background(), email, password, "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLoginCreatesASession(t *testing.T) {
	e, _, owner := setup(t)
	s, u, err := e.svc.Login(context.Background(), "ana@x.example", password, "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Token) != 43 || !s.ExpiresAt.Equal(start.Add(90*day)) || u.ID != owner.ID {
		t.Fatalf("session %d chars, expires %v, user %v", len(s.Token), s.ExpiresAt, u.ID)
	}
	h := sha256.Sum256([]byte(s.Token))
	var exp, abs, seen time.Time
	err = e.pool.QueryRow(context.Background(),
		`SELECT expires_at, absolute_expires_at, last_seen_at FROM sessions WHERE token_hash = $1`, h[:]).Scan(&exp, &abs, &seen)
	if err != nil {
		t.Fatal(err)
	}
	if !exp.Equal(start.Add(30*day)) || !abs.Equal(start.Add(90*day)) || !seen.Equal(start) {
		t.Fatalf("row: %v %v %v", exp, abs, seen)
	}
}

func TestLoginIgnoresEmailCaseAndSpaces(t *testing.T) {
	e, _, _ := setup(t)
	if _, _, err := e.svc.Login(context.Background(), "  ANA@X.example ", password, "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
}

func TestLoginWithAWrongPassword(t *testing.T) {
	e, _, _ := setup(t)
	_, _, err := e.svc.Login(context.Background(), "ana@x.example", "wrong password!", "1.2.3.4")
	requireCode(t, err, 401, "unauthenticated")
	if n := countRows(t, e.pool, `SELECT count(*) FROM sessions`); n != 0 {
		t.Fatalf("sessions = %d", n)
	}
}

func detailOf(t *testing.T, err error) string {
	t.Helper()
	requireCode(t, err, 401, "unauthenticated")
	return err.Error()
}

func TestLoginWithAnUnknownEmail(t *testing.T) {
	e, _, _ := setup(t)
	_, _, err1 := e.svc.Login(context.Background(), "nobody@x.example", password, "1.2.3.4")
	_, _, err2 := e.svc.Login(context.Background(), "ana@x.example", "wrong password!", "1.2.3.4")
	if detailOf(t, err1) != detailOf(t, err2) {
		t.Fatal("the details differ")
	}
}

func TestADisabledUserCannotLogIn(t *testing.T) {
	e, _, _ := setup(t)
	if _, err := e.pool.Exec(context.Background(), `UPDATE users SET disabled_at = now()`); err != nil {
		t.Fatal(err)
	}
	_, _, err1 := e.svc.Login(context.Background(), "ana@x.example", password, "1.2.3.4")
	_, _, err2 := e.svc.Login(context.Background(), "nobody@x.example", password, "1.2.3.4")
	if detailOf(t, err1) != detailOf(t, err2) {
		t.Fatal("the details differ")
	}
}

func TestLoginIsRateLimitedPerEmail(t *testing.T) {
	e, _, _ := setup(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_, _, err := e.svc.Login(ctx, "ana@x.example", "wrong password!", "10.0.0."+string(rune('1'+i)))
		requireCode(t, err, 401, "unauthenticated")
	}
	_, _, err := e.svc.Login(ctx, "ana@x.example", password, "10.0.0.9")
	requireCode(t, err, 429, "rate_limited")
	e.clk.Advance(time.Minute)
	if _, _, err := e.svc.Login(ctx, "ana@x.example", password, "10.0.0.9"); err != nil {
		t.Fatal(err)
	}
}

func TestLoginDeletesExpiredSessionsOfTheUser(t *testing.T) {
	e, _, owner := setup(t)
	login(t, e, "ana@x.example")
	e.clk.Advance(31 * day)
	login(t, e, "ana@x.example")
	if n := countRows(t, e.pool, `SELECT count(*) FROM sessions WHERE user_id = $1`, owner.ID); n != 1 {
		t.Fatalf("sessions = %d", n)
	}
}

func TestTheSessionAuthenticates(t *testing.T) {
	e, org, owner := setup(t)
	s := login(t, e, "ana@x.example")
	p, err := e.svc.Authenticate(context.Background(), s.Token, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.UserID != owner.ID || p.OrganizationID != org || p.Role != tenancy.RoleOwner || p.ClientID != nil {
		t.Fatalf("principal = %+v", p)
	}
	cid := newClient(t, e.pool, org, "Gym")
	cu := newUser(t, e.pool, org, &cid, "gym@x.example")
	s2 := login(t, e, "gym@x.example")
	p2, err := e.svc.Authenticate(context.Background(), s2.Token, "")
	if err != nil {
		t.Fatal(err)
	}
	if p2.UserID != cu.ID || p2.Role != tenancy.RoleClient || p2.ClientID == nil || *p2.ClientID != cid {
		t.Fatalf("principal = %+v", p2)
	}
}

func TestUnknownOrEmptyCredentials(t *testing.T) {
	e, _, _ := setup(t)
	_, err := e.svc.Authenticate(context.Background(), "nope", "")
	requireCode(t, err, 401, "unauthenticated")
	_, err = e.svc.Authenticate(context.Background(), "", "")
	requireCode(t, err, 401, "unauthenticated")
}

func sessionTimes(t *testing.T, e *env, token string) (exp, seen time.Time) {
	t.Helper()
	h := sha256.Sum256([]byte(token))
	err := e.pool.QueryRow(context.Background(), `SELECT expires_at, last_seen_at FROM sessions WHERE token_hash = $1`, h[:]).Scan(&exp, &seen)
	if err != nil {
		t.Fatal(err)
	}
	return exp, seen
}

func TestTheSessionSlides(t *testing.T) {
	e, _, _ := setup(t)
	s := login(t, e, "ana@x.example")
	ctx := context.Background()
	e.clk.Advance(29 * day)
	if _, err := e.svc.Authenticate(ctx, s.Token, ""); err != nil {
		t.Fatal(err)
	}
	exp, seen := sessionTimes(t, e, s.Token)
	if !exp.Equal(start.Add(59*day)) || !seen.Equal(start.Add(29*day)) {
		t.Fatalf("%v %v", exp, seen)
	}
	e.clk.Advance(29 * day)
	if _, err := e.svc.Authenticate(ctx, s.Token, ""); err != nil {
		t.Fatal(err)
	}
	exp, _ = sessionTimes(t, e, s.Token)
	if !exp.Equal(start.Add(88 * day)) {
		t.Fatalf("expires %v", exp)
	}
}

func TestASessionUnusedFor30DaysExpires(t *testing.T) {
	e, _, _ := setup(t)
	s := login(t, e, "ana@x.example")
	e.clk.Advance(30*day - time.Second)
	if _, err := e.svc.Authenticate(context.Background(), s.Token, ""); err != nil {
		t.Fatal(err)
	}

	e2, _, _ := setup(t)
	s2 := login(t, e2, "ana@x.example")
	e2.clk.Advance(30 * day)
	_, err := e2.svc.Authenticate(context.Background(), s2.Token, "")
	requireCode(t, err, 401, "unauthenticated")
	if n := countRows(t, e2.pool, `SELECT count(*) FROM sessions`); n != 0 {
		t.Fatalf("sessions = %d", n)
	}
}

func TestTheAbsoluteExpiryIs90Days(t *testing.T) {
	e, _, _ := setup(t)
	s := login(t, e, "ana@x.example")
	ctx := context.Background()
	for _, d := range []int{20, 40, 60, 80} {
		e.clk.Advance(20 * day)
		if _, err := e.svc.Authenticate(ctx, s.Token, ""); err != nil {
			t.Fatalf("day %d: %v", d, err)
		}
	}
	exp, _ := sessionTimes(t, e, s.Token)
	if !exp.Equal(start.Add(90 * day)) {
		t.Fatalf("expires %v", exp)
	}
	e.clk.Advance(10 * day)
	_, err := e.svc.Authenticate(ctx, s.Token, "")
	requireCode(t, err, 401, "unauthenticated")
}

func TestLogout(t *testing.T) {
	e, _, _ := setup(t)
	s := login(t, e, "ana@x.example")
	ctx := context.Background()
	if err := e.svc.Logout(ctx, s.Token); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Authenticate(ctx, s.Token, "")
	requireCode(t, err, 401, "unauthenticated")
	if err := e.svc.Logout(ctx, s.Token); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Logout(ctx, "unknown"); err != nil {
		t.Fatal(err)
	}
}

func TestChangePasswordKeepsOnlyTheCurrentSession(t *testing.T) {
	e, _, owner := setup(t)
	ctx := context.Background()
	a, b := login(t, e, "ana@x.example"), login(t, e, "ana@x.example")
	if err := e.svc.ChangePassword(ctx, principalOf(owner), a.Token, password, "new password 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Authenticate(ctx, a.Token, ""); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Authenticate(ctx, b.Token, "")
	requireCode(t, err, 401, "unauthenticated")
	if _, _, err := e.svc.Login(ctx, "ana@x.example", "new password 1", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	_, _, err = e.svc.Login(ctx, "ana@x.example", password, "1.2.3.4")
	requireCode(t, err, 401, "unauthenticated")
}

func TestChangePasswordWithAWrongCurrentPassword(t *testing.T) {
	e, _, owner := setup(t)
	ctx := context.Background()
	a, b := login(t, e, "ana@x.example"), login(t, e, "ana@x.example")
	err := e.svc.ChangePassword(ctx, principalOf(owner), a.Token, "not the password", "new password 1")
	requireFields(t, err, "current_password")
	for _, s := range []auth.Session{a, b} {
		if _, err := e.svc.Authenticate(ctx, s.Token, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := e.svc.Login(ctx, "ana@x.example", password, "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
}

func TestChangePasswordValidatesTheNewPassword(t *testing.T) {
	e, _, owner := setup(t)
	a := login(t, e, "ana@x.example")
	err := e.svc.ChangePassword(context.Background(), principalOf(owner), a.Token, password, "short")
	requireCode(t, err, 400, "invalid_request")
	if _, _, err := e.svc.Login(context.Background(), "ana@x.example", password, "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
}

func TestChangePasswordThroughAnAPIKey(t *testing.T) {
	e, _, owner := setup(t)
	ctx := context.Background()
	p := principalOf(owner)
	a, b := login(t, e, "ana@x.example"), login(t, e, "ana@x.example")
	key := insertKey(t, e.pool, owner.ID, "n8n")
	if err := e.svc.ChangePassword(ctx, p, "", password, "new password 1"); err != nil {
		t.Fatal(err)
	}
	for _, s := range []auth.Session{a, b} {
		_, err := e.svc.Authenticate(ctx, s.Token, "")
		requireCode(t, err, 401, "unauthenticated")
	}
	if _, err := e.svc.Authenticate(ctx, "", key); err != nil {
		t.Fatal(err)
	}
}

var _ = uuid.Nil

func TestCheckDoesNotWrite(t *testing.T) {
	e, org, owner := setup(t)
	ctx := context.Background()
	s := login(t, e, "ana@x.example")
	k, _ := e.svc.CreateAPIKey(ctx, principalOf(owner), "k")
	exp0, seen0 := sessionTimes(t, e, s.Token)
	e.clk.Advance(5 * day)
	p, err := e.svc.Check(ctx, s.Token, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.UserID != owner.ID || p.OrganizationID != org || p.APIKeyID != nil {
		t.Fatalf("principal = %+v", p)
	}
	exp, seen := sessionTimes(t, e, s.Token)
	if !exp.Equal(exp0) || !seen.Equal(seen0) {
		t.Fatalf("session changed: %v %v", exp, seen)
	}
	pk, err := e.svc.Check(ctx, "", k.Token)
	if err != nil {
		t.Fatal(err)
	}
	if pk.APIKeyID == nil || *pk.APIKeyID != k.ID {
		t.Fatalf("APIKeyID = %v", pk.APIKeyID)
	}
	var used *time.Time
	if err := e.pool.QueryRow(ctx, `SELECT last_used_at FROM api_keys WHERE id=$1`, k.ID).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used != nil {
		t.Fatalf("last_used_at = %v, want null", used)
	}
}

func TestCheckRefusesWhatAuthenticateRefuses(t *testing.T) {
	ctx := context.Background()
	e, _, owner := setup(t)
	s := login(t, e, "ana@x.example")
	k, _ := e.svc.CreateAPIKey(ctx, principalOf(owner), "k")
	_, err := e.svc.Check(ctx, "nope", "")
	requireCode(t, err, 401, "unauthenticated")
	_, err = e.svc.Check(ctx, "", "")
	requireCode(t, err, 401, "unauthenticated")
	if err := e.svc.RevokeAPIKey(ctx, principalOf(owner), k.ID); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Check(ctx, "", k.Token)
	requireCode(t, err, 401, "unauthenticated")
	if _, err := e.pool.Exec(ctx, `UPDATE users SET disabled_at = now() WHERE id = $1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Check(ctx, s.Token, "")
	requireCode(t, err, 401, "unauthenticated")

	e2, _, _ := setup(t)
	s2 := login(t, e2, "ana@x.example")
	e2.clk.Advance(30 * day)
	_, err = e2.svc.Check(ctx, s2.Token, "")
	requireCode(t, err, 401, "unauthenticated")
	if n := countRows(t, e2.pool, `SELECT count(*) FROM sessions`); n != 1 {
		t.Fatalf("Check deleted the session: %d", n)
	}
}
