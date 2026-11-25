package auth_test

import (
	"context"
	"errors"
	"fmt"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"calyx/backend/internal/auth"
	"calyx/backend/internal/database/gen"
	"calyx/backend/internal/mail"
)

const inviteLinkPrefix = "http://localhost:8080/invite?token="

// ownerless creates an environment with an organization that has no owner.
func ownerless(t *testing.T) (*env, uuid.UUID) {
	t.Helper()
	e := newEnv(t)
	org, _ := newOrg(t, e.pool)
	return e, org
}

func TestInviteOwnerSendsTheMail(t *testing.T) {
	e, org := ownerless(t)
	res, err := e.svc.InviteOwner(context.Background(), org, "Ana@X.example", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Email != "ana@x.example" || !res.ExpiresAt.Equal(start.Add(7*day)) || !res.MailSent || res.Link != "" {
		t.Fatalf("result = %+v", res)
	}
	sent := e.mailer.Sent()
	if len(sent) != 1 || sent[0].To != "ana@x.example" || !strings.Contains(sent[0].Subject, "Org") ||
		!strings.Contains(sent[0].Body, inviteLinkPrefix) {
		t.Fatalf("sent = %+v", sent)
	}
	if n := countRows(t, e.pool, `SELECT count(*) FROM invitations WHERE role='owner' AND client_id IS NULL AND organization_id=$1`, org); n != 1 {
		t.Fatalf("rows = %d", n)
	}
}

func TestInviteWithoutMailReturnsTheLink(t *testing.T) {
	for _, ferr := range []error{mail.ErrNotConfigured, errors.New("smtp down")} {
		e, org := ownerless(t)
		e.mailer.FailWith(ferr)
		res, err := e.svc.InviteOwner(context.Background(), org, "ana@x.example", nil)
		if err != nil {
			t.Fatal(err)
		}
		if res.MailSent || !strings.HasPrefix(res.Link, inviteLinkPrefix) || len(tokenOf(t, res.Link)) != 43 {
			t.Fatalf("result = %+v", res)
		}
		if n := countRows(t, e.pool, `SELECT count(*) FROM invitations`); n != 1 {
			t.Fatalf("rows = %d", n)
		}
	}
}

func TestInviteOwnerRefusesASecondOwner(t *testing.T) {
	e, org, _ := setup(t)
	_, err := e.svc.InviteOwner(context.Background(), org, "bob@x.example", nil)
	requireCode(t, err, 409, "conflict")
	if n := countRows(t, e.pool, `SELECT count(*) FROM invitations`); n != 0 {
		t.Fatalf("rows = %d", n)
	}
}

func TestInviteOwnerWithAWrongCurrentOwner(t *testing.T) {
	e, org, _ := setup(t)
	wrong := uuid.New()
	_, err := e.svc.InviteOwner(context.Background(), org, "bob@x.example", &wrong)
	requireCode(t, err, 409, "conflict")
	// no active owner at all, with a replacesOwner
	e2, org2 := ownerless(t)
	_, err = e2.svc.InviteOwner(context.Background(), org2, "bob@x.example", &wrong)
	requireCode(t, err, 409, "conflict")
}

func TestInviteOwnerToAnUnknownOrganization(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.InviteOwner(context.Background(), uuid.New(), "ana@x.example", nil)
	requireCode(t, err, 404, "not_found")
}

func TestInvitationsRefuseInvalidEmails(t *testing.T) {
	e, org, owner := setup(t)
	cid := newClient(t, e.pool, org, "Gym")
	for _, in := range []string{"not-an-email", "Ana <ana@x.example>", ""} {
		_, err := e.svc.InviteOwner(context.Background(), org, in, nil)
		requireCode(t, err, 400, "invalid_request")
		_, err = e.svc.InviteClientUser(context.Background(), principalOf(owner), cid, in)
		requireCode(t, err, 400, "invalid_request")
	}
}

func TestInvitationsDoNotDependOnARegisteredEmail(t *testing.T) {
	e, org, owner := setup(t)
	cid := newClient(t, e.pool, org, "Gym")
	ctx := context.Background()
	// ana@x.example is a registered email; new@x.example is not. The answers must look the same.
	known, err := e.svc.InviteClientUser(ctx, principalOf(owner), cid, "ana@x.example")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := e.svc.InviteClientUser(ctx, principalOf(owner), cid, "new@x.example")
	if err != nil {
		t.Fatal(err)
	}
	if known.MailSent != fresh.MailSent || (known.Link == "") != (fresh.Link == "") {
		t.Fatalf("different results: %+v and %+v", known, fresh)
	}
	e2, org2 := ownerless(t)
	other, _ := newOrg(t, e2.pool)
	newUser(t, e2.pool, other, nil, "ana@x.example")
	if _, err = e2.svc.InviteOwner(ctx, org2, "Ana@x.example", nil); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptRefusesARegisteredEmail(t *testing.T) {
	e, org, owner := setup(t)
	cid := newClient(t, e.pool, org, "Gym")
	if _, err := e.svc.InviteClientUser(context.Background(), principalOf(owner), cid, "ana@x.example"); err != nil {
		t.Fatal(err)
	}
	_, _, err := e.svc.AcceptInvitation(context.Background(), tokenOf(t, lastLink(t, e.mailer)), "a good password", "1.2.3.4")
	requireCode(t, err, 409, "email_in_use")
	if !strings.Contains(err.Error(), "This email already has an account. Log in, or ask for an invitation to another email.") {
		t.Fatalf("err = %v", err)
	}
}

func TestANewInvitationReplacesThePendingOne(t *testing.T) {
	e, org := ownerless(t)
	ctx := context.Background()
	if _, err := e.svc.InviteOwner(ctx, org, "ana@x.example", nil); err != nil {
		t.Fatal(err)
	}
	first := tokenOf(t, lastLink(t, e.mailer))
	if _, err := e.svc.InviteOwner(ctx, org, "ana@x.example", nil); err != nil {
		t.Fatal(err)
	}
	second := tokenOf(t, lastLink(t, e.mailer))
	if n := countRows(t, e.pool, `SELECT count(*) FROM invitations WHERE accepted_at IS NULL`); n != 1 {
		t.Fatalf("pending = %d", n)
	}
	_, _, err := e.svc.AcceptInvitation(ctx, first, "a good password", "1.2.3.4")
	requireCode(t, err, 410, "token_invalid")
	if _, _, err := e.svc.AcceptInvitation(ctx, second, "a good password", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
}

func inviteClient(t *testing.T) (*env, uuid.UUID, uuid.UUID, string) {
	t.Helper()
	e, org, owner := setup(t)
	cid := newClient(t, e.pool, org, "Gym Centre")
	if _, err := e.svc.InviteClientUser(context.Background(), principalOf(owner), cid, "front@gym.example"); err != nil {
		t.Fatal(err)
	}
	return e, org, cid, tokenOf(t, lastLink(t, e.mailer))
}

func TestInviteClientUser(t *testing.T) {
	e, org, owner := setup(t)
	cid := newClient(t, e.pool, org, "Gym Centre")
	res, err := e.svc.InviteClientUser(context.Background(), principalOf(owner), cid, "front@gym.example")
	if err != nil {
		t.Fatal(err)
	}
	if !res.MailSent || res.Email != "front@gym.example" {
		t.Fatalf("result = %+v", res)
	}
	sent := e.mailer.Sent()
	if len(sent) != 1 || !strings.Contains(sent[0].Subject, "Gym Centre") {
		t.Fatalf("sent = %+v", sent)
	}
	if n := countRows(t, e.pool, `SELECT count(*) FROM invitations WHERE role='client' AND client_id=$1`, cid); n != 1 {
		t.Fatalf("rows = %d", n)
	}
}

func TestInviteClientUserIsOwnerOnly(t *testing.T) {
	e, org, _ := setup(t)
	cid := newClient(t, e.pool, org, "Gym")
	cu := newUser(t, e.pool, org, &cid, "gym@x.example")
	cid2 := newClient(t, e.pool, org, "Other")
	_, err := e.svc.InviteClientUser(context.Background(), principalOf(cu), cid2, "z@x.example")
	requireCode(t, err, 403, "owner_only")
	if n := countRows(t, e.pool, `SELECT count(*) FROM invitations`); n != 0 {
		t.Fatalf("rows = %d", n)
	}
}

func TestInviteToAClientOfAnotherOrganization(t *testing.T) {
	e, orgA, _ := setup(t)
	orgB, _ := newOrg(t, e.pool)
	ownerB := newUser(t, e.pool, orgB, nil, "b@x.example")
	cid := newClient(t, e.pool, orgA, "Gym")
	_, err := e.svc.InviteClientUser(context.Background(), principalOf(ownerB), cid, "z@x.example")
	requireCode(t, err, 404, "not_found")
}

func TestInviteToTheDefaultClient(t *testing.T) {
	e, org, owner := setup(t)
	var def uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `SELECT id FROM clients WHERE organization_id=$1 AND is_default`, org).Scan(&def); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.InviteClientUser(context.Background(), principalOf(owner), def, "z@x.example")
	requireCode(t, err, 409, "default_client")
}

func TestInviteToAClientWithAnActiveUser(t *testing.T) {
	e, org, owner := setup(t)
	cid := newClient(t, e.pool, org, "Gym")
	newUser(t, e.pool, org, &cid, "gym@x.example")
	_, err := e.svc.InviteClientUser(context.Background(), principalOf(owner), cid, "z@x.example")
	requireCode(t, err, 409, "conflict")
}

func TestAcceptCreatesTheUserAndLogsIn(t *testing.T) {
	e, _, cid, token := inviteClient(t)
	ctx := context.Background()
	sess, u, err := e.svc.AcceptInvitation(ctx, token, "a good password", "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "front@gym.example" || u.Role != "client" || u.ClientID == nil || *u.ClientID != cid {
		t.Fatalf("user = %+v", u)
	}
	if ok, _ := auth.VerifyPassword(u.PasswordHash, "a good password"); !ok {
		t.Fatal("the password does not verify")
	}
	if _, err := e.svc.Authenticate(ctx, sess.Token, ""); err != nil {
		t.Fatal(err)
	}
	var acc time.Time
	if err := e.pool.QueryRow(ctx, `SELECT accepted_at FROM invitations`).Scan(&acc); err != nil || !acc.Equal(start) {
		t.Fatalf("accepted_at = %v, %v", acc, err)
	}
	if _, _, err := e.svc.Login(ctx, "front@gym.example", "a good password", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptAnExpiredInvitation(t *testing.T) {
	e, _, _, token := inviteClient(t)
	e.clk.Advance(7 * day)
	_, _, err := e.svc.AcceptInvitation(context.Background(), token, "a good password", "1.2.3.4")
	requireCode(t, err, 410, "token_invalid")
	if n := countRows(t, e.pool, `SELECT count(*) FROM users WHERE email='front@gym.example'`); n != 0 {
		t.Fatalf("users = %d", n)
	}

	e2, _, _, token2 := inviteClient(t)
	e2.clk.Advance(7*day - time.Second)
	if _, _, err := e2.svc.AcceptInvitation(context.Background(), token2, "a good password", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptTwice(t *testing.T) {
	e, _, _, token := inviteClient(t)
	ctx := context.Background()
	if _, _, err := e.svc.AcceptInvitation(ctx, token, "a good password", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	_, _, err := e.svc.AcceptInvitation(ctx, token, "a good password", "1.2.3.4")
	requireCode(t, err, 410, "token_invalid")
	if n := countRows(t, e.pool, `SELECT count(*) FROM users WHERE email='front@gym.example'`); n != 1 {
		t.Fatalf("users = %d", n)
	}
}

func TestAcceptAWrongToken(t *testing.T) {
	e := newEnv(t)
	_, _, err := e.svc.AcceptInvitation(context.Background(), "nope", "a good password", "1.2.3.4")
	requireCode(t, err, 410, "token_invalid")
}

func TestAcceptWithAShortPassword(t *testing.T) {
	e, _, _, token := inviteClient(t)
	ctx := context.Background()
	_, _, err := e.svc.AcceptInvitation(ctx, token, "123456789", "1.2.3.4")
	requireCode(t, err, 400, "invalid_request")
	if n := countRows(t, e.pool, `SELECT count(*) FROM invitations WHERE accepted_at IS NULL`); n != 1 {
		t.Fatalf("pending = %d", n)
	}
	if _, _, err := e.svc.AcceptInvitation(ctx, token, "a good password", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptWhenTheEmailWasRegisteredMeanwhile(t *testing.T) {
	e, _, _, token := inviteClient(t)
	orgB, _ := newOrg(t, e.pool)
	newUser(t, e.pool, orgB, nil, "front@gym.example")
	_, _, err := e.svc.AcceptInvitation(context.Background(), token, "a good password", "1.2.3.4")
	requireCode(t, err, 409, "email_in_use")
	if n := countRows(t, e.pool, `SELECT count(*) FROM invitations WHERE accepted_at IS NULL`); n != 1 {
		t.Fatalf("pending = %d", n)
	}
}

func TestAcceptIsRateLimitedPerIP(t *testing.T) {
	e, _, _, token := inviteClient(t)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		_, _, err := e.svc.AcceptInvitation(ctx, "wrong", "a good password", "9.9.9.9")
		requireCode(t, err, 410, "token_invalid")
	}
	_, _, err := e.svc.AcceptInvitation(ctx, token, "a good password", "9.9.9.9")
	requireCode(t, err, 429, "rate_limited")
}

func TestOwnerTransfer(t *testing.T) {
	e, org, old := setup(t)
	ctx := context.Background()
	sess := login(t, e, "ana@x.example")
	key := insertKey(t, e.pool, old.ID, "k")
	if _, err := e.svc.InviteOwner(ctx, org, "new@x.example", &old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Authenticate(ctx, sess.Token, ""); err != nil {
		t.Fatalf("before acceptance: %v", err)
	}
	if _, err := e.svc.Authenticate(ctx, "", key); err != nil {
		t.Fatalf("before acceptance: %v", err)
	}
	_, nu, err := e.svc.AcceptInvitation(ctx, tokenOf(t, lastLink(t, e.mailer)), "a good password", "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	if nu.Role != "owner" || nu.ClientID != nil {
		t.Fatalf("new user = %+v", nu)
	}
	var dis, rev time.Time
	if err := e.pool.QueryRow(ctx, `SELECT disabled_at FROM users WHERE id=$1`, old.ID).Scan(&dis); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(ctx, `SELECT revoked_at FROM api_keys WHERE user_id=$1`, old.ID).Scan(&rev); err != nil {
		t.Fatal(err)
	}
	if !dis.Equal(start) || !rev.Equal(start) {
		t.Fatalf("disabled %v revoked %v", dis, rev)
	}
	if n := countRows(t, e.pool, `SELECT count(*) FROM sessions WHERE user_id=$1`, old.ID); n != 0 {
		t.Fatalf("sessions = %d", n)
	}
	_, err = e.svc.Authenticate(ctx, "", key)
	requireCode(t, err, 401, "unauthenticated")
	_, _, err = e.svc.Login(ctx, "ana@x.example", password, "1.2.3.4")
	requireCode(t, err, 401, "unauthenticated")
	if _, _, err := e.svc.Login(ctx, "new@x.example", "a good password", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, e.pool, `SELECT count(*) FROM users WHERE organization_id=$1 AND role='owner' AND disabled_at IS NULL`, org); n != 1 {
		t.Fatalf("active owners = %d", n)
	}
}

func TestAFailedTransferChangesNothing(t *testing.T) {
	e, org, old := setup(t)
	ctx := context.Background()
	sess := login(t, e, "ana@x.example")
	key := insertKey(t, e.pool, old.ID, "k")
	if _, err := e.svc.InviteOwner(ctx, org, "new@x.example", &old.ID); err != nil {
		t.Fatal(err)
	}
	orgB, _ := newOrg(t, e.pool)
	newUser(t, e.pool, orgB, nil, "new@x.example")
	_, _, err := e.svc.AcceptInvitation(ctx, tokenOf(t, lastLink(t, e.mailer)), "a good password", "1.2.3.4")
	requireCode(t, err, 409, "email_in_use")
	if _, err := e.svc.Authenticate(ctx, sess.Token, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Authenticate(ctx, "", key); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, e.pool, `SELECT count(*) FROM users WHERE id=$1 AND disabled_at IS NULL`, old.ID); n != 1 {
		t.Fatal("the old owner was disabled")
	}
}

func TestTwoConcurrentAcceptancesOfOneInvitation(t *testing.T) {
	e, _, _, token := inviteClient(t)
	ctx := context.Background()
	gate := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			_, _, errs[i] = e.svc.AcceptInvitation(ctx, token, "a good password", []string{"1.1.1.1", "2.2.2.2"}[i])
		}()
	}
	close(gate)
	wg.Wait()
	ok, gone := 0, 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else {
			requireCode(t, err, 410, "token_invalid")
			gone++
		}
	}
	if ok != 1 || gone != 1 {
		t.Fatalf("ok %d, gone %d", ok, gone)
	}
	if n := countRows(t, e.pool, `SELECT count(*) FROM users WHERE email='front@gym.example'`); n != 1 {
		t.Fatalf("users = %d", n)
	}
}

func TestAcceptWhenTheInvitationIsDeletedBeforeTheLock(t *testing.T) {
	e, _, _, token := inviteClient(t)
	ctx := context.Background()
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM invitations FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := e.svc.AcceptInvitation(ctx, token, "a good password", "1.2.3.4")
		done <- err
	}()
	waitForLockWaiter(t, e)
	if _, err := tx.Exec(ctx, `DELETE FROM invitations`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	requireCode(t, <-done, 410, "token_invalid")
}

// Two invitations at the same time must leave one pending row, never two.
func TestConcurrentInvitationsLeaveOnePendingRow(t *testing.T) {
	for round := 0; round < 10; round++ {
		e, org := ownerless(t)
		ctx := context.Background()
		gate := make(chan struct{})
		errs := make([]error, 4)
		var wg sync.WaitGroup
		for i := range errs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-gate
				_, errs[i] = e.svc.InviteOwner(ctx, org, "ana"+string(rune('a'+i))+"@x.example", nil)
			}()
		}
		close(gate)
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				requireCode(t, err, 409, "conflict")
			}
		}
		if n := countRows(t, e.pool, `SELECT count(*) FROM invitations WHERE organization_id = $1 AND accepted_at IS NULL`, org); n != 1 {
			t.Fatalf("round %d: %d pending invitations, want 1", round, n)
		}
	}
}

func TestAFailedInvitationMailWritesOneWarnLine(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		class string
	}{
		{"timeout", fmt.Errorf("send mail: %w", context.DeadlineExceeded), "error_class=timeout"},
		{"refused", fmt.Errorf("send mail: %w", &textproto.Error{Code: 451, Msg: "later"}), "error_class=refused"},
		{"other", errors.New("invalid To address"), "error_class=other"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			buf := captureLog(t)
			e, org := ownerless(t)
			e.mailer.FailWith(c.err)
			res, err := e.svc.InviteOwner(context.Background(), org, "ana@x.example", nil)
			if err != nil {
				t.Fatal(err)
			}
			var id string
			if err := e.pool.QueryRow(context.Background(), `SELECT id::text FROM invitations`).Scan(&id); err != nil {
				t.Fatal(err)
			}
			out := buf.String()
			if strings.Count(out, "\n") != 1 || !strings.Contains(out, "level=WARN") ||
				!strings.Contains(out, "kind=invitation") || !strings.Contains(out, "invitation_id="+id) ||
				!strings.Contains(out, c.class) {
				t.Fatalf("log = %q", out)
			}
			if strings.Contains(out, "ana@x.example") || strings.Contains(out, tokenOf(t, res.Link)) {
				t.Fatalf("the line holds an address or a token: %q", out)
			}
		})
	}
}

