package admintui

import (
	"context"
	"errors"
	"strings"

	"bord/api/generated"
	"bord/internal/adminclient"
	"bord/internal/terminalui"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

type Screen uint8

const (
	ReportsScreen Screen = iota
	BansScreen
	AuditScreen
	BoardsScreen
	HelpScreen
	AttachmentScreen
)

type Focus uint8

const (
	ListFocus Focus = iota
	ContextFocus
)

type Service interface {
	Session(context.Context) (adminclient.Session, error)
	Reports(context.Context, *string, int) (adminclient.Page[adminclient.Report], error)
	PostContext(context.Context, string, *string, int) (adminclient.PostContext, error)
	Attachment(context.Context, string) ([]byte, string, error)
	Bans(context.Context, string, *string, int) (adminclient.Page[adminclient.BanRecord], error)
	Audit(context.Context, *string, int) (adminclient.Page[adminclient.AuditEntry], error)
	Boards(context.Context, string, *string, int) (adminclient.Page[adminclient.Board], error)
	CreateBoard(context.Context, adminclient.CreateBoardRequest) (adminclient.Board, error)
	ArchiveBoard(context.Context, string, string) (adminclient.Board, error)
	RestoreBoard(context.Context, string, string) (adminclient.Board, error)
	SetVisibility(context.Context, string, bool, string) error
	CloseThread(context.Context, string, string) error
	CreatePostBan(context.Context, string, adminclient.PostBanRequest) (adminclient.Ban, error)
	RevokeBan(context.Context, string, string) error
	RevealIP(context.Context, string, string) (string, error)
}

type ImageViewer interface {
	Render(terminalui.ImageMetadata, []byte, string, int, int) (ImageView, error)
	Clear() string
	Open(context.Context, string, bool) tea.Cmd
}

type Model struct {
	ctx, requestContext   context.Context
	cancel, requestCancel context.CancelFunc
	service               Service
	viewer                ImageViewer

	screen, previous Screen
	focus            Focus
	width, height    int
	theme            terminalui.ThemeName
	themes           terminalui.ThemeCatalog
	themePicker      terminalui.ThemePicker

	session                                                             adminclient.Session
	reports                                                             []adminclient.Report
	context                                                             adminclient.PostContext
	bans                                                                []adminclient.BanRecord
	audit                                                               []adminclient.AuditEntry
	boards                                                              []adminclient.Board
	reportIndex, postIndex, banIndex, auditIndex, boardIndex            int
	listScroll, contextScroll                                           int
	reportsCursor, contextCursor, bansCursor, auditCursor, boardsCursor *string
	banState                                                            string
	boardState                                                          string

	requestID                 uint64
	loading                   bool
	err                       error
	status                    string
	modal                     *modal
	revealedIP                string
	attachmentID              string
	attachmentMeta            terminalui.ImageMetadata
	attachmentBody            []byte
	attachmentType            string
	attachmentView            ImageView
	terminalControl           string
	terminalControlGeneration uint64
}

type sessionMsg struct {
	id    uint64
	value adminclient.Session
	err   error
}
type reportsMsg struct {
	id     uint64
	page   adminclient.Page[adminclient.Report]
	append bool
	err    error
}
type contextMsg struct {
	id     uint64
	value  adminclient.PostContext
	append bool
	err    error
}
type bansMsg struct {
	id     uint64
	page   adminclient.Page[adminclient.BanRecord]
	append bool
	err    error
}
type auditMsg struct {
	id     uint64
	page   adminclient.Page[adminclient.AuditEntry]
	append bool
	err    error
}
type boardsMsg struct {
	id     uint64
	page   adminclient.Page[adminclient.Board]
	append bool
	err    error
}
type mutationMsg struct {
	id    uint64
	kind  modalKind
	value string
	err   error
}
type attachmentMsg struct {
	id        uint64
	meta      terminalui.ImageMetadata
	body      []byte
	mediaType string
	err       error
}
type viewerClosedMsg struct{ err error }
type clearTerminalControlMsg struct{ generation uint64 }

func NewModel(ctx context.Context, service Service) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	return Model{ctx: ctx, cancel: cancel, service: service, screen: ReportsScreen, previous: ReportsScreen, focus: ListFocus, theme: terminalui.ThemeClassic, themes: terminalui.DiscoverThemes(), requestID: 1, loading: true, banState: "active", boardState: "all"}
}

func (m Model) WithThemes(catalog terminalui.ThemeCatalog, selected terminalui.ThemeName) Model {
	if len(catalog.Options()) > 0 {
		m.themes = catalog
	}
	if m.themes.Has(selected) {
		m.theme = selected
	}
	return m
}

