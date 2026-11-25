package database_test

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"calyx/backend/internal/testutil"
)

func wantViolation(t *testing.T, err error, sqlstate, name string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("err = %v, want a PgError %s %s", err, sqlstate, name)
	}
	if pgErr.Code != sqlstate || pgErr.ConstraintName != name {
		t.Fatalf("got %s %q, want %s %q (%s)", pgErr.Code, pgErr.ConstraintName, sqlstate, name, pgErr.Message)
	}
}

func run(pool *pgxpool.Pool, sql string, args ...any) error {
	_, err := pool.Exec(context.Background(), sql, args...)
	return err
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func count(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	must(t, pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n))
	return n
}

func newOrg(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	must(t, run(pool, `INSERT INTO organizations (id, name, whatsapp_web_allowed) VALUES ($1, 'Org', true)`, id))
	return id
}

func insertClient(pool *pgxpool.Pool, org uuid.UUID, isDefault bool) (uuid.UUID, error) {
	id := uuid.New()
	return id, run(pool, `INSERT INTO clients (id, organization_id, name, is_default) VALUES ($1, $2, 'C', $3)`, id, org, isDefault)
}

func newClient(t *testing.T, pool *pgxpool.Pool, org uuid.UUID, isDefault bool) uuid.UUID {
	t.Helper()
	id, err := insertClient(pool, org, isDefault)
	must(t, err)
	return id
}

func insertUser(pool *pgxpool.Pool, email, role string, org uuid.UUID, client *uuid.UUID) (uuid.UUID, error) {
	id := uuid.New()
	return id, run(pool, `INSERT INTO users (id, email, password_hash, organization_id, role, client_id)
		VALUES ($1, $2, 'hash', $3, $4, $5)`, id, email, org, role, client)
}

func newUser(t *testing.T, pool *pgxpool.Pool, email, role string, org uuid.UUID, client *uuid.UUID) uuid.UUID {
	t.Helper()
	id, err := insertUser(pool, email, role, org, client)
	must(t, err)
	return id
}

func coreName(id uuid.UUID) string { return "i_" + strings.ReplaceAll(id.String(), "-", "") }

func insertInstance(pool *pgxpool.Pool, id, org, client uuid.UUID, coreName, provider string, phone, lastStatus *string) error {
	return run(pool, `INSERT INTO instances (id, organization_id, client_id, core_id, core_name, display_name,
		provider, state, phone_number_id, last_status)
		VALUES ($1, $2, $3, 'main', $4, 'Name', $5, 'active', $6, $7)`,
		id, org, client, coreName, provider, phone, lastStatus)
}

func newInstance(t *testing.T, pool *pgxpool.Pool, org, client uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	must(t, insertInstance(pool, id, org, client, coreName(id), "WHATSAPP_WEB", nil, nil))
	return id
}

func tokenHash() []byte { b := uuid.New(); return b[:] }

func ptr[T any](v T) *T { return &v }

func TestOneDefaultClientPerOrganization(t *testing.T) {
	pool := testutil.NewDB(t)
	a := newOrg(t, pool)
	newClient(t, pool, a, true)
	_, err := insertClient(pool, a, true)
	wantViolation(t, err, "23505", "clients_one_default_per_org")
	b := newOrg(t, pool)
	newClient(t, pool, b, true)
}

func TestOwnerHasNoClientAndClientUserHasOne(t *testing.T) {
	pool := testutil.NewDB(t)
	org := newOrg(t, pool)
	c := newClient(t, pool, org, false)
	_, err := insertUser(pool, "owner@example.com", "owner", org, &c)
	wantViolation(t, err, "23514", "users_role_client")
	_, err = insertUser(pool, "client@example.com", "client", org, nil)
	wantViolation(t, err, "23514", "users_role_client")
}

func TestOneActiveOwnerPerOrganization(t *testing.T) {
	pool := testutil.NewDB(t)
	org := newOrg(t, pool)
	first := newUser(t, pool, "a@example.com", "owner", org, nil)
	_, err := insertUser(pool, "b@example.com", "owner", org, nil)
	wantViolation(t, err, "23505", "users_one_active_owner_per_org")
	must(t, run(pool, `UPDATE users SET disabled_at = now() WHERE id = $1`, first))
	newUser(t, pool, "b@example.com", "owner", org, nil)
}

func TestOneActiveUserPerClient(t *testing.T) {
	pool := testutil.NewDB(t)
	org := newOrg(t, pool)
	c := newClient(t, pool, org, false)
	first := newUser(t, pool, "a@example.com", "client", org, &c)
	_, err := insertUser(pool, "b@example.com", "client", org, &c)
	wantViolation(t, err, "23505", "users_one_active_user_per_client")
	must(t, run(pool, `UPDATE users SET disabled_at = now() WHERE id = $1`, first))
	newUser(t, pool, "b@example.com", "client", org, &c)
}

