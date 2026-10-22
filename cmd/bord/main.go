// Command bord is the terminal client entrypoint.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"bord/internal/clientapp"
	"bord/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "bord:", tui.SanitizeTerminalText(err.Error()))
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 1 && args[0] == "version" {
		fmt.Fprintf(stdout, "bord %s commit=%s built=%s\n", safeText(version), safeText(commit), safeText(buildDate))
		return nil
	}

	flags := flag.NewFlagSet("bord", flag.ContinueOnError)
	flags.SetOutput(safeTerminalWriter{stderr})
	server := flags.String("server", "", "адрес сервера")
	statePath := flags.String("config", "", "путь к локальному состоянию")
	inline := flags.String("inline", "", "inline-графика: auto, kitty или off")
	themeFlag := flags.String("theme", "", "тема: встроенное имя, MC skin или путь к .ini")
	timeout := flags.Duration("timeout", 15*time.Second, "таймаут HTTP")
	showVersion := flags.Bool("version", false, "показать версию")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		if commit == "unknown" && buildDate == "unknown" {
			fmt.Fprintln(stdout, safeText(version))
		} else {
			fmt.Fprintf(stdout, "bord %s commit=%s built=%s\n", safeText(version), safeText(commit), safeText(buildDate))
		}
		return nil
	}

	publicServer := *server
	if publicServer == "" {
		publicServer = os.Getenv("BORD_SERVER")
	}
	runtime, err := clientapp.Bootstrap(ctx, clientapp.Options{Server: publicServer, StatePath: *statePath, Inline: *inline, Theme: *themeFlag, Timeout: *timeout})
	if err != nil {
		return err
	}
	program := tea.NewProgram(runtime.Model, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(ctx), tea.WithOutput(stdout))
	final, err := program.Run()
	if err != nil {
		return fmt.Errorf("интерфейс завершился с внутренней ошибкой")
	}
	if m, ok := final.(tui.Model); ok {
		if err := runtime.Save(m); err != nil {
			return err
		}
	}
	return nil
}

type safeTerminalWriter struct{ io.Writer }

func safeText(value string) string { return tui.SanitizeTerminalText(value) }

func (w safeTerminalWriter) Write(p []byte) (int, error) {
	output := tui.SanitizeTerminalText(string(p))
	n, err := io.WriteString(w.Writer, output)
	if err != nil {
		return 0, err
	}
	if n != len(output) {
		return 0, io.ErrShortWrite
	}
	return len(p), nil
}
