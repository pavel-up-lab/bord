package terminalui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Row is one selectable logical row in a framed panel. A row may occupy
// multiple terminal lines while sharing the same selection state.
type Row struct {
	Lines       []string
	Active      bool
	TrustedANSI bool
}

// Panel renders the canonical Bord frame used by both public and private TUI.
func Panel(theme Theme, width, height int, title, hint string, rows []Row, active bool) []string {
	if height <= 0 {
		return nil
	}
	border := theme.Muted
	if active {
		border = theme.Accent
	}
	if width < 2 || height < 2 {
		result := make([]string, height)
		for index := range result {
			value := ""
			if index == 0 {
				value = SanitizeTerminalText(title)
			}
			result[index] = Paint(theme.Background, theme.Foreground, value, width)
		}
		return result
	}
	inner := width - 2
	lines := theme.Lines
	if lines.Horizontal == "" {
		lines = DefaultLines()
	}
	fill := []rune(lines.Horizontal)[0]
	topText := lines.Horizontal + " " + SanitizeTerminalText(title) + " "
	top := lines.LeftTop + fitPanel(topText, inner, fill, false) + lines.RightTop
	result := []string{Paint(theme.Background, border, top, width)}
	if height > 2 {
		result = append(result, paintPanel(theme.BarBackground, theme.BarForeground, " "+SanitizeTerminalText(hint), inner, false,
			lipgloss.NewStyle().Background(theme.Background).Foreground(border).Render(lines.Vertical),
			lipgloss.NewStyle().Background(theme.Background).Foreground(border).Render(lines.Vertical), ' '))
	}
	available := height - len(result) - 1
	for _, row := range rows {
		for _, text := range row.Lines {
			if available <= 0 {
				break
			}
			background, foreground := theme.Background, theme.Foreground
			if row.Active {
				if active {
					background, foreground = theme.SelectionBackground, theme.Selection
				} else {
					foreground = theme.Accent
				}
			}
			left := lipgloss.NewStyle().Background(theme.Background).Foreground(border).Render(lines.Vertical)
			result = append(result, paintPanel(background, foreground, " "+text, inner, row.TrustedANSI, left, left, ' '))
			available--
		}
	}
	for available > 0 {
		left := lipgloss.NewStyle().Background(theme.Background).Foreground(border).Render(lines.Vertical)
		result = append(result, paintPanel(theme.Background, theme.Foreground, "", inner, false, left, left, ' '))
		available--
	}
	bottom := lines.LeftBottom + strings.Repeat(lines.Horizontal, inner) + lines.RightBottom
	return append(result, Paint(theme.Background, border, bottom, width))
}

func paintPanel(background, foreground lipgloss.Color, value string, inner int, trusted bool, left, right string, fill rune) string {
	return left + lipgloss.NewStyle().Background(background).Foreground(foreground).Render(fitPanel(value, inner, fill, trusted)) + right
}

func fitPanel(value string, width int, fill rune, trusted bool) string {
	if width <= 0 {
		return ""
	}
	if !trusted {
		value = SanitizeTerminalText(value)
	}
	value = ansi.Truncate(value, width, "…")
	if missing := width - TextWidth(value); missing > 0 {
		value += strings.Repeat(string(fill), missing)
	}
	return value
}

// SplitWidth returns the ordinary Bord two-panel geometry.
func SplitWidth(total, minimumLeft, minimumRight int) (left, right int) {
	left = max(minimumLeft, total/3)
	if left > total-minimumRight {
		left = total / 2
	}
	left = min(max(0, total), left)
	return left, max(0, total-left)
}

// JoinPanels joins already width-bounded columns without introducing spacing.
func JoinPanels(height int, columns ...[]string) []string {
	result := make([]string, max(0, height))
	for row := range result {
		var joined strings.Builder
		for _, column := range columns {
			if row < len(column) {
				joined.WriteString(column[row])
			}
		}
		result[row] = joined.String()
	}
	return result
}
