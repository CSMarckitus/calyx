// Command vb6lint checks the format rules of the VB6 files of the SDK.
//
// Usage: vb6lint <folder>... A folder that does not exist is skipped. A folder named "src"
// also gets the prefix and the header rules.
package main

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"calyx/vb6tools/internal/vbfile"
)

// Finding is one broken rule in one file.
type Finding struct {
	Path string
	Line int
	Rule string
	Msg  string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: %s: %s", f.Path, f.Line, f.Rule, f.Msg)
}

var (
	// Lines can be indented, for example inside #If ... #End If.
	// A module-level line with no access word is public too, except Dim, Const and variables.
	publicDecl = regexp.MustCompile(`(?i)^\s*(?:(?:Public|Global)\s+(?:Static\s+)?(?:Declare\s+(?:PtrSafe\s+)?)?(?:Function|Sub|Const|Enum|Type|Property\s+(?:Get|Let|Set))|(?:Static\s+)?(?:Declare\s+(?:PtrSafe\s+)?)?(?:Function|Sub|Enum|Type|Property\s+(?:Get|Let|Set)))\s+(\w+)`)
	publicVar  = regexp.MustCompile(`(?i)^\s*(?:Public|Global)\s+(?:WithEvents\s+)?(\w+)`)
	publicList = regexp.MustCompile(`(?i)^\s*(?:Public|Global)\s+(?:Const\s+|WithEvents\s+)?(.*)$`)
	enumStart  = regexp.MustCompile(`(?i)^\s*(?:(?:Public|Global)\s+)?Enum\s+`)
	enumEnd    = regexp.MustCompile(`(?i)^\s*End\s+Enum\b`)
	enumMember = regexp.MustCompile(`^\s*([A-Za-z_]\w*)`)
	firstWord  = regexp.MustCompile(`^\s*(\w+)`)
	vbName     = regexp.MustCompile(`^Attribute VB_Name = "([^"]*)"$`)
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}

// run returns the exit code: 0 for no finding, 1 for findings, 2 for a usage or read error.
func run(args []string, out io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(out, "usage: vb6lint <folder>...")
		return 2
	}
	findings, err := lint(args)
	if err != nil {
		fmt.Fprintln(out, "vb6lint:", err)
		return 2
	}
	for _, f := range findings {
		fmt.Fprintln(out, f)
	}
	if len(findings) > 0 {
		return 1
	}
	return 0
}

