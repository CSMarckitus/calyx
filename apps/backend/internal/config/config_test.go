package config_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"calyx/backend/internal/config"
)

// token is a valid CORE_TOKEN: 32 characters.
const token = "0123456789abcdef0123456789abcdef"

func env(m map[string]string) func(string) string {
	return func(name string) string { return m[name] }
}

func valid() map[string]string {
	return map[string]string{
		"DATABASE_URL": "postgres://calyx:pw@localhost:5432/calyx",
		"PUBLIC_URL":   "https://api.calyx.test",
		"CORE_ADDRESS": "core:50051",
		"CORE_TOKEN":   token,
		"SECRET_KEY":   "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
	}
}

func with(kv ...string) map[string]string {
	m := valid()
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func lines(err error) []string {
	if err == nil {
		return nil
	}
	return strings.Split(err.Error(), "\n")
}

func TestLoadAppliesTheDefaults(t *testing.T) {
	c, err := config.Load(env(valid()))
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name      string
		got, want any
	}{
		{"HTTPHost", c.HTTPHost, "127.0.0.1"},
		{"HTTPPort", c.HTTPPort, 8080},
		{"CoreID", c.CoreID, "main"},
		{"SMTP.Port", c.SMTP.Port, 587},
		{"WhatsAppWebDefault", c.WhatsAppWebDefault, true},
		{"AlertGrace", c.AlertGrace, 5 * time.Minute},
		{"StateCheckInterval", c.StateCheckInterval, 30 * time.Second},
		{"PairingTimeout", c.PairingTimeout, 3 * time.Minute},
		{"TrustProxy", c.TrustProxy, false},
		{"MCPEnabled", c.MCPEnabled, true},
		{"LogFormat", c.LogFormat, "text"},
		{"CoreMetaWebhookURL", c.CoreMetaWebhookURL, ""},
		{"OperatorEmail", c.OperatorEmail, ""},
		{"MailConfigured", c.MailConfigured(), false},
		{"len(SecretKey)", len(c.SecretKey), 32},
		{"PublicURL", c.PublicURL.String(), "https://api.calyx.test"},
	}
	for _, ch := range checks {
		if ch.got != ch.want {
			t.Errorf("%s = %v, want %v", ch.name, ch.got, ch.want)
		}
	}
}

func TestLoadReadsEveryVariable(t *testing.T) {
	c, err := config.Load(env(with(
		"HTTP_HOST", "0.0.0.0", "HTTP_PORT", "9090", "PUBLIC_URL", "https://api.calyx.test/",
		"CORE_META_VERIFY_TOKEN", "vt",
		"SMTP_HOST", "smtp.calyx.test", "SMTP_PORT", "2525", "SMTP_USERNAME", "u", "SMTP_PASSWORD", "p",
		"SMTP_FROM", "calyx@calyx.test", "OPERATOR_EMAIL", "ops@calyx.test",
		"WHATSAPP_WEB_DEFAULT", "blocked", "TRUST_PROXY", "true", "MCP_ENABLED", "false", "LOG_FORMAT", "json",
	)))
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name      string
		got, want any
	}{
		{"DatabaseURL", c.DatabaseURL, "postgres://calyx:pw@localhost:5432/calyx"},
		{"HTTPHost", c.HTTPHost, "0.0.0.0"},
		{"HTTPPort", c.HTTPPort, 9090},
		{"PublicURL", c.PublicURL.String(), "https://api.calyx.test"},
		{"CoreID", c.CoreID, "main"},
		{"CoreAddress", c.CoreAddress, "core:50051"},
		{"CoreToken", c.CoreToken, token},
		{"CoreMetaWebhookURL", c.CoreMetaWebhookURL, "https://api.calyx.test/meta/webhook"},
		{"CoreMetaVerifyToken", c.CoreMetaVerifyToken, "vt"},
		{"SMTP.Host", c.SMTP.Host, "smtp.calyx.test"},
		{"SMTP.Port", c.SMTP.Port, 2525},
		{"SMTP.Username", c.SMTP.Username, "u"},
		{"SMTP.Password", c.SMTP.Password, "p"},
		{"SMTP.From", c.SMTP.From, "calyx@calyx.test"},
		{"OperatorEmail", c.OperatorEmail, "ops@calyx.test"},
		{"WhatsAppWebDefault", c.WhatsAppWebDefault, false},
		{"AlertGrace", c.AlertGrace, 5 * time.Minute},
		{"StateCheckInterval", c.StateCheckInterval, 30 * time.Second},
		{"PairingTimeout", c.PairingTimeout, 3 * time.Minute},
		{"TrustProxy", c.TrustProxy, true},
		{"MCPEnabled", c.MCPEnabled, false},
		{"LogFormat", c.LogFormat, "json"},
		{"MailConfigured", c.MailConfigured(), true},
	}
	for _, ch := range checks {
		if ch.got != ch.want {
			t.Errorf("%s = %v, want %v", ch.name, ch.got, ch.want)
		}
	}
}

