package schema

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	storesqlite "bord/internal/storage/sqlite"
)

func TestMigrateAndStatus(t *testing.T) {
	db, err := storesqlite.Open(filepath.Join(t.TempDir(), "bord.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	before, err := Inspect(context.Background(), db)
	if err != nil || before.Current != 0 || before.State != "migration_required" {
		t.Fatalf("before = %#v, %v", before, err)
	}
	after, err := Migrate(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if after.Current != storesqlite.MaxSchemaVersion || after.State != "ready" {
		t.Fatalf("after = %#v", after)
	}
}

func TestInspectRejectsNewerSchema(t *testing.T) {
	db, err := storesqlite.Open(filepath.Join(t.TempDir(), "bord.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER NOT NULL); INSERT INTO schema_migrations VALUES (?)`, storesqlite.MaxSchemaVersion+1); err != nil {
		t.Fatal(err)
	}
	status, err := Inspect(context.Background(), db)
	if !errors.Is(err, ErrNewerSchema) || status.State != "incompatible_newer" {
		t.Fatalf("status = %#v, err = %v", status, err)
	}
}
