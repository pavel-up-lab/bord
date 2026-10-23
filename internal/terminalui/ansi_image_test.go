package terminalui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func TestRenderANSIImageUsesBoundedHalfBlocks(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	source.Set(0, 0, color.NRGBA{R: 255, A: 255})
	source.Set(1, 0, color.NRGBA{G: 255, A: 255})
	source.Set(0, 1, color.NRGBA{B: 255, A: 255})
	source.Set(1, 1, color.NRGBA{R: 255, G: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}

	lines, err := RenderANSIImage(encoded.Bytes(), 2, 1, color.RGBA{})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || TextWidth(lines[0]) != 2 || strings.Count(lines[0], "▀") != 2 {
		t.Fatalf("lines=%q", lines)
	}
	for _, sequence := range []string{"38;2;255;0;0", "48;2;0;0;255", "38;2;0;255;0", "48;2;255;255;0"} {
		if !strings.Contains(lines[0], sequence) {
			t.Fatalf("missing %q in %q", sequence, lines[0])
		}
	}
}

func TestRenderANSIImageRejectsInvalidInput(t *testing.T) {
	if _, err := RenderANSIImage([]byte("not an image"), 10, 10, color.RGBA{}); err == nil {
		t.Fatal("invalid image accepted")
	}
}

func TestInlineModesIncludeSettingsChoices(t *testing.T) {
	for _, mode := range []InlineMode{InlineAuto, InlineKitty, InlineW3M, InlineANSI, InlineOff} {
		if !ValidInlineMode(mode) {
			t.Fatalf("mode %q rejected", mode)
		}
	}
	if ValidInlineMode("sixel") {
		t.Fatal("unknown mode accepted")
	}
}

func TestANSIViewerCommandRestoresTerminal(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	source.Set(0, 0, color.NRGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command := &ANSIViewerCommand{Data: encoded.Bytes(), Columns: 10, Rows: 5}
	command.SetStdin(strings.NewReader("\n"))
	command.SetStdout(&output)
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	for _, sequence := range []string{"▀", "\x1b[?25l", "\x1b[?25h", "Enter — вернуться"} {
		if !strings.Contains(output.String(), sequence) {
			t.Fatalf("missing %q in %q", sequence, output.String())
		}
	}
}

func TestW3MViewerRejectsMissingPath(t *testing.T) {
	if err := (&W3MViewerCommand{}).Run(); err == nil {
		t.Fatal("empty w3m path accepted")
	}
}
