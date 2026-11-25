package spec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const (
	openAPIPath     = "../../../../shared-types/openapi/backend.json"
	advicePath      = "../../../testdata/retry-advice.json"
	createCallsPath = "../../../testdata/create-calls.json"
)

type object = map[string]any

// inputs are the three real documents as generic JSON, so a test can change them.
type inputs struct {
	openAPI, advice, createCalls object
}

func realInputs(t *testing.T) inputs {
	t.Helper()
	read := func(path string) object {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var doc object
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		return doc
	}
	return inputs{openAPI: read(openAPIPath), advice: read(advicePath), createCalls: read(createCallsPath)}
}

func (in inputs) parse(t *testing.T) (*Doc, error) {
	t.Helper()
	var data [3][]byte
	for i, doc := range []object{in.openAPI, in.advice, in.createCalls} {
		var err error
		if data[i], err = json.Marshal(doc); err != nil {
			t.Fatal(err)
		}
	}
	return Parse(data[0], data[1], data[2])
}

func (in inputs) mustParse(t *testing.T) *Doc {
	t.Helper()
	doc, err := in.parse(t)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// at returns the object at the path of keys, for example at(doc, "components", "schemas").
func at(o object, keys ...string) object {
	for _, k := range keys {
		o = o[k].(object)
	}
	return o
}

func (in inputs) schemas() object { return at(in.openAPI, "components", "schemas") }

func (in inputs) operation(path, method string) object { return at(in.openAPI, "paths", path, method) }

func operation(t *testing.T, doc *Doc, id string) Operation {
	t.Helper()
	i := slices.IndexFunc(doc.Operations, func(op Operation) bool { return op.ID == id })
	if i < 0 {
		t.Fatalf("no operation %s", id)
	}
	return doc.Operations[i]
}

func checkEqual(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		g, _ := json.MarshalIndent(got, "", " ")
		w, _ := json.MarshalIndent(want, "", " ")
		t.Errorf("%s: got %s\nwant %s", what, g, w)
	}
}

func TestOperationsAreTheIncludedOnesInOrder(t *testing.T) {
	var got []string
	for _, op := range realInputs(t).mustParse(t).Operations {
		got = append(got, op.ID)
	}
	checkEqual(t, "operations", got, includedOperations)
}

func TestOperation(t *testing.T) {
	in := realInputs(t)
	send := in.operation("/api/v1/instances/{id}/messages", "post")
	send["summary"] = "The summary."
	send["description"] = "The description."
	send["parameters"].([]any)[0].(object)["description"] = "The id text."
	doc := in.mustParse(t)

	checkEqual(t, "sendMessage", operation(t, doc, "sendMessage"), Operation{
		ID:             "sendMessage",
		Method:         "POST",
		Path:           "/api/v1/instances/{id}/messages",
		Summary:        "The summary.",
		Description:    "The description.",
		PathParams:     []PathParam{{Name: "id", Description: "The id text."}},
		Request:        &Schema{Ref: "SendMessageRequest"},
		Answer:         &Schema{Ref: "SendMessageResult"},
		Statuses:       []int{200},
		TimeLimit:      360,
		IdempotencyKey: true,
	})

	remove := operation(t, doc, "removeInstance")
	checkEqual(t, "removeInstance statuses", remove.Statuses, []int{202, 204})
	if remove.Request != nil || remove.Answer != nil || remove.IdempotencyKey {
		t.Errorf("removeInstance: got %+v, want no body, no answer and no key", remove)
	}
	if list := operation(t, doc, "listClients"); len(list.PathParams) != 0 {
		t.Errorf("listClients: got path parameters %v", list.PathParams)
	}
}

func TestTimeLimits(t *testing.T) {
	doc := realInputs(t).mustParse(t)
	for id, want := range map[string]int{"sendMessage": 360, "createBulkJob": 150, "followPairing": 0, "getMe": 120} {
		if got := operation(t, doc, id).TimeLimit; got != want {
			t.Errorf("%s: got %d, want %d", id, got, want)
		}
	}
}

func TestPairingEvents(t *testing.T) {
	doc := realInputs(t).mustParse(t)
	checkEqual(t, "events", doc.PairingEvents, []Event{
		{Name: "done", Data: &Schema{Ref: "Done"}},
		{Name: "failed", Data: &Schema{Ref: "Failed"}},
		{Name: "update", Data: &Schema{Ref: "Pairing"}},
	})
	if op := operation(t, doc, "followPairing"); op.Answer != nil {
		t.Errorf("followPairing: got the answer %+v, want none", op.Answer)
	}
}

