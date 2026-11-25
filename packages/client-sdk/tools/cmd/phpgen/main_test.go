package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"strings"
	"testing"

	"calyx/sdktools/internal/genfiles"
	"calyx/sdktools/internal/spec"
)

var update = flag.Bool("update", false, "write the files of testdata/golden again")

// toolsDir is the tools module root, seen from this package folder.
const toolsDir = "../.."

const goldenDir = "testdata/golden"

func readInput(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// realOpenAPI returns backend.json as generic JSON, so a test can change it before parsing.
func realOpenAPI(t *testing.T) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(readInput(t, inputPaths(toolsDir).openAPI), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// parse reads doc with the real advice and create calls.
func parse(t *testing.T, doc map[string]any) (*spec.Doc, error) {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	in := inputPaths(toolsDir)
	return spec.Parse(data, readInput(t, in.advice), readInput(t, in.createCalls))
}

// build returns the files of doc, or the error of the parser or of the generator.
func build(t *testing.T, doc map[string]any) (map[string][]byte, error) {
	t.Helper()
	parsed, err := parse(t, doc)
	if err != nil {
		return nil, err
	}
	return buildFiles(parsed)
}

func schemas(doc map[string]any) map[string]any {
	return doc["components"].(map[string]any)["schemas"].(map[string]any)
}

// Failure tests.

func TestFailsOnOperationInNeitherList(t *testing.T) {
	doc := realOpenAPI(t)
	paths := doc["paths"].(map[string]any)
	paths["/api/v1/new-thing"] = map[string]any{"get": map[string]any{"operationId": "getNewThing"}}
	_, err := build(t, doc)
	if err == nil || !strings.Contains(err.Error(), "getNewThing") {
		t.Fatalf("want an error that names getNewThing, got %v", err)
	}
}

func TestFailsOnReservedWordNotInRenameTable(t *testing.T) {
	doc := realOpenAPI(t)
	s := schemas(doc)
	s["List"] = s["SendMessageResult"]
	delete(s, "SendMessageResult")
	op := doc["paths"].(map[string]any)["/api/v1/instances/{id}/messages"].(map[string]any)["post"].(map[string]any)
	answer := op["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)
	answer["schema"] = map[string]any{"$ref": "#/components/schemas/List"}
	_, err := build(t, doc)
	if err == nil || !strings.Contains(err.Error(), "List") {
		t.Fatalf("want an error that names List, got %v", err)
	}
}

func TestFailsOnBackendCodeNotInAdvice(t *testing.T) {
	doc := realOpenAPI(t)
	code := schemas(doc)["Problem"].(map[string]any)["properties"].(map[string]any)["code"].(map[string]any)
	code["enum"] = append(code["enum"].([]any), "new_code")
	_, err := build(t, doc)
	if err == nil || !strings.Contains(err.Error(), "new_code") {
		t.Fatalf("want an error that names new_code, got %v", err)
	}
}

func TestFailsOnUnsupportedType(t *testing.T) {
	doc := realOpenAPI(t)
	props := schemas(doc)["SendMessageResult"].(map[string]any)["properties"].(map[string]any)
	props["counts"] = map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}
	_, err := build(t, doc)
	if err == nil || !strings.Contains(err.Error(), "SendMessageResult.counts") {
		t.Fatalf("want an error that names SendMessageResult.counts, got %v", err)
	}
}

func TestFailsOnSchemaWithoutTypeThatIsNotEmpty(t *testing.T) {
	for name, s := range map[string]spec.Schema{
		"additionalProperties": {AdditionalProperties: new(true)},
		"enum":                 {Enum: []*string{nil}},
	} {
		if _, err := newField("S", "x", &s, true); err == nil {
			t.Errorf("%s: want an error, got none", name)
		}
	}
}

// Positive tests.

func TestClientCodeHasThePHPCodes(t *testing.T) {
	files, err := build(t, realOpenAPI(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"public const string UNSUPPORTED_RUNTIME = 'unsupported_runtime';",
		"public const string PENDING_SEND_EXPIRED = 'pending_send_expired';",
	} {
		if !bytes.Contains(files["ClientCode.php"], []byte(line)) {
			t.Errorf("ClientCode.php has no line %q", line)
		}
	}
}

// Every model encodes with json_encode() through jsonSerialize(), so an empty object is {}.
func TestModelsAreJsonSerializable(t *testing.T) {
	files, err := build(t, realOpenAPI(t))
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if !strings.HasPrefix(name, "Model/") {
			continue
		}
		for _, want := range []string{"implements JsonSerializable", "public function jsonSerialize(): object", "return (object) "} {
			if !bytes.Contains(content, []byte(want)) {
				t.Errorf("%s has no %q", name, want)
			}
		}
	}
	pacing := string(files["Model/CreateBulkJobRequest.php"])
	_, serialize, _ := strings.Cut(pacing, "function jsonSerialize")
	if !strings.Contains(serialize, "'pacing' => $this->pacing,") || !strings.Contains(serialize, "'messages' => $this->messages,") {
		t.Errorf("jsonSerialize must pass the nested models as they are:\n%s", serialize)
	}
}

func TestFailsOnPHPCodeInAdvice(t *testing.T) {
	doc, err := parse(t, realOpenAPI(t))
	if err != nil {
		t.Fatal(err)
	}
	doc.Advice = append(doc.Advice, spec.AdviceRow{Source: "client", Code: "pending_send_expired", Retry: "check_if_sent"})
	_, err = buildFiles(doc)
	if err == nil || !strings.Contains(err.Error(), "pending_send_expired") {
		t.Fatalf("want an error that names pending_send_expired, got %v", err)
	}
}

func TestFieldTypes(t *testing.T) {
	str := func(s string) *string { return &s }
	cases := []struct {
		name     string
		schema   spec.Schema
		required bool
		want     string // PHP type, doc type, default, reader
	}{
		{"string", spec.Schema{Type: "string"}, true, "string||no|$r->string('x')"},
		{"optional int", spec.Schema{Type: "integer"}, false, "?int||null|$r->intOrNull('x')"},
		{"nullable number", spec.Schema{Type: "number", Nullable: true}, true, "?float||null|$r->floatOrNull('x', required: true)"},
		{"boolean", spec.Schema{Type: "boolean"}, true, "bool||no|$r->bool('x')"},
		{"ref", spec.Schema{Ref: "Pairing"}, true, "Pairing||no|$r->object('x', Pairing::fromArray(...))"},
		{"renamed ref", spec.Schema{Ref: "Client"}, false, "?OrgClient||null|$r->objectOrNull('x', OrgClient::fromArray(...))"},
		{"oneOf ref and null", spec.Schema{Nullable: true, OneOf: []*spec.Schema{{Ref: "Alert"}, {Type: "null", Nullable: true}}}, true,
			"?Alert||null|$r->objectOrNull('x', Alert::fromArray(...), required: true)"},
		{"list of refs", spec.Schema{Type: "array", Items: &spec.Schema{Ref: "Webhook"}}, true,
			"array|list<Webhook>|no|$r->list('x', Webhook::fromArray(...))"},
		{"nullable list of strings", spec.Schema{Type: "array", Nullable: true, Items: &spec.Schema{Type: "string"}}, false,
			"?array|list<string>|null|$r->stringListOrNull('x')"},
		{"enum with null", spec.Schema{Type: "string", Nullable: true, Enum: []*string{str("A"), nil}}, true,
			"?string||null|$r->stringOrNull('x', required: true)"},
		{"any value", spec.Schema{}, true, "mixed||no|$r->mixed('x', required: true)"},
		{"required list that can be null", spec.Schema{Type: "array", Nullable: true, Items: &spec.Schema{Ref: "Webhook"}}, true,
			"?array|list<Webhook>|null|$r->listOrNull('x', Webhook::fromArray(...), required: true)"},
	}
	for _, c := range cases {
		f, err := newField("S", "x", &c.schema, c.required)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		def := "no"
		if f.HasDefault {
			def = "null"
		}
		got := strings.Join([]string{f.PHPType, f.DocType, def, f.Reader}, "|")
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestPropertyNames(t *testing.T) {
	for wire, want := range map[string]string{"id": "id", "message_id": "messageId", "phone_number_id": "phoneNumberId"} {
		if got := propertyName(wire); got != want {
			t.Errorf("%s: got %s, want %s", wire, got, want)
		}
	}
}

// TestGoldenFiles compares each generated file with its golden file, in both directions.
func TestGoldenFiles(t *testing.T) {
	files, err := build(t, realOpenAPI(t))
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := genfiles.Write(files, goldenDir, ".php"); err != nil {
			t.Fatal(err)
		}
	}
	stale, err := genfiles.Compare(files, goldenDir, ".php")
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) > 0 {
		t.Fatalf("these files differ from testdata/golden: %v; run go test ./cmd/phpgen -update and review the diff", stale)
	}
}

func TestEveryFileHasTheHeader(t *testing.T) {
	files, err := build(t, realOpenAPI(t))
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if !bytes.HasPrefix(content, []byte("<?php\n\n// Generated by phpgen. Do not edit.\n")) {
			t.Errorf("%s does not start with the header", name)
		}
	}
}
