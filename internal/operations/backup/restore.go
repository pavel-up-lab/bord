package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func Verify(root string) (Manifest, error) {
	data, err := os.ReadFile(filepath.Join(root, ManifestName))
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest: %w", err)
	}
	if manifest.Format != ManifestVersion {
		return Manifest{}, fmt.Errorf("unsupported manifest format %d", manifest.Format)
	}
	if manifest.Database.Path != "database.sqlite3" {
		return Manifest{}, errors.New("manifest database path must be database.sqlite3")
	}
	for _, blob := range manifest.Blobs {
		if !strings.HasPrefix(blob.Path, "blobs/") {
			return Manifest{}, fmt.Errorf("blob path is outside blobs directory: %q", blob.Path)
		}
	}
	expected := map[string]File{}
	for _, file := range append([]File{manifest.Database}, manifest.Blobs...) {
		if !safePath(file.Path) {
			return Manifest{}, fmt.Errorf("unsafe manifest path %q", file.Path)
		}
		if _, duplicate := expected[file.Path]; duplicate {
			return Manifest{}, fmt.Errorf("duplicate manifest path %q", file.Path)
		}
		expected[file.Path] = file
		actual, err := describe(root, filepath.Join(root, filepath.FromSlash(file.Path)))
		if err != nil {
			return Manifest{}, fmt.Errorf("verify %s: %w", file.Path, err)
		}
		if actual.Size != file.Size || actual.SHA256 != file.SHA256 {
			return Manifest{}, fmt.Errorf("checksum mismatch for %s", file.Path)
		}
	}
	if err := verifySQLite(root, manifest); err != nil {
		return Manifest{}, err
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink is not allowed in backup: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel != ManifestName {
			if _, ok := expected[rel]; !ok {
				return fmt.Errorf("unmanifested file %q", rel)
			}
		}
		return nil
	})
	return manifest, err
}

func verifySQLite(root string, manifest Manifest) error {
	db, err := sql.Open("sqlite", filepath.Join(root, "database.sqlite3"))
	if err != nil {
		return err
	}
	defer db.Close()
	var integrity string
	if err := db.QueryRowContext(context.Background(), `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return fmt.Errorf("SQLite integrity check: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("SQLite integrity check failed: %s", integrity)
	}
	var version int
	if err := db.QueryRowContext(context.Background(), `SELECT version FROM schema_migrations LIMIT 1`).Scan(&version); err != nil {
		return fmt.Errorf("read restored schema version: %w", err)
	}
	if version != manifest.SchemaVersion {
		return fmt.Errorf("schema version mismatch: manifest=%d database=%d", manifest.SchemaVersion, version)
	}
	return nil
}

func Restore(backupRoot, destination string) error {
	manifest, err := Verify(backupRoot)
	if err != nil {
		return err
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		return errors.New("restore destination must not exist")
	}
	if overlap, err := lexicalPathsOverlap(backupRoot, destination); err != nil {
		return fmt.Errorf("validate restore paths: %w", err)
	} else if overlap {
		return errors.New("restore destination must be outside the backup directory")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	if overlap, err := pathsOverlap(backupRoot, destination); err != nil {
		return fmt.Errorf("validate restore paths: %w", err)
	} else if overlap {
		return errors.New("restore destination must be outside the backup directory")
	}
	stage, err := os.MkdirTemp(filepath.Dir(destination), ".bord-restore-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	for _, file := range append([]File{manifest.Database}, manifest.Blobs...) {
		if err := copyFile(filepath.Join(backupRoot, filepath.FromSlash(file.Path)), filepath.Join(stage, filepath.FromSlash(file.Path))); err != nil {
			return err
		}
	}
	if err := os.Rename(stage, destination); err != nil {
		return fmt.Errorf("publish restore: %w", err)
	}
	return nil
}

func safePath(path string) bool {
	if strings.ContainsRune(path, '\\') {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if path == "" || filepath.IsAbs(filepath.FromSlash(path)) || clean != path {
		return false
	}
	return path != "." && path != ".." && !strings.HasPrefix(path, "../")
}