func TestInvitationWithoutMailConfiguredWritesNoLine(t *testing.T) {
	buf := captureLog(t)
	e, org := ownerless(t)
	e.mailer.FailWith(mail.ErrNotConfigured)
	if _, err := e.svc.InviteOwner(context.Background(), org, "ana@x.example", nil); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("log = %q", buf.String())
	}
}

func TestASentInvitationMailWritesNoLine(t *testing.T) {
	buf := captureLog(t)
	e, org := ownerless(t)
	if _, err := e.svc.InviteOwner(context.Background(), org, "ana@x.example", nil); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("log = %q", buf.String())
	}
}

// removedUser creates a client user front@gym.example in the client "Gym" and disables it.
func removedUser(t *testing.T) (e *env, org, cid uuid.UUID, owner, u gen.User) {
	t.Helper()
	e, org, owner = setup(t)
	cid = newClient(t, e.pool, org, "Gym")
	u = newUser(t, e.pool, org, &cid, "front@gym.example")
	if err := e.svc.SetUserDisabled(context.Background(), "front@gym.example", true); err != nil {
		t.Fatal(err)
	}
	return e, org, cid, owner, u
}

// inviteAgain invites email in the client and returns the token of the link.
func inviteAgain(t *testing.T, e *env, owner gen.User, cid uuid.UUID, email string) string {
	t.Helper()
	if _, err := e.svc.InviteClientUser(context.Background(), principalOf(owner), cid, email); err != nil {
		t.Fatal(err)
	}
	return tokenOf(t, lastLink(t, e.mailer))
}

