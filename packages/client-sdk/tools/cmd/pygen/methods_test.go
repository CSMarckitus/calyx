package main

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"calyx/sdktools/internal/spec"
)

func methods(t *testing.T) string {
	t.Helper()
	return string(build(t)["methods.py"])
}

// methodClass returns the part of methods.py of one class.
func methodClass(t *testing.T, file, class string) string {
	t.Helper()
	_, body, ok := strings.Cut(file, "\nclass "+class+"(")
	if !ok {
		t.Fatalf("methods.py has no class %s", class)
	}
	body, _, _ = strings.Cut(body, "\nclass ")
	return body
}

var defLine = regexp.MustCompile(`(?m)^    (?:async )?def (\w+)\(`)

// defs returns each "def" of a method of SyncMethods, the overloads too, with its docstring and
// its body.
func defs(t *testing.T, file, method string) []string {
	t.Helper()
	body := methodClass(t, file, "SyncMethods")
	var out []string
	matches := defLine.FindAllStringSubmatchIndex(body, -1)
	for i, m := range matches {
		end := len(body)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		if body[m[2]:m[3]] == method {
			text := strings.TrimSpace(body[m[0]:end])
			out = append(out, strings.TrimSpace(strings.TrimSuffix(text, "@overload")))
		}
	}
	if len(out) == 0 {
		t.Fatalf("methods.py has no method %s", method)
	}
	return out
}

// oneLine returns a text with one space between two words.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func TestTheMethodsAreTheOnesOfTheDesign(t *testing.T) {
	want := []string{
		"list_api_keys", "revoke_api_key",
		"list_clients", "create_client", "get_client", "update_client", "delete_client",
		"list_instances", "create_instance", "get_instance", "update_instance", "remove_instance",
		"pair_instance", "cancel_pairing",
		"send_message", "create_bulk_job", "list_bulk_jobs", "get_bulk_job", "cancel_bulk_job",
		"get_me", "get_organization", "update_organization",
	}
	for _, class := range []string{"SyncMethods", "AsyncMethods"} {
		var got []string
		for _, m := range defLine.FindAllStringSubmatch(methodClass(t, methods(t), class), -1) {
			got = append(got, m[1])
		}
		if got = slices.Compact(got); !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", class, got, want)
		}
	}
}

func TestMethodNames(t *testing.T) {
	for id, want := range map[string]string{
		"listAPIKeys": "list_api_keys", "revokeAPIKey": "revoke_api_key", "getMe": "get_me", "createBulkJob": "create_bulk_job",
	} {
		if got := methodName(id); got != want {
			t.Errorf("%s: got %s, want %s", id, got, want)
		}
	}
}

// Only "async" and "await" are different in the two classes.
func TestTheClassesHaveTheSameMethods(t *testing.T) {
	file := methods(t)
	_, syncBody, _ := strings.Cut(methodClass(t, file, "SyncMethods"), "\"\"\"\n\n")
	_, asyncBody, _ := strings.Cut(methodClass(t, file, "AsyncMethods"), "\"\"\"\n\n")
	asyncBody = strings.ReplaceAll(asyncBody, "async def ", "def ")
	asyncBody = strings.ReplaceAll(asyncBody, "await ", "")
	// A def can need one line in one class and more lines in the other one.
	normal := func(s string) string {
		return strings.NewReplacer("( ", "(", ", )", ")", " )", ")").Replace(oneLine(s))
	}
	if normal(syncBody) != normal(asyncBody) {
		t.Error("SyncMethods and AsyncMethods differ")
	}
}

