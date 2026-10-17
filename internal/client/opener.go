package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// OpenExternal starts the platform file association directly, never through a shell.
func OpenExternal(ctx context.Context, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return errors.New("resolve attachment path")
	}
	info, err := os.Stat(abs)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("attachment is not a regular file")
	}
	return openPlatform(ctx, abs)
}
