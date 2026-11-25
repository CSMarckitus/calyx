package testutil

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"calyx/backend/internal/core"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// FakeCoreToken is the token that the connection of NewFakeCore sends.
// The real core needs 32 characters or more, and the browser tests run the real backend on the fake core.
const FakeCoreToken = "fake-core-token-0123456789abcdef0123"

// Method is an alias, so plain string literals ("RemoveInstance") work too.
type Method = string

const (
	MethodCreateInstance         Method = "CreateInstance"
	MethodRemoveInstance         Method = "RemoveInstance"
	MethodUpdateInstanceConfig   Method = "UpdateInstanceConfig"
	MethodListInstances          Method = "ListInstances"
	MethodGetInstanceStatus      Method = "GetInstanceStatus"
	MethodSendPlainText          Method = "SendPlainText"
	MethodSendBulkSameMessage    Method = "SendBulkSameMessage"
	MethodSendBulkCustomMessages Method = "SendBulkCustomMessages"
	MethodGetBulkJob             Method = "GetBulkJob"
	MethodGetBulkJobs            Method = "GetBulkJobs"
	MethodCancelBulkJob          Method = "CancelBulkJob"
	MethodSubscribe              Method = "Subscribe"
)

// fakeLocaleForm is the form of a locale that the core accepts.
var fakeLocaleForm = regexp.MustCompile(`^[a-z]{2}-[A-Z]{2}$`)

// FakeInstance is one instance of the fake core.
type FakeInstance struct {
	Name          string // the core name, "i_..."
	Provider      corev1.Provider
	Status        corev1.InstanceStatus
	Enabled       bool
	SendOnly      bool
	Webhooks      []*corev1.WebhookConfig // as last received, secrets included
	PhoneNumberID string                  // Meta only
	// WhatsApp Web only, as the core stores them.
	DeviceIdentity corev1.DeviceIdentity
	Locale         string
}

// CreateStep is one step of a scripted CreateInstance stream.
type CreateStep struct {
	Status      corev1.InstanceStatus // sends one CreateInstanceResponse with this status; UNSPECIFIED sends nothing
	PairingCode string                // set on that response when not empty
	Message     string
	Delay       time.Duration // waited before the step; ends early when the caller cancels
	Err         error         // ends the stream with this error (build it with status.Error)
	Hold        bool          // waits until Release(name) or until the caller cancels
}

// Request is one recorded call.
type Request struct {
	Method      Method
	Message     proto.Message // a copy of the request
	Token       string        // the "authorization" metadata value, "" when absent
	HasDeadline bool
	Timeout     time.Duration // time left until the deadline when the call arrived; 0 without one
}

// FakeCore is a scriptable in-memory core. Every access to its state holds one mutex.
type FakeCore struct {
	mu          sync.Mutex
	server      *grpc.Server
	lis         net.Listener
	instances   map[string]*FakeInstance
	pairing     map[string]bool
	scripts     map[string][]CreateStep
	holds       map[string]chan struct{}
	onCreate    func(ctx context.Context, req *corev1.CreateInstanceRequest, send func(*corev1.CreateInstanceResponse) error) error
	onSend      func(ctx context.Context, req *corev1.SendPlainTextRequest) (*corev1.SendPlainTextResponse, error)
	errs        map[Method]error
	delays      map[Method]time.Duration
	unavailable bool
	jobs        map[string]*corev1.BulkJob
	requests    []Request
	sendCount   int
	replays     map[replayIndex]proto.Message
	subs        map[*fakeSub]struct{}
	noHeaders   bool
	version     string
}

// fakeSub is one open Subscribe stream. end closes when EndSubscriptions runs.
type fakeSub struct {
	names, types map[string]bool // empty = all
	events       chan *corev1.Event
	end          chan struct{}
}

// replayIndex is the key of the replay table: the same key on two instances is two entries.
type replayIndex struct{ instance, key string }

// fakeCoreMaxMessageBytes is grpcserver.MaxMessageBytes of the real core (apps/core): 17 MiB.
// The backend cannot import the core, so the value is repeated here.
const fakeCoreMaxMessageBytes = 17 << 20

