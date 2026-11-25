//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"calyx/backend/internal/testutil"
)

const (
	coreToken = "e2e-core-token-0123456789abcdef00"
	// secretKey is the base64 of 32 ASCII bytes. It is for this test only.
	secretKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type process struct {
	name string
	cmd  *exec.Cmd
	out  *lockedBuffer // stdout and stderr together
	done chan struct{} // closed when the process has exited
	err  error         // the Wait result, valid after done
}

type harness struct {
	t           *testing.T
	dir         string
	root        string
	coreBin     string
	backendBin  string
	graph       *fakeGraph
	smtp        *testutil.FakeSMTP
	coreAddr    string
	backendURL  string
	databaseURL string
	core        *process
	backend     *process
	stopped     []*process // cores that stopped before the current one, for the logs
	grpcPort    int
	httpPort    int
}

// newHarness builds both binaries and starts the core and the backend. The backend does a round
// each second. The cleanup stops both processes.
func newHarness(t *testing.T) *harness {
	return newHarnessWithRounds(t, "1s")
}

// newHarnessWithRounds is newHarness with a round each roundEvery (a Go duration).
func newHarnessWithRounds(t *testing.T, roundEvery string) *harness {
	t.Helper()
	h := &harness{t: t, dir: t.TempDir(), root: repoRoot(t)}
	h.coreBin = filepath.Join(h.dir, "core")
	h.backendBin = filepath.Join(h.dir, "backend")
	h.graph = newFakeGraph(t)
	h.smtp = testutil.NewFakeSMTP(t, testutil.FakeSMTPOptions{})
	h.databaseURL = testutil.NewDatabaseURL(t)

	// The Graph API address of the core and the timings of the backend are not environment
	// variables. The test sets them at build time, so the alert comes in seconds, not minutes.
	const cfgPkg = "calyx/backend/internal/config"
	buildBinary(t, h.root, "./apps/core/cmd/core", h.coreBin, "-X main.graphURL="+h.graph.URL)
	buildBinary(t, h.root, "./apps/backend/cmd/backend", h.backendBin,
		"-X "+cfgPkg+".stateCheckInterval="+roundEvery+" -X "+cfgPkg+".alertGrace=2s -X "+cfgPkg+".pairingTimeout=30s")

	h.grpcPort = freePort(t)
	h.httpPort = freePort(t)
	h.coreAddr = net.JoinHostPort("127.0.0.1", strconv.Itoa(h.grpcPort))
	h.backendURL = "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(h.httpPort))

	// Cleanups run last in, first out: the backend stops first, then the core. The fakes and the
	// database drop that testutil registered run after both.
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(h.logs())
		}
	})
	// h.core is the last core that started. The cleanup comes first, so a core that never gets
	// ready still stops.
	t.Cleanup(func() { h.stop(h.core, 15*time.Second) })
	h.startCore()

	h.backend = h.start("backend", h.backendBin, h.backendEnv(), "serve")
	t.Cleanup(func() { h.stop(h.backend, 15*time.Second) })
	waitFor(t, "the backend is ready (an \"address already in use\" in the logs is a flaky port, not a defect: run again)",
		30*time.Second, func() bool { return h.health()["database"] == "ok" }, h.logs)
	// "ok" comes only after the core link is up and a round after it succeeded.
	waitFor(t, "the core is ok in /healthz", 30*time.Second, func() bool { return h.health()["core"] == "ok" }, h.logs)
	return h
}

// startCore starts the core with the same data directory and waits until it is ready.
func (h *harness) startCore() {
	h.t.Helper()
	if h.core != nil {
		h.stopped = append(h.stopped, h.core)
	}
	h.core = h.start("core", h.coreBin, h.coreEnv(h.grpcPort))
	waitFor(h.t, "the core is ready", 30*time.Second, func() bool {
		c, err := net.DialTimeout("tcp", h.coreAddr, time.Second)
		if err != nil {
			return false
		}
		_ = c.Close()
		return strings.Contains(h.core.out.String(), "Calyx Core is fully operational.")
	}, h.logs)
}

// health returns the body of GET /healthz, or nil when the request fails.
func (h *harness) health() map[string]any {
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(h.backendURL + "/healthz")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var out map[string]any
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return nil
	}
	return out
}

// repoRoot is the directory of go.work.
func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOWORK").Output()
	if err != nil {
		t.Fatalf("go env GOWORK: %v", err)
	}
	work := strings.TrimSpace(string(out))
	if work == "" || work == "off" {
		t.Fatal("go env GOWORK is empty or off: run the test inside the workspace of the repository.")
	}
	return filepath.Dir(work)
}

