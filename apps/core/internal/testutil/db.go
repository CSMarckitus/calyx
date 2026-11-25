// Package testutil holds helpers shared by the tests of several packages.
package testutil

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"calyx/core/internal/database"
)

// NewDB opens a fresh, migrated database in a temporary folder, closed when the test ends.
func NewDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Open(context.Background(), "file:"+filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// CreateInstance inserts a connected WhatsApp Web instance and returns its id.
func CreateInstance(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	id := uuid.NewString()
	now := time.Now().UnixMilli()
	_, err := db.Exec(`INSERT INTO instances (id, name, provider, connection_status, created_at, updated_at)
		VALUES (?, ?, 'WHATSAPP_WEB', 'CONNECTED', ?, ?)`, id, name, now, now)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