// NewFakeCore serves the generated InstanceService, MessageService and EventService on a bufconn
// listener and returns a connection to it that carries the token FakeCoreToken. Everything is
// closed with t.Cleanup.
func NewFakeCore(t *testing.T) (*FakeCore, *grpc.ClientConn) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	f := NewFakeCoreOn(t, lis)
	unary, stream := core.TokenInterceptors(FakeCoreToken)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(unary),
		grpc.WithChainStreamInterceptor(stream))
	if err != nil {
		t.Fatalf("fake core: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return f, conn
}

// NewFakeCoreOn serves the same services on lis, for a client in another process (a real backend
// binary). The caller dials lis and sends the token FakeCoreToken. t.Cleanup stops the server and
// closes lis.
func NewFakeCoreOn(t *testing.T, lis net.Listener) *FakeCore {
	t.Helper()
	f := &FakeCore{
		instances: map[string]*FakeInstance{},
		pairing:   map[string]bool{},
		scripts:   map[string][]CreateStep{},
		holds:     map[string]chan struct{}{},
		errs:      map[Method]error{},
		delays:    map[Method]time.Duration{},
		jobs:      map[string]*corev1.BulkJob{},
		subs:      map[*fakeSub]struct{}{},
		lis:       lis,
		server: grpc.NewServer(grpc.UnaryInterceptor(checkTokenUnary), grpc.StreamInterceptor(checkTokenStream),
			grpc.MaxRecvMsgSize(fakeCoreMaxMessageBytes)),
	}
	corev1.RegisterInstanceServiceServer(f.server, &fakeInstanceServer{f: f})
	corev1.RegisterMessageServiceServer(f.server, &fakeMessageServer{f: f})
	corev1.RegisterEventServiceServer(f.server, &fakeEventServer{f: f})
	go func() { _ = f.server.Serve(f.lis) }() // Serve returns when the test stops the server

	t.Cleanup(func() {
		f.server.Stop()
		f.lis.Close()
	})
	return f
}

// checkToken does what the real core does (apps/core/internal/grpcserver): the metadata
// "authorization" must be "Bearer <token>".
func checkToken(ctx context.Context) error {
	md, _ := metadata.FromIncomingContext(ctx)
	for _, v := range md.Get("authorization") {
		if v == "Bearer "+FakeCoreToken {
			return nil
		}
	}
	return status.Error(codes.Unauthenticated, "Missing or wrong authorization token.")
}

func checkTokenUnary(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
	if err := checkToken(ctx); err != nil {
		return nil, err
	}
	return h(ctx, req)
}

func checkTokenStream(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, h grpc.StreamHandler) error {
	if err := checkToken(ss.Context()); err != nil {
		return err
	}
	return h(srv, ss)
}

// --- Instances ---

func copyInstance(in *FakeInstance) FakeInstance {
	c := *in
	c.Webhooks = cloneWebhooks(in.Webhooks)
	return c
}

func cloneWebhooks(ws []*corev1.WebhookConfig) []*corev1.WebhookConfig {
	if ws == nil {
		return nil
	}
	out := make([]*corev1.WebhookConfig, len(ws))
	for i, w := range ws {
		out[i] = proto.Clone(w).(*corev1.WebhookConfig)
	}
	return out
}

func (f *FakeCore) PutInstance(in FakeInstance) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := copyInstance(&in)
	f.instances[in.Name] = &c
}

// AddInstance is PutInstance with Enabled true.
func (f *FakeCore) AddInstance(name string, provider corev1.Provider, status corev1.InstanceStatus) {
	in := FakeInstance{Name: name, Provider: provider, Status: status, Enabled: true}
	if provider == corev1.Provider_PROVIDER_WHATSAPP_WEB {
		// As an instance that the core paired with a device identity and a locale.
		in.DeviceIdentity, in.Locale = corev1.DeviceIdentity_DEVICE_IDENTITY_CHROME_WINDOWS, "es-ES"
	}
	f.PutInstance(in)
}

// Instance returns a copy of one instance.
func (f *FakeCore) Instance(name string) (FakeInstance, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	in, ok := f.instances[name]
	if !ok {
		return FakeInstance{}, false
	}
	return copyInstance(in), true
}

// Instances returns copies of every instance, sorted by name.
func (f *FakeCore) Instances() []FakeInstance {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sortedLocked()
}

func (f *FakeCore) sortedLocked() []FakeInstance {
	out := make([]FakeInstance, 0, len(f.instances))
	for _, in := range f.instances {
		out = append(out, copyInstance(in))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SetStatus changes the status of an instance. Unknown names are ignored.
func (f *FakeCore) SetStatus(name string, status corev1.InstanceStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if in, ok := f.instances[name]; ok {
		in.Status = status
	}
}

// DropInstance forgets an instance. No request is recorded.
func (f *FakeCore) DropInstance(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.instances, name)
}

// --- CreateInstance and SendPlainText ---

// ScriptCreate sets the steps for the next CreateInstance of that name.
func (f *FakeCore) ScriptCreate(name string, steps ...CreateStep) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts[name] = steps
}

