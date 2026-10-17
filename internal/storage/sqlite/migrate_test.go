package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
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
