package main

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"calyx/sdktools/internal/protospec"
)

const (
	corePackage   = "calyx.core.v1"
	timestampType = "google.protobuf.Timestamp"
	keyField      = "idempotency_key"
)

// coreCall is a unary call of the core.
type coreCall struct {
	// limit is the default time limit in seconds.
	limit int
	// sent is what a call with an idempotency key sends, "" for a call with no key.
	sent string
}

var coreCalls = map[string]coreCall{
	"RemoveInstance":         {limit: 120},
	"UpdateInstanceConfig":   {limit: 120},
	"ListInstances":          {limit: 120},
	"GetInstanceStatus":      {limit: 120},
	"SendPlainText":          {limit: 360, sent: "message"},
	"SendBulkSameMessage":    {limit: 120, sent: "job"},
	"SendBulkCustomMessages": {limit: 120, sent: "job"},
	"GetBulkJob":             {limit: 120},
	"GetBulkJobs":            {limit: 120},
	"CancelBulkJob":          {limit: 120},
}

// coreStreams tells, for each stream with a hand-written method, if its answer has a class. The
// events of Subscribe have hand-written classes.
var coreStreams = map[string]bool{"CreateInstance": true, "Subscribe": false}

// coreEnums are the enums with a class of constants, in the order of the file.
var coreEnums = []string{"Provider", "DeviceIdentity", "InstanceStatus", "BulkJobStatus"}

// coreSecrets are not in the repr of their class.
var coreSecrets = []string{
	"WebhookConfig.secret",
	"MetaCloudSettings.access_token",
	"MetaCloudSettings.app_secret",
	"CreateInstanceResponse.pairing_code",
}

// coreUnions gives the one argument of a oneof. The class of the argument selects the member.
var coreUnions = map[string]string{"CreateInstanceRequest.provider_settings": "provider"}

// coreSingles gives the one member of a oneof that a program sees.
var coreSingles = map[string]string{"WhatsAppWebSettings.pairing_method": "pairing_code"}

// coreLists are the messages that only hold a list. They let a request tell an empty list from no
// list. A program gives a Python list, and the text says what None means.
var coreLists = map[string]string{"WebhookList": "`None` keeps the webhooks."}