func (f *FakeCore) holdChan(name string) chan struct{} {
	ch, ok := f.holds[name]
	if !ok {
		ch = make(chan struct{}, 1)
		f.holds[name] = ch
	}
	return ch
}

// Release ends the current or the next Hold of that name, once.
func (f *FakeCore) Release(name string) {
	f.mu.Lock()
	ch := f.holdChan(name)
	f.mu.Unlock()
	select {
	case ch <- struct{}{}:
	default:
	}
}

// OnCreateInstance replaces the scripts and the default script. nil clears it.
func (f *FakeCore) OnCreateInstance(fn func(ctx context.Context, req *corev1.CreateInstanceRequest,
	send func(*corev1.CreateInstanceResponse) error) error,
) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onCreate = fn
}

// OnSendPlainText replaces the default answer of SendPlainText. nil clears it.
func (f *FakeCore) OnSendPlainText(fn func(ctx context.Context, req *corev1.SendPlainTextRequest) (*corev1.SendPlainTextResponse, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onSend = fn
}

// --- Failures ---

// SetError makes every call of m return err. nil clears it.
func (f *FakeCore) SetError(m Method, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.errs, m)
		return
	}
	f.errs[m] = err
}

// SetDelay makes every call of m wait d first. 0 clears it.
func (f *FakeCore) SetDelay(m Method, d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d == 0 {
		delete(f.delays, m)
		return
	}
	f.delays[m] = d
}

// SetUnavailable makes every call of every method return UNAVAILABLE.
func (f *FakeCore) SetUnavailable(on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unavailable = on
}

// Kill stops the server: it cuts every open call and connection.
func (f *FakeCore) Kill() { f.server.Stop() }

// --- Bulk jobs ---

// PutBulkJob adds or replaces a job. A copy is stored.
func (f *FakeCore) PutBulkJob(job *corev1.BulkJob) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobs[job.GetId()] = proto.Clone(job).(*corev1.BulkJob)
}

// BulkJob returns a copy of one job.
func (f *FakeCore) BulkJob(id string) (*corev1.BulkJob, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return nil, false
	}
	return proto.Clone(j).(*corev1.BulkJob), true
}

// --- Recorded calls ---

// Requests returns the recorded calls of m in arrival order.
func (f *FakeCore) Requests(m Method) []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Request
	for _, r := range f.requests {
		if r.Method == m {
			r.Message = proto.Clone(r.Message)
			out = append(out, r)
		}
	}
	return out
}

// WaitForRequests polls every 5 ms until m has n requests. It calls t.Fatal after 2 s.
func (f *FakeCore) WaitForRequests(t *testing.T, m Method, n int) []Request {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if rs := f.Requests(m); len(rs) >= n {
			return rs
		}
		if time.Now().After(deadline) {
			t.Fatalf("fake core: waited for %d %s requests", n, m)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func typedRequests[T proto.Message](f *FakeCore, m Method) []T {
	var out []T
	for _, r := range f.Requests(m) {
		out = append(out, r.Message.(T))
	}
	return out
}

func (f *FakeCore) CreateRequests() []*corev1.CreateInstanceRequest {
	return typedRequests[*corev1.CreateInstanceRequest](f, MethodCreateInstance)
}

func (f *FakeCore) UpdateRequests() []*corev1.UpdateInstanceConfigRequest {
	return typedRequests[*corev1.UpdateInstanceConfigRequest](f, MethodUpdateInstanceConfig)
}

func (f *FakeCore) RemoveRequests() []*corev1.RemoveInstanceRequest {
	return typedRequests[*corev1.RemoveInstanceRequest](f, MethodRemoveInstance)
}

func (f *FakeCore) StatusRequests() []*corev1.GetInstanceStatusRequest {
	return typedRequests[*corev1.GetInstanceStatusRequest](f, MethodGetInstanceStatus)
}

func (f *FakeCore) SendRequests() []*corev1.SendPlainTextRequest {
	return typedRequests[*corev1.SendPlainTextRequest](f, MethodSendPlainText)
}

func (f *FakeCore) BulkSameRequests() []*corev1.SendBulkSameMessageRequest {
	return typedRequests[*corev1.SendBulkSameMessageRequest](f, MethodSendBulkSameMessage)
}

func (f *FakeCore) BulkCustomRequests() []*corev1.SendBulkCustomMessagesRequest {
	return typedRequests[*corev1.SendBulkCustomMessagesRequest](f, MethodSendBulkCustomMessages)
}

func (f *FakeCore) GetBulkRequests() []*corev1.GetBulkJobRequest {
	return typedRequests[*corev1.GetBulkJobRequest](f, MethodGetBulkJob)
}

func (f *FakeCore) GetBulkJobsRequests() []*corev1.GetBulkJobsRequest {
	return typedRequests[*corev1.GetBulkJobsRequest](f, MethodGetBulkJobs)
}

func (f *FakeCore) CancelBulkRequests() []*corev1.CancelBulkJobRequest {
	return typedRequests[*corev1.CancelBulkJobRequest](f, MethodCancelBulkJob)
}

// begin runs the steps 1 to 4 of every handler: record, the "all unavailable" switch, the delay
// and the scripted error. A refused call is still recorded.
func (f *FakeCore) begin(ctx context.Context, m Method, req proto.Message) error {
	r := Request{Method: m, Message: proto.Clone(req)}
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md.Get("authorization"); len(v) > 0 {
			r.Token = v[0]
		}
	}
	if dl, ok := ctx.Deadline(); ok {
		r.HasDeadline = true
		r.Timeout = time.Until(dl)
	}
	f.mu.Lock()
	f.requests = append(f.requests, r)
	unavailable, delay, err := f.unavailable, f.delays[m], f.errs[m]
	f.mu.Unlock()

	if unavailable {
		return status.Error(codes.Unavailable, "The fake core is unavailable.")
	}
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return status.FromContextError(ctx.Err()).Err()
		}
	}
	return err
}

