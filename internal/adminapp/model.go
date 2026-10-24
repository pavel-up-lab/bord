// Package adminapp hosts the integrated administrative and ordinary user TUI.
package adminapp

import (
	"strings"

	"bord/internal/admintui"
	"bord/internal/terminalui"
	"bord/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

type Mode uint8

const (
	AdminMode Mode = iota
	UserMode
)

type taggedMsg struct {
	mode Mode
	msg  tea.Msg
}
type clearTerminalControlMsg struct{ generation uint64 }

type Model struct {
	admin                     admintui.Model
	user                      tui.Model
	mode                      Mode
	width                     int
	height                    int
	themes                    terminalui.ThemeCatalog
	terminalControl           string
	terminalControlGeneration uint64
}

func New(admin admintui.Model, user tui.Model, themes terminalui.ThemeCatalog, mode string) Model {
	active := AdminMode
	if mode == "user" {
		active = UserMode
	}
	return Model{admin: admin, user: user, mode: active, themes: themes}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(tag(AdminMode, m.admin.Init()), tag(UserMode, m.user.Init()))
}

func tag(mode Mode, command tea.Cmd) tea.Cmd {
	if command == nil {
		return nil
	}
	return func() tea.Msg {
		message := command()
		if batch, ok := message.(tea.BatchMsg); ok {
			wrapped := make(tea.BatchMsg, 0, len(batch))
			for _, child := range batch {
				wrapped = append(wrapped, tag(mode, child))
			}
			return wrapped
		}
		return taggedMsg{mode: mode, msg: message}
	}
}

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, msg.Width), max(2, msg.Height)
		childSize := tea.WindowSizeMsg{Width: m.width, Height: max(1, m.height-1)}
		admin, adminCmd := m.admin.Update(childSize)
		user, userCmd := m.user.Update(childSize)
		m.admin, m.user = admin.(admintui.Model), user.(tui.Model)
		return m, tea.Batch(tag(AdminMode, adminCmd), tag(UserMode, userCmd))
	case taggedMsg:
		if _, quit := msg.msg.(tea.QuitMsg); quit {
			m.Close()
			return m, tea.Quit
		}
		return m.updateChild(msg.mode, msg.msg)
	case clearTerminalControlMsg:
		if msg.generation == m.terminalControlGeneration {
			m.terminalControl = ""
		}
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "f3", "ctrl+left", "ctrl+right":
			return m, m.toggle()
		case "alt+a":
			return m, m.switchMode(AdminMode)
		case "alt+u":
			return m, m.switchMode(UserMode)
		}
	case tea.MouseMsg:
		if msg.Y == 0 && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			return m, m.clickMode(msg.X)
		}
		if msg.Y > 0 {
			msg.Y--
			return m.updateChild(m.mode, msg)
		}
	}
	return m.updateChild(m.mode, message)
}

func (m Model) updateChild(mode Mode, message tea.Msg) (tea.Model, tea.Cmd) {
	if mode == AdminMode {
		updated, command := m.admin.Update(message)
		m.admin = updated.(admintui.Model)
		m.user = m.user.WithThemes(m.themes, m.admin.Theme())
		return m, tag(AdminMode, command)
	}
	updated, command := m.user.Update(message)
	m.user = updated.(tui.Model)
	m.admin = m.admin.WithThemes(m.themes, terminalui.ThemeName(m.user.Theme()))
	m.admin = m.admin.WithImageMode(terminalui.InlineMode(m.user.ImageModeSetting()))
	return m, tag(UserMode, command)
}

func (m *Model) toggle() tea.Cmd {
	if m.mode == AdminMode {
		return m.switchMode(UserMode)
	}
	return m.switchMode(AdminMode)
}

func (m *Model) clickMode(x int) tea.Cmd {
	adminLabel, userLabel := " [Администрирование] ", " [Пользователь] "
	if m.width < 40 {
		adminLabel, userLabel = " [A Admin] ", " [U User] "
	}
	adminWidth := terminalui.TextWidth(adminLabel)
	if x < adminWidth {
		return m.switchMode(AdminMode)
	}
	if x < adminWidth+terminalui.TextWidth(userLabel) {
		return m.switchMode(UserMode)
	}
	return nil
}

func (m *Model) switchMode(target Mode) tea.Cmd {
	if target == m.mode {
		return nil
	}
	cleanup := ""
	if m.mode == AdminMode {
		m.admin, cleanup = m.admin.DetachTerminalImage()
	} else {
		m.user, cleanup = m.user.DetachTerminalImage()
	}
	m.mode = target
	var refresh tea.Cmd
	if target == AdminMode {
		m.admin, refresh = m.admin.RefreshTerminalImage()
	} else {
		m.user, refresh = m.user.RefreshTerminalImage()
	}
	var clear tea.Cmd
	if cleanup != "" {
		m.terminalControl = cleanup
		m.terminalControlGeneration++
		generation := m.terminalControlGeneration
		clear = func() tea.Msg { return clearTerminalControlMsg{generation: generation} }
	}
	return tea.Batch(clear, tag(target, refresh))
}

func (m Model) View() string {
	palette := m.themes.Palette(m.admin.Theme())
	admin, user := "[Администрирование]", "[Пользователь]"
	if m.width < 40 {
		admin, user = "[A Admin]", "[U User]"
	}
	if m.mode == AdminMode {
		admin = "▶ " + admin
	} else {
		user = "▶ " + user
	}
	bar := terminalui.TopBar(palette, " "+admin+"  "+user, "F3 режим ", max(1, m.width))
	if m.terminalControl != "" {
		bar = m.terminalControl + bar
	}
	body := m.admin.View()
	if m.mode == UserMode {
		body = m.user.View()
	}
	if m.height <= 1 {
		return bar
	}
	lines := strings.Split(body, "\n")
	if len(lines) > m.height-1 {
		lines = lines[:m.height-1]
	}
	return bar + "\n" + strings.Join(lines, "\n")
}

func (m Model) ModeSetting() string {
	if m.mode == UserMode {
		return "user"
	}
	return "admin"
}
func (m Model) ThemeSetting() string  { return m.admin.ThemeSetting() }
func (m Model) Admin() admintui.Model { return m.admin }
func (m Model) User() tui.Model       { return m.user }
func (m Model) Close()                { m.admin.Close(); m.user.Close() }
