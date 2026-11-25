package database_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"

	"calyx/core/internal/database"
	"calyx/core/internal/testutil"
)

func tableNames(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type = 'table'")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	return names
}

func TestOpenCreatesTheFolderAndTheTables(t *testing.T) {
	url := "file:" + filepath.Join(t.TempDir(), "missing", "dir", "core.sqlite")
	db, err := database.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	names := tableNames(t, db)
	for _, want := range []string{"instances", "webhooks", "bulk_jobs", "bulk_job_messages"} {
		found := false
		for _, name := range names {
			if name == want {
				found = true
			}
		}
		if !found {
			t.Errorf("table %q is missing, got %v", want, names)
		}
	}
}

func TestOpenTwiceRunsEachMigrationOnce(t *testing.T) {
	ctx := context.Background()
	url := "file:" + filepath.Join(t.TempDir(), "core.sqlite")

	db, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	db, err = database.Open(ctx, url)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM calyx_migrations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("calyx_migrations has %d rows, want 3", count)
	}
}

// TestMigration3RunsOnAVersion2Database builds a database as the core left it before
// send_records existed, then checks that Open adds the table.
func TestMigration3RunsOnAVersion2Database(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "core.sqlite")
	old, err := sql.Open("sqlite3", "file:"+path+"?_foreign_keys=on")
	if err != nil {
		t.Fatal(err)
	}
	execAll(t, old, "CREATE TABLE calyx_migrations (name TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)")
	for _, pattern := range []string{"0001_*.sql", "0002_*.sql"} {
		files, err := filepath.Glob(filepath.Join("migrations", pattern))
		if err != nil || len(files) != 1 {
			t.Fatalf("migration %s: found %v, err %v", pattern, files, err)
		}
		content, err := os.ReadFile(files[0])
		if err != nil {
			t.Fatal(err)
		}
		execAll(t, old, string(content))
		if _, err := old.Exec("INSERT INTO calyx_migrations (name, applied_at) VALUES (?, 0)", filepath.Base(files[0])); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()

	db, err := database.Open(ctx, "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var applied int
	if err := db.QueryRow("SELECT COUNT(*) FROM calyx_migrations WHERE name = '0003_send_records.sql'").Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("migration 0003 recorded %d times, want 1", applied)
	}
	if n := countRows(t, db, "send_records"); n != 0 {
		t.Fatalf("send_records has %d rows on a migrated database", n)
	}
}

func TestSendRecordsKeyIsUniquePerInstance(t *testing.T) {
	db := testutil.NewDB(t)
	shop := testutil.CreateInstance(t, db, "shop")
	other := testutil.CreateInstance(t, db, "other")
	insert := func(instanceID string, key any) error {
		_, err := db.Exec(`INSERT INTO send_records (instance_id, kind, idempotency_key, fingerprint, status, created_at)
			VALUES (?, 'SINGLE', ?, x'00', 'PENDING', 0)`, instanceID, key)
		return err
	}
	for _, step := range []struct {
		instanceID string
		key        any
	}{{shop, "k1"}, {other, "k1"}, {shop, nil}, {shop, nil}} {
		if err := insert(step.instanceID, step.key); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	if err := insert(shop, "k1"); err == nil {
		t.Fatal("a second row with the same instance and key was accepted")
	}
}

func TestInstancesHaveADeviceIdentityAndALocale(t *testing.T) {
	db := testutil.NewDB(t)
	id := testutil.CreateInstance(t, db, "shop")
	var identity, loc sql.NullString
	if err := db.QueryRow("SELECT device_identity, locale FROM instances WHERE id = ?", id).Scan(&identity, &loc); err != nil {
		t.Fatal(err)
	}
	if identity.Valid || loc.Valid {
		t.Fatalf("a new row must have no values: %v %v", identity, loc)
	}
}

func TestForeignKeysCascade(t *testing.T) {
	db := testutil.NewDB(t)
	instanceID := testutil.CreateInstance(t, db, "shop")

	statements := []string{
		`INSERT INTO webhooks (id, instance_id, event_type, url) VALUES ('w1', '` + instanceID + `', 'message.received', 'http://localhost')`,
		`INSERT INTO bulk_jobs (id, instance_id, total_messages, estimated_ms, created_at) VALUES ('j1', '` + instanceID + `', 1, 0, 0)`,
		`INSERT INTO bulk_job_messages (job_id, position, recipient, text, delay_ms) VALUES ('j1', 0, '34600111222', 'hi', 2000)`,
		`INSERT INTO send_records (instance_id, kind, fingerprint, status, created_at) VALUES ('` + instanceID + `', 'SINGLE', x'00', 'PENDING', 0)`,
		`DELETE FROM instances WHERE id = '` + instanceID + `'`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}

	for _, table := range []string{"webhooks", "bulk_jobs", "bulk_job_messages", "send_records"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("%s still has %d rows after deleting the instance", table, count)
		}
	}
}

