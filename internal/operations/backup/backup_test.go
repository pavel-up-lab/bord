package backup

import (
	"context"
	"encoding/json"
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

func TestCreateRejectsDestinationInsideBlobs(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "source.db")
	db, err := storesqlite.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	if err := storesqlite.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	db.Close()
	blobs := filepath.Join(root, "blobs")
	if err := os.MkdirAll(blobs, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = Create(context.Background(), Config{
		Database: database, Blobs: blobs, Destination: filepath.Join(blobs, "backup"), Quiescer: Offline{},
	})
	if err == nil {
		t.Fatal("expected overlapping backup paths to be rejected")
	}
}

func TestRestoreRejectsDestinationInsideBackup(t *testing.T) {
	backupRoot := createFixtureBackup(t)
	destination := filepath.Join(backupRoot, "unexpected", "restored")
	if err := Restore(backupRoot, destination); err == nil {
		t.Fatal("expected overlapping restore paths to be rejected")
	}
	if _, err := os.Stat(filepath.Dir(destination)); !os.IsNotExist(err) {
		t.Fatalf("restore mutated backup before rejecting destination: %v", err)
	}
}

func TestVerifyRejectsNonPortableManifestPath(t *testing.T) {
	backupRoot := createFixtureBackup(t)
	manifestPath := filepath.Join(backupRoot, ManifestName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Blobs = append(manifest.Blobs, File{Path: `blobs/..\database.sqlite3`})
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(backupRoot); err == nil {
		t.Fatal("expected non-portable path to be rejected")
	}
}

func TestVerifyBoundsManifestAndRejectsSymlinkEntries(t *testing.T) {
	backupRoot := createFixtureBackup(t)
	manifestPath := filepath.Join(backupRoot, ManifestName)
	if err := os.WriteFile(manifestPath, make([]byte, maxManifestBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(backupRoot); err == nil {
		t.Fatal("oversized manifest accepted")
	}

	backupRoot = createFixtureBackup(t)
	database := filepath.Join(backupRoot, "database.sqlite3")
	outside := filepath.Join(t.TempDir(), "outside.sqlite3")
	if err := os.Rename(database, outside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, database); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := Verify(backupRoot); err == nil {
		t.Fatal("symlinked backup entry accepted")
	}
}

func createFixtureBackup(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	database := filepath.Join(root, "source.db")
	db, err := storesqlite.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	if err := storesqlite.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	db.Close()
	blobs := filepath.Join(root, "blobs")
	if err := os.MkdirAll(blobs, 0o700); err != nil {
		t.Fatal(err)
	}
	backupRoot := filepath.Join(root, "backup")
	if _, err := Create(context.Background(), Config{
		Database: database, Blobs: blobs, Destination: backupRoot, Quiescer: Offline{},
	}); err != nil {
		t.Fatal(err)
	}
	return backupRoot
}
