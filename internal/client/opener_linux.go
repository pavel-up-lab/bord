//go:build linux

package client

import (
	"context"
	"os/exec"
)

func openerCommand(path string) (string, []string) { return "xdg-open", []string{path} }
func openPlatform(ctx context.Context, path string) error {
	name, args := openerCommand(path)
	return exec.CommandContext(ctx, name, args...).Start()
}
