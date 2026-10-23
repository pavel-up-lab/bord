package tui

import (
	"strings"

	"bord/api/generated"
	"bord/internal/terminalui"

	tea "github.com/charmbracelet/bubbletea"
)

const mouseWheelStep = 1

// updateMouse deliberately uses the same model operations as the keyboard.
// Bubble Tea reports zero-based cell coordinates, which map directly onto the
// exact-width lines emitted by View.
func (m Model) updateMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	zone := terminalui.MouseZoneAt(m.width, m.height, msg.X, msg.Y)
	if zone == terminalui.MouseOutside {
		return m, nil
	}
	if m.themePicker.IsOpen() {
		return m.activateThemeMenuMouse(msg)
	}
	if m.settingsOpen || m.imageSettings != nil {
		return m.updateSettingsMouse(msg)
	}
	if m.report != nil {
		return m.updateReportMouse(msg)
	}
	if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
		if zone == terminalui.MouseTopBar {
			return m.activateTop(msg.X)
		}
		if zone == terminalui.MouseCommandBar {
			return m.activateCommand(msg.X)
		}
	}
	if m.screen == ComposerScreen && m.draft.ReplyTo != nil {
		if updated, cmd, handled := m.selectReplyPostMouse(msg); handled {
			return updated, cmd
		}
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
	if m.screen == PostScreen {
		label := "[Пожаловаться] "
		if x >= offset && x < minInt(m.width, offset+textWidth(label)) {
			m.openReportForm()
			return m, nil
		}
		offset += textWidth(label)
	}
	for _, item := range []struct {
		label string
		key   tea.KeyMsg
	}{
		{m.themeControlLabel(), tea.KeyMsg{Type: tea.KeyF2}},
		{"[Настройки ▾] ", tea.KeyMsg{Type: tea.KeyF6}},
		{"[Справка]", tea.KeyMsg{Type: tea.KeyF1}},
	} {
		if x >= offset && x < minInt(m.width, offset+textWidth(item.label)) {
			return m.updateKey(item.key)
		}
		offset += textWidth(item.label)
	}
	return m, nil
}

func (m Model) activateThemeMenuMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		delta := -1
		if msg.Button == tea.MouseButtonWheelDown {
			delta = 1
		}
		m.themePicker.HandleWheel(delta, m.themes, m.height)
		return m, nil
	}
	if msg.Button != tea.MouseButtonLeft {
		return m, nil
	}
	if msg.Y == 0 {
		m.themePicker.Close()
		return m.activateTop(msg.X)
	}
	if selected, ok := m.themePicker.HandleClick(msg.X, msg.Y, m.themes, m.width, m.height, textWidth(" BORD  ")); ok {
		m.theme = selected
	}
	return m, nil
}

func (m Model) canGoBack() bool {
	return m.screen == ThreadScreen || m.screen == PostScreen || m.screen == AttachmentScreen || m.screen == ComposerScreen || m.screen == HelpScreen || (m.isNarrow() && m.focus == FocusContent)
}

func (m Model) activateCommand(x int) (tea.Model, tea.Cmd) {
	labels := m.commandLabels()
	keys := m.commandKeys()
	if len(labels) != len(keys) {
		// A malformed command bar must never turn one visible action into a
		// different (especially destructive) action.
		return m, nil
	}
	index, ok := terminalui.CommandIndexAt(labels, m.width, x)
	if ok {
		return m.updateKey(keys[index])
	}
	return m, nil
}

// commandLabelWidth mirrors commandBar exactly. The styled form renders the
// key without padding and " action " with one trailing cell; the compact form
// renders the source label as-is.
func commandLabelWidth(label string, compact bool) int {
	return terminalui.CommandLabelWidth(label, compact)
}

func (m Model) commandKeys() []tea.KeyMsg {
	if m.screen == ComposerScreen {
		if m.draft.ReplyTo != nil && m.replyPane == replySourcePane {
			return []tea.KeyMsg{{Type: tea.KeyCtrlC}, {Type: tea.KeyTab}, {Type: tea.KeyEsc}}
		}
		if m.draft.ReplyTo != nil {
			return []tea.KeyMsg{{Type: tea.KeyCtrlS}, {Type: tea.KeyCtrlO}, {Type: tea.KeyTab}, {Type: tea.KeyEsc}}
		}
		return []tea.KeyMsg{{Type: tea.KeyCtrlS}, {Type: tea.KeyCtrlO}, {Type: tea.KeyEsc}}
	}
	if m.screen == HelpScreen {
		return []tea.KeyMsg{{Type: tea.KeyEsc}, {Type: tea.KeyF2}, {Type: tea.KeyF10}}
	}
	if m.screen == AttachmentScreen {
		return []tea.KeyMsg{{Type: tea.KeyEnter}, {Type: tea.KeyEsc}, {Type: tea.KeyF10}}
	}
	if m.screen == ThreadScreen {
		return []tea.KeyMsg{{Type: tea.KeyF1}, {Type: tea.KeyF5}, {Type: tea.KeyF7}, {Type: tea.KeyF8}, {Type: tea.KeyRunes, Runes: []rune{'d'}}, {Type: tea.KeyF10}}
	}
	if m.screen == PostScreen {
		return []tea.KeyMsg{{Type: tea.KeyEsc}, {Type: tea.KeyRunes, Runes: []rune{'o'}}, {Type: tea.KeyF7}, {Type: tea.KeyF8}, {Type: tea.KeyRunes, Runes: []rune{'d'}}, {Type: tea.KeyRunes, Runes: []rune{'R'}}, {Type: tea.KeyF10}}
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
	left, _ := terminalui.SplitWidth(m.width, 24, 28)
	return left
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
		m.focus, m.boardIndex, m.screen = FocusContent, index, CatalogScreen
		m.activeThread, m.posts = "", nil
		return m, m.loadThreads(false)
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
	if m.screen == ThreadScreen && localY%2 == 0 {
		post := m.posts[index]
		if post.ReplyToId != nil && m.clicksReplyTarget(x, post, index) {
			if target := indexPost(m.posts, *post.ReplyToId); target >= 0 {
				m.focus, m.contentIndex = FocusContent, target
				m.ensurePostVisible(target)
			}
			return m, nil
		}
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

func (m Model) clicksReplyTarget(x int, post generated.Post, index int) bool {
	if post.ReplyToId == nil {
		return false
	}
	panelStart := 0
	if !m.isNarrow() {
		panelStart = m.leftPanelWidth()
	}
	meta := postMeta(post, index == m.contentIndex)
	target := ">>" + SanitizeTerminalText(*post.ReplyToId)
	byteOffset := strings.LastIndex(meta, target)
	if byteOffset < 0 {
		return false
	}
	start := panelStart + 2 + textWidth(meta[:byteOffset]) // border + row padding
	return x >= start && x < start+textWidth(target)
}

func (m *Model) ensurePostVisible(index int) {
	visible := max(1, m.visibleRows()/2)
	if index < m.contentScroll {
		m.contentScroll = index
	} else if index >= m.contentScroll+visible {
		m.contentScroll = index - visible + 1
	}
}

func indexPost(posts []generated.Post, id string) int {
	for i := range posts {
		if posts[i].Id == id {
			return i
		}
	}
	return -1
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
