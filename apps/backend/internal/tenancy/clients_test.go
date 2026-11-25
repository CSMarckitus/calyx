package tenancy_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"calyx/backend/internal/tenancy"
)

func TestCreateClient(t *testing.T) {
	s, pool, _ := newService(t)
	org, _ := newOrg(t, s, 10, true)
	c, err := s.CreateClient(context.Background(), ownerOf(t, pool, org), tenancy.ClientInput{Name: "Gym Centre", CanCreateInstances: true, MaxInstances: 2})
	if err != nil {
		t.Fatal(err)
	}
	if c.IsDefault || !c.CanCreateInstances || c.MaxInstances != 2 || c.OrganizationID != org.ID || c.Name != "Gym Centre" {
		t.Fatalf("%+v", c)
	}
}

func TestCreateClientRejectsInvalidInput(t *testing.T) {
	s, pool, _ := newService(t)
	org, _ := newOrg(t, s, 10, true)
	p := ownerOf(t, pool, org)
	for _, in := range []tenancy.ClientInput{
		{Name: ""}, {Name: strings.Repeat("é", 101)}, {Name: "A", MaxInstances: -1}, {Name: "A", MaxInstances: 10001},
	} {
		_, err := s.CreateClient(context.Background(), p, in)
		requireCode(t, err, 400, "invalid_request")
	}
	if n := countRows(t, pool, "SELECT count(*) FROM clients WHERE organization_id=$1", org.ID); n != 1 {
		t.Fatalf("clients = %d", n)
	}
}

func TestAClientUserCannotManageClients(t *testing.T) {
	s, pool, _ := newService(t)
	org, c := newOrg(t, s, 10, true)
	_ = org
	u := userOf(t, pool, c)
	ctx := context.Background()
	_, err := s.CreateClient(ctx, u, tenancy.ClientInput{Name: "X"})
	requireCode(t, err, 403, "owner_only")
	_, err = s.UpdateClient(ctx, u, c.ID, tenancy.ClientChange{Name: ptr("X")})
	requireCode(t, err, 403, "owner_only")
	requireCode(t, s.DeleteClient(ctx, u, c.ID), 403, "owner_only")
	_, err = s.ListClients(ctx, u)
	requireCode(t, err, 403, "owner_only")
	_, err = s.GetClient(ctx, u, c.ID)
	requireCode(t, err, 403, "owner_only")
	if n := countRows(t, pool, "SELECT count(*) FROM clients WHERE id=$1 AND name='Default'", c.ID); n != 1 {
		t.Fatal("client changed or deleted")
	}
}

func TestClientsOfAnotherOrganizationAreNotFound(t *testing.T) {
	s, pool, _ := newService(t)
	_, ca := newOrg(t, s, 10, true)
	b, _ := newOrg(t, s, 10, true)
	p := ownerOf(t, pool, b)
	ctx := context.Background()
	_, err := s.GetClient(ctx, p, ca.ID)
	requireCode(t, err, 404, "not_found")
	_, err = s.UpdateClient(ctx, p, ca.ID, tenancy.ClientChange{Name: ptr("X")})
	requireCode(t, err, 404, "not_found")
	requireCode(t, s.DeleteClient(ctx, p, ca.ID), 404, "not_found")
	if n := countRows(t, pool, "SELECT count(*) FROM clients WHERE id=$1 AND name='Default'", ca.ID); n != 1 {
		t.Fatal("client changed or deleted")
	}
}

func TestUpdateClientChangesOnlyTheGivenFields(t *testing.T) {
	s, pool, _ := newService(t)
	org, _ := newOrg(t, s, 10, true)
	p := ownerOf(t, pool, org)
	ctx := context.Background()
	c, _ := s.CreateClient(ctx, p, tenancy.ClientInput{Name: "Old", CanCreateInstances: true, MaxInstances: 1})
	got, err := s.UpdateClient(ctx, p, c.ID, tenancy.ClientChange{MaxInstances: ptr(5)})
	if err != nil || got.MaxInstances != 5 || got.Name != "Old" || !got.CanCreateInstances {
		t.Fatalf("%v %+v", err, got)
	}
	got, err = s.UpdateClient(ctx, p, c.ID, tenancy.ClientChange{Name: ptr(" New ")})
	if err != nil || got.Name != "New" || got.MaxInstances != 5 {
		t.Fatalf("%v %+v", err, got)
	}
	_, err = s.UpdateClient(ctx, p, c.ID, tenancy.ClientChange{Name: ptr("")})
	requireCode(t, err, 400, "invalid_request")
}