func (m Model) WithImageViewer(viewer ImageViewer) Model { m.viewer = viewer; return m }

func (m Model) WithImageMode(mode terminalui.InlineMode) Model {
	if viewer, ok := m.viewer.(TerminalImageViewer); ok {
		viewer.Renderer.Mode = mode
		m.viewer = viewer
	}
	return m
}

func (m Model) DetachTerminalImage() (Model, string) {
	cleanup := ""
	if m.viewer != nil {
		cleanup = m.viewer.Clear()
	}
	if m.attachmentView.Backend == "Kitty placeholders" || m.attachmentView.Backend == "Sixel" {
		m.attachmentView.Lines = nil
		m.attachmentView.Backend = ""
	}
	return m, cleanup
}

func (m Model) RefreshTerminalImage() (Model, tea.Cmd) {
	return m, m.renderAttachment()
}

func (m Model) WithSession(session adminclient.Session) Model { m.session = session; return m }

func (m Model) Init() tea.Cmd {
	if m.session.Id == "" {
		return m.sessionCommand(m.requestID, m.ctx)
	}
	switch m.screen {
	case BansScreen:
		return m.bansCommand(m.requestID, m.ctx, nil, false)
	case AuditScreen:
		return m.auditCommand(m.requestID, m.ctx, nil, false)
	case BoardsScreen:
		return m.boardsCommand(m.requestID, m.ctx, nil, false)
	default:
		return m.reportsCommand(m.requestID, m.ctx, nil, false)
	}
}

func (m Model) Close() {
	if m.requestCancel != nil {
		m.requestCancel()
	}
	if m.cancel != nil {
		m.cancel()
	}
}
func (m Model) Screen() Screen              { return m.screen }
func (m Model) ThemeSetting() string        { return m.themes.Setting(m.theme) }
func (m Model) Theme() terminalui.ThemeName { return m.theme }
func (m Model) Role() string                { return string(m.session.Role) }
func (m Model) ImageModeSetting() string {
	if viewer, ok := m.viewer.(TerminalImageViewer); ok {
		return string(viewer.Renderer.Mode)
	}
	return ""
}
func (m Model) ViewSetting() string {
	switch m.screen {
	case BansScreen:
		return "bans"
	case AuditScreen:
		return "audit"
	case BoardsScreen:
		return "boards"
	default:
		return "reports"
	}
}

