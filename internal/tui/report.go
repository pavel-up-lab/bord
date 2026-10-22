package tui

import (
	"context"
	"strings"
	"time"

	"bord/api/generated"
	"bord/internal/terminalui"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type reportReason struct {
	value generated.CreateReportReason
	label string
}

var reportReasons = []reportReason{
	{generated.Spam, "Спам"},
	{generated.Harassment, "Домогательство"},
	{generated.PersonalData, "Персональные данные"},
	{generated.Illegal, "Незаконный контент"},
	{generated.Other, "Другое"},
}

type reportForm struct {
	post        generated.Post
	editor      textarea.Model
	reasonIndex int
	pending     bool
	err         error
}

func (m *Model) openReportForm() {
	if m.screen != PostScreen || m.report != nil {
		return
	}
	post, ok := m.currentPost()
	if !ok {
		return
	}
	editor := textarea.New()
	editor.Placeholder = "Дополнительный комментарий (до 500 символов)…"
	editor.CharLimit = 500
	editor.SetHeight(3)
	editor.Focus()
	m.report = &reportForm{post: post, editor: editor}
	m.resizeReportEditor()
	m.notice = ""
}

func (m *Model) resizeReportEditor() {
	if m.report == nil {
		return
	}
	_, width, _ := m.reportGeometry()
	m.report.editor.SetWidth(max(1, width-6))
}

func (m Model) updateReportKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.report == nil {
		return m, nil
	}
	if m.report.pending {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.report = nil
		return m, nil
	case "shift+tab":
		m.report.reasonIndex = (m.report.reasonIndex - 1 + len(reportReasons)) % len(reportReasons)
		m.report.err = nil
		return m, nil
	case "tab":
		m.report.reasonIndex = (m.report.reasonIndex + 1) % len(reportReasons)
		m.report.err = nil
		return m, nil
	case "ctrl+s":
		return m, m.submitReport()
	}
	var command tea.Cmd
	m.report.editor, command = m.report.editor.Update(msg)
	m.report.err = nil
	return m, command
}

func (m *Model) submitReport() tea.Cmd {
	if m.report == nil || m.report.pending {
		return nil
	}
	postID := m.report.post.Id
	reason := reportReasons[m.report.reasonIndex].value
	detailText := strings.TrimSpace(m.report.editor.Value())
	var detail *string
	if detailText != "" {
		detail = &detailText
	}
	m.report.pending, m.report.err = true, nil
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
		defer cancel()
		err := m.api.Report(ctx, postID, generated.CreateReport{Reason: reason, Detail: detail})
		return reportSentMsg{postID: postID, err: err}
	}
}

func (m Model) reportGeometry() (x, width, height int) {
	width = minInt(72, max(24, m.width-4))
	width = minInt(max(1, m.width), width)
	height = minInt(16, max(6, m.height-2))
	height = minInt(max(1, m.height), height)
	x = max(0, (m.width-width)/2)
	return x, width, height
}

func (m Model) reportRows(height, innerWidth int) []terminalui.Row {
	available := max(1, height-3)
	form := m.report
	body := "[пусто]"
	if form.post.Body != nil {
		body = preview(*form.post.Body)
	}
	wrapWidth := max(1, innerWidth-2)
	body = ansi.Hardwrap(ansi.Wordwrap(SanitizeTerminalText(body), wrapWidth, " "), wrapWidth, true)
	bodyLines := strings.Split(body, "\n")
	if len(bodyLines) > 3 {
		bodyLines = bodyLines[:3]
		bodyLines[2] = ansi.Truncate(bodyLines[2], max(1, wrapWidth-1), "") + "…"
	}
	rows := []terminalui.Row{
		{Lines: []string{"Пост >>" + SanitizeTerminalText(form.post.Id) + " • " + SanitizeTerminalText(form.post.AuthorId)}},
		{Lines: bodyLines},
		{Lines: []string{"Категория: " + reportReasons[form.reasonIndex].label + "  Tab / Shift+Tab"}},
		{Lines: []string{"Комментарий (необязательно):"}},
		{Lines: strings.Split(form.editor.View(), "\n"), TrustedANSI: true},
	}
	status := "Ctrl+S или кнопка — отправить • Esc — отменить"
	if form.pending {
		status = "Отправка жалобы…"
	} else if form.err != nil {
		status = "Ошибка отправки • можно повторить"
	}
	rows = append(rows, terminalui.Row{Lines: []string{status}})
	flat := make([]terminalui.Row, 0, available)
	for _, row := range rows {
		for _, line := range row.Lines {
			if len(flat) >= available-1 {
				break
			}
			flat = append(flat, terminalui.Row{Lines: []string{line}, TrustedANSI: row.TrustedANSI})
		}
	}
	for len(flat) < available-1 {
		flat = append(flat, terminalui.Row{Lines: []string{""}})
	}
	return append(flat, terminalui.Row{Lines: []string{"[Пожаловаться]   [Отмена]"}, Active: !form.pending})
}

func (m Model) overlayReport(lines []string, theme theme) []string {
	if m.report == nil || len(lines) == 0 {
		return lines
	}
	x, width, height := m.reportGeometry()
	y := max(0, (m.height-height)/2)
	panel := terminalui.Panel(theme, width, height, "Пожаловаться на пост", "ПОСТ / КАТЕГОРИЯ / КОММЕНТАРИЙ", m.reportRows(height, width-2), true)
	for index, overlay := range panel {
		row := y + index
		if row < 0 || row >= len(lines) {
			continue
		}
		lines[row] = ansi.Cut(lines[row], 0, x) + overlay + ansi.Cut(lines[row], x+width, m.width)
	}
	return lines
}

func (m Model) updateReportMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.report == nil || msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil
	}
	x, _, height := m.reportGeometry()
	y := max(0, (m.height-height)/2)
	actionY := y + height - 2
	if msg.Y != actionY || m.report.pending {
		return m, nil
	}
	reportX := x + 2
	cancelX := reportX + textWidth("[Пожаловаться]   ")
	if msg.X >= reportX && msg.X < reportX+textWidth("[Пожаловаться]") {
		return m, m.submitReport()
	}
	if msg.X >= cancelX && msg.X < cancelX+textWidth("[Отмена]") {
		m.report = nil
	}
	return m, nil
}