func TestSchema(t *testing.T) {
	in := realInputs(t)
	in.schemas()["Sample"] = object{
		"type":                 "object",
		"description":          "The sample.",
		"additionalProperties": false,
		"required":             []any{"when", "status"},
		"properties": object{
			"zone":   object{"type": "string"},
			"status": object{"type": []any{"string", "null"}, "enum": []any{"A", nil}},
			"when":   object{"type": "string", "format": "date-time", "description": "The time."},
			"alert":  object{"description": "The alert.", "oneOf": []any{object{"$ref": "#/components/schemas/Alert"}, object{"type": "null"}}},
			"hooks":  object{"type": "array", "items": object{"$ref": "#/components/schemas/Webhook"}},
			"any":    object{},
		},
	}
	checkEqual(t, "Sample", in.mustParse(t).Schemas["Sample"], &Schema{
		Type:                 "object",
		Description:          "The sample.",
		AdditionalProperties: new(false),
		// The required ones in the order of "required", then the others in name order.
		Properties: []Property{
			{Name: "when", Required: true, Schema: &Schema{Type: "string", Format: "date-time", Description: "The time."}},
			{Name: "status", Required: true, Schema: &Schema{Type: "string", Nullable: true, Enum: []*string{new("A"), nil}}},
			{Name: "alert", Schema: &Schema{Description: "The alert.", Nullable: true, OneOf: []*Schema{{Ref: "Alert"}, {Type: "null", Nullable: true}}}},
			{Name: "any", Schema: &Schema{}},
			{Name: "hooks", Schema: &Schema{Type: "array", Items: &Schema{Ref: "Webhook"}}},
			{Name: "zone", Schema: &Schema{Type: "string"}},
		},
	})
}

func TestProperty(t *testing.T) {
	s := &Schema{Properties: []Property{{Name: "a", Schema: &Schema{Type: "string"}}}}
	if p := s.Property("a"); p == nil || p.Type != "string" {
		t.Errorf("a: got %v", p)
	}
	if p := s.Property("b"); p != nil {
		t.Errorf("b: got %v, want nil", p)
	}
}

func TestAdviceAndCreateCalls(t *testing.T) {
	in := realInputs(t)
	in.createCalls = object{"operations": []any{"createClient"}, "codes": []any{"timeout"}, "retry": "unknown"}
	doc := in.mustParse(t)
	checkEqual(t, "first row", doc.Advice[0], AdviceRow{Source: "backend", Code: "send_outcome_unknown", Retry: "retry_now"})
	checkEqual(t, "rows", len(doc.Advice), len(in.advice["rows"].([]any)))
	checkEqual(t, "create calls", doc.CreateCalls,
		CreateCalls{Operations: []string{"createClient"}, Codes: []string{"timeout"}, Retry: "unknown"})
}