func TestEachRequiredVariableIsReported(t *testing.T) {
	for _, name := range []string{"DATABASE_URL", "PUBLIC_URL", "CORE_ADDRESS", "CORE_TOKEN", "SECRET_KEY"} {
		m := valid()
		delete(m, name)
		_, err := config.Load(env(m))
		got := lines(err)
		if len(got) != 1 || !strings.HasPrefix(got[0], name+": is required.") {
			t.Errorf("without %s: %q", name, got)
		}
		m[name] = "" // an empty value counts as missing
		_, err = config.Load(env(m))
		if got := lines(err); len(got) != 1 || !strings.HasPrefix(got[0], name+": is required.") {
			t.Errorf("empty %s: %q", name, got)
		}
	}
}

func TestAllProblemsAreReportedTogether(t *testing.T) {
	_, err := config.Load(env(nil))
	got := lines(err)
	want := []string{
		"DATABASE_URL: is required.", "PUBLIC_URL: is required.", "CORE_ADDRESS: is required.",
		"CORE_TOKEN: is required.", "SECRET_KEY: is required.",
	}
	if len(got) != len(want) {
		t.Fatalf("got %q", got)
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestInvalidValues(t *testing.T) {
	b64of16 := "AAAAAAAAAAAAAAAAAAAAAA=="
	smtp := []string{"SMTP_HOST", "smtp.calyx.test", "SMTP_FROM", "a@calyx.test", "OPERATOR_EMAIL", "o@calyx.test"}
	tests := []struct {
		name string
		kv   []string
	}{
		{"DATABASE_URL", []string{"DATABASE_URL", "mysql://u:p@h/db"}},
		{"DATABASE_URL", []string{"DATABASE_URL", "::not a url"}},
		{"PUBLIC_URL", []string{"PUBLIC_URL", "api.calyx.test"}},
		{"PUBLIC_URL", []string{"PUBLIC_URL", "ftp://api.calyx.test"}},
		{"PUBLIC_URL", []string{"PUBLIC_URL", "https://api.calyx.test/app"}},
		{"PUBLIC_URL", []string{"PUBLIC_URL", "https://api.calyx.test?x=1"}},
		{"PUBLIC_URL", []string{"PUBLIC_URL", "https://"}},
		{"PUBLIC_URL", []string{"PUBLIC_URL", "https://u:p@api.calyx.test"}},
		{"HTTP_PORT", []string{"HTTP_PORT", "0"}},
		{"HTTP_PORT", []string{"HTTP_PORT", "65536"}},
		{"HTTP_PORT", []string{"HTTP_PORT", "abc"}},
		{"SMTP_PORT", append([]string{"SMTP_PORT", "abc"}, smtp...)},
		{"CORE_ADDRESS", []string{"CORE_ADDRESS", "core"}},
		{"CORE_ADDRESS", []string{"CORE_ADDRESS", "core:abc"}},
		{"CORE_ADDRESS", []string{"CORE_ADDRESS", ":50051"}},
		{"SECRET_KEY", []string{"SECRET_KEY", b64of16}},
		{"SECRET_KEY", []string{"SECRET_KEY", "%%%"}},
		{"WHATSAPP_WEB_DEFAULT", []string{"WHATSAPP_WEB_DEFAULT", "yes"}},
		{"TRUST_PROXY", []string{"TRUST_PROXY", "1"}},
		{"MCP_ENABLED", []string{"MCP_ENABLED", "1"}},
		{"LOG_FORMAT", []string{"LOG_FORMAT", "xml"}},
		{"SMTP_PASSWORD", append([]string{"SMTP_USERNAME", "u"}, smtp...)},
	}
	for _, tc := range tests {
		_, err := config.Load(env(with(tc.kv...)))
		got := lines(err)
		if len(got) != 1 || !strings.HasPrefix(got[0], tc.name+": ") {
			t.Errorf("%v: got %q, want one line starting with %q", tc.kv, got, tc.name)
		}
	}
}

func TestMailNeedsFromAndOperatorEmail(t *testing.T) {
	_, err := config.Load(env(with("SMTP_HOST", "smtp.calyx.test")))
	got := lines(err)
	if len(got) != 2 || got[0] != "SMTP_FROM: is required with SMTP_HOST." ||
		got[1] != "OPERATOR_EMAIL: is required with SMTP_HOST." {
		t.Errorf("got %q", got)
	}
	_, err = config.Load(env(with("SMTP_HOST", "h", "SMTP_FROM", "not-an-email", "OPERATOR_EMAIL", "o@calyx.test")))
	if got := lines(err); len(got) != 1 || got[0] != "SMTP_FROM: must be an email address." {
		t.Errorf("got %q", got)
	}
	_, err = config.Load(env(with("SMTP_HOST", "h", "SMTP_FROM", "a@calyx.test", "OPERATOR_EMAIL", "nope")))
	if got := lines(err); len(got) != 1 || got[0] != "OPERATOR_EMAIL: must be an email address." {
		t.Errorf("got %q", got)
	}
}

func TestErrorsNeverContainSecretValues(t *testing.T) {
	_, err := config.Load(env(map[string]string{
		"DATABASE_URL":  "mysql://u:db-secret-1@h/x",
		"PUBLIC_URL":    "https://api.calyx.test",
		"CORE_ADDRESS":  "core:50051",
		"CORE_TOKEN":    "tok-secret-2",
		"SECRET_KEY":    "c2VjcmV0LTM=",
		"SMTP_USERNAME": "u", "SMTP_PASSWORD": "smtp-secret-4", "SMTP_HOST": "h",
	}))
	if err == nil {
		t.Fatal("want an error")
	}
	for _, s := range []string{"db-secret-1", "tok-secret-2", "c2VjcmV0LTM=", "smtp-secret-4"} {
		if strings.Contains(err.Error(), s) {
			t.Errorf("error contains %q: %s", s, err)
		}
	}
}

func TestNewLogger(t *testing.T) {
	var buf bytes.Buffer
	config.NewLogger("json", &buf).Info("hello", "k", "v")
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("not JSON: %q", buf.String())
	}
	if m["msg"] != "hello" || m["k"] != "v" {
		t.Errorf("got %v", m)
	}
	if strings.Count(strings.TrimSpace(buf.String()), "\n") != 0 {
		t.Errorf("more than one line: %q", buf.String())
	}
	buf.Reset()
	config.NewLogger("text", &buf).Info("hello")
	if !strings.Contains(buf.String(), "msg=hello") {
		t.Errorf("got %q", buf.String())
	}
}

func TestMetaWebhookURLIsDerivedFromPublicURL(t *testing.T) {
	c, err := config.Load(env(with("PUBLIC_URL", "https://api.calyx.test/", "CORE_META_VERIFY_TOKEN", "vt")))
	if err != nil {
		t.Fatal(err)
	}
	if c.CoreMetaWebhookURL != "https://api.calyx.test/meta/webhook" || c.CoreMetaVerifyToken != "vt" {
		t.Errorf("got %q %q", c.CoreMetaWebhookURL, c.CoreMetaVerifyToken)
	}
	c, err = config.Load(env(valid()))
	if err != nil || c.CoreMetaWebhookURL != "" {
		t.Errorf("without a verify token: %q %v", c.CoreMetaWebhookURL, err)
	}
}

func TestRemovedVariablesAreIgnored(t *testing.T) {
	c, err := config.Load(env(with("CORE_ID", "x", "ALERT_GRACE", "1s", "STATE_CHECK_INTERVAL", "1s",
		"PAIRING_TIMEOUT", "1s", "CORE_META_WEBHOOK_URL", "https://x.calyx.test")))
	if err != nil {
		t.Fatal(err)
	}
	if c.CoreID != "main" || c.AlertGrace != 5*time.Minute || c.StateCheckInterval != 30*time.Second ||
		c.PairingTimeout != 3*time.Minute || c.CoreMetaWebhookURL != "" {
		t.Errorf("got %+v", c)
	}
}

func TestPlaceholdersAreRefused(t *testing.T) {
	tests := []struct {
		name string
		kv   []string
	}{
		{"PUBLIC_URL", []string{"PUBLIC_URL", "https://api.example.com"}},
		{"PUBLIC_URL", []string{"PUBLIC_URL", "https://example.org"}},
		{"CORE_ADDRESS", []string{"CORE_ADDRESS", "core.example.net:50051"}},
		{"DATABASE_URL", []string{"DATABASE_URL", "postgres://calyx:pw@db.example.com:5432/calyx"}},
		{"DATABASE_URL", []string{"DATABASE_URL", "postgres://calyx:changeme@localhost:5432/calyx"}},
		{"CORE_TOKEN", []string{"CORE_TOKEN", "changeme"}},
		{"CORE_TOKEN", []string{"CORE_TOKEN", "change-me"}},
		{"SECRET_KEY", []string{"SECRET_KEY", "replace-me"}},
		{"SMTP_HOST", []string{"SMTP_HOST", "smtp.example.com", "SMTP_FROM", "a@calyx.test", "OPERATOR_EMAIL", "o@calyx.test"}},
		{"SMTP_FROM", []string{"SMTP_HOST", "smtp.calyx.test", "SMTP_FROM", "Calyx <a@example.com>", "OPERATOR_EMAIL", "o@calyx.test"}},
		{"OPERATOR_EMAIL", []string{"SMTP_HOST", "smtp.calyx.test", "SMTP_FROM", "a@calyx.test", "OPERATOR_EMAIL", "admin@example.org"}},
		{"SMTP_USERNAME", []string{"SMTP_HOST", "smtp.calyx.test", "SMTP_FROM", "a@calyx.test", "OPERATOR_EMAIL", "o@calyx.test", "SMTP_USERNAME", "user@example.net", "SMTP_PASSWORD", "p"}},
		{"SMTP_PASSWORD", []string{"SMTP_HOST", "smtp.calyx.test", "SMTP_FROM", "a@calyx.test", "OPERATOR_EMAIL", "o@calyx.test", "SMTP_USERNAME", "u", "SMTP_PASSWORD", "changeme"}},
		{"CORE_META_VERIFY_TOKEN", []string{"CORE_META_VERIFY_TOKEN", "replace-me"}},
	}
	for _, tc := range tests {
		_, err := config.Load(env(with(tc.kv...)))
		got := lines(err)
		if len(got) != 1 || !strings.HasPrefix(got[0], tc.name+": ") || !strings.Contains(got[0], "placeholder") {
			t.Errorf("%v: got %q, want a placeholder line for %s", tc.kv, got, tc.name)
		}
	}
}

func TestSubdomainsOfRealDomainsAreNotPlaceholders(t *testing.T) {
	if _, err := config.Load(env(with("PUBLIC_URL", "https://api.notexample.com"))); err != nil {
		t.Errorf("notexample.com: %v", err)
	}
}

func TestShortTokenIsRefused(t *testing.T) {
	_, err := config.Load(env(with("CORE_TOKEN", "0123456789abcdef0123456789abcde")))
	if got := lines(err); len(got) != 1 || !strings.HasPrefix(got[0], "CORE_TOKEN: ") || !strings.Contains(got[0], "32") {
		t.Errorf("got %q", got)
	}
}

func TestPlainHTTPOnlyForLoopbackHosts(t *testing.T) {
	for _, u := range []string{"http://localhost:8080", "http://127.0.0.1:8080", "http://[::1]:8080"} {
		if _, err := config.Load(env(with("PUBLIC_URL", u))); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	for _, u := range []string{"http://api.calyx.test", "http://192.168.1.5:8080"} {
		_, err := config.Load(env(with("PUBLIC_URL", u)))
		if got := lines(err); len(got) != 1 || !strings.HasPrefix(got[0], "PUBLIC_URL: ") || !strings.Contains(got[0], "https") {
			t.Errorf("%s: got %q", u, got)
		}
	}
}

func TestSMTPGroupIsSetTogether(t *testing.T) {
	// SMTP_FROM or SMTP_USERNAME without SMTP_HOST.
	for _, kv := range [][]string{{"SMTP_FROM", "a@calyx.test"}, {"SMTP_USERNAME", "u", "SMTP_PASSWORD", "p"}, {"OPERATOR_EMAIL", "o@calyx.test"}} {
		_, err := config.Load(env(with(kv...)))
		got := lines(err)
		if len(got) != len(kv)/2 {
			t.Errorf("%v: got %q", kv, got)
		}
		for _, l := range got {
			if !strings.HasSuffix(l, "set it together with SMTP_HOST.") {
				t.Errorf("%v: got %q", kv, got)
			}
		}
	}
}

func TestEveryLineSaysHowToFix(t *testing.T) {
	_, err := config.Load(env(nil))
	for _, l := range lines(err) {
		if !strings.HasSuffix(l, ".") || len(strings.Fields(l)) < 4 {
			t.Errorf("short line %q", l)
		}
	}
}

func TestMCPEnabledAcceptsTrueExplicitly(t *testing.T) {
	c, err := config.Load(env(with("MCP_ENABLED", "true")))
	if err != nil {
		t.Fatal(err)
	}
	if !c.MCPEnabled {
		t.Error("MCPEnabled: got false, want true")
	}
}
