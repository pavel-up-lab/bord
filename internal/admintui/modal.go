package admintui

import (
	"strings"
	"time"

	"bord/internal/adminclient"
	"bord/internal/terminalui"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type modalKind uint8

const (
	modalHide modalKind = iota
	modalRestore
	modalClose
	modalBan
	modalRevoke
	modalReveal
	modalCreateBoard
	modalArchiveBoard
	modalRestoreBoard
)

type modal struct {
	kind               modalKind
	target             string
	editor             textarea.Model
	confirmed, pending bool
	err                string
	operations         []string
	ttl                time.Duration
	slug, title        textinput.Model
	field              int
}

func (m Model) selectedPost() (string, bool) {
	if len(m.context.Posts) > 0 && m.postIndex >= 0 && m.postIndex < len(m.context.Posts) {
		return m.context.Posts[m.postIndex].Id, true
	}
	if len(m.reports) > 0 && m.reportIndex >= 0 && m.reportIndex < len(m.reports) {
		return m.reports[m.reportIndex].PostId, true
	}
	return "", false
}

func (m Model) openVisibilityModal(visible bool) (tea.Model, tea.Cmd) {
	if !m.canModerate() {
		m.err = &adminclient.Error{StatusCode: 403, Code: "forbidden"}
		return m, nil
	}
	postID, ok := m.selectedPost()
	if !ok {
		return m, nil
	}
	kind := modalHide
	if visible {
		kind = modalRestore
	}
	return m.openModal(kind, postID), nil
}

func (m Model) openCloseModal() (tea.Model, tea.Cmd) {
	if !m.canModerate() || m.context.Thread.Id == "" {
		return m, nil
	}
	return m.openModal(modalClose, m.context.Thread.Id), nil
}

func (m Model) openBanModal() (tea.Model, tea.Cmd) {
	if !m.canModerate() {
		return m, nil
	}
	postID, ok := m.selectedPost()
	if !ok {
		return m, nil
	}
	updated := m.openModal(modalBan, postID)
	updated.modal.operations, updated.modal.ttl = []string{"create_post"}, 24*time.Hour
	return updated, nil
}

func (m Model) openRevokeModal() (tea.Model, tea.Cmd) {
	if !m.canModerate() || m.screen != BansScreen || len(m.bans) == 0 || m.bans[m.banIndex].State != "active" {
		return m, nil
	}
	return m.openModal(modalRevoke, m.bans[m.banIndex].Id), nil
}

func (m Model) openRevealModal() (tea.Model, tea.Cmd) {
	if string(m.session.Role) != "administrator" {
		return m, nil
	}
	postID, ok := m.selectedPost()
	if !ok {
		return m, nil
	}
	return m.openModal(modalReveal, postID), nil
}

func (m Model) openCreateBoardModal() (tea.Model, tea.Cmd) {
	if string(m.session.Role) != "administrator" || m.screen != BoardsScreen {
		return m, nil
	}
	slug := textinput.New()
	slug.Placeholder, slug.CharLimit, slug.Width = "slug", 32, 32
	title := textinput.New()
	title.Placeholder, title.CharLimit, title.Width = "Название", 100, 56
	updated := m.openModal(modalCreateBoard, "")
	updated.modal.slug, updated.modal.title = slug, title
	updated.focusBoardModalField(0)
	return updated, nil
}

func (m Model) openBoardStateModal(enabled bool) (tea.Model, tea.Cmd) {
	if string(m.session.Role) != "administrator" || m.screen != BoardsScreen || len(m.boards) == 0 || m.boardIndex < 0 || m.boardIndex >= len(m.boards) {
		return m, nil
	}
	board := m.boards[m.boardIndex]
	want := "archived"
	kind := modalArchiveBoard
	if enabled {
		want, kind = "active", modalRestoreBoard
	}
	if string(board.State) == want {
		return m, nil
	}
	return m.openModal(kind, board.Id), nil
}

func (m Model) openModal(kind modalKind, target string) Model {
	editor := newReasonEditor()
	m.modal = &modal{kind: kind, target: target, editor: editor}
	m.err, m.revealedIP = nil, ""
	return m
}

func (m Model) canModerate() bool {
	role := string(m.session.Role)
	return role == "moderator" || role == "administrator"
}

func (m Model) updateModal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.modal.pending {
		return m, nil
	}
	if m.modal.kind == modalReveal && m.revealedIP != "" && msg.String() != "esc" {
		return m, nil
	}
	key := msg.String()
	if m.modal.kind == modalCreateBoard && (key == "tab" || key == "shift+tab" || key == "enter" && m.modal.field < 2) {
		delta := 1
		if key == "shift+tab" {
			delta = -1
		}
		m.focusBoardModalField((m.modal.field + delta + 3) % 3)
		return m, nil
	}
	switch key {
	case "esc":
		m.modal, m.revealedIP = nil, ""
		return m, nil
	case "ctrl+s", "enter":
		reason := strings.TrimSpace(m.modal.editor.Value())
		if !validReason(reason) {
			m.modal.err = "Причина должна содержать 3–500 символов"
			return m, nil
		}
		if m.modal.kind == modalCreateBoard {
			slug, title := strings.TrimSpace(m.modal.slug.Value()), strings.TrimSpace(m.modal.title.Value())
			if !validAdminBoardSlug(slug) {
				m.modal.err = "Slug: 1–32 символа a-z, 0-9 или -"
				return m, nil
			}
			if title == "" || len([]rune(title)) > 100 {
				m.modal.err = "Название должно содержать 1–100 символов"
				return m, nil
			}
		}
		if !m.modal.confirmed {
			m.modal.confirmed, m.modal.err = true, "Нажмите Ctrl+S повторно для подтверждения"
			return m, nil
		}
		m.modal.pending, m.modal.err = true, ""
		m, id, ctx := m.beginRequest()
		kind, target := m.modal.kind, m.modal.target
		operations, ttl := append([]string(nil), m.modal.operations...), m.modal.ttl
		slug, title := strings.TrimSpace(m.modal.slug.Value()), strings.TrimSpace(m.modal.title.Value())
		return m, func() tea.Msg {
			var value string
			var err error
			switch kind {
			case modalHide:
				err = m.service.SetVisibility(ctx, target, false, reason)
			case modalRestore:
				err = m.service.SetVisibility(ctx, target, true, reason)
			case modalClose:
				err = m.service.CloseThread(ctx, target, reason)
			case modalBan:
				_, err = m.service.CreatePostBan(ctx, target, adminclient.PostBanRequest{Operations: operations, ExpiresAt: time.Now().Add(ttl), Reason: reason})
			case modalRevoke:
				err = m.service.RevokeBan(ctx, target, reason)
			case modalReveal:
				value, err = m.service.RevealIP(ctx, target, reason)
			case modalCreateBoard:
				_, err = m.service.CreateBoard(ctx, adminclient.CreateBoardRequest{Slug: slug, Title: title, Reason: reason})
			case modalArchiveBoard:
				_, err = m.service.ArchiveBoard(ctx, target, reason)
			case modalRestoreBoard:
				_, err = m.service.RestoreBoard(ctx, target, reason)
			}
			return mutationMsg{id: id, kind: kind, value: value, err: err}
		}
	case "f6":
		if m.modal.kind == modalBan {
			switch m.modal.ttl {
			case time.Hour:
				m.modal.ttl = 24 * time.Hour
			case 24 * time.Hour:
				m.modal.ttl = 7 * 24 * time.Hour
			default:
				m.modal.ttl = time.Hour
			}
			m.modal.confirmed = false
		}
		return m, nil
	case "f7":
		if m.modal.kind == modalBan {
			presets := [][]string{{"create_post"}, {"create_thread", "create_post"}, {"create_thread", "create_post", "delete_post", "report"}}
			next := 0
			for index, preset := range presets {
				if strings.Join(preset, ",") == strings.Join(m.modal.operations, ",") {
					next = (index + 1) % len(presets)
					break
				}
			}
			m.modal.operations = append([]string(nil), presets[next]...)
			m.modal.confirmed = false
		}
		return m, nil
	}
	var cmd tea.Cmd
	if m.modal.kind == modalCreateBoard {
		switch m.modal.field {
		case 0:
			m.modal.slug, cmd = m.modal.slug.Update(msg)
		case 1:
			m.modal.title, cmd = m.modal.title.Update(msg)
		default:
			m.modal.editor, cmd = m.modal.editor.Update(msg)
		}
	} else {
		m.modal.editor, cmd = m.modal.editor.Update(msg)
	}
	m.modal.confirmed, m.modal.err = false, ""
	return m, cmd
}

