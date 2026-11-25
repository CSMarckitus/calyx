package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoodFilesHaveNoFinding(t *testing.T) {
	got, err := lint([]string{"testdata/good/src", "testdata/good/tests", "testdata/good/examples"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range got {
		t.Errorf("unexpected finding: %s", f)
	}
}

// Each bad file is named after the rule it breaks.
func TestEachRuleFindsItsBadFile(t *testing.T) {
	cases := []struct{ rule, file string }{
		{"bom", "bom.bas"},
		{"crlf", "crlf.bas"},
		{"ascii", "ascii.bas"},
		{"vb-name", "vb_name.bas"},
		{"class-header", "class_header.cls"},
		{"option-explicit", "option_explicit.bas"},
		{"prefix", "prefix.bas"},
		{"header", "header.bas"},
	}
	got, err := lint([]string{"testdata/bad/src"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if !has(got, c.file, c.rule, "") {
			t.Errorf("rule %s: no finding for %s", c.rule, c.file)
		}
	}
}

func TestPrefixChecksPublicNames(t *testing.T) {
	got, err := lint([]string{"testdata/bad/src"})
	if err != nil {
		t.Fatal(err)
	}
	if !has(got, "CalyxPrefixName.bas", "prefix", "Foo") {
		t.Error("no prefix finding for the public function Foo")
	}
	for _, name := range []string{"BadMember", "BadConst", "BadVar", "BadDeclare", "BadSecond", "BadSecondConst", "BadAfterArray", "BadIndented"} {
		if !has(got, "CalyxPrefixMore.bas", "prefix", `"`+name+`"`) {
			t.Errorf("no prefix finding for %s", name)
		}
	}
	for _, name := range []string{"CalyxKindA", "CalyxOk", "CalyxA", "CalyxArr", "CalyxText", "CalyxB", "BadInComment", "b", "2"} {
		if has(got, "CalyxPrefixMore.bas", "prefix", `"`+name+`"`) {
			t.Errorf("false prefix finding for %s", name)
		}
	}
	if has(got, "CalyxCore.bas", "", "") {
		t.Error("CalyxCore.bas must pass every rule")
	}
}

func TestPrefixFollowsLineContinuations(t *testing.T) {
	got, err := lint([]string{"testdata/bad/src"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"BadContinued", "BadContinuedConst", "BadAfterComment"} {
		if !has(got, "CalyxContinued.bas", "prefix", `"`+name+`"`) {
			t.Errorf("no prefix finding for %s", name)
		}
	}
	for _, name := range []string{"CalyxFirst", "CalyxC1", "CalyxFn", "CalyxAfter", "BadInComment", "A", "B"} {
		if has(got, "CalyxContinued.bas", "prefix", `"`+name+`"`) {
			t.Errorf("false prefix finding for %s", name)
		}
	}
}

func TestPrefixChecksFormsWithoutPublic(t *testing.T) {
	got, err := lint([]string{"testdata/bad/src"})
	if err != nil {
		t.Fatal(err)
	}
	bad := []string{"BadFunction", "BadSub", "BadProperty", "BadPropertyLet", "BadEnum", "BadEnumMember", "BadType",
		"BadDeclareBare", "BadDeclareSub", "BadGlobal", "BadGlobalConst", "BadStaticFunction", "BadStaticSub"}
	for _, name := range bad {
		if !has(got, "CalyxPublicForms.bas", "prefix", `"`+name+`"`) {
			t.Errorf("no prefix finding for %s", name)
		}
	}
	ok := []string{"PrivateOk", "PrivateEnum", "PrivateMember", "PrivateDeclare", "PrivateStatic", "DimOk", "ConstOk",
		"FriendOk", "CalyxFunctionOk", "CalyxEnumOk", "CalyxGlobalOk"}
	for _, name := range ok {
		if has(got, "CalyxPublicForms.bas", "prefix", `"`+name+`"`) {
			t.Errorf("false prefix finding for %s", name)
		}
	}
}

func TestPrefixIgnoresCase(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	write(t, filepath.Join(src, "CalyxCore.bas"), "Attribute VB_Name = \"CalyxCore\"\r\n' Calyx SDK for VB6\r\nOption Explicit\r\nPublic Const CALYX_SDK_VERSION As String = \"0.1.0\"\r\n")
	write(t, filepath.Join(src, "calyxLower.bas"), "Attribute VB_Name = \"calyxLower\"\r\n' Calyx SDK for VB6\r\nOption Explicit\r\nPublic Const CALYX_CODE_TIMEOUT As String = \"t\"\r\nPublic Function calyxRetryNow() As Long\r\nEnd Function\r\n")
	got, err := lint([]string{src})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range got {
		t.Errorf("unexpected finding: %s", f)
	}
}

func TestMissingFolderIsSkipped(t *testing.T) {
	got, err := lint([]string{filepath.Join(t.TempDir(), "nope")})
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v; want no finding and no error", got, err)
	}
}

func TestRunExitCodes(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"testdata/good/src", "testdata/good/tests"}, &out); code != 0 {
		t.Fatalf("good: exit %d, output %q", code, out.String())
	}
	out.Reset()
	if code := run([]string{"testdata/bad/src"}, &out); code != 1 {
		t.Fatalf("bad: exit %d", code)
	}
	if !strings.Contains(out.String(), "crlf.bas:") {
		t.Fatalf("output has no file name: %q", out.String())
	}
	if code := run(nil, &out); code != 2 {
		t.Fatalf("no arguments: exit %d", code)
	}
}

func has(fs []Finding, file, rule, msg string) bool {
	for _, f := range fs {
		if filepath.Base(f.Path) == file && (rule == "" || f.Rule == rule) && strings.Contains(f.Msg, msg) {
			return true
		}
	}
	return false
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
