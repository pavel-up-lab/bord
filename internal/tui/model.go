package tui

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"bord/api/generated"
	"bord/internal/client"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type Screen uint8

const (
	BoardsScreen Screen = iota
	CatalogScreen
	ThreadScreen
	ComposerScreen
	HelpScreen
	AttachmentScreen
)

type Focus uint8

const (
	FocusBoards Focus = iota
	FocusContent
)

type Service interface {
	Boards(context.Context) ([]generated.Board, error)
	Threads(context.Context, string, *string, int) (generated.ThreadPage, error)
	Posts(context.Context, string, *string, int) (generated.PostPage, error)
	CreateThread(context.Context, string, string, generated.CreateThread) (generated.CreateResult, error)
	CreatePost(context.Context, string, string, generated.CreatePost) (generated.CreateResult, error)
	UploadAttachment(context.Context, string, io.Reader) (generated.Attachment, error)
	Subscribe(context.Context, client.StreamScope, client.StreamCallbacks, client.Backoff) error
}

type KeyMap struct {
	Up, Down, Switch, Open, Back, Refresh, Search, NewThread, Reply, Attachment, PickAttachment, Help, Theme, Quit, Send, Cancel string
}

func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up: "up,k", Down: "down,j", Switch: "tab", Open: "enter", Back: "esc", Refresh: "r,f5", Search: "/,f4",
		NewThread: "n,f7", Reply: "p", Attachment: "o", PickAttachment: "ctrl+o", Help: "?,f1", Quit: "q,ctrl+c,f10", Send: "ctrl+s", Cancel: "esc",
		Theme: "t,f2",
	}
}

type Model struct {
	ctx                                                  context.Context
	cancel                                               context.CancelFunc
	api                                                  Service
	keys                                                 KeyMap
	screen, previous                                     Screen
	focus                                                Focus
	width, height                                        int
	boards                                               []generated.Board
	threads                                              []generated.Thread
	posts                                                []generated.Post
	boardIndex, contentIndex, boardScroll, contentScroll int
	activeThread                                         string
	nextThreadCursor, nextPostCursor                     *string
	loading                                              bool
	searching                                            bool
	searchQuery                                          string
	attachmentPicking                                    bool
	attachmentPath                                       string
	streamCancel                                         context.CancelFunc
	requestCancel                                        context.CancelFunc
	requestID                                            uint64
	err                                                  error
	editor                                               textarea.Model
	draft                                                Draft
	submitted                                            *submission
	catalogBoard                                         string
	preferredBoard                                       string
	theme                                                ThemeName
}

type Draft struct {
	Board, Thread, AttachmentID, IdempotencyKey string
	ReplyTo                                     *string
}

type submission struct {
	board, thread, attachment, replyTo, body string
}

type boardsMsg struct {
	id     uint64
	boards []generated.Board
	err    error
}
type threadsMsg struct {
	id     uint64
	board  string
	page   generated.ThreadPage
	append bool
	err    error
}
type postsMsg struct {
	id     uint64
	page   generated.PostPage
	append bool
	err    error
}
type sentMsg struct {
	id     uint64
	result generated.CreateResult
	err    error
}
type ReconcileMsg struct{ Board, Thread string }

// AttachmentSelectedMsg is emitted by a non-blocking file/upload command.
type AttachmentSelectedMsg struct {
	ID        string
	Err       error
	requestID uint64
}

func NewModel(ctx context.Context, api Service) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	editor := textarea.New()
	editor.Placeholder = "Введите текст публикации…"
	editor.CharLimit = 10000
	editor.SetWidth(60)
	editor.SetHeight(8)
	return Model{ctx: ctx, cancel: cancel, api: api, keys: DefaultKeyMap(), screen: BoardsScreen, focus: FocusBoards, editor: editor, requestID: 1, loading: true, theme: ThemeClassic}
}

// WithTheme selects a validated presentation theme.
func (m Model) WithTheme(name ThemeName) Model {
	if _, err := ParseTheme(string(name)); err == nil {
		m.theme = name
	}
	return m
}

// WithLastBoard selects the persisted board when it is present in the first response.
func (m Model) WithLastBoard(slug string) Model {
	m.preferredBoard = slug
	return m
}

