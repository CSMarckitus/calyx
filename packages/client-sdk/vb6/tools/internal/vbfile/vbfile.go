// Package vbfile writes and reads the VB6 files of the SDK. vb6gen and vb6lint share it, so
// both agree on the line endings and the header comment.
package vbfile

import (
	"fmt"
	"os"
	"strings"
)

// File is one VB6 source file before it is written.
type File struct {
	// Attributes are the Attribute lines of the header, for example `Attribute VB_Name = "X"`.
	Attributes []string
	// Body is every line after the header comment.
	Body []string
}

// Header returns the header comment line. It has no version, so a release does not change it.
func Header() string {
	return "' Calyx SDK for VB6"
}

// Render returns the file with CRLF line endings. It refuses a non-ASCII character, because
// the VB6 editor reads the file in the code page of the machine.
func (f File) Render() ([]byte, error) {
	lines := make([]string, 0, len(f.Attributes)+1+len(f.Body))
	lines = append(lines, f.Attributes...)
	lines = append(lines, Header())
	lines = append(lines, f.Body...)

	var b strings.Builder
	for i, line := range lines {
		if strings.ContainsAny(line, "\r\n") {
			return nil, fmt.Errorf("line %d contains a line break", i+1)
		}
		for _, r := range line {
			if r > 0x7e || (r < 0x20 && r != '\t') {
				return nil, fmt.Errorf("line %d: character %q is not allowed (ASCII only)", i+1, r)
			}
		}
		b.WriteString(line)
		b.WriteString("\r\n")
	}
	return []byte(b.String()), nil
}

// WriteFile renders the file and writes it to path.
func (f File) WriteFile(path string) error {
	data, err := f.Render()
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