func TestSignatures(t *testing.T) {
	file := methods(t)
	for method, want := range map[string]string{
		"list_api_keys":  "(self, *, time_limit: float | None = None) -> ApiKeyList:",
		"revoke_api_key": "(self, api_key_id: str, *, time_limit: float | None = None) -> None:",
		"create_client": "(self, *, name: str, can_create_instances: bool | None = None, max_instances: int | None = None, " +
			"time_limit: float | None = None) -> OrgClient:",
		"update_client": "(self, client_id: str, *, can_create_instances: bool | None = None, max_instances: int | None = None, " +
			"name: str | None = None, time_limit: float | None = None) -> OrgClient:",
		"update_instance": "(self, instance_id: str, *, display_name: str | None = None, enabled: bool | None = None, " +
			"meta_credentials: MetaCredentialsInput | None = None, send_only: bool | None = None, " +
			"webhooks: Sequence[WebhookInput] | None = None, time_limit: float | None = None) -> Instance:",
		"remove_instance": "(self, instance_id: str, *, time_limit: float | None = None) -> int:",
		"cancel_pairing":  "(self, instance_id: str, *, time_limit: float | None = None) -> None:",
		"send_message": "(self, instance_id: str, *, to: str, text: str, idempotency_key: str | None = None, " +
			"time_limit: float | None = None) -> Sent[SendMessageResult]:",
		"get_bulk_job": "(self, job_id: str, *, time_limit: float | None = None) -> BulkJob:",
		"get_me":       "(self, *, time_limit: float | None = None) -> Me:",
	} {
		got := defs(t, file, method)
		if len(got) != 1 || !strings.HasPrefix(signature(got[0]), "def "+method+want) {
			t.Errorf("%s: got %s", method, signature(got[0]))
		}
	}
}

// signature returns a "def" on one line.
func signature(def string) string {
	def, _, _ = strings.Cut(def, `"""`)
	return strings.NewReplacer("( ", "(", ", )", ")").Replace(oneLine(def))
}

func docstringOf(def string) string {
	_, text, _ := strings.Cut(def, `"""`)
	text, _, _ = strings.Cut(text, `"""`)
	return text
}

func TestOverloads(t *testing.T) {
	file := methods(t)
	const bulkEnd = "pacing: BulkPacingInput | None = None, idempotency_key: str | None = None, " +
		"time_limit: float | None = None) -> Sent[BulkJob]:"
	const instanceEnd = "webhooks: Sequence[WebhookInput] | None = None, client_id: str | None = None, " +
		"time_limit: float | None = None) -> CreateInstanceResult:"
	for method, want := range map[string][]string{
		"create_bulk_job": {
			"(self, instance_id: str, *, recipients: Sequence[str], text: str, " + bulkEnd,
			"(self, instance_id: str, *, messages: Sequence[BulkMessageInput], " + bulkEnd,
			"(self, instance_id: str, *, messages: Sequence[BulkMessageInput] | None = None, " +
				"pacing: BulkPacingInput | None = None, recipients: Sequence[str] | None = None, text: str | None = None, " +
				"idempotency_key: str | None = None, time_limit: float | None = None) -> Sent[BulkJob]:",
		},
		"create_instance": {
			`(self, *, provider: Literal["WHATSAPP_WEB"], display_name: str, locale: str, pairing: str | None = None, ` +
				"phone: str | None = None, device_identity: str | None = None, send_only: bool | None = None, " + instanceEnd,
			`(self, *, provider: Literal["META_CLOUD"], display_name: str, meta_credentials: MetaCredentialsInput, ` + instanceEnd,
			"(self, *, display_name: str, provider: str, client_id: str | None = None, device_identity: str | None = None, " +
				"locale: str | None = None, meta_credentials: MetaCredentialsInput | None = None, pairing: str | None = None, " +
				"phone: str | None = None, send_only: bool | None = None, webhooks: Sequence[WebhookInput] | None = None, " +
				"time_limit: float | None = None) -> CreateInstanceResult:",
		},
	} {
		got := defs(t, file, method)
		if len(got) != len(want) {
			t.Fatalf("%s has %d definitions, want %d", method, len(got), len(want))
		}
		for i := range got {
			if s := signature(got[i]); s != "def "+method+want[i] {
				t.Errorf("%s: got\n%s, want\n%s", method, s, want[i])
			}
			// An IDE can show the text of the method or the text of one overload.
			if text := docstringOf(got[i]); text == "" || text != docstringOf(got[len(got)-1]) {
				t.Errorf("%s: definition %d has another docstring", method, i)
			}
			if isLast := i == len(got)-1; strings.Contains(got[i], "self._") != isLast {
				t.Errorf("%s: definition %d: only the last one has a body", method, i)
			}
		}
	}
	if got := strings.Count(methodClass(t, file, "SyncMethods"), "@overload"); got != 4 {
		t.Errorf("got %d overloads, want 4", got)
	}
}

