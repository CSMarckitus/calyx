// Command vb6gen writes the generated VB6 modules of the SDK: src/CalyxData.bas (codes, retry
// advice and operations) and src/CalyxJson.bas (VBA-JSON with the Calyx prefix).
//
// Run it from the tools folder. With -check, it writes the files to a temporary folder,
// compares them with the files in ../src and exits with 1 on any difference.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"calyx/vb6tools/internal/vbfile"
)

type paths struct {
	advice  string
	openAPI string
	vbaJSON string
	src     string
}

// inputPaths returns the inputs and the output folder, relative to the tools folder.
func inputPaths(toolsDir string) paths {
	return paths{
		advice:  filepath.Join(toolsDir, "../../testdata/retry-advice.json"),
		openAPI: filepath.Join(toolsDir, "../../../shared-types/openapi/backend.json"),
		vbaJSON: filepath.Join(toolsDir, "../third_party/vba-json/JsonConverter.bas"),
		src:     filepath.Join(toolsDir, "../src"),
	}
}

func main() {
	os.Exit(run(os.Args[1:], ".", os.Stderr))
}

// run returns the exit code: 0 for success, 1 for a difference in -check mode, 2 for an error.
func run(args []string, toolsDir string, out io.Writer) int {
	flags := flag.NewFlagSet("vb6gen", flag.ContinueOnError)
	flags.SetOutput(out)
	check := flags.Bool("check", false, "compare the generated files with ../src, write nothing there")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	p := inputPaths(toolsDir)
	files, err := generate(p)
	if err != nil {
		fmt.Fprintln(out, "vb6gen:", err)
		return 2
	}
	if !*check {
		for name, f := range files {
			if err := f.WriteFile(filepath.Join(p.src, name)); err != nil {
				fmt.Fprintln(out, "vb6gen:", err)
				return 2
			}
		}
		return 0
	}
	stale, err := compare(files, p.src)
	if err != nil {
		fmt.Fprintln(out, "vb6gen:", err)
		return 2
	}
	for _, name := range stale {
		fmt.Fprintf(out, "vb6gen: %s is out of date; run pnpm --filter @calyx/client-sdk-vb6 generate\n", filepath.Join(p.src, name))
	}
	if len(stale) > 0 {
		return 1
	}
	return 0
}

// generate reads the three inputs, and returns the files by name.
func generate(p paths) (map[string]vbfile.File, error) {
	adviceData, err := os.ReadFile(p.advice)
	if err != nil {
		return nil, err
	}
	rows, err := parseAdvice(adviceData)
	if err != nil {
		return nil, err
	}
	openAPIData, err := os.ReadFile(p.openAPI)
	if err != nil {
		return nil, err
	}
	doc, err := parseOpenAPI(openAPIData)
	if err != nil {
		return nil, err
	}
	data, err := buildData(rows, doc)
	if err != nil {
		return nil, err
	}
	vbaJSON, err := os.ReadFile(p.vbaJSON)
	if err != nil {
		return nil, err
	}
	jsonFile, err := buildJSON(vbaJSON)
	if err != nil {
		return nil, err
	}
	return map[string]vbfile.File{"CalyxData.bas": data, "CalyxJson.bas": jsonFile}, nil
}

// compare writes the files to a temporary folder and returns the names that differ from the
// files in src. A missing file in src counts as a difference.
func compare(files map[string]vbfile.File, src string) ([]string, error) {
	tmp, err := os.MkdirTemp("", "vb6gen-check-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	var stale []string
	for _, name := range []string{"CalyxData.bas", "CalyxJson.bas"} {
		if err := files[name].WriteFile(filepath.Join(tmp, name)); err != nil {
			return nil, err
		}
		want, err := os.ReadFile(filepath.Join(tmp, name))
		if err != nil {
			return nil, err
		}
		got, err := os.ReadFile(filepath.Join(src, name))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if !bytes.Equal(got, want) {
			stale = append(stale, name)
		}
	}
	return stale, nil
}