// --- InstanceService ---

type fakeInstanceServer struct {
	corev1.UnimplementedInstanceServiceServer
	f *FakeCore
}

func (s *fakeInstanceServer) ListInstances(ctx context.Context, req *corev1.ListInstancesRequest) (*corev1.ListInstancesResponse, error) {
	if err := s.f.begin(ctx, MethodListInstances, req); err != nil {
		return nil, err
	}
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	resp := &corev1.ListInstancesResponse{}
	for _, in := range s.f.sortedLocked() {
		info := &corev1.InstanceInfo{
			InstanceName:              in.Name,
			Provider:                  in.Provider,
			ConnectionStatus:          in.Status,
			IsEnabled:                 in.Enabled,
			IsUsedOnlyForSendMessages: in.SendOnly,
			DeviceIdentity:            in.DeviceIdentity,
		}
		if in.Locale != "" {
			info.Locale = proto.String(in.Locale)
		}
		for _, w := range in.Webhooks {
			info.Webhooks = append(info.Webhooks, &corev1.WebhookSettings{
				Url: w.GetUrl(), Event: w.GetEvent(), IsActive: true, HasSecret: w.GetSecret() != "",
			})
		}
		resp.Instances = append(resp.Instances, info)
	}
	return resp, nil
}

func (s *fakeInstanceServer) GetInstanceStatus(ctx context.Context, req *corev1.GetInstanceStatusRequest) (*corev1.GetInstanceStatusResponse, error) {
	if err := s.f.begin(ctx, MethodGetInstanceStatus, req); err != nil {
		return nil, err
	}
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	in, ok := s.f.instances[req.GetInstanceName()]
	if !ok {
		return nil, status.Error(codes.NotFound, "Instance "+req.GetInstanceName()+" not found.")
	}
	return &corev1.GetInstanceStatusResponse{Status: in.Status}, nil
}

func (s *fakeInstanceServer) RemoveInstance(ctx context.Context, req *corev1.RemoveInstanceRequest) (*corev1.RemoveInstanceResponse, error) {
	if err := s.f.begin(ctx, MethodRemoveInstance, req); err != nil {
		return nil, err
	}
	name := req.GetInstanceName()
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	if _, ok := s.f.instances[name]; !ok {
		return nil, status.Error(codes.NotFound, "Instance "+name+" not found.")
	}
	if s.f.pairing[name] {
		return nil, status.Error(codes.FailedPrecondition, "Instance "+name+" is being paired. Cancel the pairing first.")
	}
	delete(s.f.instances, name)
	return &corev1.RemoveInstanceResponse{}, nil
}