func TestARemovedUserReturnsToTheSameClient(t *testing.T) {
	buf := captureLog(t)
	e, _, cid, owner, u := removedUser(t)
	ctx := context.Background()
	token := inviteAgain(t, e, owner, cid, "front@gym.example")
	sess, back, err := e.svc.AcceptInvitation(ctx, token, "a good password", "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	if back.ID != u.ID || back.DisabledAt != nil || back.ClientID == nil || *back.ClientID != cid {
		t.Fatalf("user = %+v", back)
	}
	if ok, _ := auth.VerifyPassword(back.PasswordHash, "a good password"); !ok {
		t.Fatal("the new password does not verify")
	}
	if _, err := e.svc.Authenticate(ctx, sess.Token, ""); err != nil {
		t.Fatal(err)
	}
	_, _, err = e.svc.Login(ctx, "front@gym.example", password, "1.2.3.4")
	requireCode(t, err, 401, "unauthenticated")
	if n := countRows(t, e.pool, `SELECT count(*) FROM users WHERE email='front@gym.example'`); n != 1 {
		t.Fatalf("users = %d", n)
	}
	if n := countRows(t, e.pool, `SELECT count(*) FROM invitations WHERE accepted_at IS NULL`); n != 0 {
		t.Fatalf("pending = %d", n)
	}
	out := buf.String()
	if strings.Count(out, "\n") != 1 || !strings.Contains(out, "level=INFO") || !strings.Contains(out, "actor_id="+u.ID.String()) ||
		!strings.Contains(out, "client_id="+cid.String()) || strings.Contains(out, "front@gym.example") {
		t.Fatalf("log = %q", out)
	}
}

