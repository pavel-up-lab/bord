package terminalui

import (
	"strings"
	"testing"
)

func TestSanitizeTerminalTextAndMultiline(t *testing.T) {
	if got := SanitizeTerminalText("safe\x1b[31m\nline"); strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\n') {
		t.Fatalf("single-line controls survived: %q", got)
	}
	if got := SanitizeMultilineTerminalText("first\nsecond\x9b"); !strings.ContainsRune(got, '\n') || strings.ContainsRune(got, '\x9b') {
		t.Fatalf("multiline sanitation=%q", got)
	}
}

func TestChromeAlwaysMatchesRequestedWidth(t *testing.T) {
	theme := Palette(ThemeClassic)
	for _, width := range []int{20, 48, 120} {
		if got := TopBar(theme, " BORD ", " Reports ", width); TextWidth(got) != width {
			t.Fatalf("top width=%d got=%d", width, TextWidth(got))
		}
		if got := CommandBar(theme, []string{"F1 Help", "F5 Refresh", "F10 Quit"}, width); TextWidth(got) != width {
			t.Fatalf("command width=%d got=%d", width, TextWidth(got))
		}
	}
}
