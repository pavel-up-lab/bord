// Package sqlite provides the pure-Go SQLite adapter foundation.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"bord/migrations"
	_ "modernc.org/sqlite"
)

const MaxSchemaVersion = 2

const (
	defaultBusyTimeout = 5_000
	// A single database/sql connection serializes SQLite writes predictably. WAL
	// still lets external readers proceed, and PostgreSQL is the scale-out path.
	defaultMaxOpenConns = 1
)

// Open configures every connection for foreign keys, bounded lock waits and WAL.
func Open(dsn string) (*sql.DB, error) {
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	configured := dsn + separator + fmt.Sprintf("_pragma=foreign_keys(1)&_pragma=busy_timeout(%d)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", defaultBusyTimeout)
	db, err := sql.Open("sqlite", configured)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(defaultMaxOpenConns)
	db.SetMaxIdleConns(defaultMaxOpenConns)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// Ready verifies connectivity, required runtime pragmas and exact schema compatibility.
func Ready(ctx context.Context, db *sql.DB) error {
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("storage ping: %w", err)
	}
	var foreignKeys int
	if err := db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		return fmt.Errorf("foreign keys disabled: value=%d err=%v", foreignKeys, err)
	}
	var mode string
	if err := db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil || !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("journal mode is %q: %v", mode, err)
	}
	var version int
	if err := db.QueryRowContext(ctx, `SELECT version FROM schema_migrations LIMIT 1`).Scan(&version); err != nil {
		return fmt.Errorf("schema version: %w", err)
	}
	if version != MaxSchemaVersion {
		return fmt.Errorf("schema version %d does not match supported version %d", version, MaxSchemaVersion)
	}
	return nil
}

// Migrate upgrades an empty or older database and rejects schemas newer than this binary.
func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var version int
	err := db.QueryRowContext(ctx, `SELECT version FROM schema_migrations LIMIT 1`).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err = db.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES (0)`); err != nil {
			return err
		}
		version = 0
	} else if err != nil {
		return err
	}
	if version > MaxSchemaVersion {
		return fmt.Errorf("schema version %d is newer than supported version %d", version, MaxSchemaVersion)
	}
	entries, err := fs.Glob(migrations.Files, "*.up.sql")
	if err != nil {
		return err
	}
	sort.Strings(entries)
	for _, name := range entries {
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			return fmt.Errorf("invalid migration filename %q", name)
		}
		n, err := strconv.Atoi(prefix)
		if err != nil {
			return fmt.Errorf("invalid migration filename %q", name)
		}
		if n <= version {
			continue
		}
		if n != version+1 {
			return fmt.Errorf("migration gap after version %d", version)
		}
		script, err := migrations.Files.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, string(script)); err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE schema_migrations SET version = ?`, n)
		}
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d: %w", n, err)
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		version = n
	}
	return nil
}
