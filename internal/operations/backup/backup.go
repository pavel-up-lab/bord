// Package backup creates and verifies coordinated SQLite and blob snapshots.
package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	ManifestName    = "manifest.json"
	ManifestVersion = 1
)

type Quiescer interface {
	WithQuiesce(context.Context, func() error) error
}

// Offline asserts that the caller has stopped the service before snapshotting.
type Offline struct{}

func (Offline) WithQuiesce(_ context.Context, fn func() error) error { return fn() }

type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type Manifest struct {
	Format        int       `json:"format"`
	CreatedAt     time.Time `json:"created_at"`
	SchemaVersion int       `json:"schema_version"`
	Database      File      `json:"database"`
	Blobs         []File    `json:"blobs"`
}

type Config struct {
	Database    string
	Blobs       string
	Destination string
	Quiescer    Quiescer
	Now         func() time.Time
}

func Create(ctx context.Context, cfg Config) (Manifest, error) {
	if cfg.Database == "" || cfg.Blobs == "" || cfg.Destination == "" || cfg.Quiescer == nil {
		return Manifest{}, errors.New("database, blobs, destination, and quiescer are required")
	}
	if _, err := os.Stat(cfg.Database); err != nil {
		return Manifest{}, fmt.Errorf("inspect database: %w", err)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if overlap, err := lexicalPathsOverlap(cfg.Blobs, cfg.Destination); err != nil {
		return Manifest{}, fmt.Errorf("validate backup paths: %w", err)
	} else if overlap {
		return Manifest{}, errors.New("backup destination must be outside the blob directory")
	}
	parent := filepath.Dir(cfg.Destination)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return Manifest{}, err
	}
	if overlap, err := pathsOverlap(cfg.Blobs, cfg.Destination); err != nil {
		return Manifest{}, fmt.Errorf("validate backup paths: %w", err)
	} else if overlap {
		return Manifest{}, errors.New("backup destination must be outside the blob directory")
	}
	stage, err := os.MkdirTemp(parent, ".bord-backup-")
	if err != nil {
		return Manifest{}, err
	}
	defer os.RemoveAll(stage)

	var manifest Manifest
	err = cfg.Quiescer.WithQuiesce(ctx, func() error {
		manifest = Manifest{Format: ManifestVersion, CreatedAt: cfg.Now().UTC()}
		databaseTarget := filepath.Join(stage, "database.sqlite3")
		if err := vacuumInto(ctx, cfg.Database, databaseTarget); err != nil {
			return err
		}
		manifest.SchemaVersion, err = readSchemaVersion(ctx, databaseTarget)
		if err != nil {
			return err
		}
		manifest.Database, err = describe(stage, databaseTarget)
		if err != nil {
			return err
		}
		manifest.Blobs, err = copyBlobs(cfg.Blobs, filepath.Join(stage, "blobs"), stage)
		return err
	})
	if err != nil {
		return Manifest{}, err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Manifest{}, err
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(stage, ManifestName), data, 0o600); err != nil {
		return Manifest{}, err
	}
	if _, err := Verify(stage); err != nil {
		return Manifest{}, fmt.Errorf("verify staged backup: %w", err)
	}
	if _, err := os.Stat(cfg.Destination); !errors.Is(err, os.ErrNotExist) {
		return Manifest{}, errors.New("backup destination already exists")
	}
	if err := os.Rename(stage, cfg.Destination); err != nil {
		return Manifest{}, fmt.Errorf("publish backup: %w", err)
	}
	return manifest, nil
}

func readSchemaVersion(ctx context.Context, database string) (int, error) {
	db, err := sql.Open("sqlite", database)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var version int
	if err := db.QueryRowContext(ctx, `SELECT version FROM schema_migrations LIMIT 1`).Scan(&version); err != nil {
		return 0, fmt.Errorf("read snapshot schema version: %w", err)
	}
	return version, nil
}

func vacuumInto(ctx context.Context, source, target string) error {
	db, err := sql.Open("sqlite", source)
	if err != nil {
		return err
	}
	defer db.Close()
	quoted := strings.ReplaceAll(target, "'", "''")
	if _, err := db.ExecContext(ctx, "VACUUM INTO '"+quoted+"'"); err != nil {
		return fmt.Errorf("snapshot SQLite: %w", err)
	}
	return nil
}

func copyBlobs(source, target, root string) ([]File, error) {
	info, err := os.Stat(source)
	if err != nil {
		return nil, fmt.Errorf("inspect blobs: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("blob path is not a directory")
	}
	var files []File
	err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("blob symlink is not allowed: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("non-regular blob is not allowed: %s", path)
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if strings.ContainsRune(rel, '\\') {
			return fmt.Errorf("blob path is not portable: %q", rel)
		}
		destination := filepath.Join(target, rel)
		if err := copyFile(path, destination); err != nil {
			return err
		}
		file, err := describe(root, destination)
		if err != nil {
			return err
		}
		files = append(files, file)
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, err
}

// pathsOverlap resolves existing parents so a symlink cannot hide that the
// destination is nested below the source (or vice versa).
func pathsOverlap(first, second string) (bool, error) {
	canonical := func(path string) (string, error) {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
		if err != nil {
			return "", err
		}
		return filepath.Join(parent, filepath.Base(absolute)), nil
	}
	a, err := canonical(first)
	if err != nil {
		return false, err
	}
	b, err := canonical(second)
	if err != nil {
		return false, err
	}
	return cleanPathsOverlap(a, b)
}

func lexicalPathsOverlap(first, second string) (bool, error) {
	a, err := filepath.Abs(first)
	if err != nil {
		return false, err
	}
	b, err := filepath.Abs(second)
	if err != nil {
		return false, err
	}
	return cleanPathsOverlap(a, b)
}

func cleanPathsOverlap(first, second string) (bool, error) {
	contains := func(parent, child string) (bool, error) {
		rel, err := filepath.Rel(parent, child)
		if err != nil {
			return false, err
		}
		return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))), nil
	}
	ab, err := contains(first, second)
	if err != nil {
		return false, err
	}
	ba, err := contains(second, first)
	return ab || ba, err
}

func describe(root, path string) (File, error) {
	f, err := os.Open(path)
	if err != nil {
		return File{}, err
	}
	defer f.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, f)
	if err != nil {
		return File{}, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return File{}, err
	}
	return File{Path: filepath.ToSlash(rel), SHA256: hex.EncodeToString(hash.Sum(nil)), Size: size}, nil
}

func copyFile(source, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}