// A login and a key creation that ran beside the removal leave a row. The return must not bring
// them back.
func TestAReturnDeletesOldSessionsKeysAndResets(t *testing.T) {
	e, _, cid, owner, u := removedUser(t)
	ctx := context.Background()
	oldSess := insertSession(t, e.pool, u.ID)
	oldKey := insertKey(t, e.pool, u.ID, "k")
	resetLink, err := e.svc.CreatePasswordReset(ctx, "front@gym.example")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range [][2]string{{oldSess, ""}, {"", oldKey}} {
		_, err := e.svc.Authenticate(ctx, c[0], c[1])
		requireCode(t, err, 401, "unauthenticated")
	}
	token := inviteAgain(t, e, owner, cid, "front@gym.example")
	sess, _, err := e.svc.AcceptInvitation(ctx, token, "a good password", "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range [][2]string{{oldSess, ""}, {"", oldKey}} {
		_, err := e.svc.Authenticate(ctx, c[0], c[1])
		requireCode(t, err, 401, "unauthenticated")
	}
	if _, err := e.svc.Authenticate(ctx, sess.Token, ""); err != nil {
		t.Fatal(err)
	}
	requireCode(t, e.svc.ConfirmPasswordReset(ctx, tokenOf(t, resetLink), "new password 1", "1.2.3.4"), 410, "token_invalid")
	if n := countRows(t, e.pool, `SELECT count(*) FROM sessions WHERE user_id=$1`, u.ID); n != 1 {
		t.Fatalf("sessions = %d", n)
	}
}

