// Package config reads the configuration of the backend from environment variables.
package config

import (
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultHTTPHost = "127.0.0.1"
	DefaultHTTPPort = 8080
	// CoreID names the one core of v1 in the database.
	CoreID          = "main"
	DefaultSMTPPort = 587
	// MetaWebhookPath is where Traefik routes the Meta webhook of the core on the public domain.
	MetaWebhookPath = "/meta/webhook"
	// minTokenLength is the shortest CORE_TOKEN.
	minTokenLength = 32
)

// The timings are constants, not variables of the environment. They are strings only so the
// end-to-end test can build a binary with short values: -ldflags "-X <package>.alertGrace=2s".
var (
	stateCheckInterval = "30s"
	alertGrace         = "5m"
	pairingTimeout     = "3m"
)

type Config struct {
	DatabaseURL         string
	HTTPHost            string // default "127.0.0.1"
	HTTPPort            int    // default 8080
	PublicURL           *url.URL
	CoreID              string // always "main"
	CoreAddress         string
	CoreToken           string
	CoreMetaWebhookURL  string // PublicURL + MetaWebhookPath; "" without CoreMetaVerifyToken
	CoreMetaVerifyToken string
	SecretKey           []byte // decoded, exactly 32 bytes
	SMTP                SMTP
	OperatorEmail       string
	WhatsAppWebDefault  bool          // true = "allowed" (default)
	AlertGrace          time.Duration // constant 5m
	StateCheckInterval  time.Duration // constant 30s
	PairingTimeout      time.Duration // constant 3m
	TrustProxy          bool
	MCPEnabled          bool   // default true
	LogFormat           string // "text" (default) or "json"
}

type SMTP struct {
	Host               string
	Port               int // default 587
	Username, Password string
	From               string
}

func (c Config) MailConfigured() bool { return c.SMTP.Host != "" }

