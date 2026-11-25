package httpapi

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"calyx/backend/internal/bulk"
	"calyx/backend/internal/core"
	"calyx/backend/internal/instances"
	"calyx/backend/internal/tenancy"
	"calyx/backend/internal/testutil"
	corev1 "github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// The isolation matrix. Every route that needs a login is called by eight
// principals (four users, each with a session cookie and with an API key) against targets in four
// clients: A0, A1, A2 (organization A) and B0 (organization B).

type class int

const (
	clsOK class = iota
	clsANY
	cls403
	cls404
	cls403Key // 403 api_key_not_allowed: the operation needs a login cookie
)

func (c class) String() string { return [...]string{"OK", "ANY", "403", "404", "403Key"}[c] }

type matrixRow struct {
	n       int    // route number in routeTable
	pattern string // "N", "O", "S", "C" or "K"
	body    any
	okCode  int
}

const (
	pwSame = testPassword
)

var matrixRows = []matrixRow{
	{2, "N", nil, 204},
	{6, "N", nil, 200},
	{7, "N", map[string]string{"current_password": pwSame, "new_password": pwSame}, 204},
	{8, "N", nil, 200},
	{9, "N", map[string]string{"name": "matrix"}, 201},
	{10, "K", nil, 204},
	{11, "O", nil, 200},
	{12, "O", "ORGNAME", 200},
	{13, "O", map[string]bool{"accept": true}, 200},
	{14, "O", nil, 200},
	{15, "O", map[string]string{"name": "Matrix"}, 201},
	{16, "C", nil, 200},
	{17, "C", "CLIENTNAME", 200},
	{18, "C", nil, 409},
	{19, "C", "INVITE", 0},
	{20, "N", nil, 200},
	{21, "S", "NEWMETA", 201},
	{22, "S", nil, 200},
	{23, "S", map[string]string{"display_name": "Renamed"}, 200},
	{24, "S", nil, 204},
	{25, "S", map[string]string{"pairing": "qr"}, 202},
	{26, "S", nil, 200},
	{27, "S", nil, 204},
	{28, "S", map[string]string{"to": "34600000000", "text": "matrix"}, 200},
	{29, "S", map[string]any{"recipients": []string{"34600000001"}, "text": "matrix"}, 201},
	{30, "S", nil, 200},
	{31, "S", nil, 200},
	{32, "S", nil, 200},
	{35, "C", nil, 204},
	{36, "C", nil, 204},
	{37, "N", nil, 200},
}

// The four patterns. Rows are principals, columns are the target clients.
type principalDef struct {
	name   string
	user   string // oA, uA1, uA2, oB
	viaKey bool
}

var principalDefs = []principalDef{
	{"oA", "oA", false}, {"kA", "oA", true},
	{"uA1", "uA1", false}, {"kA1", "uA1", true},
	{"uA2", "uA2", false}, {"kA2", "uA2", true},
	{"oB", "oB", false}, {"kB", "oB", true},
}

var columns = []string{"A0", "A1", "A2", "B0"}

var patternS = map[string][4]class{
	"oA": {clsOK, clsOK, clsOK, cls404}, "uA1": {cls404, clsOK, cls404, cls404},
	"uA2": {cls404, cls404, clsOK, cls404}, "oB": {cls404, cls404, cls404, clsOK},
}
var patternC = map[string][4]class{
	"oA": {clsOK, clsOK, clsOK, cls404}, "uA1": {cls403, cls403, cls403, cls403},
	"uA2": {cls403, cls403, cls403, cls403}, "oB": {cls404, cls404, cls404, clsOK},
}
var patternK = map[string][4]class{ // columns are the users oA, uA1, uA2, oB
	"oA": {clsOK, cls404, cls404, cls404}, "uA1": {cls404, clsOK, cls404, cls404},
	"uA2": {cls404, cls404, clsOK, cls404}, "oB": {cls404, cls404, cls404, clsOK},
}

var userColumns = []string{"oA", "uA1", "uA2", "oB"}

