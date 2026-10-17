package media

import (
	"context"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
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
	s.mu.Lock()
	defer s.mu.Unlock()
	var report CleanupReport
	if opts.Grace < 0 || opts.Now.IsZero() {
		return report, errors.New("media: invalid cleanup options")
	}
	cutoff := opts.Now.Add(-opts.Grace)
	for _, area := range []string{"tmp", "blobs"} {
		err := fs.WalkDir(s.fs.FS(), area, func(name string, entry fs.DirEntry, walkErr error) error {
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
			entryName := entry.Name()
			if area == "tmp" {
				if !validTemporaryName(entryName) {
					return nil
				}
				report.TemporaryCandidates = append(report.TemporaryCandidates, filepath.Join(s.root, filepath.FromSlash(name)))
			} else {
				ext := filepath.Ext(entryName)
				key := strings.TrimSuffix(entryName, ext)
				decoded, decodeErr := hex.DecodeString(key)
				rel := strings.TrimPrefix(name, area+"/")
				expected := strings.Join([]string{key[:min(2, len(key))], key[min(2, len(key)):min(4, len(key))], entryName}, "/")
				if decodeErr != nil || len(decoded) != 32 || strings.ToLower(key) != key || rel == name || rel != expected || (ext != ".jpg" && ext != ".png") {
					return nil
				}
				if _, ok := opts.Referenced[key]; ok {
					return nil
				}
				report.OrphanCandidates = append(report.OrphanCandidates, filepath.Join(s.root, filepath.FromSlash(name)))
			}
			report.Bytes += info.Size()
			if !opts.DryRun {
				if err := s.fs.Remove(filepath.FromSlash(name)); errors.Is(err, os.ErrNotExist) {
					return nil
				} else if err != nil {
					return err
				}
				report.Removed = append(report.Removed, filepath.Join(s.root, filepath.FromSlash(name)))
			}
			return nil
		})
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return report, err
		}
	}
	return report, nil
}

func validTemporaryName(name string) bool {
	const prefix = ".incoming-"
	if !strings.HasPrefix(name, prefix) || len(name) <= len(prefix)+64 || name[len(prefix)+64] != '-' {
		return false
	}
	key := name[len(prefix) : len(prefix)+64]
	decoded, err := hex.DecodeString(key)
	if err != nil || len(decoded) != 32 || strings.ToLower(key) != key {
		return false
	}
	_, err = strconv.ParseInt(name[len(prefix)+65:], 10, 64)
	return err == nil
}