// coreTextChanges are the texts of the .proto files for a program with its own gRPC code, or that are
// not exact, and the texts for a program with this SDK.
var coreTextChanges = []struct{ proto, sdk string }{
	{"Any field except webhooks restarts the instance when it is present, and a direct message that waits gets UNAVAILABLE.",
		"A setting that is `None` stays as it is. Each setting that the call gives, except `webhooks`, restarts the instance. " +
			"A direct message that waits then gets UNAVAILABLE."},
	{"answers once WhatsApp accepted it", "returns when WhatsApp accepted it"},
	{"ERROR_REASON_INSTANCE_QUEUE_FULL means too many", "The error code `CoreCode.INSTANCE_QUEUE_FULL` means that too many"},
	{"With a known idempotency_key and the same request, the core returns the stored provider message id at once, with replayed true,",
		"With a known `idempotency_key` and the same arguments, the core gives the stored `message_id` at once, with `replayed` true,"},
	{"Bulk sends are stored and processed in the background. They survive restarts of the core.",
		"Starts a bulk job: one text for many recipients. The core stores the job and sends its messages in the background. " +
			"The job survives a restart of the core."},
	{"Like SendBulkSameMessage, but each message has its own text.",
		"Starts a bulk job: each message has its own text. The core stores the job and sends its messages in the background. " +
			"The job survives a restart of the core."},

	{"Absent means", "`None` means"},
	{"Absent when", "`None` when"},
	{"Absent for", "`None` for"},
	{"Absent before", "`None` before"},
	{"Absent keeps", "`None` keeps"},
	{"Absent: false for", "`None`: false for"},
	{"Unspecified: the core picks", "`None`: the core picks"},
	{"in the format of SendPlainTextRequest.to", "in the format of `to` of `send_plain_text`"},
	{"The answer of GetBulkJobs.", "The jobs that the core has."},
	{"CancelBulkJob stopped the job", "`cancel_bulk_job` stopped the job"},
	{"GetBulkJob and CancelBulkJob give false.", "`get_bulk_job`, `get_bulk_jobs` and `cancel_bulk_job` give false."},
	{"see the X-Calyx-Signature header", "`verify_webhook` of `calyx_client_sdk` checks the signature"},
	{"the client draws the image", "your program draws the image"},
	{"Empty: the instance is removed.", "An empty answer: the instance is removed."},
	{"Empty: the changes are stored.", "An empty answer: the changes are stored."},
	{"The core picks one entry at random.", "In a request, the core picks one entry at random. In an answer, there is no value."},
	{"Set only on the last update (CONNECTED) of a WhatsApp Web pairing: the device identity that the core used (requested or random) " +
		"and the locale of the instance.",
		"The device identity that the core used (requested or random). Only the last update (CONNECTED) of a WhatsApp Web pairing " +
			"has it. Every other update has UNSPECIFIED."},
	{"Set only on the last update of a WHATSAPP_WEB pairing.", "`None` except on the last update of a WHATSAPP_WEB pairing."},

	{"the pairing code: 9 characters with a hyphen, for example \"ABCD-2345\".",
		"the pairing code, for example ABCD-2345. Keep it secret: with it, a person can link a WhatsApp account to this instance."},
	{"Values below the limits of the provider are rejected. META_CLOUD ignores pause_every and pause_duration.",
		"A value below the limits of the provider is rejected. META_CLOUD ignores `pause_every` and `pause_duration`."},
}

// coreChecks are the functions of the hand-written module _values.py that the converters can import.
var coreChecks = strings.Fields("check_bool check_constant check_int32 check_list check_member check_optional " +
	"check_optional_bool check_text if_present require utc_date")

// coreReserved are the arguments of each method, and the names that the body of a method uses.
var coreReserved = append(strings.Fields("self time_limit key convert"), coreChecks...)

// coreReservedClasses are the names that the generated files import or use.
var coreReservedClasses = strings.Fields("Sequence Mapping Final Sent Rpc SyncCoreBase AsyncCoreBase WrongArgumentError")

// coreScalars gives the Python type of a scalar kind, and the function that checks it in a request.
// A kind with no function is for answers only.
var coreScalars = map[string]struct{ pythonType, check string }{
	"string": {"str", "check_text"},
	"bool":   {"bool", "check_bool"},
	"int32":  {"int", "check_int32"},
	"float":  {"float", ""},
}

// descriptorSet runs the protoc of the host on the .proto files of the core.
func descriptorSet(protoDir string) ([]byte, error) {
	files, err := filepath.Glob(filepath.Join(protoDir, filepath.FromSlash(strings.ReplaceAll(corePackage, ".", "/")), "*.proto"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s has no .proto file of %s", protoDir, corePackage)
	}
	dir, err := os.MkdirTemp("", "pygen")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "core.binpb")
	args := []string{"--proto_path=" + protoDir, "--include_source_info", "--include_imports", "--descriptor_set_out=" + out}
	if text, err := exec.Command("protoc", append(args, files...)...).CombinedOutput(); errors.Is(err, exec.ErrNotFound) {
		return nil, fmt.Errorf("install protoc on this machine, because pygen -core reads the .proto files with it: %w", err)
	} else if err != nil {
		return nil, fmt.Errorf("protoc: %w\n%s", err, bytes.TrimSpace(text))
	}
	return os.ReadFile(out)
}

// coreField is a field of a message. For a union, proto is nil.
type coreField struct {
	pyField
	proto   *protospec.Field
	members []*protospec.Field
}

