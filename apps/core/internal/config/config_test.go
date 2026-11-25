package config_test

import (
	"strings"
	"testing"

	"calyx/core/internal/config"
)

const token = "0123456789abcdef0123456789abcdef"

func env(kv ...string) func(string) string {
	m := map[string]string{"GRPC_PORT": "50051", "GRPC_AUTH_TOKEN": token}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(name string) string { return m[name] }
}

func lines(err error) []string {
	if err == nil {
		return nil
	}
	return strings.Split(err.Error(), "\n")
}

func TestLoadAppliesTheDefaults(t *testing.T) {
	c, err := config.Load(env())
	if err != nil {
		t.Fatal(err)
	}
	if c.GRPCPort != 50051 || c.GRPCHost != "127.0.0.1" || c.AuthToken != token ||
		c.DatabaseURL != "file:./data/core.sqlite" || c.RedisURL != "" || c.LogFormat != "text" ||
		c.MetaWebhookPort != "" || c.MetaGraphVersion != "" {
		t.Errorf("got %+v", c)
	}
}

func TestLoadReadsEveryVariable(t *testing.T) {
	c, err := config.Load(env("GRPC_HOST", "0.0.0.0", "DATABASE_URL", "file:/data/x.sqlite",
		"REDIS_URL", "redis://cache:6379", "LOG_FORMAT", "json", "META_WEBHOOK_PORT", "8081",
		"META_WEBHOOK_VERIFY_TOKEN", "vt", "META_GRAPH_API_VERSION", "v24.0"))
	if err != nil {
		t.Fatal(err)
	}
	if c.GRPCHost != "0.0.0.0" || c.DatabaseURL != "file:/data/x.sqlite" || c.RedisURL != "redis://cache:6379" ||
		c.LogFormat != "json" || c.MetaWebhookPort != "8081" || c.MetaVerifyToken != "vt" || c.MetaGraphVersion != "v24.0" {
		t.Errorf("got %+v", c)
	}
}

func TestRequiredVariables(t *testing.T) {
	for _, name := range []string{"GRPC_PORT", "GRPC_AUTH_TOKEN"} {
		get := func(n string) string {
			if n == name {
				return ""
			}
			return env()(n)
		}
		_, err := config.Load(get)
		if got := lines(err); len(got) != 1 || !strings.HasPrefix(got[0], name+": is required.") {
			t.Errorf("without %s: %q", name, got)
		}
	}
}

func TestAllProblemsAreReportedTogether(t *testing.T) {
	_, err := config.Load(func(string) string { return "" })
	got := lines(err)
	if len(got) != 2 || !strings.HasPrefix(got[0], "GRPC_PORT: ") || !strings.HasPrefix(got[1], "GRPC_AUTH_TOKEN: ") {
		t.Errorf("got %q", got)
	}
}

func TestInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		kv   []string
	}{
		{"GRPC_PORT", []string{"GRPC_PORT", "0"}},
		{"GRPC_PORT", []string{"GRPC_PORT", "70000"}},
		{"GRPC_PORT", []string{"GRPC_PORT", "abc"}},
		{"GRPC_AUTH_TOKEN", []string{"GRPC_AUTH_TOKEN", "0123456789abcdef0123456789abcde"}},
		{"GRPC_AUTH_TOKEN", []string{"GRPC_AUTH_TOKEN", "changeme"}},
		{"GRPC_AUTH_TOKEN", []string{"GRPC_AUTH_TOKEN", "replace-me"}},
		{"LOG_FORMAT", []string{"LOG_FORMAT", "xml"}},
		{"REDIS_URL", []string{"REDIS_URL", "cache:6379"}},
		{"REDIS_URL", []string{"REDIS_URL", "redis://redis.example.com:6379"}},
		{"META_WEBHOOK_PORT", []string{"META_WEBHOOK_PORT", "abc", "META_WEBHOOK_VERIFY_TOKEN", "vt"}},
		{"META_WEBHOOK_VERIFY_TOKEN", []string{"META_WEBHOOK_PORT", "8081", "META_WEBHOOK_VERIFY_TOKEN", "change-me"}},
		{"META_WEBHOOK_VERIFY_TOKEN", []string{"META_WEBHOOK_PORT", "8081"}},
		{"META_WEBHOOK_PORT", []string{"META_WEBHOOK_VERIFY_TOKEN", "vt"}},
		{"META_GRAPH_API_VERSION", []string{"META_GRAPH_API_VERSION", "23"}},
		{"GRPC_HOST", []string{"GRPC_HOST", "core.example.com"}},
	}
	for _, tc := range tests {
		_, err := config.Load(env(tc.kv...))
		got := lines(err)
		if len(got) != 1 || !strings.HasPrefix(got[0], tc.name+": ") {
			t.Errorf("%v: got %q, want one line for %s", tc.kv, got, tc.name)
		}
	}
}

func TestErrorsNeverContainSecretValues(t *testing.T) {
	_, err := config.Load(env("GRPC_AUTH_TOKEN", "short-secret-1", "META_WEBHOOK_PORT", "8081", "META_WEBHOOK_VERIFY_TOKEN", "changeme"))
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), "short-secret-1") {
		t.Errorf("error contains a secret: %s", err)
	}
}

func TestLoadWorker(t *testing.T) {
	get := func(kv ...string) func(string) string {
		m := map[string]string{"REDIS_URL": "redis://cache:6379"}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(n string) string { return m[n] }
	}
	c, err := config.LoadWorker(get("LOG_FORMAT", "json"))
	if err != nil || c.RedisURL != "redis://cache:6379" || c.LogFormat != "json" || c.DatabaseURL != "file:./data/core.sqlite" {
		t.Errorf("got %+v %v", c, err)
	}
	_, err = config.LoadWorker(get("REDIS_URL", ""))
	if got := lines(err); len(got) != 1 || !strings.HasPrefix(got[0], "REDIS_URL: is required.") {
		t.Errorf("got %q", got)
	}
	_, err = config.LoadWorker(get("LOG_FORMAT", "xml"))
	if got := lines(err); len(got) != 1 || !strings.HasPrefix(got[0], "LOG_FORMAT: ") {
		t.Errorf("got %q", got)
	}
}
