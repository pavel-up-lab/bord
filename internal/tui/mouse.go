package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

const mouseWheelStep = 3

// updateMouse deliberately uses the same model operations as the keyboard.
// Bubble Tea reports zero-based cell coordinates, which map directly onto the
// exact-width lines emitted by View.
func (m Model) updateMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.width <= 0 || m.height <= 0 || msg.X < 0 || msg.Y < 0 || msg.X >= m.width || msg.Y >= m.height {
		return m, nil
	}
	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		delta := -mouseWheelStep
		if msg.Button == tea.MouseButtonWheelDown {
			delta = mouseWheelStep
		}
		return m.wheelAt(msg.X, msg.Y, delta)
	}
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil
	}
	if msg.Y == 0 {
		return m.activateTop(msg.X)
	}
	if msg.Y == m.height-1 {
		return m.activateCommand(msg.X)
	}
	if m.screen == ComposerScreen {
		if msg.Y > 1 && msg.Y < m.height-3 {
			m.editor.Focus()
		}
		return m, nil
	}
	if m.screen == HelpScreen {
		return m, nil
	}
	return m.clickWorkspace(msg.X, msg.Y)
}

func (m Model) activateTop(x int) (tea.Model, tea.Cmd) {
	offset := textWidth(" BORD  ")
	if m.canGoBack() {
		label := "[← Назад] "
		if x >= offset && x < offset+textWidth(label) {
			return m.updateKey(tea.KeyMsg{Type: tea.KeyEsc})
		}
		offset += textWidth(label)
	}
	for _, item := range []struct {
		label string
		key   tea.KeyMsg
	}{
		{"[Тема] ", tea.KeyMsg{Type: tea.KeyF2}},
		{"[Справка]", tea.KeyMsg{Type: tea.KeyF1}},
	} {
		if x >= offset && x < minInt(m.width, offset+textWidth(item.label)) {
			return m.updateKey(item.key)
		}
		offset += textWidth(item.label)
	}
	return m, nil
}

func (m Model) canGoBack() bool {
	return m.screen == ThreadScreen || m.screen == PostScreen || m.screen == AttachmentScreen || m.screen == ComposerScreen || m.screen == HelpScreen || (m.isNarrow() && m.focus == FocusContent)
}

func (m Model) activateCommand(x int) (tea.Model, tea.Cmd) {
	labels := m.commandLabels()
	keys := m.commandKeys()
	compact := m.compactCommandBar(m.width)
	offset := 0
	for i, label := range labels {
		width := commandLabelWidth(label, compact)
		if x >= offset && x < minInt(m.width, offset+width) {
			return m.updateKey(keys[i])
		}
		offset += width
		if i < len(labels)-1 {
			offset++ // one background-colored separator cell
		}
	}
	return m, nil
}

// commandLabelWidth mirrors commandBar exactly. The styled form renders the
// key without padding and " action " with one trailing cell; the compact form
// renders the source label as-is.
func commandLabelWidth(label string, compact bool) int {
	width := textWidth(label)
	if !compact && strings.Contains(label, " ") {
		width++
	}
	return width
}

func (m Model) commandKeys() []tea.KeyMsg {
	if m.screen == ComposerScreen {
		return []tea.KeyMsg{{Type: tea.KeyCtrlS}, {Type: tea.KeyCtrlO}, {Type: tea.KeyEsc}}
	}
	if m.screen == HelpScreen {
		return []tea.KeyMsg{{Type: tea.KeyEsc}, {Type: tea.KeyF2}, {Type: tea.KeyF10}}
	}
	if m.screen == AttachmentScreen {
		return []tea.KeyMsg{{Type: tea.KeyEnter}, {Type: tea.KeyEsc}, {Type: tea.KeyF10}}
	}
	if m.screen == ThreadScreen {
		return []tea.KeyMsg{{Type: tea.KeyF1}, {Type: tea.KeyF5}, {Type: tea.KeyF7}, {Type: tea.KeyF8}, {Type: tea.KeyF10}}
	}
	if m.screen == PostScreen {
		return []tea.KeyMsg{{Type: tea.KeyEsc}, {Type: tea.KeyF7}, {Type: tea.KeyF8}, {Type: tea.KeyF10}}
	}
	return []tea.KeyMsg{{Type: tea.KeyF1}, {Type: tea.KeyF2}, {Type: tea.KeyF4}, {Type: tea.KeyF5}, {Type: tea.KeyF7}, {Type: tea.KeyF10}}
}

