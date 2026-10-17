//go:build !windows

package tui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type signalSmokeModel struct{}

func (signalSmokeModel) Init() tea.Cmd {
	return func() tea.Msg {
		_, _ = fmt.Fprintln(os.Stderr, "BORD_SIGNAL_SMOKE_READY")
		return nil
	}
}
func (m signalSmokeModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }
func (signalSmokeModel) View() string                          { return "bord signal smoke" }

func TestTerminalSmokeSignalRestoresAlternateScreen(t *testing.T) {
	if os.Getenv("BORD_SIGNAL_SMOKE_HELPER") == "1" {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
		defer stop()
		program := tea.NewProgram(signalSmokeModel{}, tea.WithContext(ctx), tea.WithoutSignalHandler(), tea.WithInput(nil), tea.WithOutput(os.Stdout), tea.WithAltScreen())
		if _, err := program.Run(); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		return
	}

	command := exec.Command(os.Args[0], "-test.run=^TestTerminalSmokeSignalRestoresAlternateScreen$")
	command.Env = append(os.Environ(), "BORD_SIGNAL_SMOKE_HELPER=1")
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}

	ready := make(chan error, 1)
	stderrDone := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stderr)
		var output strings.Builder
		found := false
		for scanner.Scan() {
			line := scanner.Text()
			output.WriteString(line)
			output.WriteByte('\n')
			if !found && strings.Contains(line, "BORD_SIGNAL_SMOKE_READY") {
				found = true
				ready <- nil
			}
		}
		if !found {
			ready <- fmt.Errorf("helper exited before readiness: %v", scanner.Err())
		}
		stderrDone <- output.String()
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		t.Fatal("signal smoke helper did not become ready")
	}

	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatal(err)
	}
	waitErr := command.Wait()
	helperStderr := <-stderrDone
	if waitErr != nil {
		t.Fatalf("helper did not exit gracefully: %v; output=%q; stderr=%q", waitErr, output, helperStderr)
	}
	if rendered := string(output); !strings.Contains(rendered, "\x1b[?1049h") || !strings.Contains(rendered, "\x1b[?1049l") {
		t.Fatalf("alternate screen was not entered and restored: %q", rendered)
	}
}
