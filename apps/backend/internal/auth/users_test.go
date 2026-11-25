package auth_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDisablingAUserDeletesItsSessionsAndKeys(t *testing.T) {
	e, _, owner := setup(t)
	ctx := context.Background()
	s := login(t, e, "ana@x.example")
	key := insertKey(t, e.pool, owner.ID, "k")
	if err := e.svc.SetUserDisabled(ctx, "ana@x.example", true); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Authenticate(ctx, s.Token, "")
	requireCode(t, err, 401, "unauthenticated")
	_, err = e.svc.Authenticate(ctx, "", key)
	requireCode(t, err, 401, "unauthenticated")
	_, _, err = e.svc.Login(ctx, "ana@x.example", password, "1.2.3.4")
	requireCode(t, err, 401, "unauthenticated")
	var dis, rev time.Time
	if err := e.pool.QueryRow(ctx, `SELECT disabled_at FROM users WHERE id=$1`, owner.ID).Scan(&dis); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(ctx, `SELECT revoked_at FROM api_keys WHERE user_id=$1`, owner.ID).Scan(&rev); err != nil {
		t.Fatal(err)
	}
	if !dis.Equal(start) || !rev.Equal(start) {
		t.Fatalf("disabled %v revoked %v", dis, rev)
	}
	if n := countRows(t, e.pool, `SELECT count(*) FROM sessions`); n != 0 {
		t.Fatalf("sessions = %d", n)
	}
}