func lint(roots []string) ([]Finding, error) {
	var all []Finding
	for _, root := range roots {
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		isSrc := filepath.Base(filepath.Clean(root)) == "src"
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			switch strings.ToLower(filepath.Ext(path)) {
			case ".bas", ".cls", ".frm", ".vbp":
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				all = append(all, checkFile(path, data, isSrc)...)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return all, nil
}

func checkFile(path string, data []byte, isSrc bool) []Finding {
	var found []Finding
	add := func(line int, rule, format string, a ...any) {
		found = append(found, Finding{path, line, rule, fmt.Sprintf(format, a...)})
	}
	ext := strings.ToLower(filepath.Ext(path))
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		add(1, "bom", "the file starts with a byte order mark")
		data = data[3:]
	}
	rawLines := bytes.Split(data, []byte("\n"))
	lines := make([]string, len(rawLines))
	badEnding, badASCII := false, false
	for i, raw := range rawLines {
		hasLF := i < len(rawLines)-1
		if hasLF && !bytes.HasSuffix(raw, []byte("\r")) && !badEnding {
			add(i+1, "crlf", "the line ends with LF, not CRLF")
			badEnding = true
		}
		line := strings.TrimSuffix(string(raw), "\r")
		lines[i] = line
		if !badASCII && strings.IndexFunc(line, func(r rune) bool { return r > 0x7e || (r < 0x20 && r != '\t') }) >= 0 {
			add(i+1, "ascii", "the line has a character that is not ASCII")
			badASCII = true
		}
	}
	if ext == ".vbp" {
		return found
	}

	nameLine := -1
	for i, l := range lines {
		if m := vbName.FindStringSubmatch(l); m != nil {
			nameLine = i
			if m[1] != name {
				add(i+1, "vb-name", "Attribute VB_Name is %q, the file name is %q", m[1], name)
			}
			break
		}
	}
	if nameLine < 0 {
		add(1, "vb-name", "no Attribute VB_Name line")
	}
	if ext == ".cls" && (len(lines) == 0 || lines[0] != "VERSION 1.0 CLASS") {
		add(1, "class-header", "the first line is not VERSION 1.0 CLASS")
	}
	if !contains(lines, "Option Explicit") {
		add(1, "option-explicit", "no Option Explicit line")
	}
	if !isSrc || ext == ".frm" {
		return found
	}

	if !hasCalyxPrefix(name) {
		add(1, "prefix", "the name %q does not start with Calyx", name)
	}
	if ext == ".bas" {
		found = append(found, checkPublicNames(path, lines)...)
	}
	if nameLine >= 0 {
		i := nameLine + 1
		for i < len(lines) && strings.HasPrefix(lines[i], "Attribute ") {
			i++
		}
		want := vbfile.Header()
		if i >= len(lines) || lines[i] != want {
			add(i+1, "header", "the line after the attributes is not %q", want)
		}
	}
	return found
}

func checkPublicNames(path string, lines []string) []Finding {
	var found []Finding
	check := func(i int, name string) {
		if !hasCalyxPrefix(name) {
			found = append(found, Finding{path, i + 1, "prefix", fmt.Sprintf("the public name %q does not start with Calyx", name)})
		}
	}
	inEnum := false
	for _, ll := range joinContinuations(lines) {
		i, l := ll.index, ll.text
		switch {
		case inEnum && enumEnd.MatchString(l):
			inEnum = false
		case inEnum:
			if m := enumMember.FindStringSubmatch(l); m != nil {
				check(i, m[1])
			}
		case publicDecl.MatchString(l):
			m := publicDecl.FindStringSubmatch(l)
			if strings.EqualFold(constWord(l), "Const") {
				checkList(i, l, check)
			} else {
				check(i, m[1])
			}
			inEnum = enumStart.MatchString(l)
		case publicVar.MatchString(l):
			checkList(i, l, check)
		}
	}
	return found
}

// logicalLine is a statement. index is the 0-based number of its first physical line.
type logicalLine struct {
	index int
	text  string
}

// joinContinuations joins a line that ends with " _" to the next line, as VB6 does. A comment
// line that ends with " _" continues too, as in VBA, so the next line is part of the comment.
func joinContinuations(lines []string) []logicalLine {
	var out []logicalLine
	for i := 0; i < len(lines); i++ {
		text := lines[i]
		first := i
		for strings.HasSuffix(text, " _") && i+1 < len(lines) {
			i++
			text = strings.TrimSuffix(text, "_") + strings.TrimSpace(lines[i])
		}
		out = append(out, logicalLine{first, text})
	}
	return out
}

func constWord(l string) string {
	f := strings.Fields(l)
	if len(f) > 1 {
		return f[1]
	}
	return ""
}

// checkList checks every name of "Public a, b As Long" and "Public Const A = 1, B = 2".
func checkList(i int, line string, check func(int, string)) {
	rest := publicList.FindStringSubmatch(line)[1]
	for _, part := range splitDeclaration(rest) {
		if m := firstWord.FindStringSubmatch(part); m != nil {
			check(i, m[1])
		}
	}
}

// splitDeclaration splits on the commas of the declaration itself. It ignores commas inside
// parentheses (array bounds) and strings, and it stops at a comment.
func splitDeclaration(s string) []string {
	var parts []string
	depth, start, inStr := 0, 0, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '\'':
			return append(parts, s[start:i])
		case c == '(':
			depth++
		case c == ')':
			depth--
		case c == ',' && depth == 0:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

func hasCalyxPrefix(name string) bool {
	return strings.HasPrefix(strings.ToLower(name), "calyx")
}

func contains(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}
