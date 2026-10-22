package terminalui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// SanitizeTerminalText neutralizes terminal control characters in untrusted
// single-line text, including ESC, CSI, OSC terminators and C0/C1 ranges.
func SanitizeTerminalText(value string) string { return sanitize(value, false) }

func SanitizeMultilineTerminalText(value string) string { return sanitize(value, true) }

func sanitize(value string, allowNewline bool) string {
	unsafe := func(r rune) bool { return unsafeTerminalRune(r) && (!allowNewline || r != '\n') }
	if !strings.ContainsFunc(value, unsafe) {
		return value
	}
	return strings.Map(func(r rune) rune {
		if unsafe(r) {
			return '\uFFFD'
		}
		return r
	}, value)
}

func unsafeTerminalRune(r rune) bool { return r < 0x20 || (r >= 0x7f && r <= 0x9f) }

func TextWidth(value string) int { return lipgloss.Width(value) }
