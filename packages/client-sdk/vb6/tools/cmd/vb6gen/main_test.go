package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"calyx/vb6tools/internal/vbfile"
)

var update = flag.Bool("update", false, "write testdata/CalyxData.golden.bas again")

// toolsDir is the tools module root, seen from this package folder.
const toolsDir = "../.."

func readInput(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func realAdvice(t *testing.T) []adviceRow {
	t.Helper()
	rows, err := parseAdvice(readInput(t, inputPaths(toolsDir).advice))
	if err != nil {
		t.Fatal(err)
	}
	return rows
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

func parseDoc(t *testing.T, doc map[string]any) openAPIDoc {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseOpenAPI(data)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

// realData renders CalyxData.bas from the real inputs.
func realData(t *testing.T) string {
	t.Helper()
	f, err := buildData(realAdvice(t), parseDoc(t, realOpenAPI(t)))
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Render()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// caseValues reads a generated function of the form
// "Case X" followed by "<Name> = <value>", and returns X -> value.
func caseValues(t *testing.T, src, name string) map[string]string {
	t.Helper()
	start := strings.Index(src, "Public Function "+name+"(")
	if start < 0 {
		t.Fatalf("no function %s", name)
	}
	body, _, found := strings.Cut(src[start:], "End Function")
	if !found {
		t.Fatalf("no End Function after %s", name)
	}
	lines := strings.Split(body, "\r\n")
	got := map[string]string{}
	for i, l := range lines {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "Case ") || l == "Case Else" || i+1 >= len(lines) {
			continue
		}
		next := strings.TrimSpace(lines[i+1])
		prefix := name + " = "
		if strings.HasPrefix(next, prefix) {
			got[strings.TrimPrefix(l, "Case ")] = strings.TrimPrefix(next, prefix)
		}
	}
	return got
}

func constName(code string) string {
	return "CALYX_CODE_" + strings.ToUpper(code)
}

// Failure tests.

func TestFailsOnOperationInNeitherList(t *testing.T) {
	doc := realOpenAPI(t)
	paths := doc["paths"].(map[string]any)
	paths["/api/v1/new-thing"] = map[string]any{"get": map[string]any{"operationId": "getNewThing"}}
	_, err := buildData(realAdvice(t), parseDoc(t, doc))
	if err == nil || !strings.Contains(err.Error(), "getNewThing") {
		t.Fatalf("want an error that names getNewThing, got %v", err)
	}
}

func TestFailsOnMissingFieldName(t *testing.T) {
	doc := realOpenAPI(t)
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	props := schemas["BulkMessageInput"].(map[string]any)["properties"].(map[string]any)
	delete(props, "delay_ms")
	_, err := buildData(realAdvice(t), parseDoc(t, doc))
	if err == nil || !strings.Contains(err.Error(), "delay_ms") {
		t.Fatalf("want an error that names delay_ms, got %v", err)
	}
}

func TestFailsOnChangedHash(t *testing.T) {
	src := readInput(t, inputPaths(toolsDir).vbaJSON)
	if err := checkSHA256(src); err != nil {
		t.Fatalf("the pinned file must pass: %v", err)
	}
	changed := append(bytes.Clone(src), ' ')
	if err := checkSHA256(changed); err == nil {
		t.Fatal("want an error for a changed file")
	}
}

func TestFailsOnPublicNameNotInRenameList(t *testing.T) {
	src := "Attribute VB_Name = \"JsonConverter\"\nOption Explicit\nPublic Function ParseJson() As Object\nEnd Function\nPublic Function NewHelper() As Long\nEnd Function\n"
	_, err := transformJSON([]byte(src))
	if err == nil || !strings.Contains(err.Error(), "NewHelper") {
		t.Fatalf("want an error that names NewHelper, got %v", err)
	}
}

func TestPublicNamesOfOneLine(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{`Declare Function Foo Lib "kernel32" () As Long`, []string{"Foo"}},
		{`Declare PtrSafe Sub Bar Lib "kernel32" ()`, []string{"Bar"}},
		{`Public Declare Function Baz Lib "kernel32" () As Long`, []string{"Baz"}},
		{`Public A As Long, B As Long`, []string{"A", "B"}},
		{`Public Const C1 = 1, C2 = "x, y", C3 = 3 ' D4`, []string{"C1", "C2", "C3"}},
		{`Public Arr(1, 2) As Long, After As Long`, []string{"Arr", "After"}},
		{`Public Function Fn(ByVal X As Long, ByVal Y As Long) As Long`, []string{"Fn"}},
		{`Private A As Long, B As Long`, nil},
		{`    Dim A As Long, B As Long`, nil},
	}
	for _, c := range cases {
		got := publicNames(c.line)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%q: got %v, want %v", c.line, got, c.want)
		}
	}
}

func TestFailsOnSecondPublicNameOfOneLine(t *testing.T) {
	src := "Attribute VB_Name = \"JsonConverter\"\nOption Explicit\nPublic ParseJson As Long, NewSecond As Long\n"
	_, err := transformJSON([]byte(src))
	if err == nil || !strings.Contains(err.Error(), "NewSecond") {
		t.Fatalf("want an error that names NewSecond, got %v", err)
	}
}

func TestFailsOnDeclareWithoutAccessWord(t *testing.T) {
	src := "Attribute VB_Name = \"JsonConverter\"\nOption Explicit\nDeclare Function NewApi Lib \"kernel32\" () As Long\n"
	_, err := transformJSON([]byte(src))
	if err == nil || !strings.Contains(err.Error(), "NewApi") {
		t.Fatalf("want an error that names NewApi, got %v", err)
	}
}

// Positive tests.

// adviceBlock returns the constant -> value pairs of one Case "<source>" block of CalyxAdviceFor.
func adviceBlock(t *testing.T, src, source string) map[string]string {
	t.Helper()
	start := strings.Index(src, "Public Function CalyxAdviceFor(")
	if start < 0 {
		t.Fatal("no function CalyxAdviceFor")
	}
	body, _, _ := strings.Cut(src[start:], "End Function")
	_, block, found := strings.Cut(body, "    Case \""+source+"\"\r\n")
	if !found {
		t.Fatalf("no Case %q in CalyxAdviceFor", source)
	}
	// The block ends at the next source case, or at the end of the function.
	if end := strings.Index(block, "\r\n    Case \""); end >= 0 {
		block = block[:end]
	}
	lines := strings.Split(block, "\r\n")
	got := map[string]string{}
	for i, l := range lines {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "Case ") || l == "Case Else" || i+1 >= len(lines) {
			continue
		}
		next := strings.TrimSpace(lines[i+1])
		if value, ok := strings.CutPrefix(next, "CalyxAdviceFor = "); ok {
			got[strings.TrimPrefix(l, "Case ")] = value
		}
	}
	return got
}