func (m Model) WithScreen(value string) Model {
	switch value {
	case "bans":
		m.screen = BansScreen
	case "audit":
		m.screen = AuditScreen
	case "boards":
		m.screen = BoardsScreen
	default:
		m.screen = ReportsScreen
	}
	m.previous = m.screen
	return m
}

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resizeModalFields()
		return m, m.renderAttachment()
	case tea.KeyMsg:
		return m.updateKey(msg)
	case tea.MouseMsg:
		return m.updateMouse(msg)
	case sessionMsg:
		if msg.id != m.requestID {
			return m, nil
		}
		m.loading, m.err = false, msg.err
		if msg.err != nil {
			return m, nil
		}
		m.session = msg.value
		return m.startScreenLoad(false)
	case reportsMsg:
		if msg.id != m.requestID {
			return m, nil
		}
		m.loading, m.err = false, msg.err
		if msg.err != nil {
			return m, nil
		}
		m.reports = mergeBy(m.reports, msg.page.Items, msg.append, func(item adminclient.Report) string { return item.Id })
		m.reportsCursor = msg.page.NextCursor
		m.reportIndex = clamp(m.reportIndex, 0, max(0, len(m.reports)-1))
		if len(m.reports) > 0 && !msg.append && m.canModerate() {
			return m.startContextLoad(false)
		}
		m.context = adminclient.PostContext{}
		return m, nil
	case contextMsg:
		if msg.id != m.requestID {
			return m, nil
		}
		m.loading, m.err = false, msg.err
		if msg.err != nil {
			return m, nil
		}
		if msg.append {
			msg.value.Posts = mergeBy(m.context.Posts, msg.value.Posts, true, func(item generated.Post) string { return item.Id })
		}
		m.context = msg.value
		m.contextCursor = msg.value.NextCursor
		m.postIndex = clamp(m.postIndex, 0, max(0, len(m.context.Posts)-1))
		return m, nil
	case bansMsg:
		if msg.id != m.requestID {
			return m, nil
		}
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			m.bans = mergeBy(m.bans, msg.page.Items, msg.append, func(item adminclient.BanRecord) string { return item.Id })
			m.bansCursor = msg.page.NextCursor
			m.banIndex = clamp(m.banIndex, 0, max(0, len(m.bans)-1))
		}
		return m, nil
	case auditMsg:
		if msg.id != m.requestID {
			return m, nil
		}
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			m.audit = mergeBy(m.audit, msg.page.Items, msg.append, func(item adminclient.AuditEntry) string { return item.Id })
			m.auditCursor = msg.page.NextCursor
			m.auditIndex = clamp(m.auditIndex, 0, max(0, len(m.audit)-1))
		}
		return m, nil
	case boardsMsg:
		if msg.id != m.requestID {
			return m, nil
		}
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			m.boards = mergeBy(m.boards, msg.page.Items, msg.append, func(item adminclient.Board) string { return item.Id })
			m.boardsCursor = msg.page.NextCursor
			m.boardIndex = clamp(m.boardIndex, 0, max(0, len(m.boards)-1))
		}
		return m, nil
	case mutationMsg:
		if msg.id != m.requestID {
			return m, nil
		}
		m.loading, m.err = false, msg.err
		if msg.err != nil {
			if m.modal != nil {
				m.modal.pending = false
				m.modal.err = safeError(msg.err)
			}
			return m, nil
		}
		if msg.kind == modalReveal {
			m.revealedIP = terminalui.SanitizeTerminalText(msg.value)
			if m.modal != nil {
				m.modal.pending = false
				m.modal.confirmed = true
			}
			return m, nil
		}
		m.modal = nil
		m.status = "Операция выполнена; данные обновлены"
		if msg.kind == modalBan {
			m.switchScreen(BansScreen)
			m.banState = "active"
		}
		return m.startScreenLoad(false)
	case attachmentMsg:
		if msg.id != m.requestID {
			return m, nil
		}
		m.loading, m.err = false, msg.err
		if msg.err != nil {
			return m, nil
		}
		if m.viewer == nil {
			m.err = errors.New("просмотрщик вложений недоступен")
			return m, nil
		}
		m.attachmentMeta, m.attachmentBody, m.attachmentType = msg.meta, msg.body, msg.mediaType
		return m, m.renderAttachment()
	case viewerClosedMsg:
		m.err = msg.err
		return m, nil
	case clearTerminalControlMsg:
		if msg.generation == m.terminalControlGeneration {
			m.terminalControl = ""
		}
	}
	return m, nil
}

func (m *Model) renderAttachment() tea.Cmd {
	if m.screen != AttachmentScreen || m.loading || len(m.attachmentBody) == 0 || m.viewer == nil {
		return nil
	}
	view, err := m.viewer.Render(m.attachmentMeta, m.attachmentBody, m.attachmentType, max(1, m.width-4), max(1, m.height-8))
	if err != nil {
		m.err = err
		m.attachmentView = ImageView{}
		return nil
	}
	m.err = nil
	m.attachmentView = view
	return m.queueTerminalControl(view.Control)
}

func (m *Model) clearAttachment() tea.Cmd {
	cleanup := ""
	if m.viewer != nil {
		cleanup = m.viewer.Clear()
	}
	m.attachmentID = ""
	m.attachmentMeta = terminalui.ImageMetadata{}
	m.attachmentBody = nil
	m.attachmentType = ""
	m.attachmentView = ImageView{}
	m.loading = false
	m.err = nil
	return m.queueTerminalControl(cleanup)
}

func (m *Model) queueTerminalControl(control string) tea.Cmd {
	if control == "" {
		return nil
	}
	m.terminalControl = control
	m.terminalControlGeneration++
	generation := m.terminalControlGeneration
	return func() tea.Msg { return clearTerminalControlMsg{generation: generation} }
}

func (m Model) beginRequest() (Model, uint64, context.Context) {
	if m.requestCancel != nil {
		m.requestCancel()
	}
	m.requestID++
	ctx, cancel := context.WithCancel(m.ctx)
	m.requestContext, m.requestCancel = ctx, cancel
	m.loading, m.err = true, nil
	return m, m.requestID, ctx
}

func (m Model) startScreenLoad(appendPage bool) (tea.Model, tea.Cmd) {
	m, id, ctx := m.beginRequest()
	switch m.screen {
	case ReportsScreen:
		var cursor *string
		if appendPage {
			cursor = m.reportsCursor
		} else {
			m.reportsCursor = nil
		}
		return m, m.reportsCommand(id, ctx, cursor, appendPage)
	case BansScreen:
		var cursor *string
		if appendPage {
			cursor = m.bansCursor
		} else {
			m.bansCursor = nil
		}
		return m, m.bansCommand(id, ctx, cursor, appendPage)
	case AuditScreen:
		var cursor *string
		if appendPage {
			cursor = m.auditCursor
		} else {
			m.auditCursor = nil
		}
		return m, m.auditCommand(id, ctx, cursor, appendPage)
	case BoardsScreen:
		var cursor *string
		if appendPage {
			cursor = m.boardsCursor
		} else {
			m.boardsCursor = nil
		}
		return m, m.boardsCommand(id, ctx, cursor, appendPage)
	default:
		m.loading = false
		return m, nil
	}
}

