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
	Up, Down, Switch, Open, Back, Refresh, Search, NewThread, Reply, Attachment, PickAttachment, Help, Quit, Send, Cancel string
}

func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up: "up,k", Down: "down,j", Switch: "tab", Open: "enter", Back: "esc", Refresh: "r", Search: "/",
		NewThread: "n", Reply: "p", Attachment: "o", PickAttachment: "ctrl+o", Help: "?", Quit: "q,ctrl+c", Send: "ctrl+s", Cancel: "esc",
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
	return Model{ctx: ctx, cancel: cancel, api: api, keys: DefaultKeyMap(), screen: BoardsScreen, focus: FocusBoards, editor: editor, requestID: 1, loading: true}
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
		m.width, m.height = msg.Width, msg.Height
		m.editor.SetWidth(max(20, msg.Width-6))
		return m, nil
	case tea.KeyMsg:
		return m.updateKey(msg)
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
	if matches(key, m.keys.Quit) && (m.screen != ComposerScreen || key == "ctrl+c") {
		m.cancel()
		if m.streamCancel != nil {
			m.streamCancel()
		}
		if m.requestCancel != nil {
			m.requestCancel()
		}
		return m, tea.Quit
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
		case matches(key, m.keys.Cancel):
			m.cancelRequest()
			m.screen = m.previous
			m.err = nil
			return m, nil
		case matches(key, m.keys.Send):
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
	if m.screen == ComposerScreen {
		return m.composerView()
	}
	if m.screen == HelpScreen {
		return m.helpView()
	}
	left := m.boardsView()
	right := m.contentView()
	if m.isNarrow() {
		if m.focus == FocusBoards {
			return frame("Доски", left, true, m.width) + m.status()
		}
		return frame(m.contentTitle(), right, true, m.width) + m.status()
	}
	lw := max(22, m.width/3)
	rw := max(30, m.width-lw)
	return lipgloss.JoinHorizontal(lipgloss.Top, frame("Доски", left, m.focus == FocusBoards, lw), frame(m.contentTitle(), right, m.focus == FocusContent, rw)) + m.status()
}

func (m Model) isNarrow() bool { return m.width > 0 && m.width < 72 }
func (m Model) boardsView() string {
	var b strings.Builder
	end := minInt(len(m.boards), m.boardScroll+m.visibleRows())
	for i := m.boardScroll; i < end; i++ {
		board := m.boards[i]
		marker := "  "
		if i == m.boardIndex {
			marker = "> "
		}
		fmt.Fprintf(&b, "%s/%s/ %s\n", marker, SanitizeTerminalText(board.Slug), SanitizeTerminalText(board.Title))
	}
	return b.String()
}
func (m Model) contentTitle() string {
	if m.screen == ThreadScreen {
		return "Тред " + SanitizeTerminalText(m.activeThread)
	}
	if m.screen == AttachmentScreen {
		return "Вложение"
	}
	return "Каталог"
}
func (m Model) contentView() string {
	if m.screen == AttachmentScreen {
		return "Enter: открыть системным приложением\nEsc: назад"
	}
	var b strings.Builder
	if m.screen == ThreadScreen {
		end := minInt(len(m.posts), m.contentScroll+m.visibleRows())
		for i := m.contentScroll; i < end; i++ {
			p := m.posts[i]
			marker := "  "
			if i == m.contentIndex {
				marker = "> "
			}
			fmt.Fprintf(&b, "%s%s\n", marker, formatPost(p))
		}
	} else {
		end := minInt(len(m.threads), m.contentScroll+m.visibleRows())
		for i := m.contentScroll; i < end; i++ {
			t := m.threads[i]
			marker := "  "
			if i == m.contentIndex {
				marker = "> "
			}
			fmt.Fprintf(&b, "%s%s  %s\n", marker, SanitizeTerminalText(t.Id), t.UpdatedAt.Format("2006-01-02 15:04"))
		}
	}
	return b.String()
}
func (m Model) composerView() string {
	if m.attachmentPicking {
		return frame("Путь к изображению", m.attachmentPath+"_\n\nEnter загрузить • Esc отменить", true, max(30, m.width))
	}
	attachment := "нет"
	if m.draft.AttachmentID != "" {
		attachment = SanitizeTerminalText(m.draft.AttachmentID)
	}
	return frame("Редактор", m.editor.View()+"\n\nCtrl+S отправить • Ctrl+O вложение • Esc отменить\n10000 символов • вложение: "+attachment, true, max(30, m.width)) + m.status()
}
func (m Model) helpView() string {
	text := fmt.Sprintf("%s %s навигация\n%s сменить панель\n%s открыть\n%s новый тред • %s ответ\n%s обновить • %s поиск\n%s вложение • %s справка\n%s назад • %s выход", m.keys.Up, m.keys.Down, m.keys.Switch, m.keys.Open, m.keys.NewThread, m.keys.Reply, m.keys.Refresh, m.keys.Search, m.keys.Attachment, m.keys.Help, m.keys.Back, m.keys.Quit)
	return frame("Справка", text, true, max(30, m.width))
}
func (m Model) status() string {
	if m.searching {
		return "\n Поиск: " + m.searchQuery + "_  Enter применить • Esc отменить"
	}
	if m.loading {
		return "\n Загрузка…"
	}
	if m.err != nil {
		var apiErr *client.APIError
		if errorsAs(m.err, &apiErr) {
			return "\n Ошибка: " + SanitizeTerminalText(string(apiErr.Code))
		}
		return "\n Ошибка сети"
	}
	return "\n Tab панели • ? справка • q выход"
}

func frame(title, content string, active bool, width int) string {
	color := lipgloss.Color("240")
	if active {
		color = lipgloss.Color("39")
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(color).Width(max(1, width-3)).Height(12).Render(" " + title + " \n" + content)
}
func formatPost(p generated.Post) string {
	if p.State != generated.Visible {
		return fmt.Sprintf(">>%s [%s]", SanitizeTerminalText(p.Id), SanitizeTerminalText(string(p.State)))
	}
	prefix := fmt.Sprintf(">>%s (%s)", SanitizeTerminalText(p.Id), SanitizeTerminalText(p.AuthorId))
	if p.ReplyToId != nil {
		prefix += " → >>" + SanitizeTerminalText(*p.ReplyToId)
	}
	body := ""
	if p.Body != nil {
		body = sanitizeMultilineTerminalText(*p.Body)
	}
	return prefix + "\n" + body
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
func (m Model) visibleRows() int { return max(1, m.height-7) }
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

// errorsAs is kept tiny so View never risks exposing the wrapped error text.
func errorsAs(err error, target any) bool { return errors.As(err, target) }
