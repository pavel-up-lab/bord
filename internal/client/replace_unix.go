//go:build !windows

package client

import (
	"os"
	"path/filepath"
)

func replaceFile(source, target string) error { return os.Rename(source, target) }
func secureFile(path string) error            { return os.Chmod(path, 0o600) }
func secureDir(path string) error             { return os.Chmod(path, 0o700) }
func syncParent(path string) error {
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
