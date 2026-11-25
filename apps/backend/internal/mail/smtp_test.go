package mail_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"calyx/backend/internal/config"
	"calyx/backend/internal/mail"
	"calyx/backend/internal/testutil"
)

func cfgFor(f *testutil.FakeSMTP) config.SMTP {
	return config.SMTP{Host: f.Host(), Port: f.Port(), From: "Calyx <calyx@test.example>"}
}

func msg() mail.Message {
	return mail.Message{To: "ana@test.example", Subject: "Hello", Body: "Line 1\nLine 2"}
}

// headersAndBody splits a received message at the first blank line.
func headersAndBody(t *testing.T, data string) (map[string]string, string) {
	t.Helper()
	head, body, ok := strings.Cut(data, "\r\n\r\n")
	if !ok {
		t.Fatalf("no blank line in %q", data)
	}
	h := map[string]string{}
	for _, l := range strings.Split(head, "\r\n") {
		k, v, _ := strings.Cut(l, ": ")
		h[k] = v
	}
	return h, body
}

func TestSendWithoutSTARTTLS(t *testing.T) {
	f := testutil.NewFakeSMTP(t, testutil.FakeSMTPOptions{})
	if err := mail.NewSMTPSender(cfgFor(f), nil).Send(context.Background(), msg()); err != nil {
		t.Fatal(err)
	}
	ms := f.Messages()
	if len(ms) != 1 {
		t.Fatalf("mails = %d", len(ms))
	}
	m := ms[0]
	if m.From != "calyx@test.example" || len(m.To) != 1 || m.To[0] != "ana@test.example" || m.TLS {
		t.Fatalf("envelope %+v", m)
	}
	h, body := headersAndBody(t, m.Data)
	want := map[string]string{
		"From": "Calyx <calyx@test.example>", "To": "ana@test.example", "Subject": "Hello",
		"MIME-Version": "1.0", "Content-Type": "text/plain; charset=utf-8", "Content-Transfer-Encoding": "8bit",
	}
	for k, v := range want {
		if h[k] != v {
			t.Errorf("header %s = %q, want %q", k, h[k], v)
		}
	}
	if h["Date"] == "" {
		t.Error("no Date header")
	}
	if !strings.HasSuffix(h["Message-ID"], "@test.example>") {
		t.Errorf("Message-ID = %q", h["Message-ID"])
	}
	if body != "Line 1\r\nLine 2" {
		t.Errorf("body = %q", body)
	}
}

func TestSendWithSTARTTLS(t *testing.T) {
	f := testutil.NewFakeSMTP(t, testutil.FakeSMTPOptions{StartTLS: true})
	if err := mail.NewSMTPSender(cfgFor(f), f.ClientTLS()).Send(context.Background(), msg()); err != nil {
		t.Fatal(err)
	}
	ms := f.Messages()
	if len(ms) != 1 || !ms[0].TLS {
		t.Fatalf("mails %+v", ms)
	}
}

func TestSTARTTLSWithAnUntrustedCertificateFails(t *testing.T) {
	f := testutil.NewFakeSMTP(t, testutil.FakeSMTPOptions{StartTLS: true})
	if err := mail.NewSMTPSender(cfgFor(f), nil).Send(context.Background(), msg()); err == nil {
		t.Fatal("no error")
	}
	if n := len(f.Messages()); n != 0 {
		t.Fatalf("mails = %d", n)
	}
}

func TestAuthAfterSTARTTLS(t *testing.T) {
	f := testutil.NewFakeSMTP(t, testutil.FakeSMTPOptions{StartTLS: true})
	cfg := cfgFor(f)
	cfg.Username, cfg.Password = "u", "p"
	if err := mail.NewSMTPSender(cfg, f.ClientTLS()).Send(context.Background(), msg()); err != nil {
		t.Fatal(err)
	}
	ms := f.Messages()
	if len(ms) != 1 || ms[0].AuthUser != "u" || ms[0].AuthPassword != "p" || !ms[0].TLS {
		t.Fatalf("mails %+v", ms)
	}
}

func TestAFailureIsReported(t *testing.T) {
	f := testutil.NewFakeSMTP(t, testutil.FakeSMTPOptions{})
	f.SetFail(true)
	err := mail.NewSMTPSender(cfgFor(f), nil).Send(context.Background(), msg())
	if err == nil || !strings.Contains(err.Error(), "451") {
		t.Fatalf("err = %v", err)
	}
	if len(f.Messages()) != 0 || f.MailAttempts() != 1 {
		t.Fatalf("mails %d attempts %d", len(f.Messages()), f.MailAttempts())
	}
}

