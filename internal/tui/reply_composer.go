package tui

import (
	"strings"
	"time"

	"bord/api/generated"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type replyTextLine struct {
	start, end int
	text       string
}

func (m *Model) configureEditorWidth() {
	width := m.width
	if m.draft.ReplyTo != nil && !m.isNarrow() {
		width = m.replyEditorPanelWidth()
	}
	m.editor.SetWidth(max(1, width-6))
}

func (m Model) replyEditorPanelWidth() int {
	return max(28, m.width/2)
}

func (m Model) replySourcePanelGeometry() (int, int) {
	if m.isNarrow() {
		return 0, m.width
	}
	left := m.replyEditorPanelWidth()
	return left, max(1, m.width-left)
}

func (m *Model) toggleReplyPane() {
	m.clipboardCopied = false
	if m.replyPane == replyEditorPane {
		m.replyPane = replySourcePane
		m.editor.Blur()
		m.ensureReplyCursorVisible()
		return
	}
	m.replyPane = replyEditorPane
	m.editor.Focus()
}

func (m Model) replyComposerWorkspace(t theme, width, height int) []string {
	post, _ := m.replySourcePost()
	sourceTitle := "Исходный пост"
	sourceHint := "SHIFT+СТРЕЛКИ ВЫДЕЛИТЬ • CTRL+C КОПИРОВАТЬ • TAB ОТВЕТ"
	if post.Id != "" {
		sourceTitle = "Пост >>" + SanitizeTerminalText(post.Id)
		sourceHint = SanitizeTerminalText(post.AuthorId) + " • " + post.CreatedAt.Format("2006-01-02 15:04")
	}
	if m.isNarrow() {
		if m.replyPane == replySourcePane {
			return m.singlePanel(t, width, height, sourceTitle, sourceHint, m.replySourceRows(width, t), true)
		}
		return m.singlePanel(t, width, height, m.composerTitle(), "ТЕКСТ ОТВЕТА • TAB ИСХОДНЫЙ ПОСТ", m.composerLines(), true)
	}
	leftWidth := m.replyEditorPanelWidth()
	left := m.singlePanel(t, leftWidth, height, m.composerTitle(), "ТЕКСТ ОТВЕТА • TAB ПАНЕЛИ", m.composerLines(), m.replyPane == replyEditorPane)
	right := m.singlePanel(t, width-leftWidth, height, sourceTitle, sourceHint, m.replySourceRows(width-leftWidth, t), m.replyPane == replySourcePane)
	joined := make([]string, height)
	for i := range joined {
		joined[i] = left[i] + right[i]
	}
	return joined
}

func (m Model) replySourcePost() (generated.Post, bool) {
	if m.draft.ReplyTo == nil {
		return generated.Post{}, false
	}
	for i := range m.posts {
		if m.posts[i].Id == *m.draft.ReplyTo {
			return m.posts[i], true
		}
	}
	return generated.Post{}, false
}

func (m Model) replySourceText() string {
	post, ok := m.replySourcePost()
	if !ok {
		return "Пост не найден"
	}
	if post.State != generated.Visible || post.Body == nil {
		return "[" + SanitizeTerminalText(string(post.State)) + "]"
	}
	text := sanitizeMultilineTerminalText(*post.Body)
	if strings.TrimSpace(text) == "" {
		return "(пусто)"
	}
	return text
}

func (m Model) replySourceLines(panelWidth int) []replyTextLine {
	width := max(1, panelWidth-3)
	runes := []rune(m.replySourceText())
	lines := make([]replyTextLine, 0, max(1, len(runes)/width))
	start, used := 0, 0
	flush := func(end int) {
		lines = append(lines, replyTextLine{start: start, end: end, text: string(runes[start:end])})
	}
	for i, r := range runes {
		if r == '\n' {
			flush(i)
			start, used = i+1, 0
			continue
		}
		runeWidth := max(1, lipgloss.Width(string(r)))
		if used > 0 && used+runeWidth > width {
			flush(i)
			start, used = i, 0
		}
		used += runeWidth
	}
	flush(len(runes))
	if len(lines) == 0 {
		return []replyTextLine{{}}
	}
	return lines
}

func (m Model) replySourceRows(panelWidth int, t theme) []displayRow {
	lines := m.replySourceLines(panelWidth)
	start := clamp(m.replyPostScroll, 0, max(0, len(lines)-1))
	visible := max(1, m.height-6)
	end := minInt(len(lines), start+visible)
	rendered := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		rendered = append(rendered, m.renderReplySourceLine(lines[i], t))
	}
	return []displayRow{{lines: rendered, trustedANSI: true}}
}

