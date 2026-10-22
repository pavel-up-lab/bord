package client

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func startOpener(ctx context.Context, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Env = openerEnvironment(os.Environ())
	return command.Start()
}

func openerEnvironment(source []string) []string {
	result := make([]string, 0, len(source))
	for _, entry := range source {
		key, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "BORD_ADMIN_CREDENTIAL") {
			continue
		}
		result = append(result, entry)
	}
	return result
}