// world is the set of organizations, clients, users and principals.
type world struct {
	e        *testEnv
	orgA     testOrg
	orgB     testOrg
	clientID map[string]uuid.UUID // A0, A1, A2, B0
	users    map[string]*testUser // oA, uA1, uA2, oB
	orgName  map[string]string    // by user
	owners   map[string]testUser  // "A" and "B": the owner who creates the targets
	srv      *httptest.Server
	counter  int
}

func (w *world) orgOf(col string) testOrg {
	if col == "B0" {
		return w.orgB
	}
	return w.orgA
}

func buildWorld(t *testing.T) *world {
	e := newTestEnv(t)
	w := &world{e: e, clientID: map[string]uuid.UUID{}, users: map[string]*testUser{}, orgName: map[string]string{}}
	w.orgA = e.newOrg("Org A", 10000, true, true)
	w.orgB = e.newOrg("Org B", 10000, true, true)
	a1 := e.newClient(w.orgA, "A1", true, 0)
	a2 := e.newClient(w.orgA, "A2", true, 0)
	w.clientID = map[string]uuid.UUID{"A0": w.orgA.DefaultClientID, "A1": a1.ID, "A2": a2.ID, "B0": w.orgB.DefaultClientID}
	oa, ob := w.orgA.Owner, w.orgB.Owner
	ua1 := e.newClientUser(w.orgA, a1.ID, "ua1@example.com")
	ua2 := e.newClientUser(w.orgA, a2.ID, "ua2@example.com")
	w.users = map[string]*testUser{"oA": &oa, "uA1": &ua1, "uA2": &ua2, "oB": &ob}
	w.orgName = map[string]string{"oA": "Org A", "uA1": "Org A", "uA2": "Org A", "oB": "Org B"}
	w.owners = map[string]testUser{"A": oa, "B": ob}
	w.srv = httptest.NewServer(e.Handler)
	t.Cleanup(w.srv.Close)
	e.Fake.OnSendPlainText(func(ctx context.Context, req *corev1.SendPlainTextRequest) (*corev1.SendPlainTextResponse, error) {
		return &corev1.SendPlainTextResponse{MessageId: "wamid.m"}, nil
	})
	return w
}

func (w *world) unique() string {
	w.counter++
	return fmt.Sprintf("%d%06d", time.Now().UnixNano()%1e9, w.counter)
}

// relogin gives a user a new session, after the limit of the login window has passed.
func (w *world) relogin(user string) {
	w.e.Clock.Advance(61 * time.Second)
	w.users[user].Cookie = w.e.login(w.users[user].Email)
}

func (w *world) ownerOf(col string) testUser {
	if col == "B0" {
		return w.owners["B"]
	}
	return w.owners["A"]
}

// freshMeta creates a connected Meta instance in a client through the service.
func (w *world) freshMeta(t *testing.T, col string) uuid.UUID {
	t.Helper()
	pn := w.unique()
	v, _, err := w.e.Deps.Instances.Create(context.Background(), w.ownerOf(col).Principal, instances.CreateInput{
		ClientID: w.clientID[col].String(), DisplayName: "Matrix", Provider: "META_CLOUD",
		Meta: &core.MetaCredentials{PhoneNumberID: pn, AccessToken: "t", AppSecret: "s"}})
	if err != nil {
		t.Fatalf("fresh Meta instance in %s: %v", col, err)
	}
	return v.ID
}

// freshPairing starts the creation of a WhatsApp Web instance: its pairing session runs.
func (w *world) freshPairing(t *testing.T, col string) uuid.UUID {
	t.Helper()
	v, _, err := w.e.Deps.Instances.Create(context.Background(), w.ownerOf(col).Principal, instances.CreateInput{
		ClientID: w.clientID[col].String(), DisplayName: "Matrix pairing", Provider: "WHATSAPP_WEB", Pairing: "qr", Locale: ptr("es-ES")})
	if err != nil {
		t.Fatalf("fresh pairing in %s: %v", col, err)
	}
	return v.ID
}

