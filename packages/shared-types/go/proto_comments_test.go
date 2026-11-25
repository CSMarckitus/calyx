package sharedtypes_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The client SDKs show the comments of the .proto files in the IDE.

var (
	// needsComment matches a message, an enum, an RPC, a field and an enum value. Each pattern ends
	// with the line, so a second declaration on it cannot hide.
	needsComment = regexp.MustCompile(`^((message|enum) \w+ \{\}?|rpc [^;{}]*;|((optional|repeated) )?[\w.]+ \w+ = \d+;|\w+ = \d+;)( *//.*)?$`)
	// noComment matches every other line that a file can have.
	noComment  = regexp.MustCompile(`^$|^//|^\}$|^(service|oneof) \w+ \{$|^(syntax|package|option|import|reserved) ([^;{}"]|"[^"]*")*;$`)
	ownComment = regexp.MustCompile(`^// *\S`)
	endComment = regexp.MustCompile(`[;{}] *// *\S`)
)

// uncommented returns the lines of source that need a comment and have none, directly above or at
// the end. It also returns each line that it cannot read, so nothing is skipped in silence.
func uncommented(source string) []string {
	var found []string
	above := ""
	for i, line := range strings.Split(source, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case needsComment.MatchString(line):
			if !ownComment.MatchString(above) && !endComment.MatchString(line) {
				found = append(found, fmt.Sprintf("line %d has no comment: %s", i+1, line))
			}
		case !noComment.MatchString(line):
			found = append(found, fmt.Sprintf("line %d is not known to this test: %s", i+1, line))
		}
		above = line
	}
	return found
}

func TestProtoComments(t *testing.T) {
	files, err := filepath.Glob("../proto/calyx/core/v1/*.proto")
	if err != nil || len(files) != 3 {
		t.Fatalf("found the files %v, error %v", files, err)
	}
	for _, file := range files {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, problem := range uncommented(string(source)) {
			t.Errorf("%s: %s", filepath.Base(file), problem)
		}
	}
}

func TestUncommented(t *testing.T) {
	tests := []struct {
		name, source string
		want         []string
	}{
		{"above", "// A.\nmessage A {\n  // B.\n  optional string b = 1;\n}", nil},
		{"at the end", "enum A { // A.\n  A_B = 0; // B.\n}", nil},
		{"a message", "message A {}", []string{"line 1 has no comment: message A {}"}},
		{"an enum", "enum A {\n}", []string{"line 1 has no comment: enum A {"}},
		{"an enum value", "A_B = 0;", []string{"line 1 has no comment: A_B = 0;"}},
		{"an RPC", "rpc A (B) returns (stream C);", []string{"line 1 has no comment: rpc A (B) returns (stream C);"}},
		{"a field", "google.protobuf.Timestamp a = 1;", []string{"line 1 has no comment: google.protobuf.Timestamp a = 1;"}},
		{"a repeated field", "repeated A a = 1;", []string{"line 1 has no comment: repeated A a = 1;"}},
		{"an empty line between", "// A.\n\nmessage A {}", []string{"line 3 has no comment: message A {}"}},
		{"an empty comment", "//\nmessage A {}", []string{"line 2 has no comment: message A {}"}},
		{"the comment of the line above", "string a = 1; // A.\nstring b = 2;", []string{"line 2 has no comment: string b = 2;"}},
		{"a message on one line", "// A.\nmessage A { string b = 1; }", []string{"line 2 is not known to this test: message A { string b = 1; }"}},
		{"a oneof on one line", "oneof a { string b = 1; }", []string{"line 1 is not known to this test: oneof a { string b = 1; }"}},
		{"two fields on one line", "// A.\nstring a = 1; string b = 2;", []string{"line 2 is not known to this test: string a = 1; string b = 2;"}},
		{"a line that is not known", "string a", []string{"line 1 is not known to this test: string a"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := uncommented(test.source); !slices.Equal(got, test.want) {
				t.Errorf("got %q, want %q", got, test.want)
			}
		})
	}
}