func TestUsedBy(t *testing.T) {
	doc := &Doc{Schemas: map[string]*Schema{
		"A": {Properties: []Property{
			{Name: "b", Schema: &Schema{Type: "array", Items: &Schema{Ref: "B"}}},
			{Name: "c", Schema: &Schema{OneOf: []*Schema{{Ref: "C"}, {Type: "null"}}}},
		}},
		"B":     {Properties: []Property{{Name: "a", Schema: &Schema{Ref: "A"}}}},
		"C":     {},
		"Other": {},
		"Bad":   {Items: &Schema{Ref: "Missing"}},
	}}
	got, err := doc.UsedBy(&Schema{Ref: "A"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	checkEqual(t, "names", got, []string{"A", "B", "C"})
	if _, err := doc.UsedBy(&Schema{Ref: "Bad"}); err == nil || !strings.Contains(err.Error(), "Missing") {
		t.Errorf("want an error that names Missing, got %v", err)
	}
}

func TestLoad(t *testing.T) {
	doc, err := Load(openAPIPath, advicePath, createCallsPath)
	if err != nil {
		t.Fatal(err)
	}
	checkEqual(t, "Load", doc, realInputs(t).mustParse(t))

	missing := filepath.Join(t.TempDir(), "missing.json")
	for _, paths := range [][3]string{
		{missing, advicePath, createCallsPath},
		{openAPIPath, missing, createCallsPath},
		{openAPIPath, advicePath, missing},
	} {
		if _, err := Load(paths[0], paths[1], paths[2]); err == nil || !strings.Contains(err.Error(), "missing.json") {
			t.Errorf("want an error that names missing.json, got %v", err)
		}
	}
}

func TestParseFailsOnBadJSON(t *testing.T) {
	data := func(path string) []byte {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	good := [3][]byte{data(openAPIPath), data(advicePath), data(createCallsPath)}
	for i, name := range []string{"backend.json", "retry-advice.json", "create-calls.json"} {
		bad := good
		bad[i] = []byte("{")
		if _, err := Parse(bad[0], bad[1], bad[2]); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("want an error that names %s, got %v", name, err)
		}
	}
}

func TestParseFails(t *testing.T) {
	const (
		me      = "/api/v1/me"
		login   = "/api/v1/auth/login"
		pairing = "/api/v1/instances/{id}/pairing"
	)
	problemCode := func(in inputs) object { return at(in.schemas(), "Problem", "properties", "code") }
	addRow := func(in inputs, source, code string) {
		in.advice["rows"] = append(in.advice["rows"].([]any), object{"source": source, "code": code, "retry": "unknown"})
	}
	stream := func(in inputs) object {
		return at(in.operation(pairing, "get"), "responses", "200", "content", "text/event-stream", "schema", "items")
	}
	sample := func(in inputs, s object) {
		in.schemas()["Sample"] = object{"type": "object", "properties": object{"x": s}}
	}
	cases := []struct {
		name   string
		change func(in inputs)
		want   []string
	}{
		{"operation in neither list", func(in inputs) {
			at(in.openAPI, "paths")["/api/v1/new"] = object{"get": object{"operationId": "getNewThing"}}
		}, []string{"getNewThing"}},
		{"included operation not in the document", func(in inputs) {
			delete(at(in.openAPI, "paths"), me)
		}, []string{"getMe"}},
		{"excluded operation not in the document", func(in inputs) {
			delete(at(in.openAPI, "paths"), login)
		}, []string{"login"}},
		{"request that is not JSON", func(in inputs) {
			body := in.operation("/api/v1/clients", "post")["requestBody"].(object)
			body["content"] = object{"text/plain": object{"schema": object{"type": "string"}}}
		}, []string{"createClient", "text/plain"}},
		{"answer that is not JSON", func(in inputs) {
			at(in.operation(me, "get"), "responses", "200")["content"] = object{"text/plain": object{"schema": object{"type": "string"}}}
		}, []string{"getMe", "text/plain"}},
		{"two answers", func(in inputs) {
			responses := at(in.operation(me, "get"), "responses")
			responses["202"] = responses["200"]
		}, []string{"getMe", "202"}},
		{"status that is not a number", func(in inputs) {
			at(in.operation(me, "get"), "responses")["2XX"] = object{}
		}, []string{"getMe", "2XX"}},
		{"stream with no event", func(in inputs) {
			stream(in)["oneOf"] = []any{}
		}, []string{"followPairing"}},
		{"stream event with no name", func(in inputs) {
			event := stream(in)["oneOf"].([]any)[0].(object)
			delete(at(event, "properties", "event"), "const")
		}, []string{"followPairing"}},
		{"stream event with no data", func(in inputs) {
			event := stream(in)["oneOf"].([]any)[0].(object)
			delete(at(event, "properties"), "data")
		}, []string{"followPairing", "done"}},
		{"required field with no property", func(in inputs) {
			at(in.schemas(), "Me")["required"] = []any{"ghost"}
		}, []string{"Me", "ghost"}},
		{"two types", func(in inputs) {
			sample(in, object{"type": []any{"string", "integer"}})
		}, []string{"Sample", "x", "type"}},
		{"enum value that is not a string", func(in inputs) {
			sample(in, object{"type": "integer", "enum": []any{1}})
		}, []string{"Sample", "x"}},
		{"ref to another place", func(in inputs) {
			sample(in, object{"$ref": "other.json#/Thing"})
		}, []string{"Sample", "x", "other.json#/Thing"}},
		{"additionalProperties that is a schema", func(in inputs) {
			sample(in, object{"type": "object", "additionalProperties": object{"type": "string"}})
		}, []string{"Sample", "x"}},
		{"unknown source", func(in inputs) { addRow(in, "moon", "x") }, []string{"moon"}},
		{"backend code not in the advice", func(in inputs) {
			code := problemCode(in)
			code["enum"] = append(code["enum"].([]any), "new_code")
		}, []string{"new_code"}},
		{"backend code not in the document", func(in inputs) { addRow(in, "backend", "old_code") }, []string{"old_code"}},
		{"null backend code", func(in inputs) {
			code := problemCode(in)
			code["enum"] = append(code["enum"].([]any), nil)
		}, []string{"Problem.code", "null"}},
		{"no Problem.code", func(in inputs) {
			delete(in.schemas(), "Problem")
		}, []string{"Problem.code"}},
		{"anyOf", func(in inputs) {
			sample(in, object{"anyOf": []any{object{"type": "string"}}})
		}, []string{"Sample", "x", "anyOf"}},
		{"allOf", func(in inputs) {
			sample(in, object{"allOf": []any{object{"type": "string"}}})
		}, []string{"Sample", "x", "allOf"}},
		{"patternProperties", func(in inputs) {
			sample(in, object{"type": "object", "patternProperties": object{}})
		}, []string{"Sample", "x", "patternProperties"}},
		{"properties without a type", func(in inputs) {
			sample(in, object{"properties": object{}})
		}, []string{"Sample", "x", "properties"}},
		{"null keyword", func(in inputs) {
			sample(in, object{"additionalProperties": nil})
		}, []string{"Sample", "x", "additionalProperties"}},
		{"null oneOf member", func(in inputs) {
			sample(in, object{"oneOf": []any{nil}})
		}, []string{"Sample", "x", "null"}},
		{"null property", func(in inputs) {
			in.schemas()["Sample"] = object{"type": "object", "properties": object{"x": nil}}
		}, []string{"Sample", "x", "null"}},
		{"null schema", func(in inputs) {
			in.schemas()["Sample"] = nil
		}, []string{"Sample", "null"}},
		{"null answer schema", func(in inputs) {
			at(in.operation(me, "get"), "responses", "200", "content", "application/json")["schema"] = nil
		}, []string{"getMe", "null"}},
		{"path-level parameters", func(in inputs) {
			at(in.openAPI, "paths", me)["parameters"] = []any{}
		}, []string{"getMe", "path-level"}},
		{"$ref parameter", func(in inputs) {
			in.operation(me, "get")["parameters"] = []any{object{"$ref": "#/components/parameters/Id"}}
		}, []string{"getMe", "#/components/parameters/Id"}},
		{"$ref request", func(in inputs) {
			in.operation("/api/v1/clients", "post")["requestBody"] = object{"$ref": "#/components/requestBodies/New"}
		}, []string{"createClient", "#/components/requestBodies/New"}},
		{"$ref answer", func(in inputs) {
			at(in.operation(me, "get"), "responses")["200"] = object{"$ref": "#/components/responses/Me"}
		}, []string{"getMe", "#/components/responses/Me"}},
		{"query parameter", func(in inputs) {
			in.operation(me, "get")["parameters"] = []any{object{"in": "query", "name": "page"}}
		}, []string{"getMe", "query", "page"}},
		{"other header", func(in inputs) {
			in.operation(me, "get")["parameters"] = []any{object{"in": "header", "name": "X-Other"}}
		}, []string{"getMe", "header", "X-Other"}},
		{"path name with no parameter", func(in inputs) {
			in.operation("/api/v1/clients/{id}", "get")["parameters"] = []any{}
		}, []string{"getClient", "/api/v1/clients/{id}"}},
		{"path parameter that is not in the path", func(in inputs) {
			in.operation(me, "get")["parameters"] = []any{object{"in": "path", "name": "id"}}
		}, []string{"getMe", me}},
		{"duplicate operation id", func(in inputs) {
			at(in.openAPI, "paths")["/api/v1/new"] = object{"get": object{"operationId": "getMe"}}
		}, []string{"getMe", "two"}},
		{"duplicate advice row", func(in inputs) { addRow(in, "backend", "not_found") }, []string{"backend", "not_found", "two"}},
		{"unknown retry value", func(in inputs) {
			in.advice["rows"].([]any)[0].(object)["retry"] = "maybe"
		}, []string{"retry-advice.json", "maybe"}},
		{"no create operations", func(in inputs) {
			in.createCalls["operations"] = []any{}
		}, []string{"create-calls.json", "operations"}},
		{"create operation that is not included", func(in inputs) {
			in.createCalls["operations"] = []any{"createClient", "createAPIKey"}
		}, []string{"create-calls.json", "createAPIKey"}},
		{"no create codes", func(in inputs) {
			delete(in.createCalls, "codes")
		}, []string{"create-calls.json", "codes"}},
		{"no create retry value", func(in inputs) {
			delete(in.createCalls, "retry")
		}, []string{"create-calls.json", "retry"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := realInputs(t)
			c.change(in)
			_, err := in.parse(t)
			if err == nil {
				t.Fatal("want an error, got none")
			}
			for _, want := range c.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("want an error with %q, got %v", want, err)
				}
			}
		})
	}
}

func TestParseIgnoresSafeKeywords(t *testing.T) {
	in := realInputs(t)
	in.schemas()["Sample"] = object{"type": "string", "title": "T", "default": nil, "maxLength": 5, "pattern": "^a"}
	checkEqual(t, "Sample", in.mustParse(t).Schemas["Sample"], &Schema{Type: "string"})
}

// The rules for parameters, bodies and answers are only for the included operations.
func TestParseAcceptsOtherShapesInExcludedOperations(t *testing.T) {
	in := realInputs(t)
	path := at(in.openAPI, "paths", "/api/v1/auth/login")
	path["parameters"] = []any{object{"in": "query", "name": "a"}}
	at(path, "post")["requestBody"] = object{"$ref": "#/components/requestBodies/Login"}
	at(path, "post")["parameters"] = []any{object{"in": "query", "name": "b"}}
	in.mustParse(t)
}
