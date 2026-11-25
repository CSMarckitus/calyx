package main

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"calyx/sdktools/internal/spec"
)

func models(t *testing.T) string {
	t.Helper()
	content, err := newModelWriter(loadSpec(t)).file()
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

// classBody returns the lines of one class of the file.
func classBody(t *testing.T, file, class string) string {
	t.Helper()
	_, after, ok := strings.Cut(file, "\nclass "+class+":\n")
	if !ok {
		t.Fatalf("models.py has no class %s", class)
	}
	body, _, _ := strings.Cut(after, "\n\n\n")
	return body
}

func TestTheClassesAreTheOnesOfTheDesign(t *testing.T) {
	want := []string{
		"Provider", "DeviceIdentity", "InstanceStatus", "InstanceState", "BulkJobStatus", "PairingFailureCode",

		"Alert", "ApiKey", "ApiKeyList", "BulkJob", "BulkJobList", "BulkMessageInput", "BulkPacingInput",
		"ClientList", "CreateInstanceResult", "Instance", "InstanceList", "Me", "MeOrganization",
		"MetaCredentialsInput", "MetaWebhook", "OrgClient", "Organization", "PairResult", "Pairing",
		"PendingInvitation", "RangeInput", "SendMessageResult", "User", "Webhook", "WebhookInput",
	}
	var got []string
	for _, m := range regexp.MustCompile(`(?m)^class (\w+):$`).FindAllStringSubmatch(models(t), -1) {
		got = append(got, m[1])
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestEachClassIsAFrozenDataclass(t *testing.T) {
	file := models(t)
	classes := strings.Count(file, "\nclass ") - len(enumClasses)
	if got := strings.Count(file, "@dataclass(frozen=True, slots=True, kw_only=True)\nclass "); got != classes {
		t.Errorf("%d of %d classes are frozen dataclasses", got, classes)
	}
}

func TestFields(t *testing.T) {
	file := models(t)
	for class, lines := range map[string][]string{
		"Alert": {"since: datetime | None"}, // required and nullable: no default
		"ApiKey": {
			"id: str",
			"created_at: datetime",
		},
		"BulkJob":          {"total_messages: int", "progress_percent: float", "replayed: bool", "status: str"},
		"BulkMessageInput": {"delay_ms: int | None = None"},
		"BulkPacingInput":  {"pause_every: RangeInput | None = None"},
		"ClientList":       {"items: tuple[OrgClient, ...]"},
		"Instance":         {"webhooks: tuple[Webhook, ...]", "alert: Alert | None", "status: str | None"},
		"MeOrganization": {
			"instance_count: int | None = None",
			"meta_webhook: MetaWebhook | None = None",
		},
		"MetaCredentialsInput": {
			"phone_number_id: str",
			"access_token: str = field(repr=False)",
			"app_secret: str = field(repr=False)",
		},
		"MetaWebhook": {"url: str", "verify_token: str = field(repr=False)"},
		"Pairing": {
			"message: str",
			"pairing_code: str | None = field(repr=False)",
			"qr_text: str | None = field(repr=False)",
		},
		"WebhookInput": {"secret: str | None = field(default=None, repr=False)"},
	} {
		body := classBody(t, file, class)
		for _, line := range lines {
			if !strings.Contains(body, "\n    "+line+"\n    \"\"\"") {
				t.Errorf("%s has no field %q with a text under it:\n%s", class, line, body)
			}
		}
	}
	if strings.Contains(classBody(t, file, "ApiKey"), "token:") {
		t.Error("ApiKey has the field token")
	}
}

// No nested request schema has a list today, but the method arguments have the same types.
func TestListTypes(t *testing.T) {
	list := spec.Schema{Type: "array", Nullable: true, Items: &spec.Schema{Type: "string"}}
	for schema, want := range map[string]string{
		"RangeInput": "x: Sequence[str] | None = None",
		"Alert":      "x: tuple[str, ...] | None = None",
	} {
		doc := loadSpec(t)
		addProperty(doc, schema, "x", false, list)
		content, err := newModelWriter(doc).file()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), "    "+want+"\n") {
			t.Errorf("models.py has no field %q", want)
		}
		if got := strings.Contains(string(content), "import Sequence"); got != (schema == "RangeInput") {
			t.Errorf("%s: the import of Sequence is %t", schema, got)
		}
	}
}

func TestConstants(t *testing.T) {
	file := models(t)
	for class, lines := range map[string][]string{
		"Provider":           {`WHATSAPP_WEB: Final = "WHATSAPP_WEB"`, `META_CLOUD: Final = "META_CLOUD"`},
		"DeviceIdentity":     {`SAFARI_MAC_OS: Final = "SAFARI_MAC_OS"`},
		"InstanceStatus":     {`NEEDS_AUTH: Final = "NEEDS_AUTH"`, `ERROR: Final = "ERROR"`},
		"InstanceState":      {`MISSING: Final = "missing"`},
		"BulkJobStatus":      {`QUEUED: Final = "QUEUED"`},
		"PairingFailureCode": {`PAIRING_EXPIRED: Final = "pairing_expired"`},
	} {
		body := classBody(t, file, class)
		for _, line := range lines {
			if !strings.Contains(body, "\n    "+line) {
				t.Errorf("%s has no line %q:\n%s", class, line, body)
			}
		}
	}
	for field, class := range enumFields {
		schema, name, _ := strings.Cut(field, ".")
		if schema != "Instance" && schema != "Pairing" && schema != "BulkJob" {
			continue // it has no dataclass
		}
		_, text, _ := strings.Cut(classBody(t, file, schema), "\n    "+name+": ")
		text, _, _ = strings.Cut(text, "\n\n")
		if !strings.Contains(strings.Join(strings.Fields(text), " "), "The class `"+class+"` has the known values.") {
			t.Errorf("the text of %s does not name the class %s: %s", field, class, text)
		}
	}
}

func TestPythonText(t *testing.T) {
	for text, want := range map[string]string{
		"Absent or null keeps it.":                   "Absent or None keeps it.",
		"The default is false; true sends.":          "The default is False; True sends.",
		"When the job was created, RFC 3339 in UTC.": "When the job was created, in UTC.",
		"trueish, falsely, nullable, True, False":    "trueish, falsely, nullable, True, False",
	} {
		if got := pythonText(text); got != want {
			t.Errorf("%q: got %q, want %q", text, got, want)
		}
	}
}

func TestDocstrings(t *testing.T) {
	for text, want := range map[string]string{
		"One sentence.":       `    """One sentence."""`,
		"two\n  lines\t here": `    """two lines here"""`,
		`back\slash`:          `    """back\\slash"""`,
		`ends with \`:         `    """ends with \\"""`,
		`says "yes"`:          `    """says \"yes\""""`,
		`"""`:                 `    """\"\"\""""`,
		"nul\x00":             `    """nul\x00"""`,
		strings.Repeat("a", 48) + " " + strings.Repeat("b", 44) + " c": `    """` + strings.Repeat("a", 48) +
			" " + strings.Repeat("b", 44) + "\n    c\"\"\"",
	} {
		if got := docstring(4, []string{text}); got != want+"\n" {
			t.Errorf("%q: got %s, want %s", text, got, want)
		}
	}
	for line := range strings.Lines(models(t)) {
		if len([]rune(strings.TrimSuffix(line, "\n"))) > 100 {
			t.Errorf("the line has more than 100 characters: %s", line)
		}
	}
}

func property(doc *spec.Doc, schema, name string) *spec.Schema {
	return doc.Schemas[schema].Property(name)
}

func addProperty(doc *spec.Doc, schema, name string, required bool, s spec.Schema) {
	if s.Description == "" {
		s.Description = "A text."
	}
	doc.Schemas[schema].Properties = append(doc.Schemas[schema].Properties,
		spec.Property{Name: name, Required: required, Schema: &s})
}

func TestModelsRefusals(t *testing.T) {
	text := func(s string) *string { return &s }
	free := true
	// Each case adds a property to the answer schema Alert.
	shapes := map[string]spec.Schema{
		"no type":             {},
		"null":                {Type: "null", Nullable: true},
		"an object":           {Type: "object"},
		"a free object":       {Type: "object", AdditionalProperties: &free},
		"an unknown format":   {Type: "string", Format: "byte"},
		"a list of anything":  {Type: "array"},
		"a list with null":    {Type: "array", Items: &spec.Schema{Type: "string", Nullable: true}},
		"a list of an enum":   {Type: "array", Items: &spec.Schema{Type: "string", Enum: []*string{text("A")}}},
		"a ref with a type":   {Ref: "Instance", Type: "string"},
		"two refs":            {OneOf: []*spec.Schema{{Ref: "Instance"}, {Ref: "Alert"}}, Nullable: true},
		"three members":       {OneOf: []*spec.Schema{{Ref: "Instance"}, {Type: "null"}, {Type: "null"}}},
		"a ref to no schema":  {Ref: "Nothing"},
		"a ref to no class":   {Ref: "Done"},
		"an enum with no map": {Type: "string", Enum: []*string{text("A")}},
	}
	type refusal struct {
		name, want string
		change     func(doc *spec.Doc)
	}
	var cases []refusal
	for name, s := range shapes {
		want := "Alert.x"
		if s.Ref == "Nothing" {
			want = "Nothing"
		}
		cases = append(cases, refusal{name, want, func(doc *spec.Doc) { addProperty(doc, "Alert", "x", true, s) }})
	}
	for _, keyword := range []string{"from", "class", "None"} {
		cases = append(cases, refusal{"keyword " + keyword, "Alert." + keyword, func(doc *spec.Doc) {
			addProperty(doc, "Alert", keyword, true, spec.Schema{Type: "string"})
		}})
	}
	for _, name := range shadowed {
		cases = append(cases, refusal{"shadowing name " + name, "Alert." + name, func(doc *spec.Doc) {
			addProperty(doc, "Alert", name, true, spec.Schema{Type: "string"})
		}})
	}
	cases = append(cases, []refusal{
		{"a rename of a schema that no operation uses", "the schema Client", func(doc *spec.Doc) {
			doc.Operations = slices.DeleteFunc(doc.Operations, func(op spec.Operation) bool {
				for _, s := range []*spec.Schema{op.Request, op.Answer} {
					if s == nil {
						continue
					}
					used, _ := doc.UsedBy(s)
					if slices.Contains(used, "Client") {
						return true
					}
				}
				return false
			})
		}},
		{"a schema with no class that no operation uses", "the schema Done", func(doc *spec.Doc) {
			doc.PairingEvents = slices.DeleteFunc(doc.PairingEvents, func(e spec.Event) bool { return e.Name == "done" })
		}},
		{"a keyword in a body", "SendMessageRequest.from", func(doc *spec.Doc) {
			addProperty(doc, "SendMessageRequest", "from", true, spec.Schema{Type: "string"})
		}},
		{"a name that is not Python", "Alert.two-parts", func(doc *spec.Doc) {
			addProperty(doc, "Alert", "two-parts", true, spec.Schema{Type: "string"})
		}},
		{"a private name", "Alert._x", func(doc *spec.Doc) {
			addProperty(doc, "Alert", "_x", true, spec.Schema{Type: "string"})
		}},

		{"a schema that is not an object", "the schema Alert", func(doc *spec.Doc) {
			doc.Schemas["Alert"] = &spec.Schema{Type: "string", Description: "A text."}
		}},
		{"a schema with free properties", "the schema Alert", func(doc *spec.Doc) {
			doc.Schemas["Alert"].AdditionalProperties = &free
		}},
		{"a schema with a bad name", "the schema name alert", func(doc *spec.Doc) {
			doc.Schemas["alert"] = doc.Schemas["Alert"]
			property(doc, "Instance", "alert").OneOf[0].Ref = "alert"
		}},
		{"a schema with the name of a keyword", "the schema name None", func(doc *spec.Doc) {
			doc.Schemas["None"] = doc.Schemas["Alert"]
			property(doc, "Instance", "alert").OneOf[0].Ref = "None"
		}},
		{"a schema with the name of a class of constants", "two classes have the name Provider", func(doc *spec.Doc) {
			doc.Schemas["Provider"] = doc.Schemas["Alert"]
			property(doc, "Instance", "alert").OneOf[0].Ref = "Provider"
		}},
		{"two schemas with one class name", "two classes have the name OrgClient", func(doc *spec.Doc) {
			doc.Schemas["OrgClient"] = doc.Schemas["Alert"]
			property(doc, "Instance", "alert").OneOf[0].Ref = "OrgClient"
		}},
		{"a schema in a request and an answer", "the schema RangeInput", func(doc *spec.Doc) {
			addProperty(doc, "Alert", "x", true, spec.Schema{Ref: "RangeInput"})
		}},
		{"a ref to a body", "the schema SendMessageRequest", func(doc *spec.Doc) {
			addProperty(doc, "Alert", "x", true, spec.Schema{Ref: "SendMessageRequest"})
		}},
		{"a body in a body", "RangeInput.x", func(doc *spec.Doc) {
			addProperty(doc, "RangeInput", "x", true, spec.Schema{Ref: "SendMessageRequest"})
		}},
		{"a schema that is not used", "Failed", func(doc *spec.Doc) {
			doc.PairingEvents = slices.DeleteFunc(doc.PairingEvents, func(e spec.Event) bool { return e.Name == "failed" })
		}},
		{"a body with no name", "createClient", func(doc *spec.Doc) {
			for i, op := range doc.Operations {
				if op.ID == "createClient" {
					doc.Operations[i].Request = doc.Schemas["CreateClientRequest"]
				}
			}
		}},
		{"an answer with no name", "getMe", func(doc *spec.Doc) {
			for i, op := range doc.Operations {
				if op.ID == "getMe" {
					doc.Operations[i].Answer = doc.Schemas["Me"]
				}
			}
		}},
		{"an event with no name", "the event update", func(doc *spec.Doc) {
			for i, e := range doc.PairingEvents {
				if e.Name == "update" {
					doc.PairingEvents[i].Data = doc.Schemas["Pairing"]
				}
			}
		}},
		{"a date in a request", "RangeInput.x", func(doc *spec.Doc) {
			addProperty(doc, "RangeInput", "x", false, spec.Schema{Type: "string", Format: "date-time"})
		}},
		{"a required nullable field in a request", "RangeInput.x", func(doc *spec.Doc) {
			addProperty(doc, "RangeInput", "x", true, spec.Schema{Type: "string", Nullable: true})
		}},

		{"an enum in a body with no class", "SendMessageRequest.x", func(doc *spec.Doc) {
			addProperty(doc, "SendMessageRequest", "x", true, spec.Schema{Type: "string", Enum: []*string{text("A")}})
		}},
		{"a mapped field that does not exist", "BulkJob.status", func(doc *spec.Doc) {
			doc.Schemas["BulkJob"].Properties = slices.DeleteFunc(doc.Schemas["BulkJob"].Properties,
				func(p spec.Property) bool { return p.Name == "status" })
		}},
		{"a class of constants with no value", "BulkJob.status", func(doc *spec.Doc) {
			property(doc, "BulkJob", "status").Enum = nil
		}},
		{"a value that the class does not have", "Pairing.status", func(doc *spec.Doc) {
			s := property(doc, "Pairing", "status")
			s.Enum = append(s.Enum, text("NEW"))
		}},
		{"a value that gives no constant name", `"two words"`, func(doc *spec.Doc) {
			s := property(doc, "BulkJob", "status")
			s.Enum = append(s.Enum, text("two words"))
		}},
		{"two values that give one constant name", `"queued"`, func(doc *spec.Doc) {
			s := property(doc, "BulkJob", "status")
			s.Enum = append(s.Enum, text("queued"))
		}},

		{"a schema with no text", "the schema Alert has no text", func(doc *spec.Doc) {
			doc.Schemas["Alert"].Description = ""
		}},
		{"a field with no text", "Alert.kind has no text", func(doc *spec.Doc) {
			property(doc, "Alert", "kind").Description = " "
		}},
		{"a body with no text", "the schema SendMessageRequest has no text", func(doc *spec.Doc) {
			doc.Schemas["SendMessageRequest"].Description = ""
		}},
		{"a field of a body with no text", "SendMessageRequest.to has no text", func(doc *spec.Doc) {
			property(doc, "SendMessageRequest", "to").Description = ""
		}},
		{"a field of an event with no text", "Failed.detail has no text", func(doc *spec.Doc) {
			property(doc, "Failed", "detail").Description = ""
		}},
		{"a text about CSRF", "Alert.kind", func(doc *spec.Doc) {
			property(doc, "Alert", "kind").Description = "Errors: 403 csrf_check_failed."
		}},
		{"a text with a header", `the text of Alert.kind has "Retry-After"`, func(doc *spec.Doc) {
			property(doc, "Alert", "kind").Description = "Look at Retry-After."
		}},
		{"a text about calls with no key", "the schema Alert", func(doc *spec.Doc) {
			doc.Schemas["Alert"].Description = "Without a key, do not retry automatically."
		}},

		{"a secret that does not exist", "WebhookInput.secret", func(doc *spec.Doc) {
			doc.Schemas["WebhookInput"].Properties = slices.DeleteFunc(doc.Schemas["WebhookInput"].Properties,
				func(p spec.Property) bool { return p.Name == "secret" })
		}},
		{"an omitted field that does not exist", "ApiKey.token", func(doc *spec.Doc) {
			doc.Schemas["ApiKey"].Properties = slices.DeleteFunc(doc.Schemas["ApiKey"].Properties,
				func(p spec.Property) bool { return p.Name == "token" })
		}},
	}...)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := loadSpec(t)
			c.change(doc)
			_, err := buildFiles(doc)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("want an error with %q, got %v", c.want, err)
			}
		})
	}
}
