package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"calyx/backend/internal/auth"
	"calyx/backend/internal/bulk"
	"calyx/backend/internal/clock"
	"calyx/backend/internal/config"
	"calyx/backend/internal/core"
	"calyx/backend/internal/database/gen"
	"calyx/backend/internal/instances"
	"calyx/backend/internal/live"
	"calyx/backend/internal/mail"
	"calyx/backend/internal/tenancy"
	"calyx/backend/internal/testutil"
)

const testPassword = "correct horse battery"

var testStart = time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)

// syncBuffer is a bytes.Buffer that many goroutines can write.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

// lines returns the non-empty lines of the log.
func (b *syncBuffer) lines() []string {
	var out []string
	for _, l := range strings.Split(b.String(), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// testEnv is a whole backend over a test database and a fake core.
type testEnv struct {
	t       *testing.T
	Deps    Deps
	Handler http.Handler
	Fake    *testutil.FakeCore
	Mailer  *testutil.FakeMailer // fails with mail.ErrNotConfigured until the test calls MailOn
	Clock   *testutil.FakeClock
	Logs    *syncBuffer
}

// newTestDeps builds the services like cmd/backend does, over a fresh database and a fake core.
func newTestEnv(t *testing.T, mods ...func(*config.Config)) *testEnv {
	t.Helper()
	pool := testutil.NewDB(t)
	fake, conn := testutil.NewFakeCore(t)
	clk := testutil.NewFakeClock(testStart)
	mailer := testutil.NewFakeMailer()
	mailer.FailWith(mail.ErrNotConfigured)
	pub, _ := url.Parse("https://calyx.example")
	cfg := config.Config{
		PublicURL:      pub,
		CoreID:         "main",
		SecretKey:      bytes.Repeat([]byte{7}, 32),
		PairingTimeout: 3 * time.Minute,
		HTTPHost:       "127.0.0.1",
		HTTPPort:       8080,
	}
	for _, m := range mods {
		m(&cfg)
	}
	logs := &syncBuffer{}
	log := config.NewLogger("json", logs)
	// The services log through slog.Default() (cmd/backend sets it before it builds them).
	prev := slog.Default()
	slog.SetDefault(log)
	t.Cleanup(func() { slog.SetDefault(prev) })
	coreClient := core.NewClient(conn)
	authSvc := auth.NewService(pool, clk, mailer, cfg.PublicURL, auth.NewLimiter(clk))
	inst := instances.NewService(pool, coreClient, clk, cfg.SecretKey, cfg.CoreID, cfg.PairingTimeout)
	l := live.New()
	inst.SetLive(l)
	d := Deps{
		Config:    cfg,
		Auth:      authSvc,
		Tenancy:   tenancy.NewService(pool, clk),
		Instances: inst,
		Bulk:      bulk.NewService(pool, coreClient, inst),
		Pool:      pool,
		Clock:     clock.Clock(clk),
		Logger:    log,
		Live:      l,
	}
	t.Cleanup(inst.CancelAllPairings)
	return &testEnv{t: t, Deps: d, Handler: NewServer(d), Fake: fake, Mailer: mailer, Clock: clk, Logs: logs}
}

// MailOn makes the fake mailer record mails.
func (e *testEnv) MailOn() { e.Mailer.FailWith(nil) }

// testUser is a user with a session cookie and an API key.
type testUser struct {
	ID        uuid.UUID
	Email     string
	Principal tenancy.Principal
	Cookie    string // session token
	Key       string // API key token
}

// testOrg is an organization with its default client and its owner.
type testOrg struct {
	ID              uuid.UUID
	DefaultClientID uuid.UUID
	Owner           testUser
}

var (
	hashOnce sync.Once
	hashOfPw string
)

func testHash(t *testing.T) string {
	t.Helper()
	hashOnce.Do(func() {
		h, err := auth.HashPassword(testPassword)
		if err != nil {
			t.Fatal(err)
		}
		hashOfPw = h
	})
	return hashOfPw
}

// newOrg creates an organization with an owner. Users are written with SQL and a cached password
// hash, because hashing a password with argon2id is slow under -race.
func (e *testEnv) newOrg(name string, maxInstances int, webAllowed, ack bool) testOrg {
	e.t.Helper()
	ctx := context.Background()
	org, def, err := e.Deps.Tenancy.CreateOrganization(ctx, tenancy.NewOrganization{Name: name, MaxInstances: maxInstances, WhatsAppWebAllowed: webAllowed})
	if err != nil {
		e.t.Fatal(err)
	}
	owner := e.newUser(org.ID, nil, "owner-"+strings.ReplaceAll(strings.ToLower(name), " ", "-")+"@example.com")
	if ack {
		if _, err := e.Deps.Tenancy.AcknowledgeWhatsAppWeb(ctx, owner.Principal, true); err != nil {
			e.t.Fatal(err)
		}
	}
	return testOrg{ID: org.ID, DefaultClientID: def.ID, Owner: owner}
}

// newUser inserts an owner (clientID nil) or a client user, and gives it a session and an API key.
func (e *testEnv) newUser(orgID uuid.UUID, clientID *uuid.UUID, email string) testUser {
	e.t.Helper()
	ctx := context.Background()
	role := "owner"
	if clientID != nil {
		role = "client"
	}
	id := uuid.New()
	if _, err := e.Deps.Pool.Exec(ctx, `INSERT INTO users (id, email, password_hash, organization_id, role, client_id)
		VALUES ($1, $2, $3, $4, $5, $6)`, id, email, testHash(e.t), orgID, role, clientID); err != nil {
		e.t.Fatal(err)
	}
	u := testUser{ID: id, Email: email, Principal: tenancy.Principal{UserID: id, OrganizationID: orgID, Role: tenancy.Role(role), ClientID: clientID}}
	u.Cookie = e.login(email)
	k, err := e.Deps.Auth.CreateAPIKey(ctx, u.Principal, "test")
	if err != nil {
		e.t.Fatal(err)
	}
	u.Key = k.Token
	return u
}

// login returns a new session token. It passes an empty IP, so the rate limits do not count.
func (e *testEnv) login(email string) string {
	e.t.Helper()
	s, _, err := e.Deps.Auth.Login(context.Background(), email, testPassword, "")
	if err != nil {
		e.t.Fatal(err)
	}
	return s.Token
}

// newClient creates a client with the tenancy service.
func (e *testEnv) newClient(o testOrg, name string, canCreate bool, maxInstances int) gen.Client {
	e.t.Helper()
	c, err := e.Deps.Tenancy.CreateClient(context.Background(), o.Owner.Principal, tenancy.ClientInput{Name: name, CanCreateInstances: canCreate, MaxInstances: maxInstances})
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

// newClientUser creates the user of a client.
func (e *testEnv) newClientUser(o testOrg, clientID uuid.UUID, email string) testUser {
	e.t.Helper()
	return e.newUser(o.ID, &clientID, email)
}

// request options

type reqOpt func(*http.Request)

func withKey(token string) reqOpt {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

func withCookie(token string) reqOpt {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token}) }
}

func withOrigin(o string) reqOpt { return func(r *http.Request) { r.Header.Set("Origin", o) } }

func withHeader(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }

const goodOrigin = "https://calyx.example"

// do sends one request to the handler. A body that is a string is sent as it is; any other
// non-nil value is sent as JSON.
func (e *testEnv) do(method, path string, body any, opts ...reqOpt) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd io.Reader
	ct := ""
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
		ct = "application/json"
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
		ct = "application/json"
	}
	r := httptest.NewRequest(method, path, rd)
	if ct != "" {
		r.Header.Set("Content-Type", ct)
	}
	for _, o := range opts {
		o(r)
	}
	w := httptest.NewRecorder()
	e.Handler.ServeHTTP(w, r)
	return w
}