func (s *fakeInstanceServer) UpdateInstanceConfig(ctx context.Context, req *corev1.UpdateInstanceConfigRequest) (*corev1.UpdateInstanceConfigResponse, error) {
	if err := s.f.begin(ctx, MethodUpdateInstanceConfig, req); err != nil {
		return nil, err
	}
	name := req.GetInstanceName()
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	in, ok := s.f.instances[name]
	if !ok {
		return nil, status.Error(codes.NotFound, "Instance "+name+" not found.")
	}
	// As the core, no change goes through while the instance pairs: it would open with old values.
	if s.f.pairing[name] {
		return nil, status.Error(codes.FailedPrecondition, "Instance "+name+" is being paired. Cancel the pairing first.")
	}
	if req.IsEnabled != nil {
		in.Enabled = req.GetIsEnabled()
	}
	if req.IsUsedOnlyForSendMessages != nil {
		in.SendOnly = req.GetIsUsedOnlyForSendMessages()
	}
	if req.Webhooks != nil {
		in.Webhooks = cloneWebhooks(req.Webhooks.GetWebhooks())
	}
	if req.MetaCloud != nil {
		in.PhoneNumberID = req.MetaCloud.GetPhoneNumberId()
		in.Status = corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED
	}
	return &corev1.UpdateInstanceConfigResponse{}, nil
}

func requestedProvider(req *corev1.CreateInstanceRequest) corev1.Provider {
	if req.GetMetaCloud() != nil {
		return corev1.Provider_PROVIDER_META_CLOUD
	}
	return corev1.Provider_PROVIDER_WHATSAPP_WEB
}

func defaultScript(req *corev1.CreateInstanceRequest) []CreateStep {
	connected := CreateStep{Status: corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED, Message: "Instance connected successfully."}
	if req.GetMetaCloud() != nil {
		return []CreateStep{connected}
	}
	needs := CreateStep{
		Status:      corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH,
		PairingCode: "fake-qr-1", Message: "Please scan the QR code to authenticate.",
	}
	if req.GetWhatsappWeb().GetPairingCode() != nil {
		needs.PairingCode = "FAKE1234"
		needs.Message = "Please enter the pairing code to authenticate."
	}
	return []CreateStep{needs, {Hold: true}, connected}
}

func (s *fakeInstanceServer) CreateInstance(req *corev1.CreateInstanceRequest, stream grpc.ServerStreamingServer[corev1.CreateInstanceResponse]) error {
	f := s.f
	ctx := stream.Context()
	if err := f.begin(ctx, MethodCreateInstance, req); err != nil {
		return err
	}
	name := req.GetInstanceName()
	f.mu.Lock()
	stored, found := f.instances[name]
	if found {
		if stored.Status != corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH {
			f.mu.Unlock()
			return status.Error(codes.AlreadyExists, "Instance with name "+name+" already exists.")
		}
		if stored.Provider != requestedProvider(req) {
			f.mu.Unlock()
			return status.Error(codes.InvalidArgument, "Instance "+name+" uses another provider.")
		}
		if !stored.Enabled {
			f.mu.Unlock()
			return status.Error(codes.FailedPrecondition, "Instance "+name+" is disabled. Enable it before you pair it again.")
		}
	}
	// As the core: a WhatsApp Web pairing needs a locale; without an identity the core
	// picks one at random, and the fake always picks CHROME_WINDOWS.
	var identity corev1.DeviceIdentity
	var locale string
	if web := req.GetWhatsappWeb(); requestedProvider(req) == corev1.Provider_PROVIDER_WHATSAPP_WEB {
		identity, locale = web.GetDeviceIdentity(), web.GetLocale()
		if identity == corev1.DeviceIdentity_DEVICE_IDENTITY_UNSPECIFIED {
			identity = corev1.DeviceIdentity_DEVICE_IDENTITY_CHROME_WINDOWS
		}
		if _, known := corev1.DeviceIdentity_name[int32(identity)]; !known {
			f.mu.Unlock()
			return status.Error(codes.InvalidArgument, "device_identity is not in the list of the core.")
		}
		if locale != "" && !fakeLocaleForm.MatchString(locale) {
			f.mu.Unlock()
			return status.Error(codes.InvalidArgument, "locale must be a locale like es-ES.")
		}
		if locale == "" && found {
			locale = stored.Locale
		}
		if locale == "" {
			f.mu.Unlock()
			return status.Error(codes.InvalidArgument, "locale is required for a new pairing, for example es-ES.")
		}
	}
	if found {
		// As the core, a re-pair writes its configuration before the pairing.
		stored.DeviceIdentity, stored.Locale = identity, locale
		if len(req.GetWebhooks()) > 0 {
			stored.Webhooks = cloneWebhooks(req.GetWebhooks())
		}
		if req.IsUsedOnlyForSendMessages != nil {
			stored.SendOnly = req.GetIsUsedOnlyForSendMessages()
		}
	}
	if f.pairing[name] {
		f.mu.Unlock()
		return status.Error(codes.AlreadyExists, "Instance "+name+" is being paired right now.")
	}
	f.pairing[name] = true
	fn := f.onCreate
	steps, scripted := f.scripts[name]
	delete(f.scripts, name)
	hold := f.holdChan(name)
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		delete(f.pairing, name)
		f.mu.Unlock()
	}()

	last := corev1.InstanceStatus_INSTANCE_STATUS_UNSPECIFIED
	send := func(resp *corev1.CreateInstanceResponse) error {
		last = resp.GetStatus()
		if last == corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED && locale != "" && resp.Locale == nil {
			resp.DeviceIdentity, resp.Locale = identity, proto.String(locale)
		}
		return stream.Send(resp)
	}
	var err error
	if fn != nil {
		err = fn(ctx, req, send)
	} else {
		if !scripted {
			steps = defaultScript(req)
		}
		err = runSteps(ctx, steps, hold, send)
	}
	if err != nil {
		return err
	}
	if last == corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED {
		f.mu.Lock()
		in := &FakeInstance{
			Name:           name,
			Provider:       requestedProvider(req),
			Status:         corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED,
			Enabled:        true,
			SendOnly:       req.GetIsUsedOnlyForSendMessages(),
			Webhooks:       cloneWebhooks(req.GetWebhooks()),
			PhoneNumberID:  req.GetMetaCloud().GetPhoneNumberId(),
			DeviceIdentity: identity,
			Locale:         locale,
		}
		// As the core, a re-pair keeps what the request does not carry.
		if found && len(req.GetWebhooks()) == 0 {
			in.Webhooks = cloneWebhooks(stored.Webhooks)
		}
		if found && req.IsUsedOnlyForSendMessages == nil {
			in.SendOnly = stored.SendOnly
		}
		f.instances[name] = in
		f.mu.Unlock()
	}
	return nil
}