func buildBinary(t *testing.T, root, pkg, out, ldflags string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-ldflags", ldflags, "-o", out, pkg)
	cmd.Dir = root
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %s: %v\n%s", pkg, err, b)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// baseEnv is the only part of the environment of the shell that the processes get.
func baseEnv() []string {
	var env []string
	for _, k := range []string{"PATH", "HOME", "TMPDIR"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

func (h *harness) coreEnv(grpcPort int) []string {
	return append(baseEnv(),
		"GRPC_HOST=127.0.0.1",
		"GRPC_PORT="+strconv.Itoa(grpcPort),
		"GRPC_AUTH_TOKEN="+coreToken,
		"DATABASE_URL=file:"+filepath.Join(h.dir, "core.sqlite"),
		"META_GRAPH_API_VERSION=v23.0",
	)
}

func (h *harness) backendEnv() []string {
	return append(baseEnv(),
		"DATABASE_URL="+h.databaseURL,
		"HTTP_HOST=127.0.0.1",
		"HTTP_PORT="+strconv.Itoa(h.httpPort),
		"PUBLIC_URL="+h.backendURL,
		"CORE_ADDRESS="+h.coreAddr,
		"CORE_TOKEN="+coreToken,
		"SECRET_KEY="+secretKey,
		"SMTP_HOST="+h.smtp.Host(),
		"SMTP_PORT="+strconv.Itoa(h.smtp.Port()),
		"SMTP_FROM=calyx@e2e.test",
		"OPERATOR_EMAIL=operator@e2e.test",
		"WHATSAPP_WEB_DEFAULT=allowed",
		"TRUST_PROXY=false",
		"LOG_FORMAT=json",
	)
}

func (h *harness) start(name, bin string, env []string, args ...string) *process {
	h.t.Helper()
	p := &process{name: name, out: &lockedBuffer{}, done: make(chan struct{})}
	p.cmd = exec.Command(bin, args...)
	p.cmd.Dir = h.dir
	p.cmd.Env = env
	p.cmd.Stdout = p.out
	p.cmd.Stderr = p.out
	if err := p.cmd.Start(); err != nil {
		h.t.Fatalf("start %s: %v", name, err)
	}
	go func() {
		p.err = p.cmd.Wait()
		close(p.done)
	}()
	return p
}

// operator runs the backend binary as an operator command, with the environment of the server.
func (h *harness) operator(args ...string) (stdout, stderr string, code int) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out, errOut bytes.Buffer
	cmd := exec.CommandContext(ctx, h.backendBin, args...)
	cmd.Dir = h.dir
	cmd.Env = h.backendEnv()
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	code = 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			h.t.Fatalf("operator command %v: %v\n%s", args, err, h.logs())
		}
	}
	return out.String(), errOut.String(), code
}

// stop sends SIGTERM, waits, then kills. A nil process or one that already stopped is left
// alone. A non-zero exit after SIGTERM is an error of the test.
func (h *harness) stop(p *process, limit time.Duration) {
	h.t.Helper()
	if p == nil {
		return
	}
	select {
	case <-p.done:
		return
	default:
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.done:
		if p.err != nil {
			h.t.Errorf("%s exited with an error after SIGTERM: %v", p.name, p.err)
		}
	case <-time.After(limit):
		_ = p.cmd.Process.Kill()
		<-p.done
		h.t.Errorf("%s did not stop within %s after SIGTERM; killed it", p.name, limit)
	}
}

func (h *harness) logs() string {
	var sb strings.Builder
	for _, p := range append(slices.Clone(h.stopped), h.core, h.backend) {
		if p == nil {
			continue
		}
		fmt.Fprintf(&sb, "--- %s output ---\n%s\n", p.name, p.out.String())
	}
	return sb.String()
}

// waitFor polls cond every 100 ms. On a timeout it fails with the name of the step and the logs.
func waitFor(t *testing.T, what string, limit time.Duration, cond func() bool, logs func() string) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout after %s: waiting for %s\n%s", limit, what, logs())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestHarnessStartsBothProcesses(t *testing.T) {
	start := time.Now()
	h := newHarness(t)

	resp, err := http.Get(h.backendURL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v\n%s", err, h.logs())
	}
	var health map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&health)
	resp.Body.Close()
	if health["database"] != "ok" || health["core"] != "ok" {
		t.Fatalf("healthz = %v, want database and core ok\n%s", health, h.logs())
	}
	// Both binaries are built with no version, so each one is "dev". The core version comes in the
	// header of Subscribe.
	if health["version"] != "dev" || health["core_version"] != "dev" {
		t.Fatalf("healthz = %v, want version and core_version dev\n%s", health, h.logs())
	}

	stdout, stderr, code := h.operator("org", "list")
	if code != 0 || stdout != "No organizations.\n" {
		t.Fatalf("org list: code %d, stdout %q, stderr %q\n%s", code, stdout, stderr, h.logs())
	}

	h.stop(h.backend, 15*time.Second)
	if !strings.Contains(h.backend.out.String(), "Shutdown complete.") {
		t.Errorf("the backend log has no \"Shutdown complete.\"\n%s", h.logs())
	}
	t.Logf("harness test took %s (with the builds)", time.Since(start))
}
