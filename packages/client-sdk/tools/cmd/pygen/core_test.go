package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"calyx/sdktools/internal/genfiles"
	"calyx/sdktools/internal/protospec"
)

const goldenCoreDir = "testdata/golden_core"

// protoc runs once. Each test parses the bytes again, because it can change the result.
var coreDescriptors = sync.OnceValues(func() ([]byte, error) {
	return descriptorSet(inputPaths(toolsDir).proto)
})

func loadCore(t *testing.T) *protospec.Spec {
	t.Helper()
	data, err := coreDescriptors()
	if err != nil {
		t.Fatal(err)
	}
	s, err := protospec.Parse(data, corePackage)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func buildCore(t *testing.T) map[string]string {
	t.Helper()
	files, err := buildCoreFiles(loadCore(t))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for name, content := range files {
		out[name] = string(content)
	}
	return out
}

// TestCoreGoldenFiles compares each generated file with its golden file, in both directions.
func TestCoreGoldenFiles(t *testing.T) {
	files, err := buildCoreFiles(loadCore(t))
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := genfiles.Write(files, goldenCoreDir, ".py"); err != nil {
			t.Fatal(err)
		}
	}
	stale, err := genfiles.Compare(files, goldenCoreDir, ".py")
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) > 0 {
		t.Fatalf("these files differ from %s: %v; run go test ./cmd/pygen -update and review the diff", goldenCoreDir, stale)
	}
}

func TestCoreFiles(t *testing.T) {
	var names []string
	for name, content := range buildCore(t) {
		names = append(names, name)
		if !strings.HasPrefix(content, header) {
			t.Errorf("%s does not start with the header", name)
		}
		for line := range strings.Lines(content) {
			if len([]rune(strings.TrimSuffix(line, "\n"))) > 100 {
				t.Errorf("%s: the line has more than 100 characters: %s", name, line)
			}
		}
	}
	slices.Sort(names)
	if want := []string{"__init__.py", "convert.py", "methods.py", "models.py"}; !slices.Equal(names, want) {
		t.Errorf("got the files %v, want %v", names, want)
	}
}

