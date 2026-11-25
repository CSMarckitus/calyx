package testutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"
)

// FakeSMTPOptions sets the behaviour of a FakeSMTP.
type FakeSMTPOptions struct {
	StartTLS   bool // offer STARTTLS in the EHLO answer
	NoGreeting bool // accept connections and never send the 220 greeting (timeout tests)
}

// ReceivedMail is one mail that the fake accepted.
type ReceivedMail struct {
	From         string   // the MAIL FROM address
	To           []string // every RCPT TO address
	Data         string   // the message after DATA, dot-unstuffed, lines joined with "\r\n"
	TLS          bool     // STARTTLS was done before DATA
	AuthUser     string   // from AUTH PLAIN, "" when no AUTH
	AuthPassword string
	Hello        string // the name of the last EHLO or HELO
}

// FakeSMTP is a small SMTP server on 127.0.0.1 for tests.
type FakeSMTP struct {
	opts FakeSMTPOptions
	ln   net.Listener
	cert tls.Certificate
	pool *x509.CertPool

	mu       sync.Mutex
	mails    []ReceivedMail
	fail     bool
	attempts int
	conns    []net.Conn
	closed   bool
}

// NewFakeSMTP starts a fake server on a free port. t.Cleanup closes it.
func NewFakeSMTP(t *testing.T, opts FakeSMTPOptions) *FakeSMTP {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("NewFakeSMTP: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "fake.smtp.test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},

		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("NewFakeSMTP: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("NewFakeSMTP: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("NewFakeSMTP: %v", err)
	}
	s := &FakeSMTP{
		opts: opts, ln: ln, pool: pool,
		cert: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key},
	}
	go s.accept()
	t.Cleanup(s.close)
	return s
}

func (s *FakeSMTP) close() {
	s.mu.Lock()
	s.closed = true
	conns := s.conns
	s.mu.Unlock()
	s.ln.Close()
	for _, c := range conns {
		c.Close()
	}
}

func (s *FakeSMTP) Host() string { return "127.0.0.1" }

func (s *FakeSMTP) Port() int { return s.ln.Addr().(*net.TCPAddr).Port }

// ClientTLS returns a client configuration that trusts the certificate of the fake.
func (s *FakeSMTP) ClientTLS() *tls.Config {
	return &tls.Config{RootCAs: s.pool, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12}
}

// SetFail makes every MAIL FROM answer "451 4.3.0 Forced failure".
func (s *FakeSMTP) SetFail(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = on
}

// Messages returns a copy of the accepted mails.
func (s *FakeSMTP) Messages() []ReceivedMail {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ReceivedMail, len(s.mails))
	for i, m := range s.mails {
		m.To = append([]string(nil), m.To...)
		out[i] = m
	}
	return out
}

// MailAttempts returns the number of MAIL FROM commands received, failed ones included.
func (s *FakeSMTP) MailAttempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts
}

func (s *FakeSMTP) accept() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			c.Close()
			return
		}
		s.conns = append(s.conns, c)
		s.mu.Unlock()
		go s.serve(c)
	}
}

func (s *FakeSMTP) serve(raw net.Conn) {
	defer raw.Close()
	tc := textproto.NewConn(raw)
	inTLS := false
	if s.opts.NoGreeting {
		// Hold the connection open until the client or the test closes it.
		buf := make([]byte, 1)
		_, _ = raw.Read(buf)
		return
	}
	if tc.PrintfLine("%s", "220 fake.smtp.test ESMTP") != nil {
		return
	}
	// A failed write makes the next ReadLine fail, so the loop ends there.
	say := func(line string) { _ = tc.PrintfLine("%s", line) }
	var cur ReceivedMail
	var authUser, authPass, hello string
	reset := func() { cur = ReceivedMail{} }
	for {
		line, err := tc.ReadLine()
		if err != nil {
			return
		}
		verb, rest, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "EHLO":
			hello = rest
			say("250-fake.smtp.test")
			if s.opts.StartTLS && !inTLS {
				say("250-STARTTLS")
			}
			say("250-AUTH PLAIN")
			say("250 8BITMIME")
		case "HELO":
			hello = rest
			say("250 fake.smtp.test")
		case "STARTTLS":
			if !s.opts.StartTLS || inTLS {
				say("502 5.5.1 Not supported")
				continue
			}
			say("220 2.0.0 Ready to start TLS")
			conn := tls.Server(raw, &tls.Config{Certificates: []tls.Certificate{s.cert}, MinVersion: tls.VersionTLS12})
			if err := conn.Handshake(); err != nil {
				return
			}
			raw = conn
			tc = textproto.NewConn(conn)
			inTLS = true
			reset()
			authUser, authPass = "", ""
		case "AUTH":
			mech, initial, _ := strings.Cut(rest, " ")
			if !strings.EqualFold(mech, "PLAIN") || initial == "" {
				say("504 5.5.4 Not supported")
				continue
			}
			dec, err := base64.StdEncoding.DecodeString(initial)
			if err != nil {
				say("501 5.5.2 Bad encoding")
				continue
			}
			parts := strings.Split(string(dec), "\x00")
			if len(parts) == 3 {
				authUser, authPass = parts[1], parts[2]
			}
			say("235 2.7.0 Authentication successful")
		case "MAIL":
			s.mu.Lock()
			s.attempts++
			fail := s.fail
			s.mu.Unlock()
			if fail {
				say("451 4.3.0 Forced failure")
				continue
			}
			reset()
			cur.From = angle(rest)
			say("250 2.1.0 OK")
		case "RCPT":
			cur.To = append(cur.To, angle(rest))
			say("250 2.1.5 OK")
		case "DATA":
			say("354 End data with <CR><LF>.<CR><LF>")
			lines, err := tc.ReadDotLines()
			if err != nil {
				return
			}
			cur.Data = strings.Join(lines, "\r\n")
			cur.TLS = inTLS
			cur.AuthUser, cur.AuthPassword = authUser, authPass
			cur.Hello = hello
			s.mu.Lock()
			s.mails = append(s.mails, cur)
			s.mu.Unlock()
			reset()
			say("250 2.0.0 OK")
		case "RSET", "NOOP":
			if strings.EqualFold(verb, "RSET") {
				reset()
			}
			say("250 2.0.0 OK")
		case "QUIT":
			say("221 2.0.0 Bye")
			return
		default:
			say("502 5.5.2 Command not recognized")
		}
	}
}

// angle returns the address inside "FROM:<addr>" or "TO:<addr>".
func angle(s string) string {
	i := strings.Index(s, "<")
	j := strings.LastIndex(s, ">")
	if i < 0 || j < i {
		return strings.TrimSpace(s)
	}
	return s[i+1 : j]
}
