package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"

	"calyx/backend/internal/config"
	"calyx/backend/internal/core"
	"calyx/backend/internal/database"
	"calyx/backend/internal/operator"
	"calyx/backend/internal/testutil"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// closedPort returns an address where nothing listens, so the core is down.
func closedPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func serveEnv(t *testing.T, databaseURL string, kv ...string) func(string) string {
	t.Helper()
	core := closedPort(t)
	m := map[string]string{
		"DATABASE_URL": databaseURL,
		"PUBLIC_URL":   "https://api.calyx.test",
		"CORE_ADDRESS": core,
		"CORE_TOKEN":   "0123456789abcdef0123456789abcdef",
		"SECRET_KEY":   "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		"LOG_FORMAT":   "json",
	}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

type serving struct {
	done     chan int
	signals  chan os.Signal
	logs     *syncBuf
	addr     chan string
	listened chan struct{}
	pool     *pgxpool.Pool
}

// startServe runs serve with a listener on a free port and a signal channel that the test drives.
// The core address of the configuration is a closed port.
func startServe(t *testing.T, databaseURL string, kv ...string) *serving {
	t.Helper()
	return startServeWithCore(t, databaseURL, nil, kv...)
}

// startServeWithCore is startServe with a connection to a fake core. A nil coreConn dials the
// address of the configuration.
func startServeWithCore(t *testing.T, databaseURL string, coreConn *grpc.ClientConn, kv ...string) *serving {
	t.Helper()
	cfg, err := config.Load(serveEnv(t, databaseURL, kv...))
	if err != nil {
		t.Fatal(err)
	}
	logs := &syncBuf{}
	log := config.NewLogger("json", logs)
	pool, err := database.Open(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	s := &serving{done: make(chan int, 1), signals: make(chan os.Signal, 2), logs: logs, addr: make(chan string, 1), listened: make(chan struct{}), pool: pool}
	listen := func() (net.Listener, error) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err == nil {
			s.addr <- l.Addr().String()
		}
		close(s.listened)
		return l, err
	}
	dial := dialCore(cfg)
	if coreConn != nil {
		dial = func() (*core.Client, io.Closer, error) { return core.NewClient(coreConn), coreConn, nil }
	}
	go func() { s.done <- serve(context.Background(), cfg, log, pool, listen, dial, s.signals) }()
	t.Cleanup(func() {
		// Stop a server that a failed test left running.
		select {
		case <-s.done:
		default:
			s.signals <- os.Interrupt
			select {
			case <-s.done:
			case <-time.After(10 * time.Second):
			}
		}
	})
	return s
}

func (s *serving) waitCode(t *testing.T, d time.Duration) int {
	t.Helper()
	select {
	case code := <-s.done:
		s.done <- code // later cleanups may read it again
		return code
	case <-time.After(d):
		t.Fatalf("serve did not return within %s. Log:\n%s", d, s.logs.String())
		return -1
	}
}

func (s *serving) waitAddr(t *testing.T) string {
	t.Helper()
	select {
	case a := <-s.addr:
		return a
	case <-time.After(10 * time.Second):
		t.Fatalf("serve did not listen within 10 s. Log:\n%s", s.logs.String())
		return ""
	}
}

func (s *serving) waitLog(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(s.logs.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("the log has no %q:\n%s", want, s.logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func getHealthz(t *testing.T, addr string) (int, map[string]string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			defer resp.Body.Close()
			var m map[string]string
			_ = json.NewDecoder(resp.Body).Decode(&m)
			return resp.StatusCode, m
		}
		if time.Now().After(deadline) {
			t.Fatalf("healthz: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitCore asks /healthz until the core health is want.
func waitCore(t *testing.T, s *serving, addr, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, body := getHealthz(t, addr)
		if body["core"] == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the core health is %q, want %q. Log:\n%s", body["core"], want, s.logs.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestHelpIsUsage(t *testing.T) {
	code, stdout, _ := runCmd([]string{"help"}, envOf(""))
	if code != 0 || stdout != operator.Usage {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
}

func TestBadConfiguration(t *testing.T) {
	code, _, stderr := runCmd([]string{"migrate"}, func(string) string { return "" })
	if code != 1 || !strings.Contains(stderr, "DATABASE_URL") || !strings.Contains(stderr, "PUBLIC_URL") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	code, _, stderr = runCmd([]string{"serve"}, func(string) string { return "" })
	if code != 1 || !strings.Contains(stderr, "DATABASE_URL") {
		t.Fatalf("serve: code=%d stderr=%q", code, stderr)
	}
}

func TestOperatorCommandsRunThroughRun(t *testing.T) {
	url := testutil.NewDatabaseURL(t)
	if code, _, stderr := runCmd([]string{"migrate"}, serveEnv(t, url)); code != 0 {
		t.Fatalf("migrate: code=%d stderr=%q", code, stderr)
	}
	code, stdout, stderr := runCmd([]string{"org", "list"}, serveEnv(t, url))
	if code != 0 || stdout != "No organizations.\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, _, stderr = runCmd([]string{"org", "frob"}, serveEnv(t, url))
	if code != 2 || !strings.Contains(stderr, `unknown command "org frob"`) {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	code, _, stderr = runCmd([]string{"instances", "orphans"}, serveEnv(t, url))
	if code != 1 || !strings.HasPrefix(stderr, "Error: ") { // the core is down
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestServeRefusesASecondProcess(t *testing.T) {
	url := testutil.NewDatabaseURL(t)
	pool, err := database.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	release, err := database.Lock(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	s := startServe(t, url)
	if code := s.waitCode(t, 10*time.Second); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	select {
	case <-s.listened:
		t.Fatal("serve listened although another process holds the lock")
	default:
	}
}

func TestServeRefusesAForeignCore(t *testing.T) {
	url := testutil.NewDatabaseURL(t)
	if code, _, stderr := runCmd([]string{"migrate"}, serveEnv(t, url)); code != 0 {
		t.Fatalf("migrate: code=%d stderr=%q", code, stderr)
	}
	pool, err := database.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	seed := testutil.SeedTenant(t, pool, testutil.TenantOptions{Name: "Existing", WhatsAppWebAllowed: true})
	testutil.SeedInstance(t, pool, testutil.InstanceSeed{OrganizationID: seed.OrganizationID, ClientID: seed.DefaultClientID, CoreID: "other"})
	pool.Close()
	s := startServe(t, url)
	if code := s.waitCode(t, 10*time.Second); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	log := s.logs.String()
	if !strings.Contains(log, "another core") || !strings.Contains(log, "other") {
		t.Fatalf("log = %s", log)
	}
	select {
	case <-s.listened:
		t.Fatal("serve listened")
	default:
	}
}

func TestServeStartsAndStops(t *testing.T) {
	url := testutil.NewDatabaseURL(t)
	s := startServe(t, url)
	addr := s.waitAddr(t)
	status, body := getHealthz(t, addr)
	if status != 200 || body["database"] != "ok" {
		t.Fatalf("healthz = %d %v", status, body)
	}
	// The core port is closed, so the core link fails at once.
	waitCore(t, s, addr, "unreachable")
	s.waitLog(t, "Calyx backend is ready.")
	s.waitLog(t, "Mail is not configured")
	s.signals <- os.Interrupt
	if code := s.waitCode(t, 10*time.Second); code != 0 {
		t.Fatalf("code = %d, want 0. Log:\n%s", code, s.logs.String())
	}
	if !strings.Contains(s.logs.String(), "Shutdown complete.") {
		t.Fatalf("log = %s", s.logs.String())
	}
	pool, err := database.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	release, err := database.Lock(context.Background(), pool)
	if err != nil {
		t.Fatalf("the lock was not released: %v", err)
	}
	release()
}

func TestServeWithTheCoreDownStillStarts(t *testing.T) {
	url := testutil.NewDatabaseURL(t)
	s := startServe(t, url)
	addr := s.waitAddr(t)
	status, _ := getHealthz(t, addr)
	if status != 200 {
		t.Fatalf("healthz = %d", status)
	}
	s.waitLog(t, "The core cannot be reached.")
	s.signals <- os.Interrupt
	if code := s.waitCode(t, 10*time.Second); code != 0 {
		t.Fatalf("code = %d", code)
	}
}

func TestServeWithACoreThatAnswersGivesOK(t *testing.T) {
	_, conn := testutil.NewFakeCore(t)
	s := startServeWithCore(t, testutil.NewDatabaseURL(t), conn)
	addr := s.waitAddr(t)
	waitCore(t, s, addr, "ok")
	s.signals <- os.Interrupt
	if code := s.waitCode(t, 10*time.Second); code != 0 {
		t.Fatalf("code = %d, want 0. Log:\n%s", code, s.logs.String())
	}
	// The listener stops before the core connection closes, so the link never looks down.
	if strings.Contains(s.logs.String(), "The core link is down.") {
		t.Fatalf("the listener ran after the core connection closed. Log:\n%s", s.logs.String())
	}
}

func TestServeStartsWithTheStoredUnreachableCore(t *testing.T) {
	url := testutil.NewDatabaseURL(t)
	if code, _, stderr := runCmd([]string{"migrate"}, serveEnv(t, url)); code != 0 {
		t.Fatalf("migrate: code=%d stderr=%q", code, stderr)
	}
	pool, err := database.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(context.Background(), `INSERT INTO core_status (core_id, failed_checks, unreachable_since, created_at, updated_at)
		VALUES ('main', 2, now(), now(), now())`)
	pool.Close()
	if err != nil {
		t.Fatal(err)
	}
	fake, conn := testutil.NewFakeCore(t)
	// A slow ListInstances keeps the rounds from ending while the core link is already up. Without
	// the stored fact, the core would be unknown in this time.
	fake.SetDelay(testutil.MethodListInstances, 2*time.Second)
	s := startServeWithCore(t, url, conn)
	addr := s.waitAddr(t)
	s.waitLog(t, "The core link is up.")
	if _, body := getHealthz(t, addr); body["core"] != "unreachable" {
		t.Fatalf("core = %q, want unreachable. Log:\n%s", body["core"], s.logs.String())
	}
	// A round that succeeds clears the stored fact.
	waitCore(t, s, addr, "ok")
}

func TestSecondSignalReturnsAtOnce(t *testing.T) {
	url := testutil.NewDatabaseURL(t)
	s := startServe(t, url)
	addr := s.waitAddr(t)
	getHealthz(t, addr)
	// A request that waits for its body keeps the shutdown waiting.
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	w := bufio.NewWriter(conn)
	_, _ = io.WriteString(w, "POST /api/v1/auth/login HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\nContent-Length: 1000\r\n\r\n{\"email\":")
	_ = w.Flush()
	time.Sleep(200 * time.Millisecond) // the handler has started and waits for the body
	s.signals <- os.Interrupt
	time.Sleep(100 * time.Millisecond)
	s.signals <- os.Interrupt
	started := time.Now()
	if code := s.waitCode(t, time.Second); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if d := time.Since(started); d > time.Second {
		t.Fatalf("serve took %s", d)
	}
	if !strings.Contains(s.logs.String(), "Second signal") {
		t.Fatalf("log = %s", s.logs.String())
	}
}

func TestServeStopsWhenTheLockIsLost(t *testing.T) {
	prev := lockCheckInterval.Swap(int64(50 * time.Millisecond))
	t.Cleanup(func() { lockCheckInterval.Store(prev) })
	s := startServe(t, testutil.NewDatabaseURL(t))
	s.waitAddr(t)
	// End the session that holds the advisory lock, like a PostgreSQL restart does.
	_, err := s.pool.Exec(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_locks
		WHERE locktype = 'advisory' AND objid = $1 AND granted
		AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`, int64(database.LockKey))
	if err != nil {
		t.Fatal(err)
	}
	if code := s.waitCode(t, 10*time.Second); code != 1 {
		t.Fatalf("exit code = %d, want 1. Log:\n%s", code, s.logs.String())
	}
	s.waitLog(t, "The advisory lock is lost.")
}

func smtpEnv(f *testutil.FakeSMTP) []string {
	return []string{
		"SMTP_HOST", f.Host(), "SMTP_PORT", strconv.Itoa(f.Port()),
		"SMTP_FROM", "calyx@calyx.test", "OPERATOR_EMAIL", "ops@calyx.test",
	}
}

func TestServeLogsAnErrorWhenTheMailCheckFails(t *testing.T) {
	// The fake offers STARTTLS with a certificate that the system roots do not trust.
	f := testutil.NewFakeSMTP(t, testutil.FakeSMTPOptions{StartTLS: true})
	s := startServe(t, testutil.NewDatabaseURL(t), smtpEnv(f)...)
	s.waitLog(t, "alert mails will fail")
	if !strings.Contains(s.logs.String(), "mail test") || !strings.Contains(s.logs.String(), `"level":"ERROR"`) {
		t.Errorf("the log does not name the command: %s", s.logs.String())
	}
	// The API keeps running.
	if code, _ := getHealthz(t, s.waitAddr(t)); code != 200 {
		t.Errorf("healthz = %d", code)
	}
}

func TestServeMailCheckPassesAndSendsNothing(t *testing.T) {
	f := testutil.NewFakeSMTP(t, testutil.FakeSMTPOptions{})
	s := startServe(t, testutil.NewDatabaseURL(t), smtpEnv(f)...)
	s.waitLog(t, "SMTP check passed")
	if strings.Contains(s.logs.String(), "alert mails will fail") || len(f.Messages()) != 0 || f.MailAttempts() != 0 {
		t.Errorf("log: %s", s.logs.String())
	}
}
