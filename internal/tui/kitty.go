package tui

import (
	"encoding/base64"
	"fmt"
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

func (r KittyRenderer) Available() bool {
	return r.Mode == InlineKitty || (r.Mode == InlineAuto && r.Capability != nil && r.Capability.KittyGraphics())
}
func (r KittyRenderer) Render(data []byte, columns, rows int) (string, bool) {
	if !r.Available() {
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
