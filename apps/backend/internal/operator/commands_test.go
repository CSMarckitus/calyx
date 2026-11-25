package operator_test

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/auth"
	"calyx/backend/internal/config"
	"calyx/backend/internal/core"
	"calyx/backend/internal/mail"
	"calyx/backend/internal/operator"
	"calyx/backend/internal/tenancy"
	"calyx/backend/internal/testutil"
	corev1 "github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

var testStart = time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)

const password = "correct horse battery"

type opEnv struct {
	t      *testing.T
	deps   operator.Deps
	pool   *pgxpool.Pool
	fake   *testutil.FakeCore
	mailer *testutil.FakeMailer
	clk    *testutil.FakeClock
}

func newOpEnv(t *testing.T, mods ...func(*config.Config)) *opEnv {
	t.Helper()
	return newOpEnvOn(t, testutil.NewDB(t), mods...)
}

// newOpEnvOn builds the services over a given pool (an empty database for the "not migrated" test).
func newOpEnvOn(t *testing.T, pool *pgxpool.Pool, mods ...func(*config.Config)) *opEnv {
	t.Helper()
	fake, conn := testutil.NewFakeCore(t)
	clk := testutil.NewFakeClock(testStart)
	mailer := testutil.NewFakeMailer()
	mailer.FailWith(mail.ErrNotConfigured)
	pub, _ := url.Parse("https://calyx.example")
	cfg := config.Config{PublicURL: pub, CoreID: "main", WhatsAppWebDefault: true}
	for _, m := range mods {
		m(&cfg)
	}
	return &opEnv{
		t: t, pool: pool, fake: fake, mailer: mailer, clk: clk,
		deps: operator.Deps{
			Config:  cfg,
			Pool:    pool,
			Auth:    auth.NewService(pool, clk, mailer, cfg.PublicURL, auth.NewLimiter(clk)),
			Tenancy: tenancy.NewService(pool, clk),
			Core:    core.NewClient(conn),
			Mailer:  mailer,
		},
	}
}

func (o *opEnv) run(args ...string) (code int, stdout, stderr string) {
	o.t.Helper()
	var out, errOut bytes.Buffer
	code = operator.Run(context.Background(), args, o.deps, &out, &errOut)
	return code, out.String(), errOut.String()
}

func (o *opEnv) count(query string, args ...any) int {
	o.t.Helper()
	var n int
	if err := o.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		o.t.Fatal(err)
	}
	return n
}

var (
	hashOnce sync.Once
	hashOfPw string
)

// userWithPassword inserts a user with the password above.
func (o *opEnv) userWithPassword(orgID uuid.UUID, email string) uuid.UUID {
	o.t.Helper()
	hashOnce.Do(func() {
		h, err := auth.HashPassword(password)
		if err != nil {
			o.t.Fatal(err)
		}
		hashOfPw = h
	})
	id := uuid.New()
	if _, err := o.pool.Exec(context.Background(), `INSERT INTO users (id, email, password_hash, organization_id, role) VALUES ($1, $2, $3, $4, 'owner')`,
		id, email, hashOfPw, orgID); err != nil {
		o.t.Fatal(err)
	}
	return id
}

func lines(s string) []string { return strings.Split(strings.TrimRight(s, "\n"), "\n") }

