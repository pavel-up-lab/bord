package tui

import "bord/internal/terminalui"

type ThemeName = terminalui.ThemeName

const (
	ThemeClassic = terminalui.ThemeClassic
	ThemeAmber   = terminalui.ThemeAmber
	ThemeGreen   = terminalui.ThemeGreen
	ThemeMono    = terminalui.ThemeMono
)

type theme = terminalui.Theme
type ThemeOption = terminalui.ThemeOption
type ThemeCatalog = terminalui.ThemeCatalog

var doubleLines = terminalui.DefaultLines()

func DiscoverThemes() ThemeCatalog               { return terminalui.DiscoverThemes() }
func ParseTheme(value string) (ThemeName, error) { return terminalui.ParseTheme(value) }
func palette(name ThemeName) theme               { return terminalui.Palette(name) }
