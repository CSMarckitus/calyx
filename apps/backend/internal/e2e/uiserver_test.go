//go:build e2e && uiserver

package e2e_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"calyx/backend/internal/auth"
	"calyx/backend/internal/database"
	"calyx/backend/internal/instances"
	"calyx/backend/internal/testutil"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// This file is the test program of the frontend browser tests. It is not a test of the backend:
// it starts the real backend binary on a fake core and a fake SMTP server, opens a control endpoint,
// and stays up until POST /quit. The Playwright tests call the control endpoint.
//
//	go test -tags 'e2e uiserver' -run '^TestUIServer$' -count=1 -timeout 0 ./internal/e2e/
//
// The browser tests start one program for each Playwright worker (a lane). The global setup gives
// each program its ports in CALYX_UI_BACKEND_PORT, CALYX_UI_CONTROL_PORT and CALYX_UI_PREVIEW_PORT.
// A run by hand uses the ports of lane 0 (apps/frontend/test/ui/lanes.ts).

// uiPort reads a port from the environment, or gives def when the variable is not set.
func uiPort(t *testing.T, env string, def int) int {
	v := os.Getenv(env)
	if v == "" {
		return def
	}
	p, err := strconv.Atoi(v)
	if err != nil || p < 1 || p > 65535 {
		t.Fatalf("%s=%q is not a port", env, v)
	}
	return p
}

// uiServer holds the state that the control handlers share.
type uiServer struct {
	t         *testing.T
	h         *harness
	core      *testutil.FakeCore
	pool      *pgxpool.Pool
	publicURL string // the origin of the preview; the backend checks the Origin header against it

	mu      sync.Mutex // guards the fields below
	backend *process   // nil while stopped
	mail    bool
	next    chan struct{} // the "wait" steps of the pairing that is set; nil without one

	quit     chan struct{}
	quitOnce sync.Once
}

func TestUIServer(t *testing.T) {
	backendPort := uiPort(t, "CALYX_UI_BACKEND_PORT", 18080)
	controlPort := uiPort(t, "CALYX_UI_CONTROL_PORT", 18100)
	previewPort := uiPort(t, "CALYX_UI_PREVIEW_PORT", 4180) // the vite preview: the public URL
	backendAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(backendPort))
	controlAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(controlPort))
	h := &harness{t: t, dir: t.TempDir(), root: repoRoot(t), httpPort: backendPort}
	h.backendBin = filepath.Join(h.dir, "backend")
	h.backendURL = "http://" + backendAddr
	h.smtp = testutil.NewFakeSMTP(t, testutil.FakeSMTPOptions{})
	h.databaseURL = testutil.NewDatabaseURL(t)
	// The default times of the backend, with no -X: a short interval would hide a broken stream.
	buildBinary(t, h.root, "./apps/backend/cmd/backend", h.backendBin, "")

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h.coreAddr = lis.Addr().String()
	s := &uiServer{
		t: t, h: h, core: testutil.NewFakeCoreOn(t, lis), mail: true, quit: make(chan struct{}),
		publicURL: "http://localhost:" + strconv.Itoa(previewPort),
	}
	s.core.SetVersion("0.0.0-fake") // the browser tests show this core version

	t.Cleanup(func() {
		if t.Failed() {
			t.Log(h.logs())
		}
	})
	t.Cleanup(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		h.stop(s.backend, 15*time.Second)
	})
	p, err := s.startBackend(true)
	if err != nil {
		t.Fatalf("%v\n%s", err, h.logs())
	}
	s.backend, h.backend = p, p

	s.pool, err = database.Open(context.Background(), h.databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.pool.Close)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ready", s.handleReady)
	mux.HandleFunc("POST /seed", s.handleSeed)
	mux.HandleFunc("POST /pairing", s.handlePairing)
	mux.HandleFunc("POST /pairing/next", s.handlePairingNext)
	mux.HandleFunc("POST /fault", s.handleFault)
	mux.HandleFunc("POST /status", s.handleStatus)
	mux.HandleFunc("POST /core", s.handleCore)
	mux.HandleFunc("POST /backend", s.handleBackend)
	mux.HandleFunc("POST /bulk", s.handleBulk)
	mux.HandleFunc("GET /core/updates", s.handleCoreUpdates)
	mux.HandleFunc("GET /mails", s.handleMails)
	mux.HandleFunc("POST /quit", s.handleQuit)
	ctl := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	ctlLis, err := net.Listen("tcp", controlAddr)
	if err != nil {
		t.Fatalf("control endpoint: %v", err)
	}
	go func() { _ = ctl.Serve(ctlLis) }() // Serve returns when the cleanup shuts the server down
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = ctl.Shutdown(ctx)
	})

	t.Logf("UI server ready: backend http://%s, control http://%s. POST /quit ends it.", backendAddr, controlAddr)
	<-s.quit
}