func TestEmailsAreUniqueAndLowerCase(t *testing.T) {
	pool := testutil.NewDB(t)
	a, b := newOrg(t, pool), newOrg(t, pool)
	_, err := insertUser(pool, "Ana@Example.com", "owner", a, nil)
	wantViolation(t, err, "23514", "users_email_check")
	newUser(t, pool, "ana@example.com", "owner", a, nil)
	_, err = insertUser(pool, "ana@example.com", "owner", b, nil)
	wantViolation(t, err, "23505", "users_email_key")
}

func TestARowCannotNameAClientOfAnotherOrganization(t *testing.T) {
	pool := testutil.NewDB(t)
	a, b := newOrg(t, pool), newOrg(t, pool)
	clientOfA := newClient(t, pool, a, false)
	_, err := insertUser(pool, "x@example.com", "client", b, &clientOfA)
	wantViolation(t, err, "23503", "users_client_fkey")
	id := uuid.New()
	err = insertInstance(pool, id, b, clientOfA, coreName(id), "WHATSAPP_WEB", nil, nil)
	wantViolation(t, err, "23503", "instances_client_fkey")
}

func TestCoreNameFollowsTheID(t *testing.T) {
	pool := testutil.NewDB(t)
	org := newOrg(t, pool)
	c := newClient(t, pool, org, false)
	id := uuid.New()
	wantViolation(t, insertInstance(pool, id, org, c, "i_x", "WHATSAPP_WEB", nil, nil), "23514", "instances_core_name_from_id")
	must(t, insertInstance(pool, id, org, c, coreName(id), "WHATSAPP_WEB", nil, nil))
}

func TestPhoneNumberIDOnlyForMeta(t *testing.T) {
	pool := testutil.NewDB(t)
	org := newOrg(t, pool)
	c := newClient(t, pool, org, false)
	id := uuid.New()
	wantViolation(t, insertInstance(pool, id, org, c, coreName(id), "META_CLOUD", nil, nil), "23514", "instances_phone_number_id_meta_only")
	wantViolation(t, insertInstance(pool, id, org, c, coreName(id), "WHATSAPP_WEB", ptr("123"), nil), "23514", "instances_phone_number_id_meta_only")
	must(t, insertInstance(pool, id, org, c, coreName(id), "META_CLOUD", ptr("123"), nil))
	id2 := uuid.New()
	wantViolation(t, insertInstance(pool, id2, org, c, coreName(id2), "META_CLOUD", ptr("123"), nil), "23505", "instances_phone_number_id_key")
}

func TestLastStatusIsACoreStatusName(t *testing.T) {
	pool := testutil.NewDB(t)
	org := newOrg(t, pool)
	c := newClient(t, pool, org, false)
	id := uuid.New()
	wantViolation(t, insertInstance(pool, id, org, c, coreName(id), "WHATSAPP_WEB", nil, ptr("INSTANCE_STATUS_CONNECTED")), "23514", "instances_last_status_check")
	must(t, insertInstance(pool, id, org, c, coreName(id), "WHATSAPP_WEB", nil, ptr("NEEDS_AUTH")))
}

func insertWebhook(pool *pgxpool.Pool, instance uuid.UUID, event string) error {
	return run(pool, `INSERT INTO webhooks (id, instance_id, url, event) VALUES ($1, $2, 'https://example.com/h', $3)`,
		uuid.New(), instance, event)
}

func TestOneWebhookPerEvent(t *testing.T) {
	pool := testutil.NewDB(t)
	org := newOrg(t, pool)
	inst := newInstance(t, pool, org, newClient(t, pool, org, false))
	must(t, insertWebhook(pool, inst, "message.received"))
	wantViolation(t, insertWebhook(pool, inst, "message.received"), "23505", "webhooks_instance_id_event_key")
	must(t, insertWebhook(pool, inst, "message.sent"))
}

func TestDeletingAnInstanceDeletesItsWebhooksAndBulkJobs(t *testing.T) {
	pool := testutil.NewDB(t)
	org := newOrg(t, pool)
	c := newClient(t, pool, org, false)
	user := newUser(t, pool, "u@example.com", "client", org, &c)
	inst := newInstance(t, pool, org, c)
	must(t, insertWebhook(pool, inst, "message.received"))
	must(t, run(pool, `INSERT INTO bulk_jobs (id, instance_id, organization_id, client_id, created_by)
		VALUES ($1, $2, $3, $4, $5)`, uuid.New(), inst, org, c, user))
	if count(t, pool, "webhooks") != 1 || count(t, pool, "bulk_jobs") != 1 {
		t.Fatal("setup failed")
	}
	must(t, run(pool, `DELETE FROM instances WHERE id = $1`, inst))
	if count(t, pool, "webhooks") != 0 || count(t, pool, "bulk_jobs") != 0 {
		t.Fatal("webhooks or bulk_jobs remain")
	}
}

func TestAClientWithInstancesCannotBeDeleted(t *testing.T) {
	pool := testutil.NewDB(t)
	org := newOrg(t, pool)
	c := newClient(t, pool, org, false)
	newInstance(t, pool, org, c)
	wantViolation(t, run(pool, `DELETE FROM clients WHERE id = $1`, c), "23503", "instances_client_fkey")
}

