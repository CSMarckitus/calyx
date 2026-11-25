package main

import (
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"calyx/sdktools/internal/spec"
)

// noClass are the answer schemas with no class. pairing.py has the content of both.
var noClass = []string{"Done", "Failed"}

// renames maps a schema name to its class name. Go and PHP use the same name.
var renames = map[string]string{"Client": "OrgClient"}

// omittedFields are the fields that no included operation returns.
var omittedFields = []string{"ApiKey.token"}

// secretFields are not in the repr of their class.
var secretFields = []string{
	"WebhookInput.secret",
	"MetaCredentialsInput.access_token",
	"MetaCredentialsInput.app_secret",
	"MetaWebhook.verify_token",
	"Pairing.pairing_code",
	"Pairing.qr_text",
}

// enumClasses are the classes of constants, in the order of the file. The enum of the field
// source gives the values.
var enumClasses = []struct{ name, source, doc string }{
	{"Provider", "Instance.provider", "How an instance connects to WhatsApp."},
	{"DeviceIdentity", "CreateInstanceRequest.device_identity", "The device that the phone shows for an instance."},
	{"InstanceStatus", "Instance.status", "The connection status of an instance."},
	{"InstanceState", "Instance.state", "The state of an instance in Calyx."},
	{"BulkJobStatus", "BulkJob.status", "The status of a bulk job."},
	{"PairingFailureCode", "Failed.code", "Why a pairing ended with no connection."},
}

// enumFields gives the class of constants of each enum. Instance.device_identity is an open
// string, and its text names the same values.
var enumFields = map[string]string{
	"Instance.provider":                     "Provider",
	"CreateInstanceRequest.provider":        "Provider",
	"Instance.device_identity":              "DeviceIdentity",
	"CreateInstanceRequest.device_identity": "DeviceIdentity",
	"PairInstanceRequest.device_identity":   "DeviceIdentity",
	"Instance.status":                       "InstanceStatus",
	"Pairing.status":                        "InstanceStatus",
	"Done.status":                           "InstanceStatus",
	"Instance.state":                        "InstanceState",
	"BulkJob.status":                        "BulkJobStatus",
	"Failed.code":                           "PairingFailureCode",
}

// forbiddenTexts are the parts of backend.json that are wrong for a program with this SDK.
var forbiddenTexts = []string{"csrf_check_failed", "do not retry automatically"}

// keywords are the reserved words of Python 3.14.
var keywords = strings.Fields(`
	False None True and as assert async await break class continue def del elif else except
	finally for from global if import in is lambda nonlocal not or pass raise return try while
	with yield`)

// shadowed are the names that the generated annotations and the dataclass use. A field with
// one of these names would hide them when get_type_hints reads the class.
var shadowed = strings.Fields("str int float bool tuple datetime field")