// --- The backend process ---

// startBackend starts the binary with the current mail setting. It waits until the database is ok,
// and with waitCore also until the core link is ok. The caller holds no lock in the first start.
func (s *uiServer) startBackend(waitCore bool) (*process, error) {
	env := append(s.h.backendEnv(),
		"PUBLIC_URL="+s.publicURL,
		"TRUST_PROXY=true",
		"CORE_TOKEN="+testutil.FakeCoreToken,
	)
	if !s.mail {
		env = append(env, "SMTP_HOST=", "SMTP_FROM=", "OPERATOR_EMAIL=")
	}
	// h.start stops the test with Fatalf, which is wrong in a handler goroutine. This copy returns the error.
	p := &process{name: "backend", out: &lockedBuffer{}, done: make(chan struct{})}
	p.cmd = exec.Command(s.h.backendBin, "serve")
	p.cmd.Dir, p.cmd.Env, p.cmd.Stdout, p.cmd.Stderr = s.h.dir, env, p.out, p.out
	if err := p.cmd.Start(); err != nil {
		return nil, fmt.Errorf("start backend: %w", err)
	}
	go func() {
		p.err = p.cmd.Wait()
		close(p.done)
	}()
	deadline := time.Now().Add(30 * time.Second)
	for {
		select {
		case <-p.done:
			return nil, fmt.Errorf("the backend exited at start: %w\n%s", p.err, p.out.String())
		default:
		}
		hl := s.h.health()
		if hl["database"] == "ok" && (!waitCore || hl["core"] == "ok") {
			return p, nil
		}
		if time.Now().After(deadline) {
			s.h.stop(p, 15*time.Second)
			return nil, fmt.Errorf("the backend is not ready after 30 s\n%s", p.out.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// --- Helpers of the handlers ---

// badRequest is an error that the handler answers with 400.
type badRequest string

func (e badRequest) Error() string { return string(e) }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	var br badRequest
	if errors.As(err, &br) {
		code = http.StatusBadRequest
	}
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// decode reads the JSON body. An unknown field is an error, so a typo in a test fails at once.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, badRequest("bad JSON body: "+err.Error()))
		return false
	}
	return true
}

func (s *uiServer) handleQuit(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"quit": true})
	s.quitOnce.Do(func() { close(s.quit) })
}

// GET /ready: 200 {"ready":true} when the backend answers and its database is ok.
func (s *uiServer) handleReady(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	running := s.backend != nil
	s.mu.Unlock()
	if running && s.h.health()["database"] == "ok" {
		writeJSON(w, http.StatusOK, map[string]bool{"ready": true})
		return
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]bool{"ready": false})
}

// --- /core and /backend ---

// POST /core {"action":"stop"|"start"}
func (s *uiServer) handleCore(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Action string `json:"action"`
	}
	if !decode(w, r, &in) {
		return
	}
	switch in.Action {
	case "stop":
		s.core.SetUnavailable(true)
		s.core.EndSubscriptions()
	case "start":
		s.core.SetUnavailable(false)
	default:
		writeErr(w, badRequest(`action must be "stop" or "start".`))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"core": map[string]string{"stop": "stopped", "start": "started"}[in.Action]})
}

// POST /backend {"action":"stop"|"start","mail":true|false}
// "mail" is for start. Absent keeps the last value (true at the first start).
func (s *uiServer) handleBackend(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Action string `json:"action"`
		Mail   *bool  `json:"mail"`
	}
	if !decode(w, r, &in) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch in.Action {
	case "stop":
		s.h.stop(s.backend, 15*time.Second)
		s.backend = nil
		writeJSON(w, http.StatusOK, map[string]string{"backend": "stopped"})
	case "start":
		if s.backend != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "The backend runs already."})
			return
		}
		if in.Mail != nil {
			s.mail = *in.Mail
		}
		p, err := s.startBackend(false)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		s.backend, s.h.backend = p, p
		writeJSON(w, http.StatusOK, map[string]any{"backend": "started", "mail": s.mail})
	default:
		writeErr(w, badRequest(`action must be "stop" or "start".`))
	}
}

