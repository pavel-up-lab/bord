// Package schema exposes operator-facing schema inspection and migration helpers.
package schema

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	storesqlite "bord/internal/storage/sqlite"
)

var ErrNewerSchema = errors.New("database schema is newer than this binary")

type Status struct {
	Current int    `json:"current"`
	Maximum int    `json:"maximum_supported"`
	State   string `json:"state"`
}

func Inspect(ctx context.Context, db *sql.DB) (Status, error) {
	status := Status{Maximum: storesqlite.MaxSchemaVersion, State: "migration_required"}
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&exists); err != nil {
		return Status{}, fmt.Errorf("inspect schema metadata: %w", err)
	}
	if exists == 0 {
		status.Current = 0
		return status, nil
	}
	if err := db.QueryRowContext(ctx, `SELECT version FROM schema_migrations LIMIT 1`).Scan(&status.Current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Status{}, errors.New("schema_migrations contains no version")
		}
		return Status{}, fmt.Errorf("read schema version: %w", err)
	}
	switch {
	case status.Current > status.Maximum:
		status.State = "incompatible_newer"
		return status, fmt.Errorf("%w: current=%d maximum_supported=%d", ErrNewerSchema, status.Current, status.Maximum)
	case status.Current == status.Maximum:
		status.State = "ready"
	default:
		status.State = "migration_required"
	}
	return status, nil
}

func Migrate(ctx context.Context, db *sql.DB) (Status, error) {
	if status, err := Inspect(ctx, db); err != nil {
		return status, err
	}
	if err := storesqlite.Migrate(ctx, db); err != nil {
		return Status{}, fmt.Errorf("apply migrations: %w", err)
	}
	return Inspect(ctx, db)
}