// freshNeedsAuth writes a WhatsApp Web row that needs a new pairing, and the instance in the core.
func (w *world) freshNeedsAuth(t *testing.T, col string) uuid.UUID {
	t.Helper()
	st := "NEEDS_AUTH"
	org := w.orgOf(col)
	s := testutil.SeedInstance(t, w.e.Deps.Pool, testutil.InstanceSeed{OrganizationID: org.ID, ClientID: w.clientID[col], LastStatus: &st})
	w.e.Fake.AddInstance(s.CoreName, corev1.Provider_PROVIDER_WHATSAPP_WEB, corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH)
	return s.ID
}

func (w *world) freshJob(t *testing.T, col string) (uuid.UUID, string) {
	t.Helper()
	id := w.freshMeta(t, col)
	job, err := w.e.Deps.Bulk.Create(context.Background(), w.ownerOf(col).Principal, id, bulk.Input{Recipients: []string{"34600000001"}, Text: ptr("x")})
	if err != nil {
		t.Fatal(err)
	}
	return id, job.ID
}

// freshClient creates a client of organization A with a user, or with a pending invitation.
func (w *world) freshClient(t *testing.T, withUser bool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	c, err := w.e.Deps.Tenancy.CreateClient(ctx, w.orgA.Owner.Principal, tenancy.ClientInput{Name: "Fresh " + w.unique()})
	if err != nil {
		t.Fatal(err)
	}
	email := "f" + w.unique() + "@example.com"
	if withUser {
		_, err = w.e.Deps.Pool.Exec(ctx, `INSERT INTO users (id, email, password_hash, organization_id, role, client_id)
			VALUES ($1, $2, $3, $4, 'client', $5)`, uuid.New(), email, testHash(t), w.orgA.ID, c.ID)
	} else {
		_, err = w.e.Deps.Auth.InviteClientUser(ctx, w.orgA.Owner.Principal, c.ID, email)
	}
	if err != nil {
		t.Fatal(err)
	}
	return c.ID
}

// requireClientAccess checks that a fresh client still has its active user, or its pending invitation.
func (w *world) requireClientAccess(t *testing.T, clientID uuid.UUID, user bool) {
	t.Helper()
	q := `SELECT count(*) FROM invitations WHERE client_id = $1 AND accepted_at IS NULL AND expires_at > now()`
	what := "pending invitation"
	if user {
		q = `SELECT count(*) FROM users WHERE client_id = $1`
		what = "user"
	}
	var n int
	if err := w.e.Deps.Pool.QueryRow(context.Background(), q, clientID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("the fresh client has %d %s rows after a 404 cell, want 1", n, what)
	}
}

// cancelPairing ends the session that a cell started, so a later cell does not see it.
func (w *world) cancelPairing(col string, id uuid.UUID) {
	_ = w.e.Deps.Instances.CancelPairing(context.Background(), w.ownerOf(col).Principal, id)
}

// target is what a cell calls.
type target struct {
	path    string
	body    any
	marker  string // the core name or the job id: no core request of a 404 cell may name it
	cleanup func()
	// unchanged checks, after a 404 cell, that the cell changed nothing.
	unchanged func(t *testing.T)
	expectOK  int // overrides the row's OK status when not 0
	okCode    string
}

// cookieOnlyRows are the routes with the level accessLoggedInCookie.
var cookieOnlyRows = map[int]bool{2: true, 7: true, 9: true, 13: true, 19: true, 35: true, 36: true, 37: true}

func classOf(row matrixRow, p principalDef, col int) class {
	c := patternClassOf(row, p, col)
	// The role check runs first, so a cls403 cell stays cls403.
	if p.viaKey && cookieOnlyRows[row.n] && c != cls403 {
		return cls403Key
	}
	return c
}

