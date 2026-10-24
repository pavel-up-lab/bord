package tui

import (
	"strings"

	"bord/internal/terminalui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type imageSettingsModal struct {
	index int
}

var imageModeOptions = []struct {
	mode        InlineMode
	label, help string
}{
	{InlineAuto, "Auto", "Kitty → Sixel → ANSI по подтверждённым возможностям"},
	{InlineKitty, "Kitty", "Unicode placeholders; без поддержки используется ANSI"},
	{InlineSixel, "Sixel", "Windows Terminal и совместимые; иначе ANSI"},
	{InlineANSI, "ANSI", "Цветные текстовые ячейки без графического протокола"},
	{InlineW3M, "w3m", "Только явное открытие; программа должна быть установлена"},
	{InlineOff, "System", "Только явное открытие системным приложением"},
}

func (m *Model) openSettingsMenu() {
	m.themePicker.Close()
	m.settingsOpen = true
}

func (m *Model) openImageSettings() {
	m.settingsOpen = false
	modal := &imageSettingsModal{}
	for index, option := range imageModeOptions {
		if option.mode == m.imageMode {
			modal.index = index
			break
		}
	}
	m.imageSettings = modal
}

func (m Model) updateSettingsMenuKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "s", "f6":
		m.settingsOpen = false
	case "enter":
		m.openImageSettings()
	}
	return m, nil
}

func (m Model) updateImageSettingsKey(key string) (tea.Model, tea.Cmd) {
	if m.imageSettings == nil {
		return m, nil
	}
	switch key {
	case "esc":
		m.imageSettings = nil
	case "up", "k", "shift+tab":
		m.imageSettings.index = (m.imageSettings.index - 1 + len(imageModeOptions)) % len(imageModeOptions)
	case "down", "j", "tab":
		m.imageSettings.index = (m.imageSettings.index + 1) % len(imageModeOptions)
	case "enter":
		cmd := m.setImageMode(imageModeOptions[m.imageSettings.index].mode)
		m.imageSettings = nil
		m.notice = "Режим изображений: " + terminalui.InlineModeLabel(m.imageMode)
		return m, cmd
	}
	return m, nil
}

func (m *Model) setImageMode(mode InlineMode) tea.Cmd {
	if !terminalui.ValidInlineMode(mode) {
		return nil
	}
	changed := m.imageMode != mode
	m.imageMode = mode
	m.attachmentRenderer.Mode = mode
	if !changed || m.screen != AttachmentScreen {
		return nil
	}
	backend := terminalui.SelectImageBackend(mode, m.attachmentRenderer.Capability).Backend
	if backend == terminalui.BackendANSI || backend == terminalui.BackendW3M || backend == terminalui.BackendOff {
		if m.imageSession != nil {
			m.queueTerminalControl(m.imageSession.Clear())
		}
	}
	return m.renderAttachmentFrame()
}

func (m Model) settingsAnchor() int {
	return m.themeAnchor() + textWidth(m.themeControlLabel())
}

func (m Model) settingsMenuGeometry() (x, width int) {
	x = minInt(max(0, m.settingsAnchor()), max(0, m.width-1))
	width = minInt(22, max(1, m.width-x))
	return x, width
}

func (m Model) overlaySettingsMenu(lines []string, colors theme) []string {
	if !m.settingsOpen || len(lines) < 2 {
		return lines
	}
	x, width := m.settingsMenuGeometry()
	menu := []string{
		terminalui.Paint(colors.BarBackground, colors.BarForeground, "┌"+strings.Repeat("─", max(0, width-2))+"┐", width),
		terminalui.Paint(colors.SelectionBackground, colors.Selection, "│> Изображения", width-1) + terminalui.Paint(colors.BarBackground, colors.BarForeground, "│", 1),
		terminalui.Paint(colors.BarBackground, colors.BarForeground, "└"+strings.Repeat("─", max(0, width-2))+"┘", width),
	}
	for index, overlay := range menu {
		y := index + 1
		if y >= len(lines) {
			break
		}
		lines[y] = ansi.Cut(lines[y], 0, x) + overlay + ansi.Cut(lines[y], x+width, m.width)
	}
	return lines
}

func (m Model) imageSettingsGeometry() (x, y, width, height int) {
	width = minInt(max(1, m.width), minInt(72, max(28, m.width-4)))
	height = minInt(max(1, m.height), len(imageModeOptions)+5)
	x, y = max(0, (m.width-width)/2), max(0, (m.height-height)/2)
	return
}

func (m Model) overlayImageSettings(lines []string, colors theme) []string {
	if m.imageSettings == nil || len(lines) == 0 {
		return lines
	}
	x, y, width, height := m.imageSettingsGeometry()
	rows := make([]terminalui.Row, 0, len(imageModeOptions)+2)
	for index, option := range imageModeOptions {
		marker := "  "
		if index == m.imageSettings.index {
			marker = "> "
		}
		rows = append(rows, terminalui.Row{Lines: []string{marker + option.label + " — " + option.help}, Active: index == m.imageSettings.index})
	}
	rows = append(rows, terminalui.Row{Lines: []string{"", "Enter — выбрать • Esc — отменить"}})
	panel := terminalui.Panel(colors, width, height, "Настройки изображений", "ТИП ПОКАЗА", rows, true)
	for index, overlay := range panel {
		row := y + index
		if row >= len(lines) {
			break
		}
		lines[row] = ansi.Cut(lines[row], 0, x) + overlay + ansi.Cut(lines[row], x+width, m.width)
	}
	return lines
}

func (m Model) updateSettingsMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil
	}
	if m.imageSettings != nil {
		x, y, width, _ := m.imageSettingsGeometry()
		if msg.X < x || msg.X >= x+width || msg.Y < y+2 || msg.Y >= y+2+len(imageModeOptions) {
			return m, nil
		}
		m.imageSettings.index = msg.Y - (y + 2)
		cmd := m.setImageMode(imageModeOptions[m.imageSettings.index].mode)
		m.imageSettings = nil
		m.notice = "Режим изображений: " + terminalui.InlineModeLabel(m.imageMode)
		return m, cmd
	}
	if m.settingsOpen {
		x, width := m.settingsMenuGeometry()
		if msg.X >= x && msg.X < x+width && msg.Y == 2 {
			m.openImageSettings()
		} else {
			m.settingsOpen = false
		}
	}
	return m, nil
}
