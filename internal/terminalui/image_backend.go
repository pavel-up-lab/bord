package terminalui

type ImageBackend string

const (
	BackendKitty ImageBackend = "kitty"
	BackendSixel ImageBackend = "sixel"
	BackendANSI  ImageBackend = "ansi"
	BackendW3M   ImageBackend = "w3m"
	BackendOff   ImageBackend = "off"
)

type BackendSelection struct {
	Backend ImageBackend
	Warning string
}

type sixelCapabilities interface{ SixelGraphics() bool }

func SelectImageBackend(mode InlineMode, capabilities Capabilities) BackendSelection {
	kitty := capabilities != nil && capabilities.KittyGraphics()
	sixel := false
	if detector, ok := capabilities.(sixelCapabilities); ok {
		sixel = detector.SixelGraphics()
	}
	switch mode {
	case InlineAuto:
		if kitty {
			return BackendSelection{Backend: BackendKitty}
		}
		if sixel {
			return BackendSelection{Backend: BackendSixel}
		}
		return BackendSelection{Backend: BackendANSI}
	case InlineKitty:
		if kitty {
			return BackendSelection{Backend: BackendKitty}
		}
		return BackendSelection{Backend: BackendANSI, Warning: "Kitty не подтверждён терминалом; используется ANSI"}
	case InlineSixel:
		if sixel {
			return BackendSelection{Backend: BackendSixel}
		}
		return BackendSelection{Backend: BackendANSI, Warning: "Sixel не подтверждён терминалом; используется ANSI"}
	case InlineW3M:
		return BackendSelection{Backend: BackendW3M}
	case InlineOff:
		return BackendSelection{Backend: BackendOff}
	default:
		return BackendSelection{Backend: BackendANSI}
	}
}