func patternClassOf(row matrixRow, p principalDef, col int) class {
	switch row.pattern {
	case "N":
		return clsOK
	case "O":
		if strings.HasPrefix(p.user, "o") {
			return clsOK
		}
		return cls403
	case "S":
		return patternS[p.user][col]
	case "C":
		c := patternC[p.user][col]
		if c == clsOK && row.n == 19 {
			return clsANY
		}
		// The default clients A0 and B0 have no invitation and no user.
		if c == clsOK && (row.n == 35 || row.n == 36) && (col == 0 || col == 3) {
			return cls404
		}
		return c
	case "K":
		return patternK[p.user][col]
	}
	panic("pattern " + row.pattern)
}

func (w *world) targetFor(t *testing.T, row matrixRow, p principalDef, colIdx int) target {
	t.Helper()
	col := columns[colIdx]
	switch row.n {
	case 2, 6, 7, 8, 9, 11, 13, 14, 15, 20, 37:
		paths := map[int]string{2: "/auth/logout", 6: "/me", 7: "/me/password", 8: "/api-keys", 9: "/api-keys", 11: "/organization",
			13: "/organization/whatsapp-web-acknowledgement", 14: "/clients", 15: "/clients", 20: "/instances", 37: "/events"}
		method := map[int]string{2: "POST", 6: "GET", 7: "PUT", 8: "GET", 9: "POST", 11: "GET", 13: "POST", 14: "GET", 15: "POST", 20: "GET", 37: "GET"}[row.n]
		return target{path: method + " /api/v1" + paths[row.n], body: row.body}
	case 12:
		return target{path: "PATCH /api/v1/organization", body: map[string]string{"name": w.orgName[p.user]}}
	case 10:
		u := w.users[userColumns[colIdx]]
		k, err := w.e.Deps.Auth.CreateAPIKey(context.Background(), u.Principal, "fresh")
		if err != nil {
			t.Fatal(err)
		}
		return target{path: "DELETE /api/v1/api-keys/" + k.ID.String()}
	case 16, 17, 18, 19:
		id := w.clientID[col]
		tg := target{path: map[int]string{16: "GET", 17: "PATCH", 18: "DELETE", 19: "POST"}[row.n] + " /api/v1/clients/" + id.String()}
		switch row.n {
		case 17:
			var name string
			if err := w.e.Deps.Pool.QueryRow(context.Background(), `SELECT name FROM clients WHERE id = $1`, id).Scan(&name); err != nil {
				t.Fatal(err)
			}
			tg.body = map[string]string{"name": name}
		case 18:
			tg.expectOK = 409
			if col == "A1" || col == "A2" {
				w.freshMeta(t, col)
				tg.okCode = "client_has_instances"
			} else {
				tg.okCode = "default_client"
			}
		case 19:
			tg.path += "/invitations"
			tg.body = map[string]string{"email": "m" + w.unique() + "@example.com"}
		}
		return tg
	case 35, 36:
		// A cell in A1 or A2 gets a fresh client of organization A, so it does not change the
		// clients of the world.
		id := w.clientID[col]
		fresh := col == "A1" || col == "A2"
		if fresh {
			id = w.freshClient(t, row.n == 36)
		}
		suffix := map[int]string{35: "/invitations", 36: "/user"}[row.n]
		tg := target{path: "DELETE /api/v1/clients/" + id.String() + suffix}
		if fresh {
			tg.unchanged = func(t *testing.T) { w.requireClientAccess(t, id, row.n == 36) }
		}
		return tg
	case 21:
		return target{path: "POST /api/v1/instances", body: map[string]any{
			"client_id": w.clientID[col].String(), "display_name": "Matrix", "provider": "META_CLOUD",
			"meta_credentials": map[string]string{"phone_number_id": w.unique(), "access_token": "t", "app_secret": "s"}}}
	case 22, 23, 24, 28, 29, 30:
		id := w.freshMeta(t, col)
		sub := map[int]string{22: "GET ", 23: "PATCH ", 24: "DELETE ", 28: "POST ", 29: "POST ", 30: "GET "}[row.n]
		suffix := map[int]string{28: "/messages", 29: "/bulk-jobs", 30: "/bulk-jobs"}[row.n]
		return target{path: sub + "/api/v1/instances/" + id.String() + suffix, body: row.body, marker: tenancy.CoreName(id)}
	case 25:
		id := w.freshNeedsAuth(t, col)
		return target{path: "POST /api/v1/instances/" + id.String() + "/pair", body: row.body, marker: tenancy.CoreName(id),
			cleanup: func() { w.cancelPairing(col, id) }}
	case 26, 27:
		id := w.freshPairing(t, col)
		method := map[int]string{26: "GET ", 27: "DELETE "}[row.n]
		return target{path: method + "/api/v1/instances/" + id.String() + "/pairing", marker: tenancy.CoreName(id),
			cleanup: func() { w.cancelPairing(col, id) }}
	case 31, 32:
		_, job := w.freshJob(t, col)
		method := map[int]string{31: "GET ", 32: "POST "}[row.n]
		suffix := map[int]string{31: "", 32: "/cancel"}[row.n]
		return target{path: method + "/api/v1/bulk-jobs/" + job + suffix, marker: job}
	}
	t.Fatalf("no target for route %d", row.n)
	return target{}
}