func TestOnlyTheSendsUseAKey(t *testing.T) {
	file := methods(t)
	for _, op := range loadSpec(t).Operations {
		if op.TimeLimit == 0 {
			continue
		}
		all := defs(t, file, methodName(op.ID))
		def := all[len(all)-1]
		if strings.Contains(def, "self._send(") != op.IdempotencyKey || strings.Contains(def, "self._call(") == op.IdempotencyKey {
			t.Errorf("%s: the call does not fit IdempotencyKey %t", op.ID, op.IdempotencyKey)
		}
		if strings.Contains(def, "idempotency_key=idempotency_key") != op.IdempotencyKey {
			t.Errorf("%s: the key does not fit IdempotencyKey %t", op.ID, op.IdempotencyKey)
		}
	}
}

func TestMethodTexts(t *testing.T) {
	file := methods(t)
	for method, parts := range map[string][]string{
		"send_message": {
			`"""Send one text message. A 504 send_outcome_unknown means the message may or may not have been sent. ` +
				"After a 504, retry with the same key: the core sends the message at most once. " +
				"After 429 instance_queue_full or 409 send_in_progress, wait for `retry_after` seconds, then retry with the same key. " +
				"A resend with a known key gets its answer also while the instance is not connected. Args:",
			" to: Not empty. At most 64 bytes. The core checks the format.",
			" instance_id: The id of the instance, a UUID. Any other value gives 404 not_found.",
			" idempotency_key: The key of this message: 1 to 255 printable ASCII characters",
			"Use a new key for each message. Use the same key only to repeat the same call.",
			"Without it, the SDK makes a new key.",
			" time_limit: The time limit of the call, in seconds. Without it, the `time_limit` of the SDK client applies, " +
				"or 360 seconds when it has none.",
			"A message that WhatsApp accepted.",
			"Raises: CalyxError: ",
			"When `retry` is `RetryAdvice.RETRY_NOW`, call again with the same arguments and the `idempotency_key` of the error. " +
				"When it is `RetryAdvice.RETRY_LATER`, do the same, but first wait for `retry_after` seconds or more.",
			"400 invalid_request, 401 unauthenticated, 404 not_found, 409 instance_not_connected",
			"24 hours",
			"A cancelled call with no stored key cannot be repeated safely.",
		},
		"create_bulk_job": {
			"Send either recipients with text, or messages.",
			"Retry with the same key: the retry gives the job and stores it. Without this retry, the job cannot be read",
			"24 hours",
			"Use a new key for each job. Use the same key only to repeat the same call.",
			"A cancelled call with no stored key cannot be repeated safely.",
			"or 150 seconds",
		},
		"create_client": {
			"List the clients with `list_clients` before you call again",
			"timeout, network_error or invalid_response", "`RetryAdvice.UNKNOWN`",
			"or 120 seconds",
		},
		"create_instance": {
			"List the instances with `list_instances` before you call again",
			" device_identity: WHATSAPP_WEB only.", "The class `DeviceIdentity` has the known values.",
		},
		"update_client": {"An absent or None field keeps its value."},
		"list_api_keys": {"The keys, newest first. The token is never shown again."},
		"remove_instance": {
			"Returns: int: The HTTP status. It is 204 when the instance is removed, and 202 when",
		},
		"list_bulk_jobs": {"Returns: BulkJobList: The latest bulk jobs of an instance, newest first, at most 50."},
		"revoke_api_key": {"Errors of the backend: 401 unauthenticated, 404 not_found."},
	} {
		for _, def := range defs(t, file, method) {
			text := oneLine(def)
			for _, part := range parts {
				if !strings.Contains(text, part) {
					t.Errorf("%s: the text has no %q:\n%s", method, part, def)
				}
			}
		}
	}
	for _, method := range []string{"revoke_api_key", "delete_client", "cancel_pairing"} {
		if strings.Contains(defs(t, file, method)[0], "Returns:") {
			t.Errorf("%s returns nothing, but its text has a part Returns", method)
		}
	}
}

