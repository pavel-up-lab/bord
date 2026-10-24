package terminalui

import "testing"

type backendCapabilities struct{ kitty, sixel bool }

func (c backendCapabilities) KittyGraphics() bool { return c.kitty }
func (c backendCapabilities) SixelGraphics() bool { return c.sixel }

func TestSelectImageBackend(t *testing.T) {
	tests := []struct {
		name         string
		mode         InlineMode
		capabilities backendCapabilities
		want         ImageBackend
		warning      bool
	}{
		{name: "auto prefers Kitty", mode: InlineAuto, capabilities: backendCapabilities{kitty: true, sixel: true}, want: BackendKitty},
		{name: "auto chooses Sixel", mode: InlineAuto, capabilities: backendCapabilities{sixel: true}, want: BackendSixel},
		{name: "auto falls back ANSI", mode: InlineAuto, want: BackendANSI},
		{name: "forced Kitty confirmed", mode: InlineKitty, capabilities: backendCapabilities{kitty: true}, want: BackendKitty},
		{name: "forced Kitty fallback", mode: InlineKitty, want: BackendANSI, warning: true},
		{name: "forced Sixel confirmed", mode: InlineSixel, capabilities: backendCapabilities{sixel: true}, want: BackendSixel},
		{name: "forced Sixel fallback", mode: InlineSixel, want: BackendANSI, warning: true},
		{name: "ANSI", mode: InlineANSI, want: BackendANSI},
		{name: "w3m", mode: InlineW3M, want: BackendW3M},
		{name: "off", mode: InlineOff, want: BackendOff},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := SelectImageBackend(test.mode, test.capabilities)
			if got.Backend != test.want || (got.Warning != "") != test.warning {
				t.Fatalf("selection=%+v", got)
			}
		})
	}
}