func (m Model) Init() tea.Cmd { return m.loadBoards() }

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, msg.Width), max(1, msg.Height)
		m.editor.SetWidth(max(1, msg.Width-6))
		m.editor.SetHeight(max(1, msg.Height-10))
		return m, nil
	case tea.KeyMsg:
		return m.updateKey(msg)
	case tea.MouseMsg:
		return m.updateMouse(msg)
	case boardsMsg:
		if msg.id != m.requestID {
			return m, nil
		}
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			selected := selectedBoard(m.boards, m.boardIndex)
			if selected == "" {
				selected = m.preferredBoard
			}
			m.boards = msg.boards
			m.boardIndex = indexBoard(m.boards, selected, clamp(m.boardIndex, 0, len(m.boards)-1))
		}
		if len(m.boards) > 0 && m.screen == BoardsScreen {
			m.screen = CatalogScreen
			return m, m.loadThreads(false)
		}
	case threadsMsg:
		if msg.id != m.requestID {
			return m, nil
		}
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			selected, top := selectedThread(m.threads, m.contentIndex), selectedThread(m.threads, m.contentScroll)
			preserve := msg.board == m.catalogBoard
			m.threads = mergeThreads(m.threads, msg.page.Threads, msg.append)
			if preserve {
				m.contentIndex, m.contentScroll = restoreThreadPosition(m.threads, selected, top, m.contentIndex, m.contentScroll)
			} else {
				m.contentIndex, m.contentScroll = 0, 0
			}
			m.catalogBoard = msg.board
			m.nextThreadCursor = msg.page.NextCursor
			cmd := m.watchEvents()
			return m, cmd
		}
	case postsMsg:
		if msg.id != m.requestID {
			return m, nil
		}
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			selected, top := selectedPost(m.posts, m.contentIndex), selectedPost(m.posts, m.contentScroll)
			m.posts = mergePosts(m.posts, msg.page.Posts, msg.append)
			m.contentIndex, m.contentScroll = restorePostPosition(m.posts, selected, top, m.contentIndex, m.contentScroll)
			m.nextPostCursor = msg.page.NextCursor
			cmd := m.watchEvents()
			return m, cmd
		}
	case sentMsg:
		if msg.id != m.requestID {
			return m, nil
		}
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			m.editor.Reset()
			m.draft = Draft{}
			m.submitted = nil
			m.screen = ThreadScreen
			m.activeThread = msg.result.ThreadId
			return m, m.loadPosts(false)
		}
	case ReconcileMsg:
		if msg.Thread != "" && msg.Thread == m.activeThread {
			return m, m.loadPosts(false)
		}
		if msg.Board != "" && m.activeThread == "" && len(m.boards) > 0 && msg.Board == m.boards[m.boardIndex].Slug {
			return m, m.loadThreads(false)
		}
	case AttachmentSelectedMsg:
		if msg.requestID != 0 && msg.requestID != m.requestID {
			return m, nil
		}
		if m.screen == ComposerScreen {
			m.loading = false
			m.err = msg.Err
			if msg.Err == nil {
				m.draft.AttachmentID = msg.ID
			}
		}
	}
	return m, nil
}

