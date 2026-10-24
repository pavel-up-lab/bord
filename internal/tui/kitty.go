package tui

import "bord/internal/terminalui"

type InlineMode = terminalui.InlineMode

const (
	InlineAuto  = terminalui.InlineAuto
	InlineKitty = terminalui.InlineKitty
	InlineSixel = terminalui.InlineSixel
	InlineW3M   = terminalui.InlineW3M
	InlineANSI  = terminalui.InlineANSI
	InlineOff   = terminalui.InlineOff
)

type Capabilities = terminalui.Capabilities
type EnvironmentCapabilities = terminalui.EnvironmentCapabilities
type KittyRenderer = terminalui.KittyRenderer
type kittyViewerCommand = terminalui.KittyViewerCommand

func PrepareKittyImage(data []byte, mediaType string) ([]byte, error) {
	return terminalui.PrepareKittyImage(data, mediaType)
}
