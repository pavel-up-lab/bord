// Command bord is the terminal client entrypoint.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"bord/internal/client"
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
	themeFlag := flags.String("theme", "", "тема: classic, amber, green или mono")
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

	var store *client.Store
	var err error
	if *statePath != "" {
		store = client.NewStore(*statePath)
	} else {
		store, err = client.DefaultStore()
		if err != nil {
			return err
		}
	}
	state, err := store.Load()
	if err != nil {
		return err
	}
	if *server != "" {
		state.Server = *server
	} else if env := os.Getenv("BORD_SERVER"); env != "" {
		state.Server = env
	}
	if state.Server == "" {
		state.Server = "http://localhost:8080"
	}
	if *inline != "" {
		switch *inline {
		case "auto", "kitty", "off":
			state.InlineMode = *inline
		default:
			return fmt.Errorf("недопустимый --inline")
		}
	}
	if *themeFlag != "" {
		name, parseErr := tui.ParseTheme(*themeFlag)
		if parseErr != nil {
			return fmt.Errorf("недопустимый --theme: %w", parseErr)
		}
		state.Theme = string(name)
	}
	if state.Version == 0 {
		fresh, err := client.NewState(state.Server)
		if err != nil {
			return err
		}
		fresh.InlineMode = state.InlineMode
		fresh.Theme = state.Theme
		state = fresh
	}
	if state.Theme == "" {
		state.Theme = string(tui.ThemeClassic)
	}
	themeName, err := tui.ParseTheme(state.Theme)
	if err != nil {
		themeName = tui.ThemeClassic
	}
	api, err := client.New(state.Server, &http.Client{}, *timeout)
	if err != nil {
		return err
	}
	postingAPI, err := client.NewPostingAPI(api, store, &state)
	if err != nil {
		return err
	}
	model := tui.NewModel(ctx, postingAPI).WithLastBoard(state.LastBoard).WithTheme(themeName)
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(ctx), tea.WithOutput(stdout))
	final, err := program.Run()
	if err != nil {
		return fmt.Errorf("интерфейс завершился с внутренней ошибкой")
	}
	if m, ok := final.(tui.Model); ok {
		if board := m.LastBoard(); board != "" {
			state.LastBoard = board
		}
		state.Theme = string(m.Theme())
	}
	if err := store.Save(state); err != nil {
		return err
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