func (m Model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if matches(key, m.keys.Quit) && (m.screen != ComposerScreen || key == "ctrl+c" || key == "f10") {
		m.cancel()
		if m.streamCancel != nil {
			m.streamCancel()
		}
		if m.requestCancel != nil {
			m.requestCancel()
		}
		return m, tea.Quit
	}
	if matches(key, m.keys.Theme) && (m.screen != ComposerScreen || key == "f2") {
		m.theme = nextTheme(m.theme)
		return m, nil
	}
	if m.screen == ComposerScreen {
		if m.attachmentPicking {
			switch key {
			case "esc":
				m.attachmentPicking, m.attachmentPath = false, ""
			case "enter":
				m.attachmentPicking = false
				return m, m.uploadAttachment(m.attachmentPath)
			case "backspace":
				if len(m.attachmentPath) > 0 {
					runes := []rune(m.attachmentPath)
					m.attachmentPath = string(runes[:len(runes)-1])
				}
			default:
				if msg.Type == tea.KeyRunes {
					m.attachmentPath += string(msg.Runes)
				}
			}
			return m, nil
		}
		switch {
		case key == "f1":
			m.previous, m.screen = m.screen, HelpScreen
			return m, nil
		case matches(key, m.keys.Cancel):
			m.cancelRequest()
			m.screen = m.previous
			m.err = nil
			return m, nil
		case matches(key, m.keys.Send):
			if m.loading {
				return m, nil
			}
			return m, m.sendDraft()
		case matches(key, m.keys.PickAttachment):
			m.attachmentPicking, m.attachmentPath, m.err = true, "", nil
			return m, nil
		}
		var cmd tea.Cmd
		m.editor, cmd = m.editor.Update(msg)
		return m, cmd
	}
	if m.screen == HelpScreen {
		if key == "?" || key == "esc" {
			m.screen = m.previous
		}
		return m, nil
	}
	if m.searching {
		switch key {
		case "esc":
			m.searching, m.searchQuery = false, ""
		case "enter":
			m.searching = false
			m.selectSearchMatch()
		case "backspace":
			if len(m.searchQuery) > 0 {
				runes := []rune(m.searchQuery)
				m.searchQuery = string(runes[:len(runes)-1])
			}
		default:
			if msg.Type == tea.KeyRunes {
				m.searchQuery += string(msg.Runes)
			}
		}
		return m, nil
	}
	switch {
	case matches(key, m.keys.Help):
		m.previous, m.screen = m.screen, HelpScreen
	case matches(key, m.keys.Switch):
		if m.focus == FocusBoards {
			m.focus = FocusContent
		} else {
			m.focus = FocusBoards
		}
	case matches(key, m.keys.Back):
		if m.screen == ThreadScreen || m.screen == AttachmentScreen {
			m.screen, m.activeThread, m.posts = CatalogScreen, "", nil
		} else if m.isNarrow() && m.focus == FocusContent {
			m.focus = FocusBoards
		}
	case matches(key, m.keys.Up):
		m.move(-1)
	case matches(key, m.keys.Down):
		wasLast := (m.screen == ThreadScreen && m.contentIndex == len(m.posts)-1) || (m.screen == CatalogScreen && m.contentIndex == len(m.threads)-1)
		m.move(1)
		if wasLast && m.focus == FocusContent {
			if m.screen == ThreadScreen && m.nextPostCursor != nil {
				return m, m.loadPosts(true)
			}
			if m.screen == CatalogScreen && m.nextThreadCursor != nil {
				return m, m.loadThreads(true)
			}
		}
	case matches(key, m.keys.Refresh):
		if m.activeThread != "" {
			return m, m.loadPosts(false)
		}
		return m, m.loadThreads(false)
	case matches(key, m.keys.Open):
		return m.openSelection()
	case matches(key, m.keys.NewThread):
		if len(m.boards) > 0 {
			m.beginComposer("")
		}
	case matches(key, m.keys.Reply):
		if m.activeThread != "" {
			m.beginComposer(m.activeThread)
		}
	case matches(key, m.keys.Attachment):
		if m.screen == ThreadScreen {
			m.previous, m.screen = m.screen, AttachmentScreen
		}
	case matches(key, m.keys.Search):
		m.searching, m.searchQuery, m.err = true, "", nil
	}
	return m, nil
}

func (m *Model) beginComposer(thread string) {
	board := ""
	if len(m.boards) > 0 {
		board = m.boards[m.boardIndex].Slug
	}
	if m.draft.Board != board || m.draft.Thread != thread {
		m.editor.Reset()
		m.draft = Draft{Board: board, Thread: thread}
		m.submitted = nil
	}
	m.previous, m.screen = m.screen, ComposerScreen
	if m.draft.IdempotencyKey == "" {
		key, err := newKey()
		if err != nil {
			m.err = err
			return
		}
		m.draft.IdempotencyKey = key
	}
	m.editor.Focus()
}

func (m Model) openSelection() (tea.Model, tea.Cmd) {
	if m.focus == FocusBoards && len(m.boards) > 0 {
		m.screen, m.focus = CatalogScreen, FocusContent
		return m, m.loadThreads(false)
	}
	if m.screen == CatalogScreen && len(m.threads) > 0 {
		m.activeThread, m.screen = m.threads[m.contentIndex].Id, ThreadScreen
		m.contentIndex, m.contentScroll = 0, 0
		return m, m.loadPosts(false)
	}
	return m, nil
}

func (m *Model) move(delta int) {
	if m.focus == FocusBoards {
		m.boardIndex = clamp(m.boardIndex+delta, 0, len(m.boards)-1)
		m.boardScroll = ensureVisible(m.boardScroll, m.boardIndex, m.visibleRows())
		m.screen = CatalogScreen
		return
	}
	length := len(m.threads)
	if m.screen == ThreadScreen {
		length = len(m.posts)
	}
	m.contentIndex = clamp(m.contentIndex+delta, 0, length-1)
	m.contentScroll = ensureVisible(m.contentScroll, m.contentIndex, m.visibleRows())
}