// asKey sends a request with the API key of u.
func (e *testEnv) asKey(u testUser, method, path string, body any, opts ...reqOpt) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.do(method, path, body, append([]reqOpt{withKey(u.Key)}, opts...)...)
}

// asCookie sends a request with the session cookie and the right Origin of u.
func (e *testEnv) asCookie(u testUser, method, path string, body any, opts ...reqOpt) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.do(method, path, body, append([]reqOpt{withCookie(u.Cookie), withOrigin(goodOrigin)}, opts...)...)
}

// result checks

func decodeMap(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("body is not a JSON object: %v\n%s", err, w.Body.String())
	}
	return m
}

func requireStatus(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status = %d, want %d. Body: %s", w.Code, want, w.Body.String())
	}
}

// requireProblem checks the status and the code of a problem answer and returns its detail.
func requireProblem(t *testing.T, w *httptest.ResponseRecorder, status int, code string) string {
	t.Helper()
	requireStatus(t, w, status)
	if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json. Body: %s", ct, w.Body.String())
	}
	m := decodeMap(t, w)
	if m["code"] != code {
		t.Fatalf("code = %v, want %s. Body: %s", m["code"], code, w.Body.String())
	}
	d, _ := m["detail"].(string)
	return d
}

// requireFields checks a 400 invalid_request answer and the locations of its errors[], in order.
// It returns the messages.
func requireFields(t *testing.T, w *httptest.ResponseRecorder, fields ...string) []string {
	t.Helper()
	body := w.Body.String()
	requireProblem(t, w, 400, "invalid_request")
	var p struct {
		Errors []struct{ Location, Message string } `json:"errors"`
	}
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatal(err)
	}
	var got, want, msgs []string
	for _, e := range p.Errors {
		got = append(got, e.Location)
		msgs = append(msgs, e.Message)
	}
	for _, f := range fields {
		want = append(want, "body."+f)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("errors = %v, want %v. Body: %s", got, want, body)
	}
	return msgs
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func ptr[T any](v T) *T { return &v }