// result is the answer of a cell.
type result struct {
	status int
	body   string
	code   string
}

// send calls the server. The streams (routes 26 and 37) use the real server and read the first
// event only.
func (w *world) send(t *testing.T, row matrixRow, p principalDef, tg target) result {
	t.Helper()
	method, path, _ := strings.Cut(tg.path, " ")
	u := w.users[p.user]
	if row.n == 26 || row.n == 37 {
		req, _ := http.NewRequest(method, w.srv.URL+path, nil)
		if p.viaKey {
			req.Header.Set("Authorization", "Bearer "+u.Key)
		} else {
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: u.Cookie})
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			br := bufio.NewReader(resp.Body)
			first := readBlock(t, br)
			if want := map[int]string{26: "event: update\n", 37: "event: changed\n"}[row.n]; !strings.HasPrefix(first, want) {
				t.Fatalf("first block = %q", first)
			}
			return result{status: 200}
		}
		buf := new(strings.Builder)
		_, _ = bufioCopy(buf, resp.Body)
		return result{status: resp.StatusCode, body: buf.String(), code: codeOf(buf.String())}
	}
	body := tg.body
	var opts []reqOpt
	if p.viaKey {
		opts = append(opts, withKey(u.Key))
	} else {
		opts = append(opts, withCookie(u.Cookie), withOrigin(goodOrigin))
	}
	rec := w.e.do(method, path, body, opts...)
	return result{status: rec.Code, body: rec.Body.String(), code: codeOf(rec.Body.String())}
}

func bufioCopy(sb *strings.Builder, r interface{ Read([]byte) (int, error) }) (int, error) {
	b := make([]byte, 4096)
	total := 0
	for {
		n, err := r.Read(b)
		sb.Write(b[:n])
		total += n
		if err != nil {
			return total, nil
		}
	}
}

func codeOf(body string) string {
	i := strings.Index(body, `"code":"`)
	if i < 0 {
		return ""
	}
	code, _, _ := strings.Cut(body[i+len(`"code":"`):], `"`)
	return code
}

var allMethods = []string{
	testutil.MethodCreateInstance, testutil.MethodRemoveInstance, testutil.MethodUpdateInstanceConfig, testutil.MethodListInstances,
	testutil.MethodGetInstanceStatus, testutil.MethodSendPlainText, testutil.MethodSendBulkSameMessage,
	testutil.MethodSendBulkCustomMessages, testutil.MethodGetBulkJob, testutil.MethodGetBulkJobs, testutil.MethodCancelBulkJob,
}

func (w *world) coreCounts() map[string]int {
	out := map[string]int{}
	for _, m := range allMethods {
		out[m] = len(w.e.Fake.Requests(m))
	}
	return out
}

// newCoreRequests returns the text of every core request since the counts were taken.
func (w *world) newCoreRequests(before map[string]int) []string {
	var out []string
	for _, m := range allMethods {
		reqs := w.e.Fake.Requests(m)
		for _, r := range reqs[before[m]:] {
			out = append(out, m+" "+fmt.Sprint(r.Message))
		}
	}
	return out
}

