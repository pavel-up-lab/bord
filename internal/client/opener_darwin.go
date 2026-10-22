//go:build darwin

package client

import (
	"context"
)

func openerCommand(path string) (string, []string) { return "open", []string{path} }
func openPlatform(ctx context.Context, path string) error {
	name, args := openerCommand(path)
	return startOpener(ctx, name, args...)
}