func checkAdviceBlock(t *testing.T, source string) {
	t.Helper()
	enum := map[string]string{
		"retry_now":     "calyxRetryNow",
		"retry_later":   "calyxRetryLater",
		"check_if_sent": "calyxRetryCheckIfSent",
		"do_not_retry":  "calyxRetryDoNotRetry",
		"unknown":       "calyxRetryUnknown",
	}
	got := adviceBlock(t, realData(t), source)
	used := 0
	for _, row := range realAdvice(t) {
		if row.Source != source {
			continue
		}
		used++
		if got[constName(row.Code)] != enum[row.Retry] {
			t.Errorf("%s/%s: got %q, want %q", row.Source, row.Code, got[constName(row.Code)], enum[row.Retry])
		}
	}
	if used == 0 || len(got) != used {
		t.Errorf("the %s block has %d cases, want %d", source, len(got), used)
	}
}

func TestAdviceForBackendRows(t *testing.T) { checkAdviceBlock(t, "backend") }

func TestAdviceForClientRows(t *testing.T) { checkAdviceBlock(t, "client") }

func TestCodeConstantNames(t *testing.T) {
	src := realData(t)
	constLine := regexp.MustCompile(`(?m)^Public Const (CALYX_CODE_\w+) As String = "([^"]*)"\r$`)
	found := map[string]string{}
	for _, m := range constLine.FindAllStringSubmatch(src, -1) {
		found[m[2]] = m[1]
	}
	want := 0
	for _, row := range realAdvice(t) {
		if row.Source == "core" {
			continue
		}
		want++
		if found[row.Code] != constName(row.Code) {
			t.Errorf("code %s: constant %q, want %q", row.Code, found[row.Code], constName(row.Code))
		}
	}
	if len(found) != want {
		t.Errorf("got %d code constants, want %d", len(found), want)
	}
}