func (m *Model) nextRequest() (uint64, context.Context, context.CancelFunc) {
	if m.streamCancel != nil {
		m.streamCancel()
		m.streamCancel = nil
	}
	if m.requestCancel != nil {
		m.requestCancel()
	}
	m.requestID++
	m.loading = true
	m.err = nil
	ctx, cancel := context.WithCancel(m.ctx)
	m.requestCancel = cancel
	return m.requestID, ctx, cancel
}

func (m *Model) cancelRequest() {
	if m.requestCancel != nil {
		m.requestCancel()
		m.requestCancel = nil
		m.requestID++
		m.loading = false
	}
}
func (m Model) loadBoards() tea.Cmd {
	id := m.requestID
	return func() tea.Msg { v, err := m.api.Boards(m.ctx); return boardsMsg{id, v, err} }
}
func (m *Model) loadThreads(appendPage bool) tea.Cmd {
	id, ctx, cancel := m.nextRequest()
	board := ""
	if len(m.boards) > 0 {
		board = m.boards[m.boardIndex].Slug
	}
	var cursor *string
	if appendPage {
		cursor = m.nextThreadCursor
	}
	return func() tea.Msg {
		defer cancel()
		v, err := m.api.Threads(ctx, board, cursor, 50)
		return threadsMsg{id, board, v, appendPage, err}
	}
}
func (m *Model) loadPosts(appendPage bool) tea.Cmd {
	id, ctx, cancel := m.nextRequest()
	thread := m.activeThread
	var cursor *string
	if appendPage {
		cursor = m.nextPostCursor
	}
	return func() tea.Msg {
		defer cancel()
		v, err := m.api.Posts(ctx, thread, cursor, 100)
		return postsMsg{id, v, appendPage, err}
	}
}
func (m *Model) sendDraft() tea.Cmd {
	text := m.editor.Value()
	candidate := submissionFor(m.draft, text)
	if m.draft.IdempotencyKey == "" || (m.submitted != nil && *m.submitted != candidate) {
		key, err := newKey()
		if err != nil {
			m.err = err
			return nil
		}
		m.draft.IdempotencyKey = key
	}
	candidate = submissionFor(m.draft, text)
	m.submitted = &candidate
	draft := m.draft
	id, ctx, cancel := m.nextRequest()
	return func() tea.Msg {
		defer cancel()
		body := &text
		if draft.Thread == "" {
			v, err := m.api.CreateThread(ctx, draft.Board, draft.IdempotencyKey, generated.CreateThread{Body: body, AttachmentId: optional(draft.AttachmentID)})
			return sentMsg{id, v, err}
		}
		v, err := m.api.CreatePost(ctx, draft.Thread, draft.IdempotencyKey, generated.CreatePost{Body: body, AttachmentId: optional(draft.AttachmentID), ReplyToId: draft.ReplyTo})
		return sentMsg{id, v, err}
	}
}

func (m *Model) uploadAttachment(path string) tea.Cmd {
	path = filepath.Clean(path)
	id, ctx, cancel := m.nextRequest()
	return func() tea.Msg {
		defer cancel()
		file, err := os.Open(path)
		if err != nil {
			return AttachmentSelectedMsg{Err: errors.New("не удалось открыть вложение"), requestID: id}
		}
		defer file.Close()
		header := make([]byte, 512)
		n, err := io.ReadFull(file, header)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			return AttachmentSelectedMsg{Err: errors.New("не удалось прочитать вложение"), requestID: id}
		}
		mediaType := http.DetectContentType(header[:n])
		if mediaType != "image/jpeg" && mediaType != "image/png" {
			return AttachmentSelectedMsg{Err: errors.New("поддерживаются только JPEG и PNG"), requestID: id}
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return AttachmentSelectedMsg{Err: errors.New("не удалось прочитать вложение"), requestID: id}
		}
		attachment, err := m.api.UploadAttachment(ctx, mediaType, file)
		if err != nil {
			return AttachmentSelectedMsg{Err: err, requestID: id}
		}
		return AttachmentSelectedMsg{ID: attachment.Id, requestID: id}
	}
}

