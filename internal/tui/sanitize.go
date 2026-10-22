package tui

import "bord/internal/terminalui"

// SanitizeTerminalText neutralizes all terminal control characters in untrusted
// single-line text, including ESC, CSI, OSC terminators and the C0/C1 ranges.
func SanitizeTerminalText(value string) string {
	return terminalui.SanitizeTerminalText(value)
}

func sanitizeMultilineTerminalText(value string) string {
	return terminalui.SanitizeMultilineTerminalText(value)
}
