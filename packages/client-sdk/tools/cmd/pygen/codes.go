package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"calyx/sdktools/internal/spec"
)

// codeClasses are the classes of codes.py, in the order of the file.
var codeClasses = []struct{ source, name, doc string }{
	{"backend", "BackendCode", "The error codes of the backend. A newer backend can send other codes."},
	{"core", "CoreCode", "The error codes of the core. A newer core can send other codes."},
	{"client", "ClientCode", "The error codes of the SDK itself. They do not come from a server."},
}

var constantPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// constantName returns the Python name of a code: upper case, without the ERROR_REASON_ prefix
// of the core reasons.
func constantName(code string) string {
	return strings.TrimPrefix(strings.ToUpper(code), "ERROR_REASON_")
}

// checkCodes checks that each code gives a Python name that is unique in its class.
func checkCodes(rows []spec.AdviceRow) error {
	seen := map[[2]string]bool{}
	for _, r := range rows {
		key := [2]string{r.Source, constantName(r.Code)}
		if !constantPattern.MatchString(key[1]) || seen[key] {
			return fmt.Errorf("retry-advice.json: the %s code %q gives no unique Python constant name", r.Source, r.Code)
		}
		seen[key] = true
	}
	return nil
}

// pythonString writes s as a Python string. Go and Python have the same escapes.
func pythonString(s string) string {
	return strconv.Quote(s)
}

func codesFile(rows []spec.AdviceRow) []byte {
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\nfrom typing import Final\n")
	for _, class := range codeClasses {
		fmt.Fprintf(&b, "\n\nclass %s:\n    \"\"\"%s\"\"\"\n\n", class.name, class.doc)
		for _, r := range rows {
			if r.Source == class.source {
				// Final without a type: a type checker reads the value as a Literal.
				fmt.Fprintf(&b, "    %s: Final = %s\n", constantName(r.Code), pythonString(r.Code))
			}
		}
	}
	return []byte(b.String())
}

// adviceFile uses the string values of the advice, so errors.py can import advice.py.
func adviceFile(rows []spec.AdviceRow) []byte {
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\nfrom collections.abc import Mapping\nfrom typing import Final\n\n")
	b.WriteString("# The retry advice of testdata/retry-advice.json, by source and code.\n")
	b.WriteString("ADVICE: Final[Mapping[tuple[str, str], str]] = {\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "    (%s, %s): %s,\n", pythonString(r.Source), pythonString(r.Code), pythonString(r.Retry))
	}
	b.WriteString("}\n")
	return []byte(b.String())
}