func (m *Model) watchEvents() tea.Cmd {
	if m.streamCancel != nil {
		m.streamCancel()
	}
	scope := client.StreamScope{}
	if m.activeThread != "" {
		scope.Thread = m.activeThread
	} else if len(m.boards) > 0 {
		scope.Board = m.boards[m.boardIndex].Slug
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.streamCancel = cancel
	return func() tea.Msg {
		defer cancel()
		triggered := false
		trigger := func() {
			if !triggered {
				triggered = true
				cancel()
			}
		}
		_ = m.api.Subscribe(ctx, scope, client.StreamCallbacks{Event: func(client.Event) { trigger() }, Reset: trigger}, client.DefaultBackoff())
		if !triggered {
			return nil
		}
		return ReconcileMsg{Board: scope.Board, Thread: scope.Thread}
	}
}

func (m Model) View() string {
	w, h := max(1, m.width), max(1, m.height)
	colors := palette(m.theme)
	lines := make([]string, 0, h)
	lines = append(lines, m.topBar(colors, w))
	workspaceHeight := max(0, h-3)
	if workspaceHeight > 0 {
		lines = append(lines, m.workspace(colors, w, workspaceHeight)...)
	}
	if len(lines) < h-1 {
		lines = append(lines, m.statusLine(colors, w))
	}
	if len(lines) < h {
		lines = append(lines, m.commandBar(colors, w))
	}
	for len(lines) < h {
		lines = append(lines, paint(colors.background, colors.foreground, "", w))
	}
	return strings.Join(lines[:h], "\n")
}

func (m Model) isNarrow() bool { return m.width > 0 && m.width < 72 }

func (m Model) contentTitle() string {
	if m.screen == ThreadScreen {
		return "Тред " + SanitizeTerminalText(m.activeThread)
	}
	if m.screen == AttachmentScreen {
		return "Вложение"
	}
	return "Каталог"
}

func (m Model) statusText() string {
	if m.searching {
		return " Поиск: " + SanitizeTerminalText(m.searchQuery) + "_  Enter применить • Esc отменить"
	}
	if m.loading {
		if m.screen == ComposerScreen {
			return " Отправка публикации…"
		}
		return " Загрузка…"
	}
	if m.err != nil {
		var apiErr *client.APIError
		if errorsAs(m.err, &apiErr) {
			return " Ошибка отправки: " + SanitizeTerminalText(string(apiErr.Code)) + " • Ctrl+S повторить"
		}
		if m.screen == ComposerScreen {
			return " Ошибка отправки • Ctrl+S повторить • Esc сохранить черновик"
		}
		return " Ошибка сети"
	}
	return fmt.Sprintf(" %s • %s • тема:%s • Tab панели • ? справка", m.location(), m.selectionCount(), m.theme)
}

func (m Model) topBar(t theme, width int) string {
	left := m.topBarLeft()
	right := " " + m.contentTitle() + " "
	space := width - textWidth(left) - textWidth(right)
	if space < 1 {
		return paint(t.barBackground, t.barForeground, left, width)
	}
	return paint(t.barBackground, t.barForeground, left+strings.Repeat(" ", space)+right, width)
}

func (m Model) topBarLeft() string {
	left := " BORD  "
	if m.canGoBack() {
		left += "[← Назад] "
	}
	return left + "[Тема] [Справка]"
}

func (m Model) workspace(t theme, width, height int) []string {
	if m.screen == ComposerScreen {
		return m.singlePanel(t, width, height, "Редактор", "ТЕКСТ ПУБЛИКАЦИИ", m.composerLines(), true)
	}
	if m.screen == HelpScreen {
		return m.singlePanel(t, width, height, "Справка", "КЛАВИША  ДЕЙСТВИЕ", m.helpLines(), true)
	}
	if m.isNarrow() {
		if m.focus == FocusBoards {
			return m.singlePanel(t, width, height, "Доски", "РАЗДЕЛ / НАЗВАНИЕ", m.boardRows(), true)
		}
		return m.singlePanel(t, width, height, m.contentTitle(), m.contentHint(), m.contentRows(), true)
	}
	leftWidth := m.leftPanelWidth()
	rightWidth := width - leftWidth
	left := m.singlePanel(t, leftWidth, height, "Доски", "РАЗДЕЛ / НАЗВАНИЕ", m.boardRows(), m.focus == FocusBoards)
	right := m.singlePanel(t, rightWidth, height, m.contentTitle(), m.contentHint(), m.contentRows(), m.focus == FocusContent)
	joined := make([]string, height)
	for i := range joined {
		joined[i] = left[i] + right[i]
	}
	return joined
}

type displayRow struct {
	lines       []string
	active      bool
	trustedANSI bool
}

func (m Model) singlePanel(t theme, width, height int, title, hint string, rows []displayRow, active bool) []string {
	if height <= 0 {
		return nil
	}
	border := t.muted
	if active {
		border = t.accent
	}
	if width < 2 || height < 2 {
		lines := make([]string, height)
		for i := range lines {
			value := ""
			if i == 0 {
				value = title
			}
			lines[i] = paint(t.background, t.foreground, value, width)
		}
		return lines
	}
	inner := width - 2
	topText := "═ " + SanitizeTerminalText(title) + " "
	top := paint(t.background, border, "╔"+fit(topText, inner, '═')+"╗", width)
	bottom := paint(t.background, border, "╚"+strings.Repeat("═", inner)+"╝", width)
	lines := []string{top}
	if height > 2 {
		lines = append(lines, paint(t.background, border, "║", 1)+paint(t.barBackground, t.barForeground, " "+hint, inner)+paint(t.background, border, "║", 1))
	}
	available := height - len(lines) - 1
	for _, row := range rows {
		for _, text := range row.lines {
			if available <= 0 {
				break
			}
			bg, fg := t.background, t.foreground
			if row.active {
				if active {
					bg, fg = t.selectionBackground, t.selection
				} else {
					bg, fg = t.background, t.accent
				}
			}
			rowPaint := paint
			if row.trustedANSI {
				rowPaint = paintANSI
			}
			lines = append(lines, paint(t.background, border, "║", 1)+rowPaint(bg, fg, " "+text, inner)+paint(t.background, border, "║", 1))
			available--
		}
		if available <= 0 {
			break
		}
	}
	for available > 0 {
		lines = append(lines, paint(t.background, border, "║", 1)+paint(t.background, t.foreground, "", inner)+paint(t.background, border, "║", 1))
		available--
	}
	return append(lines, bottom)
}

func (m Model) boardRows() []displayRow {
	end := minInt(len(m.boards), m.boardScroll+m.visibleRows())
	rows := make([]displayRow, 0, end-m.boardScroll)
	for i := m.boardScroll; i < end; i++ {
		board := m.boards[i]
		marker := "  "
		if i == m.boardIndex {
			marker = "> "
		}
		rows = append(rows, displayRow{lines: []string{fmt.Sprintf("%s/%s/  %s", marker, SanitizeTerminalText(board.Slug), SanitizeTerminalText(board.Title))}, active: i == m.boardIndex})
	}
	return rows
}

func (m Model) contentHint() string {
	if m.screen == ThreadScreen {
		return "POST / AUTHOR / TIME"
	}
	if m.screen == AttachmentScreen {
		return "ВЛОЖЕНИЕ"
	}
	return "THREAD / UPDATED / PREVIEW"
}

func (m Model) contentRows() []displayRow {
	if m.screen == AttachmentScreen {
		return []displayRow{{lines: []string{"Enter  открыть системным приложением", "Esc    вернуться к треду"}}}
	}
	rows := make([]displayRow, 0, m.visibleRows())
	if m.screen == ThreadScreen {
		end := minInt(len(m.posts), m.contentScroll+m.visibleRows())
		for i := m.contentScroll; i < end; i++ {
			p := m.posts[i]
			marker := "  "
			if i == m.contentIndex {
				marker = "> "
			}
			meta := fmt.Sprintf("%s>>%s  %-12s  %s", marker, SanitizeTerminalText(p.Id), SanitizeTerminalText(p.AuthorId), p.CreatedAt.Format("2006-01-02 15:04"))
			if p.ReplyToId != nil {
				meta += "  → >>" + SanitizeTerminalText(*p.ReplyToId)
			}
			body := "[" + SanitizeTerminalText(string(p.State)) + "]"
			if p.State == generated.Visible && p.Body != nil {
				body = preview(*p.Body)
			}
			rows = append(rows, displayRow{lines: []string{meta, "  " + body}, active: i == m.contentIndex})
		}
		return rows
	}
	end := minInt(len(m.threads), m.contentScroll+m.visibleRows())
	for i := m.contentScroll; i < end; i++ {
		t := m.threads[i]
		marker := "  "
		if i == m.contentIndex {
			marker = "> "
		}
		state := " "
		if t.Closed {
			state = "×"
		}
		meta := fmt.Sprintf("%s%s %-18s  %s", marker, state, SanitizeTerminalText(t.Id), t.UpdatedAt.Format("2006-01-02 15:04"))
		body := "(нет текста)"
		if t.InitialPost != nil && t.InitialPost.Body != nil {
			body = preview(*t.InitialPost.Body)
		}
		rows = append(rows, displayRow{lines: []string{meta, "  " + body}, active: i == m.contentIndex})
	}
	return rows
}

func (m Model) composerLines() []displayRow {
	if m.attachmentPicking {
		return []displayRow{{lines: []string{"Путь: " + SanitizeTerminalText(m.attachmentPath) + "_", "", "Enter загрузить • Esc отменить"}}}
	}
	attachment := "нет"
	if m.draft.AttachmentID != "" {
		attachment = SanitizeTerminalText(m.draft.AttachmentID)
	}
	lines := strings.Split(m.editor.View(), "\n")
	action := "Ctrl+S ОТПРАВИТЬ • Ctrl+O вложение • Esc оставить черновик"
	if m.loading {
		action = "ОТПРАВКА… дождитесь результата • Esc отменить запрос"
	} else if m.err != nil {
		action = "ОШИБКА • Ctrl+S ПОВТОРИТЬ • Esc оставить черновик"
	}
	lines = append(lines, "", "Команды ниже также доступны мышью", action, "Вложение: "+attachment)
	return []displayRow{{lines: lines, trustedANSI: true}}
}

func (m Model) helpLines() []displayRow {
	return []displayRow{{lines: []string{
		"↑/k ↓/j   навигация и дозагрузка", "Tab        сменить панель", "Enter      открыть", "Esc        назад / закрыть",
		"n / p      новый тред / ответ", "r /        обновить / поиск", "o Ctrl+O   открыть / прикрепить", "t          следующая тема",
		"?          эта справка", "q Ctrl+C   выход", "Мышь       выбор; повторный щелчок открыть", "Колесо     прокрутка панели под указателем",
	}}}
}

func (m Model) statusLine(t theme, width int) string {
	return paint(t.background, t.foreground, m.statusText(), width)
}

func (m Model) commandBar(t theme, width int) string {
	commands := m.commandLabels()
	plain := strings.Join(commands, " ")
	if textWidth(plain)+len(commands)-1 > width {
		return paint(t.barBackground, t.barForeground, plain, width)
	}
	var b strings.Builder
	for i, command := range commands {
		parts := strings.SplitN(command, " ", 2)
		b.WriteString(lipgloss.NewStyle().Background(t.accent).Foreground(t.selection).Render(parts[0]))
		if len(parts) == 2 {
			b.WriteString(lipgloss.NewStyle().Background(t.barBackground).Foreground(t.barForeground).Render(" " + parts[1] + " "))
		}
		if i < len(commands)-1 {
			b.WriteString(lipgloss.NewStyle().Background(t.background).Render(" "))
		}
	}
	return b.String() + paint(t.background, t.foreground, "", max(0, width-lipgloss.Width(b.String())))
}

func (m Model) commandLabels() []string {
	if m.screen == ComposerScreen {
		return []string{"Ctrl+S Send", "Ctrl+O Attach", "Esc Cancel"}
	}
	if m.screen == HelpScreen {
		return []string{"Esc Back", "F2 Theme", "F10 Quit"}
	}
	return []string{"F1 Help", "F2 Theme", "F4 Search", "F5 Refresh", "F7 New", "F10 Quit"}
}

func (m Model) location() string {
	if len(m.boards) == 0 {
		return "BORD"
	}
	return "/" + SanitizeTerminalText(m.boards[m.boardIndex].Slug) + "/ " + m.contentTitle()
}

func (m Model) selectionCount() string {
	if m.screen == ThreadScreen {
		return fmt.Sprintf("%d posts", len(m.posts))
	}
	return fmt.Sprintf("%d threads", len(m.threads))
}

func preview(value string) string {
	value = sanitizeMultilineTerminalText(value)
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return "(пусто)"
	}
	return value
}

