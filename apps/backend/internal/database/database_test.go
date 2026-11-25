package database_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"calyx/backend/internal/database"
	"calyx/backend/internal/testutil"
)

func TestOpenPings(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if n := pool.Config().MaxConns; n != 10 {
		t.Fatalf("MaxConns = %d, want 10", n)
	}
}

func TestOpenKeepsPoolMaxConnsFromTheURL(t *testing.T) {
	pool, err := database.Open(context.Background(), testutil.NewDatabaseURL(t)+"?pool_max_conns=3")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if n := pool.Config().MaxConns; n != 3 {
		t.Fatalf("MaxConns = %d, want 3", n)
	}
}

func TestOpenRefusesAPoolOfLessThanTwoConnections(t *testing.T) {
	// pgx itself refuses 0 or less. The backend refuses 1.
	for _, q := range []string{"?pool_max_conns=1", "?pool_max_conns=0"} {
		pool, err := database.Open(context.Background(), testutil.NewDatabaseURL(t)+q)
		if err == nil {
			pool.Close()
			t.Fatalf("%s: Open accepted the pool", q)
		}
		if q == "?pool_max_conns=1" && !strings.Contains(err.Error(), "at least 2") {
			t.Fatalf("%s: unclear error: %v", q, err)
		}
	}
}

func TestOpenFailsWhenTheServerIsNotThere(t *testing.T) {
	start := time.Now()
	_, err := database.Open(context.Background(), "postgres://postgres:postgres@127.0.0.1:1/x?connect_timeout=2")
	if err == nil {
		t.Fatal("want an error")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestASecondLockFailsWhileTheFirstIsHeld(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewEmptyDB(t)
	release1, err := database.Lock(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	defer release1()
	_, err2 := database.Lock(ctx, pool)
	if !errors.Is(err2, database.ErrLocked) {
		t.Fatalf("err = %v, want ErrLocked", err2)
	}
	if err2.Error() != "another backend process is running" {
		t.Fatalf("text = %q", err2.Error())
	}
}

func TestReleaseLetsANewLockSucceed(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewEmptyDB(t)
	release1, err := database.Lock(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	release1()
	release1() // safe to call twice
	release2, err := database.Lock(ctx, pool)
	if err != nil {
		t.Fatalf("Lock after release: %v", err)
	}
	release2()
}

func TestLockAcrossTwoPools(t *testing.T) {
	ctx := context.Background()
	url := testutil.NewDatabaseURL(t)
	a, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	release, err := database.Lock(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := database.Lock(ctx, b); !errors.Is(err, database.ErrLocked) {
		t.Fatalf("err = %v, want ErrLocked", err)
	}
}

func TestUniqueViolation(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewEmptyDB(t)
	if _, err := pool.Exec(ctx, `CREATE TABLE t (x int CONSTRAINT t_x_key UNIQUE, y int NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO t VALUES (1, 1)`); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(ctx, `INSERT INTO t VALUES (1, 2)`)
	if c, ok := database.UniqueViolation(err); !ok || c != "t_x_key" {
		t.Fatalf("UniqueViolation = %q, %v", c, ok)
	}
	if c, ok := database.UniqueViolation(errors.New("x")); ok || c != "" {
		t.Fatalf("plain error: %q, %v", c, ok)
	}
	if c, ok := database.UniqueViolation(nil); ok || c != "" {
		t.Fatalf("nil: %q, %v", c, ok)
	}
	_, err = pool.Exec(ctx, `INSERT INTO t VALUES (2, NULL)`)
	if err == nil {
		t.Fatal("want a NOT NULL violation")
	}
	if c, ok := database.UniqueViolation(err); ok || c != "" {
		t.Fatalf("not null: %q, %v", c, ok)
	}
}

func TestLockCheckFailsAfterTheSessionEnds(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewEmptyDB(t)
	release, check, err := database.LockWithCheck(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := check(ctx); err != nil {
		t.Fatalf("check on a live lock: %v", err)
	}
	// Another connection of the pool ends the session that holds the lock, like a restart does.
	_, err = pool.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_locks
		WHERE locktype = 'advisory' AND objid = $1 AND granted
		AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`, int64(database.LockKey))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for check(ctx) == nil {
		if time.Now().After(deadline) {
			t.Fatal("check still passes after the session ended")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
