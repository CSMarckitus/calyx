// Package protospec reads a descriptor set of .proto files into a small model for the generators.
// The protoc flags --include_source_info and --include_imports make a set with comments.
package protospec

import (
	"fmt"
	"regexp"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// Spec is the content of one proto package, in the order of its files.
type Spec struct {
	Messages []*Message
	Enums    []*Enum
	Methods  []*Method
}

// Message is a message at the top level of a file.
type Message struct {
	Name string
	// File is the path of the .proto file, for example calyx/core/v1/event.proto.
	File    string
	Comment string
	Fields  []*Field
}

// Field is a field of a message.
type Field struct {
	Name, Comment string
	// Kind is the proto kind, for example int32, enum or message. It is "map" for a map.
	Kind string
	// Type is the name of the message or of the enum. A type of another package has its full name.
	Type string
	// Optional is true only with the keyword "optional".
	Optional, Repeated bool
	// Oneof is the name of the oneof, "" for none.
	Oneof string
}

// Enum is an enum at the top level of a file.
type Enum struct {
	Name, Comment string
	// Prefix is the start of each value name in the file, for example BULK_JOB_STATUS_.
	Prefix string
	Values []EnumValue
}

// EnumValue is a value of an enum.
type EnumValue struct {
	// Name has no prefix.
	Name    string
	Number  int
	Comment string
}

// Method is an RPC of a service.
type Method struct {
	Name string
	// Path is the gRPC path, for example /calyx.core.v1.EventService/Subscribe.
	Path, Comment                   string
	Request, Answer                 string
	StreamsRequests, StreamsAnswers bool
}

// Message returns nil when the message does not exist.
func (s *Spec) Message(name string) *Message {
	for _, m := range s.Messages {
		if m.Name == name {
			return m
		}
	}
	return nil
}

// Enum returns nil when the enum does not exist.
func (s *Spec) Enum(name string) *Enum {
	for _, e := range s.Enums {
		if e.Name == name {
			return e
		}
	}
	return nil
}

// Parse reads the files of one package from a FileDescriptorSet.
func Parse(data []byte, pkg string) (*Spec, error) {
	set := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(data, set); err != nil {
		return nil, fmt.Errorf("read the descriptor set: %w", err)
	}
	files, err := protodesc.NewFiles(set)
	if err != nil {
		return nil, fmt.Errorf("read the descriptor set: %w", err)
	}
	s := &Spec{}
	found := false
	for _, f := range set.File { // the Range of files has no order
		if f.GetPackage() != pkg {
			continue
		}
		found = true
		file, err := files.FindFileByPath(f.GetName())
		if err != nil {
			return nil, err
		}
		if err := s.add(file); err != nil {
			return nil, fmt.Errorf("%s: %w", f.GetName(), err)
		}
	}
	if !found {
		return nil, fmt.Errorf("the descriptor set has no file of the package %s", pkg)
	}
	return s, nil
}

func (s *Spec) add(file protoreflect.FileDescriptor) error {
	for i := range file.Messages().Len() {
		m, err := newMessage(file.Messages().Get(i))
		if err != nil {
			return err
		}
		s.Messages = append(s.Messages, m)
	}
	for i := range file.Enums().Len() {
		e, err := newEnum(file.Enums().Get(i))
		if err != nil {
			return err
		}
		s.Enums = append(s.Enums, e)
	}
	for i := range file.Services().Len() {
		service := file.Services().Get(i)
		for j := range service.Methods().Len() {
			d := service.Methods().Get(j)
			s.Methods = append(s.Methods, &Method{
				Name: string(d.Name()), Path: fmt.Sprintf("/%s/%s", service.FullName(), d.Name()), Comment: comment(d),
				Request: typeName(file, d.Input()), Answer: typeName(file, d.Output()),
				StreamsRequests: d.IsStreamingClient(), StreamsAnswers: d.IsStreamingServer(),
			})
		}
	}
	return nil
}

func newMessage(d protoreflect.MessageDescriptor) (*Message, error) {
	nested := d.Enums().Len()
	for i := range d.Messages().Len() {
		if !d.Messages().Get(i).IsMapEntry() {
			nested++
		}
	}
	if nested > 0 {
		return nil, fmt.Errorf("the message %s has a nested message or enum", d.Name())
	}
	m := &Message{Name: string(d.Name()), File: d.ParentFile().Path(), Comment: comment(d)}
	for i := range d.Fields().Len() {
		m.Fields = append(m.Fields, newField(d.Fields().Get(i)))
	}
	return m, nil
}

func newField(d protoreflect.FieldDescriptor) *Field {
	f := &Field{
		Name: string(d.Name()), Comment: comment(d), Kind: d.Kind().String(),
		Optional: d.HasOptionalKeyword(), Repeated: d.IsList(),
	}
	switch {
	case d.IsMap():
		f.Kind = "map"
	case d.Enum() != nil:
		f.Type = typeName(d.ParentFile(), d.Enum())
	case d.Message() != nil:
		f.Type = typeName(d.ParentFile(), d.Message())
	}
	// protoc puts each "optional" field in a oneof of its own.
	if oneof := d.ContainingOneof(); oneof != nil && !oneof.IsSynthetic() {
		f.Oneof = string(oneof.Name())
	}
	return f
}

var lowerUpper = regexp.MustCompile(`([a-z0-9])([A-Z])`)

func newEnum(d protoreflect.EnumDescriptor) (*Enum, error) {
	name := string(d.Name())
	e := &Enum{
		Name: name, Comment: comment(d),
		Prefix: strings.ToUpper(lowerUpper.ReplaceAllString(name, "${1}_${2}")) + "_",
	}
	for i := range d.Values().Len() {
		v := d.Values().Get(i)
		short, ok := strings.CutPrefix(string(v.Name()), e.Prefix)
		if !ok {
			return nil, fmt.Errorf("the value %s does not start with %s", v.Name(), e.Prefix)
		}
		e.Values = append(e.Values, EnumValue{Name: short, Number: int(v.Number()), Comment: comment(v)})
	}
	return e, nil
}

func typeName(file protoreflect.FileDescriptor, d protoreflect.Descriptor) string {
	if d.ParentFile().Package() == file.Package() {
		return string(d.Name())
	}
	return string(d.FullName())
}

// comment returns the comment above a declaration, or the one at its end.
func comment(d protoreflect.Descriptor) string {
	location := d.ParentFile().SourceLocations().ByDescriptor(d)
	text := location.LeadingComments
	if strings.TrimSpace(text) == "" {
		text = location.TrailingComments
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return strings.Join(lines, "\n")
}
