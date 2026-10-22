package terminalui

import (
	"strings"
	"testing"
)

func TestPanelGeometryThemesAndRows(t *testing.T) {
	theme := Palette(ThemeClassic)
	theme.Lines = LineSet{"-", "|", "+", "+", "+", "+"}
	rows := []Row{{Lines: []string{"选中", "\x1b[31mtrusted\x1b[0m"}, Active: true, TrustedANSI: true}}
	for _, active := range []bool{false, true} {
		panel := Panel(theme, 20, 6, "Title", "HINT", rows, active)
		if len(panel) != 6 {
			t.Fatalf("height=%d", len(panel))
		}
		for index, line := range panel {
			if TextWidth(line) != 20 {
				t.Fatalf("line %d width=%d: %q", index, TextWidth(line), line)
			}
		}
		if !strings.Contains(panel[0], "+-") || !strings.Contains(panel[1], "HINT") || !strings.Contains(panel[3], "trusted") {
			t.Fatalf("panel=%q", panel)
		}
	}
}

func TestPanelTinySanitizesAndClips(t *testing.T) {
	theme := Palette(ThemeClassic)
	for _, size := range [][2]int{{0, 0}, {1, 1}, {2, 2}, {8, 3}} {
		panel := Panel(theme, size[0], size[1], "bad\x1b[2J title", "hint", []Row{{Lines: []string{"long value"}}}, true)
		if len(panel) != size[1] {
			t.Fatalf("size=%v height=%d", size, len(panel))
		}
		for _, line := range panel {
			if TextWidth(line) != size[0] || strings.Contains(line, "\x1b[2J") {
				t.Fatalf("size=%v line=%q width=%d", size, line, TextWidth(line))
			}
		}
	}
}

func TestSplitAndJoinPanels(t *testing.T) {
	left, right := SplitWidth(120, 24, 28)
	if left != 40 || right != 80 {
		t.Fatalf("split=%d,%d", left, right)
	}
	joined := JoinPanels(2, []string{"a", "b"}, []string{"c", "d"})
	if strings.Join(joined, "") != "acbd" {
		t.Fatalf("joined=%q", joined)
	}
}