func TestNoReturnToAnotherClient(t *testing.T) {
	e, org, _, owner, u := removedUser(t)
	other := newClient(t, e.pool, org, "Other")
	token := inviteAgain(t, e, owner, other, "front@gym.example")
	_, _, err := e.svc.AcceptInvitation(context.Background(), token, "a good password", "1.2.3.4")
	requireCode(t, err, 409, "email_in_use")
	requireStillRemoved(t, e, u)
}

func TestNoReturnToAnotherOrganization(t *testing.T) {
	e, _, _, _, u := removedUser(t)
	orgB, _ := newOrg(t, e.pool)
	ownerB := newUser(t, e.pool, orgB, nil, "b@x.example")
	cidB := newClient(t, e.pool, orgB, "Gym")
	token := inviteAgain(t, e, ownerB, cidB, "front@gym.example")
	_, _, err := e.svc.AcceptInvitation(context.Background(), token, "a good password", "1.2.3.4")
	requireCode(t, err, 409, "email_in_use")
	requireStillRemoved(t, e, u)
}

func TestNoReturnOfAnOwner(t *testing.T) {
	e, org, owner := setup(t)
	ctx := context.Background()
	if err := e.svc.SetUserDisabled(ctx, "ana@x.example", true); err != nil {
		t.Fatal(err)
	}
	// An invitation of a client user to the email of the owner.
	cid := newClient(t, e.pool, org, "Gym")
	boss := newUser(t, e.pool, org, nil, "boss@x.example")
	token := inviteAgain(t, e, boss, cid, "ana@x.example")
	_, _, err := e.svc.AcceptInvitation(ctx, token, "a good password", "1.2.3.4")
	requireCode(t, err, 409, "email_in_use")
	requireStillRemoved(t, e, owner)
	// An owner invitation: the client ids of the user and of the invitation are both null.
	if err := e.svc.SetUserDisabled(ctx, "boss@x.example", true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.InviteOwner(ctx, org, "ana@x.example", nil); err != nil {
		t.Fatal(err)
	}
	_, _, err = e.svc.AcceptInvitation(ctx, tokenOf(t, lastLink(t, e.mailer)), "a good password", "1.2.3.4")
	requireCode(t, err, 409, "email_in_use")
	requireStillRemoved(t, e, owner)
}

func TestNoReturnOfAnActiveUser(t *testing.T) {
	e, org, owner := setup(t)
	cid := newClient(t, e.pool, org, "Gym")
	token := inviteAgain(t, e, owner, cid, "front@gym.example")
	u := newUser(t, e.pool, org, &cid, "front@gym.example")
	_, _, err := e.svc.AcceptInvitation(context.Background(), token, "a good password", "1.2.3.4")
	requireCode(t, err, 409, "email_in_use")
	var hash string
	if err := e.pool.QueryRow(context.Background(), `SELECT password_hash FROM users WHERE id=$1`, u.ID).Scan(&hash); err != nil || hash != u.PasswordHash {
		t.Fatalf("the password changed: %v", err)
	}
}

func TestAReturnWhenTheClientHasAnotherActiveUser(t *testing.T) {
	e, org, cid, owner, u := removedUser(t)
	token := inviteAgain(t, e, owner, cid, "front@gym.example")
	newUser(t, e.pool, org, &cid, "two@gym.example")
	_, _, err := e.svc.AcceptInvitation(context.Background(), token, "a good password", "1.2.3.4")
	requireCode(t, err, 409, "conflict")
	requireStillRemoved(t, e, u)
}

func TestTwoConcurrentReturns(t *testing.T) {
	e, _, cid, owner, u := removedUser(t)
	token := inviteAgain(t, e, owner, cid, "front@gym.example")
	ctx := context.Background()
	gate := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			_, _, errs[i] = e.svc.AcceptInvitation(ctx, token, "a good password", []string{"1.1.1.1", "2.2.2.2"}[i])
		}()
	}
	close(gate)
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else {
			requireCode(t, err, 410, "token_invalid")
		}
	}
	if ok != 1 {
		t.Fatalf("ok = %d", ok)
	}
	if n := countRows(t, e.pool, `SELECT count(*) FROM users WHERE email='front@gym.example' AND disabled_at IS NULL AND id=$1`, u.ID); n != 1 {
		t.Fatalf("active = %d", n)
	}
	if n := countRows(t, e.pool, `SELECT count(*) FROM sessions WHERE user_id=$1`, u.ID); n != 1 {
		t.Fatalf("sessions = %d", n)
	}
}

