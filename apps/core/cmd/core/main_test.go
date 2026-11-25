package main

import (
	"bytes"
	"testing"
)

func TestVersionFlagPrintsOnlyTheVersion(t *testing.T) {
	old := version
	version = "1.2.3"
	t.Cleanup(func() { version = old })

	// --version must not read the configuration or the environment.
	getenv := func(name string) string {
		t.Errorf("--version read the environment variable %s", name)
		return ""
	}
	var out bytes.Buffer
	if code := run([]string{"--version"}, getenv, &out); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := out.String(); got != "1.2.3\n" {
		t.Fatalf("output = %q, want %q", got, "1.2.3\n")
	}
}
