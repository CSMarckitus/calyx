package database_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"calyx/backend/internal/database"
	"calyx/backend/internal/testutil"
)

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

func versions(t *testing.T, pool *pgxpool.Pool) []int {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func TestMigrateCreatesEveryTable(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewEmptyDB(t)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx,
		`SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' ORDER BY table_name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		got = append(got, n)
	}
	want := []string{
		"api_keys", "bulk_jobs", "clients", "core_status", "instances", "invitations",
		"organizations", "password_resets", "schema_migrations", "sessions", "users", "webhooks",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tables = %v, want %v", got, want)
	}
	if v := versions(t, pool); !reflect.DeepEqual(v, []int{1, 2, 3}) {
		t.Fatalf("versions = %v", v)
	}
}

func TestMigrateTwiceAppliesNothing(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewEmptyDB(t)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var first time.Time
	if err := pool.QueryRow(ctx, `SELECT applied_at FROM schema_migrations WHERE version = 1`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var second time.Time
	if err := pool.QueryRow(ctx, `SELECT applied_at FROM schema_migrations WHERE version = 1`).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if !first.Equal(second) || len(versions(t, pool)) != 3 {
		t.Fatalf("second run changed the record: %v vs %v", first, second)
	}
}

func TestMigrateRefusesAnUnknownVersion(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewEmptyDB(t)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES (9999)`); err != nil {
		t.Fatal(err)
	}
	err := database.Migrate(ctx, pool)
	if err == nil || !strings.Contains(err.Error(), "9999") || !strings.Contains(err.Error(), "newer binary") {
		t.Fatalf("err = %v", err)
	}
}

func TestMigrateRefusesBeforeRunningAnything(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewEmptyDB(t)
	if _, err := pool.Exec(ctx, `CREATE TABLE schema_migrations (version int PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now());
		INSERT INTO schema_migrations (version) VALUES (9999)`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, pool); err == nil {
		t.Fatal("want an error")
	}
	if tableExists(t, pool, "organizations") {
		t.Fatal("a file ran before the check")
	}
}

func TestAFailingFileLeavesNoChange(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewEmptyDB(t)
	err := database.MigrateFS(ctx, pool, fstest.MapFS{
		"0001_a.sql": {Data: []byte("CREATE TABLE a (x int);")},
		"0002_b.sql": {Data: []byte("CREATE TABLE b (x int); SELECT * FROM missing_table;")},
	})
	if err == nil || !strings.Contains(err.Error(), "0002_b.sql") {
		t.Fatalf("err = %v", err)
	}
	if !tableExists(t, pool, "a") || tableExists(t, pool, "b") {
		t.Fatal("want table a and no table b")
	}
	if v := versions(t, pool); !reflect.DeepEqual(v, []int{1}) {
		t.Fatalf("versions = %v", v)
	}
}

func TestBadFileNamesAreRefused(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewEmptyDB(t)
	err := database.MigrateFS(ctx, pool, fstest.MapFS{"1_a.sql": {Data: []byte("CREATE TABLE a (x int);")}})
	if err == nil || !strings.Contains(err.Error(), "1_a.sql") {
		t.Fatalf("err = %v", err)
	}
	err = database.MigrateFS(ctx, pool, fstest.MapFS{
		"0001_a.sql": {Data: []byte("CREATE TABLE a (x int);")},
		"0001_b.sql": {Data: []byte("CREATE TABLE b (x int);")},
	})
	if err == nil {
		t.Fatal("want an error for a repeated version")
	}
	if tableExists(t, pool, "a") || tableExists(t, pool, "b") {
		t.Fatal("a file ran")
	}
	if v := versions(t, pool); len(v) != 0 {
		t.Fatalf("versions = %v, want none", v)
	}
}

func TestAFileWithManyStatementsRuns(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewEmptyDB(t)
	err := database.MigrateFS(ctx, pool, fstest.MapFS{"0001_a.sql": {
		Data: []byte("CREATE TABLE a (x int); CREATE TABLE b (y int); INSERT INTO a VALUES (1);"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM a`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("a has %d rows (%v)", n, err)
	}
}

// The migration 0003 renames device_name to device_identity. It keeps only the names of the list
// of the core, and adds the column locale.
func TestMigration3KeepsOnlyTheDeviceIdentitiesOfTheList(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewEmptyDB(t)
	before := fstest.MapFS{}
	for _, name := range []string{"0001_initial.sql", "0002_one_pending_invitation.sql"} {
		data, err := os.ReadFile(filepath.Join("migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		before[name] = &fstest.MapFile{Data: data}
	}
	if err := database.MigrateFS(ctx, pool, before); err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`INSERT INTO organizations (id, name, whatsapp_web_allowed) VALUES ('00000000-0000-0000-0000-000000000001', 'o', true)`,
		`INSERT INTO clients (id, organization_id, name) VALUES ('00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000001', 'c')`,
	}
	for i, device := range []string{"Front desk", "FIREFOX_LINUX"} {
		statements = append(statements, fmt.Sprintf(`INSERT INTO instances (id, organization_id, client_id, core_id, core_name,
			display_name, provider, device_name, state) VALUES ('00000000-0000-0000-0000-00000000001%d',
			'00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000002', 'main',
			'i_0000000000000000000000000000001%d', 'd', 'WHATSAPP_WEB', '%s', 'active')`, i, i, device))
	}
	for _, s := range statements {
		if _, err := pool.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}

	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `SELECT device_identity, locale FROM instances ORDER BY core_name`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var identity, locale *string
		if err := rows.Scan(&identity, &locale); err != nil {
			t.Fatal(err)
		}
		if locale != nil {
			t.Errorf("locale %q, want NULL", *locale)
		}
		if identity == nil {
			got = append(got, "NULL")
		} else {
			got = append(got, *identity)
		}
	}
	if want := []string{"NULL", "FIREFOX_LINUX"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("device_identity = %v, want %v", got, want)
	}
}
