package terminalui

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func testPNG(t testing.TB, rgba color.RGBA) []byte {
	t.Helper()
	var data bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := range 2 {
		for x := range 2 {
			img.SetRGBA(x, y, rgba)
		}
	}
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestRenderKittyFrameGolden(t *testing.T) {
	data := testPNG(t, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	frame, err := RenderKittyFrame(data, 0x01022a, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	wantControl := "\x1b_Ga=T,f=100,i=66090,c=2,r=2,U=1,q=2,m=0;" + base64.StdEncoding.EncodeToString(data) + "\x1b\\"
	if frame.Control != wantControl {
		t.Fatalf("control mismatch\n got: %q\nwant: %q", frame.Control, wantControl)
	}
	wantLines := []string{
		"\x1b[38;2;1;2;42m\U0010eeee\u0305\u0305\U0010eeee\u0305\u030d\x1b[39m",
		"\x1b[38;2;1;2;42m\U0010eeee\u030d\u0305\U0010eeee\u030d\u030d\x1b[39m",
	}
	for index, line := range frame.Lines {
		if line != wantLines[index] || ansi.StringWidth(line) != 2 {
			t.Fatalf("line %d=%q width=%d", index, line, ansi.StringWidth(line))
		}
	}
	if frame.Delete != "\x1b_Ga=d,d=I,i=66090,q=2\x1b\\" {
		t.Fatalf("delete=%q", frame.Delete)
	}
}

func TestRenderKittyFrameBoundsAndValidation(t *testing.T) {
	data := testPNG(t, color.RGBA{A: 255})
	frame, err := RenderKittyFrame(data, 1, 999, 999)
	if err != nil || frame.Columns != maxKittyCells || frame.Rows != maxKittyCells || len(frame.Lines) != maxKittyCells {
		t.Fatalf("frame=%+v err=%v", frame, err)
	}
	for _, test := range []struct {
		data    []byte
		id      uint32
		columns int
		rows    int
	}{
		{data: []byte("not png"), id: 1, columns: 1, rows: 1},
		{data: data, columns: 1, rows: 1},
		{data: data, id: 0x01000000, columns: 1, rows: 1},
		{data: data, id: 1, columns: 0, rows: 1},
	} {
		if _, err := RenderKittyFrame(test.data, test.id, test.columns, test.rows); err == nil {
			t.Fatalf("expected error for %+v", test)
		}
	}
}

func FuzzRenderKittyFrame(f *testing.F) {
	f.Add(uint32(42), 20, 8, byte(1), byte(2), byte(3), byte(255))
	f.Fuzz(func(t *testing.T, id uint32, columns, rows int, red, green, blue, alpha byte) {
		id = id%0x00ffffff + 1
		columns = int(uint(columns)%400) + 1
		rows = int(uint(rows)%400) + 1
		data := testPNG(t, color.RGBA{R: red, G: green, B: blue, A: alpha})
		frame, err := RenderKittyFrame(data, id, columns, rows)
		if err != nil {
			t.Fatal(err)
		}
		if len(frame.Lines) > maxKittyCells || frame.Columns > maxKittyCells || frame.Rows > maxKittyCells {
			t.Fatalf("unbounded frame: %+v", frame)
		}
		for _, line := range frame.Lines {
			if width := ansi.StringWidth(line); width != frame.Columns {
				t.Fatalf("width=%d columns=%d", width, frame.Columns)
			}
		}
		for _, payload := range regexp.MustCompile(`;([^\x1b]*)\x1b\\`).FindAllStringSubmatch(frame.Control, -1) {
			if strings.Trim(payload[1], "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/=") != "" {
				t.Fatalf("unsafe payload %q", payload[1])
			}
		}
	})
}
