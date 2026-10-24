package terminalui

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	term "github.com/charmbracelet/x/term"
)

const (
	kittyProbeID = "2147483646"
	kittyProbe   = "\x1b_Gi=" + kittyProbeID + ",a=q,t=d,f=24,s=1,v=1;AAAA\x1b\\"
	deviceProbe  = "\x1b[c"
)

// TerminalCapabilities is an immutable result of probing the terminal before
// Bubble Tea starts reading input.
type TerminalCapabilities struct {
	KittyPlaceholders bool
	Sixel             bool
}

func (c TerminalCapabilities) KittyGraphics() bool { return c.KittyPlaceholders }
func (c TerminalCapabilities) SixelGraphics() bool { return c.Sixel }

// ProbeTransport performs one bounded request/response exchange with the
// controlling terminal. Implementations must not retain response bytes for the
// application's subsequent input loop.
type ProbeTransport interface {
	Exchange(context.Context, []byte) ([]byte, error)
}

type CapabilityDetector struct {
	Transport ProbeTransport
	Timeout   time.Duration
	LookupEnv func(string) string
}

func (d CapabilityDetector) Detect(ctx context.Context) TerminalCapabilities {
	lookup := d.LookupEnv
	if lookup == nil {
		lookup = os.Getenv
	}
	// Multiplexers need protocol-specific passthrough. Stay conservative until
	// such a probe succeeds rather than trusting the outer terminal variables.
	if lookup("TMUX") != "" || lookup("STY") != "" || d.Transport == nil {
		return TerminalCapabilities{}
	}
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = 150 * time.Millisecond
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	response, err := d.Transport.Exchange(probeCtx, []byte(kittyProbe+deviceProbe))
	if err != nil && len(response) == 0 {
		return TerminalCapabilities{}
	}
	return ParseTerminalCapabilities(response, lookup)
}

func ParseTerminalCapabilities(response []byte, lookup func(string) string) TerminalCapabilities {
	if lookup == nil {
		lookup = func(string) string { return "" }
	}
	text := string(response)
	kittyOK := strings.Contains(text, "_Gi="+kittyProbeID+";OK") && knownPlaceholderTerminal(lookup)
	return TerminalCapabilities{KittyPlaceholders: kittyOK, Sixel: responseHasDA1Feature(text, "4")}
}

func knownPlaceholderTerminal(lookup func(string) string) bool {
	termName := strings.ToLower(lookup("TERM"))
	program := strings.ToLower(lookup("TERM_PROGRAM"))
	return strings.Contains(termName, "kitty") || lookup("KITTY_WINDOW_ID") != "" ||
		program == "kitty" || program == "ghostty" || program == "wezterm" || program == "iterm.app"
}

func responseHasDA1Feature(response, feature string) bool {
	for {
		start := strings.Index(response, "\x1b[?")
		if start < 0 {
			return false
		}
		response = response[start+3:]
		end := strings.IndexByte(response, 'c')
		if end < 0 {
			return false
		}
		for _, value := range strings.Split(response[:end], ";") {
			if value == feature {
				return true
			}
		}
		response = response[end+1:]
	}
}

type TTYProbeTransport struct {
	Input, Output *os.File
}

func (t TTYProbeTransport) Exchange(ctx context.Context, query []byte) ([]byte, error) {
	if t.Input == nil || t.Output == nil || !term.IsTerminal(t.Input.Fd()) || !term.IsTerminal(t.Output.Fd()) {
		return nil, errors.New("terminal probe unavailable")
	}
	state, err := term.MakeRaw(t.Input.Fd())
	if err != nil {
		return nil, err
	}
	defer term.Restore(t.Input.Fd(), state) //nolint:errcheck -- best-effort terminal restoration after probe
	if _, err = t.Output.Write(query); err != nil {
		return nil, err
	}
	return readProbeResponse(ctx, t.Input)
}

func probeResponseComplete(response []byte) bool {
	text := string(response)
	return strings.Contains(text, "_Gi="+kittyProbeID+";") && strings.Contains(text, "c")
}

func DetectTerminalCapabilities(ctx context.Context, input, output *os.File) TerminalCapabilities {
	return (CapabilityDetector{Transport: TTYProbeTransport{Input: input, Output: output}}).Detect(ctx)
}
