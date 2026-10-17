package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	storesqlite "bord/internal/storage/sqlite"
)

func TestBackupVerifyRestore(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "source.db")
	db, err := storesqlite.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	if err := storesqlite.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO foundation_check(id, created_at) VALUES (1, 'test')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	blobs := filepath.Join(root, "source-blobs")
	if err := os.MkdirAll(filepath.Join(blobs, "ab"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blobs, "ab", "blob"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	backupRoot := filepath.Join(root, "backup")
	manifest, err := Create(context.Background(), Config{
		Database: database, Blobs: blobs, Destination: backupRoot, Quiescer: Offline{},
		Now: func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Blobs) != 1 {
		t.Fatalf("blob count = %d", len(manifest.Blobs))
	}
	if _, err := Verify(backupRoot); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(root, "restored")
	if err := Restore(backupRoot, restored); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(restored, "blobs", "ab", "blob"))
	if err != nil || string(got) != "fixture" {
		t.Fatalf("restored blob = %q, %v", got, err)
	}
	restoredDB, err := storesqlite.Open(filepath.Join(restored, "database.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer restoredDB.Close()
	var created string
	if err := restoredDB.QueryRow(`SELECT created_at FROM foundation_check WHERE id = 1`).Scan(&created); err != nil || created != "test" {
		t.Fatalf("restored database value = %q, %v", created, err)
	}
}

func TestVerifyRejectsTampering(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "source.db")
	db, _ := storesqlite.Open(database)
	if err := storesqlite.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	db.Close()
	blobs := filepath.Join(root, "blobs")
	os.MkdirAll(blobs, 0o700)
	backupRoot := filepath.Join(root, "backup")
	if _, err := Create(context.Background(), Config{Database: database, Blobs: blobs, Destination: backupRoot, Quiescer: Offline{}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupRoot, "database.sqlite3"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(backupRoot); err == nil {
		t.Fatal("expected checksum failure")
	}
}