func (m Model) renderReplySourceLine(line replyTextLine, t theme) string {
	runes := []rune(m.replySourceText())
	selectionStart, selectionEnd := m.replySelection()
	var b strings.Builder
	selectionStyle := lipgloss.NewStyle().Background(t.accent).Foreground(t.selection)
	cursorStyle := lipgloss.NewStyle().Background(t.muted).Foreground(t.background)
	for i := line.start; i < line.end && i < len(runes); i++ {
		value := string(runes[i])
		if selectionStart < selectionEnd && i >= selectionStart && i < selectionEnd {
			b.WriteString(selectionStyle.Render(value))
		} else if m.replyPane == replySourcePane && selectionStart == selectionEnd && i == m.replyPostCursor {
			b.WriteString(cursorStyle.Render(value))
		} else {
			b.WriteString(value)
		}
	}
	if m.replyPane == replySourcePane && selectionStart == selectionEnd && m.replyPostCursor == line.end {
		b.WriteString(cursorStyle.Render(" "))
	}
	return b.String()
}

func (m Model) replySelection() (int, int) {
	if m.replyPostAnchor < 0 {
		return m.replyPostCursor, m.replyPostCursor
	}
	if m.replyPostAnchor <= m.replyPostCursor {
		return m.replyPostAnchor, m.replyPostCursor
	}
	return m.replyPostCursor, m.replyPostAnchor
}

func (m Model) copyReplySelection() (tea.Model, tea.Cmd) {
	start, end := m.replySelection()
	runes := []rune(m.replySourceText())
	start, end = clamp(start, 0, len(runes)), clamp(end, 0, len(runes))
	if start >= end {
		m.clipboardCopied = false
		return m, nil
	}
	m.clipboardOSC = ansi.SetSystemClipboard(string(runes[start:end]))
	m.clipboardCopied = true
	return m, tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return clearClipboardOSCMsg{} })
}

func (m Model) updateReplySourceKey(key string) (tea.Model, tea.Cmd) {
	m.clipboardCopied = false
	switch key {
	case "esc":
		m.cancelRequest()
		m.screen, m.err = m.previous, nil
		return m, nil
	case "f1", "?":
		m.previous, m.screen = m.screen, HelpScreen
		return m, nil
	case "left", "right", "up", "down", "home", "end", "pgup", "pgdown", "shift+left", "shift+right", "shift+up", "shift+down", "shift+home", "shift+end":
		m.moveReplyCursor(key)
	}
	return m, nil
}

func (m *Model) moveReplyCursor(key string) {
	textLength := len([]rune(m.replySourceText()))
	shift := strings.HasPrefix(key, "shift+")
	plain := strings.TrimPrefix(key, "shift+")
	if shift && m.replyPostAnchor < 0 {
		m.replyPostAnchor = m.replyPostCursor
	} else if !shift {
		m.replyPostAnchor = -1
	}
	lines := m.replySourceLines(m.replySourcePanelWidth())
	lineIndex := m.replyCursorLine(lines)
	switch plain {
	case "left":
		m.replyPostCursor--
	case "right":
		m.replyPostCursor++
	case "home":
		m.replyPostCursor = lines[lineIndex].start
	case "end":
		m.replyPostCursor = lines[lineIndex].end
	case "up":
		m.replyPostCursor = replyOffsetOnLine(lines, lineIndex-1, m.replyPostCursor-lines[lineIndex].start)
	case "down":
		m.replyPostCursor = replyOffsetOnLine(lines, lineIndex+1, m.replyPostCursor-lines[lineIndex].start)
	case "pgup":
		m.replyPostCursor = replyOffsetOnLine(lines, lineIndex-m.replySourceVisible(), m.replyPostCursor-lines[lineIndex].start)
	case "pgdown":
		m.replyPostCursor = replyOffsetOnLine(lines, lineIndex+m.replySourceVisible(), m.replyPostCursor-lines[lineIndex].start)
	}
	m.replyPostCursor = clamp(m.replyPostCursor, 0, textLength)
	m.ensureReplyCursorVisible()
}

