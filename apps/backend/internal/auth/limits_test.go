package auth_test

import (
	"context"
	"strings"
	"testing"

	"calyx/backend/internal/auth"
)

// Each limit is accepted at its value and refused one step past it, with the field path.

func TestEmailLimit(t *testing.T) {
	at := strings.Repeat("a", 254-len("@x.example")) + "@x.example"
	if _, err := auth.NormalizeEmail(at); err != nil {
		t.Fatalf("254 bytes: %v", err)
	}
	_, err := auth.NormalizeEmail("a" + at)
	requireFields(t, err, "email")
	_, err = auth.NormalizeEmail(" ")
	requireFields(t, err, "email")
}

func TestEmailRuleOnEveryMethod(t *testing.T) {
	e, org, owner := setup(t)
	ctx := context.Background()
	cid := newClient(t, e.pool, org, "Gym")
	requireFields(t, e.svc.RequestPasswordReset(ctx, "not-an-email", "1.2.3.4"), "email")
	_, err := e.svc.InviteClientUser(ctx, principalOf(owner), cid, "not-an-email")
	requireFields(t, err, "email")
	_, err = e.svc.InviteOwner(ctx, org, "not-an-email", nil)
	requireFields(t, err, "email")
	requireFields(t, e.svc.SetUserDisabled(ctx, "not-an-email", true), "email")
	_, err = e.svc.CreatePasswordReset(ctx, "not-an-email")
	requireFields(t, err, "email")
}

func TestPasswordLimitsAndRequiredFields(t *testing.T) {
	e, _, owner := setup(t)
	ctx := context.Background()
	p := principalOf(owner)
	requireFields(t, e.svc.ChangePassword(ctx, p, "", "", ""), "current_password", "new_password")
	requireFields(t, e.svc.ChangePassword(ctx, p, "", password, strings.Repeat("é", auth.MinPasswordLength-1)), "new_password")
	requireFields(t, e.svc.ChangePassword(ctx, p, "", password, strings.Repeat("é", auth.MaxPasswordLength+1)), "new_password")
	requireFields(t, e.svc.ChangePassword(ctx, p, "", "not the password", "a good password"), "current_password")
	if err := e.svc.ChangePassword(ctx, p, "", password, strings.Repeat("é", auth.MaxPasswordLength)); err != nil {
		t.Fatalf("128 runes: %v", err)
	}
	_, _, err := e.svc.Login(ctx, "", "", "1.2.3.4")
	requireFields(t, err, "email", "password")
	requireFields(t, e.svc.ConfirmPasswordReset(ctx, "", "", "1.2.3.4"), "token", "password")
	_, _, err = e.svc.AcceptInvitation(ctx, "", "", "1.2.3.4")
	requireFields(t, err, "token", "password")
	_, err = e.svc.LookupInvitation(ctx, "", "1.2.3.4")
	requireFields(t, err, "token")
	_, err = e.svc.LookupPasswordReset(ctx, "", "1.2.3.4")
	requireFields(t, err, "token")
}

func TestAPIKeyNameLimit(t *testing.T) {
	e, _, owner := setup(t)
	ctx := context.Background()
	p := principalOf(owner)
	if _, err := e.svc.CreateAPIKey(ctx, p, " "+strings.Repeat("é", auth.MaxAPIKeyNameLength)+" "); err != nil {
		t.Fatalf("limit: %v", err)
	}
	_, err := e.svc.CreateAPIKey(ctx, p, strings.Repeat("é", auth.MaxAPIKeyNameLength+1))
	requireFields(t, err, "name")
	_, err = e.svc.CreateAPIKey(ctx, p, "a\x01b")
	requireFields(t, err, "name")
}
