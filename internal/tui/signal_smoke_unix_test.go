//go:build !windows

package tui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
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
		program := tea.NewProgram(signalSmokeModel{}, tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout), tea.WithAltScreen())
		if _, err := program.Run(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		return
	}

	command := exec.Command(os.Args[0], "-test.run=^TestTerminalSmokeSignalRestoresAlternateScreen$")
	command.Env = append(os.Environ(), "BORD_SIGNAL_SMOKE_HELPER=1")
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
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
	defer stdin.Close()

	ready := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(stderr).ReadString('\n')
		if err == nil && !strings.Contains(line, "BORD_SIGNAL_SMOKE_READY") {
			err = fmt.Errorf("unexpected helper readiness: %q", line)
		}
		ready <- err
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
	if err := command.Wait(); err != nil {
		t.Fatalf("helper did not exit gracefully: %v; output=%q", err, output)
	}
	if rendered := string(output); !strings.Contains(rendered, "\x1b[?1049h") || !strings.Contains(rendered, "\x1b[?1049l") {
		t.Fatalf("alternate screen was not entered and restored: %q", rendered)
	}
}
