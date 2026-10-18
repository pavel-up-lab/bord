package tui

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"os"
	"strings"
)

type InlineMode string

const (
	InlineAuto  InlineMode = "auto"
	InlineKitty InlineMode = "kitty"
	InlineOff   InlineMode = "off"
)

type Capabilities interface{ KittyGraphics() bool }
type EnvironmentCapabilities struct{}

func (EnvironmentCapabilities) KittyGraphics() bool {
	term := strings.ToLower(os.Getenv("TERM"))
	program := strings.ToLower(os.Getenv("TERM_PROGRAM"))
	return strings.Contains(term, "kitty") || program == "kitty" || os.Getenv("KITTY_WINDOW_ID") != ""
}

type KittyRenderer struct {
	Capability Capabilities
	Mode       InlineMode
}

// kittyViewerCommand renders outside Bubble Tea's fullscreen renderer. Native
// graphics APCs cannot be reliably embedded in a Bubble Tea View, so tea.Exec
// temporarily releases the terminal while this command owns it.
type kittyViewerCommand struct {
	data          []byte
	columns, rows int
	stdin         io.Reader
	stdout        io.Writer
	stderr        io.Writer
}

func (c *kittyViewerCommand) SetStdin(r io.Reader)  { c.stdin = r }
func (c *kittyViewerCommand) SetStdout(w io.Writer) { c.stdout = w }
func (c *kittyViewerCommand) SetStderr(w io.Writer) { c.stderr = w }

func (c *kittyViewerCommand) Run() error {
	if c.stdin == nil || c.stdout == nil {
		return errors.New("terminal streams unavailable")
	}
	rendered, ok := (KittyRenderer{Mode: InlineKitty}).Render(c.data, c.columns, c.rows)
	if !ok {
		return errors.New("inline image unavailable")
	}
	// Delete stale placements, clear the normal screen and render directly at
	// the top-left. Bubble Tea has released raw mode here, so Enter is enough to
	// return to the board.
	if _, err := fmt.Fprintf(c.stdout, "\x1b_Ga=d,d=A,q=2\x1b\\\x1b[2J\x1b[H\x1b[?25l%s\x1b[%d;1H\x1b[0mEnter — вернуться в Bord", rendered, c.rows+2); err != nil {
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

func (r KittyRenderer) Available() bool {
	return r.Mode == InlineKitty || (r.Mode == InlineAuto && r.Capability != nil && r.Capability.KittyGraphics())
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