func TestIsolationMatrix(t *testing.T) {
	started := time.Now()
	w := buildWorld(t)

	// A route that is not in the matrix fails the test, so a new route cannot skip it.
	// The public routes have no row: their tests are in auth_ops_test.go and pages_test.go.
	inMatrix := map[int]bool{}
	for _, n := range publicRoutes {
		inMatrix[n] = true
	}
	for _, r := range matrixRows {
		inMatrix[r.n] = true
	}
	routes := apiRoutes()
	if len(routes) != len(routeTable) {
		t.Errorf("%d routes, want %d", len(routes), len(routeTable))
	}
	for i, r := range routes {
		if i >= len(routeTable) || (routeKey{r.Method, r.Pattern}) != routeTable[i] {
			t.Errorf("route %s %s is not route %d of the plan", r.Method, r.Pattern, i+1)
			continue
		}
		if !inMatrix[i+1] {
			t.Errorf("route %d (%s %s) is not in the isolation matrix", i+1, r.Method, r.Pattern)
		}
		if (r.Auth == Public) != slices.Contains(publicRoutes, i+1) {
			t.Errorf("route %d has an unexpected auth mode", i+1)
		}
		if r.OperationID != "" && r.Action != actionOfRoute(i+1) {
			t.Errorf("route %d (%s %s) has the action %q, want %q", i+1, r.Method, r.Pattern, r.Action, actionOfRoute(i+1))
		}
	}

	cells := 0
	for _, row := range matrixRows {
		for _, p := range principalDefs {
			ncol := 4
			if row.pattern == "N" || row.pattern == "O" {
				ncol = 1
			}
			for c := 0; c < ncol; c++ {
				row, p, c := row, p, c
				name := fmt.Sprintf("%d %s %s", row.n, p.name, columns[c])
				if ncol == 1 {
					name = fmt.Sprintf("%d %s", row.n, p.name)
				}
				cells++
				t.Run(name, func(t *testing.T) { w.runCell(t, row, p, c) })
			}
		}
	}
	t.Logf("%d cells in %s", cells, time.Since(started).Round(time.Millisecond))
	if d := time.Since(started); d > 60*time.Second {
		t.Errorf("the matrix took %s, the limit is 60 s", d)
	}
}

// publicRoutes are the numbers of the routes that need no login.
var publicRoutes = []int{1, 3, 4, 5, 33, 34}

// actionOfRoute is the action of route n of routeTable. The public
// routes have none.
func actionOfRoute(n int) tenancy.Action {
	switch {
	case slices.Contains(publicRoutes, n):
		return ""
	case n == 35 || n == 36:
		return tenancy.ManageClients
	case n <= 10:
		return tenancy.ManageOwnAccount
	case n <= 13:
		return tenancy.ManageOrganization
	case n <= 19:
		return tenancy.ManageClients
	case n == 21:
		return tenancy.CreateInstance
	}
	return tenancy.UseInstances
}

// An owner-only operation refuses a client user before Huma reads the body: an invalid body
// still gets 403 owner_only. The test checks every operation with a body whose action a client
// user does not have. The test operations of api_test.go are on the server too, so the check
// runs also while no production operation is owner-only.
func TestOwnerOnlyOperationsRefuseAClientUserFirst(t *testing.T) {
	e := newTestEnv(t)
	mux, api := newMux(e.Deps)
	registerTestOps(api)
	e.Handler = mux
	o := e.newOrg("Org One", 10, true, true)
	c := e.newClient(o, "Client", true, 1)
	cu := e.newClientUser(o, c.ID, "client@example.com")
	const bad = `{"zzz_unknown": 5}`
	checked := 0
	for _, item := range api.OpenAPI().Paths {
		for _, op := range []*huma.Operation{item.Put, item.Post, item.Patch} {
			if op == nil || op.RequestBody == nil {
				continue
			}
			action, _ := op.Metadata[metaAction].(tenancy.Action)
			if action == "" || tenancy.Allow(cu.Principal, action) == nil {
				continue
			}
			checked++
			path := strings.ReplaceAll(op.Path, "{id}", uuid.NewString())
			t.Run(op.OperationID, func(t *testing.T) {
				requireProblem(t, e.asKey(cu, op.Method, path, bad), 403, "owner_only")
				requireProblem(t, e.asCookie(cu, op.Method, path, bad), 403, "owner_only")
				// The body is invalid: the owner gets 400.
				requireProblem(t, e.asCookie(o.Owner, op.Method, path, bad), 400, "invalid_request")
			})
		}
	}
	if checked == 0 {
		t.Fatal("no owner-only operation with a body: the test operations are missing")
	}
}

