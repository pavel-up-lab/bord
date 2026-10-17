package command

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	storesqlite "bord/internal/storage/sqlite"
)

func TestMigrateAndStatusCommands(t *testing.T) {
	database := filepath.Join(t.TempDir(), "bord.db")
	for _, args := range [][]string{{"migrate", "--database", database}, {"status", "--database", database}} {
		var stdout, stderr bytes.Buffer
		if code := Run(context.Background(), args, &stdout, &stderr); code != ExitOK {
			t.Fatalf("%v exit=%d stderr=%s", args, code, stderr.String())
		}
		if !strings.Contains(stdout.String(), `"state":"ready"`) {
			t.Fatalf("%v output=%s", args, stdout.String())
		}
	}
}

func TestStatusUsesDistinctExitForNewerSchema(t *testing.T) {
	database := filepath.Join(t.TempDir(), "bord.db")
	db, err := storesqlite.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER NOT NULL); INSERT INTO schema_migrations VALUES (?)`, storesqlite.MaxSchemaVersion+1); err != nil {
		t.Fatal(err)
	}
	db.Close()
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"status", "--database", database}, &stdout, &stderr); code != ExitIncompatible {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
}

func TestBackupAndRestoreVerificationCommands(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "bord.db")
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
	if err := os.WriteFile(filepath.Join(blobs, "fixture"), []byte("blob"), 0o600); err != nil {
		t.Fatal(err)
	}
	backupRoot := filepath.Join(root, "backup")
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"backup", "--offline", "--database", database, "--blobs", blobs, "--output", backupRoot}, &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("backup exit=%d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"restore-verify", "--backup", backupRoot}, &stdout, &stderr)
	if code != ExitOK || !strings.Contains(stdout.String(), "verification passed") {
		t.Fatalf("verify exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}
