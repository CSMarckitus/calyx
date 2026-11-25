package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"calyx/backend/internal/database"
	"calyx/backend/internal/testutil"
)

func envOf(databaseURL string) func(string) string {
	m := map[string]string{
		"DATABASE_URL": databaseURL,
		"PUBLIC_URL":   "https://api.calyx.test",
		"CORE_ADDRESS": "core:50051",
		"CORE_TOKEN":   "0123456789abcdef0123456789abcdef",
		"SECRET_KEY":   "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
	}
	return func(name string) string { return m[name] }
}

func runCmd(args []string, getenv func(string) string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run(context.Background(), args, getenv, &out, &errOut)
	return code, out.String(), errOut.String()
}

func tableExists(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var n int
	err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1`, name).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func TestNoArgumentPrintsTheUsage(t *testing.T) {
	code, stdout, stderr := runCmd(nil, envOf(""))
	if code != 2 || !strings.Contains(stderr, "Usage: backend <command>") || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestHelp(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		code, stdout, _ := runCmd([]string{arg}, envOf(""))
		if code != 0 || !strings.Contains(stdout, "Usage:") || !strings.Contains(stdout, "instances orphans") {
			t.Fatalf("%s: code=%d stdout=%q", arg, code, stdout)
		}
	}
}

// "backend openapi" prints the OpenAPI document and needs no environment variable.
func TestOpenAPIPrintsTheDocument(t *testing.T) {
	code, stdout, stderr := runCmd([]string{"openapi"}, func(string) string { return "" })
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("the output is not JSON: %v", err)
	}
	if doc["openapi"] != "3.1.0" || !strings.HasSuffix(stdout, "}\n") {
		t.Fatalf("openapi = %v", doc["openapi"])
	}
	if _, stdout, _ := runCmd([]string{"help"}, envOf("")); !strings.Contains(stdout, "openapi") {
		t.Fatal("the usage does not list openapi")
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, stderr := runCmd([]string{"frobnicate"}, envOf(""))
	if code != 2 || !strings.Contains(stderr, `unknown command "frobnicate"`) || !strings.Contains(stderr, "Usage:") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestMigrateWithAnInvalidConfiguration(t *testing.T) {
	code, _, stderr := runCmd([]string{"migrate"}, func(string) string { return "" })
	if code != 1 || !strings.Contains(stderr, "DATABASE_URL: is required.") || !strings.HasPrefix(stderr, "Error: invalid configuration:") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestMigrateAppliesTheMigrations(t *testing.T) {
	url := testutil.NewDatabaseURL(t)
	code, stdout, stderr := runCmd([]string{"migrate"}, envOf(url))
	if code != 0 || stdout != "Database migrated.\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	pool, err := database.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if !tableExists(t, pool, "users") {
		t.Fatal("table users does not exist")
	}
	if code, _, stderr := runCmd([]string{"migrate"}, envOf(url)); code != 0 {
		t.Fatalf("second run: code=%d stderr=%q", code, stderr)
	}
}

func TestMigrateRefusesWhileAnotherProcessHoldsTheLock(t *testing.T) {
	url := testutil.NewDatabaseURL(t)
	pool, err := database.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	release, err := database.Lock(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	code, _, stderr := runCmd([]string{"migrate"}, envOf(url))
	if code != 1 || !strings.Contains(stderr, "another backend process is running") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if tableExists(t, pool, "users") {
		t.Fatal("migrate ran while the lock was held")
	}
}

func TestMigrateReleasesTheLock(t *testing.T) {
	url := testutil.NewDatabaseURL(t)
	if code, _, stderr := runCmd([]string{"migrate"}, envOf(url)); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	pool, err := database.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	release, err := database.Lock(context.Background(), pool)
	if err != nil {
		t.Fatalf("Lock after migrate: %v", err)
	}
	release()
}

func TestMigrateDoesNotPrintTheDatabasePassword(t *testing.T) {
	code, _, stderr := runCmd([]string{"migrate"}, envOf("postgres://u:topsecretpw@127.0.0.1:1/x?connect_timeout=2"))
	if code != 1 || !strings.HasPrefix(stderr, "Error: cannot connect to the database") || strings.Contains(stderr, "topsecretpw") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestVersionFlagPrintsOnlyTheVersion(t *testing.T) {
	old := version
	version = "1.2.3"
	t.Cleanup(func() { version = old })

	// --version must not read the configuration or the environment.
	getenv := func(name string) string {
		t.Errorf("--version read the environment variable %s", name)
		return ""
	}
	code, stdout, stderr := runCmd([]string{"--version"}, getenv)
	if code != 0 || stdout != "1.2.3\n" || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestUsageListsTheVersionFlag(t *testing.T) {
	_, stdout, _ := runCmd([]string{"help"}, envOf(""))
	if !strings.Contains(stdout, "--version") {
		t.Fatalf("the usage text does not list --version: %q", stdout)
	}
}
