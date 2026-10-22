package terminalui

import (
	"strings"
	"testing"
)

func TestThemePickerKeyboardViewportAndCancellation(t *testing.T) {
	catalog := builtinCatalog()
	for index := 0; index < 12; index++ {
		name := ThemeName("extra-" + string(rune('a'+index)))
		catalog.add(name, "Extra theme", catalog.Palette(ThemeClassic))
	}
	var picker ThemePicker
	picker.Open(catalog, ThemeClassic, 8)
	if !picker.IsOpen() {
		t.Fatal("picker did not open")
	}
	for range 8 {
		picker.HandleKey("down", catalog, 8)
	}
	picker.HandleKey("pgdown", catalog, 8)
	selected, ok := picker.HandleKey("enter", catalog, 8)
	if !ok || selected == ThemeClassic {
		t.Fatalf("selection=%q ok=%v", selected, ok)
	}
	picker.Open(catalog, ThemeAmber, 8)
	if selected, ok = picker.HandleKey("esc", catalog, 8); ok || selected != "" || picker.IsOpen() {
		t.Fatalf("cancel selected=%q ok=%v", selected, ok)
	}
	for _, key := range []string{"f2", "t"} {
		picker.Open(catalog, ThemeAmber, 8)
		picker.HandleKey("j", catalog, 8)
		picker.HandleKey("k", catalog, 8)
		picker.HandleKey("pgdown", catalog, 8)
		picker.HandleKey("pgup", catalog, 8)
		picker.HandleKey("up", catalog, 8)
		if selected, ok = picker.HandleKey(key, catalog, 8); ok || selected != "" || picker.IsOpen() {
			t.Fatalf("%s cancel selected=%q ok=%v", key, selected, ok)
		}
	}
}

func TestThemePickerMouseWheelGeometryAndSanitization(t *testing.T) {
	catalog := builtinCatalog()
	catalog.add("unsafe", "bad\x1b[2J label that is deliberately very long", catalog.Palette(ThemeClassic))
	var picker ThemePicker
	picker.Open(catalog, ThemeClassic, 7)
	if !picker.HandleWheel(2, catalog, 7) {
		t.Fatal("wheel was not handled")
	}
	lines := make([]string, 7)
	for index := range lines {
		lines[index] = strings.Repeat(" ", 20)
	}
	lines = picker.Overlay(lines, catalog, catalog.Palette(ThemeClassic), 20, 7, 6)
	for _, line := range lines {
		if TextWidth(line) != 20 || strings.Contains(line, "\x1b[2J") {
			t.Fatalf("unsafe overlay line=%q width=%d", line, TextWidth(line))
		}
	}
	if selected, ok := picker.HandleClick(7, 2, catalog, 20, 7, 6); !ok || selected == "" {
		t.Fatalf("click selected=%q ok=%v", selected, ok)
	}
	picker.Open(catalog, ThemeClassic, 7)
	if _, ok := picker.HandleClick(0, 6, catalog, 20, 7, 6); ok || picker.IsOpen() {
		t.Fatal("outside click did not cancel")
	}
}
