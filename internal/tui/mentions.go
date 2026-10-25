package tui

import (
	"context"
	"fmt"
	"strings"

	"bord/internal/client"
	"bord/internal/terminalui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

const maxVisibleMentions = 5

func (m Model) watchMentions() tea.Cmd {
	source, ok := m.api.(mentionService)
	if !ok {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(m.ctx)
		defer cancel()
		var found client.Mention
		err := source.Subscribe(ctx, client.StreamScope{Global: true}, client.StreamCallbacks{Event: func(event client.Event) {
			if found.PostID != "" || event.Type != "post.created" || event.ReplyToID == "" || !source.OwnsPost(event.ReplyToID) {
				return
			}
			found = client.Mention{BoardID: event.BoardID, ThreadID: event.ThreadID, PostID: event.PostID, ReplyToID: event.ReplyToID}
			cancel()
		}}, client.DefaultBackoff())
		if found.PostID != "" {
			return mentionMsg{mention: found}
		}
		if m.ctx.Err() != nil {
			return nil
		}
		return mentionMsg{err: err}
	}
}

func (m *Model) addMention(mention client.Mention) {
	for _, existing := range m.mentions {
		if existing.PostID == mention.PostID {
			return
		}
	}
	m.mentions = append(m.mentions, mention)
	if len(m.mentions) > 50 {
		m.mentions = append([]client.Mention(nil), m.mentions[len(m.mentions)-50:]...)
	}
}

func (m Model) mentionGeometry() (x, y, width, height, count int) {
	if len(m.mentions) == 0 || m.width < 20 || m.height < 7 {
		return 0, 0, 0, 0, 0
	}
	width = minInt(48, m.width)
	count = minInt(maxVisibleMentions, len(m.mentions))
	height = 3
	if !m.mentionsCollapsed {
		height += count
	}
	x = m.width - width
	y = max(1, m.height-2-height)
	return
}

func (m Model) overlayMentions(lines []string, theme theme) []string {
	x, y, width, height, count := m.mentionGeometry()
	if width == 0 || y+height > len(lines) {
		return lines
	}
	inner := width - 2
	fill := func(value string) string {
		value = ansi.Truncate(value, inner, "…")
		return value + strings.Repeat(" ", max(0, inner-textWidth(value)))
	}
	popup := make([]string, 0, height)
	popup = append(popup, terminalui.Paint(theme.Background, theme.Accent, "┌"+strings.Repeat("─", inner)+"┐", width))
	toggle := "[−]"
	if m.mentionsCollapsed {
		toggle = "[+]"
	}
	header := fmt.Sprintf(" Упоминания: %d", len(m.mentions))
	header = ansi.Truncate(header, max(1, inner-textWidth(toggle)-1), "…")
	header += strings.Repeat(" ", max(1, inner-textWidth(header)-textWidth(toggle))) + toggle
	popup = append(popup, terminalui.Paint(theme.SelectionBackground, theme.Selection, "│"+fill(header)+"│", width))
	if !m.mentionsCollapsed {
		for offset := 0; offset < count; offset++ {
			mention := m.mentions[len(m.mentions)-1-offset]
			label := fmt.Sprintf(" > тред %s • >>%s", SanitizeTerminalText(mention.ThreadID), SanitizeTerminalText(mention.PostID))
			popup = append(popup, terminalui.Paint(theme.Background, theme.Foreground, "│"+fill(label)+"│", width))
		}
	}
	popup = append(popup, terminalui.Paint(theme.Background, theme.Accent, "└"+strings.Repeat("─", inner)+"┘", width))
	for index, overlay := range popup {
		row := y + index
		lines[row] = ansi.Cut(lines[row], 0, x) + overlay + ansi.Cut(lines[row], x+width, m.width)
	}
	return lines
}

func (m Model) updateMentionsMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd, bool) {
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil, false
	}
	x, y, width, height, count := m.mentionGeometry()
	if width == 0 || msg.X < x || msg.X >= x+width || msg.Y < y || msg.Y >= y+height {
		return m, nil, false
	}
	localY := msg.Y - y
	if localY == 1 {
		m.mentionsCollapsed = !m.mentionsCollapsed
		if source, ok := m.api.(mentionService); ok {
			if err := source.SetMentionsCollapsed(m.mentionsCollapsed); err != nil {
				m.notice = "Не удалось сохранить состояние уведомлений"
			}
		}
		return m, nil, true
	}
	if !m.mentionsCollapsed && localY >= 2 && localY < 2+count {
		index := len(m.mentions) - 1 - (localY - 2)
		return m.openMention(index)
	}
	return m, nil, true
}

func (m Model) openMention(index int) (tea.Model, tea.Cmd, bool) {
	if index < 0 || index >= len(m.mentions) {
		return m, nil, true
	}
	mention := m.mentions[index]
	if source, ok := m.api.(mentionService); ok {
		if err := source.DismissMention(mention.PostID); err != nil {
			m.notice = "Не удалось сохранить уведомления"
			return m, nil, true
		}
	}
	m.mentions = append(m.mentions[:index], m.mentions[index+1:]...)
	attachmentCleanup := m.clearAttachmentViewer()
	previewCleanup := m.clearPostPreview()
	m.previous, m.screen, m.focus = m.screen, ThreadScreen, FocusContent
	m.activeThread, m.pendingMentionPost = mention.ThreadID, mention.PostID
	m.posts, m.contentIndex, m.contentScroll, m.postScroll = nil, 0, 0, 0
	return m, tea.Batch(attachmentCleanup, previewCleanup, m.loadPosts(false)), true
}