// The texts for a program that writes its HTTP calls are wrong for a program with the SDK.
func TestMethodTextsAreForTheSDK(t *testing.T) {
	file := methods(t)
	for _, part := range []string{
		"csrf_check_failed", "Without a key", "automatically", "Idempotency-Key", "Retry-After", " header", "null",
		"true", "false", "`RETRY_", "GET /", "POST /", "/instances", "aborted", "  .", " ,", ",.", "..\n",
	} {
		if strings.Contains(file, part) {
			t.Errorf("methods.py has %q", part)
		}
	}
	for _, op := range loadSpec(t).Operations {
		if strings.Contains(file, op.ID) {
			t.Errorf("methods.py has the operation id %s", op.ID)
		}
	}
	for line := range strings.Lines(file) {
		if len([]rune(strings.TrimSuffix(line, "\n"))) > 100 {
			t.Errorf("the line has more than 100 characters: %s", line)
		}
	}
}

func TestOperations(t *testing.T) {
	file := string(build(t)["operations.py"])
	for _, want := range []string{
		"LIST_API_KEYS: Final = Operation(\n" +
			"    id=\"listAPIKeys\",\n" +
			"    method=\"GET\",\n" +
			"    path=\"/api/v1/api-keys\",\n" +
			"    time_limit=120,\n" +
			"    read=read_model(ApiKeyList),\n" +
			")\n",
		"    id=\"sendMessage\",\n" +
			"    method=\"POST\",\n" +
			"    path=\"/api/v1/instances/{id}/messages\",\n" +
			"    time_limit=360,\n" +
			"    read=read_model(SendMessageResult),\n",
		"    id=\"removeInstance\",\n" +
			"    method=\"DELETE\",\n" +
			"    path=\"/api/v1/instances/{id}\",\n" +
			"    time_limit=120,\n" +
			"    read=read_status,\n",
		"    path=\"/api/v1/instances/{id}/pairing\",\n    time_limit=120,\n    read=read_nothing,\n",
		"    path=\"/api/v1/instances/{id}/bulk-jobs\",\n    time_limit=150,\n    read=read_model(BulkJob),\n",
	} {
		if !strings.Contains(file, want) {
			t.Errorf("operations.py has no\n%s", want)
		}
	}
	if got := strings.Count(file, " = Operation("); got != 22 {
		t.Errorf("got %d operations, want 22", got)
	}
}

func operation(doc *spec.Doc, id string) *spec.Operation {
	i := slices.IndexFunc(doc.Operations, func(op spec.Operation) bool { return op.ID == id })
	return &doc.Operations[i]
}

