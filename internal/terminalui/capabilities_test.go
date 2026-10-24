package terminalui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type probeResult struct {
	response []byte
	err      error
	wait     bool
}

func (p probeResult) Exchange(ctx context.Context, _ []byte) ([]byte, error) {
	if p.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return p.response, p.err
}

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestCapabilityDetectorRequiresCompleteConfirmedResponses(t *testing.T) {
	response := []byte("\x1b_Gi=" + kittyProbeID + ";OK\x1b\\\x1b[?1;2;4;22c")
	tests := []struct {
		name      string
		probe     probeResult
		env       map[string]string
		wantKitty bool
		wantSixel bool
	}{
		{name: "kitty and sixel", probe: probeResult{response: response}, env: map[string]string{"TERM_PROGRAM": "kitty"}, wantKitty: true, wantSixel: true},
		{name: "unknown terminal does not get placeholders", probe: probeResult{response: response}, env: map[string]string{"TERM": "xterm-256color"}, wantSixel: true},
		{name: "incomplete", probe: probeResult{response: []byte("\x1b_Gi=" + kittyProbeID)}, env: map[string]string{"TERM": "xterm-kitty"}},
		{name: "partial response with error", probe: probeResult{response: []byte("\x1b[?1;4c"), err: errors.New("timeout")}, wantSixel: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := (CapabilityDetector{Transport: test.probe, LookupEnv: env(test.env)}).Detect(context.Background())
			if got.KittyPlaceholders != test.wantKitty || got.Sixel != test.wantSixel {
				t.Fatalf("capabilities=%+v", got)
			}
		})
	}
}

func TestCapabilityDetectorRejectsMultiplexersAndTimeout(t *testing.T) {
	response := []byte("\x1b_Gi=" + kittyProbeID + ";OK\x1b\\\x1b[?1;4c")
	for _, variables := range []map[string]string{{"TMUX": "1", "TERM": "xterm-kitty"}, {"STY": "1", "TERM": "xterm-kitty"}} {
		got := (CapabilityDetector{Transport: probeResult{response: response}, LookupEnv: env(variables)}).Detect(context.Background())
		if got != (TerminalCapabilities{}) {
			t.Fatalf("multiplexer capabilities=%+v", got)
		}
	}
	started := time.Now()
	got := (CapabilityDetector{Transport: probeResult{wait: true}, Timeout: time.Millisecond, LookupEnv: env(map[string]string{"TERM": "xterm-kitty"})}).Detect(context.Background())
	if got != (TerminalCapabilities{}) || time.Since(started) > time.Second {
		t.Fatalf("timeout capabilities=%+v duration=%s", got, time.Since(started))
	}
}

func TestTerminalProbeQueryIsBoundedAndContainsBothProtocols(t *testing.T) {
	query := kittyProbe + deviceProbe
	if len(query) > 256 || !strings.Contains(query, "a=q") || !strings.HasSuffix(query, "\x1b[c") {
		t.Fatalf("query=%q", query)
	}
}
