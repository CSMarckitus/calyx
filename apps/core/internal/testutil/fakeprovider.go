package testutil

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"time"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/engine"
	"calyx/core/internal/providers/meta"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

type SentMessage struct {
	Recipient, Text string
	At              time.Time
}

// FakeProvider stands in for WhatsApp in tests. It is used from the outbox goroutine and from the
// test at the same time, so every field is behind the mutex.
type FakeProvider struct {
	mu          sync.Mutex
	name        string
	pacing      engine.PacingPolicy
	status      corev1.InstanceStatus
	hooks       engine.ProviderHooks
	sent        []SentMessage
	failFor     map[string]bool
	unavailable map[string]bool
	beforeSend  func(recipient string)
	beforeType  func(ctx context.Context)
	pair        func(ctx context.Context, method engine.PairingMethod, onCode func(string)) error
	connectErr  error
	beforeConn  func(ctx context.Context)
	beforeDest  func()
	counter     int
	connects    int
	destroys    int
	logouts     int
	connections *ConnectionCounter
	open        bool // counted as an open connection
	closed      bool // Destroy or Logout was called
}

// ConnectionCounter counts the open connections of every instance name across all the fakes
// built for it, to check that one instance never has two connections at once.
type ConnectionCounter struct {
	mu              sync.Mutex
	open            map[string]int
	max             map[string]int
	sendsAfterClose int
}

func NewConnectionCounter() *ConnectionCounter {
	return &ConnectionCounter{open: map[string]int{}, max: map[string]int{}}
}

// Max returns the highest number of connections name ever had open at the same time.
func (c *ConnectionCounter) Max(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.max[name]
}

// Open returns how many connections name has open right now.
func (c *ConnectionCounter) Open(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.open[name]
}

// SendsAfterClose returns how many SendText calls reached a provider after its Destroy or Logout.
func (c *ConnectionCounter) SendsAfterClose() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sendsAfterClose
}

// CountConnections makes this fake report its connection to c.
func (f *FakeProvider) CountConnections(c *ConnectionCounter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connections = c
}

// opened and closedConnection are called with f.mu held.
func (f *FakeProvider) opened() {
	if f.connections == nil || f.open {
		return
	}
	f.open = true
	c := f.connections
	c.mu.Lock()
	c.open[f.name]++
	c.max[f.name] = max(c.max[f.name], c.open[f.name])
	c.mu.Unlock()
}

func (f *FakeProvider) closedConnection() {
	f.closed = true
	if f.connections == nil || !f.open {
		return
	}
	f.open = false
	c := f.connections
	c.mu.Lock()
	c.open[f.name]--
	c.mu.Unlock()
}

func NewFakeProvider(name string) *FakeProvider {
	return &FakeProvider{
		name: name,
		pacing: engine.PacingPolicy{
			MinGapMs:         30,
			MaxPendingDirect: 3,
			Delay:            engine.Rule{Fallback: engine.Range{Min: 20, Max: 40}, Min: 10},
		},
		status:      corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED,
		failFor:     map[string]bool{},
		unavailable: map[string]bool{},
	}
}

// Sent returns a copy of the messages sent so far, in order.
func (f *FakeProvider) Sent() []SentMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]SentMessage(nil), f.sent...)
}

// FailFor makes every SendText to recipient return a plain error.
func (f *FakeProvider) FailFor(recipient string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failFor[recipient] = true
}

// UnavailableFor makes the next SendText to recipient return Unavailable, only once.
func (f *FakeProvider) UnavailableFor(recipient string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unavailable[recipient] = true
}

// BeforeSend runs fn at the start of every SendText, outside the lock, so a test can act while
// the outbox is busy sending.
func (f *FakeProvider) BeforeSend(fn func(recipient string)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.beforeSend = fn
}

// BeforeTyping runs fn at the start of every ShowTyping, outside the lock, with the context of
// ShowTyping. A test can hold the outbox while it types.
func (f *FakeProvider) BeforeTyping(fn func(ctx context.Context)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.beforeType = fn
}

// BeforeConnect runs fn at the start of every Connect, outside the lock, with the context of
// Connect. A test can act while the instance is being loaded, or hold it until the context ends.
func (f *FakeProvider) BeforeConnect(fn func(ctx context.Context)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.beforeConn = fn
}

// BeforeDestroy runs fn at the start of Destroy, outside the lock, so a test can act while the
// instance is being closed.
func (f *FakeProvider) BeforeDestroy(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.beforeDest = fn
}

// SetHooks is what a provider factory does with the hooks the orchestrator gives it.
func (f *FakeProvider) SetHooks(h engine.ProviderHooks) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hooks = h
}

