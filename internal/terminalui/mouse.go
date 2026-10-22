package terminalui

import "strings"

type MouseZone uint8

const (
	MouseOutside MouseZone = iota
	MouseTopBar
	MouseWorkspace
	MouseStatus
	MouseCommandBar
)

func MouseZoneAt(width, height, x, y int) MouseZone {
	if width <= 0 || height <= 0 || x < 0 || y < 0 || x >= width || y >= height {
		return MouseOutside
	}
	if y == 0 {
		return MouseTopBar
	}
	if y == height-1 {
		return MouseCommandBar
	}
	if y == height-2 {
		return MouseStatus
	}
	return MouseWorkspace
}

// CommandLabelWidth mirrors CommandBar: styled labels add one trailing action
// cell, while compact labels are rendered verbatim.
func CommandLabelWidth(label string, compact bool) int {
	width := TextWidth(label)
	if !compact && strings.Contains(label, " ") {
		width++
	}
	return width
}

func CommandIndexAt(labels []string, barWidth, x int) (int, bool) {
	if x < 0 || x >= barWidth {
		return 0, false
	}
	compact := CompactCommands(labels, barWidth)
	offset := 0
	for index, label := range labels {
		width := CommandLabelWidth(label, compact)
		if x >= offset && x < min(barWidth, offset+width) {
			return index, true
		}
		offset += width
		if index < len(labels)-1 {
			offset++
		}
	}
	return 0, false
}
