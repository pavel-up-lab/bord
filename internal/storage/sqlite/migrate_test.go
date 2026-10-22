package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"bord/migrations"
)

func TestMigrateTemporaryDatabase(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := db.QueryRow(`SELECT version FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != MaxSchemaVersion {
		t.Fatalf("version %d", version)
	}
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("migration is not idempotent: %v", err)
	}
}

func TestBoardLifecycleSchemaCompatibleFromEverySupportedVersion(t *testing.T) {
	ctx := context.Background()
	for version := 0; version <= MaxSchemaVersion; version++ {
		t.Run(fmt.Sprintf("version-%d", version), func(t *testing.T) {
			db, err := Open(filepath.Join(t.TempDir(), "compat.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err = db.Exec(`CREATE TABLE schema_migrations (version INTEGER NOT NULL); INSERT INTO schema_migrations VALUES (0)`); err != nil {
				t.Fatal(err)
			}
			for current := 1; current <= version; current++ {
				name := map[int]string{1: "000001_foundation.up.sql", 2: "000002_board_storage.up.sql", 3: "000003_integration_adapters.up.sql"}[current]
				script, readErr := migrations.Files.ReadFile(name)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if _, err = db.Exec(string(script)); err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec(`UPDATE schema_migrations SET version=?`, current); err != nil {
					t.Fatal(err)
				}
			}
			if version >= 2 {
				if _, err = db.Exec(`INSERT INTO boards(id,slug,title,enabled,created_at) VALUES(1,'archive','Archive',0,'2026-01-01T00:00:00Z')`); err != nil {
					t.Fatal(err)
				}
			}
			if err = Migrate(ctx, db); err != nil {
				t.Fatal(err)
			}
			if err = Ready(ctx, db); err != nil {
				t.Fatal(err)
			}
			if version < 2 {
				if _, err = db.Exec(`INSERT INTO boards(id,slug,title,enabled,created_at) VALUES(1,'archive','Archive',0,'2026-01-01T00:00:00Z')`); err != nil {
					t.Fatal(err)
				}
			}
			var enabled int
			if err = db.QueryRow(`SELECT enabled FROM boards WHERE slug='archive'`).Scan(&enabled); err != nil || enabled != 0 {
				t.Fatalf("archived lifecycle state enabled=%d err=%v", enabled, err)
			}
		})
	}
}

func TestRejectsNewerSchema(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "newer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER NOT NULL); INSERT INTO schema_migrations VALUES (?)`, MaxSchemaVersion+1); err != nil {
		t.Fatal(err)
	}
	err = Migrate(context.Background(), db)
	if err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReadyRejectsUnsafeRuntimeSettings(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*sql.DB) error
		want      string
	}{
		{
			name: "busy timeout",
			configure: func(db *sql.DB) error {
				_, err := db.Exec(`PRAGMA busy_timeout = 0`)
				return err
			},
			want: "busy timeout",
		},
		{
			name: "synchronous mode",
			configure: func(db *sql.DB) error {
				_, err := db.Exec(`PRAGMA synchronous = FULL`)
				return err
			},
			want: "synchronous mode",
		},
		{
			name: "connection pool",
			configure: func(db *sql.DB) error {
				db.SetMaxOpenConns(defaultMaxOpenConns + 1)
				return nil
			},
			want: "maximum open connections",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, err := Open(filepath.Join(t.TempDir(), "unsafe.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := Migrate(context.Background(), db); err != nil {
				t.Fatal(err)
			}
			if err := tt.configure(db); err != nil {
				t.Fatal(err)
			}
			err = Ready(context.Background(), db)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Ready() error = %v, want %q", err, tt.want)
			}
		})
	}
}
