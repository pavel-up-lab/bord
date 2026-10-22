// Command bord-admin is the private Bord moderation terminal client.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"bord/internal/adminapp"
	"bord/internal/adminclient"
	"bord/internal/admintui"
	"bord/internal/client"
	"bord/internal/clientapp"
	"bord/internal/terminalui"

	tea "github.com/charmbracelet/bubbletea"
)

var (
	version    = "dev"
	commit     = "unknown"
	buildDate  = "unknown"
	runProgram = defaultRunProgram
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "bord-admin:", terminalui.SanitizeTerminalText(err.Error()))
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 1 && args[0] == "version" {
		printVersion(stdout)
		return nil
	}
	flags := flag.NewFlagSet("bord-admin", flag.ContinueOnError)
	flags.SetOutput(safeWriter{stderr})
	management := flags.String("management", "", "адрес приватного management API")
	server := flags.String("server", "", "адрес публичного API для режима Пользователь")
	credentialFile := flags.String("credential-file", "", "путь к защищенному credential-файлу")
	themeFlag := flags.String("theme", "", "тема: встроенное имя, MC skin или путь к .ini")
	inline := flags.String("inline", "", "inline-графика: auto, kitty или off")
	timeout := flags.Duration("timeout", 15*time.Second, "таймаут HTTP")
	showVersion := flags.Bool("version", false, "показать версию")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("неожиданные позиционные аргументы")
	}
	if *showVersion {
		printVersion(stdout)
		return nil
	}
	if *timeout <= 0 {
		return errors.New("--timeout должен быть положительным")
	}
	store, err := adminclient.DefaultStateStore()
	if err != nil {
		return err
	}
	state, err := store.Load()
	if err != nil {
		return err
	}
	if *management != "" {
		state.Management = *management
	} else if value := os.Getenv("BORD_ADMIN_MANAGEMENT"); value != "" {
		state.Management = value
	}
	if state.Management == "" {
		state.Management = "http://127.0.0.1:8081"
	}
	if *server != "" {
		state.Server = *server
	} else if value := os.Getenv("BORD_SERVER"); value != "" {
		state.Server = value
	}
	if state.Server == "" {
		state.Server = "http://localhost:8080"
	}
	if *inline != "" {
		switch *inline {
		case "auto", "kitty", "off":
			state.InlineMode = *inline
		default:
			return errors.New("недопустимый --inline")
		}
	}
	if state.InlineMode == "" {
		state.InlineMode = "auto"
	}
	themes := terminalui.DiscoverThemes()
	if *themeFlag != "" {
		name, resolveErr := themes.Resolve(*themeFlag)
		if resolveErr != nil {
			return fmt.Errorf("недопустимый --theme: %w", resolveErr)
		}
		state.Theme = string(name)
	}
	if state.Theme == "" {
		state.Theme = string(terminalui.ThemeClassic)
	}
	themeName, err := themes.Resolve(state.Theme)
	if err != nil {
		themeName = terminalui.ThemeClassic
	}
	credential, err := adminclient.LoadCredential(*credentialFile)
	if err != nil {
		return err
	}
	api, err := adminclient.New(state.Management, credential, &http.Client{}, *timeout)
	if err != nil {
		return err
	}
	checkCtx, cancel := context.WithTimeout(ctx, *timeout)
	session, err := api.Session(checkCtx)
	cancel()
	if err != nil {
		var apiError *adminclient.Error
		if errors.As(err, &apiError) && apiError.StatusCode == http.StatusNotFound {
			return errors.New("management API несовместим: требуется /admin/v1/session")
		}
		return err
	}
	cache, err := client.DefaultCache()
	if err != nil {
		return err
	}
	viewer := admintui.TerminalImageViewer{Cache: cache, Renderer: terminalui.KittyRenderer{Capability: terminalui.EnvironmentCapabilities{}, Mode: terminalui.InlineMode(state.InlineMode)}, OpenExternal: client.OpenExternal}
	adminModel := admintui.NewModel(ctx, api).WithThemes(themes, themeName).WithScreen(state.View).WithSession(session).WithImageViewer(viewer)
	publicRuntime, err := clientapp.Bootstrap(ctx, clientapp.Options{Server: state.Server, Inline: state.InlineMode, Theme: state.Theme, Timeout: *timeout, Themes: themes})
	if err != nil {
		return err
	}
	model := adminapp.New(adminModel, publicRuntime.Model, themes, state.Mode)
	final, err := runProgram(ctx, model, stdout)
	if err != nil {
		return errors.New("интерфейс завершился с внутренней ошибкой")
	}
	if result, ok := final.(adminapp.Model); ok {
		state.Theme = result.ThemeSetting()
		state.View = result.Admin().ViewSetting()
		state.Mode = result.ModeSetting()
		if saveErr := publicRuntime.SaveUserData(result.User()); saveErr != nil {
			return saveErr
		}
	}
	state.InlineMode = string(terminalui.InlineMode(state.InlineMode))
	if err := store.Save(state); err != nil {
		return err
	}
	return nil
}

func defaultRunProgram(ctx context.Context, model tea.Model, output io.Writer) (tea.Model, error) {
	return tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(ctx), tea.WithOutput(output)).Run()
}

func printVersion(output io.Writer) {
	if commit == "unknown" && buildDate == "unknown" {
		fmt.Fprintln(output, terminalui.SanitizeTerminalText(version))
		return
	}
	fmt.Fprintf(output, "bord-admin %s commit=%s built=%s\n", terminalui.SanitizeTerminalText(version), terminalui.SanitizeTerminalText(commit), terminalui.SanitizeTerminalText(buildDate))
}

type safeWriter struct{ io.Writer }

func (w safeWriter) Write(data []byte) (int, error) {
	safe := terminalui.SanitizeTerminalText(string(data))
	written, err := io.WriteString(w.Writer, safe)
	if err != nil {
		return 0, err
	}
	if written != len(safe) {
		return 0, io.ErrShortWrite
	}
	return len(data), nil
}