var (
	classPattern = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
	fieldPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// scalarTypes maps a JSON type and its format to the Python type.
var scalarTypes = map[[2]string]string{
	{"string", ""}:          "str",
	{"string", "uuid"}:      "str",
	{"string", "date-time"}: "datetime",
	{"integer", ""}:         "int",
	{"integer", "int32"}:    "int",
	{"integer", "int64"}:    "int",
	{"number", ""}:          "float",
	{"number", "float"}:     "float",
	{"number", "double"}:    "float",
	{"boolean", ""}:         "bool",
}

// modelWriter writes models.py.
type modelWriter struct {
	doc *spec.Doc
	// classes has the class name of each schema with a class.
	classes map[string]string
	// requests are the schemas of the request bodies, at any depth.
	requests []string
	// constants has the values of each class of constants.
	constants map[string][]string
	// fields has the fields of each schema, also of the request bodies. The omitted ones too.
	fields map[string][]pyField
}

// pyField is a field of a class or an argument of a method.
type pyField struct {
	id   string // for example Alert.kind
	name string
	// baseType is the type without None.
	baseType string
	nullable bool
	// required tells if the field has no default. The default of another field is None.
	required bool
	// secret tells if the field is not in the repr of its class.
	secret bool
	text   string
}

func (f pyField) pythonType() string {
	if f.nullable {
		return f.baseType + " | None"
	}
	return f.baseType
}

func newModelWriter(doc *spec.Doc) *modelWriter {
	return &modelWriter{
		doc: doc, classes: map[string]string{}, constants: map[string][]string{}, fields: map[string][]pyField{},
	}
}

// file returns models.py.
func (w *modelWriter) file() ([]byte, error) {
	schemas, err := w.selectSchemas()
	if err != nil {
		return nil, err
	}

	var b strings.Builder
	for _, class := range enumClasses {
		if err := w.writeConstants(&b, class.name, class.source, class.doc); err != nil {
			return nil, err
		}
	}
	for _, name := range schemas {
		text, err := w.class(name)
		if err != nil {
			return nil, err
		}
		if w.classes[name] != "" {
			b.WriteString(text)
		}
	}
	if err := w.checkLists(); err != nil {
		return nil, err
	}

	// The annotations are texts at run time, so a class can use a class below it.
	imports := "\nfrom __future__ import annotations\n\n"
	if strings.Contains(b.String(), "Sequence[") {
		imports += "from collections.abc import Sequence\n"
	}
	imports += "from dataclasses import dataclass, field\nfrom datetime import datetime\nfrom typing import Final\n"
	return []byte(header + imports + b.String()), nil
}

// selectSchemas fills classes and requests. It returns the schemas of the included operations in
// the order of their class names, with the ones that have no class.
func (w *modelWriter) selectSchemas() ([]string, error) {
	var bodies []string
	var requests, answers []*spec.Schema
	for _, op := range w.doc.Operations {
		if op.Request != nil {
			if op.Request.Ref == "" {
				return nil, fmt.Errorf("the request of %s has no named schema", op.ID)
			}
			bodies = append(bodies, op.Request.Ref)
			requests = append(requests, op.Request)
		}
		if op.Answer != nil {
			if op.Answer.Ref == "" {
				return nil, fmt.Errorf("the answer of %s has no named schema", op.ID)
			}
			answers = append(answers, op.Answer)
		}
	}
	for _, e := range w.doc.PairingEvents {
		if e.Data.Ref == "" {
			return nil, fmt.Errorf("the event %s has no named schema", e.Name)
		}
		answers = append(answers, e.Data)
	}
	var err error
	if w.requests, err = w.doc.UsedBy(requests...); err != nil {
		return nil, err
	}
	answered, err := w.doc.UsedBy(answers...)
	if err != nil {
		return nil, err
	}

	owners := map[string]bool{}
	for _, class := range enumClasses {
		owners[class.name] = true
	}
	schemas := slices.Concat(w.requests, answered)
	for _, name := range slices.Concat(noClass, slices.Sorted(maps.Keys(renames))) {
		if !slices.Contains(schemas, name) {
			return nil, fmt.Errorf("pygen names the schema %s, but no included operation uses it", name)
		}
	}
	for _, name := range schemas {
		if slices.Contains(w.requests, name) && slices.Contains(answered, name) {
			return nil, fmt.Errorf("the schema %s is in a request and in an answer", name)
		}
		if slices.Contains(bodies, name) || slices.Contains(noClass, name) {
			continue
		}
		class := name
		if renamed, ok := renames[name]; ok {
			class = renamed
		}
		if !classPattern.MatchString(class) || slices.Contains(keywords, class) {
			return nil, fmt.Errorf("the schema name %s cannot be a Python class name", name)
		}
		if owners[class] {
			return nil, fmt.Errorf("two classes have the name %s", class)
		}
		owners[class] = true
		w.classes[name] = class
	}
	slices.SortStableFunc(schemas, func(a, b string) int { return strings.Compare(w.classes[a], w.classes[b]) })
	return schemas, nil
}

// checkLists fails when a list of this file names a field that does not exist.
func (w *modelWriter) checkLists() error {
	seen := map[string]bool{}
	for _, fields := range w.fields {
		for _, f := range fields {
			seen[f.id] = true
		}
	}
	for _, id := range slices.Concat(omittedFields, secretFields, slices.Sorted(maps.Keys(enumFields))) {
		if !seen[id] {
			return fmt.Errorf("pygen names the field %s, but backend.json does not have it", id)
		}
	}
	return nil
}

func (w *modelWriter) writeConstants(b *strings.Builder, class, source, doc string) error {
	schema, field, _ := strings.Cut(source, ".")
	var s *spec.Schema
	if owner := w.doc.Schemas[schema]; owner != nil {
		s = owner.Property(field)
	}
	if s == nil || len(s.Enum) == 0 {
		return fmt.Errorf("the field %s has no enum for the class %s", source, class)
	}
	text, err := checkedText(source, s.Description)
	if err != nil {
		return err
	}
	fmt.Fprintf(b, "\n\nclass %s:\n%s\n", class, docstring(4, []string{doc + " A newer backend can send other values.", text}))
	names := map[string]bool{}
	for _, v := range s.Enum {
		if v == nil {
			continue
		}
		name := strings.ToUpper(*v)
		if !constantPattern.MatchString(name) || names[name] {
			return fmt.Errorf("the value %q of %s gives no unique Python constant name", *v, source)
		}
		names[name] = true
		w.constants[class] = append(w.constants[class], *v)
		fmt.Fprintf(b, "    %s: Final = %s\n", name, pythonString(*v))
	}
	return nil
}

// class returns the dataclass of a schema, after its checks. A schema with no class has the
// same checks, because its fields become arguments or the fields of a hand-written class.
func (w *modelWriter) class(name string) (string, error) {
	s := w.doc.Schemas[name]
	if s.AdditionalProperties == nil || *s.AdditionalProperties || !reflect.DeepEqual(*s, spec.Schema{
		Type: "object", Description: s.Description, Properties: s.Properties, AdditionalProperties: s.AdditionalProperties,
	}) {
		return "", fmt.Errorf("the schema %s is not an object with fixed properties", name)
	}
	text, err := checkedText("the schema "+name, s.Description)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(classStart(w.classes[name], text))
	for _, p := range s.Properties {
		field, err := w.field(name+"."+p.Name, p, slices.Contains(w.requests, name))
		if err != nil {
			return "", err
		}
		w.fields[name] = append(w.fields[name], field)
		if !slices.Contains(omittedFields, field.id) {
			b.WriteString("\n" + field.code())
		}
	}
	return b.String(), nil
}

// classStart returns the lines of a dataclass before its fields.
func classStart(name, text string) string {
	return fmt.Sprintf("\n\n@dataclass(frozen=True, slots=True, kw_only=True)\nclass %s:\n%s", name, docstring(4, []string{text}))
}

func (w *modelWriter) field(id string, p spec.Property, request bool) (pyField, error) {
	s := p.Schema
	if !fieldPattern.MatchString(p.Name) || slices.Contains(keywords, p.Name) ||
		slices.Contains(shadowed, p.Name) {
		return pyField{}, fmt.Errorf("the property %s cannot be a Python field name", id)
	}
	text, err := checkedText(id, s.Description)
	if err != nil {
		return pyField{}, err
	}
	class, mapped := enumFields[id]
	if mapped {
		text += fmt.Sprintf(" The class `%s` has the known values.", class)
	} else if len(s.Enum) > 0 {
		return pyField{}, fmt.Errorf("the enum of %s has no class in enumFields", id)
	}
	for _, v := range s.Enum {
		if v != nil && !slices.Contains(w.constants[class], *v) {
			return pyField{}, fmt.Errorf("the class %s does not have the value %q of %s", class, *v, id)
		}
	}

	value := *s
	value.Enum, value.Nullable = nil, false
	pythonType := w.pythonType(value, request)
	if pythonType == "" {
		return pyField{}, fmt.Errorf("the property %s has a type that pygen does not support", id)
	}
	// None leaves a field out of a request, so it cannot be a value that the backend needs.
	if request && p.Required && s.Nullable {
		return pyField{}, fmt.Errorf("the property %s of a request is required and nullable", id)
	}
	return pyField{
		id: id, name: p.Name, baseType: pythonType, nullable: !p.Required || s.Nullable, required: p.Required,
		secret: slices.Contains(secretFields, id), text: text,
	}, nil
}

// code returns the line of a field in its class, with its docstring.
func (f pyField) code() string {
	var arguments []string
	if !f.required {
		arguments = append(arguments, "default=None")
	}
	if f.secret {
		arguments = append(arguments, "repr=False")
	}
	assignment := ""
	switch {
	case len(arguments) == 1 && !f.required:
		assignment = " = None"
	case len(arguments) > 0:
		assignment = " = field(" + strings.Join(arguments, ", ") + ")"
	}
	return fmt.Sprintf("    %s: %s%s\n%s", f.name, f.pythonType(), assignment, docstring(4, []string{f.text}))
}

// pythonType returns "" for a shape that the reader and the writer of _fields.py do not know.
// The caller removed the enum and the null of a property.
func (w *modelWriter) pythonType(s spec.Schema, request bool) string {
	s.Description = ""
	isOnly := func(want spec.Schema) bool { return reflect.DeepEqual(s, want) }
	switch {
	case s.Ref != "" && isOnly(spec.Schema{Ref: s.Ref}):
		return w.classes[s.Ref]
	case len(s.OneOf) == 2 && reflect.DeepEqual(*s.OneOf[1], spec.Schema{Type: "null", Nullable: true}) &&
		isOnly(spec.Schema{OneOf: s.OneOf}):
		return w.pythonType(*s.OneOf[0], request)
	case s.Items != nil && isOnly(spec.Schema{Type: "array", Items: s.Items}):
		item := w.pythonType(*s.Items, request)
		if item == "" {
			return ""
		}
		if request {
			return "Sequence[" + item + "]"
		}
		return "tuple[" + item + ", ...]"
	case isOnly(spec.Schema{Type: s.Type, Format: s.Format}):
		pythonType := scalarTypes[[2]string{s.Type, s.Format}]
		if request && pythonType == "datetime" {
			return ""
		}
		return pythonType
	}
	return ""
}

// checkedText returns the text of a schema or a field. It fails on a text that is missing or
// that the docstrings must not have.
func checkedText(owner, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("%s has no text", owner)
	}
	for _, part := range forbiddenTexts {
		if strings.Contains(text, part) {
			return "", fmt.Errorf("the text of %s has %q", owner, part)
		}
	}
	text = strings.ReplaceAll(text, "same Idempotency-Key", "same idempotency key")
	if found := httpText.FindString(text); found != "" {
		return "", fmt.Errorf("the text of %s has %q", owner, found)
	}
	return pythonText(text), nil
}