func TestNoCommandIsUsage(t *testing.T) {
	o := newOpEnv(t)
	code, out, errOut := o.run()
	if code != 2 || out != "" || !strings.Contains(errOut, "Usage: backend <command>") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestHelp(t *testing.T) {
	o := newOpEnv(t)
	for _, a := range []string{"help", "-h", "--help"} {
		code, out, errOut := o.run(a)
		if code != 0 || out != operator.Usage || errOut != "" {
			t.Fatalf("%s: code=%d out=%q err=%q", a, code, out, errOut)
		}
	}
	usage := operator.Usage
	for _, part := range []string{"Usage: backend <command> [flags]", "instances orphans", "org create --name N --owner-email E", "Every command reads the same environment variables as the server, except openapi, help and --version.", "openapi"} {
		if !strings.Contains(usage, part) {
			t.Fatalf("Usage has no %q", part)
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	o := newOpEnv(t)
	code, _, errOut := o.run("org", "frob")
	if code != 2 || !strings.HasPrefix(errOut, `Error: unknown command "org frob".`) || !strings.Contains(errOut, "Usage: backend <command>") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	code, _, errOut = o.run("org")
	if code != 2 || !strings.HasPrefix(errOut, `Error: unknown command "org".`) {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	code, _, errOut = o.run("frob", "x")
	if code != 2 || !strings.HasPrefix(errOut, `Error: unknown command "frob".`) {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

func TestOrgCreateWithoutMail(t *testing.T) {
	o := newOpEnv(t)
	code, out, errOut := o.run("org", "create", "--name", "Gym Group", "--owner-email", "ana@example.com")
	if code != 0 || errOut != "" {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	ls := lines(out)
	if len(ls) != 10 {
		t.Fatalf("output = %q", out)
	}
	if ls[0] != "Organization created." || !regexp.MustCompile(`^id: [0-9a-f-]{36}$`).MatchString(ls[1]) || ls[2] != "name: Gym Group" ||
		ls[3] != "max instances: 10" || ls[4] != "WhatsApp Web: allowed" ||
		ls[5] != "Owner invitation for ana@example.com, valid until 2026-11-26T10:00:00Z." ||
		ls[6] != "The invitation mail was not sent. Give this link to the owner:" ||
		!regexp.MustCompile(`^https://calyx\.example/invite\?token=[A-Za-z0-9_-]{43}$`).MatchString(ls[7]) ||
		ls[8] != "Without a frontend, run:" || ls[9] != curlLine("/api/v1/invitations/accept", ls[7]) {
		t.Fatalf("output = %q", out)
	}
	if o.count(`SELECT count(*) FROM organizations`) != 1 || o.count(`SELECT count(*) FROM clients WHERE is_default`) != 1 || o.count(`SELECT count(*) FROM invitations`) != 1 {
		t.Fatal("the database does not hold one organization, one default client and one invitation")
	}
}

func TestOrgCreateWithMail(t *testing.T) {
	o := newOpEnv(t)
	o.mailer.FailWith(nil)
	code, out, errOut := o.run("org", "create", "--name", "Gym Group", "--owner-email", "ana@example.com")
	if code != 0 || errOut != "" {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	if !strings.Contains(out, "The invitation mail was sent.") || strings.Contains(out, "token=") {
		t.Fatalf("output = %q", out)
	}
	sent := o.mailer.Sent()
	if len(sent) != 1 || sent[0].To != "ana@example.com" {
		t.Fatalf("sent = %+v", sent)
	}
}

func TestOrgCreateOptions(t *testing.T) {
	o := newOpEnv(t)
	code, out, _ := o.run("org", "create", "--name", "G", "--owner-email", "ana@example.com", "--max-instances", "3", "--whatsapp-web", "blocked")
	if code != 0 || !strings.Contains(out, "max instances: 3\n") || !strings.Contains(out, "WhatsApp Web: blocked\n") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if o.count(`SELECT count(*) FROM organizations WHERE max_instances = 3 AND NOT whatsapp_web_allowed`) != 1 {
		t.Fatal("the row does not match")
	}
}

func TestOrgCreateUsesTheDefault(t *testing.T) {
	o := newOpEnv(t, func(c *config.Config) { c.WhatsAppWebDefault = false })
	_, out, _ := o.run("org", "create", "--name", "G", "--owner-email", "ana@example.com")
	if !strings.Contains(out, "WhatsApp Web: blocked\n") {
		t.Fatalf("out = %q", out)
	}
}

func TestOrgCreateUsageErrors(t *testing.T) {
	o := newOpEnv(t)
	cases := [][]string{
		{"org", "create", "--owner-email", "ana@example.com"},
		{"org", "create", "--name", "  ", "--owner-email", "ana@example.com"},
		{"org", "create", "--name", "G"},
		{"org", "create", "--name", "G", "--owner-email", "x"},
		{"org", "create", "--name", "G", "--owner-email", "ana@example.com", "--max-instances", "10001"},
		{"org", "create", "--name", "G", "--owner-email", "ana@example.com", "--whatsapp-web", "maybe"},
		{"org", "create", "--name", "G", "--owner-email", "ana@example.com", "--nope"},
		{"org", "create", "--name", "G", "--owner-email", "ana@example.com", "extra"},
		{"org", "create", "--name", "a\nb", "--owner-email", "ana@example.com"},
		{"org", "create", "--name", strings.Repeat("n", 101), "--owner-email", "ana@example.com"},
	}
	for _, args := range cases {
		code, out, errOut := o.run(args...)
		if code != 2 || out != "" || !strings.HasPrefix(errOut, "Error: ") || !strings.Contains(errOut, "Usage: backend org create") {
			t.Fatalf("%v: code=%d out=%q err=%q", args, code, out, errOut)
		}
	}
	if o.count(`SELECT count(*) FROM organizations`) != 0 {
		t.Fatal("an organization was created")
	}
}

// The command prints the field error of the service, the same text as the HTTP API.
func TestOrgCreatePrintsTheFieldErrorOfTheService(t *testing.T) {
	o := newOpEnv(t)
	long := strings.Repeat("n", tenancy.MaxNameLength+1)
	_, _, err := o.deps.Tenancy.CreateOrganization(context.Background(), tenancy.NewOrganization{Name: long, MaxInstances: 1})
	ae, ok := apierr.As(err)
	if !ok || len(ae.Errors) != 1 || ae.Errors[0].Field != "name" {
		t.Fatalf("service error %v", err)
	}
	code, out, errOut := o.run("org", "create", "--name", long, "--owner-email", "ana@example.com")
	want := "Error: name: " + ae.Errors[0].Message + "\nUsage: backend org create"
	if code != 2 || out != "" || !strings.HasPrefix(errOut, want) {
		t.Fatalf("code=%d out=%q err=%q, want prefix %q", code, out, errOut, want)
	}
	if strings.Contains(errOut, long) {
		t.Fatal("the output quotes the name")
	}
	if o.count(`SELECT count(*) FROM organizations`) != 0 {
		t.Fatal("an organization was created")
	}
}

// The service accepts 0 to tenancy.MaxInstancesLimit: an organization with 0 creates no instance.
func TestOrgCreateAcceptsTheLimitsOfMaxInstances(t *testing.T) {
	o := newOpEnv(t)
	for _, n := range []string{"0", "10000"} {
		code, out, errOut := o.run("org", "create", "--name", "G"+n, "--owner-email", "ana"+n+"@example.com", "--max-instances", n)
		if code != 0 || !strings.Contains(out, "max instances: "+n+"\n") {
			t.Fatalf("%s: code=%d out=%q err=%q", n, code, out, errOut)
		}
	}
}

// An invitation never checks whether the email has an account elsewhere: acceptance does.
func TestOrgCreateOwnerEmailWithAccount(t *testing.T) {
	o := newOpEnv(t)
	seed := testutil.SeedTenant(t, o.pool, testutil.TenantOptions{Name: "Existing", WhatsAppWebAllowed: true})
	code, out, errOut := o.run("org", "create", "--name", "G", "--owner-email", seed.OwnerEmail)
	if code != 0 || errOut != "" {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	if !strings.HasPrefix(out, "Organization created.\nid: ") || !strings.Contains(out, "Owner invitation") {
		t.Fatalf("out = %q", out)
	}
}

func orgID(t *testing.T, out string) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^id: ([0-9a-f-]{36})$`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no id in %q", out)
	}
	return m[1]
}

func TestOrgSet(t *testing.T) {
	o := newOpEnv(t)
	_, out, _ := o.run("org", "create", "--name", "Gym Group", "--owner-email", "ana@example.com")
	id := orgID(t, out)
	code, out, errOut := o.run("org", "set", "--id", id, "--max-instances", "20")
	if code != 0 || errOut != "" {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	want := "Organization updated.\nid: " + id + "\nname: Gym Group\nmax instances: 20\nWhatsApp Web: allowed\n"
	if out != want {
		t.Fatalf("out = %q\nwant %q", out, want)
	}
	code, out, _ = o.run("org", "set", "--id", id, "--name", " New Name ", "--whatsapp-web", "blocked")
	if code != 0 || !strings.Contains(out, "name: New Name\n") || !strings.Contains(out, "max instances: 20\n") || !strings.Contains(out, "WhatsApp Web: blocked\n") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestOrgSetNothing(t *testing.T) {
	o := newOpEnv(t)
	code, _, errOut := o.run("org", "set", "--id", uuid.NewString())
	if code != 2 || !strings.HasPrefix(errOut, "Error: ") || !strings.Contains(errOut, "Usage: backend org set") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

func TestOrgSetUnknownID(t *testing.T) {
	o := newOpEnv(t)
	code, out, errOut := o.run("org", "set", "--id", uuid.NewString(), "--max-instances", "5")
	if code != 1 || out != "" || errOut != "Error: The resource does not exist.\n" {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestOrgSetBadID(t *testing.T) {
	o := newOpEnv(t)
	code, _, errOut := o.run("org", "set", "--id", "abc", "--max-instances", "5")
	if code != 2 || !strings.HasPrefix(errOut, "Error: ") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	code, _, errOut = o.run("org", "set", "--id", uuid.NewString(), "--max-instances", "10001")
	if code != 2 || !strings.HasPrefix(errOut, "Error: max_instances: ") {
		t.Fatalf("max-instances 10001: code=%d err=%q", code, errOut)
	}
}

func TestOrgSetOwner(t *testing.T) {
	o := newOpEnv(t)
	seed := testutil.SeedTenant(t, o.pool, testutil.TenantOptions{Name: "Existing", WhatsAppWebAllowed: true})
	code, out, errOut := o.run("org", "set-owner", "--id", seed.OrganizationID.String(), "--email", "new@example.com")
	if code != 0 || errOut != "" {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	ls := lines(out)
	if len(ls) != 6 || ls[0] != "Owner invitation for new@example.com, valid until 2026-11-26T10:00:00Z." ||
		ls[1] != "The invitation mail was not sent. Give this link to the owner:" ||
		!strings.HasPrefix(ls[2], "https://calyx.example/invite?token=") ||
		ls[3] != "Without a frontend, run:" || ls[4] != curlLine("/api/v1/invitations/accept", ls[2]) ||
		ls[5] != "When the invitation is accepted, "+seed.OwnerEmail+" is disabled." {
		t.Fatalf("out = %q", out)
	}
	var replaces uuid.UUID
	if err := o.pool.QueryRow(context.Background(), `SELECT replaces_owner_id FROM invitations WHERE email = 'new@example.com'`).Scan(&replaces); err != nil {
		t.Fatal(err)
	}
	if replaces != seed.OwnerID {
		t.Fatalf("replaces_owner_id = %s, want %s", replaces, seed.OwnerID)
	}
}

func TestOrgSetOwnerWithoutAnOwner(t *testing.T) {
	o := newOpEnv(t)
	org, _, err := o.deps.Tenancy.CreateOrganization(context.Background(), tenancy.NewOrganization{Name: "Empty", MaxInstances: 3})
	if err != nil {
		t.Fatal(err)
	}
	code, out, _ := o.run("org", "set-owner", "--id", org.ID.String(), "--email", "new@example.com")
	if code != 0 || strings.Contains(out, "is disabled") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestOrgList(t *testing.T) {
	o := newOpEnv(t)
	seed := testutil.SeedTenant(t, o.pool, testutil.TenantOptions{Name: "First", MaxInstances: 7, WhatsAppWebAllowed: true})
	testutil.SeedInstance(t, o.pool, testutil.InstanceSeed{OrganizationID: seed.OrganizationID, ClientID: seed.DefaultClientID})
	testutil.SeedInstance(t, o.pool, testutil.InstanceSeed{OrganizationID: seed.OrganizationID, ClientID: seed.ClientA1})
	time.Sleep(5 * time.Millisecond)
	if code, _, e := o.run("org", "create", "--name", "Second", "--owner-email", "second@example.com", "--whatsapp-web", "blocked"); code != 0 {
		t.Fatal(e)
	}
	code, out, errOut := o.run("org", "list")
	if code != 0 || errOut != "" {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	ls := lines(out)
	if len(ls) != 3 {
		t.Fatalf("out = %q", out)
	}
	if strings.Join(strings.Fields(ls[0]), " ") != "ID NAME INSTANCES MAX WHATSAPP WEB OWNER" {
		t.Fatalf("header = %q", ls[0])
	}
	if f := strings.Fields(ls[1]); f[0] != seed.OrganizationID.String() || f[1] != "First" || f[2] != "2" || f[3] != "7" || f[4] != "allowed" || f[len(f)-1] != seed.OwnerEmail {
		t.Fatalf("row 1 = %q", ls[1])
	}
	if f := strings.Fields(ls[2]); f[1] != "Second" || f[2] != "0" || f[3] != "10" || f[4] != "blocked" || f[len(f)-1] != "-" {
		t.Fatalf("row 2 = %q", ls[2])
	}
}

func TestOrgListEmpty(t *testing.T) {
	o := newOpEnv(t)
	code, out, _ := o.run("org", "list")
	if code != 0 || out != "No organizations.\n" {
		t.Fatalf("code=%d out=%q", code, out)
	}
	code, _, errOut := o.run("org", "list", "--x")
	if code != 2 || !strings.HasPrefix(errOut, "Error: ") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

func TestUserResetPassword(t *testing.T) {
	o := newOpEnv(t)
	seed := testutil.SeedTenant(t, o.pool, testutil.TenantOptions{Name: "Existing", WhatsAppWebAllowed: true})
	code, out, errOut := o.run("user", "reset-password", "--email", seed.OwnerEmail)
	if code != 0 || errOut != "" {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	ls := lines(out)
	if len(ls) != 4 || ls[0] != "Reset link for "+seed.OwnerEmail+" (valid for 1 hour):" ||
		!regexp.MustCompile(`^https://calyx\.example/reset-password\?token=[A-Za-z0-9_-]{43}$`).MatchString(ls[1]) ||
		ls[2] != "Without a frontend, run:" || ls[3] != curlLine("/api/v1/auth/password-reset/confirm", ls[1]) {
		t.Fatalf("out = %q", out)
	}
	code, out, errOut = o.run("user", "reset-password", "--email", "nobody@example.com")
	if code != 1 || out != "" || !strings.HasPrefix(errOut, "Error: ") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
	code, _, _ = o.run("user", "reset-password", "--email", "x")
	if code != 2 {
		t.Fatalf("bad email: code=%d", code)
	}
}

func TestUserDisableAndEnable(t *testing.T) {
	o := newOpEnv(t)
	ctx := context.Background()
	org, _, err := o.deps.Tenancy.CreateOrganization(ctx, tenancy.NewOrganization{Name: "Org", MaxInstances: 3})
	if err != nil {
		t.Fatal(err)
	}
	uid := o.userWithPassword(org.ID, "ana@example.com")
	p := tenancy.Principal{UserID: uid, OrganizationID: org.ID, Role: tenancy.RoleOwner}
	key, err := o.deps.Auth.CreateAPIKey(ctx, p, "k")
	if err != nil {
		t.Fatal(err)
	}
	code, out, errOut := o.run("user", "disable", "--email", "ana@example.com")
	if code != 0 || errOut != "" || out != "User ana@example.com is disabled. Their sessions were deleted and their API keys revoked.\n" {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
	if _, err := o.deps.Auth.Authenticate(ctx, "", key.Token); err == nil {
		t.Fatal("the key of a disabled user still works")
	}
	code, out, _ = o.run("user", "enable", "--email", "ana@example.com")
	if code != 0 || out != "User ana@example.com is enabled.\n" {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if _, _, err := o.deps.Auth.Login(ctx, "ana@example.com", password, ""); err != nil {
		t.Fatalf("login after enable: %v", err)
	}
}

func TestUserDisableUnknown(t *testing.T) {
	o := newOpEnv(t)
	for _, c := range []string{"disable", "enable"} {
		code, out, errOut := o.run("user", c, "--email", "nobody@example.com")
		if code != 1 || out != "" || !strings.HasPrefix(errOut, "Error: ") {
			t.Fatalf("%s: code=%d out=%q err=%q", c, code, out, errOut)
		}
	}
}

func TestInstancesOrphans(t *testing.T) {
	o := newOpEnv(t)
	seed := testutil.SeedTenant(t, o.pool, testutil.TenantOptions{Name: "Existing", WhatsAppWebAllowed: true})
	row := testutil.SeedInstance(t, o.pool, testutil.InstanceSeed{OrganizationID: seed.OrganizationID, ClientID: seed.DefaultClientID})
	o.fake.AddInstance(row.CoreName, corev1.Provider_PROVIDER_WHATSAPP_WEB, corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED)
	o.fake.AddInstance("x_b", corev1.Provider_PROVIDER_META_CLOUD, corev1.InstanceStatus_INSTANCE_STATUS_DISCONNECTED)
	code, out, errOut := o.run("instances", "orphans")
	if code != 0 || errOut != "" {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	ls := lines(out)
	if len(ls) != 2 || strings.Join(strings.Fields(ls[0]), " ") != "CORE NAME PROVIDER STATUS" || strings.Contains(out, row.CoreName) {
		t.Fatalf("out = %q", out)
	}
	if f := strings.Fields(ls[1]); len(f) != 3 || f[0] != "x_b" || f[1] != "META_CLOUD" || f[2] != "DISCONNECTED" {
		t.Fatalf("row = %q", ls[1])
	}
}

func TestNoOrphans(t *testing.T) {
	o := newOpEnv(t)
	code, out, _ := o.run("instances", "orphans")
	if code != 0 || out != "No orphan instances.\n" {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestOrphansWithTheCoreDown(t *testing.T) {
	o := newOpEnv(t)
	o.fake.SetUnavailable(true)
	code, out, errOut := o.run("instances", "orphans")
	if code != 1 || out != "" || !strings.HasPrefix(errOut, "Error: ") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestOrphansIgnoreRowsOfOtherCores(t *testing.T) {
	o := newOpEnv(t)
	seed := testutil.SeedTenant(t, o.pool, testutil.TenantOptions{Name: "Existing", WhatsAppWebAllowed: true})
	row := testutil.SeedInstance(t, o.pool, testutil.InstanceSeed{OrganizationID: seed.OrganizationID, ClientID: seed.DefaultClientID, CoreID: "other"})
	o.fake.AddInstance(row.CoreName, corev1.Provider_PROVIDER_WHATSAPP_WEB, corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED)
	_, out, _ := o.run("instances", "orphans")
	if !strings.Contains(out, row.CoreName) {
		t.Fatalf("a row of another core hides an orphan: %q", out)
	}
}

func TestNotMigrated(t *testing.T) {
	o := newOpEnvOn(t, testutil.NewEmptyDB(t))
	code, out, errOut := o.run("org", "list")
	if code != 1 || out != "" || !strings.HasPrefix(errOut, "Error: ") || !strings.Contains(errOut, "The database is not migrated. Run: backend migrate") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestMailTestSendsOneMail(t *testing.T) {
	o := newOpEnv(t)
	o.mailer.FailWith(nil)
	code, out, errOut := o.run("mail", "test", "ana@test.example")
	if code != 0 || out != "sent\n" || errOut != "" {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
	sent := o.mailer.Sent()
	if len(sent) != 1 || sent[0].To != "ana@test.example" || sent[0].Subject == "" || sent[0].Body == "" {
		t.Fatalf("sent = %+v", sent)
	}
}

func TestMailTestPrintsTheSMTPError(t *testing.T) {
	o := newOpEnv(t)
	o.mailer.FailWith(errors.New("send mail: 535 authentication failed"))
	code, out, errOut := o.run("mail", "test", "ana@test.example")
	if code != 1 || out != "" || errOut != "Error: send mail: 535 authentication failed\n" {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestMailTestWithoutSMTP(t *testing.T) {
	o := newOpEnv(t) // the fake mailer returns ErrNotConfigured
	code, _, errOut := o.run("mail", "test", "ana@test.example")
	if code != 1 || !strings.HasPrefix(errOut, "Error: mail is not configured.") || !strings.Contains(errOut, "SMTP_HOST") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

func TestMailTestUsage(t *testing.T) {
	o := newOpEnv(t)
	o.mailer.FailWith(nil)
	for _, args := range [][]string{{"mail", "test"}, {"mail", "test", "a@test.example", "b@test.example"}, {"mail", "test", "not-an-address"}} {
		code, out, errOut := o.run(args...)
		if code != 2 || out != "" || !strings.Contains(errOut, "backend mail test ADDRESS") {
			t.Errorf("%v: code=%d out=%q err=%q", args, code, out, errOut)
		}
	}
	if o.mailer.Attempts() != 0 {
		t.Error("a mail was sent")
	}
}

// curlLine is the command that the output gives after a link, with the token of the link.
func curlLine(path, link string) string {
	_, token, _ := strings.Cut(link, "?token=")
	return `curl -X POST https://calyx.example` + path + ` -H 'Content-Type: application/json' -d '{"token":"` + token + `","password":"<new password>"}'`
}
