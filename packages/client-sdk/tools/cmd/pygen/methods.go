package main

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"calyx/sdktools/internal/spec"
)

// pathArguments gives the name of the path argument, by the part of the path before it.
var pathArguments = map[string]string{
	"api-keys":  "api_key_id",
	"clients":   "client_id",
	"instances": "instance_id",
	"bulk-jobs": "job_id",
}

// overload is one way to call a method. It names the fields of the body in the order of the
// signature.
type overload struct {
	// literals gives the one value of a required enum.
	literals map[string]string
	// required can have fields that the schema does not require.
	required, optional []string
}

var overloads = map[string][]overload{
	"createBulkJob": {
		{required: []string{"recipients", "text"}, optional: []string{"pacing"}},
		{required: []string{"messages"}, optional: []string{"pacing"}},
	},
	"createInstance": {
		{
			literals: map[string]string{"provider": "WHATSAPP_WEB"},
			required: []string{"provider", "display_name", "locale"},
			optional: []string{"pairing", "phone", "device_identity", "send_only", "webhooks", "client_id"},
		},
		{
			literals: map[string]string{"provider": "META_CLOUD"},
			required: []string{"provider", "display_name", "meta_credentials"},
			optional: []string{"webhooks", "client_id"},
		},
	},
}

// statusTexts has the operations that return the HTTP status, because their 2xx statuses differ in
// meaning.
var statusTexts = map[string]string{
	"removeInstance": "The HTTP status. It is 204 when the instance is removed, and 202 when the removal goes on in the background.",
}

// createTexts gives the record and the list method of each create call.
var createTexts = map[string]struct{ record, list string }{
	"createClient":   {"client", "list_clients"},
	"createInstance": {"instance", "list_instances"},
}

// sendTexts gives what each send sends.
var sendTexts = map[string]string{"sendMessage": "message", "createBulkJob": "job"}

// textChanges are the texts of backend.json for a program that writes its own HTTP calls, and the
// texts for a program with this SDK. A call of the SDK always has a key.
var textChanges = []struct{ api, sdk string }{
	{" Send an Idempotency-Key header.", ""},
	{" Without a key, do not retry automatically.", ""},
	{"wait for the time in Retry-After", "wait for `retry_after` seconds"},
	{"A resend with a known Idempotency-Key", "A resend with a known key"},
	{"With an Idempotency-Key, retry with the same key", "Retry with the same key"},
	{"Without a key, the job cannot be read", "Without this retry, the job cannot be read"},
	{"Do not start it again automatically.", "Do not start it again with a new key."},
}

// httpText is a text that tells how to write an HTTP call.
var httpText = regexp.MustCompile(`Idempotency-Key|Retry-After|\b(GET|POST|PUT|PATCH|DELETE) /`)

// The texts that a call of the backend and a call of the core share.
const (
	sentText   = "`data` is the answer, and `idempotency_key` is the key that the call used. "
	failedText = "The call failed. Read `retry` of the error to know what to do."
	sendAdvice = " When `retry` is `RetryAdvice.RETRY_NOW`, call again with the same arguments and the `idempotency_key` of " +
		"the error. When it is `RetryAdvice.RETRY_LATER`, do the same, but first wait for `retry_after` seconds or more. " +
		"If it is 0, choose the time to wait. " +
		"Calyx forgets a key after 24 hours, so do not call again after that time. A cancelled call raises no `CalyxError`, " +
		"and a key that the SDK made is lost. A cancelled call with no stored key cannot be repeated safely."
)

const csrfError = "403 csrf_check_failed"

var (
	errorList = regexp.MustCompile(`^(.*?) ?Errors: (.*?)\.(?: (.*))?$`)
	errorItem = regexp.MustCompile(`^[1-5][0-9]{2} [a-z_]+( \([^()]*\))?$`)
)

// method is one method of the generated classes.
type method struct {
	op spec.Operation
	// constant is the name of the operation in operations.py.
	name, constant string
	// result is the return type, and reader makes it from an answer.
	result, reader string
	// answer is the class of the answer, "" for none.
	answer string
	// path is the path argument, nil for none.
	path *pyField
	body []pyField
	// variants has the body of each overload.
	variants [][]pyField
	// last are the arguments of every method of its kind: the key of a send and the time limit.
	last []pyField
	// The parts of the docstring.
	paragraphs []string
	returns    string
	raises     string
}

