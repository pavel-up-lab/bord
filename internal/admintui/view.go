package admintui

import (
	"fmt"
	"strings"

	"bord/internal/terminalui"
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
	if m.themePicker.IsOpen() {
		lines = m.themePicker.Overlay(lines, m.themes, theme, m.width, m.height, m.themePickerAnchor())
	}
	return strings.Join(lines[:height], "\n")
}

func (m Model) topBar(theme terminalui.Theme, width int) string {
	left := " BORD ADMIN  [1 Жалобы] [2 Блокировки] [3 Аудит]"
	if string(m.session.Role) == "administrator" {
		left += " [4 Доски]"
	}
	left += " [F2 Тема] [F1 Справка]"
	return terminalui.TopBar(theme, left, " "+m.screenTitle()+" ", width)
}

func (m Model) themePickerAnchor() int {
	left := " BORD ADMIN  [1 Жалобы] [2 Блокировки] [3 Аудит] "
	if string(m.session.Role) == "administrator" {
		left += "[4 Доски] "
	}
	return terminalui.TextWidth(left)
}

func (m Model) screenTitle() string {
	switch m.screen {
	case ReportsScreen:
		return "Жалобы"
	case BansScreen:
		return "Блокировки: " + m.banState
	case AuditScreen:
		return "Аудит"
	case BoardsScreen:
		return "Доски: " + m.boardState
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
		return panel(theme, "Блокировки", "ID / СОСТОЯНИЕ / ОПЕРАЦИЯ / СРОК", m.banRows(), width, height, true)
	case AuditScreen:
		return panel(theme, "Аудит", "ВРЕМЯ / OUTCOME / ACTION / TARGET", m.auditRows(), width, height, true)
	case BoardsScreen:
		return panel(theme, "Доски", "STATE / SLUG / THREADS / POSTS / TITLE", m.boardRows(), width, height, true)
	case HelpScreen:
		return panel(theme, "Справка", "КЛАВИША / ДЕЙСТВИЕ", []string{"1 / 2 / 3 / 4 — Жалобы / Блокировки / Аудит / Доски", "↑/↓, PgUp/PgDn — навигация", "Tab — переключить панель", "Enter — открыть / следующая страница", "H/U — скрыть/восстановить пост", "C — закрыть тред", "B — блокировка по посту", "X — отозвать блокировку", "I — раскрыть IP (administrator)", "A — открыть вложение", "Доски: N создать, D архивировать, U восстановить", "F6 — фильтр состояния текущего списка", "F2 — тема, F5 — обновить, F10 — выход"}, width, height, true)
	}
	return blank(theme, width, height)
}

func (m Model) reportWorkspace(theme terminalui.Theme, width, height int) []string {
	if width < narrowWidth {
		if m.focus == ListFocus {
			return panel(theme, "Жалобы • Tab: контекст", "ЖАЛОБА / POST / ПРИЧИНА", m.reportRows(), width, height, true)
		}
		return panel(theme, "Контекст • Tab: жалобы", "POST / STATE / CONTENT", m.contextRows(), width, height, true)
	}
	left, right := terminalui.SplitWidth(width, 28, 28)
	leftRows := panel(theme, "Жалобы", "ЖАЛОБА / POST / ПРИЧИНА", m.reportRows(), left, height, m.focus == ListFocus)
	rightRows := panel(theme, "Контекст", "POST / STATE / CONTENT", m.contextRows(), right, height, m.focus == ContextFocus)
	return terminalui.JoinPanels(height, leftRows, rightRows)
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

func (m Model) boardRows() []string {
	rows := make([]string, 0, len(m.boards)+1)
	for index, board := range m.boards {
		marker := "  "
		if index == m.boardIndex {
			marker = "> "
		}
		rows = append(rows, fmt.Sprintf("%s[%s] /%s/ threads:%d posts:%d %s", marker, board.State, terminalui.SanitizeTerminalText(board.Slug), board.ThreadCount, board.PostCount, terminalui.SanitizeTerminalText(board.Title)))
	}
	if len(rows) == 0 {
		rows = append(rows, "  Досок нет")
	}
	if m.boardsCursor != nil {
		rows = append(rows, "  Enter: следующая страница")
	}
	return rows
}

func (m Model) modalRows(theme terminalui.Theme, width, height int) []string {
	title := map[modalKind]string{modalHide: "Скрыть пост", modalRestore: "Восстановить пост", modalClose: "Закрыть тред", modalBan: "Создать блокировку", modalRevoke: "Отозвать блокировку", modalReveal: "Раскрыть IP — чувствительные данные", modalCreateBoard: "Создать доску", modalArchiveBoard: "Архивировать доску", modalRestoreBoard: "Восстановить доску"}[m.modal.kind]
	rows := []string{"Действие: " + title, "Цель: " + terminalui.SanitizeTerminalText(m.modal.target), ""}
	if m.modal.kind == modalCreateBoard {
		rows = []string{"Slug:", m.modal.slug.View(), "Название:", m.modal.title.View(), "Причина:"}
	}
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
	if m.modal.kind == modalCreateBoard {
		rows = append(rows, "Tab/Enter — следующее поле")
	}
	return panel(theme, title, "ДЕЙСТВИЕ / ПРИЧИНА / ПОДТВЕРЖДЕНИЕ", rows, width, height, true)
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
	case BoardsScreen:
		return []string{"F1 Help", "F2 Theme", "F5 Refresh", "F6 All/Active/Archived", "N New", "D Archive", "U Restore", "F10 Quit"}
	default:
		return []string{"Esc Back", "F2 Theme", "F10 Quit"}
	}
}

func panel(theme terminalui.Theme, title, hint string, rows []string, width, height int, active bool) []string {
	display := make([]terminalui.Row, 0, len(rows))
	for _, row := range rows {
		display = append(display, terminalui.Row{Lines: []string{row}, Active: strings.HasPrefix(row, "> ")})
	}
	return terminalui.Panel(theme, width, height, title, hint, display, active)
}

func blank(theme terminalui.Theme, width, height int) []string {
	result := make([]string, height)
	for i := range result {
		result[i] = terminalui.Paint(theme.Background, theme.Foreground, "", width)
	}
	return result
}