// 4 classes of constants, then the 10 answers and the 7 nested requests. No class is for a top-level
// request, for WebhookList or for an event.
func TestTheCoreClassesAreTheOnesOfTheDesign(t *testing.T) {
	want := []string{
		"Provider", "DeviceIdentity", "InstanceStatus", "BulkJobStatus",

		"BulkJob", "BulkPacing", "CreateInstanceResponse", "GetBulkJobsResponse", "GetInstanceStatusResponse",
		"InstanceInfo", "IntRange", "ListInstancesResponse", "MetaCloudSettings", "PairingCodeSettings",
		"PlainTextPayload", "RemoveInstanceResponse", "SendPlainTextResponse", "UpdateInstanceConfigResponse",
		"WebhookConfig", "WebhookSettings", "WhatsAppWebSettings",
	}
	file := buildCore(t)["models.py"]
	var got []string
	for _, m := range regexp.MustCompile(`(?m)^class (\w+):$`).FindAllStringSubmatch(file, -1) {
		got = append(got, m[1])
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if got := strings.Count(file, "@dataclass(frozen=True, slots=True, kw_only=True)\nclass "); got != 17 {
		t.Errorf("got %d frozen dataclasses, want 17", got)
	}
}

func TestCoreFields(t *testing.T) {
	file := buildCore(t)["models.py"]
	for class, lines := range map[string][]string{
		"BulkJob": {
			"id: str", "status: str", "total_messages: int", "progress_percent: float", "created_at: datetime",
			"finished_at: datetime | None = None", "error: str | None = None", "replayed: bool",
		},
		"BulkPacing":             {"delay: IntRange | None = None", "pause_every: IntRange | None = None"},
		"CreateInstanceResponse": {"status: str", "pairing_code: str | None = field(default=None, repr=False)", "device_identity: str", "locale: str | None = None"},
		"GetBulkJobsResponse":    {"jobs: tuple[BulkJob, ...]"},
		"InstanceInfo":           {"webhooks: tuple[WebhookSettings, ...]", "provider: str", "is_enabled: bool"},
		"IntRange":               {"min: int", "max: int"},
		"MetaCloudSettings":      {"phone_number_id: str", "access_token: str = field(repr=False)", "app_secret: str = field(repr=False)"},
		"PlainTextPayload":       {"to: str", "text: str", "delay_ms: int | None = None"},
		"WebhookConfig":          {"url: str", "event: str", "secret: str | None = field(default=None, repr=False)"},
		"WhatsAppWebSettings": {
			"pairing_code: PairingCodeSettings | None = None", "device_identity: str | None = None", "locale: str | None = None",
		},
	} {
		body := classBody(t, file, class)
		for _, line := range lines {
			if !strings.Contains(body, "\n    "+line+"\n    \"\"\"") {
				t.Errorf("%s has no field %q with a text:\n%s", class, line, body)
			}
		}
	}
	if strings.Contains(file, "pairing_qr") {
		t.Error("models.py has pairing_qr")
	}
	const secret = "Keep it secret: with it, a person can link a WhatsApp account to this instance."
	if !strings.Contains(oneLine(classBody(t, file, "CreateInstanceResponse")), secret) {
		t.Error("the pairing code has no warning")
	}
	answers := 0
	for _, class := range []string{
		"BulkJob", "CreateInstanceResponse", "GetBulkJobsResponse", "GetInstanceStatusResponse", "InstanceInfo",
		"ListInstancesResponse", "RemoveInstanceResponse", "SendPlainTextResponse", "UpdateInstanceConfigResponse", "WebhookSettings",
	} {
		answers += len(regexp.MustCompile(`(?m)^    \w+: `).FindAllString(classBody(t, file, class)+"\n", -1))
	}
	if answers != 37 {
		t.Errorf("the answers have %d fields, want 37", answers)
	}
}

func TestCoreConstants(t *testing.T) {
	file := buildCore(t)["models.py"]
	for class, parts := range map[string][]string{
		"BulkJobStatus":  {"    QUEUED: Final = \"QUEUED\"\n    \"\"\"The first status of every job."},
		"DeviceIdentity": {"    SAFARI_MAC_OS: Final = \"SAFARI_MAC_OS\"\n    \"\"\"\\\"Safari (Mac OS)\\\"\"\"\""},
		"InstanceStatus": {"    UNSPECIFIED: Final = \"UNSPECIFIED\"\n", "A newer core can give another value: the text is then the number of the value."},
		"Provider":       {"    META_CLOUD: Final = \"META_CLOUD\"\n"},
	} {
		body := classBody(t, file, class)
		for _, part := range parts {
			if !strings.Contains(oneLine(body), oneLine(part)) {
				t.Errorf("%s has no %q:\n%s", class, part, body)
			}
		}
	}
}

// No public class shows a type of protobuf.
func TestCoreModelsHaveNoProtobufType(t *testing.T) {
	for _, part := range []string{"pb2", "protobuf", "Timestamp"} {
		if strings.Contains(buildCore(t)["models.py"], part) {
			t.Errorf("models.py has %q", part)
		}
	}
}

func coreDefs(t *testing.T, method string) string {
	t.Helper()
	return defs(t, strings.ReplaceAll(buildCore(t)["methods.py"], "class SyncCoreMethods(", "class SyncMethods("), method)[0]
}

// CONTRACT part 3 has the list of the unary calls.
func TestTheCoreMethodsAreTheOnesOfTheContract(t *testing.T) {
	want := []string{
		"remove_instance", "update_instance_config", "list_instances", "get_instance_status",
		"send_plain_text", "send_bulk_same_message", "send_bulk_custom_messages",
		"get_bulk_job", "get_bulk_jobs", "cancel_bulk_job",
	}
	file := buildCore(t)["methods.py"]
	for _, class := range []string{"SyncCoreMethods", "AsyncCoreMethods"} {
		var got []string
		for _, m := range defLine.FindAllStringSubmatch(methodClass(t, file, class), -1) {
			got = append(got, m[1])
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", class, got, want)
		}
	}
}

// Only "async" and "await" are different in the two classes.
func TestTheCoreClassesHaveTheSameMethods(t *testing.T) {
	file := buildCore(t)["methods.py"]
	_, syncBody, _ := strings.Cut(methodClass(t, file, "SyncCoreMethods"), "\"\"\"\n\n")
	_, asyncBody, _ := strings.Cut(methodClass(t, file, "AsyncCoreMethods"), "\"\"\"\n\n")
	asyncBody = strings.NewReplacer("async def ", "def ", "await ", "").Replace(asyncBody)
	if syncBody == "" || oneLine(syncBody) != oneLine(asyncBody) {
		t.Error("SyncCoreMethods and AsyncCoreMethods differ")
	}
}

func TestCoreSignatures(t *testing.T) {
	const sendEnd = "idempotency_key: str | None = None, time_limit: float | None = None) -> Sent["
	for method, want := range map[string]string{
		"list_instances":  "(self, *, time_limit: float | None = None) -> ListInstancesResponse:",
		"remove_instance": "(self, *, instance_name: str, time_limit: float | None = None) -> RemoveInstanceResponse:",
		"get_bulk_jobs":   "(self, *, job_ids: Sequence[str], time_limit: float | None = None) -> GetBulkJobsResponse:",
		"update_instance_config": "(self, *, instance_name: str, new_instance_name: str | None = None, is_enabled: bool | None = None, " +
			"is_used_only_for_send_messages: bool | None = None, webhooks: Sequence[WebhookConfig] | None = None, " +
			"meta_cloud: MetaCloudSettings | None = None, time_limit: float | None = None) -> UpdateInstanceConfigResponse:",
		"send_plain_text": "(self, *, instance_name: str, to: str, text: str, " + sendEnd + "SendPlainTextResponse]:",
		"send_bulk_same_message": "(self, *, instance_name: str, recipients: Sequence[str], text: str, " +
			"pacing: BulkPacing | None = None, " + sendEnd + "BulkJob]:",
		"send_bulk_custom_messages": "(self, *, instance_name: str, messages: Sequence[PlainTextPayload], " +
			"pacing: BulkPacing | None = None, " + sendEnd + "BulkJob]:",
	} {
		if got := signature(coreDefs(t, method)); !strings.HasPrefix(got, "def "+method+want) {
			t.Errorf("%s: got %s", method, got)
		}
	}
}

func TestCoreMethodBodies(t *testing.T) {
	for method, want := range map[string]string{
		"list_instances": "return self._call( LIST_INSTANCES, convert.list_instances_request, " +
			"convert.read_list_instances_response, time_limit=time_limit, )",
		"get_bulk_job": "return self._call( GET_BULK_JOB, lambda: convert.get_bulk_job_request(job_id=job_id), " +
			"convert.read_bulk_job, time_limit=time_limit, )",
		"send_plain_text": "return self._send( SEND_PLAIN_TEXT, lambda key: convert.send_plain_text_request( " +
			"instance_name=instance_name, to=to, text=text, idempotency_key=key, ), convert.read_send_plain_text_response, " +
			"idempotency_key=idempotency_key, time_limit=time_limit, )",
	} {
		if got := oneLine(coreDefs(t, method)); !strings.HasSuffix(got, want) {
			t.Errorf("%s: got %s", method, got)
		}
	}
}

// The streams are written by hand, with these constants.
func TestCoreRPCs(t *testing.T) {
	file := buildCore(t)["methods.py"]
	for _, want := range []string{
		"\nSEND_PLAIN_TEXT: Final = Rpc(\n    path=\"/calyx.core.v1.MessageService/SendPlainText\",\n" +
			"    parse=message_pb2.SendPlainTextResponse.FromString,\n    time_limit=360,\n)\n",
		"    path=\"/calyx.core.v1.MessageService/SendBulkSameMessage\",\n    parse=message_pb2.BulkJob.FromString,\n    time_limit=120,\n",
		"\nCREATE_INSTANCE: Final = Rpc(\n    path=\"/calyx.core.v1.InstanceService/CreateInstance\",\n" +
			"    parse=instance_pb2.CreateInstanceResponse.FromString,\n    time_limit=None,\n)\n",
		"\nSUBSCRIBE: Final = Rpc(\n    path=\"/calyx.core.v1.EventService/Subscribe\",\n" +
			"    parse=event_pb2.Event.FromString,\n    time_limit=None,\n)\n",
	} {
		if !strings.Contains(file, want) {
			t.Errorf("methods.py has no\n%s", want)
		}
	}
	if got := strings.Count(file, " = Rpc("); got != 12 {
		t.Errorf("got %d RPCs, want 12", got)
	}
	if strings.Contains(file, "def create_instance") || strings.Contains(file, "def subscribe") {
		t.Error("methods.py has a stream")
	}
}

func TestCoreDocstrings(t *testing.T) {
	for method, parts := range map[string][]string{
		"remove_instance": {
			`"""Removes an instance with its webhooks`,
			"Args: instance_name: The name of the instance. time_limit: The time limit of the call, in seconds.",
			"or 120 seconds when it has none.",
			"Returns: RemoveInstanceResponse: An empty answer: the instance is removed.",
			"Raises: CalyxError: The call failed. Read `retry` of the error to know what to do. Compare `code` with `CoreCode` and " +
				"`ClientCode`. No connection, or a connection that breaks, gives `CoreCode.UNAVAILABLE`.",
		},
		"update_instance_config": {
			"A setting that is `None` stays as it is.",
			"webhooks: Replaces the whole list of webhooks of the instance. The webhooks, at most one per event. Empty removes all " +
				"webhooks of the instance. `None` keeps the webhooks.",
		},
		"send_plain_text": {
			"idempotency_key: The key of this message: 1 to 255 printable ASCII characters",
			"Calyx keeps a key for 24 hours.", "or 360 seconds when it has none.",
			"Returns: Sent[SendPlainTextResponse]: `data` is the answer, and `idempotency_key` is the key that the call used. A message",
			"gives `CoreCode.UNAVAILABLE`. When `retry` is `RetryAdvice.RETRY_NOW`, call again", "Calyx forgets a key after 24 hours",
			"A cancelled call raises no `CalyxError`",
		},
		"send_bulk_same_message": {
			"idempotency_key: The key of this job:", "recipients: 1 or more recipients, in the format of `to` of `send_plain_text`.",
		},
	} {
		text := oneLine(coreDefs(t, method))
		for _, part := range parts {
			if !strings.Contains(text, part) {
				t.Errorf("%s: the text has no %q:\n%s", method, part, text)
			}
		}
	}
}

// The texts for a program that uses the .proto files are wrong for a program with the SDK.
func TestCoreTextsAreForTheSDK(t *testing.T) {
	files := buildCore(t)
	names := regexp.MustCompile(`"/calyx[^"]*"|class \w+|_pb2\.\w+`)
	for _, file := range []string{"models.py", "methods.py"} {
		text := names.ReplaceAllString(files[file], "")
		for _, part := range []string{
			"bsent", "Unspecified", "ERROR_REASON_", "BULK_JOB_STATUS_", "8 character", "the client", "X-Calyx-Signature",
			"GetBulkJob ", "CancelBulkJob", "WebhookList", "SendPlainTextRequest", "Defaults to", " true", " false", "null", "..\n",
		} {
			if strings.Contains(text, part) {
				t.Errorf("%s has %q", file, part)
			}
		}
	}
}

func TestCoreConverters(t *testing.T) {
	file := buildCore(t)["convert.py"]
	for _, want := range []string{
		"def send_plain_text_request(*, instance_name: object, to: object, text: object, idempotency_key: object) -> " +
			"message_pb2.SendPlainTextRequest: return message_pb2.SendPlainTextRequest( " +
			`instance_name=check_text("instance_name", instance_name), to=check_text("to", to), text=check_text("text", text), ` +
			`idempotency_key=check_optional(check_text, "idempotency_key", idempotency_key), )`,
		"def list_instances_request() -> instance_pb2.ListInstancesRequest: return instance_pb2.ListInstancesRequest()",
		`webhooks=check_optional(_webhook_list, "webhooks", webhooks),`,
		"return instance_pb2.WebhookList(webhooks=check_list(_webhook_config, path, value))",
		`is_enabled=check_optional_bool("is_enabled", is_enabled),`,
		`instance_names=check_list(check_text, "instance_names", instance_names),`,

		"if not isinstance(provider, WhatsAppWebSettings | MetaCloudSettings): raise WrongArgumentError(" +
			`"provider must be a WhatsAppWebSettings or a MetaCloudSettings.")`,
		`whatsapp_web=check_member(WhatsAppWebSettings, _whats_app_web_settings, "provider", provider),`,
		`pairing_code=check_optional(_pairing_code_settings, f"{path}.pairing_code", value.pairing_code),`,
		`device_identity=check_optional(_device_identity, f"{path}.device_identity", value.device_identity),`,

		"def read_bulk_job(message: message_pb2.BulkJob) -> BulkJob: " +
			`require(message.HasField("created_at"), "BulkJob.created_at") return BulkJob( id=message.id,`,
		"status=_read_bulk_job_status(message.status),",
		"created_at=utc_date(message.created_at),",
		`finished_at=if_present(message.HasField("finished_at"), utc_date, message.finished_at),`,
		`error=if_present(message.HasField("error"), str, message.error),`,
		"instances=tuple(map(read_instance_info, message.instances))",
		"def read_remove_instance_response(message: instance_pb2.RemoveInstanceResponse) -> RemoveInstanceResponse: " +
			"return RemoveInstanceResponse()",
		"_BULK_JOB_STATUS: Final[Mapping[int, str]] = { 0: \"UNSPECIFIED\", 1: \"QUEUED\",",
	} {
		// A def can need one line or more lines.
		got := strings.NewReplacer("( ", "(", ", )", ")", " )", ")").Replace(oneLine(file))
		want = strings.NewReplacer("( ", "(", ", )", ")", " )", ")").Replace(want)
		if !strings.Contains(got, want) {
			t.Errorf("convert.py has no\n%s", want)
		}
	}
	// The functions that do not come from the .proto files are hand-written, with their own tests.
	// No call has a required bool, so there is no check_bool.
	if want := "from .._values import (\n    WrongArgumentError,\n    check_constant,\n    check_int32,\n"; !strings.Contains(file, want) {
		t.Errorf("convert.py has no\n%s", want)
	}
	for _, part := range []string{"pairing_qr", "\nclass ", "def check_", "def require", "except"} {
		if strings.Contains(file, part) {
			t.Errorf("convert.py has %q", part)
		}
	}
}

func protoField(s *protospec.Spec, message, name string) *protospec.Field {
	for _, f := range s.Message(message).Fields {
		if f.Name == name {
			return f
		}
	}
	return nil
}

func protoMethod(s *protospec.Spec, name string) *protospec.Method {
	i := slices.IndexFunc(s.Methods, func(m *protospec.Method) bool { return m.Name == name })
	return s.Methods[i]
}

func TestCoreRefusals(t *testing.T) {
	add := func(s *protospec.Spec, message string, f protospec.Field) {
		m := s.Message(message)
		m.Fields = append(m.Fields, &f)
	}
	kind := func(message, field, kind string) func(*protospec.Spec) {
		return func(s *protospec.Spec) { protoField(s, message, field).Kind = kind }
	}
	removeField := func(message, field string) func(*protospec.Spec) {
		return func(s *protospec.Spec) {
			m := s.Message(message)
			m.Fields = slices.DeleteFunc(m.Fields, func(f *protospec.Field) bool { return f.Name == field })
		}
	}
	type refusal struct {
		name, want string
		change     func(s *protospec.Spec)
	}
	cases := []refusal{
		{"a kind of an answer", "the kind bytes of SendPlainTextResponse.message_id is not supported", kind("SendPlainTextResponse", "message_id", "bytes")},
		{"a kind of an argument", "the kind int64 of PlainTextPayload.delay_ms is not supported", kind("PlainTextPayload", "delay_ms", "int64")},
		{"a float in a request", "the kind float of PlainTextPayload.delay_ms is not supported", kind("PlainTextPayload", "delay_ms", "float")},
		{"a map", "the kind map of BulkJob.id is not supported", kind("BulkJob", "id", "map")},
		{"a date in a request", "the type google.protobuf.Timestamp of PlainTextPayload.x is not supported", func(s *protospec.Spec) {
			add(s, "PlainTextPayload", protospec.Field{Name: "x", Comment: "A text.", Kind: "message", Type: timestampType})
		}},
		{"a type of another package", "the type google.protobuf.Any of BulkJob.x is not supported", func(s *protospec.Spec) {
			add(s, "BulkJob", protospec.Field{Name: "x", Comment: "A text.", Kind: "message", Type: "google.protobuf.Any"})
		}},
		{"a list that is required", "WebhookList of SendPlainTextRequest.x is not supported", func(s *protospec.Spec) {
			add(s, "SendPlainTextRequest", protospec.Field{Name: "x", Comment: "A text.", Kind: "message", Type: "WebhookList"})
		}},

		{"no comment of a field", "BulkJob.id has no comment", func(s *protospec.Spec) { protoField(s, "BulkJob", "id").Comment = " " }},
		{"no comment of an argument", "SendPlainTextRequest.to has no comment", func(s *protospec.Spec) { protoField(s, "SendPlainTextRequest", "to").Comment = "" }},
		{"no comment of the key", "SendPlainTextRequest.idempotency_key has no comment", func(s *protospec.Spec) {
			protoField(s, "SendPlainTextRequest", "idempotency_key").Comment = ""
		}},
		{"no comment of a hidden field", "WhatsAppWebSettings.pairing_qr has no comment", func(s *protospec.Spec) {
			protoField(s, "WhatsAppWebSettings", "pairing_qr").Comment = ""
		}},
		{"no comment of a stream argument", "CreateInstanceRequest.webhooks has no comment", func(s *protospec.Spec) {
			protoField(s, "CreateInstanceRequest", "webhooks").Comment = ""
		}},
		{"no comment of a message", "the message BulkJob has no comment", func(s *protospec.Spec) { s.Message("BulkJob").Comment = "" }},
		{"no comment of a request", "the message GetBulkJobRequest has no comment", func(s *protospec.Spec) { s.Message("GetBulkJobRequest").Comment = "" }},
		{"no comment of an enum", "the enum Provider has no comment", func(s *protospec.Spec) { s.Enum("Provider").Comment = "" }},
		{"no comment of a value", "the value Provider.META_CLOUD has no comment", func(s *protospec.Spec) { s.Enum("Provider").Values[2].Comment = "" }},
		{"no comment of an RPC", "the RPC GetBulkJob has no comment", func(s *protospec.Spec) { protoMethod(s, "GetBulkJob").Comment = "" }},
		{"no comment of a stream", "the RPC Subscribe has no comment", func(s *protospec.Spec) { protoMethod(s, "Subscribe").Comment = "" }},

		{"an RPC in no map", "the RPC NewCall has no method", func(s *protospec.Spec) {
			m := *protoMethod(s, "GetBulkJob")
			m.Name = "NewCall"
			s.Methods = append(s.Methods, &m)
		}},
		{"a unary call that is a stream", "the RPC GetBulkJob is in coreCalls, but it is a stream", func(s *protospec.Spec) {
			protoMethod(s, "GetBulkJob").StreamsAnswers = true
		}},
		{"a stream that is a unary call", "the RPC Subscribe is in coreStreams, but it is not a stream of answers only", func(s *protospec.Spec) {
			protoMethod(s, "Subscribe").StreamsAnswers = false
		}},
		{"a stream of requests", "the RPC Subscribe is in coreStreams, but it is not a stream of answers only", func(s *protospec.Spec) {
			protoMethod(s, "Subscribe").StreamsRequests = true
		}},
		{"a call that does not exist", "pygen names the RPC GetBulkJob, but the .proto files do not have it", func(s *protospec.Spec) {
			s.Methods = slices.DeleteFunc(s.Methods, func(m *protospec.Method) bool { return m.Name == "GetBulkJob" })
		}},
		{"a stream that does not exist", "pygen names the RPC Subscribe, but the .proto files do not have it", func(s *protospec.Spec) {
			s.Methods = slices.DeleteFunc(s.Methods, func(m *protospec.Method) bool { return m.Name == "Subscribe" })
		}},
		{"a request of another package", "the .proto files have no message google.protobuf.Empty", func(s *protospec.Spec) {
			protoMethod(s, "ListInstances").Request = "google.protobuf.Empty"
		}},
		{"an answer of another package", "the .proto files have no message google.protobuf.Empty", func(s *protospec.Spec) {
			protoMethod(s, "RemoveInstance").Answer = "google.protobuf.Empty"
		}},
		{"an answer of another package in a stream", "the .proto files have no message google.protobuf.Empty", func(s *protospec.Spec) {
			protoMethod(s, "Subscribe").Answer = "google.protobuf.Empty"
		}},
		{"a list of another package", "the type google.protobuf.ListValue of UpdateInstanceConfigRequest.webhooks is not supported", func(s *protospec.Spec) {
			protoField(s, "UpdateInstanceConfigRequest", "webhooks").Type = "google.protobuf.ListValue"
		}},
		{"a list that has no message", "the type WebhookList of UpdateInstanceConfigRequest.webhooks is not supported", func(s *protospec.Spec) {
			s.Messages = slices.DeleteFunc(s.Messages, func(m *protospec.Message) bool { return m.Name == "WebhookList" })
		}},
		{"a send with no key", "the idempotency key of SendPlainText does not fit coreCalls", removeField("SendPlainTextRequest", "idempotency_key")},
		{"a key of a call that is no send", "the idempotency key of GetBulkJob does not fit coreCalls", func(s *protospec.Spec) {
			add(s, "GetBulkJobRequest", protospec.Field{Name: "idempotency_key", Comment: "A text.", Kind: "string", Optional: true})
		}},
		{"a message in a request and in an answer", "the message IntRange is in a request and in an answer", func(s *protospec.Spec) {
			add(s, "BulkJob", protospec.Field{Name: "x", Comment: "A text.", Kind: "message", Type: "IntRange", Optional: true})
		}},

		{"a secret that does not exist", "pygen names the field WebhookConfig.secret, but the .proto files do not have it", removeField("WebhookConfig", "secret")},
		{"an enum with no class", "the enum ErrorReason of BulkJob.x has no class in coreEnums", func(s *protospec.Spec) {
			add(s, "BulkJob", protospec.Field{Name: "x", Comment: "A text.", Kind: "enum", Type: "ErrorReason"})
		}},
		{"a class of an enum that no field has", "pygen names the enum Provider, but no field has it", removeField("InstanceInfo", "provider")},
		{"a class of an enum that does not exist", "pygen names the enum Provider, but the .proto files do not have it", func(s *protospec.Spec) {
			s.Enums = slices.DeleteFunc(s.Enums, func(e *protospec.Enum) bool { return e.Name == "Provider" })
		}},
		{"a value that is no Python name", `the value "2G" of Provider cannot be a Python constant name`, func(s *protospec.Spec) {
			s.Enum("Provider").Values[1].Name = "2G"
		}},
		{"a oneof in no map", "the oneof SendPlainTextRequest.choice is in no map", func(s *protospec.Spec) {
			protoField(s, "SendPlainTextRequest", "to").Oneof = "choice"
		}},
		{"a oneof of an answer", "the oneof BulkJob.choice is in no map", func(s *protospec.Spec) { protoField(s, "BulkJob", "id").Oneof = "choice" }},
		{"a oneof that does not exist", "pygen names the oneof CreateInstanceRequest.provider_settings, but the .proto files do not have it", func(s *protospec.Spec) {
			removeField("CreateInstanceRequest", "whatsapp_web")(s)
			removeField("CreateInstanceRequest", "meta_cloud")(s)
		}},
		{"a union with the name of an earlier field", "the union CreateInstanceRequest.provider_settings has the name of a field", func(s *protospec.Spec) {
			protoField(s, "CreateInstanceRequest", "webhooks").Name = "provider"
		}},
		{"a union with the name of a later field", "the field CreateInstanceRequest.provider is there two times", func(s *protospec.Spec) {
			add(s, "CreateInstanceRequest", protospec.Field{Name: "provider", Comment: "A text.", Kind: "string"})
		}},
		{"a union with a text", "the member CreateInstanceRequest.x of a union is not a message", func(s *protospec.Spec) {
			add(s, "CreateInstanceRequest", protospec.Field{Name: "x", Comment: "A text.", Kind: "string", Oneof: "provider_settings"})
		}},
		{"a oneof with no shown member", "pygen names the field WhatsAppWebSettings.pairing_code, but the .proto files do not have it", removeField("WhatsAppWebSettings", "pairing_code")},
		{"a list that does not exist", "pygen names the list WebhookList, but no request has it", removeField("UpdateInstanceConfigRequest", "webhooks")},
		{"a list with two fields", "the message WebhookList is not a list", func(s *protospec.Spec) {
			add(s, "WebhookList", protospec.Field{Name: "x", Comment: "A text.", Kind: "string"})
		}},

		{"a changed text that does not exist", `pygen changes the text "in the format of SendPlainTextRequest.to"`, func(s *protospec.Spec) {
			protoField(s, "SendBulkSameMessageRequest", "recipients").Comment = "Recipients."
			protoField(s, "PlainTextPayload", "to").Comment = "The recipient."
		}},
		{"a text with an RPC", `the text of BulkJob.id has "GetBulkJob"`, func(s *protospec.Spec) {
			protoField(s, "BulkJob", "id").Comment = "Use it in GetBulkJob."
		}},
		{"a text with a request", `the text of BulkJob.id has "GetBulkJobRequest"`, func(s *protospec.Spec) {
			protoField(s, "BulkJob", "id").Comment = "The id of GetBulkJobRequest."
		}},
		{"a text with the prefix of an enum", `the text of BulkJob.id has "BULK_JOB_STATUS_"`, func(s *protospec.Spec) {
			protoField(s, "BulkJob", "id").Comment = "It is BULK_JOB_STATUS_QUEUED."
		}},
		{"a text about an absent field", `the text of BulkJob.id has "Absent"`, func(s *protospec.Spec) {
			protoField(s, "BulkJob", "id").Comment = "Absent in a list."
		}},
	}
	for name, want := range map[string]string{
		"class": "the field SendPlainTextRequest.class cannot be a Python name", // a keyword
		"str":   "the field SendPlainTextRequest.str cannot be a Python name",   // a type of an annotation
		"Camel": "the field SendPlainTextRequest.Camel cannot be a Python name",
		"self":  "the argument SendPlainTextRequest.self has a reserved name", "time_limit": "the argument SendPlainTextRequest.time_limit has a reserved name",
		"key": "the argument SendPlainTextRequest.key has a reserved name", "convert": "the argument SendPlainTextRequest.convert has a reserved name",
		"to": "the field SendPlainTextRequest.to is there two times",
	} {
		cases = append(cases, refusal{"the argument " + name, want, func(s *protospec.Spec) {
			add(s, "SendPlainTextRequest", protospec.Field{Name: name, Comment: "A text.", Kind: "string"})
		}})
	}
	// The converters import these names. An argument would hide a function of them.
	for _, name := range coreChecks {
		cases = append(cases, refusal{"the argument " + name, "the argument SendPlainTextRequest." + name + " has a reserved name", func(s *protospec.Spec) {
			add(s, "SendPlainTextRequest", protospec.Field{Name: name, Comment: "A text.", Kind: "string"})
		}})
	}
	for _, name := range []string{"None", "Sequence", "Mapping", "Sent", "Rpc", "WrongArgumentError", "SyncCoreBase", "AsyncCoreBase", "lower"} {
		cases = append(cases, refusal{"the class " + name, "the message " + name + " cannot be a Python class name", func(s *protospec.Spec) {
			s.Message("BulkJob").Name = name
			for _, f := range s.Message("GetBulkJobsResponse").Fields {
				f.Type = name
			}
			for _, m := range s.Methods {
				if m.Answer == "BulkJob" {
					m.Answer = name
				}
			}
		}})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := loadCore(t)
			c.change(s)
			if _, err := buildCoreFiles(s); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("want an error with %q, got %v", c.want, err)
			}
		})
	}
}