func (m Model) panelAt(x, y int) (Focus, bool) {
	workspaceHeight := max(0, m.height-3)
	if y < 1 || y >= 1+workspaceHeight {
		return m.focus, false
	}
	if m.isNarrow() {
		return m.focus, true
	}
	leftWidth := m.leftPanelWidth()
	if x < leftWidth {
		return FocusBoards, true
	}
	return FocusContent, true
}

func (m Model) leftPanelWidth() int {
	leftWidth := max(24, m.width/3)
	if leftWidth > m.width-28 {
		leftWidth = m.width / 2
	}
	return leftWidth
}

func (m Model) clickWorkspace(x, y int) (tea.Model, tea.Cmd) {
	panel, ok := m.panelAt(x, y)
	if !ok || y < 3 || y >= m.height-3 {
		return m, nil
	}
	localY := y - 3 // top bar + panel border + heading
	if panel == FocusBoards {
		if localY >= m.visibleRows() || !m.insidePanelCell(panel, x) {
			return m, nil
		}
		index := m.boardScroll + localY
		if index < 0 || index >= len(m.boards) {
			return m, nil
		}
		wasSelected := m.focus == FocusBoards && m.boardIndex == index
		m.focus, m.boardIndex, m.screen = FocusBoards, index, CatalogScreen
		if wasSelected {
			m.focus = FocusContent
			return m, m.loadThreads(false)
		}
		return m, nil
	}
	if m.screen == AttachmentScreen {
		m.focus = FocusContent
		return m, nil
	}
	if m.screen == PostScreen {
		return m, nil
	}
	if localY/2 >= m.visibleRows() || !m.insidePanelCell(panel, x) {
		return m, nil
	}
	index := m.contentScroll + localY/2
	length := len(m.threads)
	if m.screen == ThreadScreen {
		length = len(m.posts)
	}
	if index < 0 || index >= length {
		return m, nil
	}
	m.focus, m.contentIndex = FocusContent, index
	if m.screen == CatalogScreen {
		return m.openSelection()
	}
	if m.screen == ThreadScreen {
		m.screen, m.postScroll = PostScreen, 0
	}
	return m, nil
}

func (m Model) insidePanelCell(panel Focus, x int) bool {
	if m.isNarrow() {
		return x > 0 && x < m.width-1
	}
	left := m.leftPanelWidth()
	if panel == FocusBoards {
		return x > 0 && x < left-1
	}
	return x > left && x < m.width-1
}

func (m Model) wheelAt(x, y, delta int) (tea.Model, tea.Cmd) {
	panel, ok := m.panelAt(x, y)
	if !ok || m.screen == ComposerScreen || m.screen == HelpScreen {
		return m, nil
	}
	m.focus = panel
	if m.screen == PostScreen {
		m.scrollPost(delta)
		return m, nil
	}
	if panel == FocusBoards {
		m.move(delta)
		return m, nil
	}
	if m.screen == AttachmentScreen {
		return m, nil
	}
	m.move(delta)
	if delta > 0 && !m.loading {
		if m.screen == ThreadScreen && m.contentIndex == len(m.posts)-1 && m.nextPostCursor != nil {
			return m, m.loadPosts(true)
		}
		if m.screen == CatalogScreen && m.contentIndex == len(m.threads)-1 && m.nextThreadCursor != nil {
			return m, m.loadThreads(true)
		}
	}
	return m, nil
}