var jsonWord = regexp.MustCompile(`\b(null|true|false)\b`)

// pythonText gives a Python user the names that Python has: None, True, False, and a datetime in
// UTC.
func pythonText(text string) string {
	text = jsonWord.ReplaceAllStringFunc(text, func(word string) string {
		return map[string]string{"null": "None", "true": "True", "false": "False"}[word]
	})
	return strings.ReplaceAll(text, "RFC 3339 in UTC", "in UTC")
}

// section is a titled part of a docstring in the Google style, for example "Args:".
type section struct {
	title string
	items []string
}

// docstring writes the paragraphs and then the sections as a docstring with an indent.
func docstring(indent int, paragraphs []string, sections ...section) string {
	if len(paragraphs) == 1 && len(sections) == 0 {
		return wrap(indent, indent, `"""`+escaped(paragraphs[0])+`"""`)
	}
	var b strings.Builder
	for i, text := range paragraphs {
		if i == 0 {
			b.WriteString(wrap(indent, indent, `"""`+escaped(text)))
		} else {
			b.WriteString("\n" + wrap(indent, indent, escaped(text)))
		}
	}
	for _, s := range sections {
		b.WriteString("\n" + wrap(indent, indent, s.title))
		for _, item := range s.items {
			b.WriteString(wrap(indent+4, indent+8, escaped(item)))
		}
	}
	b.WriteString(wrap(indent, indent, `"""`))
	return b.String()
}

// escaped returns a text as the inside of a Python string, with one space between two words.
func escaped(text string) string {
	quoted := pythonString(strings.Join(strings.Fields(text), " "))
	return quoted[1 : len(quoted)-1]
}

// wrap writes a text in lines of 100 characters at most. The indents are the one of the first
// line and the one of the next lines.
func wrap(first, next int, text string) string {
	var b strings.Builder
	words := strings.Split(text, " ")
	line := strings.Repeat(" ", first) + words[0]
	for _, word := range words[1:] {
		if len([]rune(line))+1+len([]rune(word)) > 100 {
			b.WriteString(line + "\n")
			line = strings.Repeat(" ", next) + word
		} else {
			line += " " + word
		}
	}
	b.WriteString(line + "\n")
	return b.String()
}
