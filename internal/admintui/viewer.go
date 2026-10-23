package admintui

import (
	"context"
	"io"

	"bord/internal/terminalui"

	tea "github.com/charmbracelet/bubbletea"
)

type TerminalImageViewer struct {
	Cache        terminalui.ImageCache
	Renderer     terminalui.KittyRenderer
	OpenExternal func(context.Context, string) error
}

func (v TerminalImageViewer) View(ctx context.Context, meta terminalui.ImageMetadata, body []byte, responseType string, columns, rows int) tea.Cmd {
	prepared, err := terminalui.VerifyAndCacheImage(v.Cache, v.Renderer, meta, body, responseType)
	if err != nil {
		return func() tea.Msg { return viewerClosedMsg{err: err} }
	}
	if len(prepared.Inline) > 0 {
		if v.Renderer.Mode == terminalui.InlineANSI {
			command := &terminalui.ANSIViewerCommand{Data: prepared.Inline, Columns: columns, Rows: rows}
			return tea.Exec(command, func(err error) tea.Msg { return viewerClosedMsg{err: err} })
		}
		command := &terminalui.KittyViewerCommand{Data: prepared.Inline, Columns: columns, Rows: rows}
		return tea.Exec(command, func(err error) tea.Msg { return viewerClosedMsg{err: err} })
	}
	if v.Renderer.Mode == terminalui.InlineW3M {
		command := &terminalui.W3MViewerCommand{Context: ctx, Path: prepared.Path}
		return tea.Exec(command, func(err error) tea.Msg { return viewerClosedMsg{err: err} })
	}
	if v.OpenExternal == nil {
		return func() tea.Msg { return viewerClosedMsg{err: errViewerUnavailable} }
	}
	command := &externalViewerCommand{ctx: ctx, path: prepared.Path, open: v.OpenExternal}
	return tea.Exec(command, func(err error) tea.Msg { return viewerClosedMsg{err: err} })
}

var errViewerUnavailable = &viewerError{"external image viewer unavailable"}

type viewerError struct{ message string }

func (e *viewerError) Error() string { return e.message }

type externalViewerCommand struct {
	ctx            context.Context
	path           string
	open           func(context.Context, string) error
	stdin          io.Reader
	stdout, stderr io.Writer
}

func (c *externalViewerCommand) SetStdin(reader io.Reader)  { c.stdin = reader }
func (c *externalViewerCommand) SetStdout(writer io.Writer) { c.stdout = writer }
func (c *externalViewerCommand) SetStderr(writer io.Writer) { c.stderr = writer }
func (c *externalViewerCommand) Run() error                 { return c.open(c.ctx, c.path) }
