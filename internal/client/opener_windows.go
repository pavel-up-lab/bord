//go:build windows

package client

import (
	"context"
)

// rundll32 is invoked directly; path is one argv item and is never parsed by cmd.exe.
func openerCommand(path string) (string, []string) {
	return "rundll32.exe", []string{"url.dll,FileProtocolHandler", path}
}
func openPlatform(ctx context.Context, path string) error {
	name, args := openerCommand(path)
	return startOpener(ctx, name, args...)
}
