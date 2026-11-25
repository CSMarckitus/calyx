package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"calyx/backend/internal/database"
)

// NewDatabaseURL creates an empty database calyx_test_<16 hex> on the server of
// TEST_DATABASE_URL and returns its URL. t.Cleanup drops the database.
func NewDatabaseURL(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("database test: skipped with -short")
	}
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Fatal("TEST_DATABASE_URL is not set. Start the test database: " +
			"docker compose -f docker-compose.dev.yml --profile test up -d postgres-test, then set " +
			"TEST_DATABASE_URL=postgres://postgres:postgres@127.0.0.1:55432/postgres")
	}
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	name := "calyx_test_" + hex.EncodeToString(raw[:])

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("cannot connect to TEST_DATABASE_URL: %v", err)
	}
	_, err = conn.Exec(ctx, "CREATE DATABASE "+name)
	_ = conn.Close(ctx)
	if err != nil {
		t.Fatalf("cannot create the test database: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := pgx.Connect(ctx, base)
		if err != nil {
			t.Errorf("cannot connect to drop %s: %v", name, err)
			return
		}
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("cannot drop %s: %v", name, err)
		}
	})

	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

// NewEmptyDB opens a pool on a new empty database. t.Cleanup closes the pool before the drop.
func NewEmptyDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := database.Open(context.Background(), NewDatabaseURL(t))
	if err != nil {
		t.Fatalf("cannot open the test database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// NewDB opens a pool on a new database with every migration applied.
func NewDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := NewEmptyDB(t)
	if err := database.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("cannot migrate the test database: %v", err)
	}
	return pool
}