// coreWriter writes the files of the core package.
type coreWriter struct {
	spec *protospec.Spec
	// isRequest has each message that the program uses: true for a request and false for an answer.
	isRequest map[string]bool
	fields    map[string][]coreField
	// calls are the top-level requests. They have no class.
	calls []string
	// seen are the fields, oneofs, lists, enums and changed texts that exist.
	seen map[string]bool
	// checkedEnums are the enums of a request, and readEnums the ones of an answer.
	checkedEnums, readEnums map[string]bool
	protoNames              *regexp.Regexp
	methods                 []coreMethod
}

// buildCoreFiles returns the generated files by their path in the output folder.
func buildCoreFiles(s *protospec.Spec) (map[string][]byte, error) {
	w := &coreWriter{
		spec: s, isRequest: map[string]bool{}, fields: map[string][]coreField{}, seen: map[string]bool{},
		checkedEnums: map[string]bool{}, readEnums: map[string]bool{}, protoNames: protoNames(s),
	}
	for _, m := range s.Methods {
		if err := w.addRPC(m); err != nil {
			return nil, err
		}
	}
	classes, err := w.modelsFile()
	if err != nil {
		return nil, err
	}
	if err := w.checkLists(); err != nil {
		return nil, err
	}
	return map[string][]byte{
		"__init__.py": []byte(header),
		"models.py":   classes,
		"convert.py":  w.convertFile(),
		"methods.py":  w.methodsFile(),
	}, nil
}

// protoNames finds the names that only a program with its own gRPC code knows.
func protoNames(s *protospec.Spec) *regexp.Regexp {
	words := slices.Sorted(maps.Keys(coreLists))
	for _, m := range s.Methods {
		words = append(words, m.Name, m.Request)
	}
	prefixes := []string{"ERROR_REASON_"}
	for _, e := range s.Enums {
		prefixes = append(prefixes, e.Prefix)
	}
	return regexp.MustCompile(`\b(?:` + strings.Join(words, "|") + `|[Aa]bsent)\b|\b(?:` + strings.Join(prefixes, "|") + `)`)
}

// comment fails when a declaration has no comment. It returns the comment on one line.
func comment(owner, text string) (string, error) {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return "", fmt.Errorf("%s has no comment", owner)
	}
	return text, nil
}

// text returns a comment as a text for the docstrings.
func (w *coreWriter) text(owner, text string) (string, error) {
	text, err := comment(owner, text)
	if err != nil {
		return "", err
	}
	for _, change := range coreTextChanges {
		if strings.Contains(text, change.proto) {
			w.seen[change.proto] = true
			text = strings.ReplaceAll(text, change.proto, change.sdk)
		}
	}
	if found := w.protoNames.FindString(text); found != "" {
		return "", fmt.Errorf("the text of %s has %q", owner, found)
	}
	return pythonText(text), nil
}

// addRPC loads the messages of an RPC, and adds the method of a unary call.
func (w *coreWriter) addRPC(rpc *protospec.Method) error {
	owner := "the RPC " + rpc.Name
	call, isCall := coreCalls[rpc.Name]
	hasClass, isStream := coreStreams[rpc.Name]
	switch {
	case !isCall && !isStream:
		return fmt.Errorf("%s has no method: it is not in coreCalls or coreStreams", owner)
	case isCall && (rpc.StreamsAnswers || rpc.StreamsRequests):
		return fmt.Errorf("%s is in coreCalls, but it is a stream", owner)
	case isStream && (!rpc.StreamsAnswers || rpc.StreamsRequests):
		return fmt.Errorf("%s is in coreStreams, but it is not a stream of answers only", owner)
	}
	for _, name := range []string{rpc.Request, rpc.Answer} {
		if w.spec.Message(name) == nil {
			return fmt.Errorf("%s: the .proto files have no message %s", owner, name)
		}
	}
	w.seen[rpc.Name] = true
	if _, err := comment(owner, rpc.Comment); err != nil {
		return err
	}
	w.calls = append(w.calls, rpc.Request)
	if err := w.load(rpc.Request, true); err != nil {
		return err
	}
	if isCall || hasClass {
		if err := w.load(rpc.Answer, false); err != nil {
			return err
		}
	}
	hasKey := false
	for _, f := range w.fields[rpc.Request] {
		if f.name == keyField {
			hasKey = true
		} else if slices.Contains(coreReserved, f.name) {
			return fmt.Errorf("the argument %s has a reserved name", f.id)
		}
	}
	if hasKey != (call.sent != "") {
		return fmt.Errorf("the idempotency key of %s does not fit coreCalls", rpc.Name)
	}
	if isCall {
		return w.addMethod(rpc, call)
	}
	return nil
}