func (m Model) replyCursorLine(lines []replyTextLine) int {
	for i := range lines {
		if m.replyPostCursor >= lines[i].start && m.replyPostCursor <= lines[i].end {
			return i
		}
	}
	return max(0, len(lines)-1)
}

func replyOffsetOnLine(lines []replyTextLine, index, column int) int {
	index = clamp(index, 0, len(lines)-1)
	return minInt(lines[index].start+max(0, column), lines[index].end)
}

func (m Model) replySourcePanelWidth() int {
	_, width := m.replySourcePanelGeometry()
	return width
}

func (m Model) replySourceVisible() int { return max(1, m.height-6) }

func (m *Model) ensureReplyCursorVisible() {
	lines := m.replySourceLines(m.replySourcePanelWidth())
	line := m.replyCursorLine(lines)
	visible := m.replySourceVisible()
	if line < m.replyPostScroll {
		m.replyPostScroll = line
	} else if line >= m.replyPostScroll+visible {
		m.replyPostScroll = line - visible + 1
	}
}

func (m Model) selectReplyPostMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd, bool) {
	panelX, panelWidth := m.replySourcePanelGeometry()
	inSource := msg.X >= panelX && msg.X < panelX+panelWidth
	if m.replyPostDragging && (msg.Action == tea.MouseActionMotion || msg.Action == tea.MouseActionRelease) {
		if offset, ok := m.replyOffsetAtMouse(msg.X, msg.Y); ok {
			m.replyPostCursor = offset
		}
		if msg.Action == tea.MouseActionRelease {
			m.replyPostDragging = false
		}
		return m, nil, true
	}
	inWorkspace := msg.Y >= 1 && msg.Y < m.height-2
	if !inWorkspace {
		return m, nil, false
	}
	if !m.isNarrow() && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && !inSource {
		m.replyPane, m.replyPostDragging, m.clipboardCopied = replyEditorPane, false, false
		m.editor.Focus()
		return m, nil, true
	}
	if m.isNarrow() && m.replyPane != replySourcePane {
		return m, nil, false
	}
	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		if !inSource || msg.Action != tea.MouseActionPress {
			return m, nil, false
		}
		delta := -1
		if msg.Button == tea.MouseButtonWheelDown {
			delta = 1
		}
		lines := m.replySourceLines(panelWidth)
		m.replyPostScroll = clamp(m.replyPostScroll+delta, 0, max(0, len(lines)-m.replySourceVisible()))
		return m, nil, true
	}
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && inSource {
		m.replyPane, m.clipboardCopied = replySourcePane, false
		m.editor.Blur()
		offset, ok := m.replyOffsetAtMouse(msg.X, msg.Y)
		if !ok {
			return m, nil, true
		}
		m.replyPostAnchor, m.replyPostCursor, m.replyPostDragging = offset, offset, true
		return m, nil, true
	}
	return m, nil, false
}

func (m Model) replyOffsetAtMouse(x, y int) (int, bool) {
	panelX, panelWidth := m.replySourcePanelGeometry()
	lines := m.replySourceLines(panelWidth)
	lineIndex := m.replyPostScroll + y - 3
	if lineIndex < 0 || lineIndex >= len(lines) {
		return 0, false
	}
	line := lines[lineIndex]
	targetCell := max(0, x-panelX-2)
	used, offset := 0, line.start
	for _, r := range []rune(line.text) {
		width := max(1, lipgloss.Width(string(r)))
		if targetCell < used+width {
			return offset, true
		}
		used += width
		offset++
	}
	return line.end, true
}
