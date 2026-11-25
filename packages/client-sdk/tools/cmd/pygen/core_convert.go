package main

import (
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"calyx/sdktools/internal/protospec"
)

func (w *coreWriter) convertFile() []byte {
	var b strings.Builder
	for _, name := range coreEnums {
		w.writeEnum(&b, w.spec.Enum(name))
	}
	for _, name := range slices.Sorted(maps.Keys(w.isRequest)) {
		if w.isRequest[name] {
			w.writeRequest(&b, name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(w.isRequest)) {
		if !w.isRequest[name] {
			w.writeAnswer(&b, name)
		}
	}
	var modules []string
	for name := range w.isRequest {
		modules = append(modules, w.module(name))
	}
	// Python code with an import that it does not use fails the lint.
	checks := slices.DeleteFunc(append([]string{"WrongArgumentError"}, coreChecks...), func(name string) bool {
		return !regexp.MustCompile(`\b` + name + `\b`).MatchString(b.String())
	})
	// ruff wants one empty line between the imports and a constant.
	body := strings.TrimPrefix(b.String(), "\n")
	return []byte(header + "\nfrom collections.abc import Mapping\nfrom typing import Final\n\n" +
		w.protoImport(modules) + importLine(".._values", checks) + importLine(".models", w.classes()) + body)
}

// module returns the Python module of the protobuf class of a message.
func (w *coreWriter) module(message string) string {
	return strings.TrimSuffix(path.Base(w.spec.Message(message).File), ".proto") + "_pb2"
}

func (w *coreWriter) protoImport(modules []string) string {
	return importLine(".._proto."+corePackage, modules)
}

func (w *coreWriter) writeEnum(b *strings.Builder, e *protospec.Enum) {
	constant := "_" + strings.TrimSuffix(e.Prefix, "_")
	fmt.Fprintf(b, "\n\n%s: Final[Mapping[int, str]] = {\n", constant)
	for _, v := range e.Values {
		fmt.Fprintf(b, "    %d: %s,\n", v.Number, pythonString(v.Name))
	}
	b.WriteString("}\n")
	if w.checkedEnums[e.Name] {
		fmt.Fprintf(b, "\n\ndef _%s(path: str, value: object) -> str:\n    return check_constant(path, value, %s, %s)\n",
			methodName(e.Name), constant, pythonString(e.Prefix))
	}
	if w.readEnums[e.Name] {
		fmt.Fprintf(b, "\n\ndef _read_%s(number: int) -> str:\n    return %s.get(number, str(number))\n", methodName(e.Name), constant)
	}
}

// writeRequest writes the converter from the arguments of a call, or from a class, to protobuf.
func (w *coreWriter) writeRequest(b *strings.Builder, name string) {
	protoClass := w.module(name) + "." + name
	isCall, isList := slices.Contains(w.calls, name), coreLists[name] != ""
	var arguments, values []string
	checks := ""
	for _, f := range w.fields[name] {
		arguments = append(arguments, f.name+": object")
		path, value := `f"{path}.`+f.name+`"`, "value."+f.name
		switch {
		case isCall:
			path, value = pythonString(f.name), f.name
		case isList:
			path, value = "path", "value"
		}
		if f.proto != nil {
			values = append(values, requestValue(f, path, value))
			continue
		}
		var members []string
		for _, p := range f.members {
			members = append(members, p.Type)
			values = append(values, nested(8, p.Name+"=check_member(", []string{p.Type, checkFunction(p), path, value}))
		}
		problem := pythonString(f.name + " must be a " + strings.Join(members, " or a ") + ".")
		checks += fmt.Sprintf("    if not isinstance(%s, %s):\n", value, strings.Join(members, " | ")) +
			bracketed("        raise WrongArgumentError(", []string{problem}, ")")
	}
	b.WriteString("\n\n")
	switch {
	case isCall && len(arguments) == 0:
		fmt.Fprintf(b, "def %s() -> %s:\n", methodName(name), protoClass)
	case isCall:
		b.WriteString(bracketed("def "+methodName(name)+"(", slices.Insert(arguments, 0, "*"), ") -> "+protoClass+":"))
	default:
		fmt.Fprintf(b, "def _%s(path: str, value: object) -> %s:\n", methodName(name), protoClass)
		if !isList {
			checks = fmt.Sprintf("    if not isinstance(value, %[1]s):\n        raise WrongArgumentError(f\"{path} must be of the class %[1]s.\")\n", name)
		}
	}
	b.WriteString(checks + bracketed("    return "+protoClass+"(", values, ")"))
}

// checkFunction returns the name of the function that checks one value of a request.
func checkFunction(p *protospec.Field) string {
	if scalar, ok := coreScalars[p.Kind]; ok {
		return scalar.check
	}
	return "_" + methodName(p.Type)
}

// requestValue returns an argument of the constructor of protobuf.
func requestValue(f coreField, path, value string) string {
	check := checkFunction(f.proto)
	switch {
	case f.proto.Repeated:
		return nested(8, f.name+"=check_list(", []string{check, path, value})
	case f.nullable && f.proto.Kind == "bool":
		return nested(8, f.name+"=check_optional_bool(", []string{path, value})
	case f.nullable:
		return nested(8, f.name+"=check_optional(", []string{check, path, value})
	}
	return nested(8, f.name+"="+check+"(", []string{path, value})
}

// nested returns a call that is an argument on a line of its own, with a comma after it.
func nested(indent int, start string, parts []string) string {
	return strings.TrimSuffix(strings.TrimSpace(bracketed(strings.Repeat(" ", indent)+start, parts, "),")), ",")
}

// writeAnswer writes the converter from protobuf to a class.
func (w *coreWriter) writeAnswer(b *strings.Builder, name string) {
	b.WriteString("\n\n" + bracketed("def read_"+methodName(name)+"(", []string{"message: " + w.module(name) + "." + name}, ") -> "+name+":"))
	var values []string
	for _, f := range w.fields[name] {
		if f.proto.Kind == "message" && !f.proto.Optional && !f.proto.Repeated {
			b.WriteString(bracketed("    require(", []string{`message.HasField("` + f.name + `")`, pythonString(f.id)}, ")"))
		}
		values = append(values, answerValue(f))
	}
	b.WriteString(bracketed("    return "+name+"(", values, ")"))
}

// answerValue returns an argument of the constructor of a class.
func answerValue(f coreField) string {
	scalar, isScalar := coreScalars[f.proto.Kind]
	read := "read_" + methodName(f.proto.Type)
	switch {
	case isScalar:
		read = scalar.pythonType
	case f.proto.Kind == "enum":
		read = "_" + read
	case f.proto.Type == timestampType:
		read = "utc_date"
	}
	start, value := f.name+"=", "message."+f.name
	switch {
	case f.proto.Repeated && isScalar:
		return start + "tuple(" + value + ")"
	case f.proto.Repeated:
		return start + "tuple(map(" + read + ", " + value + "))"
	case f.proto.Optional:
		return nested(8, start+"if_present(", []string{"message.HasField(" + pythonString(f.name) + ")", read, value})
	case isScalar:
		return start + value
	}
	return start + read + "(" + value + ")"
}