func TestTheSendTimesOut(t *testing.T) {
	f := testutil.NewFakeSMTP(t, testutil.FakeSMTPOptions{NoGreeting: true})
	restore := mail.SetSendTimeout(200 * time.Millisecond)
	defer restore()
	start := time.Now()
	if err := mail.NewSMTPSender(cfgFor(f), nil).Send(context.Background(), msg()); err == nil {
		t.Fatal("no error")
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestACancelledContextStopsTheSend(t *testing.T) {
	f := testutil.NewFakeSMTP(t, testutil.FakeSMTPOptions{NoGreeting: true})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	if err := mail.NewSMTPSender(cfgFor(f), nil).Send(ctx, msg()); err == nil {
		t.Fatal("no error")
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestMailOffReturnsErrNotConfigured(t *testing.T) {
	err := mail.NewSMTPSender(config.SMTP{}, nil).Send(context.Background(), msg())
	if !errors.Is(err, mail.ErrNotConfigured) {
		t.Fatalf("err = %v", err)
	}
}

func TestHeaderLineBreaksAreRefused(t *testing.T) {
	f := testutil.NewFakeSMTP(t, testutil.FakeSMTPOptions{})
	s := mail.NewSMTPSender(cfgFor(f), nil)
	bad := []mail.Message{
		{To: "ana@test.example", Subject: "Hi\r\nBcc: x@test.example", Body: "x"},
		{To: "a@test.example\nb", Subject: "Hi", Body: "x"},
	}
	for _, m := range bad {
		if err := s.Send(context.Background(), m); err == nil {
			t.Errorf("no error for %+v", m)
		}
	}
	cfg := cfgFor(f)
	cfg.From = "Calyx <calyx@test.example>\r\nBcc: x@test.example"
	if err := mail.NewSMTPSender(cfg, nil).Send(context.Background(), msg()); err == nil {
		t.Error("no error for a From with a line break")
	}
	if len(f.Messages()) != 0 || f.MailAttempts() != 0 {
		t.Fatalf("mails %d attempts %d", len(f.Messages()), f.MailAttempts())
	}
}

func TestLeadingDotsSurvive(t *testing.T) {
	f := testutil.NewFakeSMTP(t, testutil.FakeSMTPOptions{})
	m := msg()
	m.Body = ".\n..x"
	if err := mail.NewSMTPSender(cfgFor(f), nil).Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	_, body := headersAndBody(t, f.Messages()[0].Data)
	if body != ".\r\n..x" {
		t.Fatalf("body = %q", body)
	}
}

func TestANonASCIISubjectIsEncoded(t *testing.T) {
	f := testutil.NewFakeSMTP(t, testutil.FakeSMTPOptions{})
	s := mail.NewSMTPSender(cfgFor(f), nil)
	m := msg()
	m.Subject = "Gimnasio Peña"
	if err := s.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	h, _ := headersAndBody(t, f.Messages()[0].Data)
	if !strings.HasPrefix(h["Subject"], "=?utf-8?q?") {
		t.Errorf("Subject = %q", h["Subject"])
	}
}

func TestEHLOUsesTheDomainOfTheFromAddress(t *testing.T) {
	f := testutil.NewFakeSMTP(t, testutil.FakeSMTPOptions{})
	if err := mail.NewSMTPSender(cfgFor(f), nil).Send(context.Background(), msg()); err != nil {
		t.Fatal(err)
	}
	if got := f.Messages()[0].Hello; got != "test.example" {
		t.Fatalf("EHLO name = %q", got)
	}
}

func TestCheckSMTPSendsNothing(t *testing.T) {
	f := testutil.NewFakeSMTP(t, testutil.FakeSMTPOptions{StartTLS: true})
	cfg := cfgFor(f)
	cfg.Username, cfg.Password = "u", "p"
	if err := mail.CheckSMTP(context.Background(), cfg, f.ClientTLS()); err != nil {
		t.Fatal(err)
	}
	if len(f.Messages()) != 0 || f.MailAttempts() != 0 {
		t.Fatalf("mails %d attempts %d", len(f.Messages()), f.MailAttempts())
	}
}

func TestCheckSMTPReportsAnUntrustedCertificate(t *testing.T) {
	f := testutil.NewFakeSMTP(t, testutil.FakeSMTPOptions{StartTLS: true})
	if err := mail.CheckSMTP(context.Background(), cfgFor(f), nil); err == nil {
		t.Fatal("no error")
	}
}

func TestCheckSMTPStopsWithTheContext(t *testing.T) {
	f := testutil.NewFakeSMTP(t, testutil.FakeSMTPOptions{NoGreeting: true})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := mail.CheckSMTP(ctx, cfgFor(f), nil); err == nil {
		t.Fatal("no error")
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestCheckSMTPWithoutAHostIsNotConfigured(t *testing.T) {
	if err := mail.CheckSMTP(context.Background(), config.SMTP{}, nil); !errors.Is(err, mail.ErrNotConfigured) {
		t.Fatalf("err = %v", err)
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return false }

func TestFailureClass(t *testing.T) {
	wrap := func(err error) error { return fmt.Errorf("send mail: %w", err) }
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"deadline", wrap(context.DeadlineExceeded), "timeout"},
		{"network timeout", wrap(&net.OpError{Op: "dial", Err: timeoutErr{}}), "timeout"},
		{"connection refused", wrap(&net.OpError{Op: "dial", Err: errors.New("connection refused")}), "refused"},
		{"smtp 451", wrap(&textproto.Error{Code: 451, Msg: "try later"}), "refused"},
		{"smtp 550", wrap(&textproto.Error{Code: 550, Msg: "no"}), "refused"},
		{"smtp 250", wrap(&textproto.Error{Code: 250, Msg: "ok"}), "other"},
		{"plain error", wrap(errors.New("invalid To address")), "other"},
		{"cancelled", wrap(context.Canceled), "other"},
		{"nil", nil, "other"},
	}
	for _, c := range cases {
		if got := mail.FailureClass(c.err); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestFailureClassOfARealFailure(t *testing.T) {
	f := testutil.NewFakeSMTP(t, testutil.FakeSMTPOptions{})
	f.SetFail(true)
	err := mail.NewSMTPSender(cfgFor(f), nil).Send(context.Background(), msg())
	if got := mail.FailureClass(err); got != "refused" {
		t.Fatalf("got %q (%v)", got, err)
	}
}
