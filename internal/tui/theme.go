package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ThemeName is the stable name stored in client state and accepted by --theme.
type ThemeName string

const (
	ThemeClassic ThemeName = "classic"
	ThemeAmber   ThemeName = "amber"
	ThemeGreen   ThemeName = "green"
	ThemeMono    ThemeName = "mono"
)

var themeOrder = []ThemeName{ThemeClassic, ThemeAmber, ThemeGreen, ThemeMono}

type theme struct {
	name                           ThemeName
	background, foreground         lipgloss.Color
	barBackground, barForeground   lipgloss.Color
	accent, muted                  lipgloss.Color
	selectionBackground, selection lipgloss.Color
}

func ParseTheme(value string) (ThemeName, error) {
	name := ThemeName(strings.ToLower(strings.TrimSpace(value)))
	for _, candidate := range themeOrder {
		if name == candidate {
			return name, nil
		}
	}
	return "", fmt.Errorf("unknown theme %q (use classic, amber, green, or mono)", SanitizeTerminalText(value))
}

func palette(name ThemeName) theme {
	switch name {
	case ThemeAmber:
		return theme{name, lipgloss.Color("#1B1000"), lipgloss.Color("#FFD27A"), lipgloss.Color("#8A4F00"), lipgloss.Color("#FFF1C7"), lipgloss.Color("#FFB000"), lipgloss.Color("#B9873E"), lipgloss.Color("#FFB000"), lipgloss.Color("#1B1000")}
	case ThemeGreen:
		return theme{name, lipgloss.Color("#001A08"), lipgloss.Color("#8CFF9B"), lipgloss.Color("#075D25"), lipgloss.Color("#D2FFD8"), lipgloss.Color("#39FF6A"), lipgloss.Color("#4FAE67"), lipgloss.Color("#39FF6A"), lipgloss.Color("#001A08")}
	case ThemeMono:
		return theme{name, lipgloss.Color("#101010"), lipgloss.Color("#E8E8E8"), lipgloss.Color("#D8D8D8"), lipgloss.Color("#101010"), lipgloss.Color("#FFFFFF"), lipgloss.Color("#A0A0A0"), lipgloss.Color("#E8E8E8"), lipgloss.Color("#101010")}
	default:
		return theme{ThemeClassic, lipgloss.Color("#0000A8"), lipgloss.Color("#E5E5E5"), lipgloss.Color("#00A8A8"), lipgloss.Color("#000000"), lipgloss.Color("#55FFFF"), lipgloss.Color("#A8A8A8"), lipgloss.Color("#00A8A8"), lipgloss.Color("#000000")}
	}
}

func nextTheme(current ThemeName) ThemeName {
	for i, candidate := range themeOrder {
		if current == candidate {
			return themeOrder[(i+1)%len(themeOrder)]
		}
	}
	return ThemeClassic
}
