package tui

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
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
