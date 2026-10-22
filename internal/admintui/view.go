package admintui

import (
	"fmt"
	"strings"

	"bord/internal/terminalui"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const narrowWidth = 78

func (m Model) View() string {
	width, height := max(1, m.width), max(1, m.height)
	theme := m.themes.Palette(m.theme)
	lines := []string{m.topBar(theme, width)}
	workspaceHeight := max(0, height-3)
	if workspaceHeight > 0 {
		lines = append(lines, m.workspace(theme, width, workspaceHeight)...)
	}
	if len(lines) < height-1 {
		lines = append(lines, terminalui.Paint(theme.Background, theme.Foreground, m.statusLine(), width))
	}
	if len(lines) < height {
		lines = append(lines, terminalui.CommandBar(theme, m.commandLabels(), width))
	}
	for len(lines) < height {
		lines = append(lines, terminalui.Paint(theme.Background, theme.Foreground, "", width))
	}
	if m.themeMenu {
		lines = m.renderThemeMenu(lines, theme)
	}
	return strings.Join(lines[:height], "\n")
}

func (m Model) topBar(theme terminalui.Theme, width int) string {
	return terminalui.TopBar(theme, " BORD ADMIN  [1 Жалобы] [2 Блокировки] [3 Аудит] [F2 Тема] [F1 Справка]", " "+m.screenTitle()+" ", width)
}

func (m Model) screenTitle() string {
	switch m.screen {
	case ReportsScreen:
		return "Жалобы"
	case BansScreen:
		return "Блокировки: " + m.banState
	case AuditScreen:
		return "Аудит"
	case HelpScreen:
		return "Справка"
	}
	return ""
}

func (m Model) workspace(theme terminalui.Theme, width, height int) []string {
	if m.modal != nil {
		return m.modalRows(theme, width, height)
	}
	switch m.screen {
	case ReportsScreen:
		return m.reportWorkspace(theme, width, height)
	case BansScreen:
		return panel(theme, "Блокировки", m.banRows(), width, height, true)
	case AuditScreen:
		return panel(theme, "Аудит", m.auditRows(), width, height, true)
	case HelpScreen:
		return panel(theme, "Справка", []string{"1 / 2 / 3 — экраны", "↑/↓, PgUp/PgDn — навигация", "Tab — переключить панель", "Enter — открыть / следующая страница", "H/U — скрыть/восстановить пост", "C — закрыть тред", "B — блокировка по посту", "X — отозвать блокировку", "I — раскрыть IP (administrator)", "A — открыть вложение", "F2 — тема, F5 — обновить, F10 — выход"}, width, height, true)
	}
	return blank(theme, width, height)
}

func (m Model) reportWorkspace(theme terminalui.Theme, width, height int) []string {
	if width < narrowWidth {
		if m.focus == ListFocus {
			return panel(theme, "Жалобы • Tab: контекст", m.reportRows(), width, height, true)
		}
		return panel(theme, "Контекст • Tab: жалобы", m.contextRows(), width, height, true)
	}
	left := max(28, width/3)
	right := width - left
	leftRows := panel(theme, "Жалобы", m.reportRows(), left, height, m.focus == ListFocus)
	rightRows := panel(theme, "Контекст", m.contextRows(), right, height, m.focus == ContextFocus)
	rows := make([]string, height)
	for index := range rows {
		rows[index] = leftRows[index] + rightRows[index]
	}
	return rows
}

func (m Model) reportRows() []string {
	rows := make([]string, 0, len(m.reports)+1)
	for index, report := range m.reports {
		marker := "  "
		if index == m.reportIndex {
			marker = "> "
		}
		rows = append(rows, fmt.Sprintf("%s%s post:%s %s", marker, report.Id, report.PostId, terminalui.SanitizeTerminalText(report.Reason)))
	}
	if len(rows) == 0 {
		rows = append(rows, "  Жалоб нет")
	}
	if m.reportsCursor != nil {
		rows = append(rows, "  Enter: следующая страница")
	}
	return rows
}

func (m Model) contextRows() []string {
	rows := []string{}
	if m.context.TargetPostId == "" {
		return []string{"  Выберите жалобу"}
	}
	rows = append(rows, fmt.Sprintf("  /%s/ • thread %s • target %s", terminalui.SanitizeTerminalText(m.context.Board.Slug), m.context.Thread.Id, m.context.TargetPostId))
	for index, post := range m.context.Posts {
		marker := "  "
		if index == m.postIndex {
			marker = "> "
		}
		body := "(без текста)"
		if post.Body != nil {
			body = terminalui.SanitizeMultilineTerminalText(*post.Body)
		}
		attachment := ""
		if post.Attachment != nil {
			attachment = " [image:" + terminalui.SanitizeTerminalText(post.Attachment.Id) + "]"
		}
		rows = append(rows, fmt.Sprintf("%s%s [%s]%s", marker, post.Id, post.State, attachment))
		for _, line := range strings.Split(body, "\n") {
			rows = append(rows, "    "+line)
		}
	}
	if m.contextCursor != nil {
		rows = append(rows, "  Enter: следующая страница")
	}
	return rows
}

func (m Model) banRows() []string {
	rows := make([]string, 0, len(m.bans)+1)
	for index, ban := range m.bans {
		marker := "  "
		if index == m.banIndex {
			marker = "> "
		}
		rows = append(rows, fmt.Sprintf("%s%s [%s] %s %s до %s — %s", marker, ban.Id, ban.State, ban.Operation, terminalui.SanitizeTerminalText(ban.Subject), ban.ExpiresAt.Local().Format("2006-01-02 15:04"), terminalui.SanitizeTerminalText(ban.Reason)))
	}
	if len(rows) == 0 {
		rows = append(rows, "  Записей нет")
	}
	if m.bansCursor != nil {
		rows = append(rows, "  Enter: следующая страница")
	}
	return rows
}

func (m Model) auditRows() []string {
	rows := make([]string, 0, len(m.audit)+1)
	for index, entry := range m.audit {
		marker := "  "
		if index == m.auditIndex {
			marker = "> "
		}
		target, reason, code := "-", "", ""
		if entry.TargetId != nil {
			target = terminalui.SanitizeTerminalText(*entry.TargetId)
		}
		if entry.Reason != nil {
			reason = " — " + terminalui.SanitizeTerminalText(*entry.Reason)
		}
		if entry.ErrorCode != nil {
			code = " [" + terminalui.SanitizeTerminalText(string(*entry.ErrorCode)) + "]"
		}
		rows = append(rows, fmt.Sprintf("%s%s %s %s → %s%s%s", marker, entry.CreatedAt.Local().Format("01-02 15:04"), entry.Outcome, terminalui.SanitizeTerminalText(entry.Action), target, code, reason))
	}
	if len(rows) == 0 {
		rows = append(rows, "  Записей нет")
	}
	if m.auditCursor != nil {
		rows = append(rows, "  Enter: следующая страница")
	}
	return rows
}

func (m Model) modalRows(theme terminalui.Theme, width, height int) []string {
	title := map[modalKind]string{modalHide: "Скрыть пост", modalRestore: "Восстановить пост", modalClose: "Закрыть тред", modalBan: "Создать блокировку", modalRevoke: "Отозвать блокировку", modalReveal: "Раскрыть IP — чувствительные данные"}[m.modal.kind]
	rows := []string{"Действие: " + title, "Цель: " + terminalui.SanitizeTerminalText(m.modal.target), ""}
	rows = append(rows, strings.Split(m.modal.editor.View(), "\n")...)
	if m.modal.kind == modalBan {
		rows = append(rows, "Операции: "+strings.Join(m.modal.operations, ", ")+" • TTL: "+m.modal.ttl.String())
	}
	if m.revealedIP != "" {
		rows = append(rows, "", "IP: "+m.revealedIP, "Значение не копируется автоматически.")
	}
	if m.modal.err != "" {
		rows = append(rows, "", terminalui.SanitizeTerminalText(m.modal.err))
	}
	if m.modal.pending {
		rows = append(rows, "", "Выполняется…")
	} else if m.modal.confirmed {
		rows = append(rows, "", "Ctrl+S — подтвердить окончательно • Esc — отмена")
	} else {
		rows = append(rows, "", "Ctrl+S — перейти к подтверждению • Esc — отмена")
	}
	return panel(theme, title, rows, width, height, true)
}

func (m Model) statusLine() string {
	if m.loading {
		return " Загрузка…"
	}
	if m.err != nil {
		return " Ошибка: " + safeError(m.err)
	}
	if m.status != "" {
		return " " + terminalui.SanitizeTerminalText(m.status)
	}
	return fmt.Sprintf(" %s • роль:%s • тема:%s", m.screenTitle(), terminalui.SanitizeTerminalText(string(m.session.Role)), m.theme)
}

func (m Model) commandLabels() []string {
	if m.modal != nil {
		if m.modal.kind == modalBan {
			return []string{"Ctrl+S Далее", "F6 TTL", "F7 Operations", "Esc Отмена"}
		}
		return []string{"Ctrl+S Далее", "Esc Отмена"}
	}
	switch m.screen {
	case ReportsScreen:
		labels := []string{"F1 Help", "F2 Theme", "F5 Refresh"}
		if !m.canModerate() {
			return append(labels, "F10 Quit")
		}
		labels = append(labels, "H Hide", "U Restore", "C Close", "B Ban", "A Image")
		if string(m.session.Role) == "administrator" {
			labels = append(labels, "I Reveal")
		}
		return append(labels, "F10 Quit")
	case BansScreen:
		return []string{"F1 Help", "F2 Theme", "F5 Refresh", "F6 Active/History", "X Revoke", "F10 Quit"}
	case AuditScreen:
		return []string{"F1 Help", "F2 Theme", "F5 Refresh", "F10 Quit"}
	default:
		return []string{"Esc Back", "F2 Theme", "F10 Quit"}
	}
}

func panel(theme terminalui.Theme, title string, rows []string, width, height int, active bool) []string {
	if width <= 2 || height <= 1 {
		return blank(theme, width, height)
	}
	border := theme.Muted
	if active {
		border = theme.Accent
	}
	line := theme.Lines
	if line.Horizontal == "" {
		line = terminalui.DefaultLines()
	}
	inner := width - 2
	topText := line.Horizontal + " " + terminalui.SanitizeTerminalText(title) + " "
	top := line.LeftTop + fit(topText, inner) + line.RightTop
	result := []string{terminalui.Paint(theme.Background, border, top, width)}
	for index := 0; index < height-2; index++ {
		value := ""
		if index < len(rows) {
			value = rows[index]
		}
		result = append(result, terminalui.Paint(theme.Background, border, line.Vertical, 1)+terminalui.Paint(theme.Background, theme.Foreground, fit(value, inner), inner)+terminalui.Paint(theme.Background, border, line.Vertical, 1))
	}
	result = append(result, terminalui.Paint(theme.Background, border, line.LeftBottom+strings.Repeat(line.Horizontal, inner)+line.RightBottom, width))
	return result
}

func blank(theme terminalui.Theme, width, height int) []string {
	result := make([]string, height)
	for i := range result {
		result[i] = terminalui.Paint(theme.Background, theme.Foreground, "", width)
	}
	return result
}
func fit(value string, width int) string {
	value = ansi.Truncate(value, width, "…")
	if missing := width - lipgloss.Width(value); missing > 0 {
		value += strings.Repeat(" ", missing)
	}
	return value
}

func (m Model) renderThemeMenu(lines []string, theme terminalui.Theme) []string {
	options := m.themes.Options()
	if len(options) == 0 {
		return lines
	}
	menuWidth := min(32, max(14, m.width/3))
	start := max(1, m.width-menuWidth-1)
	for index, option := range options {
		y := 2 + index
		if y >= len(lines)-1 {
			break
		}
		marker := "  "
		if index == m.themeIndex {
			marker = "> "
		}
		content := terminalui.Paint(theme.BarBackground, theme.BarForeground, marker+terminalui.SanitizeTerminalText(option.Label), menuWidth)
		prefix := ansi.Truncate(lines[y], start, "")
		lines[y] = prefix + content
		if terminalui.TextWidth(lines[y]) < m.width {
			lines[y] += terminalui.Paint(theme.Background, theme.Foreground, "", m.width-terminalui.TextWidth(lines[y]))
		}
	}
	return lines
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