// Only a hand-written method can have the argument of a union. A unary call or a class would show no
// type and no text.
func TestCoreUnionsAreOnlyOfAStream(t *testing.T) {
	for _, message := range []string{"GetBulkJobRequest", "PlainTextPayload"} {
		oneof := message + ".choice"
		coreUnions[oneof] = "choice"
		s := loadCore(t)
		m := s.Message(message)
		m.Fields = append(m.Fields, &protospec.Field{Name: "x", Comment: "A text.", Kind: "message", Type: "IntRange", Oneof: "choice"})
		_, err := buildCoreFiles(s)
		delete(coreUnions, oneof)
		if want := "the union " + oneof + " is not of a stream request"; err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want an error with %q, got %v", want, err)
		}
	}
}

// Two oneofs with one argument would be one union, and the program could not give both.
func TestCoreUnionsHaveTheirOwnArgument(t *testing.T) {
	coreUnions["CreateInstanceRequest.choice"] = "provider"
	defer delete(coreUnions, "CreateInstanceRequest.choice")
	s := loadCore(t)
	m := s.Message("CreateInstanceRequest")
	m.Fields = append(m.Fields, &protospec.Field{Name: "x", Comment: "A text.", Kind: "message", Type: "IntRange", Oneof: "choice"})
	_, err := buildCoreFiles(s)
	if want := "the union CreateInstanceRequest.choice has the name of a field"; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("want an error with %q, got %v", want, err)
	}
}