func (m *Model) focusBoardModalField(field int) {
	if m.modal == nil || m.modal.kind != modalCreateBoard {
		return
	}
	m.modal.field = clamp(field, 0, 2)
	m.modal.slug.Blur()
	m.modal.title.Blur()
	m.modal.editor.Blur()
	switch m.modal.field {
	case 0:
		m.modal.slug.Focus()
	case 1:
		m.modal.title.Focus()
	default:
		m.modal.editor.Focus()
	}
}

func validAdminBoardSlug(value string) bool {
	if len(value) < 1 || len(value) > 32 || value[0] == '-' {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-') {
			return false
		}
	}
	return true
}

func (m Model) openAttachment() (tea.Model, tea.Cmd) {
	if m.screen != ReportsScreen || len(m.context.Posts) == 0 {
		return m, nil
	}
	post := m.context.Posts[m.postIndex]
	if post.Attachment == nil {
		return m, nil
	}
	meta := terminalui.ImageMetadata{ID: post.Attachment.Id, SHA256: post.Attachment.Sha256, MediaType: string(post.Attachment.MediaType), Size: post.Attachment.Size}
	m, id, ctx := m.beginRequest()
	return m, func() tea.Msg {
		body, mediaType, err := m.service.Attachment(ctx, meta.ID)
		return attachmentMsg{id: id, meta: meta, body: body, mediaType: mediaType, err: err}
	}
}
