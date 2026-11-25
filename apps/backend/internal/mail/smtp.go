package mail

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net"
	netmail "net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"calyx/backend/internal/config"
)

// sendTimeout is the longest time that one Send can take. Tests change it through SetSendTimeout.
var sendTimeout = 15 * time.Second

type smtpSender struct {
	cfg config.SMTP
	tls *tls.Config
}

// NewSMTPSender returns the SMTP sender. It uses STARTTLS when the server offers it. Implicit TLS
// (port 465) is not supported. A nil tlsConfig means the system roots.
func NewSMTPSender(cfg config.SMTP, tlsConfig *tls.Config) Sender {
	return &smtpSender{cfg: cfg, tls: tlsConfig}
}

// FailureClass names the kind of a failed send for a log line: "timeout" (a context or network
// timeout), "refused" (another network error, or an SMTP answer of 400 or more) or "other". The
// first class that matches wins. It never returns the text of the error, which can hold an address.
func FailureClass(err error) string {
	var netErr net.Error
	isNet := errors.As(err, &netErr)
	var smtpErr *textproto.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), isNet && netErr.Timeout():
		return "timeout"
	case isNet, errors.As(err, &smtpErr) && smtpErr.Code >= 400:
		return "refused"
	}
	return "other"
}

func hasLineBreak(s string) bool { return strings.ContainsAny(s, "\r\n") }

func (s *smtpSender) Send(ctx context.Context, m Message) error {
	if s.cfg.Host == "" {
		return ErrNotConfigured
	}
	if err := s.send(ctx, m); err != nil {
		return fmt.Errorf("send mail: %w", err)
	}
	return nil
}

func (s *smtpSender) send(ctx context.Context, m Message) error {
	if hasLineBreak(m.To) || hasLineBreak(m.Subject) || hasLineBreak(s.cfg.From) {
		return fmt.Errorf("a header value contains a line break")
	}
	from, err := netmail.ParseAddress(s.cfg.From)
	if err != nil {
		return fmt.Errorf("invalid From address: %w", err)
	}
	to, err := netmail.ParseAddress(m.To)
	if err != nil {
		return fmt.Errorf("invalid To address: %w", err)
	}
	c, closeConn, err := s.connect(ctx, from.Address)
	if err != nil {
		return err
	}
	defer closeConn()
	if err := c.Mail(from.Address); err != nil {
		return err
	}
	if err := c.Rcpt(to.Address); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(buildMessage(s.cfg.From, from.Address, m)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	// The server accepted the mail at Close, so a failed QUIT is not a failed send.
	_ = c.Quit()
	return nil
}

// connect opens the connection and runs EHLO, STARTTLS and AUTH. The caller calls closeConn.
func (s *smtpSender) connect(ctx context.Context, fromAddr string) (c *smtp.Client, closeConn func(), err error) {
	port := s.cfg.Port
	if port == 0 {
		port = config.DefaultSMTPPort
	}
	deadline := time.Now().Add(sendTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn, err := (&net.Dialer{Deadline: deadline}).DialContext(ctx, "tcp", net.JoinHostPort(s.cfg.Host, strconv.Itoa(port)))
	if err != nil {
		return nil, nil, err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	closeConn = func() { stop(); conn.Close() }
	fail := func(err error) (*smtp.Client, func(), error) { closeConn(); return nil, nil, err }
	if err := conn.SetDeadline(deadline); err != nil {
		return fail(err)
	}
	c, err = smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return fail(err)
	}
	if i := strings.LastIndex(fromAddr, "@"); i >= 0 {
		if err := c.Hello(fromAddr[i+1:]); err != nil {
			return fail(err)
		}
	}
	if ok, _ := c.Extension("STARTTLS"); ok {
		tc := &tls.Config{}
		if s.tls != nil {
			tc = s.tls.Clone()
		}
		if tc.ServerName == "" {
			tc.ServerName = s.cfg.Host
		}
		tc.MinVersion = tls.VersionTLS12
		if err := c.StartTLS(tc); err != nil {
			return fail(err)
		}
	}
	if s.cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return fail(err)
		}
	}
	return c, closeConn, nil
}

// CheckSMTP connects, runs EHLO, STARTTLS and AUTH, and sends nothing. It returns
// ErrNotConfigured when there is no SMTP host.
func CheckSMTP(ctx context.Context, cfg config.SMTP, tlsConfig *tls.Config) error {
	if cfg.Host == "" {
		return ErrNotConfigured
	}
	s := &smtpSender{cfg: cfg, tls: tlsConfig}
	from, err := netmail.ParseAddress(cfg.From)
	if err != nil {
		return fmt.Errorf("invalid From address: %w", err)
	}
	c, closeConn, err := s.connect(ctx, from.Address)
	if err != nil {
		return err
	}
	defer closeConn()
	_ = c.Quit()
	return nil
}

// buildMessage writes the headers and the body. The dot writer of smtp.Client.Data turns "\n"
// into "\r\n" and doubles a leading dot, so the body goes in as it is.
func buildMessage(fromHeader, fromAddr string, m Message) []byte {
	domain := "localhost"
	if i := strings.LastIndex(fromAddr, "@"); i >= 0 {
		domain = fromAddr[i+1:]
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	var sb strings.Builder
	sb.WriteString("From: " + fromHeader + "\r\n")
	sb.WriteString("To: " + m.To + "\r\n")
	sb.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", m.Subject) + "\r\n")
	sb.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	sb.WriteString("Message-ID: <" + hex.EncodeToString(id) + "@" + domain + ">\r\n")
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	sb.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(m.Body)
	return []byte(sb.String())
}