// No message has these shapes today.
func TestCoreShapesOfALaterVersion(t *testing.T) {
	s := loadCore(t)
	for message, f := range map[string]protospec.Field{
		"SendPlainTextRequest": {Name: "x", Comment: "A text.", Kind: "message", Type: "IntRange"},
		"CancelBulkJobRequest": {Name: "x", Comment: "A text.", Kind: "string", Repeated: true},
		"IntRange":             {Name: "x", Comment: "A text.", Kind: "string", Repeated: true},
		"BulkJob":              {Name: "x", Comment: "A text.", Kind: "string", Repeated: true},
		"WebhookSettings":      {Name: "x", Comment: "A text.", Kind: "enum", Type: "Provider", Optional: true},
	} {
		s.Message(message).Fields = append(s.Message(message).Fields, &f)
	}
	files, err := buildCoreFiles(s)
	if err != nil {
		t.Fatal(err)
	}
	for file, parts := range map[string][]string{
		// A list has no default.
		"methods.py": {"        x: IntRange,\n", " x: Sequence[str],"},
		"models.py": {
			"from collections.abc import Sequence\n", "    x: Sequence[str]\n", "    x: tuple[str, ...]\n", "    x: str | None = None\n",
		},
		"convert.py": {
			`x=_int_range("x", x),`, `x=check_list(check_text, f"{path}.x", value.x),`, "x=tuple(message.x),",
			`x=if_present(message.HasField("x"), _read_provider, message.x),`,
		},
	} {
		for _, part := range parts {
			if !strings.Contains(string(files[file]), part) {
				t.Errorf("%s has no %q", file, part)
			}
		}
	}
}

