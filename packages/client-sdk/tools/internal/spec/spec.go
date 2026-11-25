// Package spec reads the inputs of the generators of the client SDKs: the OpenAPI document of
// the backend, the retry advice and the create calls. It knows no language: the names and the
// types of PHP and Python are in the generators.
package spec

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// includedOperations are the 23 included operations of CONTRACT.md part 2, in its order.
var includedOperations = []string{
	"listAPIKeys", "revokeAPIKey",
	"listClients", "createClient", "getClient", "updateClient", "deleteClient",
	"listInstances", "createInstance", "getInstance", "updateInstance", "removeInstance",
	"pairInstance", "cancelPairing", "followPairing",
	"sendMessage", "createBulkJob", "listBulkJobs", "getBulkJob", "cancelBulkJob",
	"getMe", "getOrganization", "updateOrganization",
}

// excludedOperations are the 14 excluded operations of CONTRACT.md part 2. A new operation of
// backend.json must go in one of the two lists, so nobody adds it, or leaves it out, by accident.
var excludedOperations = []string{
	"login", "logout", "createAPIKey", "inviteClientUser", "requestPasswordReset",
	"confirmPasswordReset", "acceptInvitation", "changePassword", "acknowledgeWhatsAppWeb",
	"lookupInvitation", "lookupPasswordReset", "cancelInvitation", "removeClientAccess", "followEvents",
}

// streamOperation is the one included operation that answers with a stream of events.
const streamOperation = "followPairing"

// timeLimits are the default time limits in seconds of CONTRACT.md part 8. Zero means no
// limit. Every other operation gets defaultTimeLimit.
var timeLimits = map[string]int{
	"sendMessage":   360,
	"createBulkJob": 150,
	streamOperation: 0,
}

const defaultTimeLimit = 120

// Sources are the sources of the retry advice.
var Sources = []string{"backend", "core", "client"}

// retryValues are the values of the retry advice (CONTRACT.md part 6).
var retryValues = []string{"retry_now", "retry_later", "check_if_sent", "do_not_retry", "unknown"}

// Doc is the content of the three inputs.
type Doc struct {
	// Operations are the included operations, in the order of CONTRACT.md part 2.
	Operations []Operation
	// Schemas are all the named schemas, also the ones that no included operation uses.
	Schemas map[string]*Schema
	// PairingEvents are the events of the pairing stream, in the order of the document.
	PairingEvents []Event
	Advice        []AdviceRow
	CreateCalls   CreateCalls
}

type Operation struct {
	ID          string
	Method      string // in upper case
	Path        string
	Summary     string
	Description string
	PathParams  []PathParam
	// Request and Answer are the schemas of the JSON bodies, nil without a JSON body.
	Request *Schema
	Answer  *Schema
	// Statuses are the 2xx statuses, sorted.
	Statuses []int
	// TimeLimit is the default time limit in seconds. Zero means no limit.
	TimeLimit int
	// IdempotencyKey tells if the operation takes an Idempotency-Key header.
	IdempotencyKey bool
}

type PathParam struct {
	Name        string
	Description string
}

// Event is one event of the pairing stream.
type Event struct {
	Name string
	Data *Schema
}

type AdviceRow struct {
	Source string `json:"source"`
	Code   string `json:"code"`
	Retry  string `json:"retry"`
}

// CreateCalls has the operations and the client codes that have their own retry advice
// (CONTRACT.md part 6).
type CreateCalls struct {
	Operations []string `json:"operations"`
	Codes      []string `json:"codes"`
	Retry      string   `json:"retry"`
}

// Schema has the keywords of a JSON schema that the generators read.
type Schema struct {
	// Ref is the name of the schema that this one refers to, "" for none.
	Ref string
	// Type is the JSON type without "null", for example "string". It is "null" only when null
	// is the only type, and "" for none.
	Type        string
	Format      string
	Description string
	// Nullable tells if the value can be null. It does not follow Ref.
	Nullable bool
	// Enum holds strings only. A nil member is null.
	Enum  []*string
	Items *Schema
	OneOf []*Schema
	// Properties are the required ones in the order of "required", then the others in name
	// order.
	Properties []Property
	// AdditionalProperties is nil when the document does not set it.
	AdditionalProperties *bool
}