func textWidth(value string) int { return lipgloss.Width(value) }

func fit(value string, width int, fill rune) string {
	return fitWidth(SanitizeTerminalText(value), width, fill)
}

func fitANSI(value string, width int, fill rune) string {
	return fitWidth(value, width, fill)
}

func fitWidth(value string, width int, fill rune) string {
	if width <= 0 {
		return ""
	}
	value = ansi.Truncate(value, width, "…")
	used := textWidth(value)
	if used < width {
		value += strings.Repeat(string(fill), width-used)
	}
	return value
}

func paint(background, foreground lipgloss.Color, value string, width int) string {
	return lipgloss.NewStyle().Background(background).Foreground(foreground).Render(fit(value, width, ' '))
}

func paintANSI(background, foreground lipgloss.Color, value string, width int) string {
	return lipgloss.NewStyle().Background(background).Foreground(foreground).Render(fitANSI(value, width, ' '))
}
func mergePosts(old, incoming []generated.Post, appendPage bool) []generated.Post {
	if !appendPage {
		old = nil
	}
	byID := map[string]generated.Post{}
	for _, p := range old {
		byID[p.Id] = p
	}
	for _, p := range incoming {
		byID[p.Id] = p
	}
	result := make([]generated.Post, 0, len(byID))
	for _, p := range byID {
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].Id < result[j].Id
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result
}
func mergeThreads(old, incoming []generated.Thread, appendPage bool) []generated.Thread {
	if !appendPage {
		old = nil
	}
	byID := map[string]generated.Thread{}
	for _, t := range old {
		byID[t.Id] = t
	}
	for _, t := range incoming {
		byID[t.Id] = t
	}
	result := make([]generated.Thread, 0, len(byID))
	for _, t := range byID {
		result = append(result, t)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].UpdatedAt.Equal(result[j].UpdatedAt) {
			return result[i].Id < result[j].Id
		}
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	return result
}
func newKey() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", errors.New("не удалось создать ключ безопасного повтора")
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func optional(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func submissionFor(draft Draft, body string) submission {
	replyTo := ""
	if draft.ReplyTo != nil {
		replyTo = *draft.ReplyTo
	}
	return submission{board: draft.Board, thread: draft.Thread, attachment: draft.AttachmentID, replyTo: replyTo, body: body}
}

func selectedBoard(boards []generated.Board, index int) string {
	if index >= 0 && index < len(boards) {
		return boards[index].Slug
	}
	return ""
}

func indexBoard(boards []generated.Board, slug string, fallback int) int {
	for i := range boards {
		if boards[i].Slug == slug {
			return i
		}
	}
	return fallback
}

func selectedThread(threads []generated.Thread, index int) string {
	if index >= 0 && index < len(threads) {
		return threads[index].Id
	}
	return ""
}

func restoreThreadPosition(threads []generated.Thread, selected, top string, oldIndex, oldScroll int) (int, int) {
	index, scroll := clamp(oldIndex, 0, len(threads)-1), clamp(oldScroll, 0, len(threads)-1)
	for i := range threads {
		if threads[i].Id == selected {
			index = i
		}
		if threads[i].Id == top {
			scroll = i
		}
	}
	return index, scroll
}

func selectedPost(posts []generated.Post, index int) string {
	if index >= 0 && index < len(posts) {
		return posts[index].Id
	}
	return ""
}

func restorePostPosition(posts []generated.Post, selected, top string, oldIndex, oldScroll int) (int, int) {
	index, scroll := clamp(oldIndex, 0, len(posts)-1), clamp(oldScroll, 0, len(posts)-1)
	for i := range posts {
		if posts[i].Id == selected {
			index = i
		}
		if posts[i].Id == top {
			scroll = i
		}
	}
	return index, scroll
}
func clamp(v, low, high int) int {
	if high < low {
		return low
	}
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func matches(key, binding string) bool {
	for _, candidate := range strings.Split(binding, ",") {
		if key == candidate {
			return true
		}
	}
	return false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func (m Model) visibleRows() int { return max(1, (m.height-6)/2) }
func ensureVisible(scroll, index, rows int) int {
	if index < scroll {
		return index
	}
	if index >= scroll+rows {
		return index - rows + 1
	}
	return scroll
}

func (m *Model) selectSearchMatch() {
	query := strings.ToLower(strings.TrimSpace(m.searchQuery))
	if query == "" {
		return
	}
	if m.screen == ThreadScreen {
		for i, post := range m.posts {
			body := ""
			if post.Body != nil {
				body = *post.Body
			}
			if strings.Contains(strings.ToLower(post.Id+" "+post.AuthorId+" "+body), query) {
				m.contentIndex, m.contentScroll = i, ensureVisible(m.contentScroll, i, m.visibleRows())
				return
			}
		}
	} else {
		for i, thread := range m.threads {
			body := thread.Id
			if thread.InitialPost != nil && thread.InitialPost.Body != nil {
				body += " " + *thread.InitialPost.Body
			}
			if strings.Contains(strings.ToLower(body), query) {
				m.contentIndex, m.contentScroll = i, ensureVisible(m.contentScroll, i, m.visibleRows())
				return
			}
		}
	}
	m.err = errors.New("search match not found")
}

func (m Model) LastBoard() string {
	if len(m.boards) == 0 {
		return ""
	}
	return m.boards[m.boardIndex].Slug
}

// Theme returns the current stable theme name for persistence.
func (m Model) Theme() ThemeName { return m.theme }

// errorsAs is kept tiny so View never risks exposing the wrapped error text.
func errorsAs(err error, target any) bool { return errors.As(err, target) }
