package admintui

import (
	"testing"

	"bord/internal/terminalui"
)

func TestAdminCommandHitZonesMatchSharedChrome(t *testing.T) {
	commands := []command{{"F4 Hide", "hide"}, {"F7 Ban", "ban"}, {"F10 Quit", "quit"}}
	labels := []string{commands[0].label, commands[1].label, commands[2].label}
	for _, width := range []int{22, 80} {
		for x := 0; x < width; x++ {
			index, expectedHit := terminalui.CommandIndexAt(labels, width, x)
			got, hit := commandAt(commands, width, x)
			if hit != expectedHit || hit && got != commands[index].action {
				t.Fatalf("width=%d x=%d action=%q hit=%v expected=%q/%v", width, x, got, hit, commands[index].action, expectedHit)
			}
		}
	}
}