// --- /mails ---

var mailLink = regexp.MustCompile(`https?://[^\s"<>)]+`)

// GET /mails answers a list of {"from","to":[...],"subject","body","paths":["/invite?token=..."]}.
// "paths" are the links of the body that start with the public URL, with the origin removed.
func (s *uiServer) handleMails(w http.ResponseWriter, _ *http.Request) {
	type mailOut struct {
		From    string   `json:"from"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		Body    string   `json:"body"`
		Paths   []string `json:"paths"`
	}
	out := []mailOut{}
	for _, m := range s.h.smtp.Messages() {
		head, body, _ := strings.Cut(m.Data, "\r\n\r\n")
		subject := ""
		for _, line := range strings.Split(head, "\r\n") {
			if v, ok := strings.CutPrefix(line, "Subject: "); ok {
				subject, _ = new(mime.WordDecoder).DecodeHeader(v)
			}
		}
		paths := []string{}
		for _, l := range mailLink.FindAllString(body, -1) {
			if p, ok := strings.CutPrefix(strings.TrimRight(l, ".,;"), s.publicURL); ok && p != "" {
				paths = append(paths, p)
			}
		}
		out = append(out, mailOut{From: m.From, To: m.To, Subject: subject, Body: body, Paths: paths})
	}
	writeJSON(w, http.StatusOK, out)
}

// --- /fault and /status ---

var allMethods = []testutil.Method{
	testutil.MethodCreateInstance, testutil.MethodRemoveInstance, testutil.MethodUpdateInstanceConfig,
	testutil.MethodListInstances, testutil.MethodGetInstanceStatus, testutil.MethodSendPlainText,
	testutil.MethodSendBulkSameMessage, testutil.MethodSendBulkCustomMessages, testutil.MethodGetBulkJob,
	testutil.MethodGetBulkJobs, testutil.MethodCancelBulkJob, testutil.MethodSubscribe,
}

// POST /fault {"method":"ListInstances","delay_ms":1500,"code":"UNAVAILABLE"}
// "code" is the name of a gRPC code. A method with no delay and no code clears that method.
// An empty object {} clears every method.
func (s *uiServer) handleFault(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Method  string      `json:"method"`
		DelayMS int         `json:"delay_ms"`
		Code    *codes.Code `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	methods := allMethods
	if in.Method != "" {
		methods = nil
		for _, m := range allMethods {
			if m == in.Method {
				methods = []testutil.Method{m}
			}
		}
		if methods == nil {
			writeErr(w, badRequest("unknown method "+in.Method+"."))
			return
		}
	} else if in.DelayMS != 0 || in.Code != nil {
		writeErr(w, badRequest("a delay or a code needs a method."))
		return
	}
	for _, m := range methods {
		s.core.SetDelay(m, time.Duration(in.DelayMS)*time.Millisecond)
		if in.Code == nil {
			s.core.SetError(m, nil)
		} else {
			s.core.SetError(m, status.Error(*in.Code, "Fault set by the test program."))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"methods": methods})
}

// coreNameOf accepts the id of the backend (a UUID) or the core name ("i_<hex>").
func coreNameOf(instance string) string {
	if id, err := uuid.Parse(instance); err == nil {
		return "i_" + strings.ReplaceAll(id.String(), "-", "")
	}
	return instance
}

func parseStatus(name string) (corev1.InstanceStatus, bool) {
	v, ok := corev1.InstanceStatus_value["INSTANCE_STATUS_"+name]
	return corev1.InstanceStatus(v), ok && v != 0
}

// POST /status {"instance":"<id or core name>","status":"CONNECTED"}
// It changes the status in the fake core and publishes the "connection.update" event.
func (s *uiServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Instance string `json:"instance"`
		Status   string `json:"status"`
	}
	if !decode(w, r, &in) {
		return
	}
	st, ok := parseStatus(in.Status)
	if !ok {
		writeErr(w, badRequest("status must be CONNECTED, CONNECTING, DISCONNECTED, NEEDS_AUTH or ERROR."))
		return
	}
	name := coreNameOf(in.Instance)
	if _, found := s.core.Instance(name); !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "The fake core has no instance " + name + "."})
		return
	}
	s.core.SetStatus(name, st)
	s.core.PublishConnection(name, st)
	writeJSON(w, http.StatusOK, map[string]string{"instance": name, "status": in.Status})
}