func TestEnablingAUser(t *testing.T) {
	e, _, owner := setup(t)
	ctx := context.Background()
	key := insertKey(t, e.pool, owner.ID, "k")
	if err := e.svc.SetUserDisabled(ctx, "ana@x.example", true); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetUserDisabled(ctx, "ana@x.example", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.Login(ctx, "ana@x.example", password, "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Authenticate(ctx, "", key)
	requireCode(t, err, 401, "unauthenticated")
}

func TestDisablingTwiceKeepsTheFirstTime(t *testing.T) {
	e, _, owner := setup(t)
	ctx := context.Background()
	if err := e.svc.SetUserDisabled(ctx, "ana@x.example", true); err != nil {
		t.Fatal(err)
	}
	e.clk.Advance(time.Hour)
	if err := e.svc.SetUserDisabled(ctx, "ana@x.example", true); err != nil {
		t.Fatal(err)
	}
	var dis time.Time
	if err := e.pool.QueryRow(ctx, `SELECT disabled_at FROM users WHERE id=$1`, owner.ID).Scan(&dis); err != nil {
		t.Fatal(err)
	}
	if !dis.Equal(start) {
		t.Fatalf("disabled_at = %v", dis)
	}
}

func TestSetUserDisabledOfAnUnknownEmail(t *testing.T) {
	e, _, _ := setup(t)
	requireCode(t, e.svc.SetUserDisabled(context.Background(), "nobody@x.example", true), 404, "not_found")
	requireCode(t, e.svc.SetUserDisabled(context.Background(), "not-an-email", true), 400, "invalid_request")
}

func TestEnablingAnOldOwnerWhileAnotherIsActive(t *testing.T) {
	e, org, _ := setup(t)
	ctx := context.Background()
	if err := e.svc.SetUserDisabled(ctx, "ana@x.example", true); err != nil {
		t.Fatal(err)
	}
	newUser(t, e.pool, org, nil, "bob@x.example")
	requireCode(t, e.svc.SetUserDisabled(ctx, "ana@x.example", false), 409, "conflict")
	if n := countRows(t, e.pool, `SELECT count(*) FROM users WHERE email='ana@x.example' AND disabled_at IS NOT NULL`); n != 1 {
		t.Fatal("ana was enabled")
	}
}

func TestEnablingAClientUserWhileAnotherIsActive(t *testing.T) {
	e, org, _ := setup(t)
	ctx := context.Background()
	cid := newClient(t, e.pool, org, "Gym")
	newUser(t, e.pool, org, &cid, "one@x.example")
	if err := e.svc.SetUserDisabled(ctx, "one@x.example", true); err != nil {
		t.Fatal(err)
	}
	newUser(t, e.pool, org, &cid, "two@x.example")
	requireCode(t, e.svc.SetUserDisabled(ctx, "one@x.example", false), 409, "conflict")
}

func TestRemoveClientAccess(t *testing.T) {
	buf := captureLog(t)
	e, org, owner := setup(t)
	ctx := context.Background()
	cid := newClient(t, e.pool, org, "Gym")
	u := newUser(t, e.pool, org, &cid, "front@gym.example")
	s := login(t, e, "front@gym.example")
	key := insertKey(t, e.pool, u.ID, "k")
	iid := uuid.New()
	if _, err := e.pool.Exec(ctx, `INSERT INTO instances (id, organization_id, client_id, core_id, core_name, display_name, provider, state)
		VALUES ($1, $2, $3, 'c1', $4, 'Front desk', 'WHATSAPP_WEB', 'active')`,
		iid, org, cid, "i_"+strings.ReplaceAll(iid.String(), "-", "")); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RemoveClientAccess(ctx, principalOf(owner), cid); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][2]string{{s.Token, ""}, {"", key}} {
		_, err := e.svc.Authenticate(ctx, c[0], c[1])
		requireCode(t, err, 401, "unauthenticated")
	}
	_, _, err := e.svc.Login(ctx, "front@gym.example", password, "1.2.3.4")
	requireCode(t, err, 401, "unauthenticated")
	var dis time.Time
	if err := e.pool.QueryRow(ctx, `SELECT disabled_at FROM users WHERE id=$1`, u.ID).Scan(&dis); err != nil || !dis.Equal(start) {
		t.Fatalf("disabled_at = %v, %v", dis, err)
	}
	if n := countRows(t, e.pool, `SELECT count(*) FROM instances WHERE client_id=$1 AND enabled AND state='active'`, cid); n != 1 {
		t.Fatalf("instances = %d", n)
	}
	out := buf.String()
	if strings.Count(out, "\n") != 1 || !strings.Contains(out, "level=INFO") || !strings.Contains(out, "actor_id="+owner.ID.String()) ||
		!strings.Contains(out, "client_id="+cid.String()) || strings.Contains(out, "front@gym.example") {
		t.Fatalf("log = %q", out)
	}
	// The client has no active user now.
	requireCode(t, e.svc.RemoveClientAccess(ctx, principalOf(owner), cid), 404, "not_found")
}

func TestRemoveClientAccessThenReturn(t *testing.T) {
	e, org, owner := setup(t)
	ctx := context.Background()
	cid := newClient(t, e.pool, org, "Gym")
	u := newUser(t, e.pool, org, &cid, "front@gym.example")
	old := login(t, e, "front@gym.example")
	if err := e.svc.RemoveClientAccess(ctx, principalOf(owner), cid); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.InviteClientUser(ctx, principalOf(owner), cid, "front@gym.example"); err != nil {
		t.Fatal(err)
	}
	sess, back, err := e.svc.AcceptInvitation(ctx, tokenOf(t, lastLink(t, e.mailer)), "a good password", "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	if back.ID != u.ID {
		t.Fatalf("user = %v, want %v", back.ID, u.ID)
	}
	_, err = e.svc.Authenticate(ctx, old.Token, "")
	requireCode(t, err, 401, "unauthenticated")
	if _, err := e.svc.Authenticate(ctx, sess.Token, ""); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveClientAccessErrors(t *testing.T) {
	e, org, owner := setup(t)
	ctx := context.Background()
	cid := newClient(t, e.pool, org, "Gym")
	cu := newUser(t, e.pool, org, &cid, "front@gym.example")
	empty := newClient(t, e.pool, org, "Empty")
	var def uuid.UUID
	if err := e.pool.QueryRow(ctx, `SELECT id FROM clients WHERE organization_id=$1 AND is_default`, org).Scan(&def); err != nil {
		t.Fatal(err)
	}
	orgB, _ := newOrg(t, e.pool)
	ownerB := newUser(t, e.pool, orgB, nil, "b@x.example")

	requireCode(t, e.svc.RemoveClientAccess(ctx, principalOf(cu), cid), 403, "owner_only")
	requireCode(t, e.svc.RemoveClientAccess(ctx, principalOf(ownerB), cid), 404, "not_found")
	requireCode(t, e.svc.RemoveClientAccess(ctx, principalOf(owner), def), 404, "not_found")
	requireCode(t, e.svc.RemoveClientAccess(ctx, principalOf(owner), empty), 404, "not_found")
	requireCode(t, e.svc.RemoveClientAccess(ctx, principalOf(owner), uuid.New()), 404, "not_found")
	if n := countRows(t, e.pool, `SELECT count(*) FROM users WHERE disabled_at IS NOT NULL`); n != 0 {
		t.Fatalf("disabled users = %d", n)
	}
}