func (m Model) startContextLoad(appendPage bool) (tea.Model, tea.Cmd) {
	if len(m.reports) == 0 {
		return m, nil
	}
	m, id, ctx := m.beginRequest()
	postID := m.reports[m.reportIndex].PostId
	var cursor *string
	if appendPage {
		cursor = m.contextCursor
	} else {
		m.contextCursor = nil
	}
	return m, m.contextCommand(id, ctx, postID, cursor, appendPage)
}

func (m Model) sessionCommand(id uint64, ctx context.Context) tea.Cmd {
	return func() tea.Msg { value, err := m.service.Session(ctx); return sessionMsg{id, value, err} }
}
func (m Model) reportsCommand(id uint64, ctx context.Context, cursor *string, appendPage bool) tea.Cmd {
	return func() tea.Msg {
		page, err := m.service.Reports(ctx, cursor, 50)
		return reportsMsg{id, page, appendPage, err}
	}
}
func (m Model) contextCommand(id uint64, ctx context.Context, postID string, cursor *string, appendPage bool) tea.Cmd {
	return func() tea.Msg {
		value, err := m.service.PostContext(ctx, postID, cursor, 50)
		return contextMsg{id, value, appendPage, err}
	}
}
func (m Model) bansCommand(id uint64, ctx context.Context, cursor *string, appendPage bool) tea.Cmd {
	return func() tea.Msg {
		page, err := m.service.Bans(ctx, m.banState, cursor, 50)
		return bansMsg{id, page, appendPage, err}
	}
}
func (m Model) auditCommand(id uint64, ctx context.Context, cursor *string, appendPage bool) tea.Cmd {
	return func() tea.Msg {
		page, err := m.service.Audit(ctx, cursor, 50)
		return auditMsg{id, page, appendPage, err}
	}
}
func (m Model) boardsCommand(id uint64, ctx context.Context, cursor *string, appendPage bool) tea.Cmd {
	return func() tea.Msg {
		page, err := m.service.Boards(ctx, m.boardState, cursor, 50)
		return boardsMsg{id, page, appendPage, err}
	}
}

func mergeBy[T any](old, incoming []T, appendPage bool, id func(T) string) []T {
	if !appendPage {
		old = nil
	}
	seen := make(map[string]struct{}, len(old)+len(incoming))
	result := make([]T, 0, len(old)+len(incoming))
	for _, list := range [][]T{old, incoming} {
		for _, item := range list {
			if _, ok := seen[id(item)]; ok {
				continue
			}
			seen[id(item)] = struct{}{}
			result = append(result, item)
		}
	}
	return result
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	var apiError *adminclient.Error
	if errors.As(err, &apiError) {
		return terminalui.SanitizeTerminalText(apiError.Error())
	}
	return "Операция не выполнена"
}

