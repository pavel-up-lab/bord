package terminalui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveImportsMidnightCommanderSkin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cat.ini")
	content := `[skin]
description = Cat Theme
[aliases]
Base = #101018
Text = #f0f0f8
Accent = #88ccff
[core]
_default_ = Text;Base
selected = Base;Accent
header = Accent;Base
disabled = #777788;Base
[menu]
_default_ = Base;Accent
[Lines]
dhoriz = ─
dvert = │
dlefttop = ┌
drighttop = ┐
dleftbottom = └
drightbottom = ┘
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog := DiscoverThemes()
	name, err := catalog.Resolve(path)
	if err != nil {
		t.Fatal(err)
	}
	if name != "mc:cat" {
		t.Fatalf("name=%q", name)
	}
	colors := catalog.Palette(name)
	if colors.Background != "#101018" || colors.Foreground != "#f0f0f8" || colors.Accent != "#88ccff" || colors.SelectionBackground != "#88ccff" {
		t.Fatalf("imported palette=%+v", colors)
	}
	if colors.Lines.LeftTop != "┌" || colors.Lines.Horizontal != "─" {
		t.Fatalf("imported lines=%+v", colors.Lines)
	}
	if catalog.Setting(name) != path {
		t.Fatalf("custom path was not retained: %q", catalog.Setting(name))
	}
	options := catalog.Options()
	if options[len(options)-1].Label != "Cat Theme" {
		t.Fatalf("imported label=%q", options[len(options)-1].Label)
	}
}

func TestInvalidMidnightCommanderSkinIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.ini")
	if err := os.WriteFile(path, []byte("[skin]\ndescription=broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog := DiscoverThemes()
	if _, err := catalog.Resolve(path); err == nil {
		t.Fatal("invalid skin was accepted")
	}
}

func TestMidnightCommander256ColorSyntax(t *testing.T) {
	for input, want := range map[string]string{
		"color9":   "#FF0000",
		"color231": "#FFFFFF",
		"rgb404":   "#D700D7",
		"gray22":   "#E4E4E4",
	} {
		if got := normalizeMCColor(input); got != want {
			t.Fatalf("normalizeMCColor(%q)=%q want=%q", input, got, want)
		}
	}
}
