//go:build darwin

package client

import "testing"

func TestTerminalSmokeOpenerCommand(t *testing.T) {
	path := "/tmp/bord image;not-a-command.png"
	name, args := openerCommand(path)
	if name != "open" || len(args) != 1 || args[0] != path {
		t.Fatalf("openerCommand() = %q, %#v", name, args)
	}
}
