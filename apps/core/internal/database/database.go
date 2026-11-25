// Package database opens the core's SQLite file and keeps its schema up to date.
//
// One file holds both the Calyx tables (created by the SQL files in migrations/) and the
// whatsmeow tables (created by whatsmeow itself, see NewWhatsmeowStore).
package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3" // registers the "sqlite3" driver
	"go.mau.fi/whatsmeow/store/sqlstore"
)

// DefaultURL is used when DATABASE_URL is not set. It names a file that only the Go core uses.
const DefaultURL = "file:./data/core.sqlite"

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Options every connection needs. They are added to the URL only when the caller did not set them.
var requiredOptions = []string{"_foreign_keys=on", "_journal_mode=WAL", "_busy_timeout=5000"}

// Open creates the folder of the database file if needed, opens it and applies the pending
// migrations.
func Open(ctx context.Context, url string) (*sql.DB, error) {
	path, query, _ := strings.Cut(strings.TrimPrefix(url, "file:"), "?")
	if path == "" {
		return nil, fmt.Errorf("database url %q has no file path", url)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create the database folder: %w", err)
	}

	db, err := sql.Open("sqlite3", "file:"+path+"?"+withRequiredOptions(query))
	if err != nil {
		return nil, fmt.Errorf("open the database: %w", err)
	}
	// SQLite allows one writer at a time and the core is the only process that writes, so one
	// connection avoids "database is locked" errors inside the process.
	db.SetMaxOpenConns(1)

	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func withRequiredOptions(query string) string {
	options := []string{}
	if query != "" {
		options = strings.Split(query, "&")
	}
	for _, required := range requiredOptions {
		name, _, _ := strings.Cut(required, "=")
		present := false
		for _, option := range options {
			if strings.HasPrefix(option, name+"=") {
				present = true
			}
		}
		if !present {
			options = append(options, required)
		}
	}
	return strings.Join(options, "&")
}

// migrate applies, in name order, every embedded SQL file not recorded in calyx_migrations yet.
// Each file runs in its own transaction together with its record, so a failed file leaves no trace.
func migrate(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS calyx_migrations (
		name       TEXT PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("create calyx_migrations: %w", err)
	}

	names, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return err
	}
	sort.Slice(names, func(i, j int) bool { return names[i].Name() < names[j].Name() })

	for _, entry := range names {
		name := entry.Name()
		var applied int
		err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM calyx_migrations WHERE name = ?", name).Scan(&applied)
		if err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if applied > 0 {
			continue
		}
		if err := applyMigration(ctx, db, name); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, name string) error {
	content, err := migrationFiles.ReadFile("migrations/" + name)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no effect after Commit

	if _, err := tx.ExecContext(ctx, string(content)); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO calyx_migrations (name, applied_at) VALUES (?, ?)",
		name, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	return tx.Commit()
}

// NewWhatsmeowStore returns whatsmeow's device store on the same database and lets whatsmeow
// create or upgrade its own tables. It is safe to call on every start.
func NewWhatsmeowStore(ctx context.Context, db *sql.DB) (*sqlstore.Container, error) {
	container := sqlstore.NewWithDB(db, "sqlite3", nil)
	if err := container.Upgrade(ctx); err != nil {
		return nil, fmt.Errorf("upgrade the whatsmeow tables: %w", err)
	}
	return container, nil
}

// ForgetUnusedWhatsmeowData deletes the data whatsmeow keeps after its device is gone. Privacy
// tokens have no foreign key to their device, so they stay when the device is deleted. The LID
// map (phone number <-> LID of contacts) is shared by all devices and has no owner, so it is
// deleted only when no device is left. Safe to call at any time.
func ForgetUnusedWhatsmeowData(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `DELETE FROM whatsmeow_privacy_tokens
		WHERE our_jid NOT IN (SELECT jid FROM whatsmeow_device)`)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `DELETE FROM whatsmeow_lid_map
		WHERE NOT EXISTS (SELECT 1 FROM whatsmeow_device)`)
	return err
}

// DeleteOrphanedDevices deletes the whatsmeow devices that belong to no instance: an instance
// removed while it could not connect keeps its device keys. Call it only at startup, before any
// pairing: during a pairing the new device exists before its instance points to it.
func DeleteOrphanedDevices(ctx context.Context, db *sql.DB) error {
	// The other per-device tables go with the device (ON DELETE CASCADE).
	_, err := db.ExecContext(ctx, `DELETE FROM whatsmeow_device
		WHERE jid NOT IN (SELECT device_jid FROM instances WHERE device_jid IS NOT NULL)`)
	return err
}
