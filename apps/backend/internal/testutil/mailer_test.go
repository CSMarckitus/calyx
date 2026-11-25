package testutil

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"calyx/backend/internal/mail"
)

func TestFakeMailerRecords(t *testing.T) {
	f := NewFakeMailer()
	m1 := mail.Message{To: "a@x.example", Subject: "s1", Body: "b1"}
	m2 := mail.Message{To: "b@x.example", Subject: "s2", Body: "b2"}
	_ = f.Send(context.Background(), m1)
	_ = f.Send(context.Background(), m2)
	if !reflect.DeepEqual(f.Sent(), []mail.Message{m1, m2}) {
		t.Fatalf("Sent = %v", f.Sent())
	}
	if f.Attempts() != 2 {
		t.Fatalf("Attempts = %d", f.Attempts())
	}
}

func TestFakeMailerFails(t *testing.T) {
	f := NewFakeMailer()
	m := mail.Message{To: "a@x.example"}
	f.FailWith(mail.ErrNotConfigured)
	if err := f.Send(context.Background(), m); !errors.Is(err, mail.ErrNotConfigured) {
		t.Fatalf("err = %v", err)
	}
	if len(f.Sent()) != 0 || f.Attempts() != 1 {
		t.Fatalf("sent %d, attempts %d", len(f.Sent()), f.Attempts())
	}
	f.FailWith(nil)
	if err := f.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.Sent(), []mail.Message{m}) {
		t.Fatalf("Sent = %v", f.Sent())
	}
}

func TestFakeMailerWaitsForMails(t *testing.T) {
	f := NewFakeMailer()
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = f.Send(context.Background(), mail.Message{To: "a@x.example"})
	}()
	got := f.WaitForAttempts(t, 1)
	if got != 1 || len(f.Sent()) != 1 {
		t.Fatalf("attempts %d, sent %d", got, len(f.Sent()))
	}
}
