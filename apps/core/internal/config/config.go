// Package config reads and checks the environment shared by the core and the webhook worker.
package config

import (
	"errors"
	"log/slog"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"calyx/core/internal/database"
)

const minTokenLength = 32

// Config is the checked environment of the core.
type Config struct {
	GRPCPort         int
	GRPCHost         string // default 127.0.0.1
	AuthToken        string
	DatabaseURL      string // default database.DefaultURL
	RedisURL         string // "" = no Redis
	LogFormat        string // "text" (default) or "json"
	MetaWebhookPort  string // "" = no Meta webhook endpoint
	MetaVerifyToken  string
	MetaGraphVersion string // "" = the default of the Meta provider
}

// WorkerConfig is the checked environment of the webhook worker.
type WorkerConfig struct {
	DatabaseURL string
	RedisURL    string
	LogFormat   string
}

var graphVersion = regexp.MustCompile(`^v\d+\.\d+$`)

// checker collects the problems of one Load. A line names the variable and never holds its value.
type checker struct {
	getenv   func(string) string
	problems []string
}

func (k *checker) bad(name, sentence string) { k.problems = append(k.problems, name+": "+sentence) }

// value returns the variable. It reports a missing or placeholder value and returns "" then.
func (k *checker) value(name string, required bool) string {
	v := k.getenv(name)
	switch {
	case v == "" && required:
		k.bad(name, "is required. Set it in the environment (see .env.example).")
		return ""
	case isPlaceholder(v):
		k.bad(name, "has a placeholder value from .env.example. Set a real value.")
		return ""
	}
	return v
}

func (k *checker) port(name string, required bool) (string, int) {
	v := k.value(name, required)
	if v == "" {
		return "", 0
	}
	p, err := strconv.Atoi(v)
	if err != nil || p < 1 || p > 65535 {
		k.bad(name, "must be a port number from 1 to 65535.")
		return "", 0
	}
	return v, p
}

func (k *checker) logFormat() string {
	switch v := k.getenv("LOG_FORMAT"); v {
	case "":
		return "text"
	case "text", "json":
		return v
	}
	k.bad("LOG_FORMAT", "must be text or json.")
	return "text"
}

func (k *checker) redisURL(required bool) string {
	v := k.value("REDIS_URL", required)
	if v == "" {
		return ""
	}
	if u, err := url.Parse(v); err != nil || (u.Scheme != "redis" && u.Scheme != "rediss") || u.Host == "" {
		k.bad("REDIS_URL", "must be a redis:// or rediss:// URL.")
		return ""
	}
	return v
}

func (k *checker) err() error {
	if len(k.problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(k.problems, "\n"))
}

// Load checks the environment of the core. It reports every problem in one error, one line each.
func Load(getenv func(string) string) (Config, error) {
	k := &checker{getenv: getenv}
	var c Config
	_, c.GRPCPort = k.port("GRPC_PORT", true)
	c.GRPCHost = k.value("GRPC_HOST", false)
	if c.GRPCHost == "" {
		c.GRPCHost = "127.0.0.1" // not reachable from other machines unless the operator decides so
	}
	c.AuthToken = k.value("GRPC_AUTH_TOKEN", true)
	if c.AuthToken != "" && len(c.AuthToken) < minTokenLength {
		k.bad("GRPC_AUTH_TOKEN", "must have at least 32 characters. Make one: openssl rand -hex 32")
	}
	c.DatabaseURL = databaseURL(getenv)
	c.RedisURL = k.redisURL(false)
	c.LogFormat = k.logFormat()

	c.MetaWebhookPort, _ = k.port("META_WEBHOOK_PORT", false)
	c.MetaVerifyToken = k.value("META_WEBHOOK_VERIFY_TOKEN", false)
	switch {
	case getenv("META_WEBHOOK_PORT") != "" && getenv("META_WEBHOOK_VERIFY_TOKEN") == "":
		k.bad("META_WEBHOOK_VERIFY_TOKEN", "is required with META_WEBHOOK_PORT.")
	case getenv("META_WEBHOOK_PORT") == "" && getenv("META_WEBHOOK_VERIFY_TOKEN") != "":
		k.bad("META_WEBHOOK_PORT", "is required with META_WEBHOOK_VERIFY_TOKEN.")
	}
	if v := getenv("META_GRAPH_API_VERSION"); v != "" {
		if graphVersion.MatchString(v) {
			c.MetaGraphVersion = v
		} else {
			k.bad("META_GRAPH_API_VERSION", "must look like v23.0.")
		}
	}
	if err := k.err(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// LoadWorker checks the environment of the webhook worker.
func LoadWorker(getenv func(string) string) (WorkerConfig, error) {
	k := &checker{getenv: getenv}
	c := WorkerConfig{DatabaseURL: databaseURL(getenv)}
	c.RedisURL = k.redisURL(true)
	c.LogFormat = k.logFormat()
	if err := k.err(); err != nil {
		return WorkerConfig{}, err
	}
	return c, nil
}

func databaseURL(getenv func(string) string) string {
	if v := getenv("DATABASE_URL"); v != "" {
		return v
	}
	return database.DefaultURL
}

// SetupLogging makes slog the default logger: JSON for log collectors, or readable text with
// debug messages.
func SetupLogging(format string) {
	var handler slog.Handler
	if format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	} else {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	}
	slog.SetDefault(slog.New(handler))
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