func (w *world) runCell(t *testing.T, row matrixRow, p principalDef, colIdx int) {
	if row.n == 2 && !p.viaKey {
		w.relogin(p.user)       // a fresh session that the cell ends
		defer w.relogin(p.user) // and a new one for the cells that follow
	}
	tg := w.targetFor(t, row, p, colIdx)
	if tg.cleanup != nil {
		defer tg.cleanup()
	}
	if row.n == 7 {
		defer w.relogin(p.user) // the change ends the sessions that it does not keep
	}
	want := classOf(row, p, colIdx)
	before := w.coreCounts()
	res := w.send(t, row, p, tg)
	newReqs := w.newCoreRequests(before)

	okCode := row.okCode
	if tg.expectOK != 0 {
		okCode = tg.expectOK
	}
	switch want {
	case clsOK:
		if res.status != okCode {
			t.Fatalf("status = %d, want %d. Body: %s", res.status, okCode, res.body)
		}
		if tg.okCode != "" && res.code != tg.okCode {
			t.Fatalf("code = %q, want %q", res.code, tg.okCode)
		}
	case clsANY:
		if res.status == 401 || res.status == 403 || res.status == 404 {
			t.Fatalf("status = %d, want an authorized answer. Body: %s", res.status, res.body)
		}
	case cls403:
		if res.status != 403 || res.code != "owner_only" {
			t.Fatalf("answer = %d %s, want 403 owner_only", res.status, res.body)
		}
	case cls404:
		if res.status != 404 || res.code != "not_found" {
			t.Fatalf("answer = %d %s, want 404 not_found", res.status, res.body)
		}
		if tg.unchanged != nil {
			tg.unchanged(t)
		}
	case cls403Key:
		if res.status != 403 || res.code != "api_key_not_allowed" {
			t.Fatalf("answer = %d %s, want 403 api_key_not_allowed", res.status, res.body)
		}
	}
	if want == cls403 || want == cls404 || want == cls403Key {
		for _, r := range newReqs {
			if tg.marker == "" || strings.Contains(r, tg.marker) {
				t.Fatalf("the core received a request in a refused cell: %s", r)
			}
		}
	}
	if row.n == 20 && want == clsOK {
		w.checkList(t, p, res.body)
	}
}

// checkList compares the ids of GET /instances with the instances of the scope of the principal.
func (w *world) checkList(t *testing.T, p principalDef, body string) {
	t.Helper()
	u := w.users[p.user]
	var q string
	var args []any
	if u.Principal.ClientID != nil {
		q, args = `SELECT id FROM instances WHERE organization_id = $1 AND client_id = $2`, []any{u.Principal.OrganizationID, *u.Principal.ClientID}
	} else {
		q, args = `SELECT id FROM instances WHERE organization_id = $1`, []any{u.Principal.OrganizationID}
	}
	rows, err := w.e.Deps.Pool.Query(context.Background(), q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := map[string]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		want[id.String()] = true
	}
	got := map[string]bool{}
	for _, it := range decodeBody(t, body)["items"].([]any) {
		got[it.(map[string]any)["id"].(string)] = true
	}
	if len(got) != len(want) {
		t.Fatalf("list has %d instances, the scope has %d", len(got), len(want))
	}
	for id := range want {
		if !got[id] {
			t.Fatalf("instance %s of the scope is missing from the list", id)
		}
	}
}
