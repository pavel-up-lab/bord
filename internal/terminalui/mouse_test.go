package terminalui

import "testing"

func TestMouseZoneKeepsChromeOutsideWorkspace(t *testing.T) {
	if got := MouseZoneAt(80, 24, 10, 0); got != MouseTopBar {
		t.Fatalf("top zone=%v", got)
	}
	if got := MouseZoneAt(80, 24, 10, 23); got != MouseCommandBar {
		t.Fatalf("command zone=%v", got)
	}
	if got := MouseZoneAt(80, 24, 10, 22); got != MouseStatus {
		t.Fatalf("status zone=%v", got)
	}
	if got := MouseZoneAt(80, 24, 10, 5); got != MouseWorkspace {
		t.Fatalf("workspace zone=%v", got)
	}
	if got := MouseZoneAt(80, 24, 80, 5); got != MouseOutside {
		t.Fatalf("outside zone=%v", got)
	}
}

func TestCommandIndexMatchesEveryRenderedLabelCell(t *testing.T) {
	labels := []string{"F1 Help", "F5 Refresh", "F10 Quit"}
	for _, width := range []int{24, 80} {
		compact := CompactCommands(labels, width)
		offset := 0
		for expected, label := range labels {
			labelWidth := CommandLabelWidth(label, compact)
			for x := offset; x < min(width, offset+labelWidth); x++ {
				got, ok := CommandIndexAt(labels, width, x)
				if !ok || got != expected {
					t.Fatalf("width=%d x=%d index=%d ok=%v want=%d", width, x, got, ok, expected)
				}
			}
			offset += labelWidth + 1
		}
	}
}