// --- /pairing ---

type pairStep struct {
	Step    string `json:"step"` // update, wait, connected, expired, refused, nothing
	QR      string `json:"qr"`   // update: the text of a QR code
	Code    string `json:"code"` // update: a pairing code (use qr or code)
	Message string `json:"message"`
}

// POST /pairing {"steps":[{"step":"update","qr":"2@abc"},{"step":"wait"},{"step":"connected"}]}
// The steps run in the next CreateInstance call of the fake core, and then the script clears.
func (s *uiServer) handlePairing(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Steps []pairStep `json:"steps"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Steps) == 0 {
		writeErr(w, badRequest("steps is empty."))
		return
	}
	for _, st := range in.Steps {
		switch st.Step {
		case "update":
			if (st.QR == "") == (st.Code == "") {
				writeErr(w, badRequest(`a step "update" needs "qr" or "code", and not both.`))
				return
			}
		case "wait", "connected", "expired", "refused", "nothing":
		default:
			writeErr(w, badRequest("unknown step "+strconv.Quote(st.Step)+"."))
			return
		}
	}
	next := make(chan struct{}, 8) // a "next" that comes before its "wait" still counts
	s.mu.Lock()
	s.next = next
	s.mu.Unlock()
	steps := in.Steps
	s.core.OnCreateInstance(func(ctx context.Context, _ *corev1.CreateInstanceRequest,
		send func(*corev1.CreateInstanceResponse) error,
	) error {
		s.core.OnCreateInstance(nil)
		for _, st := range steps {
			switch st.Step {
			case "update":
				code := st.QR + st.Code
				if err := send(&corev1.CreateInstanceResponse{
					Status: corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH, PairingCode: &code, Message: st.Message,
				}); err != nil {
					return err
				}
			case "wait":
				select {
				case <-next:
				case <-ctx.Done():
					return status.FromContextError(ctx.Err()).Err()
				}
			case "connected":
				if err := send(&corev1.CreateInstanceResponse{
					Status: corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED, Message: st.Message,
				}); err != nil {
					return err
				}
			case "expired":
				return status.Error(codes.DeadlineExceeded, "The pairing time ended.")
			case "refused":
				return status.Error(codes.FailedPrecondition, "The core refuses this pairing.")
			case "nothing":
				return nil
			}
		}
		return nil
	})
	writeJSON(w, http.StatusOK, map[string]int{"steps": len(steps)})
}

// POST /pairing/next ends one "wait" of the pairing that is set.
func (s *uiServer) handlePairingNext(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	next := s.next
	s.mu.Unlock()
	if next == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "No pairing is set."})
		return
	}
	select {
	case next <- struct{}{}:
	default:
	}
	writeJSON(w, http.StatusOK, map[string]bool{"next": true})
}

// --- /seed ---

type seedWebhook struct {
	URL    string  `json:"url"`
	Event  string  `json:"event"`
	Secret *string `json:"secret"`
}

type seedBulkJob struct {
	Status                   string   `json:"status"` // QUEUED, RUNNING, COMPLETED, CANCELLED or FAILED
	Total                    int32    `json:"total"`
	Sent                     int32    `json:"sent"`
	Failed                   int32    `json:"failed"`
	ProgressPercent          float32  `json:"progress_percent"`
	EstimatedFinishInSeconds *float64 `json:"estimated_finish_in_seconds"` // absent: no estimated end
	CreatedAgoSeconds        int      `json:"created_ago_seconds"`
	CreatedBy                string   `json:"created_by"` // the label of a seeded user
}

type seedRequest struct {
	Organization struct {
		Name               string `json:"name"`
		MaxInstances       *int   `json:"max_instances"`        // default 10
		WhatsAppWebAllowed *bool  `json:"whatsapp_web_allowed"` // default true; false = blocked
		Acknowledged       bool   `json:"acknowledged"`
	} `json:"organization"`
	// The default client "My numbers" exists always. A client with "default": true replaces it.
	Clients []struct {
		Name               string `json:"name"`
		Default            bool   `json:"default"`
		CanCreateInstances bool   `json:"can_create_instances"`
		MaxInstances       int    `json:"max_instances"` // 0 = no limit
	} `json:"clients"`
	Users []struct {
		Label    string `json:"label"`    // part of the new email; default: the role
		Role     string `json:"role"`     // owner or client
		Client   string `json:"client"`   // the name of the client, for a client user
		Password string `json:"password"` // default: a new one
		Pending  bool   `json:"pending"`  // an invitation, not a user. The answer has "invite_path"
		Reset    bool   `json:"reset"`    // adds a password reset. The answer has "reset_path"
		Disabled bool   `json:"disabled"`
	} `json:"users"`
	Instances []struct {
		Name                  string        `json:"name"`
		Client                string        `json:"client"`   // default: the default client
		Provider              string        `json:"provider"` // WHATSAPP_WEB (default) or META_CLOUD
		State                 string        `json:"state"`    // active (default), creating, removing or missing
		Status                string        `json:"status"`   // CONNECTED, ... or "" for none
		StatusAt              *time.Time    `json:"status_at"`
		StatusAgoSeconds      *int          `json:"status_ago_seconds"`
		Enabled               *bool         `json:"enabled"` // default true
		SendOnly              bool          `json:"send_only"`
		Locale                *string       `json:"locale"` // null or absent: none
		Webhooks              []seedWebhook `json:"webhooks"`
		AlertKind             string        `json:"alert_kind"` // needs_auth, error, disconnected or missing
		AlertOpenedAt         *time.Time    `json:"alert_opened_at"`
		AlertOpenedAgoSeconds *int          `json:"alert_opened_ago_seconds"`
		BulkJobs              []seedBulkJob `json:"bulk_jobs"`
	} `json:"instances"`
}

// at gives an absolute time, or "now minus ago", or nil.
func at(abs *time.Time, ago *int) *time.Time {
	switch {
	case abs != nil:
		return abs
	case ago != nil:
		t := time.Now().Add(-time.Duration(*ago) * time.Second)
		return &t
	}
	return nil
}

// POST /seed writes one new organization. The answer is
//
//	{"organization_id", "clients":[{"name","id","path"}],
//	 "users":[{"label","role","client","id","email","password","invite_path","reset_path"}],
//	 "instances":[{"name","id","core_name","path","bulk_job_ids"}]}
//
// with the same order as the request. The default client is the first of "clients".
// An instance with "bulk_jobs" gets the jobs in the database and in the fake core. "created_by" is the label of a
// user that is not pending.
func (s *uiServer) handleSeed(w http.ResponseWriter, r *http.Request) {
	var in seedRequest
	if !decode(w, r, &in) {
		return
	}
	out, err := s.seed(r.Context(), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type seedClientOut struct {
	Name string    `json:"name"`
	ID   uuid.UUID `json:"id"`
	Path string    `json:"path"`
}

type seedUserOut struct {
	Label      string    `json:"label"`
	Role       string    `json:"role"`
	Client     string    `json:"client,omitempty"`
	ID         uuid.UUID `json:"id"`
	Email      string    `json:"email"`
	Password   string    `json:"password,omitempty"`
	InvitePath string    `json:"invite_path,omitempty"`
	ResetPath  string    `json:"reset_path,omitempty"`
}

type seedInstanceOut struct {
	Name     string    `json:"name"`
	ID       uuid.UUID `json:"id"`
	CoreName string    `json:"core_name"`
	Path     string    `json:"path"`
	// Empty when the request has no bulk jobs.
	BulkJobIDs []uuid.UUID `json:"bulk_job_ids"`
}

type seedOut struct {
	OrganizationID uuid.UUID         `json:"organization_id"`
	Clients        []seedClientOut   `json:"clients"`
	Users          []seedUserOut     `json:"users"`
	Instances      []seedInstanceOut `json:"instances"`
}

func (s *uiServer) seed(ctx context.Context, in seedRequest) (seedOut, error) {
	var out seedOut
	if in.Organization.Name == "" {
		return out, badRequest("organization.name is required.")
	}
	key, _ := base64.StdEncoding.DecodeString(secretKey)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	maxInst, allowed := 10, true
	if in.Organization.MaxInstances != nil {
		maxInst = *in.Organization.MaxInstances
	}
	if in.Organization.WhatsAppWebAllowed != nil {
		allowed = *in.Organization.WhatsAppWebAllowed
	}
	orgID := uuid.New()
	out.OrganizationID = orgID

	// Hash the passwords before the transaction: argon2id is slow.
	type userRow struct {
		id   uuid.UUID
		hash string
	}
	rows := make([]userRow, len(in.Users))
	for i, u := range in.Users {
		if u.Role != "owner" && u.Role != "client" {
			return out, badRequest(`user.role must be "owner" or "client".`)
		}
		if u.Role == "client" && u.Client == "" {
			return out, badRequest("a client user needs a client.")
		}
		label := u.Label
		if label == "" {
			label = u.Role
		}
		o := seedUserOut{Label: label, Role: u.Role, Client: u.Client, ID: uuid.New(),
			Email: fmt.Sprintf("%s-%s-%d@ui.test", label, suffix, i)}
		if !u.Pending {
			o.Password = u.Password
			if o.Password == "" {
				o.Password = "Pw-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
			}
			h, err := auth.HashPassword(o.Password)
			if err != nil {
				return out, err
			}
			rows[i].hash = h
		}
		rows[i].id = o.ID
		out.Users = append(out.Users, o)
	}

	var puts []testutil.FakeInstance
	var jobs []*corev1.BulkJob

	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		exec := func(sql string, args ...any) error {
			_, err := tx.Exec(ctx, sql, args...)
			return err
		}
		if err := exec(`INSERT INTO organizations (id, name, max_instances, whatsapp_web_allowed) VALUES ($1, $2, $3, $4)`,
			orgID, in.Organization.Name, maxInst, allowed); err != nil {
			return err
		}
		clientIDs := map[string]uuid.UUID{}
		addClient := func(name string, isDefault, canCreate bool, limit int) error {
			id := uuid.New()
			clientIDs[name] = id
			out.Clients = append(out.Clients, seedClientOut{Name: name, ID: id, Path: "/clients/" + id.String()})
			return exec(`INSERT INTO clients (id, organization_id, name, is_default, can_create_instances, max_instances)
VALUES ($1, $2, $3, $4, $5, $6)`, id, orgID, name, isDefault, canCreate, limit)
		}
		defaultName, hasDefault := "My numbers", false
		for _, c := range in.Clients {
			if c.Default {
				defaultName, hasDefault = c.Name, true
			}
		}
		if !hasDefault {
			if err := addClient(defaultName, true, false, 0); err != nil {
				return err
			}
		}
		for _, c := range in.Clients {
			if c.Name == "" {
				return badRequest("client.name is required.")
			}
			if err := addClient(c.Name, c.Default, c.CanCreateInstances, c.MaxInstances); err != nil {
				return err
			}
		}
		// The default client is the first of the answer.
		for i, c := range out.Clients {
			if c.Name == defaultName && i != 0 {
				out.Clients[0], out.Clients[i] = out.Clients[i], out.Clients[0]
			}
		}

		var ownerID *uuid.UUID
		for i, u := range in.Users {
			o := &out.Users[i]
			var clientID *uuid.UUID
			if u.Role == "client" {
				id, ok := clientIDs[u.Client]
				if !ok {
					return badRequest("unknown client " + strconv.Quote(u.Client) + " of a user.")
				}
				clientID = &id
			}
			if u.Pending {
				plain, hash := auth.NewToken("")
				if err := exec(`INSERT INTO invitations (id, organization_id, client_id, role, email, token_hash, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)`, o.ID, orgID, clientID, u.Role, o.Email, hash, time.Now().Add(auth.InvitationTTL)); err != nil {
					return err
				}
				o.InvitePath = "/invite?token=" + plain
				continue
			}
			var disabled *time.Time
			if u.Disabled {
				now := time.Now()
				disabled = &now
			}
			if err := exec(`INSERT INTO users (id, email, password_hash, organization_id, role, client_id, disabled_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)`, o.ID, o.Email, rows[i].hash, orgID, u.Role, clientID, disabled); err != nil {
				return err
			}
			if u.Role == "owner" && ownerID == nil && !u.Disabled {
				ownerID = &o.ID
			}
			if u.Reset {
				plain, hash := auth.NewToken("")
				if err := exec(`INSERT INTO password_resets (id, user_id, token_hash, expires_at) VALUES ($1, $2, $3, $4)`,
					uuid.New(), o.ID, hash, time.Now().Add(auth.ResetTTL)); err != nil {
					return err
				}
				o.ResetPath = "/reset-password?token=" + plain
			}
		}
		if in.Organization.Acknowledged {
			if err := exec(`UPDATE organizations SET whatsapp_web_acknowledged_at = now(), whatsapp_web_acknowledged_by = $2 WHERE id = $1`,
				orgID, ownerID); err != nil {
				return err
			}
		}

		for _, inst := range in.Instances {
			if inst.Name == "" {
				return badRequest("instance.name is required.")
			}
			clientName := inst.Client
			if clientName == "" {
				clientName = defaultName
			}
			clientID, ok := clientIDs[clientName]
			if !ok {
				return badRequest("unknown client " + strconv.Quote(clientName) + " of an instance.")
			}
			provider, state := inst.Provider, inst.State
			if provider == "" {
				provider = "WHATSAPP_WEB"
			}
			if state == "" {
				state = "active"
			}
			var lastStatus *string
			var coreStatus corev1.InstanceStatus
			if inst.Status != "" {
				st, ok := parseStatus(inst.Status)
				if !ok {
					return badRequest("unknown status " + strconv.Quote(inst.Status) + ".")
				}
				lastStatus, coreStatus = &inst.Status, st
			} else {
				coreStatus = corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED
			}
			enabled := inst.Enabled == nil || *inst.Enabled
			id := uuid.New()
			hex := strings.ReplaceAll(id.String(), "-", "")
			var phone *string
			if provider == "META_CLOUD" {
				p := "pn-" + hex
				phone = &p
			}
			statusAt := at(inst.StatusAt, inst.StatusAgoSeconds)
			var checkedAt *time.Time
			if lastStatus != nil {
				now := time.Now()
				checkedAt = &now
				if statusAt == nil {
					statusAt = &now
				}
			}
			var alertKind *string
			alertAt := at(inst.AlertOpenedAt, inst.AlertOpenedAgoSeconds)
			var alertedAt *time.Time
			if inst.AlertKind != "" {
				if alertAt == nil {
					now := time.Now()
					alertAt = &now
				}
				// The alert mail counts as sent, so the watcher sends no mail for a seeded alert.
				now := time.Now()
				alertKind, alertedAt = &inst.AlertKind, &now
			}
			if err := exec(`
INSERT INTO instances (id, organization_id, client_id, core_id, core_name, display_name, provider, send_only, enabled,
                       phone_number_id, state, last_status, last_status_at, status_checked_at,
                       alert_kind, alert_opened_at, alerted_at, locale)
VALUES ($1, $2, $3, 'main', $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`,
				id, orgID, clientID, "i_"+hex, inst.Name, provider, inst.SendOnly, enabled,
				phone, state, lastStatus, statusAt, checkedAt, alertKind, alertAt, alertedAt, inst.Locale); err != nil {
				return err
			}
			var hooks []*corev1.WebhookConfig
			for _, wh := range inst.Webhooks {
				var sealed []byte
				if wh.Secret != nil {
					var err error
					if sealed, err = instances.EncryptSecret(key, *wh.Secret); err != nil {
						return err
					}
				}
				if err := exec(`INSERT INTO webhooks (id, instance_id, url, event, secret_ciphertext) VALUES ($1, $2, $3, $4, $5)`,
					uuid.New(), id, wh.URL, wh.Event, sealed); err != nil {
					return err
				}
				hooks = append(hooks, &corev1.WebhookConfig{Url: wh.URL, Event: wh.Event, Secret: wh.Secret})
			}
			jobIDs := []uuid.UUID{}
			for _, bj := range inst.BulkJobs {
				st, ok := corev1.BulkJobStatus_value["BULK_JOB_STATUS_"+bj.Status]
				if !ok || st == 0 {
					return badRequest("unknown bulk job status " + strconv.Quote(bj.Status) + ".")
				}
				var creator *uuid.UUID
				for i, u := range in.Users {
					if !u.Pending && out.Users[i].Label == bj.CreatedBy {
						creator = &out.Users[i].ID
						break
					}
				}
				if creator == nil {
					return badRequest("unknown user label " + strconv.Quote(bj.CreatedBy) + " in created_by.")
				}
				jobID := uuid.New()
				created := time.Now().Add(-time.Duration(bj.CreatedAgoSeconds) * time.Second)
				if err := exec(`INSERT INTO bulk_jobs (id, instance_id, organization_id, client_id, created_by, created_at)
VALUES ($1, $2, $3, $4, $5, $6)`, jobID, id, orgID, clientID, *creator, created); err != nil {
					return err
				}
				jobIDs = append(jobIDs, jobID)
				jobs = append(jobs, &corev1.BulkJob{
					Id: jobID.String(), InstanceName: "i_" + hex, Status: corev1.BulkJobStatus(st),
					TotalMessages: bj.Total, SentMessages: bj.Sent, FailedMessages: bj.Failed,
					ProgressPercent: bj.ProgressPercent, CreatedAt: timestamppb.New(created),
					EstimatedFinishAt: finishIn(bj.EstimatedFinishInSeconds),
				})
			}
			out.Instances = append(out.Instances, seedInstanceOut{Name: inst.Name, ID: id, CoreName: "i_" + hex,
				Path: "/instances/" + id.String(), BulkJobIDs: jobIDs})
			if state == "active" {
				fi := testutil.FakeInstance{
					Name: "i_" + hex, Status: coreStatus, Enabled: enabled, SendOnly: inst.SendOnly,
					Webhooks: hooks, Provider: corev1.Provider_PROVIDER_WHATSAPP_WEB,
				}
				if provider == "META_CLOUD" {
					fi.Provider, fi.PhoneNumberID = corev1.Provider_PROVIDER_META_CLOUD, *phone
				}
				if inst.Locale != nil {
					fi.Locale = *inst.Locale
				}
				puts = append(puts, fi)
			}
		}
		return nil
	})
	if err != nil {
		return seedOut{}, err
	}
	// The fake core gets its rows after the commit, so a failed seed leaves no instance in it.
	for _, fi := range puts {
		s.core.PutInstance(fi)
	}
	for _, j := range jobs {
		s.core.PutBulkJob(j)
	}
	return out, nil
}

// finishIn gives the estimated end of a bulk job, or nil for none.
func finishIn(seconds *float64) *timestamppb.Timestamp {
	if seconds == nil {
		return nil
	}
	return timestamppb.New(time.Now().Add(time.Duration(*seconds * float64(time.Second))))
}

// --- /bulk and /core/updates ---

// POST /bulk {"id","status","sent","failed","progress_percent","estimated_finish_in_seconds"}
// It replaces the progress of a job of the fake core. The total and the creation time stay.
func (s *uiServer) handleBulk(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID                       string   `json:"id"`
		Status                   string   `json:"status"`
		Sent                     int32    `json:"sent"`
		Failed                   int32    `json:"failed"`
		ProgressPercent          float32  `json:"progress_percent"`
		EstimatedFinishInSeconds *float64 `json:"estimated_finish_in_seconds"`
	}
	if !decode(w, r, &in) {
		return
	}
	st, ok := corev1.BulkJobStatus_value["BULK_JOB_STATUS_"+in.Status]
	if !ok || st == 0 {
		writeErr(w, badRequest("status must be QUEUED, RUNNING, COMPLETED, CANCELLED or FAILED."))
		return
	}
	job, found := s.core.BulkJob(in.ID)
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "The fake core has no bulk job " + in.ID + "."})
		return
	}
	job.Status = corev1.BulkJobStatus(st)
	job.SentMessages, job.FailedMessages, job.ProgressPercent = in.Sent, in.Failed, in.ProgressPercent
	job.EstimatedFinishAt = finishIn(in.EstimatedFinishInSeconds)
	s.core.PutBulkJob(job)
	w.WriteHeader(http.StatusNoContent)
}

// GET /core/updates?instance=<id or core name> gives the UpdateInstanceConfig calls of the fake core for that
// instance, in order. A field that the call does not set is null.
func (s *uiServer) handleCoreUpdates(w http.ResponseWriter, r *http.Request) {
	name := coreNameOf(r.URL.Query().Get("instance"))
	type webhook struct {
		Event  string  `json:"event"`
		URL    string  `json:"url"`
		Secret *string `json:"secret"`
	}
	type update struct {
		Webhooks []webhook `json:"webhooks"`
		Enabled  *bool     `json:"enabled"`
		SendOnly *bool     `json:"send_only"`
	}
	out := []update{}
	for _, req := range s.core.UpdateRequests() {
		if req.GetInstanceName() != name {
			continue
		}
		u := update{Enabled: req.IsEnabled, SendOnly: req.IsUsedOnlyForSendMessages}
		if req.Webhooks != nil {
			u.Webhooks = []webhook{}
			for _, wh := range req.Webhooks.GetWebhooks() {
				u.Webhooks = append(u.Webhooks, webhook{Event: wh.GetEvent(), URL: wh.GetUrl(), Secret: wh.Secret})
			}
		}
		out = append(out, u)
	}
	writeJSON(w, http.StatusOK, out)
}
