package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"calyx/vb6tools/internal/vbfile"
)

// includedOperations are the backend operations of the VB6 SDK, in the order of the module.
var includedOperations = []string{
	"sendMessage", "createBulkJob", "getBulkJob", "listBulkJobs", "cancelBulkJob",
	"listInstances", "getInstance",
}

const (
	reasonNotNeeded = "not needed by the ERP; the frontend and the TypeScript and Go SDKs cover it"
	reasonContract  = "excluded in every SDK (CONTRACT.md part 2)"
)

type excludedOperation struct {
	ID     string
	Reason string
}

// excludedOperations are the other operations of backend.json. A new operation must go in one
// of the two lists, so nobody adds it to the SDK, or leaves it out, by accident.
var excludedOperations = []excludedOperation{
	{"listAPIKeys", reasonNotNeeded},
	{"createAPIKey", reasonContract},
	{"revokeAPIKey", reasonNotNeeded},
	{"listClients", reasonNotNeeded},
	{"createClient", reasonNotNeeded},
	{"getClient", reasonNotNeeded},
	{"updateClient", reasonNotNeeded},
	{"deleteClient", reasonNotNeeded},
	{"inviteClientUser", reasonContract},
	{"createInstance", reasonNotNeeded},
	{"updateInstance", reasonNotNeeded},
	{"removeInstance", reasonNotNeeded},
	{"pairInstance", reasonNotNeeded},
	{"cancelPairing", reasonNotNeeded},
	{"followPairing", reasonNotNeeded},
	{"getMe", reasonNotNeeded},
	{"getOrganization", reasonNotNeeded},
	{"updateOrganization", reasonNotNeeded},
	{"login", reasonContract},
	{"logout", reasonContract},
	{"requestPasswordReset", reasonContract},
	{"confirmPasswordReset", reasonContract},
	{"acceptInvitation", reasonContract},
	{"changePassword", reasonContract},
	{"acknowledgeWhatsAppWeb", reasonContract},
	{"lookupInvitation", reasonContract},
	{"lookupPasswordReset", reasonContract},
	{"cancelInvitation", reasonContract},
	{"removeClientAccess", reasonContract},
	{"followEvents", reasonContract},
}

type fieldName struct {
	Schema string
	Field  string
}

// fieldNames are the wire names that the hand-written VB6 code uses: the request bodies and
// CalyxSendResult. The check finds a rename in backend.json before a VB6 program does.
var fieldNames = []fieldName{
	{"SendMessageRequest", "to"},
	{"SendMessageRequest", "text"},
	{"CreateBulkJobRequest", "recipients"},
	{"CreateBulkJobRequest", "text"},
	{"CreateBulkJobRequest", "messages"},
	{"BulkMessageInput", "to"},
	{"BulkMessageInput", "text"},
	{"BulkMessageInput", "delay_ms"},
	{"SendMessageResult", "message_id"},
	{"SendMessageResult", "replayed"},
}

// retryEnum maps the wire value of the retry advice to the CalyxRetry member.
var retryEnum = map[string]string{
	"unknown":       "calyxRetryUnknown",
	"retry_now":     "calyxRetryNow",
	"retry_later":   "calyxRetryLater",
	"check_if_sent": "calyxRetryCheckIfSent",
	"do_not_retry":  "calyxRetryDoNotRetry",
}

// retryOrder is the order of the enum members, which also fixes their numbers (0 to 4).
var retryOrder = []string{"unknown", "retry_now", "retry_later", "check_if_sent", "do_not_retry"}

type adviceRow struct {
	Source string `json:"source"`
	Code   string `json:"code"`
	Retry  string `json:"retry"`
}

