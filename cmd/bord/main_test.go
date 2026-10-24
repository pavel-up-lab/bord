package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"bord/internal/terminalui"

	tea "github.com/charmbracelet/bubbletea"
)

func TestVersionSmoke(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"--version"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout.String()) != version || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunAlwaysClosesImageSession(t *testing.T) {
	originalRun, originalPrepare := runProgram, prepareTerminal
	t.Cleanup(func() { runProgram, prepareTerminal = originalRun, originalPrepare })
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	for _, test := range []struct {
		name   string
		cancel bool
		err    error
	}{
		{name: "success"},
		{name: "program error", err: errors.New("render failed")},
		{name: "cancelled", cancel: true, err: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			var cleanup bytes.Buffer
			prepareTerminal = func(context.Context, io.Writer) (terminalui.TerminalCapabilities, *terminalui.ImageSession) {
				session := terminalui.NewImageSession(&cleanup)
				session.Activate(terminalui.NativeKitty, "test", 10, 4)
				return terminalui.TerminalCapabilities{}, session
			}
			runProgram = func(ctx context.Context, model tea.Model, _ io.Writer) (tea.Model, error) {
				if test.cancel && ctx.Err() == nil {
					t.Fatal("cancelled context not propagated")
				}
				return model, test.err
			}
			ctx := context.Background()
			if test.cancel {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			err := run(ctx, []string{"--config", t.TempDir() + "/state.json"}, io.Discard, io.Discard)
			if (test.err != nil) != (err != nil) || !strings.Contains(cleanup.String(), "a=d") {
				t.Fatalf("err=%v cleanup=%q", err, cleanup.String())
			}
		})
	}
}

func TestRejectsUnknownInlineModeBeforeStartingTUI(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"--config", t.TempDir() + "/state.json", "--inline", "regis"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "--inline") {
		t.Fatalf("err=%v", err)
	}
}

func TestRejectsUnknownThemeBeforeStartingTUI(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"--config", t.TempDir() + "/state.json", "--theme", "ultraviolet"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "--theme") {
		t.Fatalf("err=%v", err)
	}
}

func TestFlagErrorsAreTerminalSafe(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"--bad\x1b]52;c;c2VjcmV0\a\u009b"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected flag error")
	}
	for _, control := range []string{"\x1b", "\a", "\u009b"} {
		if strings.Contains(stderr.String(), control) {
			t.Fatalf("control %q survived in stderr %q", control, stderr.String())
		}
	}
}
