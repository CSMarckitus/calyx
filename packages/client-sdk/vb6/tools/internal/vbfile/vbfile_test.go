package vbfile

import (
	"strings"
	"testing"
)

func TestRenderCRLFAndHeader(t *testing.T) {
	f := File{
		Attributes: []string{`Attribute VB_Name = "CalyxData"`},
		Body:       []string{"Option Explicit", "", "Public Const A As Long = 1"},
	}
	got, err := f.Render()
	if err != nil {
		t.Fatal(err)
	}
	want := "Attribute VB_Name = \"CalyxData\"\r\n' Calyx SDK for VB6\r\nOption Explicit\r\n\r\nPublic Const A As Long = 1\r\n"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if strings.Contains(strings.ReplaceAll(string(got), "\r\n", ""), "\n") {
		t.Fatal("bare LF in output")
	}
}

func TestRenderRefusesNonASCII(t *testing.T) {
	f := File{Body: []string{"' café"}}
	if _, err := f.Render(); err == nil {
		t.Fatal("want an error for a non-ASCII character")
	}
}

func TestRenderRefusesLineBreakInLine(t *testing.T) {
	f := File{Body: []string{"a\nb"}}
	if _, err := f.Render(); err == nil {
		t.Fatal("want an error for a line break inside a line")
	}
}

func TestRenderControlCharacters(t *testing.T) {
	bad := File{Body: []string{"a\x01b"}}
	if _, err := bad.Render(); err == nil {
		t.Fatal("want an error for a control character")
	}
	ok := File{Body: []string{"a\tb"}}
	if _, err := ok.Render(); err != nil {
		t.Fatalf("a tab must be allowed: %v", err)
	}
}

func TestHeaderHasNoVersion(t *testing.T) {
	if got := Header(); got != "' Calyx SDK for VB6" {
		t.Fatalf("got %q", got)
	}
}
