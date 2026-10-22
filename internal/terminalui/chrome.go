package terminalui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func Paint(background, foreground lipgloss.Color, value string, width int) string {
	if width <= 0 {
		return ""
	}
	value = ansi.Truncate(value, width, "…")
	padding := width - TextWidth(value)
	if padding < 0 {
		padding = 0
	}
	return lipgloss.NewStyle().Background(background).Foreground(foreground).Render(value + strings.Repeat(" ", padding))
}

func TopBar(theme Theme, left, right string, width int) string {
	space := width - TextWidth(left) - TextWidth(right)
	if space < 1 {
		return Paint(theme.BarBackground, theme.BarForeground, left, width)
	}
	return Paint(theme.BarBackground, theme.BarForeground, left+strings.Repeat(" ", space)+right, width)
}

func CompactCommands(labels []string, width int) bool {
	return TextWidth(strings.Join(labels, " "))+len(labels)-1 > width
}

func CommandBar(theme Theme, labels []string, width int) string {
	if CompactCommands(labels, width) {
		return Paint(theme.BarBackground, theme.BarForeground, strings.Join(labels, " "), width)
	}
	var output strings.Builder
	for index, command := range labels {
		parts := strings.SplitN(command, " ", 2)
		output.WriteString(lipgloss.NewStyle().Background(theme.Accent).Foreground(theme.Selection).Render(parts[0]))
		if len(parts) == 2 {
			output.WriteString(lipgloss.NewStyle().Background(theme.BarBackground).Foreground(theme.BarForeground).Render(" " + parts[1] + " "))
		}
		if index < len(labels)-1 {
			output.WriteString(lipgloss.NewStyle().Background(theme.Background).Render(" "))
		}
	}
	return output.String() + Paint(theme.Background, theme.Foreground, "", max(0, width-lipgloss.Width(output.String())))
}
