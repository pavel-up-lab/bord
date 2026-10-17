//go:build windows

package client

import "testing"

func TestTerminalSmokeOpenerCommand(t *testing.T) {
	path := `C:\Users\bord user\image&not-a-command.png`
	name, args := openerCommand(path)
	if name != "rundll32.exe" || len(args) != 2 || args[0] != "url.dll,FileProtocolHandler" || args[1] != path {
		t.Fatalf("openerCommand() = %q, %#v", name, args)
	}
}