// Load reads the configuration. It reports every problem in one error, one line for each.
// A line names the variable and never contains its value.
func Load(getenv func(string) string) (Config, error) {
	var problems []string
	bad := func(name, sentence string) { problems = append(problems, name+": "+sentence) }
	// value returns the variable. It reports a missing or placeholder value and returns "" then.
	value := func(name string, required bool) string {
		v := getenv(name)
		switch {
		case v == "" && required:
			bad(name, "is required. Set it in the environment (see .env.example).")
			return ""
		case isPlaceholder(v):
			bad(name, "has a placeholder value from .env.example. Set a real value.")
			return ""
		}
		return v
	}

	var c Config

	c.DatabaseURL = value("DATABASE_URL", true)
	if c.DatabaseURL != "" {
		u, err := url.Parse(c.DatabaseURL)
		switch {
		case err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql"):
			bad("DATABASE_URL", "must be a postgres:// URL.")
		case u.User != nil && passwordIsPlaceholder(u):
			bad("DATABASE_URL", "has a placeholder password from .env.example. Set a real password.")
		}
	}

	c.HTTPHost = orDefault(getenv("HTTP_HOST"), DefaultHTTPHost)
	c.HTTPPort = DefaultHTTPPort
	if v := getenv("HTTP_PORT"); v != "" {
		if p, ok := parsePort(v); ok {
			c.HTTPPort = p
		} else {
			bad("HTTP_PORT", "must be a port number from 1 to 65535.")
		}
	}

	if v := value("PUBLIC_URL", true); v != "" {
		if u, ok := parseOrigin(v); !ok {
			bad("PUBLIC_URL", "must be an origin such as https://api.calyx.test.")
		} else if u.Scheme == "http" && !isLoopback(u.Hostname()) {
			bad("PUBLIC_URL", "must use https. Plain http is only for localhost, 127.0.0.1 and ::1.")
		} else {
			c.PublicURL = u
		}
	}

	c.CoreID = CoreID

	if v := value("CORE_ADDRESS", true); v != "" {
		c.CoreAddress = v
		if host, port, err := net.SplitHostPort(v); err != nil || host == "" {
			bad("CORE_ADDRESS", "must be host:port.")
		} else if _, ok := parsePort(port); !ok {
			bad("CORE_ADDRESS", "must be host:port.")
		}
	}

	c.CoreToken = value("CORE_TOKEN", true)
	if c.CoreToken != "" && len(c.CoreToken) < minTokenLength {
		bad("CORE_TOKEN", "must have at least 32 characters. Make one: openssl rand -hex 32")
	}

	c.CoreMetaVerifyToken = value("CORE_META_VERIFY_TOKEN", false)
	if c.CoreMetaVerifyToken != "" && c.PublicURL != nil {
		c.CoreMetaWebhookURL = c.PublicURL.String() + MetaWebhookPath
	}

	if v := value("SECRET_KEY", true); v != "" {
		if key, err := base64.StdEncoding.DecodeString(v); err != nil || len(key) != 32 {
			bad("SECRET_KEY", "must be 32 bytes in standard base64. Make one: openssl rand -base64 32")
		} else {
			c.SecretKey = key
		}
	}

	c.SMTP.Host = value("SMTP_HOST", false)
	c.SMTP.Port = DefaultSMTPPort
	if v := getenv("SMTP_PORT"); v != "" {
		if p, ok := parsePort(v); ok {
			c.SMTP.Port = p
		} else {
			bad("SMTP_PORT", "must be a port number from 1 to 65535.")
		}
	}
	c.SMTP.Username = value("SMTP_USERNAME", false)
	c.SMTP.Password = value("SMTP_PASSWORD", false)
	if c.SMTP.Username != "" && c.SMTP.Password == "" && getenv("SMTP_PASSWORD") == "" {
		bad("SMTP_PASSWORD", "is required with SMTP_USERNAME.")
	}
	c.SMTP.From = value("SMTP_FROM", false)
	c.OperatorEmail = value("OPERATOR_EMAIL", false)
	smtpSet := c.SMTP.Host != ""
	for _, g := range []struct{ name, v, raw string }{
		{"SMTP_FROM", c.SMTP.From, getenv("SMTP_FROM")},
		{"OPERATOR_EMAIL", c.OperatorEmail, getenv("OPERATOR_EMAIL")},
	} {
		switch {
		case g.raw == "" && smtpSet:
			bad(g.name, "is required with SMTP_HOST.")
		case g.raw != "" && !smtpSet && getenv("SMTP_HOST") == "":
			bad(g.name, "set it together with SMTP_HOST.")
		case g.v != "":
			if _, err := mail.ParseAddress(g.v); err != nil {
				bad(g.name, "must be an email address.")
			}
		}
	}
	if getenv("SMTP_HOST") == "" {
		for _, name := range []string{"SMTP_USERNAME", "SMTP_PASSWORD"} {
			if getenv(name) != "" {
				bad(name, "set it together with SMTP_HOST.")
			}
		}
	}

	c.WhatsAppWebDefault = true
	switch getenv("WHATSAPP_WEB_DEFAULT") {
	case "", "allowed":
	case "blocked":
		c.WhatsAppWebDefault = false
	default:
		bad("WHATSAPP_WEB_DEFAULT", "must be allowed or blocked.")
	}

	c.AlertGrace = timing(alertGrace, 5*time.Minute)
	c.StateCheckInterval = timing(stateCheckInterval, 30*time.Second)
	c.PairingTimeout = timing(pairingTimeout, 3*time.Minute)

	switch getenv("TRUST_PROXY") {
	case "", "false":
	case "true":
		c.TrustProxy = true
	default:
		bad("TRUST_PROXY", "must be true or false.")
	}

	switch getenv("MCP_ENABLED") {
	case "", "true":
		c.MCPEnabled = true
	case "false":
	default:
		bad("MCP_ENABLED", "must be true or false.")
	}

	c.LogFormat = "text"
	switch v := getenv("LOG_FORMAT"); v {
	case "":
	case "text", "json":
		c.LogFormat = v
	default:
		bad("LOG_FORMAT", "must be text or json.")
	}

	if len(problems) > 0 {
		return Config{}, errors.New(strings.Join(problems, "\n"))
	}
	return c, nil
}

// NewLogger returns the logger of every command: JSON for "json", text for anything else.
func NewLogger(format string, w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if format == "json" {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func parsePort(s string) (int, bool) {
	p, err := strconv.Atoi(s)
	if err != nil || p < 1 || p > 65535 {
		return 0, false
	}
	return p, true
}

func parseOrigin(s string) (*url.URL, bool) {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, false
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" ||
		(u.Path != "" && u.Path != "/") {
		return nil, false
	}
	u.Path, u.RawPath = "", ""
	return u, true
}

// timing parses a timing constant. A bad build-time value falls back to def.
func timing(v string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	return def
}

var placeholderValues = map[string]bool{"changeme": true, "change-me": true, "replace-me": true}

// isPlaceholder reports a value that .env.example leaves to replace: a literal such as changeme,
// or a host or email under example.com, example.org or example.net.
func isPlaceholder(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	if placeholderValues[v] {
		return true
	}
	if i := strings.Index(v, "://"); i >= 0 {
		v = v[i+3:]
	}
	if i := strings.IndexAny(v, "/?#"); i >= 0 {
		v = v[:i]
	}
	if i := strings.LastIndex(v, "@"); i >= 0 {
		v = v[i+1:]
	}
	v = strings.TrimSuffix(v, ">") // "Name <a@example.com>"
	if host, _, err := net.SplitHostPort(v); err == nil {
		v = host
	}
	for _, d := range []string{"example.com", "example.org", "example.net"} {
		if v == d || strings.HasSuffix(v, "."+d) {
			return true
		}
	}
	return false
}

func passwordIsPlaceholder(u *url.URL) bool {
	pw, _ := u.User.Password()
	return placeholderValues[strings.ToLower(pw)]
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
