package terminalui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func sixelPNG(t testing.TB, width, height int, fill color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.SetRGBA(x, y, fill)
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestSixelRendererFramingBoundsAndGenerationCache(t *testing.T) {
	renderer := &SixelRenderer{}
	data := sixelPNG(t, 4, 2, color.RGBA{R: 200, G: 100, B: 50, A: 255})
	first, err := renderer.Render(data, "attachment", 20, 8, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first.Control, "\x1bP0;1;8q") || !strings.HasSuffix(first.Control, "\x1b\\") || first.Columns > 20 || first.Rows > 8 || len(first.Lines) != first.Rows {
		t.Fatalf("frame=%+v", first)
	}
	for _, line := range first.Lines {
		if len(line) != first.Columns {
			t.Fatalf("reserved width=%d want=%d", len(line), first.Columns)
		}
	}
	// Invalid bytes still return the cached frame while identity, size and
	// generation are unchanged; a new generation must encode again and fail.
	cached, err := renderer.Render([]byte("invalid"), "attachment", 20, 8, 1)
	if err != nil || cached.Control != first.Control {
		t.Fatalf("cache miss: err=%v", err)
	}
	if _, err := renderer.Render([]byte("invalid"), "attachment", 20, 8, 2); err == nil {
		t.Fatal("new generation reused stale Sixel frame")
	}
}

func TestSixelRendererRejectsUnboundedInputs(t *testing.T) {
	renderer := &SixelRenderer{}
	data := sixelPNG(t, 1, 1, color.RGBA{A: 255})
	if _, err := renderer.Render(data, "attachment", 0, 1, 1); err == nil {
		t.Fatal("zero width accepted")
	}
	frame, err := renderer.Render(data, "attachment", 10000, 10000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Columns > maxSixelWidth/sixelCellWidth || frame.Rows > maxSixelHeight/sixelCellHeight || len(frame.Control) > maxSixelPayload {
		t.Fatalf("unbounded frame=%+v payload=%d", frame, len(frame.Control))
	}
}

func FuzzSixelRenderer(f *testing.F) {
	f.Add(20, 8, byte(20), byte(40), byte(60), byte(255))
	f.Fuzz(func(t *testing.T, columns, rows int, red, green, blue, alpha byte) {
		columns = int(uint(columns)%200) + 1
		rows = int(uint(rows)%80) + 1
		data := sixelPNG(t, 2, 2, color.RGBA{R: red, G: green, B: blue, A: alpha})
		frame, err := (&SixelRenderer{}).Render(data, "attachment", columns, rows, 1)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(frame.Control, "\x1bP") || !strings.HasSuffix(frame.Control, "\x1b\\") || frame.Columns > min(columns, maxSixelWidth/sixelCellWidth) || frame.Rows > min(rows, maxSixelHeight/sixelCellHeight) || len(frame.Control) > maxSixelPayload {
			t.Fatalf("invalid bounded frame=%+v", frame)
		}
	})
}