// load reads a message, and the messages of its fields.
func (w *coreWriter) load(name string, request bool) error {
	if old, ok := w.isRequest[name]; ok {
		if old != request {
			return fmt.Errorf("the message %s is in a request and in an answer", name)
		}
		return nil
	}
	w.isRequest[name] = request
	m := w.spec.Message(name)
	if _, err := comment("the message "+name, m.Comment); err != nil {
		return err
	}
	if !classPattern.MatchString(name) || slices.Contains(keywords, name) || slices.Contains(coreReservedClasses, name) {
		return fmt.Errorf("the message %s cannot be a Python class name", name)
	}
	if coreLists[name] != "" && (len(m.Fields) != 1 || !m.Fields[0].Repeated) {
		return fmt.Errorf("the message %s is not a list", name)
	}
	var fields []coreField
	unions := map[string]int{}
	for _, p := range m.Fields {
		oneof := name + "." + p.Oneof
		union, isUnion := coreUnions[oneof]
		single, isSingle := coreSingles[oneof]
		if p.Oneof != "" {
			if !request || !isUnion && !isSingle {
				return fmt.Errorf("the oneof %s is in no map", oneof)
			}
			w.seen[oneof] = true
		}
		optional := *p
		optional.Optional = p.Optional || isSingle
		f, err := w.field(name, &optional, request)
		if err != nil {
			return err
		}
		switch {
		case isSingle && p.Name != single:
			continue
		case isUnion:
			if !w.isStreamRequest(name) {
				return fmt.Errorf("the union %s is not of a stream request", oneof)
			}
			if p.Kind != "message" || p.Repeated {
				return fmt.Errorf("the member %s of a union is not a message", f.id)
			}
			i, ok := unions[oneof]
			if !ok {
				if hasField(fields, union) {
					return fmt.Errorf("the union %s has the name of a field", oneof)
				}
				i = len(fields)
				unions[oneof] = i
				fields = append(fields, coreField{pyField: pyField{id: oneof, name: union, required: true}})
			}
			fields[i].members = append(fields[i].members, p)
			continue
		}
		if hasField(fields, f.name) {
			return fmt.Errorf("the field %s is there two times", f.id)
		}
		w.seen[f.id] = true
		fields = append(fields, f)
	}
	w.fields[name] = fields
	return nil
}

func hasField(fields []coreField, name string) bool {
	return slices.ContainsFunc(fields, func(f coreField) bool { return f.name == name })
}

// isStreamRequest tells if only hand-written methods use a message as their request. A union needs
// one, because no generated text or type describes it.
func (w *coreWriter) isStreamRequest(message string) bool {
	found := false
	for _, rpc := range w.spec.Methods {
		if rpc.Request == message {
			found = true
			if _, isStream := coreStreams[rpc.Name]; !isStream {
				return false
			}
		}
	}
	return found
}