// requireStillRemoved checks that the user is disabled and kept its password.
func requireStillRemoved(t *testing.T, e *env, u gen.User) {
	t.Helper()
	var hash string
	var dis *time.Time
	if err := e.pool.QueryRow(context.Background(), `SELECT password_hash, disabled_at FROM users WHERE id=$1`, u.ID).Scan(&hash, &dis); err != nil {
		t.Fatal(err)
	}
	if dis == nil || hash != u.PasswordHash {
		t.Fatalf("disabled_at %v, password changed %v", dis, hash != u.PasswordHash)
	}
}

func TestCancelInvitation(t *testing.T) {
	buf := captureLog(t)
	e, _, cid, token := inviteClient(t)
	ctx := context.Background()
	owner, err := gen.New(e.pool).GetUserByEmail(ctx, "ana@x.example")
	if err != nil {
		t.Fatal(err)
	}
	e.clk.Advance(8 * day) // an expired invitation is also cancelled
	if err := e.svc.CancelInvitation(ctx, principalOf(owner), cid); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, e.pool, `SELECT count(*) FROM invitations`); n != 0 {
		t.Fatalf("rows = %d", n)
	}
	_, _, err = e.svc.AcceptInvitation(ctx, token, "a good password", "1.2.3.4")
	requireCode(t, err, 410, "token_invalid")
	out := buf.String()
	if strings.Count(out, "\n") != 1 || !strings.Contains(out, "level=INFO") || !strings.Contains(out, "actor_id="+owner.ID.String()) ||
		!strings.Contains(out, "client_id="+cid.String()) || strings.Contains(out, "front@gym.example") {
		t.Fatalf("log = %q", out)
	}
	requireCode(t, e.svc.CancelInvitation(ctx, principalOf(owner), cid), 404, "not_found")
}