func TestOperationsMatchBackend(t *testing.T) {
	src := realData(t)
	opConst := regexp.MustCompile(`(?m)^Public Const (CALYX_OP_\w+) As String = "([^"]*)"\r$`)
	consts := map[string]string{}
	for _, m := range opConst.FindAllStringSubmatch(src, -1) {
		consts[m[2]] = m[1]
	}
	methods := caseValues(t, src, "CalyxOperationMethod")
	paths := caseValues(t, src, "CalyxOperationPath")

	want := map[string][2]string{}
	pathItems, ok := realOpenAPI(t)["paths"].(map[string]any)
	if !ok {
		t.Fatal("backend.json has no paths object")
	}
	for path, item := range pathItems {
		operations, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for method, op := range operations {
			// A path item can hold values that are not operations, such as "parameters".
			opObject, ok := op.(map[string]any)
			if !ok {
				continue
			}
			id, _ := opObject["operationId"].(string)
			want[id] = [2]string{strings.ToUpper(method), path}
		}
	}
	ops := []string{"sendMessage", "createBulkJob", "getBulkJob", "listBulkJobs", "cancelBulkJob", "listInstances", "getInstance"}
	if len(consts) != len(ops) {
		t.Errorf("got %d operation constants, want %d", len(consts), len(ops))
	}
	for _, id := range ops {
		c := consts[id]
		if c == "" {
			t.Errorf("no constant for %s", id)
			continue
		}
		if methods[c] != `"`+want[id][0]+`"` || paths[c] != `"`+want[id][1]+`"` {
			t.Errorf("%s: got %s %s, want %s %s", id, methods[c], paths[c], want[id][0], want[id][1])
		}
	}
}

func TestListsCoverAllOperations(t *testing.T) {
	doc := parseDoc(t, realOpenAPI(t))
	if len(doc.Operations) != 37 {
		t.Fatalf("backend.json has %d operations, want 37", len(doc.Operations))
	}
	if len(includedOperations) != 7 || len(excludedOperations) != 30 {
		t.Fatalf("got %d included and %d excluded, want 7 and 30", len(includedOperations), len(excludedOperations))
	}
	reasons := map[string]int{}
	for _, ex := range excludedOperations {
		reasons[ex.Reason]++
	}
	if reasons[reasonNotNeeded] != 16 || reasons[reasonContract] != 14 {
		t.Errorf("reasons: %v, want 16 and 14", reasons)
	}
	listed := map[string]bool{}
	for _, id := range includedOperations {
		listed[id] = true
	}
	for _, ex := range excludedOperations {
		if listed[ex.ID] {
			t.Errorf("%s is in both lists", ex.ID)
		}
		listed[ex.ID] = true
	}
	for _, op := range doc.Operations {
		if !listed[op.ID] {
			t.Errorf("%s is in neither list", op.ID)
		}
	}
}

func TestGoldenData(t *testing.T) {
	got := []byte(realData(t))
	golden := filepath.Join("testdata", "CalyxData.golden.bas")
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want := readInput(t, golden)
	if !bytes.Equal(got, want) {
		t.Fatal("output differs from testdata/CalyxData.golden.bas; run go test ./cmd/vb6gen -update and review the diff")
	}
}