func TestWhatsmeowStoreLivesInTheSameFile(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)

	container, err := database.NewWhatsmeowStore(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	devices, err := container.GetAllDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 0 {
		t.Fatalf("got %d devices on a new database", len(devices))
	}

	hasWhatsmeowTables := false
	for _, name := range tableNames(t, db) {
		if strings.HasPrefix(name, "whatsmeow_") {
			hasWhatsmeowTables = true
		}
	}
	if !hasWhatsmeowTables {
		t.Fatal("no whatsmeow_ tables in the Calyx database")
	}

	if _, err := database.NewWhatsmeowStore(ctx, db); err != nil {
		t.Fatalf("second call: %v", err)
	}
}

// whatsmeowWithDevice returns a database with whatsmeow's tables and one device stored, and the
// JID of that device.
func whatsmeowWithDevice(t *testing.T) (*sql.DB, string) {
	t.Helper()
	ctx := context.Background()
	db := testutil.NewDB(t)
	container, err := database.NewWhatsmeowStore(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	return db, putDevice(t, container, "34600111222", 12)
}

// putDevice stores a device with fake keys, as whatsmeow does after a pairing.
func putDevice(t *testing.T, container *sqlstore.Container, phone string, device uint8) string {
	t.Helper()
	stored := container.NewDevice()
	jid := types.NewADJID(phone, 0, device)
	stored.ID = &jid
	stored.Account = &waAdv.ADVSignedDeviceIdentity{
		Details:             []byte{1},
		AccountSignature:    make([]byte, 64),
		AccountSignatureKey: make([]byte, 32),
		DeviceSignature:     make([]byte, 64),
	}
	if err := container.PutDevice(context.Background(), stored); err != nil {
		t.Fatal(err)
	}
	return jid.String()
}

func execAll(t *testing.T, db *sql.DB, statements ...string) {
	t.Helper()
	for _, s := range statements {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestForgetUnusedWhatsmeowDataKeepsTheLIDMapWhileADeviceExists(t *testing.T) {
	db, _ := whatsmeowWithDevice(t)
	execAll(t, db, "INSERT INTO whatsmeow_lid_map (lid, pn) VALUES ('111', '34600999888')")
	if err := database.ForgetUnusedWhatsmeowData(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, "whatsmeow_lid_map"); n != 1 {
		t.Fatalf("%d LID mappings left, want 1", n)
	}
}

func TestForgetUnusedWhatsmeowDataDeletesTheLIDMapWhenNoDeviceIsLeft(t *testing.T) {
	db, _ := whatsmeowWithDevice(t)
	execAll(t, db, "INSERT INTO whatsmeow_lid_map (lid, pn) VALUES ('111', '34600999888')",
		"DELETE FROM whatsmeow_device")
	if err := database.ForgetUnusedWhatsmeowData(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, "whatsmeow_lid_map"); n != 0 {
		t.Fatalf("%d LID mappings left with no device", n)
	}
}

func TestForgetUnusedWhatsmeowDataDeletesThePrivacyTokensOfDeletedDevices(t *testing.T) {
	db, jid := whatsmeowWithDevice(t)
	execAll(t, db,
		"INSERT INTO whatsmeow_privacy_tokens (our_jid, their_jid, token, timestamp) VALUES ('"+jid+"', 'a', x'00', 1)",
		"INSERT INTO whatsmeow_privacy_tokens (our_jid, their_jid, token, timestamp) VALUES ('34600333444:3@s.whatsapp.net', 'a', x'00', 1)")
	if err := database.ForgetUnusedWhatsmeowData(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var left string
	if err := db.QueryRow("SELECT GROUP_CONCAT(our_jid) FROM whatsmeow_privacy_tokens").Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != jid {
		t.Fatalf("privacy tokens left for %q, want only %q", left, jid)
	}
}

func TestDeleteOrphanedDevicesKeepsOnlyTheDevicesOfInstances(t *testing.T) {
	db, orphan := whatsmeowWithDevice(t)
	ctx := context.Background()
	container, err := database.NewWhatsmeowStore(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	jid := putDevice(t, container, "34600555666", 7)
	id := testutil.CreateInstance(t, db, "shop")
	execAll(t, db, "UPDATE instances SET device_jid = '"+jid+"' WHERE id = '"+id+"'")

	if err := database.DeleteOrphanedDevices(ctx, db); err != nil {
		t.Fatal(err)
	}
	var left string
	if err := db.QueryRow("SELECT GROUP_CONCAT(jid) FROM whatsmeow_device").Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != jid {
		t.Fatalf("devices left %q, want only %q (orphan was %q)", left, jid, orphan)
	}
}