func TestCancelInvitationErrors(t *testing.T) {
	e, org, cid, _ := inviteClient(t)
	ctx := context.Background()
	other := newClient(t, e.pool, org, "Other")
	cu := newUser(t, e.pool, org, &other, "z@x.example")
	var def uuid.UUID
	if err := e.pool.QueryRow(ctx, `SELECT id FROM clients WHERE organization_id=$1 AND is_default`, org).Scan(&def); err != nil {
		t.Fatal(err)
	}
	owner, err := gen.New(e.pool).GetUserByEmail(ctx, "ana@x.example")
	if err != nil {
		t.Fatal(err)
	}
	orgB, _ := newOrg(t, e.pool)
	ownerB := newUser(t, e.pool, orgB, nil, "b@x.example")

	requireCode(t, e.svc.CancelInvitation(ctx, principalOf(cu), cid), 403, "owner_only")
	requireCode(t, e.svc.CancelInvitation(ctx, principalOf(ownerB), cid), 404, "not_found")
	requireCode(t, e.svc.CancelInvitation(ctx, principalOf(owner), def), 404, "not_found")
	requireCode(t, e.svc.CancelInvitation(ctx, principalOf(owner), other), 404, "not_found")
	requireCode(t, e.svc.CancelInvitation(ctx, principalOf(owner), uuid.New()), 404, "not_found")
	if n := countRows(t, e.pool, `SELECT count(*) FROM invitations WHERE client_id=$1`, cid); n != 1 {
		t.Fatalf("rows = %d", n)
	}
}

