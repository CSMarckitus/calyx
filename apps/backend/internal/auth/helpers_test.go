package auth_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/auth"
	"calyx/backend/internal/database/gen"
	"calyx/backend/internal/tenancy"
	"calyx/backend/internal/testutil"
)

var start = time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)

const password = "correct horse battery"

type env struct {
	svc    *auth.Service
	pool   *pgxpool.Pool
	clk    *testutil.FakeClock
	mailer *testutil.FakeMailer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := testutil.NewDB(t)
	clk := testutil.NewFakeClock(start)
	mailer := testutil.NewFakeMailer()
	pub, _ := url.Parse("http://localhost:8080")
	return &env{
		svc:    auth.NewService(pool, clk, mailer, pub, auth.NewLimiter(clk)),
		pool:   pool,
		clk:    clk,
		mailer: mailer,
	}
}

func newOrg(t *testing.T, pool *pgxpool.Pool) (orgID, defaultClientID uuid.UUID) {
	t.Helper()
	orgID, defaultClientID = uuid.New(), uuid.New()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (id, name, max_instances, whatsapp_web_allowed, created_at, updated_at)
		VALUES ($1, 'Org', 10, true, now(), now())`, orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO clients (id, organization_id, name, is_default, can_create_instances, max_instances, created_at, updated_at)
		VALUES ($1, $2, 'Default', true, false, 0, now(), now())`, defaultClientID, orgID); err != nil {
		t.Fatal(err)
	}
	return orgID, defaultClientID
}

func newClient(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `INSERT INTO clients (id, organization_id, name, is_default, can_create_instances, max_instances, created_at, updated_at)
		VALUES ($1, $2, $3, false, true, 5, now(), now())`, id, orgID, name); err != nil {
		t.Fatal(err)
	}
	return id
}

var (
	hashOnce sync.Once
	hashOfPw string
)

// testHash computes one argon2id hash of the test password per test binary.
func testHash(t *testing.T) string {
	t.Helper()
	hashOnce.Do(func() {
		h, err := auth.HashPassword(password)
		if err != nil {
			t.Fatal(err)
		}
		hashOfPw = h
	})
	return hashOfPw
}

// newUser inserts an owner (clientID nil) or a client user.
func newUser(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, clientID *uuid.UUID, email string) gen.User {
	t.Helper()
	role := "owner"
	if clientID != nil {
		role = "client"
	}
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `INSERT INTO users (id, email, password_hash, organization_id, role, client_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now(), now())`, id, email, testHash(t), orgID, role, clientID); err != nil {
		t.Fatal(err)
	}
	u, err := gen.New(pool).GetUserByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func principalOf(u gen.User) tenancy.Principal {
	return tenancy.Principal{UserID: u.ID, OrganizationID: u.OrganizationID, Role: tenancy.Role(u.Role), ClientID: u.ClientID}
}

func tokenOf(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	tok := u.Query().Get("token")
	if tok == "" {
		t.Fatalf("no token in the link")
	}
	return tok
}

func lastLink(t *testing.T, m *testutil.FakeMailer) string {
	t.Helper()
	sent := m.Sent()
	if len(sent) == 0 {
		t.Fatal("no mail was sent")
	}
	for _, line := range strings.Split(sent[len(sent)-1].Body, "\n") {
		if strings.HasPrefix(line, "http") {
			return strings.TrimSpace(line)
		}
	}
	t.Fatal("no link in the last mail")
	return ""
}

func countRows(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func requireCode(t *testing.T, err error, status int, code string) {
	t.Helper()
	e, ok := apierr.As(err)
	if !ok {
		t.Fatalf("want %d %s, got %v", status, code, err)
	}
	if e.Status != status || e.Code != code {
		t.Fatalf("want %d %s, got %d %s (%s)", status, code, e.Status, e.Code, e.Detail)
	}
}

// setup creates an org with an owner ana@x.example.
func setup(t *testing.T) (*env, uuid.UUID, gen.User) {
	t.Helper()
	e := newEnv(t)
	org, _ := newOrg(t, e.pool)
	return e, org, newUser(t, e.pool, org, nil, "ana@x.example")
}

// insertKey inserts an API key with plain SQL and returns its plain token.
func insertKey(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID, name string) string {
	t.Helper()
	plain, hash := auth.NewToken(auth.APIKeyPrefix)
	if _, err := pool.Exec(context.Background(), `INSERT INTO api_keys (id, user_id, name, prefix, token_hash, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, now(), now())`, uuid.New(), userID, name, plain[3:11], hash); err != nil {
		t.Fatal(err)
	}
	return plain
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

// captureLog replaces the default logger until the test ends. Call it before newEnv, because
// the service takes slog.Default() when it starts.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// insertSession inserts a login session with plain SQL and returns its plain token. It stands for
// a login that ran at the same time as a removal: the login checked the user before the removal.
func insertSession(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) string {
	t.Helper()
	plain, hash := auth.NewToken("")
	if _, err := pool.Exec(context.Background(), `INSERT INTO sessions (id, token_hash, user_id, expires_at, absolute_expires_at, last_seen_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now(), now())`, uuid.New(), hash, userID, start.Add(auth.SessionSlidingTTL), start.Add(auth.SessionAbsoluteTTL), start); err != nil {
		t.Fatal(err)
	}
	return plain
}