func parseAdvice(data []byte) ([]adviceRow, error) {
	var doc struct {
		Rows []adviceRow `json:"rows"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("retry-advice.json: %w", err)
	}
	return doc.Rows, nil
}

type operation struct {
	ID     string
	Method string
	Path   string
}

type openAPIDoc struct {
	Operations []operation
	// Schemas maps a schema name to the set of its property names.
	Schemas map[string]map[string]bool
}

var httpMethods = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true, "patch": true, "head": true, "options": true,
}

func parseOpenAPI(data []byte) (openAPIDoc, error) {
	var raw struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return openAPIDoc{}, fmt.Errorf("backend.json: %w", err)
	}
	var doc openAPIDoc
	for path, item := range raw.Paths {
		for method, body := range item {
			if !httpMethods[method] {
				continue // for example "parameters"
			}
			var op struct {
				OperationID string `json:"operationId"`
			}
			if err := json.Unmarshal(body, &op); err != nil {
				return openAPIDoc{}, fmt.Errorf("backend.json %s %s: %w", method, path, err)
			}
			doc.Operations = append(doc.Operations, operation{op.OperationID, strings.ToUpper(method), path})
		}
	}
	sort.Slice(doc.Operations, func(i, j int) bool { return doc.Operations[i].ID < doc.Operations[j].ID })
	doc.Schemas = map[string]map[string]bool{}
	for name, s := range raw.Components.Schemas {
		props := map[string]bool{}
		for p := range s.Properties {
			props[p] = true
		}
		doc.Schemas[name] = props
	}
	return doc, nil
}

// checkOperations returns the included operations with their method and path. It fails when
// an operation is in neither list, or when a listed operation is not in backend.json.
func checkOperations(doc openAPIDoc) ([]operation, error) {
	byID := map[string]operation{}
	for _, op := range doc.Operations {
		byID[op.ID] = op
	}
	listed := map[string]bool{}
	for _, ex := range excludedOperations {
		listed[ex.ID] = true
	}
	var included []operation
	for _, id := range includedOperations {
		listed[id] = true
		op, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("the included operation %s is not in backend.json", id)
		}
		included = append(included, op)
	}
	for _, op := range doc.Operations {
		if !listed[op.ID] {
			return nil, fmt.Errorf("the operation %s of backend.json is in neither list of vb6gen", op.ID)
		}
	}
	for _, ex := range excludedOperations {
		if _, ok := byID[ex.ID]; !ok {
			return nil, fmt.Errorf("the excluded operation %s is not in backend.json", ex.ID)
		}
	}
	return included, nil
}

func checkFieldNames(doc openAPIDoc) error {
	for _, f := range fieldNames {
		if !doc.Schemas[f.Schema][f.Field] {
			return fmt.Errorf("the field %s is missing from the schema %s of backend.json", f.Field, f.Schema)
		}
	}
	return nil
}

var codePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// adviceRows returns the backend and client rows. VB6 has no core client, so the core rows
// are left out.
func adviceRows(rows []adviceRow) ([]adviceRow, error) {
	var out []adviceRow
	seen := map[string]bool{}
	for _, r := range rows {
		if r.Source == "core" {
			continue
		}
		if r.Source != "backend" && r.Source != "client" {
			return nil, fmt.Errorf("retry-advice.json: unknown source %q", r.Source)
		}
		if !codePattern.MatchString(r.Code) {
			return nil, fmt.Errorf("retry-advice.json: the code %q cannot be a VB6 constant name", r.Code)
		}
		if _, ok := retryEnum[r.Retry]; !ok {
			return nil, fmt.Errorf("retry-advice.json: unknown retry value %q", r.Retry)
		}
		// One constant per code, so the same code in two sources would clash.
		if seen[r.Code] {
			return nil, fmt.Errorf("retry-advice.json: the code %q is in two rows", r.Code)
		}
		seen[r.Code] = true
		out = append(out, r)
	}
	return out, nil
}

func codeConst(code string) string {
	return "CALYX_CODE_" + strings.ToUpper(code)
}

// opConst turns an operationId into a constant name, for example sendMessage into
// CALYX_OP_SEND_MESSAGE.
func opConst(id string) string {
	var b strings.Builder
	for i, r := range id {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return "CALYX_OP_" + strings.ToUpper(b.String())
}

// buildData returns CalyxData.bas.
func buildData(rows []adviceRow, doc openAPIDoc) (vbfile.File, error) {
	ops, err := checkOperations(doc)
	if err != nil {
		return vbfile.File{}, err
	}
	if err := checkFieldNames(doc); err != nil {
		return vbfile.File{}, err
	}
	rows, err = adviceRows(rows)
	if err != nil {
		return vbfile.File{}, err
	}

	body := []string{
		"' Generated by vb6gen from packages/client-sdk/testdata/retry-advice.json",
		"' and packages/shared-types/openapi/backend.json. Do not edit.",
		"Option Explicit",
		"",
	}
	body = append(body, retryLines()...)
	body = append(body, codeLines(rows)...)
	body = append(body, operationLines(ops)...)
	return vbfile.File{
		Attributes: []string{`Attribute VB_Name = "CalyxData"`},
		Body:       body,
	}, nil
}

func retryLines() []string {
	lines := []string{"Public Enum CalyxRetry"}
	for i, wire := range retryOrder {
		lines = append(lines, fmt.Sprintf("    %s = %d", retryEnum[wire], i))
	}
	lines = append(lines,
		"End Enum",
		"",
		"Public Function CalyxRetryText(ByVal Retry As CalyxRetry) As String",
		"    Select Case Retry",
	)
	for _, wire := range retryOrder[1:] {
		lines = append(lines,
			"    Case "+retryEnum[wire],
			fmt.Sprintf("        CalyxRetryText = %s", vbString(wire)),
		)
	}
	return append(lines,
		"    Case Else",
		`        CalyxRetryText = "unknown"`,
		"    End Select",
		"End Function",
		"",
	)
}

func codeLines(rows []adviceRow) []string {
	var lines []string
	for _, source := range []string{"backend", "client"} {
		lines = append(lines, "' The "+source+" codes.")
		for _, r := range rows {
			if r.Source == source {
				lines = append(lines, fmt.Sprintf("Public Const %s As String = %s", codeConst(r.Code), vbString(r.Code)))
			}
		}
		lines = append(lines, "")
	}

	lines = append(lines,
		"' The advice of retry-advice.json for a backend or client code.",
		"Public Function CalyxAdviceFor(ByVal Source As String, ByVal Code As String) As CalyxRetry",
		"    CalyxAdviceFor = calyxRetryUnknown",
		"    Select Case Source",
	)
	for _, source := range []string{"backend", "client"} {
		lines = append(lines,
			fmt.Sprintf("    Case %s", vbString(source)),
			"        Select Case Code",
		)
		for _, r := range rows {
			if r.Source == source {
				lines = append(lines,
					"        Case "+codeConst(r.Code),
					"            CalyxAdviceFor = "+retryEnum[r.Retry],
				)
			}
		}
		lines = append(lines, "        End Select")
	}
	return append(lines,
		"    End Select",
		"End Function",
		"",
	)
}

func operationLines(ops []operation) []string {
	var lines []string
	for _, op := range ops {
		lines = append(lines, fmt.Sprintf("Public Const %s As String = %s", opConst(op.ID), vbString(op.ID)))
	}
	lines = append(lines, "")
	lines = append(lines, operationFunc("CalyxOperationMethod", ops, func(op operation) string { return op.Method })...)
	lines = append(lines, "")
	lines = append(lines, operationFunc("CalyxOperationPath", ops, func(op operation) string { return op.Path })...)
	return lines
}

// operationFunc writes a function that gives one value per operation, and "" for any other
// operation.
func operationFunc(name string, ops []operation, value func(operation) string) []string {
	lines := []string{
		"Public Function " + name + "(ByVal Operation As String) As String",
		"    Select Case Operation",
	}
	for _, op := range ops {
		lines = append(lines,
			"    Case "+opConst(op.ID),
			fmt.Sprintf("        %s = %s", name, vbString(value(op))),
		)
	}
	return append(lines,
		"    Case Else",
		`        `+name+` = ""`,
		"    End Select",
		"End Function",
	)
}

// vbString writes s as a VB6 string literal, where a quote is written twice.
func vbString(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
