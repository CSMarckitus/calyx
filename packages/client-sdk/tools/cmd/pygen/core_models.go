package main

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// classes returns the messages with a class, in the order of their names.
func (w *coreWriter) classes() []string {
	return slices.DeleteFunc(slices.Sorted(maps.Keys(w.isRequest)), func(name string) bool {
		return slices.Contains(w.calls, name) || coreLists[name] != ""
	})
}

func (w *coreWriter) modelsFile() ([]byte, error) {
	var b strings.Builder
	for _, name := range coreEnums {
		if err := w.writeConstants(&b, name); err != nil {
			return nil, err
		}
	}
	for _, name := range w.classes() {
		text, err := w.text("the message "+name, w.spec.Message(name).Comment)
		if err != nil {
			return nil, err
		}
		b.WriteString(classStart(name, text))
		for _, f := range w.fields[name] {
			b.WriteString("\n" + f.code())
		}
	}
	imports := "\nfrom __future__ import annotations\n\n"
	if strings.Contains(b.String(), "Sequence[") {
		imports += "from collections.abc import Sequence\n"
	}
	imports += "from dataclasses import dataclass, field\nfrom datetime import datetime\nfrom typing import Final\n"
	return []byte(header + imports + b.String()), nil
}

func (w *coreWriter) writeConstants(b *strings.Builder, name string) error {
	e := w.spec.Enum(name)
	if e == nil {
		return fmt.Errorf("pygen names the enum %s, but the .proto files do not have it", name)
	}
	text, err := w.text("the enum "+name, e.Comment)
	if err != nil {
		return err
	}
	fmt.Fprintf(b, "\n\nclass %s:\n%s", name, docstring(4, []string{text +
		" A newer core can give another value: the text is then the number of the value."}))
	for _, v := range e.Values {
		if !constantPattern.MatchString(v.Name) {
			return fmt.Errorf("the value %q of %s cannot be a Python constant name", v.Name, name)
		}
		text, err := w.text("the value "+name+"."+v.Name, v.Comment)
		if err != nil {
			return err
		}
		fmt.Fprintf(b, "\n    %s: Final = %s\n%s", v.Name, pythonString(v.Name), docstring(4, []string{text}))
	}
	return nil
}
