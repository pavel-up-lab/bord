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
	"bord/internal/terminalui"
	"bord/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

var (
	version         = "dev"
	commit          = "unknown"
	buildDate       = "unknown"
	runProgram      = defaultRunProgram
	prepareTerminal = defaultPrepareTerminal
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
	inline := flags.String("inline", "", "изображения: auto, kitty, sixel, ansi, w3m или off")
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
	capabilities, imageSession := prepareTerminal(ctx, stdout)
	defer imageSession.Close() //nolint:errcheck -- terminal cleanup is best-effort on every exit path
	runtime, err := clientapp.Bootstrap(ctx, clientapp.Options{Server: publicServer, StatePath: *statePath, Inline: *inline, Theme: *themeFlag, Timeout: *timeout, Capabilities: capabilities, ImageSession: imageSession})
	if err != nil {
		return err
	}
	final, err := runProgram(ctx, runtime.Model, stdout)
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

func defaultPrepareTerminal(ctx context.Context, output io.Writer) (terminalui.TerminalCapabilities, *terminalui.ImageSession) {
	session := terminalui.NewImageSession(output)
	file, ok := output.(*os.File)
	if !ok {
		return terminalui.TerminalCapabilities{}, session
	}
	return terminalui.DetectTerminalCapabilities(ctx, os.Stdin, file), session
}

func defaultRunProgram(ctx context.Context, model tea.Model, output io.Writer) (tea.Model, error) {
	return tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(ctx), tea.WithOutput(output)).Run()
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
