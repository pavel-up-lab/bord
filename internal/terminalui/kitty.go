package terminalui

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"io"
	"os"
	"os/exec"
	"strings"
)

type InlineMode string

const (
	InlineAuto  InlineMode = "auto"
	InlineKitty InlineMode = "kitty"
	InlineSixel InlineMode = "sixel"
	InlineW3M   InlineMode = "w3m"
	InlineANSI  InlineMode = "ansi"
	InlineOff   InlineMode = "off"
)

func ValidInlineMode(mode InlineMode) bool {
	switch mode {
	case InlineAuto, InlineKitty, InlineSixel, InlineW3M, InlineANSI, InlineOff:
		return true
	default:
		return false
	}
}

func InlineModeLabel(mode InlineMode) string {
	switch mode {
	case InlineAuto:
		return "Auto (Kitty → Sixel → ANSI)"
	case InlineW3M:
		return "w3m"
	case InlineKitty:
		return "Kitty placeholders"
	case InlineSixel:
		return "Sixel"
	case InlineANSI:
		return "ANSI"
	case InlineOff:
		return "Системный просмотрщик"
	default:
		return "Нативный протокол терминала"
	}
}

type Capabilities interface{ KittyGraphics() bool }
type EnvironmentCapabilities struct{}

func (EnvironmentCapabilities) KittyGraphics() bool {
	term := strings.ToLower(os.Getenv("TERM"))
	program := strings.ToLower(os.Getenv("TERM_PROGRAM"))
	return strings.Contains(term, "kitty") || program == "kitty" || program == "iterm.app" || program == "wezterm" || os.Getenv("KITTY_WINDOW_ID") != ""
}

func (EnvironmentCapabilities) SixelGraphics() bool { return false }

type KittyRenderer struct {
	Capability Capabilities
	Mode       InlineMode
}

// kittyViewerCommand renders outside Bubble Tea's fullscreen renderer. Native
// graphics APCs cannot be reliably embedded in a Bubble Tea View, so tea.Exec
// temporarily releases the terminal while this command owns it.
type KittyViewerCommand struct {
	Data          []byte
	Columns, Rows int
	stdin         io.Reader
	stdout        io.Writer
	stderr        io.Writer
}

// W3MViewerCommand opens a verified cached image without invoking a shell.
type W3MViewerCommand struct {
	Context        context.Context
	Path           string
	stdin          io.Reader
	stdout, stderr io.Writer
}

// ANSIViewerCommand renders an image as ordinary true-color text cells while
// Bubble Tea temporarily yields the terminal.
type ANSIViewerCommand struct {
	Data          []byte
	Columns, Rows int
	stdin         io.Reader
	stdout        io.Writer
	stderr        io.Writer
}

func (c *ANSIViewerCommand) SetStdin(r io.Reader)  { c.stdin = r }
func (c *ANSIViewerCommand) SetStdout(w io.Writer) { c.stdout = w }
func (c *ANSIViewerCommand) SetStderr(w io.Writer) { c.stderr = w }
func (c *ANSIViewerCommand) Run() error {
	if c.stdin == nil || c.stdout == nil {
		return errors.New("terminal streams unavailable")
	}
	lines, err := RenderANSIImage(c.Data, max(1, c.Columns), max(1, c.Rows-2), color.RGBA{})
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(c.stdout, "\x1b[2J\x1b[H\x1b[?25l%s\n\x1b[0mEnter — вернуться в Bord", strings.Join(lines, "\n")); err != nil {
		return err
	}
	defer fmt.Fprint(c.stdout, "\x1b[2J\x1b[H\x1b[?25h")
	var input [1]byte
	_, err = c.stdin.Read(input[:])
	return err
}

func (c *W3MViewerCommand) SetStdin(r io.Reader)  { c.stdin = r }
func (c *W3MViewerCommand) SetStdout(w io.Writer) { c.stdout = w }
func (c *W3MViewerCommand) SetStderr(w io.Writer) { c.stderr = w }
func (c *W3MViewerCommand) Run() error {
	if c.Path == "" {
		return errors.New("w3m image path unavailable")
	}
	ctx := c.Context
	if ctx == nil {
		ctx = context.Background()
	}
	command := exec.CommandContext(ctx, "w3m", c.Path)
	command.Stdin, command.Stdout, command.Stderr = c.stdin, c.stdout, c.stderr
	if err := command.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return errors.New("w3m is not installed")
		}
		return err
	}
	return nil
}

func (c *KittyViewerCommand) SetStdin(r io.Reader)  { c.stdin = r }
func (c *KittyViewerCommand) SetStdout(w io.Writer) { c.stdout = w }
func (c *KittyViewerCommand) SetStderr(w io.Writer) { c.stderr = w }