// newMethods returns the methods of the normal operations. The stream has a method that is
// written by hand.
func newMethods(w *modelWriter) ([]method, error) {
	var out []method
	for _, op := range w.doc.Operations {
		if op.TimeLimit == 0 {
			continue
		}
		m, err := newMethod(w, op)
		if err != nil {
			return nil, fmt.Errorf("the operation %s: %w", op.ID, err)
		}
		out = append(out, m)
	}
	return out, checkMethodLists(w.doc, out)
}

// checkMethodLists fails when a list of this file names something that backend.json does not have.
func checkMethodLists(doc *spec.Doc, methods []method) error {
	ids, names, paths, texts := map[string]bool{}, map[string]bool{}, map[string]bool{}, ""
	for _, m := range methods {
		ids[m.op.ID] = true
		names[m.name] = true
		texts += m.op.Description + "\n"
		if m.path != nil {
			paths[m.path.name] = true
		}
	}
	for _, list := range [][]string{
		slices.Sorted(maps.Keys(overloads)), slices.Sorted(maps.Keys(statusTexts)),
		slices.Sorted(maps.Keys(createTexts)), slices.Sorted(maps.Keys(sendTexts)),
	} {
		for _, id := range list {
			if !ids[id] {
				return fmt.Errorf("pygen names the operation %s, but backend.json does not have it", id)
			}
		}
	}
	for part, name := range pathArguments {
		if !paths[name] {
			return fmt.Errorf("pygen names the path %s, but no included operation has it", part)
		}
	}
	for _, change := range textChanges {
		if !strings.Contains(texts, change.api) {
			return fmt.Errorf("pygen changes the text %q, but backend.json does not have it", change.api)
		}
	}
	for id, create := range createTexts {
		if !names[create.list] {
			return fmt.Errorf("pygen names the method %s, but backend.json does not have it", create.list)
		}
		if !slices.Contains(doc.CreateCalls.Operations, id) {
			return fmt.Errorf("pygen names the create call %s, but create-calls.json does not have it", id)
		}
	}
	return nil
}

func newMethod(w *modelWriter, op spec.Operation) (method, error) {
	m := method{op: op, name: methodName(op.ID)}
	m.constant = strings.ToUpper(m.name)
	if !fieldPattern.MatchString(m.name) || slices.Contains(keywords, m.name) {
		return m, fmt.Errorf("the id cannot be a Python method name")
	}
	for _, step := range []func(*modelWriter) error{m.setResult, m.setPath, m.setBody, m.setTexts, m.checkTexts} {
		if err := step(w); err != nil {
			return m, err
		}
	}
	return m, nil
}