func (m Model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.modal != nil {
		return m.updateModal(msg)
	}
	key := msg.String()
	if m.themePicker.IsOpen() {
		return m.updateThemeMenu(key)
	}
	if m.screen == AttachmentScreen {
		switch key {
		case "ctrl+c", "q", "f10":
			m.Close()
			return m, tea.Quit
		case "esc":
			m.screen = m.previous
			return m, m.clearAttachment()
		case "enter":
			if m.viewer != nil && m.attachmentView.Path != "" {
				return m, m.viewer.Open(m.ctx, m.attachmentView.Path, false)
			}
		case "o":
			if m.viewer != nil && m.attachmentView.Path != "" {
				return m, m.viewer.Open(m.ctx, m.attachmentView.Path, true)
			}
		}
		return m, nil
	}
	switch key {
	case "ctrl+c", "q", "f10":
		m.Close()
		return m, tea.Quit
	case "f1", "?":
		m.switchScreen(HelpScreen)
		return m, nil
	case "f2", "t":
		m.themePicker.Open(m.themes, m.theme, m.height)
		return m, nil
	case "1":
		return m.selectScreen(ReportsScreen)
	case "2":
		return m.selectScreen(BansScreen)
	case "3":
		return m.selectScreen(AuditScreen)
	case "4":
		return m.selectScreen(BoardsScreen)
	case "esc":
		if m.screen == HelpScreen {
			return m.selectScreen(m.previous)
		}
	case "tab":
		if m.screen == ReportsScreen {
			if m.focus == ListFocus {
				m.focus = ContextFocus
			} else {
				m.focus = ListFocus
			}
		}
		return m, nil
	case "r", "f5":
		return m.startScreenLoad(false)
	case "up", "k":
		m.moveSelection(-1)
		return m, nil
	case "down", "j":
		m.moveSelection(1)
		return m, nil
	case "pgup":
		m.moveSelection(-max(1, m.height-6))
		return m, nil
	case "pgdown":
		m.moveSelection(max(1, m.height-6))
		return m, nil
	case "enter":
		return m.activateSelection()
	case "h":
		return m.openVisibilityModal(false)
	case "u":
		if m.screen == BoardsScreen {
			return m.openBoardStateModal(true)
		}
		return m.openVisibilityModal(true)
	case "d":
		return m.openBoardStateModal(false)
	case "c":
		return m.openCloseModal()
	case "b":
		return m.openBanModal()
	case "x":
		return m.openRevokeModal()
	case "i":
		return m.openRevealModal()
	case "a":
		return m.openAttachment()
	case "n":
		return m.openCreateBoardModal()
	case "f6":
		if m.screen == BansScreen {
			if m.banState == "active" {
				m.banState = "history"
			} else {
				m.banState = "active"
			}
			return m.startScreenLoad(false)
		}
		if m.screen == BoardsScreen {
			switch m.boardState {
			case "all":
				m.boardState = "active"
			case "active":
				m.boardState = "archived"
			default:
				m.boardState = "all"
			}
			return m.startScreenLoad(false)
		}
	}
	return m, nil
}

func (m *Model) switchScreen(screen Screen) {
	if m.screen != HelpScreen {
		m.previous = m.screen
	}
	m.screen, m.err, m.status, m.revealedIP = screen, nil, "", ""
	if m.requestCancel != nil {
		m.requestCancel()
	}
}

func (m Model) selectScreen(screen Screen) (tea.Model, tea.Cmd) {
	if screen == BoardsScreen && string(m.session.Role) != "administrator" {
		return m, nil
	}
	m.switchScreen(screen)
	if screen == HelpScreen {
		return m, nil
	}
	return m.startScreenLoad(false)
}

func (m *Model) moveSelection(delta int) {
	switch m.screen {
	case ReportsScreen:
		if m.focus == ListFocus {
			m.reportIndex = clamp(m.reportIndex+delta, 0, max(0, len(m.reports)-1))
		} else {
			m.postIndex = clamp(m.postIndex+delta, 0, max(0, len(m.context.Posts)-1))
		}
	case BansScreen:
		m.banIndex = clamp(m.banIndex+delta, 0, max(0, len(m.bans)-1))
	case AuditScreen:
		m.auditIndex = clamp(m.auditIndex+delta, 0, max(0, len(m.audit)-1))
	case BoardsScreen:
		m.boardIndex = clamp(m.boardIndex+delta, 0, max(0, len(m.boards)-1))
	}
}

func (m Model) activateSelection() (tea.Model, tea.Cmd) {
	if m.screen == ReportsScreen && m.focus == ListFocus && len(m.reports) > 0 {
		if m.reportIndex == len(m.reports)-1 && m.reportsCursor != nil {
			return m.startScreenLoad(true)
		}
		m.focus = ContextFocus
		return m.startContextLoad(false)
	}
	if m.screen == ReportsScreen && m.focus == ContextFocus && m.contextCursor != nil {
		return m.startContextLoad(true)
	}
	var cursor *string
	switch m.screen {
	case ReportsScreen:
		cursor = m.reportsCursor
	case BansScreen:
		cursor = m.bansCursor
	case AuditScreen:
		cursor = m.auditCursor
	case BoardsScreen:
		cursor = m.boardsCursor
	}
	if cursor != nil {
		return m.startScreenLoad(true)
	}
	return m, nil
}

func (m Model) updateThemeMenu(key string) (tea.Model, tea.Cmd) {
	if selected, ok := m.themePicker.HandleKey(key, m.themes, m.height); ok {
		m.theme = selected
	}
	return m, nil
}

func clamp(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func validReason(value string) bool {
	length := len([]rune(strings.TrimSpace(value)))
	return length >= 3 && length <= 500
}

func newReasonEditor() textarea.Model {
	editor := textarea.New()
	editor.Placeholder = "Причина (3–500 символов)…"
	editor.Prompt = ""
	editor.ShowLineNumbers = false
	editor.CharLimit = 500
	editor.SetWidth(56)
	editor.SetHeight(3)
	editor.Focus()
	return editor
}