func insertSessionAndKey(t *testing.T, pool *pgxpool.Pool, user uuid.UUID) {
	t.Helper()
	must(t, run(pool, `INSERT INTO sessions (id, token_hash, user_id, expires_at, absolute_expires_at, last_seen_at)
		VALUES ($1, $2, $3, now(), now(), now())`, uuid.New(), tokenHash(), user))
	must(t, run(pool, `INSERT INTO api_keys (id, user_id, name, prefix, token_hash)
		VALUES ($1, $2, 'key', 'abcdefgh', $3)`, uuid.New(), user, tokenHash()))
}

func TestDeletingAClientDeletesItsUserAndPendingInvitations(t *testing.T) {
	pool := testutil.NewDB(t)
	org := newOrg(t, pool)
	c := newClient(t, pool, org, false)
	user := newUser(t, pool, "u@example.com", "client", org, &c)
	insertSessionAndKey(t, pool, user)
	must(t, run(pool, `INSERT INTO invitations (id, organization_id, client_id, role, email, token_hash, expires_at)
		VALUES ($1, $2, $3, 'client', 'i@example.com', $4, now())`, uuid.New(), org, c, tokenHash()))
	must(t, run(pool, `DELETE FROM clients WHERE id = $1`, c))
	for _, table := range []string{"users", "sessions", "api_keys", "invitations"} {
		if n := count(t, pool, table); n != 0 {
			t.Errorf("%s has %d rows", table, n)
		}
	}
}

func TestEveryUniqueNameIsFixed(t *testing.T) {
	pool := testutil.NewDB(t)
	rows, err := pool.Query(context.Background(), `SELECT i.relname FROM pg_index x
		JOIN pg_class i ON i.oid = x.indexrelid JOIN pg_namespace n ON n.oid = i.relnamespace
		WHERE n.nspname = 'public' AND x.indisunique AND NOT x.indisprimary`)
	must(t, err)
	defer rows.Close()
	var got []string
	for rows.Next() {
		var n string
		must(t, rows.Scan(&n))
		got = append(got, n)
	}
	want := []string{
		"users_email_key", "users_one_active_owner_per_org", "users_one_active_user_per_client",
		"clients_one_default_per_org", "clients_organization_id_id_key", "instances_phone_number_id_key",
		"instances_core_id_core_name_key", "webhooks_instance_id_event_key", "sessions_token_hash_key",
		"api_keys_token_hash_key", "invitations_token_hash_key", "password_resets_token_hash_key",
		"invitations_one_pending_owner_per_org", "invitations_one_pending_per_client",
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unique indexes:\n got %v\nwant %v", got, want)
	}
}

func TestDeletingAUserDeletesItsSessionsKeysAndResets(t *testing.T) {
	pool := testutil.NewDB(t)
	org := newOrg(t, pool)
	user := newUser(t, pool, "u@example.com", "owner", org, nil)
	insertSessionAndKey(t, pool, user)
	must(t, run(pool, `INSERT INTO password_resets (id, user_id, token_hash, expires_at) VALUES ($1, $2, $3, now())`,
		uuid.New(), user, tokenHash()))
	must(t, run(pool, `DELETE FROM users WHERE id = $1`, user))
	for _, table := range []string{"sessions", "api_keys", "password_resets"} {
		if n := count(t, pool, table); n != 0 {
			t.Errorf("%s has %d rows", table, n)
		}
	}
}

func TestOnlyOwnerInvitationsReplaceAnOwner(t *testing.T) {
	pool := testutil.NewDB(t)
	org := newOrg(t, pool)
	c := newClient(t, pool, org, false)
	owner := newUser(t, pool, "o@example.com", "owner", org, nil)
	err := run(pool, `INSERT INTO invitations (id, organization_id, client_id, role, email, token_hash, expires_at, replaces_owner_id)
		VALUES ($1, $2, $3, 'client', 'i@example.com', $4, now(), $5)`, uuid.New(), org, c, tokenHash(), owner)
	wantViolation(t, err, "23514", "invitations_transfer_is_owner")
}

func TestOnePendingInvitationPerOwnerSlotAndPerClient(t *testing.T) {
	pool := testutil.NewDB(t)
	org := newOrg(t, pool)
	c := newClient(t, pool, org, false)
	ins := func(client *uuid.UUID, role string) error {
		return run(pool, `INSERT INTO invitations (id, organization_id, client_id, role, email, token_hash, expires_at)
			VALUES ($1, $2, $3, $4, 'i@example.com', $5, now())`, uuid.New(), org, client, role, tokenHash())
	}
	must(t, ins(nil, "owner"))
	wantViolation(t, ins(nil, "owner"), "23505", "invitations_one_pending_owner_per_org")
	must(t, ins(&c, "client"))
	wantViolation(t, ins(&c, "client"), "23505", "invitations_one_pending_per_client")
	// An accepted invitation does not count.
	must(t, run(pool, `UPDATE invitations SET accepted_at = now()`))
	must(t, ins(nil, "owner"))
	must(t, ins(&c, "client"))
}