// ForceStatus changes the status and reports it through the hooks, like a real provider.
func (f *FakeProvider) ForceStatus(s corev1.InstanceStatus) {
	f.mu.Lock()
	changed := f.status != s
	f.status = s
	onStatus := f.hooks.OnStatus
	f.mu.Unlock()
	if changed && onStatus != nil {
		onStatus(s)
	}
}

// Emit reports a provider event through the hooks.
func (f *FakeProvider) Emit(eventType string, payload any) {
	f.mu.Lock()
	onEvent := f.hooks.OnEvent
	f.mu.Unlock()
	if onEvent != nil {
		onEvent(eventType, payload)
	}
}

// SetPair replaces the pairing. By default Pair sends the code "CODE-1" and succeeds.
func (f *FakeProvider) SetPair(fn func(ctx context.Context, method engine.PairingMethod, onCode func(string)) error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pair = fn
}

// FailConnect makes every Connect return err.
func (f *FakeProvider) FailConnect(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connectErr = err
}

func (f *FakeProvider) SetPacing(p engine.PacingPolicy) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pacing = p
}

// Counts returns how many times Connect, Destroy and Logout were called.
func (f *FakeProvider) Counts() (connects, destroys, logouts int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connects, f.destroys, f.logouts
}

func (f *FakeProvider) InstanceName() string { return f.name }

func (f *FakeProvider) Pacing() engine.PacingPolicy {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pacing
}

func (f *FakeProvider) Status() corev1.InstanceStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *FakeProvider) Connect(ctx context.Context) error {
	f.mu.Lock()
	before := f.beforeConn
	f.mu.Unlock()
	if before != nil {
		before(ctx)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connects++
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.connectErr != nil {
		return f.connectErr
	}
	f.opened()
	return nil
}

func (f *FakeProvider) Pair(ctx context.Context, method engine.PairingMethod, onCode func(string)) error {
	f.mu.Lock()
	pair := f.pair
	f.opened() // pairing opens the connection the instance then keeps
	f.mu.Unlock()
	if pair != nil {
		return pair(ctx, method, onCode)
	}
	onCode("CODE-1")
	return nil
}

var phoneNumber = regexp.MustCompile(`^\d{7,15}$`)

// NormalizeRecipient is the recipient option of the orchestrator in tests. It calls meta directly,
// not the normalize package, because that package imports whatsapp, whose tests import testutil.
func NormalizeRecipient(provider, to string) (string, error) {
	if provider == "META_CLOUD" {
		return meta.NormalizeRecipient(to)
	}
	return (&FakeProvider{}).NormalizeRecipient(to)
}

func (f *FakeProvider) NormalizeRecipient(to string) (string, error) {
	if !phoneNumber.MatchString(to) {
		return "", calyxerr.New(codes.InvalidArgument, "Invalid recipient %q.", to)
	}
	return to, nil
}

func (f *FakeProvider) SendText(ctx context.Context, recipient, text string) (string, error) {
	f.mu.Lock()
	hook := f.beforeSend
	f.mu.Unlock()
	if hook != nil {
		hook(recipient)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed && f.connections != nil {
		f.connections.mu.Lock()
		f.connections.sendsAfterClose++
		f.connections.mu.Unlock()
	}
	if f.unavailable[recipient] {
		delete(f.unavailable, recipient)
		return "", calyxerr.New(codes.Unavailable, "fake: connection lost")
	}
	if f.failFor[recipient] {
		return "", errors.New("fake: send failed")
	}
	f.counter++
	f.sent = append(f.sent, SentMessage{Recipient: recipient, Text: text, At: time.Now()})
	return fmt.Sprintf("fake-%d", f.counter), nil
}

func (f *FakeProvider) ShowTyping(ctx context.Context, recipient string, d time.Duration) error {
	f.mu.Lock()
	hook := f.beforeType
	f.mu.Unlock()
	if hook != nil {
		hook(ctx)
	}
	return nil
}

func (f *FakeProvider) SetOnline(ctx context.Context, online bool) error { return nil }

func (f *FakeProvider) Destroy(ctx context.Context) error {
	f.mu.Lock()
	before := f.beforeDest
	f.mu.Unlock()
	if before != nil {
		before()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.destroys++
	f.closedConnection()
	return nil
}

func (f *FakeProvider) Logout(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logouts++
	f.closedConnection()
	return nil
}

var (
	_ engine.Provider = (*FakeProvider)(nil)
	_ engine.Pairer   = (*FakeProvider)(nil)
)