type Property struct {
	Name     string
	Required bool
	Schema   *Schema
}

// Load reads the three inputs from their files.
func Load(openAPIPath, advicePath, createCallsPath string) (*Doc, error) {
	openAPI, err := os.ReadFile(openAPIPath)
	if err != nil {
		return nil, err
	}
	advice, err := os.ReadFile(advicePath)
	if err != nil {
		return nil, err
	}
	createCalls, err := os.ReadFile(createCallsPath)
	if err != nil {
		return nil, err
	}
	return Parse(openAPI, advice, createCalls)
}

// Parse reads and checks the three inputs. It fails when the operations of the document and the
// operation lists differ, when the backend codes of the document and of the advice differ, and
// on a shape that the model cannot hold.
func Parse(openAPI, advice, createCalls []byte) (*Doc, error) {
	doc := &Doc{}
	if err := doc.parseOpenAPI(openAPI); err != nil {
		return nil, fmt.Errorf("backend.json: %w", err)
	}
	var rows struct {
		Rows []AdviceRow `json:"rows"`
	}
	if err := json.Unmarshal(advice, &rows); err != nil {
		return nil, fmt.Errorf("retry-advice.json: %w", err)
	}
	doc.Advice = rows.Rows
	if err := doc.checkAdvice(); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(createCalls, &doc.CreateCalls); err != nil {
		return nil, fmt.Errorf("create-calls.json: %w", err)
	}
	if err := doc.CreateCalls.check(); err != nil {
		return nil, fmt.Errorf("create-calls.json: %w", err)
	}
	return doc, nil
}

// UsedBy returns, sorted, the names of the schemas that the given schemas use, directly or
// through another schema. A nil schema uses none.
func (d *Doc) UsedBy(schemas ...*Schema) ([]string, error) {
	seen := map[string]bool{}
	var todo []string
	for _, s := range schemas {
		todo = append(todo, s.refs()...)
	}
	for len(todo) > 0 {
		name := todo[0]
		todo = todo[1:]
		if seen[name] {
			continue
		}
		s, ok := d.Schemas[name]
		if !ok {
			return nil, fmt.Errorf("backend.json has no schema %s", name)
		}
		seen[name] = true
		todo = append(todo, s.refs()...)
	}
	return slices.Sorted(maps.Keys(seen)), nil
}

// Property returns the schema of a property, nil when there is none.
func (s *Schema) Property(name string) *Schema {
	for _, p := range s.Properties {
		if p.Name == name {
			return p.Schema
		}
	}
	return nil
}

// refs returns the names of the schemas that s refers to, at any depth inside s.
func (s *Schema) refs() []string {
	if s == nil {
		return nil
	}
	var out []string
	if s.Ref != "" {
		out = append(out, s.Ref)
	}
	out = append(out, s.Items.refs()...)
	for _, o := range s.OneOf {
		out = append(out, o.refs()...)
	}
	for _, p := range s.Properties {
		out = append(out, p.Schema.refs()...)
	}
	return out
}

// typeList is the "type" keyword of OpenAPI 3.1: one name or a list of names.
type typeList []string

func (t *typeList) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*t = typeList{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}
	*t = many
	return nil
}

const refPrefix = "#/components/schemas/"

// modelKeywords are the keywords that Schema has.
var modelKeywords = []string{
	"$ref", "type", "format", "description", "enum", "items", "oneOf", "properties", "required",
	"additionalProperties",
}

// ignoredKeywords do not change the type of a value. Each other keyword can change it, so the
// parser refuses it.
var ignoredKeywords = []string{
	"title", "default", "example", "examples", "deprecated", "readOnly", "writeOnly",
	"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf",
	"minLength", "maxLength", "pattern", "minItems", "maxItems", "uniqueItems",
}

