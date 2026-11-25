// Command phpgen writes the generated PHP code of the backend client package: the code
// classes, the retry advice, the operation table and the model classes in
// client/src/Generated.
//
// Run it from the tools folder. With -check, it compares the generated files with the files
// in -out, writes nothing, and exits with 1 on any difference.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"calyx/sdktools/internal/genfiles"
	"calyx/sdktools/internal/spec"
)

type paths struct {
	advice      string
	openAPI     string
	createCalls string
	out         string
}

// inputPaths returns the default inputs and output folder, relative to the tools folder.
func inputPaths(toolsDir string) paths {
	return paths{
		advice:      filepath.Join(toolsDir, "../testdata/retry-advice.json"),
		openAPI:     filepath.Join(toolsDir, "../../shared-types/openapi/backend.json"),
		createCalls: filepath.Join(toolsDir, "../testdata/create-calls.json"),
		out:         filepath.Join(toolsDir, "../php/client/src/Generated"),
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// run returns the exit code: 0 for success, 1 for a difference in -check mode, 2 for an error.
func run(args []string, out io.Writer) int {
	def := inputPaths(".")
	flags := flag.NewFlagSet("phpgen", flag.ContinueOnError)
	flags.SetOutput(out)
	openAPI := flags.String("openapi", def.openAPI, "the OpenAPI document of the backend")
	advice := flags.String("advice", def.advice, "the retry advice of every code")
	createCalls := flags.String("create-calls", def.createCalls, "the retry advice of the create calls")
	outDir := flags.String("out", def.out, "the folder of the generated PHP files")
	check := flags.Bool("check", false, "compare the generated files with -out, write nothing there")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	files, err := generate(paths{advice: *advice, openAPI: *openAPI, createCalls: *createCalls, out: *outDir})
	if err != nil {
		fmt.Fprintln(out, "phpgen:", err)
		return 2
	}
	if !*check {
		if err := genfiles.Write(files, *outDir, ".php"); err != nil {
			fmt.Fprintln(out, "phpgen:", err)
			return 2
		}
		return 0
	}
	stale, err := genfiles.Compare(files, *outDir, ".php")
	if err != nil {
		fmt.Fprintln(out, "phpgen:", err)
		return 2
	}
	for _, name := range stale {
		fmt.Fprintf(out, "phpgen: %s is out of date; run pnpm --filter @calyx/client-sdk-php generate\n", filepath.Join(*outDir, name))
	}
	if len(stale) > 0 {
		return 1
	}
	return 0
}

// generate reads the inputs and returns the files by their path in the output folder.
func generate(p paths) (map[string][]byte, error) {
	doc, err := spec.Load(p.openAPI, p.advice, p.createCalls)
	if err != nil {
		return nil, err
	}
	return buildFiles(doc)
}