func TestRunCoreWritesAndChecks(t *testing.T) {
	out := filepath.Join(t.TempDir(), "_generated")
	pygen := func(extra ...string) (int, string) {
		t.Helper()
		var messages bytes.Buffer
		args := []string{"-core", "-proto", inputPaths(toolsDir).proto, "-out", out}
		return run(append(args, extra...), &messages), messages.String()
	}

	if code, text := pygen("-check"); code != 1 || !strings.Contains(text, "convert.py is out of date") {
		t.Errorf("check with no folder: got %d, %q; want 1 and the name of the file", code, text)
	}
	if _, err := os.Stat(out); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("-check wrote the output folder: %v", err)
	}
	if code, text := pygen(); code != 0 || text != "" {
		t.Errorf("write: got %d, %q; want 0 and no text", code, text)
	}
	if code, text := pygen("-check"); code != 0 || text != "" {
		t.Errorf("check after write: got %d, %q; want 0 and no text", code, text)
	}
	if code, text := pygen("-proto", t.TempDir()); code != 2 || !strings.Contains(text, "no .proto file") {
		t.Errorf("no .proto file: got %d, %q; want 2 and the reason", code, text)
	}
}

func TestDefaultOutputFolders(t *testing.T) {
	in := inputPaths(".")
	if !strings.HasSuffix(filepath.ToSlash(in.out), "python/backend/src/calyx_client_sdk/_generated") ||
		!strings.HasSuffix(filepath.ToSlash(in.coreOut), "python/core/src/calyx_client_sdk_core/_generated") {
		t.Errorf("got %q and %q", in.out, in.coreOut)
	}
}

func TestProtocErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, filepath.FromSlash("calyx/core/v1/bad.proto"))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("syntax = \"proto3\";\nmessage {"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := descriptorSet(dir); err == nil || !strings.Contains(err.Error(), "bad.proto") {
		t.Errorf("a file with a mistake: want an error with the name of the file, got %v", err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := descriptorSet(dir); err == nil || !strings.Contains(err.Error(), "install protoc") {
		t.Errorf("no protoc: want an error that says what to do, got %v", err)
	}
}