func runSteps(ctx context.Context, steps []CreateStep, hold chan struct{}, send func(*corev1.CreateInstanceResponse) error) error {
	for _, st := range steps {
		if st.Delay > 0 {
			timer := time.NewTimer(st.Delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return status.FromContextError(ctx.Err()).Err()
			}
		}
		if st.Hold {
			select {
			case <-hold:
			case <-ctx.Done():
				return status.FromContextError(ctx.Err()).Err()
			}
		}
		if st.Err != nil {
			return st.Err
		}
		if st.Status != corev1.InstanceStatus_INSTANCE_STATUS_UNSPECIFIED {
			resp := &corev1.CreateInstanceResponse{Status: st.Status, Message: st.Message}
			if st.PairingCode != "" {
				code := st.PairingCode
				resp.PairingCode = &code
			}
			if err := send(resp); err != nil {
				return err
			}
		}
	}
	return nil
}

// --- MessageService ---

type fakeMessageServer struct {
	corev1.UnimplementedMessageServiceServer
	f *FakeCore
}

// replayed returns a copy of the stored answer for a keyed call, or nil. A call without a key has
// no entry.
func (f *FakeCore) replayed(instance string, key *string) proto.Message {
	if key == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if got, ok := f.replays[replayIndex{instance, *key}]; ok {
		return proto.Clone(got)
	}
	return nil
}

// remember stores the answer of a keyed call. The caller stores only successes.
func (f *FakeCore) remember(instance string, key *string, answer proto.Message) {
	if key == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.replays == nil {
		f.replays = map[replayIndex]proto.Message{}
	}
	f.replays[replayIndex{instance, *key}] = proto.Clone(answer)
}

func (s *fakeMessageServer) SendPlainText(ctx context.Context, req *corev1.SendPlainTextRequest) (*corev1.SendPlainTextResponse, error) {
	if err := s.f.begin(ctx, MethodSendPlainText, req); err != nil {
		return nil, err
	}
	name := req.GetInstanceName()
	if got := s.f.replayed(name, req.IdempotencyKey); got != nil {
		resp := got.(*corev1.SendPlainTextResponse)
		resp.Replayed = true
		return resp, nil
	}
	resp, err := s.send(ctx, req)
	if err == nil {
		s.f.remember(name, req.IdempotencyKey, resp)
	}
	return resp, err
}

func (s *fakeMessageServer) send(ctx context.Context, req *corev1.SendPlainTextRequest) (*corev1.SendPlainTextResponse, error) {
	f := s.f
	name := req.GetInstanceName()
	f.mu.Lock()
	fn := f.onSend
	if fn != nil {
		f.mu.Unlock()
		return fn(ctx, req)
	}
	defer f.mu.Unlock()
	in, ok := f.instances[name]
	if !ok {
		return nil, status.Error(codes.NotFound, "Instance "+name+" not found.")
	}
	switch {
	case !in.Enabled, in.Status == corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH, in.Status == corev1.InstanceStatus_INSTANCE_STATUS_ERROR:
		return nil, status.Error(codes.FailedPrecondition, "Instance "+name+" needs to authenticate again.")
	case in.Status == corev1.InstanceStatus_INSTANCE_STATUS_DISCONNECTED, in.Status == corev1.InstanceStatus_INSTANCE_STATUS_CONNECTING:
		return nil, status.Error(codes.Unavailable, "Instance "+name+" is not connected right now.")
	}
	f.sendCount++
	return &corev1.SendPlainTextResponse{MessageId: fmt.Sprintf("fake-%d", f.sendCount)}, nil
}

