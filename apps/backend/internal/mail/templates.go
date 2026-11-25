package mail

import (
	"embed"
	"fmt"
	"strings"
	"sync"
	"text/template"
	"time"
)

//go:embed templates/*.txt
var templateFiles embed.FS

// TemplateData holds every variable of every template. A template uses the ones it needs. In an
// alert, Link is the page of the instance in the frontend and APILink is its API address.
type TemplateData struct {
	To, OrganizationName, ClientName, InstanceName, Status, Link, APILink, PublicURL, CoreID string
	Since                                                                                    time.Time
}

var (
	parseOnce sync.Once
	parsed    map[string]*template.Template
	parseErr  error
)

func load() {
	parsed = map[string]*template.Template{}
	entries, err := templateFiles.ReadDir("templates")
	if err != nil {
		parseErr = err
		return
	}
	for _, e := range entries {
		file := e.Name()
		text, err := templateFiles.ReadFile("templates/" + file)
		if err != nil {
			parseErr = err
			return
		}
		name := strings.TrimSuffix(file, ".txt")
		t, err := template.New(name).Option("missingkey=error").Parse(string(text))
		if err != nil {
			parseErr = fmt.Errorf("template %s: %w", name, err)
			return
		}
		parsed[name] = t
	}
}

// Render fills the template called name (the file name without ".txt"). The first line of the
// file is "Subject: <text>", then an empty line, then the body.
func Render(name string, data TemplateData) (Message, error) {
	parseOnce.Do(load)
	if parseErr != nil {
		return Message{}, parseErr
	}
	t, ok := parsed[name]
	if !ok {
		return Message{}, fmt.Errorf("unknown mail template %q", name)
	}
	var sb strings.Builder
	if err := t.Execute(&sb, data); err != nil {
		return Message{}, fmt.Errorf("template %s: %w", name, err)
	}
	head, body, found := strings.Cut(sb.String(), "\n\n")
	if !found || !strings.HasPrefix(head, "Subject: ") {
		return Message{}, fmt.Errorf("template %s: no Subject line", name)
	}
	subject := strings.TrimRight(strings.TrimPrefix(head, "Subject: "), " ")
	return Message{To: data.To, Subject: subject, Body: body}, nil
}

// TemplateNames lists the names of the embedded templates (the file names without ".txt").
var TemplateNames = []string{
	"invitation_owner",
	"invitation_client",
	"password_reset",
	"alert_needs_auth_whatsapp_web",
	"alert_needs_auth_meta",
	"alert_error",
	"alert_disconnected",
	"alert_missing",
	"alert_core_unreachable",
}