func TestUpdateClientWithNoChangeReturnsTheRow(t *testing.T) {
	s, pool, _ := newService(t)
	org, c := newOrg(t, s, 10, true)
	p := ownerOf(t, pool, org)
	got, err := s.UpdateClient(context.Background(), p, c.ID, tenancy.ClientChange{})
	if err != nil || got.ID != c.ID || !got.UpdatedAt.Equal(c.UpdatedAt) {
		t.Fatalf("%v %+v", err, got)
	}
	_, err = s.UpdateClient(context.Background(), p, uuid.New(), tenancy.ClientChange{})
	requireCode(t, err, 404, "not_found")
}

func TestTheDefaultClientCanBeChanged(t *testing.T) {
	s, pool, _ := newService(t)
	org, c := newOrg(t, s, 10, true)
	got, err := s.UpdateClient(context.Background(), ownerOf(t, pool, org), c.ID, tenancy.ClientChange{MaxInstances: ptr(3)})
	if err != nil || got.MaxInstances != 3 || !got.IsDefault {
		t.Fatalf("%v %+v", err, got)
	}
}

func TestTheDefaultClientCannotBeDeleted(t *testing.T) {
	s, pool, _ := newService(t)
	org, c := newOrg(t, s, 10, true)
	requireCode(t, s.DeleteClient(context.Background(), ownerOf(t, pool, org), c.ID), 409, "default_client")
	if n := countRows(t, pool, "SELECT count(*) FROM clients WHERE id=$1", c.ID); n != 1 {
		t.Fatal("default client deleted")
	}
}

func TestAClientWithInstancesCannotBeDeleted(t *testing.T) {
	s, pool, _ := newService(t)
	org, _ := newOrg(t, s, 10, true)
	p := ownerOf(t, pool, org)
	c, _ := s.CreateClient(context.Background(), p, tenancy.ClientInput{Name: "C"})
	insertInstance(t, pool, org.ID, c.ID, "missing")
	requireCode(t, s.DeleteClient(context.Background(), p, c.ID), 409, "client_has_instances")
	if n := countRows(t, pool, "SELECT count(*) FROM clients WHERE id=$1", c.ID); n != 1 {
		t.Fatal("client deleted")
	}
}

func TestDeletingAClientDeletesItsUserAndInvitations(t *testing.T) {
	s, pool, _ := newService(t)
	org, def := newOrg(t, s, 10, true)
	owner := ownerOf(t, pool, org)
	ctx := context.Background()
	c, _ := s.CreateClient(ctx, owner, tenancy.ClientInput{Name: "C"})
	other, _ := s.CreateClient(ctx, owner, tenancy.ClientInput{Name: "Other"})
	user := userOf(t, pool, c)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, uid := range []uuid.UUID{user.UserID, owner.UserID} {
		exec(`INSERT INTO sessions (id, token_hash, user_id, expires_at, absolute_expires_at, last_seen_at, created_at, updated_at)
		      VALUES ($1, $2, $3, now() + interval '30 days', now() + interval '90 days', now(), now(), now())`,
			uuid.New(), []byte(uuid.NewString()), uid)
		exec(`INSERT INTO api_keys (id, user_id, name, prefix, token_hash, created_at, updated_at)
		      VALUES ($1, $2, 'n8n', 'abcdefgh', $3, now(), now())`, uuid.New(), uid, []byte(uuid.NewString()))
	}
	exec(`INSERT INTO invitations (id, organization_id, client_id, role, email, token_hash, expires_at, created_at, updated_at)
	      VALUES ($1, $2, $3, 'client', 'next@gym.example', $4, now() + interval '7 days', now(), now())`,
		uuid.New(), org.ID, c.ID, []byte(uuid.NewString()))

	if err := s.DeleteClient(ctx, owner, c.ID); err != nil {
		t.Fatal(err)
	}
	for q, arg := range map[string]uuid.UUID{
		"SELECT count(*) FROM clients WHERE id=$1":              c.ID,
		"SELECT count(*) FROM users WHERE id=$1":                user.UserID,
		"SELECT count(*) FROM sessions WHERE user_id=$1":        user.UserID,
		"SELECT count(*) FROM api_keys WHERE user_id=$1":        user.UserID,
		"SELECT count(*) FROM invitations WHERE client_id=$1":   c.ID,
		"SELECT count(*) FROM password_resets WHERE user_id=$1": user.UserID,
	} {
		if n := countRows(t, pool, q, arg); n != 0 {
			t.Errorf("%s: %d rows left", q, n)
		}
	}
	if countRows(t, pool, "SELECT count(*) FROM users WHERE id=$1", owner.UserID) != 1 ||
		countRows(t, pool, "SELECT count(*) FROM sessions WHERE user_id=$1", owner.UserID) != 1 ||
		countRows(t, pool, "SELECT count(*) FROM api_keys WHERE user_id=$1", owner.UserID) != 1 ||
		countRows(t, pool, "SELECT count(*) FROM clients WHERE id IN ($1, $2)", def.ID, other.ID) != 2 {
		t.Fatal("rows of the owner or other clients were deleted")
	}
}