// checkKeywords refuses a schema that is null or that has a keyword the model cannot hold.
func checkKeywords(data []byte) error {
	var keywords map[string]json.RawMessage
	if err := json.Unmarshal(data, &keywords); err != nil {
		return err
	}
	if keywords == nil {
		return fmt.Errorf("the schema is null")
	}
	for _, k := range slices.Sorted(maps.Keys(keywords)) {
		switch {
		case slices.Contains(ignoredKeywords, k):
		case !slices.Contains(modelKeywords, k):
			return fmt.Errorf("the keyword %s is not supported", k)
		case string(keywords[k]) == "null":
			return fmt.Errorf("the keyword %s is null", k)
		}
	}
	return nil
}

// parseSchema reads a schema. Unlike json.Unmarshal into a *Schema, it refuses null.
func parseSchema(data []byte) (*Schema, error) {
	s := &Schema{}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Schema) UnmarshalJSON(data []byte) error {
	if err := checkKeywords(data); err != nil {
		return err
	}
	var raw struct {
		Ref                  string                     `json:"$ref"`
		Type                 typeList                   `json:"type"`
		Format               string                     `json:"format"`
		Description          string                     `json:"description"`
		Enum                 []*string                  `json:"enum"`
		Items                *Schema                    `json:"items"`
		OneOf                []*Schema                  `json:"oneOf"`
		Properties           map[string]json.RawMessage `json:"properties"`
		Required             []string                   `json:"required"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*s = Schema{
		Format: raw.Format, Description: raw.Description, Enum: raw.Enum,
		Items: raw.Items, OneOf: raw.OneOf, AdditionalProperties: raw.AdditionalProperties,
	}
	if raw.Ref != "" {
		name, ok := strings.CutPrefix(raw.Ref, refPrefix)
		if !ok {
			return fmt.Errorf("the $ref %q does not start with %s", raw.Ref, refPrefix)
		}
		s.Ref = name
	}

	for _, t := range raw.Type {
		switch {
		case t == "null":
			s.Nullable = true
		case s.Type == "":
			s.Type = t
		default:
			return fmt.Errorf("the type %v has more than one name that is not null", raw.Type)
		}
	}
	if s.Type == "" && s.Nullable {
		s.Type = "null"
	}
	for _, o := range raw.OneOf {
		if o == nil {
			return fmt.Errorf("a oneOf member is null")
		}
		s.Nullable = s.Nullable || o.Nullable
	}
	if raw.Properties != nil && s.Type != "object" {
		return fmt.Errorf("the schema has properties, but its type is not object")
	}

	names := slices.Clone(raw.Required)
	for _, name := range slices.Sorted(maps.Keys(raw.Properties)) {
		if !slices.Contains(raw.Required, name) {
			names = append(names, name)
		}
	}
	for _, name := range names {
		body, ok := raw.Properties[name]
		if !ok {
			return fmt.Errorf("the required field %s has no property", name)
		}
		p, err := parseSchema(body)
		if err != nil {
			return fmt.Errorf("the property %s: %w", name, err)
		}
		s.Properties = append(s.Properties, Property{Name: name, Required: slices.Contains(raw.Required, name), Schema: p})
	}
	return nil
}

var httpMethods = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true, "patch": true, "head": true, "options": true,
}

type content map[string]struct {
	Schema json.RawMessage `json:"schema"`
}

type rawOperation struct {
	OperationID string `json:"operationId"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Parameters  []struct {
		Ref         string `json:"$ref"`
		Name        string `json:"name"`
		In          string `json:"in"`
		Description string `json:"description"`
	} `json:"parameters"`
	RequestBody struct {
		Ref     string  `json:"$ref"`
		Content content `json:"content"`
	} `json:"requestBody"`
	Responses map[string]struct {
		Ref     string  `json:"$ref"`
		Content content `json:"content"`
	} `json:"responses"`

	method, path string
	// pathLevel tells if the path has parameters for all its operations.
	pathLevel bool
}

func (d *Doc) parseOpenAPI(data []byte) error {
	var raw struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	d.Schemas = map[string]*Schema{}
	for name, body := range raw.Components.Schemas {
		s, err := parseSchema(body)
		if err != nil {
			return fmt.Errorf("the schema %s: %w", name, err)
		}
		d.Schemas[name] = s
	}

	byID := map[string]rawOperation{}
	for path, item := range raw.Paths {
		_, pathLevel := item["parameters"]
		for method, body := range item {
			if !httpMethods[method] {
				continue // for example "parameters"
			}
			op := rawOperation{method: strings.ToUpper(method), path: path, pathLevel: pathLevel}
			if err := json.Unmarshal(body, &op); err != nil {
				return fmt.Errorf("%s %s: %w", method, path, err)
			}
			if _, ok := byID[op.OperationID]; ok {
				return fmt.Errorf("two operations have the id %s", op.OperationID)
			}
			byID[op.OperationID] = op
		}
	}
	if err := checkOperations(byID); err != nil {
		return err
	}
	for _, id := range includedOperations {
		if err := d.addOperation(byID[id]); err != nil {
			return fmt.Errorf("the operation %s: %w", id, err)
		}
	}
	if len(d.PairingEvents) == 0 {
		return fmt.Errorf("the operation %s has no events", streamOperation)
	}
	return nil
}

// checkOperations fails when an operation is in neither list, or when a listed operation is not
// in the document.
func checkOperations(byID map[string]rawOperation) error {
	for _, id := range slices.Sorted(maps.Keys(byID)) {
		if !slices.Contains(includedOperations, id) && !slices.Contains(excludedOperations, id) {
			return fmt.Errorf("the operation %s is in neither operation list of internal/spec", id)
		}
	}
	for _, id := range slices.Concat(includedOperations, excludedOperations) {
		if _, ok := byID[id]; !ok {
			return fmt.Errorf("the listed operation %s is not in the document", id)
		}
	}
	return nil
}

var pathName = regexp.MustCompile(`\{([^}]*)\}`)

// checkPathParams fails when the names in the path and the path parameters differ.
func (op Operation) checkPathParams() error {
	var inPath, params []string
	for _, m := range pathName.FindAllStringSubmatch(op.Path, -1) {
		inPath = append(inPath, m[1])
	}
	for _, p := range op.PathParams {
		params = append(params, p.Name)
	}
	slices.Sort(inPath)
	slices.Sort(params)
	if !slices.Equal(inPath, params) {
		return fmt.Errorf("the path %s and the path parameters %v differ", op.Path, params)
	}
	return nil
}

func (d *Doc) addOperation(raw rawOperation) error {
	op := Operation{
		ID: raw.OperationID, Method: raw.method, Path: raw.path,
		Summary: raw.Summary, Description: raw.Description,
	}
	var ok bool
	if op.TimeLimit, ok = timeLimits[op.ID]; !ok {
		op.TimeLimit = defaultTimeLimit
	}
	if raw.pathLevel {
		return fmt.Errorf("path-level parameters are not supported")
	}
	for _, p := range raw.Parameters {
		switch {
		case p.Ref != "":
			return fmt.Errorf("the parameter $ref %s is not supported", p.Ref)
		case p.In == "path":
			op.PathParams = append(op.PathParams, PathParam{Name: p.Name, Description: p.Description})
		case p.In == "header" && p.Name == "Idempotency-Key":
			op.IdempotencyKey = true
		default:
			return fmt.Errorf("the %s parameter %s is not supported", p.In, p.Name)
		}
	}
	if err := op.checkPathParams(); err != nil {
		return err
	}
	if raw.RequestBody.Ref != "" {
		return fmt.Errorf("the request $ref %s is not supported", raw.RequestBody.Ref)
	}
	for mediaType, c := range raw.RequestBody.Content {
		if mediaType != "application/json" {
			return fmt.Errorf("the request has the media type %s", mediaType)
		}
		var err error
		if op.Request, err = parseSchema(c.Schema); err != nil {
			return err
		}
	}
	for _, status := range slices.Sorted(maps.Keys(raw.Responses)) {
		if !strings.HasPrefix(status, "2") {
			continue
		}
		number, err := strconv.Atoi(status)
		if err != nil {
			return fmt.Errorf("the status %s is not a number", status)
		}
		op.Statuses = append(op.Statuses, number)
		answer := raw.Responses[status]
		if answer.Ref != "" {
			return fmt.Errorf("the answer $ref %s is not supported", answer.Ref)
		}
		for mediaType, c := range answer.Content {
			switch {
			case mediaType == "application/json" && op.Answer == nil:
				op.Answer, err = parseSchema(c.Schema)
			case mediaType == "text/event-stream" && op.ID == streamOperation:
				err = d.parseEvents(c.Schema)
			default:
				err = fmt.Errorf("the status %s has one answer too many, or an answer that is not JSON: %s", status, mediaType)
			}
			if err != nil {
				return err
			}
		}
	}
	d.Operations = append(d.Operations, op)
	return nil
}

// parseEvents reads the events from the schema of a stream: one oneOf member for each event.
func (d *Doc) parseEvents(schema json.RawMessage) error {
	var raw struct {
		Items struct {
			OneOf []struct {
				Properties struct {
					Event struct {
						Const string `json:"const"`
					} `json:"event"`
					Data *Schema `json:"data"`
				} `json:"properties"`
			} `json:"oneOf"`
		} `json:"items"`
	}
	if err := json.Unmarshal(schema, &raw); err != nil {
		return err
	}
	for _, o := range raw.Items.OneOf {
		e := Event{Name: o.Properties.Event.Const, Data: o.Properties.Data}
		if e.Name == "" || e.Data == nil {
			return fmt.Errorf("the event %q has no name or no data", e.Name)
		}
		d.PairingEvents = append(d.PairingEvents, e)
	}
	return nil
}

func (c CreateCalls) check() error {
	if len(c.Operations) == 0 || len(c.Codes) == 0 {
		return fmt.Errorf("the operations or the codes are empty")
	}
	for _, id := range c.Operations {
		if !slices.Contains(includedOperations, id) {
			return fmt.Errorf("%s is not an included operation", id)
		}
	}
	if !slices.Contains(retryValues, c.Retry) {
		return fmt.Errorf("unknown retry value %q", c.Retry)
	}
	return nil
}

// checkAdvice checks the values of the rows, and checks that the backend rows are the
// Problem.code enum of backend.json in both directions.
func (d *Doc) checkAdvice() error {
	seen := map[[2]string]bool{}
	backend := map[string]bool{}
	for _, r := range d.Advice {
		if !slices.Contains(Sources, r.Source) {
			return fmt.Errorf("retry-advice.json: unknown source %q", r.Source)
		}
		if !slices.Contains(retryValues, r.Retry) {
			return fmt.Errorf("retry-advice.json: unknown retry value %q", r.Retry)
		}
		key := [2]string{r.Source, r.Code}
		if seen[key] {
			return fmt.Errorf("retry-advice.json: two rows have the %s code %s", r.Source, r.Code)
		}
		seen[key] = true
		if r.Source == "backend" {
			backend[r.Code] = true
		}
	}
	problem := d.Schemas["Problem"]
	if problem == nil || problem.Property("code") == nil {
		return fmt.Errorf("backend.json has no Problem.code")
	}
	enum := map[string]bool{}
	for _, code := range problem.Property("code").Enum {
		if code == nil {
			return fmt.Errorf("the Problem.code enum of backend.json has a null member")
		}
		if !backend[*code] {
			return fmt.Errorf("the backend code %s of backend.json is not in retry-advice.json", *code)
		}
		enum[*code] = true
	}
	for code := range backend {
		if !enum[code] {
			return fmt.Errorf("the backend code %s of retry-advice.json is not in Problem.code of backend.json", code)
		}
	}
	return nil
}
