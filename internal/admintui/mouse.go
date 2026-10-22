package admintui

import (
	"strings"

	"bord/internal/terminalui"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) updateMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	zone := terminalui.MouseZoneAt(m.width, m.height, msg.X, msg.Y)
	if zone == terminalui.MouseOutside {
		return m, nil
	}
	if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
		if zone == terminalui.MouseTopBar {
			return m.activateTop(msg.X)
		}
		if zone == terminalui.MouseCommandBar {
			return m.activateCommand(msg.X)
		}
		if zone == terminalui.MouseWorkspace && m.modal == nil {
			return m.activateWorkspace(msg.X, msg.Y)
		}
	}
	if zone == terminalui.MouseWorkspace && (msg.Button == tea.MouseButtonWheelDown || msg.Button == tea.MouseButtonWheelUp) {
		delta := 1
		if msg.Button == tea.MouseButtonWheelUp {
			delta = -1
		}
		m.moveSelection(delta)
		return m, nil
	}
	return m, nil
}

func (m Model) activateTop(x int) (tea.Model, tea.Cmd) {
	labels := []string{" BORD ADMIN  ", "[1 Жалобы]", "[2 Блокировки]", "[3 Аудит]", "[F2 Тема]", "[F1 Справка]"}
	offset := 0
	for index, label := range labels {
		width := terminalui.TextWidth(label) + 1
		if x >= offset && x < offset+width {
			switch index {
			case 1:
				return m.selectScreen(ReportsScreen)
			case 2:
				return m.selectScreen(BansScreen)
			case 3:
				return m.selectScreen(AuditScreen)
			case 4:
				m.themeMenu = true
				return m, nil
			case 5:
				m.switchScreen(HelpScreen)
				return m, nil
			}
		}
		offset += width
	}
	return m, nil
}

func (m Model) activateCommand(x int) (tea.Model, tea.Cmd) {
	labels := m.commandLabels()
	index, ok := terminalui.CommandIndexAt(labels, m.width, x)
	if !ok {
		return m, nil
	}
	keys := make([]string, len(labels))
	for i, label := range labels {
		keys[i] = strings.ToLower(strings.Fields(label)[0])
	}
	key := keys[index]
	switch key {
	case "help":
		key = "f1"
	case "theme":
		key = "f2"
	case "refresh":
		key = "f5"
	case "hide":
		key = "h"
	case "restore":
		key = "u"
	case "close":
		key = "c"
	case "ban":
		key = "b"
	case "image":
		key = "a"
	case "reveal":
		key = "i"
	case "revoke":
		key = "x"
	case "active/history":
		key = "f6"
	case "quit":
		key = "f10"
	case "back", "отмена":
		key = "esc"
	case "далее":
		key = "ctrl+s"
	}
	if strings.HasPrefix(key, "ctrl+") {
		if key == "ctrl+s" {
			return m.updateKey(tea.KeyMsg{Type: tea.KeyCtrlS})
		}
	}
	if strings.HasPrefix(key, "f") {
		var functionKeys = map[string]tea.KeyType{"f1": tea.KeyF1, "f2": tea.KeyF2, "f5": tea.KeyF5, "f6": tea.KeyF6, "f7": tea.KeyF7, "f10": tea.KeyF10}
		return m.updateKey(tea.KeyMsg{Type: functionKeys[key]})
	}
	if key == "esc" {
		return m.updateKey(tea.KeyMsg{Type: tea.KeyEsc})
	}
	return m.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
}

func (m Model) activateWorkspace(x, y int) (tea.Model, tea.Cmd) {
	row := y - 2
	if row < 0 {
		return m, nil
	}
	if m.screen == ReportsScreen {
		if m.width >= narrowWidth && x >= m.width/3 {
			m.focus = ContextFocus
			m.postIndex = clamp(row-1, 0, max(0, len(m.context.Posts)-1))
			return m, nil
		}
		if m.width < narrowWidth && m.focus == ContextFocus {
			m.postIndex = clamp(row-1, 0, max(0, len(m.context.Posts)-1))
			return m, nil
		}
		m.focus = ListFocus
		m.reportIndex = clamp(row, 0, max(0, len(m.reports)-1))
		return m.startContextLoad(false)
	}
	if m.screen == BansScreen {
		m.banIndex = clamp(row, 0, max(0, len(m.bans)-1))
	}
	if m.screen == AuditScreen {
		m.auditIndex = clamp(row, 0, max(0, len(m.audit)-1))
	}
	return m, nil
}
