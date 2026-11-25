package mail_test

import (
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"calyx/backend/internal/mail"
)

var data = mail.TemplateData{
	To:               "to@test.example",
	OrganizationName: "ORG",
	ClientName:       "CLIENT",
	Link:             "https://calyx.test/link",
	PublicURL:        "https://calyx.test",
}

func TestTheIdentityTemplatesRender(t *testing.T) {
	for _, name := range []string{"invitation_owner", "invitation_client", "password_reset"} {
		m, err := mail.Render(name, data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if m.Subject == "" || strings.Contains(m.Subject, "\n") {
			t.Errorf("%s: subject %q", name, m.Subject)
		}
		if !strings.Contains(m.Body, "https://calyx.test/link") {
			t.Errorf("%s: no link in the body", name)
		}
		if strings.Contains(m.Subject+m.Body, "<no value>") {
			t.Errorf("%s: <no value> in the text", name)
		}
		if m.To != "to@test.example" {
			t.Errorf("%s: To = %q", name, m.To)
		}
		switch name {
		case "invitation_owner":
			if !strings.Contains(m.Subject, "ORG") {
				t.Errorf("subject %q", m.Subject)
			}
		case "invitation_client":
			if !strings.Contains(m.Subject, "CLIENT") {
				t.Errorf("subject %q", m.Subject)
			}
		case "password_reset":
			if !strings.Contains(m.Body, "1 hour") {
				t.Errorf("body without \"1 hour\"")
			}
		}
	}
}

func TestAnUnknownTemplateIsAnError(t *testing.T) {
	if _, err := mail.Render("nope", data); err == nil {
		t.Fatal("no error")
	}
}

var allData = mail.TemplateData{
	To:               "to@test.example",
	OrganizationName: "ORG",
	ClientName:       "CLIENT",
	InstanceName:     "INSTANCE",
	Status:           "NEEDS_AUTH",
	Link:             "https://calyx.test/instances/ID",
	APILink:          "https://calyx.test/api/v1/instances/ID",
	PublicURL:        "https://calyx.test",
	CoreID:           "main",
	Since:            time.Date(2026, 11, 18, 10, 30, 0, 0, time.UTC),
}

func TestTemplateListMatchesTheFiles(t *testing.T) {
	entries, err := os.ReadDir("templates")
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range entries {
		files = append(files, strings.TrimSuffix(e.Name(), ".txt"))
	}
	names := append([]string(nil), mail.TemplateNames...)
	sort.Strings(files)
	sort.Strings(names)
	if len(names) != 9 || !slices.Equal(files, names) {
		t.Fatalf("files %v, names %v", files, names)
	}
}

func TestEveryTemplateRendersWithAllVariables(t *testing.T) {
	for _, name := range mail.TemplateNames {
		m, err := mail.Render(name, allData)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if m.Subject == "" || strings.Contains(m.Subject, "\n") || m.Body == "" {
			t.Errorf("%s: subject %q, body %q", name, m.Subject, m.Body)
		}
		if strings.Contains(m.Subject+m.Body, "<no value>") {
			t.Errorf("%s: <no value> in the text", name)
		}
		if m.To != "to@test.example" {
			t.Errorf("%s: To = %q", name, m.To)
		}
	}
}

func TestEachTemplateUsesItsVariables(t *testing.T) {
	cases := map[string][]string{
		"invitation_owner":              {"ORG", "https://calyx.test/instances/ID", "7 days"},
		"invitation_client":             {"ORG", "CLIENT", "https://calyx.test/instances/ID"},
		"password_reset":                {"https://calyx.test/instances/ID", "1 hour"},
		"alert_needs_auth_whatsapp_web": {"INSTANCE", "CLIENT", "ORG", "2026-11-18 10:30 UTC"},
		"alert_needs_auth_meta":         {"INSTANCE", "CLIENT", "meta_credentials"},
		"alert_error":                   {"INSTANCE", "CLIENT"},
		"alert_disconnected":            {"INSTANCE", "CLIENT"},
		"alert_missing":                 {"INSTANCE", "CLIENT"},
		"alert_core_unreachable":        {"main", "https://calyx.test", "2026-11-18 10:30 UTC"},
	}
	for name, subs := range cases {
		m, err := mail.Render(name, allData)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, s := range subs {
			if !strings.Contains(m.Subject+m.Body, s) {
				t.Errorf("%s: no %q", name, s)
			}
		}
	}
}

func TestNoTemplatePromisesThatBansCannotHappen(t *testing.T) {
	for _, name := range mail.TemplateNames {
		m, err := mail.Render(name, allData)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ToLower(m.Subject + m.Body)
		for _, bad := range []string{"cannot be banned", "will not be banned", "safe from"} {
			if strings.Contains(text, bad) {
				t.Errorf("%s contains %q", name, bad)
			}
		}
	}
}

// An alert mail opens the instance in the frontend first, then gives the operation of the API.
func TestAlertTemplatesLinkToTheFrontendAndTheAPI(t *testing.T) {
	const open = "Open https://calyx.test/instances/ID in Calyx."
	api := map[string]string{
		"alert_needs_auth_whatsapp_web": `With the API: send POST https://calyx.test/api/v1/instances/ID/pair with {"pairing":"qr"} or {"pairing":"code","phone":"<number with country code>"}, then follow GET https://calyx.test/api/v1/instances/ID/pairing`,
		"alert_needs_auth_meta":         `With the API: send PATCH https://calyx.test/api/v1/instances/ID with {"meta_credentials":{"phone_number_id":"...","access_token":"...","app_secret":"..."}}`,
		"alert_error":                   "With the API: DELETE https://calyx.test/api/v1/instances/ID",
		"alert_disconnected":            "With the API: GET https://calyx.test/api/v1/instances/ID",
		"alert_missing":                 "With the API: DELETE https://calyx.test/api/v1/instances/ID",
	}
	for name, line := range api {
		m, err := mail.Render(name, allData)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		i, j := strings.Index(m.Body, open), strings.Index(m.Body, line+"\n")
		if i < 0 || j < 0 || i > j {
			t.Errorf("%s: want %q, then the line %q:\n%s", name, open, line, m.Body)
		}
		if strings.Count(m.Body, "With the API: ") != 1 || strings.Contains(m.Body, "when it is available") {
			t.Errorf("%s:\n%s", name, m.Body)
		}
	}
}