func (s *fakeMessageServer) newJob(name string, total int) (*corev1.BulkJob, error) {
	in, ok := s.f.instances[name]
	if !ok {
		return nil, status.Error(codes.NotFound, "Instance "+name+" not found.")
	}
	if !in.Enabled || in.Status == corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH {
		return nil, status.Error(codes.FailedPrecondition, "Instance "+name+" needs to authenticate again.")
	}
	job := &corev1.BulkJob{
		Id:            uuid.NewString(),
		InstanceName:  name,
		Status:        corev1.BulkJobStatus_BULK_JOB_STATUS_QUEUED,
		TotalMessages: int32(total),
		CreatedAt:     timestamppb.Now(),
	}
	s.f.jobs[job.Id] = proto.Clone(job).(*corev1.BulkJob)
	return job, nil
}

// keyedJob creates a job, or replays the one stored for the key.
func (s *fakeMessageServer) keyedJob(name string, key *string, total int) (*corev1.BulkJob, error) {
	if got := s.f.replayed(name, key); got != nil {
		job := got.(*corev1.BulkJob)
		job.Replayed = true
		return job, nil
	}
	s.f.mu.Lock()
	job, err := s.newJob(name, total)
	s.f.mu.Unlock()
	if err == nil {
		s.f.remember(name, key, job)
	}
	return job, err
}

func (s *fakeMessageServer) SendBulkSameMessage(ctx context.Context, req *corev1.SendBulkSameMessageRequest) (*corev1.BulkJob, error) {
	if err := s.f.begin(ctx, MethodSendBulkSameMessage, req); err != nil {
		return nil, err
	}
	return s.keyedJob(req.GetInstanceName(), req.IdempotencyKey, len(req.GetRecipients()))
}

func (s *fakeMessageServer) SendBulkCustomMessages(ctx context.Context, req *corev1.SendBulkCustomMessagesRequest) (*corev1.BulkJob, error) {
	if err := s.f.begin(ctx, MethodSendBulkCustomMessages, req); err != nil {
		return nil, err
	}
	return s.keyedJob(req.GetInstanceName(), req.IdempotencyKey, len(req.GetMessages()))
}

func (s *fakeMessageServer) GetBulkJob(ctx context.Context, req *corev1.GetBulkJobRequest) (*corev1.BulkJob, error) {
	if err := s.f.begin(ctx, MethodGetBulkJob, req); err != nil {
		return nil, err
	}
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	j, ok := s.f.jobs[req.GetJobId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "Bulk job "+req.GetJobId()+" not found.")
	}
	return proto.Clone(j).(*corev1.BulkJob), nil
}

// GetBulkJobs answers like the core: 1 to 50 ids, a missing id left out, the jobs in no defined
// order; this fake answers in reverse order so that tests catch a caller that keeps the core order.
func (s *fakeMessageServer) GetBulkJobs(ctx context.Context, req *corev1.GetBulkJobsRequest) (*corev1.GetBulkJobsResponse, error) {
	if err := s.f.begin(ctx, MethodGetBulkJobs, req); err != nil {
		return nil, err
	}
	if n := len(req.GetJobIds()); n < 1 || n > 50 {
		return nil, status.Error(codes.InvalidArgument, "Send 1 to 50 job ids.")
	}
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	resp := &corev1.GetBulkJobsResponse{}
	ids := req.GetJobIds()
	for i := len(ids) - 1; i >= 0; i-- {
		if j, ok := s.f.jobs[ids[i]]; ok {
			resp.Jobs = append(resp.Jobs, proto.Clone(j).(*corev1.BulkJob))
		}
	}
	return resp, nil
}

