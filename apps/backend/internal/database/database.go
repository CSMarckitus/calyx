// Package database opens the PostgreSQL pool, runs the migrations and holds the advisory lock.
package database

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LockKey is the key of the advisory lock that one backend process holds.
const LockKey = 727001

// ErrLocked means that another backend process holds the advisory lock.
var ErrLocked = errors.New("another backend process is running")

const defaultMaxConns = 10

// Open creates the pool and pings the server. The pool has at most 10 connections, unless the URL
// sets pool_max_conns. A pool of less than 2 connections is refused. Lock holds one connection for the life of the process.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	// pgxpool removes its own parameters from the parsed config. The plain pgx parser keeps
	// pool_max_conns in RuntimeParams, so it tells whether the URL sets the size of the pool.
	plain, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	if _, set := plain.RuntimeParams["pool_max_conns"]; !set {
		cfg.MaxConns = defaultMaxConns
	}
	if cfg.MaxConns < 2 {
		return nil, fmt.Errorf("pool_max_conns is %d, it must be at least 2: the advisory lock holds one connection", cfg.MaxConns)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// Lock takes the advisory lock on one connection of the pool and holds that connection until
// release runs. It returns ErrLocked when another session holds the lock.
func Lock(ctx context.Context, pool *pgxpool.Pool) (release func(), err error) {
	release, _, err = LockWithCheck(ctx, pool)
	return release, err
}

// LockWithCheck is Lock with a second function. check runs a query on the held connection and
// returns an error when that connection is dead. A PostgreSQL restart ends the session and drops
// the lock without a sign, so the caller must call check from time to time.
func LockWithCheck(ctx context.Context, pool *pgxpool.Pool) (release func(), check func(context.Context) error, err error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, int64(LockKey)).Scan(&got); err != nil {
		conn.Release()
		return nil, nil, err
	}
	if !got {
		conn.Release()
		return nil, nil, ErrLocked
	}
	var once sync.Once
	var mu sync.Mutex // a connection runs one query at a time
	release = func() {
		once.Do(func() {
			mu.Lock()
			defer mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var unlocked bool
			err := conn.QueryRow(ctx, `SELECT pg_advisory_unlock($1)`, int64(LockKey)).Scan(&unlocked)
			if err != nil || !unlocked {
				// The lock may still be held. Close the connection: PostgreSQL drops a session
				// lock with its session, and pgxpool destroys a closed connection.
				_ = conn.Conn().Close(ctx)
			}
			conn.Release()
		})
	}
	check = func(ctx context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		_, err := conn.Exec(ctx, `SELECT 1`)
		return err
	}
	return release, check, nil
}

// UniqueViolation reports a unique violation (SQLSTATE 23505) and the name of its constraint.
// For a unique index that is not a constraint, PostgreSQL gives the name of the index.
func UniqueViolation(err error) (constraint string, ok bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return pgErr.ConstraintName, true
	}
	return "", false
}