func (w *coreWriter) field(message string, p *protospec.Field, request bool) (coreField, error) {
	f := coreField{proto: p, pyField: pyField{id: message + "." + p.Name, name: p.Name}}
	if !fieldPattern.MatchString(p.Name) || slices.Contains(keywords, p.Name) || slices.Contains(shadowed, p.Name) {
		return f, fmt.Errorf("the field %s cannot be a Python name", f.id)
	}
	var err error
	if p.Name == keyField && slices.Contains(w.calls, message) {
		// Each send has the same text.
		_, err = comment(f.id, p.Comment)
	} else {
		f.text, err = w.text(f.id, p.Comment)
	}
	if err != nil {
		return f, err
	}
	if err := w.setType(&f, request); err != nil {
		return f, err
	}
	// None is the unspecified value of an enum in a request.
	f.nullable = p.Optional || request && p.Kind == "enum" && !p.Repeated
	f.required = !f.nullable
	f.secret = slices.Contains(coreSecrets, f.id)
	return f, nil
}

func (w *coreWriter) setType(f *coreField, request bool) error {
	p := f.proto
	unsupported := fmt.Errorf("the type %s of %s is not supported", p.Type, f.id)
	scalar, isScalar := coreScalars[p.Kind]
	listText, isList := coreLists[p.Type]
	switch {
	case isScalar && (!request || scalar.check != ""):
		f.baseType = scalar.pythonType
	case p.Kind == "enum":
		if !slices.Contains(coreEnums, p.Type) {
			return fmt.Errorf("the enum %s of %s has no class in coreEnums", p.Type, f.id)
		}
		w.seen[p.Type] = true
		if request {
			w.checkedEnums[p.Type] = true
		} else {
			w.readEnums[p.Type] = true
		}
		f.baseType = "str"
		f.text += fmt.Sprintf(" The class `%s` has the known values.", p.Type)
	case p.Kind == "message" && p.Type == timestampType && !request:
		f.baseType = "datetime"
	case p.Kind == "message" && w.spec.Message(p.Type) == nil:
		return unsupported
	case p.Kind == "message" && isList:
		if !request || !p.Optional || p.Repeated {
			return unsupported
		}
		if err := w.load(p.Type, true); err != nil {
			return err
		}
		w.seen[p.Type] = true
		item := w.fields[p.Type][0]
		f.baseType = item.baseType
		f.text += " " + item.text + " " + listText
	case p.Kind == "message":
		if err := w.load(p.Type, request); err != nil {
			return err
		}
		f.baseType = p.Type
	default:
		return fmt.Errorf("the kind %s of %s is not supported", p.Kind, f.id)
	}
	switch {
	case p.Repeated && request:
		f.baseType = "Sequence[" + f.baseType + "]"
	case p.Repeated:
		f.baseType = "tuple[" + f.baseType + ", ...]"
	}
	return nil
}

// checkLists fails when a list of this file names something that the .proto files do not have.
func (w *coreWriter) checkLists() error {
	for _, list := range []struct {
		kind, problem string
		names         []string
	}{
		{"RPC", "the .proto files do not have it", slices.Sorted(maps.Keys(coreCalls))},
		{"RPC", "the .proto files do not have it", slices.Sorted(maps.Keys(coreStreams))},
		{"oneof", "the .proto files do not have it", slices.Sorted(maps.Keys(coreUnions))},
		{"oneof", "the .proto files do not have it", slices.Sorted(maps.Keys(coreSingles))},
		{"list", "no request has it", slices.Sorted(maps.Keys(coreLists))},
		{"enum", "no field has it", coreEnums},
		{"field", "the .proto files do not have it", coreSecrets},
	} {
		for _, name := range list.names {
			if !w.seen[name] {
				return fmt.Errorf("pygen names the %s %s, but %s", list.kind, name, list.problem)
			}
		}
	}
	for oneof, member := range coreSingles {
		message, _, _ := strings.Cut(oneof, ".")
		if !w.seen[message+"."+member] {
			return fmt.Errorf("pygen names the field %s.%s, but the .proto files do not have it", message, member)
		}
	}
	for _, change := range coreTextChanges {
		if !w.seen[change.proto] {
			return fmt.Errorf("pygen changes the text %q, but the .proto files do not have it", change.proto)
		}
	}
	return nil
}