// Tests of the JSON transform steps.

func TestTransformJSONRenamesWholeIdentifiersInCodeOnly(t *testing.T) {
	src := strings.Join([]string{
		`Attribute VB_Name = "JsonConverter"`,
		`' ParseJson in a comment`,
		`Option Explicit`,
		`Public JsonOptions As json_Options`,
		`Public Function ParseJson(ByVal s As String) As Object`,
		`    Set ParseJson = Nothing: x = "ParseJson ""ParseJson"" ' text" ' ParseJson`,
		`    y = ParseJsonX + json_ParseJson + JsonOptions.EscapeSolidus`,
		`End Function`,
		``,
	}, "\n")
	f, err := transformJSON([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Render()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		`Attribute VB_Name = "CalyxJson"`,
		`' Calyx SDK for VB6`,
		generatedJSONNote,
		`' ParseJson in a comment`,
		`Option Explicit`,
		`Public CalyxJsonOptions As json_Options`,
		`Public Function CalyxParseJson(ByVal s As String) As Object`,
		`    Set CalyxParseJson = Nothing: x = "ParseJson ""ParseJson"" ' text" ' ParseJson`,
		`    y = ParseJsonX + json_ParseJson + CalyxJsonOptions.EscapeSolidus`,
		`End Function`,
		``,
	}, "\r\n")
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestTransformJSONKeepsNoticeOnRealFile(t *testing.T) {
	src := readInput(t, inputPaths(toolsDir).vbaJSON)
	f, err := transformJSON(src)
	if err != nil {
		t.Fatal(err)
	}
	if f.Body[1] != "''" || f.Body[2] != "' VBA-JSON v2.3.1" {
		t.Fatalf("the notice is not at the top: %q", f.Body[:3])
	}
	for _, name := range []string{"ParseJson", "ConvertToJson", "ParseUtc", "ConvertToUtc", "ParseIso", "ConvertToIso"} {
		if !contains(f.Body, "Public Function Calyx"+name+"(") {
			t.Errorf("no Public Function Calyx%s", name)
		}
	}
}

func contains(lines []string, prefix string) bool {
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

// compare checks a fixed list of two files, so the three states need two calls.
func TestCompareFindsEqualStaleAndMissingFilesOverTwoCalls(t *testing.T) {
	files := map[string]vbfile.File{
		"CalyxData.bas": {Body: []string{"Option Explicit", "' data"}},
		"CalyxJson.bas": {Body: []string{"Option Explicit", "' json"}},
	}
	src := t.TempDir()
	write := func(name string, content []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(src, name), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("CalyxData.bas", mustRender(t, files["CalyxData.bas"]))
	write("CalyxJson.bas", mustRender(t, files["CalyxJson.bas"]))
	stale, err := compare(files, src)
	if err != nil || len(stale) != 0 {
		t.Fatalf("both equal: got %v, %v; want no difference", stale, err)
	}
	// Call 1: CalyxData.bas is equal and CalyxJson.bas is stale.
	write("CalyxJson.bas", []byte("old\r\n"))
	stale, err = compare(files, src)
	if err != nil || len(stale) != 1 || stale[0] != "CalyxJson.bas" {
		t.Fatalf("equal and stale: got %v, %v; want [CalyxJson.bas]", stale, err)
	}
	// Call 2: CalyxData.bas is missing and CalyxJson.bas is equal.
	write("CalyxJson.bas", mustRender(t, files["CalyxJson.bas"]))
	if err := os.Remove(filepath.Join(src, "CalyxData.bas")); err != nil {
		t.Fatal(err)
	}
	stale, err = compare(files, src)
	if err != nil || len(stale) != 1 || stale[0] != "CalyxData.bas" {
		t.Fatalf("missing and equal: got %v, %v; want [CalyxData.bas]", stale, err)
	}
}

func mustRender(t *testing.T, f vbfile.File) []byte {
	t.Helper()
	data, err := f.Render()
	if err != nil {
		t.Fatal(err)
	}
	return data
}
