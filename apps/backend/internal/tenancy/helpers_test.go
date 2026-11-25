package tenancy_test

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/database/gen"
	"calyx/backend/internal/tenancy"
	"calyx/backend/internal/testutil"
)

var start = time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)

var orgCounter atomic.Int64

func newService(t *testing.T) (*tenancy.Service, *pgxpool.Pool, *testutil.FakeClock) {
	t.Helper()
	pool := testutil.NewDB(t)
	clk := testutil.NewFakeClock(start)
	return tenancy.NewService(pool, clk), pool, clk
}

func newOrg(t *testing.T, s *tenancy.Service, maxInstances int, whatsAppWeb bool) (gen.Organization, gen.Client) {
	t.Helper()
	org, c, err := s.CreateOrganization(context.Background(), tenancy.NewOrganization{
		Name:               fmt.Sprintf("Org %d", orgCounter.Add(1)),
		MaxInstances:       maxInstances,
		WhatsAppWebAllowed: whatsAppWeb,
	})
	if err != nil {
		t.Fatal(err)
	}
	return org, c
}

func insertUser(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, role tenancy.Role, clientID *uuid.UUID, email string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash, organization_id, role, client_id, created_at, updated_at)
		 VALUES ($1, $2, 'x', $3, $4, $5, now(), now())`, id, email, orgID, string(role), clientID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func ownerOf(t *testing.T, pool *pgxpool.Pool, org gen.Organization) tenancy.Principal {
	t.Helper()
	id := insertUser(t, pool, org.ID, tenancy.RoleOwner, nil, "owner-"+uuid.NewString()+"@example.com")
	return tenancy.Principal{UserID: id, OrganizationID: org.ID, Role: tenancy.RoleOwner}
}

func userOf(t *testing.T, pool *pgxpool.Pool, c gen.Client) tenancy.Principal {
	t.Helper()
	cid := c.ID
	id := insertUser(t, pool, c.OrganizationID, tenancy.RoleClient, &cid, "user-"+uuid.NewString()+"@example.com")
	return tenancy.Principal{UserID: id, OrganizationID: c.OrganizationID, Role: tenancy.RoleClient, ClientID: &cid}
}

func countRows(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func insertInstance(t *testing.T, pool *pgxpool.Pool, orgID, clientID uuid.UUID, state string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO instances (id, organization_id, client_id, core_id, core_name, display_name, provider,
		                        send_only, enabled, state, created_at, updated_at)
		 VALUES ($1, $2, $3, 'main', $4, 'Test', 'WHATSAPP_WEB', false, true, $5, now(), now())`,
		id, orgID, clientID, tenancy.CoreName(id), state)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func requireCode(t *testing.T, err error, status int, code string) {
	t.Helper()
	e, ok := apierr.As(err)
	if !ok {
		t.Fatalf("want apierr %d %s, got %v", status, code, err)
	}
	if e.Status != status || e.Code != code {
		t.Fatalf("want %d %s, got %d %s (%s)", status, code, e.Status, e.Code, e.Detail)
	}
}

// requireFields checks a 400 invalid_request error and the paths of its field errors, in order.
func requireFields(t *testing.T, err error, fields ...string) {
	t.Helper()
	requireCode(t, err, 400, "invalid_request")
	e, _ := apierr.As(err)
	got := make([]string, 0, len(e.Errors))
	for _, fe := range e.Errors {
		got = append(got, fe.Field)
	}
	if strings.Join(got, ",") != strings.Join(fields, ",") {
		t.Fatalf("fields = %v, want %v (%+v)", got, fields, e.Errors)
	}
}

// errorsText joins the messages of the field errors of err.
func errorsText(err error) string {
	e, _ := apierr.As(err)
	var b strings.Builder
	for _, fe := range e.Errors {
		b.WriteString(fe.Message + "\n")
	}
	return b.String()
}