var (
	lowerUpper = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	wordAfter  = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`)
)

// methodName gives the operation id in snake_case: list_api_keys for listAPIKeys.
func methodName(id string) string {
	id = lowerUpper.ReplaceAllString(id, "${1}_${2}")
	return strings.ToLower(wordAfter.ReplaceAllString(id, "${1}_${2}"))
}

func (m *method) setResult(w *modelWriter) error {
	if m.op.Answer != nil {
		if m.answer = w.pythonType(*m.op.Answer, false); m.answer == "" {
			return fmt.Errorf("the answer has no class")
		}
	}
	text, isStatus := statusTexts[m.op.ID]
	if isStatus != (len(m.op.Statuses) > 1) || isStatus && m.answer != "" {
		return fmt.Errorf("the statuses %v and the answer do not fit statusTexts", m.op.Statuses)
	}
	switch {
	case isStatus:
		m.result, m.reader, m.returns = "int", "read_status", text
	case m.answer != "":
		m.result, m.reader = m.answer, "read_model("+m.answer+")"
		// The model writer checked this text.
		m.returns = pythonText(w.doc.Schemas[m.op.Answer.Ref].Description)
	default:
		m.result, m.reader = "None", "read_nothing"
	}
	if m.op.IdempotencyKey {
		if m.answer == "" {
			return fmt.Errorf("a send needs an answer")
		}
		m.result = "Sent[" + m.answer + "]"
		m.returns = sentText + m.returns
	}
	return nil
}

func (m *method) setPath(_ *modelWriter) error {
	if !strings.Contains(m.op.Path, "{") && len(m.op.PathParams) == 0 {
		return nil
	}
	before, after, _ := strings.Cut(m.op.Path, "/{id}")
	name := pathArguments[before[strings.LastIndex(before, "/")+1:]]
	if name == "" || strings.Contains(after, "{") || len(m.op.PathParams) != 1 {
		return fmt.Errorf("pathArguments does not have the path %s", m.op.Path)
	}
	text, err := checkedText("the path argument", m.op.PathParams[0].Description)
	if err != nil {
		return err
	}
	m.path = &pyField{name: name, baseType: "str", required: true, text: text}
	return nil
}

func (m *method) setBody(w *modelWriter) error {
	if m.op.Request != nil {
		m.body = w.fields[m.op.Request.Ref]
	}
	for _, f := range m.body {
		if slices.Contains([]string{"self", "idempotency_key", "time_limit"}, f.name) || m.path != nil && f.name == m.path.name {
			return fmt.Errorf("the method has another argument with the name of %s", f.id)
		}
	}
	for _, o := range overloads[m.op.ID] {
		fields, err := m.variant(o)
		if err != nil {
			return err
		}
		m.variants = append(m.variants, fields)
	}
	if m.variants != nil {
		if err := m.checkOverloads(w.doc.Schemas[m.op.Request.Ref]); err != nil {
			return err
		}
	}
	sent, isSend := sendTexts[m.op.ID]
	if isSend != m.op.IdempotencyKey {
		return fmt.Errorf("the idempotency key does not fit sendTexts")
	}
	if isSend {
		m.last = append(m.last, keyArgument(sent))
	}
	m.last = append(m.last, timeLimitArgument(m.op.TimeLimit))
	return nil
}

// keyArgument returns the argument of the idempotency key. sent is what the call sends.
func keyArgument(sent string) pyField {
	return pyField{
		name: "idempotency_key", baseType: "str", nullable: true,
		text: fmt.Sprintf("The key of this %[1]s: 1 to 255 printable ASCII characters, with no space. Make it with "+
			"`new_idempotency_key` and store it before the call. Use a new key for each %[1]s. Use the same key only to repeat "+
			"the same call. Calyx keeps a key for 24 hours. In that time, a second call with the same key does not send "+
			"again. Without it, the SDK makes a new key. All callers of one instance share its keys.", sent),
	}
}

// timeLimitArgument returns the argument of the time limit. limit is the default in seconds.
func timeLimitArgument(limit int) pyField {
	return pyField{
		name: "time_limit", baseType: "float", nullable: true,
		text: fmt.Sprintf("The time limit of the call, in seconds. Without it, the `time_limit` of the SDK client applies, or "+
			"%d seconds when it has none.", limit),
	}
}

// variant returns the body arguments of an overload.
func (m *method) variant(o overload) ([]pyField, error) {
	var out []pyField
	for _, name := range slices.Concat(o.required, o.optional) {
		i := slices.IndexFunc(m.body, func(f pyField) bool { return f.name == name })
		if i < 0 {
			return nil, fmt.Errorf("an overload names the field %s, but the body does not have it", name)
		}
		f := m.body[i]
		if slices.Contains(o.required, name) {
			f.required, f.nullable = true, false
		}
		if value, ok := o.literals[name]; ok {
			f.baseType = "Literal[" + pythonString(value) + "]"
		}
		out = append(out, f)
	}
	for name := range o.literals {
		if !slices.Contains(o.required, name) {
			return nil, fmt.Errorf("a literal of an overload is for %s, which the overload does not require", name)
		}
	}
	for _, f := range m.body {
		if f.required && !slices.Contains(o.required, f.name) {
			return nil, fmt.Errorf("the body needs %s, but an overload does not", f.id)
		}
	}
	return out, nil
}

// checkOverloads fails when the overloads do not have each field, or each value of a literal enum.
func (m *method) checkOverloads(schema *spec.Schema) error {
	for _, f := range m.body {
		var literals []string
		used := false
		for _, o := range overloads[m.op.ID] {
			used = used || slices.Contains(o.required, f.name) || slices.Contains(o.optional, f.name)
			if value, ok := o.literals[f.name]; ok {
				literals = append(literals, value)
			}
		}
		if !used {
			return fmt.Errorf("the field %s is in no overload", f.id)
		}
		if literals == nil {
			continue
		}
		var values []string
		for _, v := range schema.Property(f.name).Enum {
			if v != nil {
				values = append(values, *v)
			}
		}
		if !slices.Equal(literals, values) {
			return fmt.Errorf("the overloads have the values %v of %s, and backend.json has %v", literals, f.id, values)
		}
	}
	return nil
}

func (m *method) setTexts(w *modelWriter) error {
	summary, err := checkedText("the summary", m.op.Summary)
	if err != nil {
		return err
	}
	description := strings.Join(strings.Fields(m.op.Description), " ")
	for _, change := range textChanges {
		description = strings.ReplaceAll(description, change.api, change.sdk)
	}
	parts := errorList.FindStringSubmatch(description)
	if parts == nil || strings.Count(description, "Errors:") != 1 {
		return fmt.Errorf("the description has no list of errors, or two")
	}
	errors := strings.Split(parts[2], ", ")
	for _, item := range errors {
		if !errorItem.MatchString(item) {
			return fmt.Errorf("%q is not an error of the list", item)
		}
	}
	errors = slices.DeleteFunc(errors, func(item string) bool { return item == csrfError })

	// The first sentence of the text of a body names the body, and a method has no body.
	body := ""
	if m.op.Request != nil {
		_, body, _ = strings.Cut(w.doc.Schemas[m.op.Request.Ref].Description, ". ")
	}
	m.paragraphs = []string{summary}
	for _, text := range []string{parts[1], body, parts[3]} {
		if text != "" {
			m.paragraphs = append(m.paragraphs, pythonText(text))
		}
	}

	m.raises = failedText + " Errors of the backend: " + strings.Join(errors, ", ") + "."
	if m.op.IdempotencyKey {
		m.raises += sendAdvice
	}
	if slices.Contains(w.doc.CreateCalls.Operations, m.op.ID) {
		create, ok := createTexts[m.op.ID]
		if !ok {
			return fmt.Errorf("the create call is not in createTexts")
		}
		// This client never gives "aborted": a cancellation passes unchanged.
		codes := slices.DeleteFunc(slices.Clone(w.doc.CreateCalls.Codes), func(code string) bool { return code == "aborted" })
		last := len(codes) - 1
		m.raises += fmt.Sprintf(" When `code` is %s or %s, `retry` is `RetryAdvice.%s`, because the %s can exist. List the %ss "+
			"with `%s` before you call again. A second call can make a second %s. The same applies after a cancelled call.",
			strings.Join(codes[:last], ", "), codes[last], strings.ToUpper(w.doc.CreateCalls.Retry),
			create.record, create.record, create.list, create.record)
	}
	return nil
}

// checkTexts checks the docstring.
func (m *method) checkTexts(w *modelWriter) error {
	text, err := checkedText("the docstring", strings.Join(strings.Fields(m.docstring()), " "))
	if err != nil {
		return err
	}
	if found := httpText.FindString(text); found != "" {
		return fmt.Errorf("the docstring has %q", found)
	}
	for _, op := range w.doc.Operations {
		if strings.Contains(text, op.ID) {
			return fmt.Errorf("the docstring has the operation id %s", op.ID)
		}
	}
	return nil
}

// arguments returns all the arguments of a method with one of its bodies.
func (m *method) arguments(body []pyField) []pyField {
	var out []pyField
	if m.path != nil {
		out = append(out, *m.path)
	}
	return slices.Concat(out, body, m.last)
}

func (m *method) docstring() string {
	args := section{title: "Args:"}
	for _, f := range m.arguments(m.body) {
		args.items = append(args.items, f.name+": "+f.text)
	}
	sections := []section{args}
	if m.returns != "" {
		// With no type, a reader of Google docstrings takes the text before a colon as the type.
		sections = append(sections, section{"Returns:", []string{m.result + ": " + m.returns}})
	}
	sections = append(sections, section{"Raises:", []string{"CalyxError: " + m.raises}})
	return docstring(8, m.paragraphs, sections...)
}

// signature returns the "def" lines, in the format of ruff.
func (m *method) signature(async bool, arguments []pyField) string {
	parts := []string{"self"}
	if m.path != nil {
		parts = append(parts, m.path.name+": "+m.path.pythonType())
		arguments = arguments[1:]
	}
	parts = append(parts, "*")
	for _, f := range arguments {
		part := f.name + ": " + f.pythonType()
		if !f.required {
			part += " = None"
		}
		parts = append(parts, part)
	}
	start := "    def "
	if async {
		start = "    async def "
	}
	return bracketed(start+m.name+"(", parts, ") -> "+m.result+":")
}

// call returns the one statement of a method.
func (m *method) call(async bool) string {
	start := "        "
	if m.result != "None" {
		start += "return "
	}
	if async {
		start += "await "
	}
	parts := []string{m.constant}
	if m.path != nil {
		parts = append(parts, "path_id="+m.path.name)
	}
	if m.op.Request != nil {
		var items []string
		for _, f := range m.body {
			items = append(items, pythonString(f.name)+": "+f.name)
		}
		parts = append(parts, strings.TrimSpace(bracketed("            arguments={", items, "}")))
	}
	for _, f := range m.last {
		parts = append(parts, f.name+"="+f.name)
	}
	function := "self._call("
	if m.op.IdempotencyKey {
		function = "self._send("
	}
	return bracketed(start+function, parts, ")")
}

// bracketed writes a list in brackets in the format of ruff: on one line when it fits, if not with
// one part on each line. The comma after the last part makes ruff keep the second form.
func bracketed(start string, parts []string, end string) string {
	line := start + strings.Join(parts, ", ") + end + "\n"
	if len(line) <= 101 && strings.Count(line, "\n") == 1 {
		return line
	}
	indent := start[:len(start)-len(strings.TrimLeft(start, " "))]
	var b strings.Builder
	b.WriteString(start + "\n")
	for _, part := range parts {
		b.WriteString(indent + "    " + part + ",\n")
	}
	b.WriteString(indent + end + "\n")
	return b.String()
}

// code returns a method with its overloads.
func (m *method) code(async bool) string {
	var b strings.Builder
	// Some IDEs show only the docstring of the first overload, so each one has every argument.
	for _, body := range m.variants {
		b.WriteString("\n    @overload\n" + m.signature(async, m.arguments(body)) + m.docstring())
	}
	b.WriteString("\n" + m.signature(async, m.arguments(m.body)) + m.docstring() + m.call(async))
	return b.String()
}

func operationsFile(methods []method) []byte {
	var b strings.Builder
	var classes []string
	for _, m := range methods {
		if m.answer != "" {
			classes = append(classes, m.answer)
		}
		fmt.Fprintf(&b, "\n%s: Final = Operation(\n    id=%s,\n    method=%s,\n    path=%s,\n    time_limit=%d,\n    read=%s,\n)\n",
			m.constant, pythonString(m.op.ID), pythonString(m.op.Method), pythonString(m.op.Path),
			m.op.TimeLimit, m.reader)
	}
	imports := "\nfrom typing import Final\n\nfrom .._call import Operation, read_model, read_nothing, read_status\n" +
		importLine(".models", classes)
	return []byte(header + imports + b.String())
}

// className is a class in an annotation. A value of Literal is in quotes.
var className = regexp.MustCompile(`"[^"]*"|\b[A-Z]\w*`)