func TestMethodsRefusals(t *testing.T) {
	replace := func(doc *spec.Doc, id, part, other string) {
		op := operation(doc, id)
		op.Description = strings.Replace(op.Description, part, other, 1)
	}
	required := func(doc *spec.Doc, schema, name string) {
		for i, p := range doc.Schemas[schema].Properties {
			if p.Name == name {
				doc.Schemas[schema].Properties[i].Required = true
				p.Schema.Nullable = false
			}
		}
	}
	remove := func(doc *spec.Doc, schema, name string) {
		doc.Schemas[schema].Properties = slices.DeleteFunc(doc.Schemas[schema].Properties,
			func(p spec.Property) bool { return p.Name == name })
	}
	type refusal struct {
		name, want string
		change     func(doc *spec.Doc)
	}
	var cases []refusal
	for _, name := range []string{"self", "time_limit", "idempotency_key", "from", "instance_id"} {
		want := "another argument with the name of SendMessageRequest." + name
		if name == "from" {
			want = "the property SendMessageRequest.from cannot be a Python field name"
		}
		cases = append(cases, refusal{"the argument " + name, want, func(doc *spec.Doc) {
			addProperty(doc, "SendMessageRequest", name, false, spec.Schema{Type: "string"})
		}})
	}
	cases = append(cases, []refusal{
		{"a field in no overload", "the field CreateInstanceRequest.x is in no overload", func(doc *spec.Doc) {
			addProperty(doc, "CreateInstanceRequest", "x", false, spec.Schema{Type: "string", Nullable: true})
		}},
		{"a field in no overload of a send", "the field CreateBulkJobRequest.x is in no overload", func(doc *spec.Doc) {
			addProperty(doc, "CreateBulkJobRequest", "x", false, spec.Schema{Type: "string", Nullable: true})
		}},
		{"an overload with a field that does not exist", "an overload names the field phone", func(doc *spec.Doc) {
			remove(doc, "CreateInstanceRequest", "phone")
		}},
		{"a required field that an overload does not have", "the body needs CreateBulkJobRequest.text", func(doc *spec.Doc) {
			required(doc, "CreateBulkJobRequest", "text")
		}},
		{"a required field that is optional in an overload", "the body needs CreateBulkJobRequest.pacing", func(doc *spec.Doc) {
			required(doc, "CreateBulkJobRequest", "pacing")
		}},
		{"an overload with a value that does not exist", "backend.json has [WHATSAPP_WEB]", func(doc *spec.Doc) {
			s := property(doc, "CreateInstanceRequest", "provider")
			s.Enum = s.Enum[:1]
		}},
		{"a value of the enum with no overload", "backend.json has [WHATSAPP_WEB META_CLOUD NEW]", func(doc *spec.Doc) {
			value := "NEW"
			for _, schema := range []string{"Instance", "CreateInstanceRequest"} {
				s := property(doc, schema, "provider")
				s.Enum = append(s.Enum, &value)
			}
		}},
		{"an overload of an operation that does not exist", "pygen names the operation createBulkJob", func(doc *spec.Doc) {
			doc.Operations = slices.DeleteFunc(doc.Operations, func(op spec.Operation) bool { return op.ID == "createBulkJob" })
		}},

		{"a path with no argument in the map", "pathArguments does not have the path /api/v1/things/{id}", func(doc *spec.Doc) {
			operation(doc, "getClient").Path = "/api/v1/things/{id}"
		}},
		{"a path parameter with another name", "pathArguments does not have the path /api/v1/clients/{client}", func(doc *spec.Doc) {
			op := operation(doc, "getClient")
			op.Path, op.PathParams[0].Name = "/api/v1/clients/{client}", "client"
		}},
		{"a path with two parameters", "pathArguments does not have the path /api/v1/clients/{id}/{id}", func(doc *spec.Doc) {
			op := operation(doc, "getClient")
			op.Path = "/api/v1/clients/{id}/{id}"
			op.PathParams = append(op.PathParams, op.PathParams[0])
		}},
		{"a path argument that nobody uses", "pygen names the path api-keys", func(doc *spec.Doc) {
			op := operation(doc, "revokeAPIKey")
			op.Path, op.PathParams = "/api/v1/api-keys", nil
		}},
		{"a path parameter with no text", "the path argument has no text", func(doc *spec.Doc) {
			operation(doc, "getClient").PathParams[0].Description = ""
		}},

		{"a send with no answer", "a send needs an answer", func(doc *spec.Doc) { operation(doc, "sendMessage").Answer = nil }},
		{"two statuses with no text", "the statuses [202 204] and the answer do not fit", func(doc *spec.Doc) {
			operation(doc, "cancelPairing").Statuses = []int{202, 204}
		}},
		{"a text for one status", "the statuses [204] and the answer do not fit", func(doc *spec.Doc) {
			operation(doc, "removeInstance").Statuses = []int{204}
		}},
		{"a text for a status with an answer", "the statuses [202 204] and the answer do not fit", func(doc *spec.Doc) {
			operation(doc, "removeInstance").Answer = &spec.Schema{Ref: "Instance"}
		}},
		{"an answer with no class", "the answer has no class", func(doc *spec.Doc) {
			operation(doc, "getMe").Answer = &spec.Schema{Ref: "Done"}
		}},
		{"an id that is a keyword", "the operation import: the id cannot be a Python method name", func(doc *spec.Doc) {
			operation(doc, "getMe").ID = "import"
		}},
		{"an id with a bad character", "the operation get-me: the id cannot be a Python method name", func(doc *spec.Doc) {
			operation(doc, "getMe").ID = "get-me"
		}},
		{"a send with no text", "the operation getBulkJob: the idempotency key does not fit sendTexts", func(doc *spec.Doc) {
			operation(doc, "getBulkJob").IdempotencyKey = true
		}},
		{"a text for a send with no key", "the operation sendMessage: the idempotency key does not fit sendTexts", func(doc *spec.Doc) {
			operation(doc, "sendMessage").IdempotencyKey = false
		}},
		{"a list method that does not exist", "pygen names the method list_clients", func(doc *spec.Doc) {
			doc.Operations = slices.DeleteFunc(doc.Operations, func(op spec.Operation) bool { return op.ID == "listClients" })
		}},
		{"a create call with no list", "the create call is not in createTexts", func(doc *spec.Doc) {
			doc.CreateCalls.Operations = append(doc.CreateCalls.Operations, "getMe")
		}},
		{"a list for no create call", "pygen names the create call createClient", func(doc *spec.Doc) {
			doc.CreateCalls.Operations = []string{"createInstance"}
		}},

		{"no summary", "the summary has no text", func(doc *spec.Doc) { operation(doc, "getMe").Summary = "" }},
		{"no list of errors", "the description has no list of errors, or two", func(doc *spec.Doc) { operation(doc, "getMe").Description = "A text." }},
		{"two lists of errors", "the description has no list of errors, or two", func(doc *spec.Doc) {
			replace(doc, "getMe", "Errors:", "Errors: 404 not_found. Errors:")
		}},
		{"a list of errors that is no list", `"see below" is not an error of the list`, func(doc *spec.Doc) {
			replace(doc, "getMe", "401 unauthenticated", "see below")
		}},
		{"a text about calls with no key", "the docstring has \"do not retry automatically\"", func(doc *spec.Doc) {
			replace(doc, "getMe", "Errors:", "In this case, do not retry automatically. Errors:")
		}},
		{"a text about CSRF", `the docstring has "csrf_check_failed"`, func(doc *spec.Doc) {
			replace(doc, "getMe", "Errors:", "It gives csrf_check_failed. Errors:")
		}},
		{"a text with an operation id", "the docstring has the operation id listClients", func(doc *spec.Doc) {
			replace(doc, "getMe", "Errors:", "Call listClients. Errors:")
		}},
		{"a text with a header", `the docstring has "Idempotency-Key"`, func(doc *spec.Doc) {
			replace(doc, "getMe", "Errors:", "Send an Idempotency-Key. Errors:")
		}},
		{"a text with a path", `the docstring has "GET /"`, func(doc *spec.Doc) {
			replace(doc, "getMe", "Errors:", "Use GET /clients. Errors:")
		}},
		{"an argument text with a header", `the text of SendMessageRequest.to has "Retry-After"`, func(doc *spec.Doc) {
			property(doc, "SendMessageRequest", "to").Description = "Look at Retry-After."
		}},
		{"a changed text that the document does not have", `pygen changes the text " Without a key`, func(doc *spec.Doc) {
			replace(doc, "sendMessage", " Without a key, do not retry automatically.", "")
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

func TestFailsOnALiteralOfNoRequiredField(t *testing.T) {
	first := &overloads["createInstance"][0]
	literals := first.literals
	t.Cleanup(func() { first.literals = literals })
	for _, name := range []string{"providr", "phone"} { // phone is optional
		first.literals = map[string]string{"provider": "WHATSAPP_WEB", name: "x"}
		_, err := buildFiles(loadSpec(t))
		if err == nil || !strings.Contains(err.Error(), "a literal of an overload is for "+name) {
			t.Errorf("%s: want an error, got %v", name, err)
		}
	}
}

// No operation has this argument today.
func TestARequiredListArgument(t *testing.T) {
	doc := loadSpec(t)
	addProperty(doc, "SendMessageRequest", "x", true, spec.Schema{Type: "array", Items: &spec.Schema{Type: "string"}})
	files, err := buildFiles(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(files["methods.py"]), "x: Sequence[str],") {
		t.Error("send_message has no argument x: Sequence[str]")
	}
}
