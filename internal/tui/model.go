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
	requestID                                            uint64
	err                                                  error
	editor                                               textarea.Model
	draft                                                Draft
}

type Draft struct {
	Board, Thread, AttachmentID, IdempotencyKey string
	ReplyTo                                     *string
}

type boardsMsg struct {
	id     uint64
	boards []generated.Board
	err    error
}
type threadsMsg struct {
	id     uint64
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
	ID  string
	Err error
}

func NewModel(ctx context.Context, api Service) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	editor := textarea.New()
	editor.Placeholder = "Введите текст публикации…"
	editor.CharLimit = 10000
	editor.SetWidth(60)
	editor.SetHeight(8)
	return Model{ctx: ctx, api: api, keys: DefaultKeyMap(), screen: BoardsScreen, focus: FocusBoards, editor: editor, requestID: 1, loading: true}
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
		m.loading, m.err, m.boards = false, msg.err, msg.boards
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
			m.threads = mergeThreads(m.threads, msg.page.Threads, msg.append)
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
			m.posts = mergePosts(m.posts, msg.page.Posts, msg.append)
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
		if m.screen == ComposerScreen {
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
	if matches(key, m.keys.Quit) && m.screen != ComposerScreen {
		if m.streamCancel != nil {
			m.streamCancel()
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
	m.previous, m.screen = m.screen, ComposerScreen
	m.draft.Thread = thread
	if len(m.boards) > 0 {
		m.draft.Board = m.boards[m.boardIndex].Slug
	}
	if m.draft.IdempotencyKey == "" {
		key, err := newKey()
		if err != nil { m.err = err; return }
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

func (m *Model) nextRequest() uint64 {
	if m.streamCancel != nil {
		m.streamCancel()
		m.streamCancel = nil
	}
	m.requestID++
	m.loading = true
	m.err = nil
	return m.requestID
}
func (m Model) loadBoards() tea.Cmd {
	id := m.requestID
	return func() tea.Msg { v, err := m.api.Boards(m.ctx); return boardsMsg{id, v, err} }
}
func (m *Model) loadThreads(appendPage bool) tea.Cmd {
	id := m.nextRequest()
	board := ""
	if len(m.boards) > 0 {
		board = m.boards[m.boardIndex].Slug
	}
	var cursor *string
	if appendPage {
		cursor = m.nextThreadCursor
	}
	return func() tea.Msg {
		v, err := m.api.Threads(m.ctx, board, cursor, 50)
		return threadsMsg{id, v, appendPage, err}
	}
}
func (m *Model) loadPosts(appendPage bool) tea.Cmd {
	id := m.nextRequest()
	thread := m.activeThread
	var cursor *string
	if appendPage {
		cursor = m.nextPostCursor
	}
	return func() tea.Msg {
		v, err := m.api.Posts(m.ctx, thread, cursor, 100)
		return postsMsg{id, v, appendPage, err}
	}
}
func (m *Model) sendDraft() tea.Cmd {
	id := m.nextRequest()
	text := m.editor.Value()
	draft := m.draft
	return func() tea.Msg {
		body := &text
		if draft.Thread == "" {
			v, err := m.api.CreateThread(m.ctx, draft.Board, draft.IdempotencyKey, generated.CreateThread{Body: body, AttachmentId: optional(draft.AttachmentID)})
			return sentMsg{id, v, err}
		}
		v, err := m.api.CreatePost(m.ctx, draft.Thread, draft.IdempotencyKey, generated.CreatePost{Body: body, AttachmentId: optional(draft.AttachmentID), ReplyToId: draft.ReplyTo})
		return sentMsg{id, v, err}
	}
}

func (m *Model) uploadAttachment(path string) tea.Cmd {
	path = filepath.Clean(path)
	return func() tea.Msg {
		file, err := os.Open(path)
		if err != nil {
			return AttachmentSelectedMsg{Err: errors.New("не удалось открыть вложение")}
		}
		defer file.Close()
		header := make([]byte, 512)
		n, err := io.ReadFull(file, header)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			return AttachmentSelectedMsg{Err: errors.New("не удалось прочитать вложение")}
		}
		mediaType := http.DetectContentType(header[:n])
		if mediaType != "image/jpeg" && mediaType != "image/png" {
			return AttachmentSelectedMsg{Err: errors.New("поддерживаются только JPEG и PNG")}
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return AttachmentSelectedMsg{Err: errors.New("не удалось прочитать вложение")}
		}
		attachment, err := m.api.UploadAttachment(m.ctx, mediaType, file)
		if err != nil {
			return AttachmentSelectedMsg{Err: err}
		}
		return AttachmentSelectedMsg{ID: attachment.Id}
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
		fmt.Fprintf(&b, "%s/%s/ %s\n", marker, board.Slug, board.Title)
	}
	return b.String()
}
func (m Model) contentTitle() string {
	if m.screen == ThreadScreen {
		return "Тред " + m.activeThread
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
			fmt.Fprintf(&b, "%s%s  %s\n", marker, t.Id, t.UpdatedAt.Format("2006-01-02 15:04"))
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
		attachment = m.draft.AttachmentID
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
			return "\n Ошибка: " + string(apiErr.Code)
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
		return fmt.Sprintf(">>%s [%s]", p.Id, p.State)
	}
	prefix := fmt.Sprintf(">>%s (%s)", p.Id, p.AuthorId)
	if p.ReplyToId != nil {
		prefix += " → >>" + *p.ReplyToId
	}
	body := ""
	if p.Body != nil {
		body = *p.Body
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
	sort.Slice(result, func(i, j int) bool { return result[i].Id < result[j].Id })
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
	sort.Slice(result, func(i, j int) bool { return result[i].UpdatedAt.After(result[j].UpdatedAt) })
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
