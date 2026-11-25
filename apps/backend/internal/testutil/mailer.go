package testutil

import (
	"context"
	"sync"
	"testing"
	"time"

	"calyx/backend/internal/mail"
)

// FakeMailer is a mail.Sender that records the mails in memory.
type FakeMailer struct {
	mu       sync.Mutex
	sent     []mail.Message
	attempts int
	err      error
}

func NewFakeMailer() *FakeMailer { return &FakeMailer{} }

// Send counts the attempt. It returns the forced error, or records m.
func (f *FakeMailer) Send(ctx context.Context, m mail.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, m)
	return nil
}

// Sent returns a copy of the recorded mails.
func (f *FakeMailer) Sent() []mail.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mail.Message(nil), f.sent...)
}

// Attempts returns every Send call, failed ones included.
func (f *FakeMailer) Attempts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts
}

// FailWith makes Send return err. A nil err makes it succeed again.
func (f *FakeMailer) FailWith(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// WaitForAttempts blocks until Send was called at least n times (5 seconds at most) and returns
// the number of attempts. It fails the test on a timeout. Use it when the code under test sends
// the mail in a goroutine.
func (f *FakeMailer) WaitForAttempts(t *testing.T, n int) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got := f.Attempts(); got >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("waited for %d mail attempts, got %d", n, f.Attempts())
		}
		time.Sleep(5 * time.Millisecond)
	}
}