// testConfigForRoutes is a configuration that is enough to build a server without a database.
func testConfigForRoutes() config.Config {
	pub, _ := url.Parse("https://calyx.example")
	return config.Config{PublicURL: pub}
}

// seedInstance writes an active WhatsApp Web instance row for a client (no core call).
func seedInstance(t *testing.T, e *testEnv, o testOrg, clientID uuid.UUID) testutil.SeededInstance {
	t.Helper()
	st := "CONNECTED"
	return testutil.SeedInstance(t, e.Deps.Pool, testutil.InstanceSeed{OrganizationID: o.ID, ClientID: clientID, LastStatus: &st})
}

// decodeBody reads a JSON object from a body string.
func decodeBody(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("body is not a JSON object: %v\n%s", err, body)
	}
	return m
}

// apiRoute is one route under /api/v1: a Huma operation.
type apiRoute struct {
	Method, Pattern string
	Auth            AuthMode
	OperationID     string
	Action          tenancy.Action // "" for a public operation
	access          access
}

// apiRoutes returns the Huma operations of the server in the order of routeTable. A route that is
// not in routeTable comes last. The tests that cover every route read this list.
func apiRoutes() []apiRoute {
	var out []apiRoute
	_, api := newMux(Deps{Config: testConfigForRoutes()})
	for _, item := range api.OpenAPI().Paths {
		for _, op := range []*huma.Operation{item.Get, item.Put, item.Post, item.Patch, item.Delete} {
			if op == nil {
				continue
			}
			r := apiRoute{Method: op.Method, Pattern: op.Path, Auth: LoggedIn, OperationID: op.OperationID}
			r.access, _ = op.Metadata[metaAccess].(access)
			if r.access == accessPublic {
				r.Auth = Public
			}
			r.Action, _ = op.Metadata[metaAction].(tenancy.Action)
			out = append(out, r)
		}
	}
	order := func(r apiRoute) int {
		for i, k := range routeTable {
			if k == (routeKey{r.Method, r.Pattern}) {
				return i
			}
		}
		return len(routeTable)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if order(out[i]) != order(out[j]) {
			return order(out[i]) < order(out[j])
		}
		return out[i].Method+" "+out[i].Pattern < out[j].Method+" "+out[j].Pattern
	})
	return out
}

// mcpCall sends one JSON-RPC request with the id 1 to POST /mcp. An empty apiKey sends no
// Authorization header. The map is the JSON-RPC answer, or nil when the answer is not JSON-RPC
// (for example a problem).
func mcpCall(t *testing.T, e *testEnv, apiKey, method string, params any, opts ...reqOpt) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		req["params"] = params
	}
	if apiKey != "" {
		opts = append([]reqOpt{withKey(apiKey)}, opts...)
	}
	w := e.do("POST", mcpPath, req, opts...)
	if w.Header().Get("Content-Type") != "application/json" {
		return w, nil
	}
	return w, decodeMap(t, w)
}
