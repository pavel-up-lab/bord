package admintui

import (
	"context"
	"errors"
	"image/color"

	"bord/internal/terminalui"

	tea "github.com/charmbracelet/bubbletea"
)

type ImageView struct {
	Lines                  []string
	Backend, Warning, Path string
	TrustedANSI            bool
	Control                string
}

type TerminalImageViewer struct {
	Cache        terminalui.ImageCache
	Renderer     terminalui.KittyRenderer
	Session      *terminalui.ImageSession
	Sixel        *terminalui.SixelRenderer
	OpenExternal func(context.Context, string) error
}

func (v TerminalImageViewer) Render(meta terminalui.ImageMetadata, body []byte, responseType string, columns, rows int) (ImageView, error) {
	prepared, err := terminalui.VerifyAndCacheImage(v.Cache, v.Renderer, meta, body, responseType)
	if err != nil {
		return ImageView{}, err
	}
	result := ImageView{Path: prepared.Path, Warning: prepared.Warning}
	selection := terminalui.SelectImageBackend(v.Renderer.Mode, v.Renderer.Capability)
	if selection.Warning != "" {
		result.Warning = selection.Warning
	}
	columns, rows = max(1, columns), max(1, rows)
	switch selection.Backend {
	case terminalui.BackendKitty:
		if v.Session == nil {
			selection.Backend = terminalui.BackendANSI
			result.Warning = "Kitty session недоступна; используется ANSI"
			break
		}
		data := prepared.Inline
		if len(data) == 0 {
			data, err = terminalui.PrepareKittyImage(prepared.Original, meta.MediaType)
		}
		if err == nil {
			handle := v.Session.Activate(terminalui.NativeKitty, meta.ID, columns, rows)
			var frame terminalui.KittyFrame
			frame, err = terminalui.RenderKittyFrame(data, handle.ID, columns, rows)
			if err == nil {
				result.Lines, result.Backend, result.TrustedANSI = frame.Lines, "Kitty placeholders", true
				if handle.Changed {
					result.Control = handle.Cleanup + frame.Control
				}
				return result, nil
			}
		}
		result.Warning = "Kitty-показ недоступен; используется ANSI"
		result.Control = v.Session.Clear()
		selection.Backend = terminalui.BackendANSI
	case terminalui.BackendSixel:
		if v.Session != nil {
			renderer := v.Sixel
			if renderer == nil {
				renderer = &terminalui.SixelRenderer{}
			}
			handle := v.Session.Activate(terminalui.NativeSixel, meta.ID, columns, rows)
			frame, renderErr := renderer.Render(prepared.Original, meta.ID, columns, rows, handle.Generation)
			if renderErr == nil && len(frame.Lines) > 0 {
				result.Lines, result.Backend, result.TrustedANSI = frame.Lines, "Sixel", true
				result.Lines[0] = frame.Control + result.Lines[0]
				result.Control = handle.Cleanup
				return result, nil
			}
			v.Session.Clear()
		}
		result.Warning = "Sixel-показ недоступен; используется ANSI"
		selection.Backend = terminalui.BackendANSI
	case terminalui.BackendW3M:
		result.Backend = "w3m (по клавише O)"
		return result, nil
	case terminalui.BackendOff:
		result.Backend = "Системный просмотрщик (Enter)"
		return result, nil
	}
	if selection.Backend == terminalui.BackendANSI {
		result.Lines, err = terminalui.RenderANSIImage(prepared.Original, columns, rows, color.RGBA{})
		if err != nil {
			return ImageView{}, err
		}
		result.TrustedANSI = true
		if v.Renderer.Mode == terminalui.InlineANSI {
			result.Backend = "ANSI"
		} else {
			result.Backend = "ANSI fallback"
		}
		return result, nil
	}
	return ImageView{}, errors.New("image renderer unavailable")
}

func (v TerminalImageViewer) Clear() string {
	if v.Session == nil {
		return ""
	}
	return v.Session.Clear()
}

func (v TerminalImageViewer) Open(ctx context.Context, path string, useW3M bool) tea.Cmd {
	if useW3M {
		command := &terminalui.W3MViewerCommand{Context: ctx, Path: path}
		return tea.Exec(command, func(err error) tea.Msg { return viewerClosedMsg{err: err} })
	}
	return func() tea.Msg {
		if v.OpenExternal == nil {
			return viewerClosedMsg{err: errors.New("external image viewer unavailable")}
		}
		return viewerClosedMsg{err: v.OpenExternal(ctx, path)}
	}
}