func TestListClientsShowsCountsAndUserEmail(t *testing.T) {
	s, pool, _ := newService(t)
	org, def := newOrg(t, s, 10, true)
	owner := ownerOf(t, pool, org)
	ctx := context.Background()
	c, _ := s.CreateClient(ctx, owner, tenancy.ClientInput{Name: "C"})
	insertInstance(t, pool, org.ID, def.ID, "active")
	insertInstance(t, pool, org.ID, c.ID, "active")
	insertInstance(t, pool, org.ID, c.ID, "creating")
	cid := c.ID
	insertUser(t, pool, org.ID, tenancy.RoleClient, &cid, "old@gym.example")
	if _, err := pool.Exec(ctx, `UPDATE users SET disabled_at = now() WHERE email = 'old@gym.example'`); err != nil {
		t.Fatal(err)
	}
	insertUser(t, pool, org.ID, tenancy.RoleClient, &cid, "front@gym.example")
	list, err := s.ListClients(ctx, owner)
	if err != nil || len(list) != 2 {
		t.Fatalf("%v %d", err, len(list))
	}
	if list[0].ID != def.ID || list[0].InstanceCount != 1 || list[0].UserEmail != "" {
		t.Fatalf("default: %+v", list[0])
	}
	if list[1].ID != c.ID || list[1].InstanceCount != 2 || list[1].UserEmail != "front@gym.example" {
		t.Fatalf("C: %+v", list[1])
	}
}

func TestGetClient(t *testing.T) {
	s, pool, _ := newService(t)
	org, _ := newOrg(t, s, 10, true)
	owner := ownerOf(t, pool, org)
	ctx := context.Background()
	c, _ := s.CreateClient(ctx, owner, tenancy.ClientInput{Name: "C"})
	insertInstance(t, pool, org.ID, c.ID, "active")
	list, _ := s.ListClients(ctx, owner)
	got, err := s.GetClient(ctx, owner, c.ID)
	if err != nil || !reflect.DeepEqual(got, list[1]) {
		t.Fatalf("%v %+v vs %+v", err, got, list[1])
	}
}

func TestClientViewsShowThePendingInvitation(t *testing.T) {
	s, pool, clk := newService(t)
	org, def := newOrg(t, s, 10, true)
	owner := ownerOf(t, pool, org)
	ctx := context.Background()
	c := newClient(t, s, owner, tenancy.ClientInput{Name: "C"})
	other := newClient(t, s, owner, tenancy.ClientInput{Name: "Other"})
	expires := clk.Now().Add(-time.Hour) // an expired invitation that nobody accepted still shows
	invite := func(clientID uuid.UUID, email string, expiresAt time.Time, accepted bool) {
		t.Helper()
		_, err := pool.Exec(ctx, `INSERT INTO invitations (id, organization_id, client_id, role, email, token_hash, expires_at, accepted_at)
			VALUES ($1, $2, $3, 'client', $4, $5, $6, CASE WHEN $7 THEN now() END)`,
			uuid.New(), org.ID, clientID, email, []byte(uuid.NewString()), expiresAt, accepted)
		if err != nil {
			t.Fatal(err)
		}
	}
	invite(c.ID, "new@gym.example", expires, false)
	invite(other.ID, "done@gym.example", expires, true)

	check := func(v tenancy.ClientView) {
		t.Helper()
		switch v.ID {
		case c.ID:
			pi := v.PendingInvitation
			if pi == nil || pi.Email != "new@gym.example" || !pi.ExpiresAt.Equal(expires) {
				t.Fatalf("C: %+v", pi)
			}
		default:
			if v.PendingInvitation != nil {
				t.Fatalf("%s: %+v", v.Name, v.PendingInvitation)
			}
		}
	}
	list, err := s.ListClients(ctx, owner)
	if err != nil || len(list) != 3 {
		t.Fatalf("%v %d", err, len(list))
	}
	for _, v := range list {
		check(v)
	}
	for _, id := range []uuid.UUID{def.ID, c.ID, other.ID} {
		v, err := s.GetClient(ctx, owner, id)
		if err != nil {
			t.Fatal(err)
		}
		check(v)
	}
}
