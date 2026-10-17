package media

import (
	"context"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type CleanupOptions struct {
	Now        time.Time
	Grace      time.Duration
	Referenced map[string]struct{}
	DryRun     bool
}

type CleanupReport struct {
	TemporaryCandidates []string
	OrphanCandidates    []string
	Removed             []string
	Bytes               int64
}

// Cleanup removes only regular, recognized files older than the grace period.
// Referenced content hashes are always retained. Symlinks and unknown files are
// ignored so reconciliation cannot escape or destructively tidy operator data.
func (s *Store) Cleanup(ctx context.Context, opts CleanupOptions) (CleanupReport, error) {
	var report CleanupReport
	if opts.Grace < 0 || opts.Now.IsZero() {
		return report, errors.New("media: invalid cleanup options")
	}
	cutoff := opts.Now.Add(-opts.Grace)
	for _, area := range []string{"tmp", "blobs"} {
		base := filepath.Join(s.root, area)
		err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() || info.ModTime().After(cutoff) {
				return err
			}
			name := entry.Name()
			if area == "tmp" {
				if !strings.HasPrefix(name, ".incoming-") {
					return nil
				}
				report.TemporaryCandidates = append(report.TemporaryCandidates, path)
			} else {
				key := strings.TrimSuffix(strings.TrimSuffix(name, ".jpg"), ".png")
				decoded, decodeErr := hex.DecodeString(key)
				rel, relErr := filepath.Rel(base, path)
				expected := filepath.Join(key[:min(2, len(key))], key[min(2, len(key)):min(4, len(key))], name)
				if decodeErr != nil || len(decoded) != 32 || strings.ToLower(key) != key || relErr != nil || rel != expected || (filepath.Ext(name) != ".jpg" && filepath.Ext(name) != ".png") {
					return nil
				}
				if _, ok := opts.Referenced[key]; ok {
					return nil
				}
				report.OrphanCandidates = append(report.OrphanCandidates, path)
			}
			report.Bytes += info.Size()
			if !opts.DryRun {
				if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
				report.Removed = append(report.Removed, path)
			}
			return nil
		})
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return report, err
		}
	}
	return report, nil
}
