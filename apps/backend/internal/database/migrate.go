package database

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

var migrationName = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)

type migrationFile struct {
	version int
	name    string
}

// Migrate applies the embedded migration files that the database does not have yet.
// The caller holds the advisory lock (see Lock).
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	return MigrateFS(ctx, pool, sub)
}

// MigrateFS applies the *.sql files at the root of fsys. Each file runs in one transaction with
// the insert of its version in schema_migrations, so a file that fails leaves no change.
func MigrateFS(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) error {
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version int PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	files, err := listMigrations(fsys)
	if err != nil {
		return err
	}
	known := make(map[int]bool, len(files))
	for _, f := range files {
		known[f.version] = true
	}

	applied, err := appliedVersions(ctx, pool)
	if err != nil {
		return err
	}
	for v := range applied {
		if !known[v] {
			return fmt.Errorf("the database has migration version %d, which this binary does not know: use a newer binary", v)
		}
	}

	for _, f := range files {
		if applied[f.version] {
			continue
		}
		sql, err := fs.ReadFile(fsys, f.name)
		if err != nil {
			return fmt.Errorf("migration %s: %w", f.name, err)
		}
		err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			// An Exec without arguments uses the simple protocol, so one call runs many statements.
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, f.version)
			return err
		})
		if err != nil {
			return fmt.Errorf("migration %s: %w", f.name, err)
		}
	}
	return nil
}

func listMigrations(fsys fs.FS) ([]migrationFile, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	var files []migrationFile
	byVersion := map[int]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migration file %s: the name must look like 0001_name.sql", e.Name())
		}
		v, _ := strconv.Atoi(m[1])
		if other, ok := byVersion[v]; ok {
			return nil, fmt.Errorf("migration files %s and %s have the same version", other, e.Name())
		}
		byVersion[v] = e.Name()
		files = append(files, migrationFile{version: v, name: e.Name()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].version < files[j].version })
	return files, nil
}

func appliedVersions(ctx context.Context, pool *pgxpool.Pool) (map[int]bool, error) {
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()
	applied := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}