func (s *fakeMessageServer) CancelBulkJob(ctx context.Context, req *corev1.CancelBulkJobRequest) (*corev1.BulkJob, error) {
	if err := s.f.begin(ctx, MethodCancelBulkJob, req); err != nil {
		return nil, err
	}
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	j, ok := s.f.jobs[req.GetJobId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "Bulk job "+req.GetJobId()+" not found.")
	}
	if j.Status == corev1.BulkJobStatus_BULK_JOB_STATUS_QUEUED || j.Status == corev1.BulkJobStatus_BULK_JOB_STATUS_RUNNING {
		j.Status = corev1.BulkJobStatus_BULK_JOB_STATUS_CANCELLED
		j.FinishedAt = timestamppb.Now()
	}
	return proto.Clone(j).(*corev1.BulkJob), nil
}

// --- EventService ---

type fakeEventServer struct {
	corev1.UnimplementedEventServiceServer
	f *FakeCore
}

func toSet(l []string) map[string]bool {
	m := map[string]bool{}
	for _, v := range l {
		m[v] = true
	}
	return m
}

// Subscribe registers the stream, then sends the headers, as the real core does. A client that
// sees the headers can rely on the next PublishConnection reaching it.
func (s *fakeEventServer) Subscribe(req *corev1.SubscribeRequest, stream grpc.ServerStreamingServer[corev1.Event]) error {
	f := s.f
	if err := f.begin(stream.Context(), MethodSubscribe, req); err != nil {
		return err
	}
	sub := &fakeSub{
		names: toSet(req.InstanceNames), types: toSet(req.EventTypes),
		events: make(chan *corev1.Event, 1024), end: make(chan struct{}),
	}
	f.mu.Lock()
	f.subs[sub] = struct{}{}
	noHeaders := f.noHeaders
	version := f.version
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		delete(f.subs, sub)
		f.mu.Unlock()
	}()
	if !noHeaders {
		header := metadata.MD{}
		if version != "" {
			header.Set("calyx-version", version)
		}
		if err := stream.SendHeader(header); err != nil {
			return err
		}
	}
	for {
		select {
		case ev := <-sub.events:
			if err := stream.Send(ev); err != nil {
				return err
			}
		case <-sub.end:
			return status.Error(codes.Unavailable, "The core stops.")
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

// SetSubscribeHeaders turns the headers of Subscribe on (the default) or off. Without headers a
// client waits for them until its own timer ends.
func (f *FakeCore) SetSubscribeHeaders(on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noHeaders = !on
}

// SetVersion sets the version that the next Subscribe streams send in the header "calyx-version",
// as the real core does. Empty (the default) sends no version, as an older core does.
func (f *FakeCore) SetVersion(v string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.version = v
}

// PublishConnection sends a "connection.update" event of the instance to every matching stream.
func (f *FakeCore) PublishConnection(name string, st corev1.InstanceStatus) {
	ev := &corev1.Event{
		Id: uuid.NewString(), InstanceName: name, Type: "connection.update",
		PayloadJson: fmt.Sprintf(`{"status":%q}`, strings.TrimPrefix(st.String(), "INSTANCE_STATUS_")),
		OccurredAt:  timestamppb.Now(),
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for sub := range f.subs {
		if (len(sub.names) == 0 || sub.names[name]) && (len(sub.types) == 0 || sub.types[ev.Type]) {
			sub.events <- proto.Clone(ev).(*corev1.Event)
		}
	}
}

// EndSubscriptions ends every open Subscribe stream with UNAVAILABLE "The core stops.".
func (f *FakeCore) EndSubscriptions() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for sub := range f.subs {
		select {
		case <-sub.end:
		default:
			close(sub.end)
		}
	}
}

func (f *FakeCore) SubscribeRequests() []*corev1.SubscribeRequest {
	return typedRequests[*corev1.SubscribeRequest](f, MethodSubscribe)
}

// ReasonError builds the status the core sends for a domain error: the code, the message and an
// ErrorInfo detail with the reason.
func ReasonError(code codes.Code, reason corev1.ErrorReason, meta map[string]string, message string) error {
	st, err := status.New(code, message).WithDetails(&errdetails.ErrorInfo{
		Reason: reason.String(), Domain: "calyx.core.v1", Metadata: meta,
	})
	if err != nil {
		panic(err) // only a nil or unregistered detail fails
	}
	return st.Err()
}

// ReasonRetryError is ReasonError with a google.rpc.RetryInfo detail after the ErrorInfo detail,
// as the core sends for a full queue or a send in progress.
func ReasonRetryError(code codes.Code, reason corev1.ErrorReason, wait time.Duration, message string) error {
	st, err := status.New(code, message).WithDetails(
		&errdetails.ErrorInfo{Reason: reason.String(), Domain: "calyx.core.v1"},
		&errdetails.RetryInfo{RetryDelay: durationpb.New(wait)},
	)
	if err != nil {
		panic(err) // only a nil or unregistered detail fails
	}
	return st.Err()
}
