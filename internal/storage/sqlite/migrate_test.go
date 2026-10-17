package sqlite

import (
	"context"
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
