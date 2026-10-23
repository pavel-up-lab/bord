// Package clientapp composes the ordinary public Bord client for reuse by
// both command entrypoints. It never accepts management credentials.
package clientapp

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"bord/internal/client"
	"bord/internal/terminalui"
	"bord/internal/tui"
)

type Options struct {
	Server, StatePath, Inline, Theme string
	Timeout                          time.Duration
	HTTPClient                       *http.Client
	Themes                           terminalui.ThemeCatalog
}

type Runtime struct {
	Model       tui.Model
	State       client.State
	Store       *client.Store
	storedTheme string
}

func Bootstrap(ctx context.Context, options Options) (*Runtime, error) {
	store, err := publicStore(options.StatePath)
	if err != nil {
		return nil, err
	}
	state, err := store.Load()
	if err != nil {
		return nil, err
	}
	storedTheme := state.Theme
	if options.Server != "" {
		state.Server = options.Server
	}
	if state.Server == "" {
		state.Server = "http://localhost:8080"
	}
	if options.Inline != "" {
		if !terminalui.ValidInlineMode(terminalui.InlineMode(options.Inline)) {
			return nil, fmt.Errorf("недопустимый --inline")
		}
		state.InlineMode = options.Inline
	}
	themes := options.Themes
	if len(themes.Options()) == 0 {
		themes = terminalui.DiscoverThemes()
	}
	if options.Theme != "" {
		name, resolveErr := themes.Resolve(options.Theme)
		if resolveErr != nil {
			return nil, fmt.Errorf("недопустимый --theme: %w", resolveErr)
		}
		state.Theme = string(name)
	}
	if state.Version == 0 {
		fresh, createErr := client.NewState(state.Server)
		if createErr != nil {
			return nil, createErr
		}
		fresh.InlineMode, fresh.Theme = state.InlineMode, state.Theme
		state = fresh
	}
	if state.Theme == "" {
		state.Theme = string(terminalui.ThemeClassic)
	}
	if !terminalui.ValidInlineMode(terminalui.InlineMode(state.InlineMode)) {
		state.InlineMode = "auto"
	}
	theme, err := themes.Resolve(state.Theme)
	if err != nil {
		theme = terminalui.ThemeClassic
	}
	api, err := client.New(state.Server, options.HTTPClient, options.Timeout)
	if err != nil {
		return nil, err
	}
	posting, err := client.NewPostingAPI(api, store, &state)
	if err != nil {
		return nil, err
	}
	cache, err := client.DefaultCache()
	if err != nil {
		return nil, err
	}
	model := tui.NewModel(ctx, posting).WithLastBoard(state.LastBoard).WithThemes(themes, theme).
		WithAttachmentViewer(tui.InlineMode(state.InlineMode), cache)
	return &Runtime{Model: model, State: state, Store: store, storedTheme: storedTheme}, nil
}

// SaveUserData persists board/session/delete capabilities without moving
// integrated presentation ownership out of admin.json.
func (r *Runtime) SaveUserData(model tui.Model) error {
	if board := model.LastBoard(); board != "" {
		r.State.LastBoard = board
	}
	r.State.Theme = r.storedTheme
	return r.Store.Save(r.State)
}

func (r *Runtime) Save(model tui.Model) error {
	if board := model.LastBoard(); board != "" {
		r.State.LastBoard = board
	}
	// The ordinary store owns user capabilities only. Integrated admin mode
	// persists presentation in admin state, so callers may overwrite Theme.
	r.State.Theme = model.ThemeSetting()
	r.State.InlineMode = model.ImageModeSetting()
	return r.Store.Save(r.State)
}

func publicStore(path string) (*client.Store, error) {
	if path != "" {
		return client.NewStore(path), nil
	}
	return client.DefaultStore()
}
