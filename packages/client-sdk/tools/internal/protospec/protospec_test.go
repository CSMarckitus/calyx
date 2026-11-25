package protospec

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

// This command makes the fixture. Run it in this folder after a change of fixture.proto:
//
//	protoc --proto_path=testdata --include_source_info --include_imports \
//	  --descriptor_set_out=testdata/fixture.binpb testdata/fixture.proto
const fixture = "testdata/fixture.binpb"

func parseFixture(t *testing.T) *Spec {
	t.Helper()
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Parse(data, "fixture.v1")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMessages(t *testing.T) {
	s := parseFixture(t)
	if len(s.Messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(s.Messages))
	}
	thing := s.Message("Thing")
	if thing.File != "fixture.proto" || thing.Comment != "A thing with a field\nof each kind." {
		t.Errorf("Thing: got %q, %q", thing.File, thing.Comment)
	}
	want := []*Field{
		{Name: "name", Comment: "The name.", Kind: "string"},
		{Name: "nick", Comment: "Has   two\n\nparagraphs.", Kind: "string", Optional: true},
		{Name: "is_new", Comment: "At the end.", Kind: "bool"},
		{Name: "count", Kind: "int32"},
		{Name: "big", Kind: "int64", Optional: true},
		{Name: "ratio", Kind: "float"},
		{Name: "raw", Kind: "bytes"},
		{Name: "color", Kind: "enum", Type: "PaintColor"},
		{Name: "plain", Kind: "message", Type: "Answer"},
		{Name: "maybe", Kind: "message", Type: "Answer", Optional: true},
		{Name: "answers", Kind: "message", Type: "Answer", Repeated: true},
		{Name: "tags", Kind: "string", Repeated: true},
		{Name: "totals", Kind: "map"},
		{Name: "made_at", Kind: "message", Type: "google.protobuf.Timestamp"},
		{Name: "one", Comment: "The first choice.", Kind: "message", Type: "Answer", Oneof: "choice"},
		{Name: "two", Kind: "bool", Oneof: "choice"},
	}
	for i, f := range thing.Fields {
		if i >= len(want) || !reflect.DeepEqual(f, want[i]) {
			t.Errorf("field %d: got %+v", i, *f)
		}
	}
	if len(thing.Fields) != len(want) {
		t.Errorf("got %d fields, want %d", len(thing.Fields), len(want))
	}
	if answer := s.Message("Answer"); answer.Comment != "" || len(answer.Fields) != 0 {
		t.Errorf("Answer: got %+v", *answer)
	}
	if s.Message("Nothing") != nil {
		t.Error("a message that does not exist: want nil")
	}
}

func TestEnums(t *testing.T) {
	want := []*Enum{{
		Name: "PaintColor", Comment: "The color of a thing.", Prefix: "PAINT_COLOR_",
		Values: []EnumValue{
			{Name: "UNSPECIFIED", Number: 0, Comment: "No color."},
			{Name: "RED", Number: 5, Comment: `"Red"`},
		},
	}}
	s := parseFixture(t)
	if !reflect.DeepEqual(s.Enums, want) {
		t.Errorf("got %+v", s.Enums)
	}
	if s.Enum("PaintColor") != s.Enums[0] || s.Enum("Nothing") != nil {
		t.Error("Enum does not find an enum by its name")
	}
}

func TestMethods(t *testing.T) {
	want := []*Method{
		{Name: "Get", Path: "/fixture.v1.FixtureService/Get", Comment: "One answer.", Request: "Thing", Answer: "Answer"},
		{Name: "Watch", Path: "/fixture.v1.FixtureService/Watch", Comment: "Many answers.", Request: "Thing", Answer: "Answer", StreamsAnswers: true},
		{Name: "Upload", Path: "/fixture.v1.FixtureService/Upload", Comment: "Many requests.", Request: "Thing", Answer: "Answer", StreamsRequests: true},
	}
	if got := parseFixture(t).Methods; !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestParseRefusals(t *testing.T) {
	for _, c := range []struct {
		name, pkg, want string
		change          func(file *descriptorpb.FileDescriptorProto)
	}{
		{"no such package", "other.v1", "no file of the package other.v1", func(*descriptorpb.FileDescriptorProto) {}},
		{"a value with no prefix", "fixture.v1", "the value RED does not start with PAINT_COLOR_", func(file *descriptorpb.FileDescriptorProto) {
			file.EnumType[0].Value[1].Name = proto.String("RED")
		}},
		{"a nested message", "fixture.v1", "the message Answer has a nested message or enum", func(file *descriptorpb.FileDescriptorProto) {
			file.MessageType[1].NestedType = append(file.MessageType[1].NestedType, &descriptorpb.DescriptorProto{Name: proto.String("Inner")})
		}},
		{"a nested enum", "fixture.v1", "the message Answer has a nested message or enum", func(file *descriptorpb.FileDescriptorProto) {
			file.MessageType[1].EnumType = append(file.MessageType[1].EnumType, &descriptorpb.EnumDescriptorProto{
				Name:  proto.String("Inner"),
				Value: []*descriptorpb.EnumValueDescriptorProto{{Name: proto.String("INNER_A"), Number: proto.Int32(0)}},
			})
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			data, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatal(err)
			}
			set := &descriptorpb.FileDescriptorSet{}
			if err := proto.Unmarshal(data, set); err != nil {
				t.Fatal(err)
			}
			c.change(set.File[len(set.File)-1]) // the imports come first
			if data, err = proto.Marshal(set); err != nil {
				t.Fatal(err)
			}
			if _, err := Parse(data, c.pkg); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("want an error with %q, got %v", c.want, err)
			}
		})
	}
	if _, err := Parse([]byte("no descriptor set"), "fixture.v1"); err == nil {
		t.Error("bytes that are no descriptor set: want an error")
	}
}