func TestLookupInvitation(t *testing.T) {
	e, _, _, token := inviteClient(t)
	ctx := context.Background()
	info, err := e.svc.LookupInvitation(ctx, token, "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	if info.Email != "front@gym.example" || info.OrganizationName != "Org" || !info.ExpiresAt.Equal(start.Add(7*day)) {
		t.Fatalf("info = %+v", info)
	}
	// The lookup does not use the token.
	if _, _, err := e.svc.AcceptInvitation(ctx, token, "a good password", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.LookupInvitation(ctx, token, "1.2.3.4")
	requireCode(t, err, 410, "token_invalid")
	_, err = e.svc.LookupInvitation(ctx, "nope", "1.2.3.4")
	requireCode(t, err, 410, "token_invalid")

	e2, _, _, token2 := inviteClient(t)
	e2.clk.Advance(7 * day)
	_, err = e2.svc.LookupInvitation(ctx, token2, "1.2.3.4")
	requireCode(t, err, 410, "token_invalid")
}

func TestLookupsAreRateLimitedPerIP(t *testing.T) {
	e, _, _, token := inviteClient(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_, err := e.svc.LookupInvitation(ctx, "wrong", "9.9.9.9")
		requireCode(t, err, 410, "token_invalid")
		_, err = e.svc.LookupPasswordReset(ctx, "wrong", "9.9.9.9")
		requireCode(t, err, 410, "token_invalid")
	}
	_, err := e.svc.LookupInvitation(ctx, token, "9.9.9.9")
	requireCode(t, err, 429, "rate_limited")
	_, err = e.svc.LookupPasswordReset(ctx, "wrong", "9.9.9.9")
	requireCode(t, err, 429, "rate_limited")
	// The lookups have their own kind: the acceptance still works from this IP.
	if _, _, err := e.svc.AcceptInvitation(ctx, token, "a good password", "9.9.9.9"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.LookupInvitation(ctx, token, "8.8.8.8"); err == nil {
		t.Fatal("an accepted invitation was found")
	}
}