func (c *KittyViewerCommand) Run() error {
	if c.stdin == nil || c.stdout == nil {
		return errors.New("terminal streams unavailable")
	}
	rendered, ok := (KittyRenderer{Capability: TerminalCapabilities{KittyPlaceholders: true}, Mode: InlineKitty}).Render(c.Data, c.Columns, c.Rows)
	if !ok {
		return errors.New("inline image unavailable")
	}
	// Delete stale placements, clear the normal screen and render directly at
	// the top-left. Bubble Tea has released raw mode here, so Enter is enough to
	// return to the board.
	if _, err := fmt.Fprintf(c.stdout, "\x1b_Ga=d,d=A,q=2\x1b\\\x1b[2J\x1b[H\x1b[?25l%s\x1b[%d;1H\x1b[0mEnter — вернуться в Bord", rendered, c.Rows+2); err != nil {
		return err
	}
	defer fmt.Fprint(c.stdout, "\x1b_Ga=d,d=A,q=2\x1b\\\x1b[2J\x1b[H\x1b[?25h")
	var input [1]byte
	_, err := c.stdin.Read(input[:])
	return err
}

// PrepareKittyImage validates bounded JPEG/PNG input and encodes it as PNG,
// the compressed image format accepted by Kitty's f=100 transmission mode.
func PrepareKittyImage(data []byte, mediaType string) ([]byte, error) {
	if mediaType != "image/jpeg" && mediaType != "image/png" {
		return nil, errors.New("unsupported inline image type")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > 6000 || config.Height > 6000 || int64(config.Width)*int64(config.Height) > 16_000_000 {
		return nil, errors.New("invalid inline image dimensions")
	}
	if (mediaType == "image/jpeg" && format != "jpeg") || (mediaType == "image/png" && format != "png") {
		return nil, errors.New("inline image type mismatch")
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("decode inline image")
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, decoded); err != nil {
		return nil, errors.New("encode inline image")
	}
	return encoded.Bytes(), nil
}

// RenderANSIImage converts bounded JPEG/PNG data into true-color half blocks.
// It uses only ordinary ANSI text cells and therefore needs no image protocol.
func RenderANSIImage(data []byte, columns, rows int, background color.RGBA) ([]string, error) {
	if columns <= 0 || rows <= 0 {
		return nil, errors.New("invalid ANSI image area")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > 6000 || config.Height > 6000 || int64(config.Width)*int64(config.Height) > 16_000_000 {
		return nil, errors.New("invalid ANSI image dimensions")
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("decode ANSI image")
	}
	maxPixelHeight := rows * 2
	scale := minFloat(float64(columns)/float64(config.Width), float64(maxPixelHeight)/float64(config.Height))
	width := max(1, min(columns, int(float64(config.Width)*scale)))
	height := max(1, min(maxPixelHeight, int(float64(config.Height)*scale)))
	result := make([]string, 0, (height+1)/2)
	bounds := decoded.Bounds()
	for y := 0; y < height; y += 2 {
		var line strings.Builder
		for x := 0; x < width; x++ {
			top := sampleImage(decoded, bounds, x, y, width, height, background)
			bottom := background
			if y+1 < height {
				bottom = sampleImage(decoded, bounds, x, y+1, width, height, background)
			}
			fmt.Fprintf(&line, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀", top.R, top.G, top.B, bottom.R, bottom.G, bottom.B)
		}
		line.WriteString("\x1b[0m")
		result = append(result, line.String())
	}
	return result, nil
}

func sampleImage(source image.Image, bounds image.Rectangle, x, y, width, height int, background color.RGBA) color.RGBA {
	sourceX := bounds.Min.X + min(bounds.Dx()-1, x*bounds.Dx()/max(1, width))
	sourceY := bounds.Min.Y + min(bounds.Dy()-1, y*bounds.Dy()/max(1, height))
	r, g, b, a := source.At(sourceX, sourceY).RGBA()
	blend := func(component uint32, matte uint8) uint8 {
		value := component + uint32(matte)*257*(65535-a)/65535
		return uint8(min(65535, int(value)) >> 8)
	}
	return color.RGBA{R: blend(r, background.R), G: blend(g, background.G), B: blend(b, background.B), A: 255}
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func (r KittyRenderer) Available() bool {
	return SelectImageBackend(r.Mode, r.Capability).Backend == BackendKitty
}
func (r KittyRenderer) Render(data []byte, columns, rows int) (string, bool) {
	if !r.Available() || len(data) == 0 || columns <= 0 || rows <= 0 {
		return "", false
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	const chunk = 4096
	var out strings.Builder
	for offset := 0; offset < len(encoded); offset += chunk {
		end := min(offset+chunk, len(encoded))
		more := 0
		if end < len(encoded) {
			more = 1
		}
		if offset == 0 {
			fmt.Fprintf(&out, "\x1b_Ga=T,f=100,c=%d,r=%d,m=%d;%s\x1b\\", columns, rows, more, encoded[offset:end])
		} else {
			fmt.Fprintf(&out, "\x1b_Gm=%d;%s\x1b\\", more, encoded[offset:end])
		}
	}
	return out.String(), true
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
