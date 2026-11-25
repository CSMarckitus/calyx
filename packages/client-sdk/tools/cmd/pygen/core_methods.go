package main

import (
	"fmt"
	"slices"
	"strings"

	"calyx/sdktools/internal/protospec"
)

const coreFailedText = failedText + " Compare `code` with `CoreCode` and `ClientCode`. " +
	"No connection, or a connection that breaks, gives `CoreCode.UNAVAILABLE`."

// coreMethod is a method of the generated classes.
type coreMethod struct {
	method
	request, answer string
}

func (w *coreWriter) addMethod(rpc *protospec.Method, call coreCall) error {
	m := coreMethod{request: rpc.Request, answer: rpc.Answer}
	m.name, m.constant, m.result = methodName(rpc.Name), strings.ToUpper(methodName(rpc.Name)), rpc.Answer
	for _, f := range w.fields[rpc.Request] {
		if f.name != keyField {
			m.body = append(m.body, f.pyField)
		}
	}
	text, err := w.text("the RPC "+rpc.Name, rpc.Comment)
	if err != nil {
		return err
	}
	summary, rest, isLong := strings.Cut(text, ". ")
	m.paragraphs = []string{text}
	if isLong {
		m.paragraphs = []string{summary + ".", rest}
	}
	if m.returns, err = w.text("the message "+rpc.Answer, w.spec.Message(rpc.Answer).Comment); err != nil {
		return err
	}
	m.raises = coreFailedText
	if call.sent != "" {
		m.result = "Sent[" + rpc.Answer + "]"
		m.returns = sentText + m.returns
		m.raises += sendAdvice
		m.last = append(m.last, keyArgument(call.sent))
	}
	m.last = append(m.last, timeLimitArgument(call.limit))
	w.methods = append(w.methods, m)
	return nil
}

func (w *coreWriter) methodsFile() []byte {
	var b strings.Builder
	var classes, modules []string
	for _, rpc := range w.spec.Methods {
		limit := "None"
		if call, ok := coreCalls[rpc.Name]; ok {
			limit = fmt.Sprint(call.limit)
		}
		modules = append(modules, w.module(rpc.Answer))
		fmt.Fprintf(&b, "\n%s: Final = Rpc(\n    path=%s,\n    parse=%s.%s.FromString,\n    time_limit=%s,\n)\n",
			strings.ToUpper(methodName(rpc.Name)), pythonString(rpc.Path), w.module(rpc.Answer), rpc.Answer, limit)
	}
	for _, m := range w.methods {
		classes = append(classes, m.answer)
		for _, f := range m.body {
			classes = append(classes, className.FindAllString(f.baseType, -1)...)
		}
	}
	classes = slices.DeleteFunc(classes, func(name string) bool { return name == "Sequence" })
	for _, class := range []struct {
		name  string
		async bool
	}{{"Sync", false}, {"Async", true}} {
		fmt.Fprintf(&b, "\n\nclass %[1]sCoreMethods(%[1]sCoreBase):\n    \"\"\"The calls of the core that are not a stream.\"\"\"\n", class.name)
		for _, m := range w.methods {
			b.WriteString("\n" + m.signature(class.async, m.arguments(m.body)) + m.docstring() + m.call(class.async))
		}
	}
	return []byte(header + "\nfrom collections.abc import Sequence\nfrom typing import Final\n\nfrom calyx_client_sdk import Sent\n\n" +
		"from .._base import AsyncCoreBase, Rpc, SyncCoreBase\n" + w.protoImport(modules) +
		"from . import convert\n" + importLine(".models", classes) + b.String())
}

// call returns the one statement of a method. The request is made in a function, because the error
// of a wrong argument must have the idempotency key.
func (m *coreMethod) call(async bool) string {
	start := "        return "
	if async {
		start += "await "
	}
	function, lambda := "self._call(", "lambda: "
	var values []string
	for _, f := range m.body {
		values = append(values, f.name+"="+f.name)
	}
	if strings.HasPrefix(m.result, "Sent[") {
		function, lambda = "self._send(", "lambda key: "
		values = append(values, keyField+"=key")
	}
	request := "convert." + methodName(m.request)
	if len(values) > 0 {
		request = nested(12, lambda+request+"(", values)
	}
	parts := []string{m.constant, request, "convert.read_" + methodName(m.answer)}
	for _, f := range m.last {
		parts = append(parts, f.name+"="+f.name)
	}
	return bracketed(start+function, parts, ")")
}
