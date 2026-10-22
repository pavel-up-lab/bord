package terminalui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// ThemePicker owns the interaction state for the shared framed theme popup.
type ThemePicker struct {
	open          bool
	index, scroll int
}

func (p ThemePicker) IsOpen() bool { return p.open }

func (p *ThemePicker) Open(catalog ThemeCatalog, selected ThemeName, height int) {
	options := catalog.Options()
	if len(options) == 0 {
		return
	}
	p.open, p.index = true, 0
	for index := range options {
		if options[index].Name == selected {
			p.index = index
			break
		}
	}
	p.ensureVisible(height)
}

func (p *ThemePicker) Close() { p.open = false }

func (p ThemePicker) Visible(height int) int { return max(1, min(10, max(1, height-4))) }

func (p *ThemePicker) ensureVisible(height int) {
	visible := p.Visible(height)
	if p.index < p.scroll {
		p.scroll = p.index
	} else if p.index >= p.scroll+visible {
		p.scroll = p.index - visible + 1
	}
	p.scroll = max(0, p.scroll)
}

// HandleKey returns a selected theme only when the operator confirms it.
func (p *ThemePicker) HandleKey(key string, catalog ThemeCatalog, height int) (ThemeName, bool) {
	options := catalog.Options()
	if !p.open || len(options) == 0 {
		p.open = false
		return "", false
	}
	switch key {
	case "esc", "f2", "t":
		p.open = false
	case "up", "k":
		p.index = max(0, p.index-1)
		p.ensureVisible(height)
	case "down", "j":
		p.index = min(len(options)-1, p.index+1)
		p.ensureVisible(height)
	case "pgup":
		p.index = max(0, p.index-p.Visible(height))
		p.ensureVisible(height)
	case "pgdown":
		p.index = min(len(options)-1, p.index+p.Visible(height))
		p.ensureVisible(height)
	case "enter":
		p.open = false
		return options[p.index].Name, true
	}
	return "", false
}

func (p ThemePicker) Geometry(catalog ThemeCatalog, terminalWidth, height, anchorX int) (x, width, visible int) {
	x = min(max(0, anchorX), max(0, terminalWidth-1))
	width = 14
	for _, option := range catalog.Options() {
		width = max(width, TextWidth(SanitizeTerminalText(option.Label))+4)
	}
	width = min(width, max(1, terminalWidth-x))
	return x, width, p.Visible(height)
}

// HandleClick closes on an outside click and confirms a visible option click.
func (p *ThemePicker) HandleClick(x, y int, catalog ThemeCatalog, terminalWidth, height, anchorX int) (ThemeName, bool) {
	if !p.open {
		return "", false
	}
	left, width, visible := p.Geometry(catalog, terminalWidth, height, anchorX)
	if x < left || x >= left+width || y < 2 || y >= 2+visible {
		p.open = false
		return "", false
	}
	options := catalog.Options()
	index := p.scroll + y - 2
	if index >= 0 && index < len(options) {
		p.index, p.open = index, false
		return options[index].Name, true
	}
	p.open = false
	return "", false
}

func (p *ThemePicker) HandleWheel(delta int, catalog ThemeCatalog, height int) bool {
	options := catalog.Options()
	if !p.open || len(options) == 0 || delta == 0 {
		return false
	}
	p.index = min(len(options)-1, max(0, p.index+delta))
	p.ensureVisible(height)
	return true
}

func (p ThemePicker) Overlay(lines []string, catalog ThemeCatalog, theme Theme, terminalWidth, height, anchorX int) []string {
	options := catalog.Options()
	if !p.open || len(options) == 0 || len(lines) < 2 {
		return lines
	}
	x, width, visible := p.Geometry(catalog, terminalWidth, height, anchorX)
	menu := []string{Paint(theme.BarBackground, theme.BarForeground, "┌"+strings.Repeat("─", max(0, width-2))+"┐", width)}
	end := min(len(options), p.scroll+visible)
	for index := p.scroll; index < end; index++ {
		prefix := "  "
		background, foreground := theme.BarBackground, theme.BarForeground
		if index == p.index {
			prefix = "> "
			background, foreground = theme.SelectionBackground, theme.Selection
		}
		label := SanitizeTerminalText(options[index].Label)
		menu = append(menu, Paint(background, foreground, "│"+prefix+label, width-1)+Paint(theme.BarBackground, theme.BarForeground, "│", 1))
	}
	menu = append(menu, Paint(theme.BarBackground, theme.BarForeground, "└"+strings.Repeat("─", max(0, width-2))+"┘", width))
	for index, overlay := range menu {
		y := index + 1
		if y >= len(lines) {
			break
		}
		lines[y] = ansi.Cut(lines[y], 0, x) + overlay + ansi.Cut(lines[y], x+width, terminalWidth)
	}
	return lines
}