func methodsFile(methods []method) []byte {
	var b strings.Builder
	var constants, classes []string
	for _, m := range methods {
		constants = append(constants, m.constant)
		for _, f := range slices.Concat(append(m.variants, m.body)...) {
			classes = append(classes, className.FindAllString(f.baseType, -1)...)
		}
		classes = append(classes, m.answer)
	}
	classes = slices.DeleteFunc(classes, func(name string) bool {
		return name == "" || name[0] == '"' || name == "Sequence" || name == "Literal"
	})
	b.WriteString(header + "\nfrom collections.abc import Sequence\nfrom typing import Literal, overload\n\n" +
		"from .._base import AsyncBase, SyncBase\nfrom ..sent import Sent\n" +
		importLine(".models", classes) + importLine(".operations", constants))
	for _, class := range []struct {
		name  string
		async bool
	}{{"Sync", false}, {"Async", true}} {
		fmt.Fprintf(&b, "\n\nclass %sMethods(%sBase):\n    \"\"\"The operations of the backend that are not a stream.\"\"\"\n",
			class.name, class.name)
		for _, m := range methods {
			b.WriteString(m.code(class.async))
		}
	}
	return []byte(b.String())
}

// importLine writes an import in the order of ruff: the classes come before the other names.
func importLine(module string, names []string) string {
	key := func(name string) string {
		if className.MatchString(name) {
			return "0" + strings.ToLower(name)
		}
		return "1" + strings.ToLower(name)
	}
	slices.SortFunc(names, func(a, b string) int { return strings.Compare(key(a), key(b)) })
	names = slices.Compact(names)
	line := "from " + module + " import " + strings.Join(names, ", ") + "\n"
	if len(line) <= 101 {
		return line
	}
	return "from " + module + " import (\n    " + strings.Join(names, ",\n    ") + ",\n)\n"
}
