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
	"time"

	"bord/api/generated"
	"bord/internal/client"
	"bord/internal/terminalui"

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
	PostScreen
	ComposerScreen
	HelpScreen
	AttachmentScreen
)

type Focus uint8

const (
	FocusBoards Focus = iota
	FocusContent
)

type replyPane uint8

const (
	replyEditorPane replyPane = iota
	replySourcePane
)

type Service interface {
	Boards(context.Context) ([]generated.Board, error)
	Threads(context.Context, string, *string, int) (generated.ThreadPage, error)
	Posts(context.Context, string, *string, int) (generated.PostPage, error)
	CreateThread(context.Context, string, string, generated.CreateThread) (generated.CreateResult, error)
	CreatePost(context.Context, string, string, generated.CreatePost) (generated.CreateResult, error)
	UploadAttachment(context.Context, string, io.Reader) (generated.Attachment, error)
	Attachment(context.Context, string) ([]byte, string, error)
	Subscribe(context.Context, client.StreamScope, client.StreamCallbacks, client.Backoff) error
}

type KeyMap struct {
	Up, Down, PageUp, PageDown, Switch, Open, Back, Refresh, Search, NewThread, Reply, ReplyPost, DeletePost, Attachment, PickAttachment, Help, Theme, Quit, Send, Cancel string
}

func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up: "up,k", Down: "down,j", PageUp: "pgup", PageDown: "pgdown", Switch: "tab", Open: "enter", Back: "esc", Refresh: "r,f5", Search: "/,f4",
		NewThread: "n,f7", Reply: "p,f7", ReplyPost: "P,shift+p,f8", DeletePost: "d,delete", Attachment: "o", PickAttachment: "ctrl+o", Help: "?,f1", Quit: "q,ctrl+c,f10", Send: "ctrl+s", Cancel: "esc",
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
	postScroll                                           int
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
	themes                                               ThemeCatalog
	themePicker                                          terminalui.ThemePicker
	replyPane                                            replyPane
	replyPostCursor, replyPostAnchor, replyPostScroll    int
	replyPostDragging                                    bool
	clipboardOSC                                         string
	clipboardCopied                                      bool
	attachmentData                                       []byte
	attachmentFile, attachmentID, attachmentWarning      string
	attachmentRenderer                                   KittyRenderer
	attachmentCache                                      *client.Cache
	openExternal                                         func(context.Context, string) error
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
	id             uint64
	board          string
	page           generated.ThreadPage
	boards         []generated.Board
	append         bool
	err, boardsErr error
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
type deletedMsg struct {
	id     uint64
	postID string
	err    error
}
type ReconcileMsg struct{ Board, Thread string }

// AttachmentSelectedMsg is emitted by a non-blocking file/upload command.
type AttachmentSelectedMsg struct {
	ID        string
	Err       error
	requestID uint64
}

type attachmentLoadedMsg struct {
	id, path, warning string
	data              []byte
	err               error
}

type attachmentOpenedMsg struct{ err error }
type attachmentInlineClosedMsg struct{ err error }
type clearClipboardOSCMsg struct{}

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
	return Model{ctx: ctx, cancel: cancel, api: api, keys: DefaultKeyMap(), screen: BoardsScreen, focus: FocusBoards, editor: editor, requestID: 1, loading: true, theme: ThemeClassic, themes: DiscoverThemes(), replyPostAnchor: -1}
}

// WithAttachmentViewer wires verified downloads to Kitty inline graphics and
// the platform opener fallback.
func (m Model) WithAttachmentViewer(mode InlineMode, cache *client.Cache) Model {
	m.attachmentRenderer = KittyRenderer{Capability: EnvironmentCapabilities{}, Mode: mode}
	m.attachmentCache = cache
	m.openExternal = client.OpenExternal
	return m
}

// WithTheme selects a validated presentation theme.
func (m Model) WithTheme(name ThemeName) Model {
	if m.themes.Has(name) {
		m.theme = name
	}
	return m
}

// WithThemes installs the discovered theme catalog and selects one palette.
func (m Model) WithThemes(catalog ThemeCatalog, name ThemeName) Model {
	if len(catalog.Options()) == 0 {
		catalog = DiscoverThemes()
	}
	m.themes = catalog
	return m.WithTheme(name)
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
		m.configureEditorWidth()
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
			if !msg.append && msg.boardsErr == nil && msg.boards != nil {
				selectedBoardSlug := selectedBoard(m.boards, m.boardIndex)
				m.boards = msg.boards
				m.boardIndex = indexBoard(m.boards, selectedBoardSlug, clamp(m.boardIndex, 0, len(m.boards)-1))
			}
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
	case deletedMsg:
		if msg.id != m.requestID {
			return m, nil
		}
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			for index := range m.posts {
				if m.posts[index].Id == msg.postID {
					m.posts = append(m.posts[:index], m.posts[index+1:]...)
					break
				}
			}
			m.contentIndex = clamp(m.contentIndex, 0, max(0, len(m.posts)-1))
			if m.screen == PostScreen {
				m.screen = ThreadScreen
			}
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
	case attachmentLoadedMsg:
		if m.screen != AttachmentScreen || msg.id != m.attachmentID {
			return m, nil
		}
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			m.attachmentData, m.attachmentFile, m.attachmentWarning = msg.data, msg.path, msg.warning
			if len(msg.data) > 0 && m.attachmentRenderer.Available() {
				return m, m.viewAttachmentInline()
			}
		}
	case attachmentOpenedMsg:
		if m.screen == AttachmentScreen {
			m.loading, m.err = false, msg.err
		}
	case attachmentInlineClosedMsg:
		if m.screen == AttachmentScreen {
			m.loading, m.err = false, msg.err
			// Bubble Tea v1.3 restores raw/alternate-screen state after tea.Exec,
			// but does not restore mouse tracking. Re-enable both cell motion and
			// SGR coordinates through its public command path.
			return m, tea.EnableMouseCellMotion
		}
	case clearClipboardOSCMsg:
		m.clipboardOSC = ""
	}
	return m, nil
}

func (m Model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.themePicker.IsOpen() {
		return m.updateThemeMenuKey(key)
	}
	if m.screen == ComposerScreen && m.draft.ReplyTo != nil && m.replyPane == replySourcePane && key == "ctrl+c" {
		return m.copyReplySelection()
	}
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
		m.openThemeMenu()
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
		if m.draft.ReplyTo != nil && key == "tab" {
			m.toggleReplyPane()
			return m, nil
		}
		if m.draft.ReplyTo != nil && m.replyPane == replySourcePane {
			return m.updateReplySourceKey(key)
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
		if m.screen == PostScreen {
			m.screen, m.postScroll = ThreadScreen, 0
		} else if m.screen == AttachmentScreen {
			m.screen = m.previous
			m.clearAttachmentViewer()
		} else if m.screen == ThreadScreen {
			m.screen, m.activeThread, m.posts = CatalogScreen, "", nil
			return m, m.loadThreads(false)
		} else if m.isNarrow() && m.focus == FocusContent {
			m.focus = FocusBoards
		}
	case matches(key, m.keys.Up):
		if m.screen == PostScreen {
			m.scrollPost(-1)
		} else {
			m.move(-1)
		}
	case matches(key, m.keys.Down):
		if m.screen == PostScreen {
			m.scrollPost(1)
			return m, nil
		}
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
	case matches(key, m.keys.PageUp):
		if m.screen == PostScreen {
			m.scrollPost(-max(1, m.height-6))
		} else if m.focus == FocusContent {
			m.movePage(-1)
		}
	case matches(key, m.keys.PageDown):
		if m.screen == PostScreen {
			m.scrollPost(max(1, m.height-6))
		} else if m.focus == FocusContent {
			atEnd := m.contentIndex == m.contentLength()-1
			m.movePage(1)
			if atEnd || m.contentIndex == m.contentLength()-1 {
				if m.screen == ThreadScreen && m.nextPostCursor != nil {
					return m, m.loadPosts(true)
				}
				if m.screen == CatalogScreen && m.nextThreadCursor != nil {
					return m, m.loadThreads(true)
				}
			}
		}
	case matches(key, m.keys.Refresh):
		if m.activeThread != "" {
			return m, m.loadPosts(false)
		}
		return m, m.loadThreads(false)
	case matches(key, m.keys.Open):
		if m.screen == AttachmentScreen {
			if m.loading || m.attachmentFile == "" {
				return m, nil
			}
			m.loading = true
			return m, m.openAttachment()
		}
		return m.openSelection()
	case matches(key, m.keys.NewThread) && len(m.boards) > 0 && m.screen == CatalogScreen:
		m.beginComposer("")
	case matches(key, m.keys.Reply) && m.activeThread != "":
		m.beginComposer(m.activeThread)
	case matches(key, m.keys.ReplyPost) && m.activeThread != "" && len(m.posts) > 0:
		postID := m.posts[clamp(m.contentIndex, 0, len(m.posts)-1)].Id
		m.beginReplyComposer(postID)
	case matches(key, m.keys.DeletePost) && (m.screen == ThreadScreen || m.screen == PostScreen):
		post, ok := m.currentPost()
		if !ok {
			return m, nil
		}
		id, ctx, cancel := m.nextRequest()
		return m, func() tea.Msg {
			defer cancel()
			owner, supported := m.api.(interface {
				DeleteOwnPost(context.Context, string) error
			})
			if !supported {
				return deletedMsg{id: id, postID: post.Id, err: errors.New("delete capability unavailable")}
			}
			return deletedMsg{id: id, postID: post.Id, err: owner.DeleteOwnPost(ctx, post.Id)}
		}
	case matches(key, m.keys.Attachment):
		if m.screen == AttachmentScreen && len(m.attachmentData) > 0 && m.attachmentRenderer.Available() {
			return m, m.viewAttachmentInline()
		}
		if m.screen == ThreadScreen || m.screen == PostScreen {
			post, ok := m.currentPost()
			if !ok || post.Attachment == nil {
				m.err = errors.New("у поста нет вложения")
				return m, nil
			}
			m.previous, m.screen = m.screen, AttachmentScreen
			m.clearAttachmentViewer()
			m.attachmentID = post.Attachment.Id
			m.loading, m.err = true, nil
			return m, m.loadAttachment(*post.Attachment)
		}
	case matches(key, m.keys.Search):
		m.searching, m.searchQuery, m.err = true, "", nil
	}
	return m, nil
}

func (m *Model) beginComposer(thread string) {
	m.beginComposerTarget(thread, nil)
}

func (m *Model) beginReplyComposer(postID string) {
	m.beginComposerTarget(m.activeThread, &postID)
}

func (m *Model) beginComposerTarget(thread string, replyTo *string) {
	board := ""
	if len(m.boards) > 0 {
		board = m.boards[m.boardIndex].Slug
	}
	if m.draft.Board != board || m.draft.Thread != thread || optionalValue(m.draft.ReplyTo) != optionalValue(replyTo) {
		m.editor.Reset()
		m.draft = Draft{Board: board, Thread: thread, ReplyTo: replyTo}
		m.submitted = nil
	}
	m.previous, m.screen = m.screen, ComposerScreen
	m.replyPane, m.replyPostCursor, m.replyPostAnchor, m.replyPostScroll = replyEditorPane, 0, -1, 0
	m.replyPostDragging, m.clipboardCopied = false, false
	m.configureEditorWidth()
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
	if m.screen == ThreadScreen && len(m.posts) > 0 {
		m.screen, m.postScroll = PostScreen, 0
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

func (m Model) contentLength() int {
	if m.screen == ThreadScreen {
		return len(m.posts)
	}
	return len(m.threads)
}

func (m *Model) movePage(direction int) {
	length := m.contentLength()
	if length == 0 {
		return
	}
	step := max(1, m.visibleRows())
	m.contentIndex = clamp(m.contentIndex+direction*step, 0, length-1)
	m.contentScroll = ensureVisible(m.contentScroll, m.contentIndex, step)
}

func (m *Model) scrollPost(delta int) {
	lines := m.postDetailLines()
	visible := max(1, m.height-6)
	m.postScroll = clamp(m.postScroll+delta, 0, max(0, len(lines)-visible))
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
		var boards []generated.Board
		var boardsErr error
		if !appendPage && err == nil {
			boards, boardsErr = m.api.Boards(ctx)
		}
		return threadsMsg{id: id, board: board, page: v, boards: boards, append: appendPage, err: err, boardsErr: boardsErr}
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

func (m *Model) clearAttachmentViewer() {
	m.attachmentData = nil
	m.attachmentFile = ""
	m.attachmentID = ""
	m.attachmentWarning = ""
	m.loading = false
	m.err = nil
}

func (m Model) loadAttachment(meta generated.Attachment) tea.Cmd {
	id := meta.Id
	return func() tea.Msg {
		body, responseType, err := m.api.Attachment(m.ctx, id)
		if err != nil {
			return attachmentLoadedMsg{id: id, err: err}
		}
		prepared, err := terminalui.VerifyAndCacheImage(m.attachmentCache, m.attachmentRenderer, terminalui.ImageMetadata{ID: id, SHA256: meta.Sha256, MediaType: string(meta.MediaType), Size: meta.Size}, body, responseType)
		if err != nil {
			return attachmentLoadedMsg{id: id, err: err}
		}
		return attachmentLoadedMsg{id: id, path: prepared.Path, data: prepared.Inline, warning: prepared.Warning}
	}
}

func (m Model) openAttachment() tea.Cmd {
	path := m.attachmentFile
	opener := m.openExternal
	return func() tea.Msg {
		if opener == nil {
			return attachmentOpenedMsg{err: errors.New("системный opener недоступен")}
		}
		ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
		defer cancel()
		if err := opener(ctx, path); err != nil {
			return attachmentOpenedMsg{err: errors.New("не удалось открыть вложение")}
		}
		return attachmentOpenedMsg{}
	}
}

func (m Model) viewAttachmentInline() tea.Cmd {
	viewer := &kittyViewerCommand{
		Data:    m.attachmentData,
		Columns: max(1, m.width-2),
		Rows:    max(1, m.height-4),
	}
	return tea.Exec(viewer, func(err error) tea.Msg {
		if err != nil {
			return attachmentInlineClosedMsg{err: errors.New("не удалось показать изображение в Kitty")}
		}
		return attachmentInlineClosedMsg{}
	})
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
	colors := m.themes.Palette(m.theme)
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
		lines = append(lines, paint(colors.Background, colors.Foreground, "", w))
	}
	if m.themePicker.IsOpen() {
		lines = m.themePicker.Overlay(lines, m.themes, colors, m.width, m.height, textWidth(" BORD  "))
	}
	if m.clipboardOSC != "" && len(lines) > 0 {
		lines[0] = m.clipboardOSC + lines[0]
	}
	return strings.Join(lines[:h], "\n")
}

func (m Model) isNarrow() bool { return m.width > 0 && m.width < 72 }

func (m Model) contentTitle() string {
	if m.screen == ComposerScreen {
		return m.composerTitle()
	}
	if m.screen == ThreadScreen {
		return "Тред " + SanitizeTerminalText(m.activeThread)
	}
	if m.screen == PostScreen {
		if post, ok := m.currentPost(); ok {
			return "Пост >>" + SanitizeTerminalText(post.Id)
		}
		return "Пост"
	}
	if m.screen == AttachmentScreen {
		return "Вложение"
	}
	return "Каталог"
}

func (m Model) statusText() string {
	if m.clipboardCopied {
		return " Выделенный текст скопирован • Tab вернуться к ответу"
	}
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
	return terminalui.TopBar(t, left, right, width)
}

func (m Model) topBarLeft() string {
	left := " BORD  "
	if m.canGoBack() {
		left += "[← Назад] "
	}
	return left + m.themeControlLabel() + "[Справка]"
}

func (m Model) themeControlLabel() string {
	label := m.themes.Label(m.theme)
	if label == "" {
		label = string(m.theme)
	}
	label = ansi.Truncate(SanitizeTerminalText(label), 18, "…")
	return "[Тема: " + label + " ▾] "
}

func (m *Model) openThemeMenu() {
	m.themePicker.Open(m.themes, m.theme, m.height)
}

func (m Model) updateThemeMenuKey(key string) (tea.Model, tea.Cmd) {
	if selected, ok := m.themePicker.HandleKey(key, m.themes, m.height); ok {
		m.theme = selected
	}
	return m, nil
}

func (m Model) themeMenuGeometry() (int, int, int) {
	return m.themePicker.Geometry(m.themes, m.width, m.height, textWidth(" BORD  "))
}

func (m Model) workspace(t theme, width, height int) []string {
	if m.screen == ComposerScreen {
		if m.draft.ReplyTo != nil {
			return m.replyComposerWorkspace(t, width, height)
		}
		return m.singlePanel(t, width, height, m.composerTitle(), "ТЕКСТ ПУБЛИКАЦИИ", m.composerLines(), true)
	}
	if m.screen == HelpScreen {
		return m.singlePanel(t, width, height, "Справка", "КЛАВИША  ДЕЙСТВИЕ", m.helpLines(), true)
	}
	if m.screen == PostScreen {
		return m.singlePanel(t, width, height, m.contentTitle(), "ПОЛНЫЙ ТЕКСТ • ↑/↓ ПРОКРУТКА • ESC НАЗАД", m.postDetailRows(), true)
	}
	if m.screen == AttachmentScreen {
		return m.singlePanel(t, width, height, "Вложение", "ИЗОБРАЖЕНИЕ • ENTER ВНЕШНИЙ ПРОСМОТР • ESC НАЗАД", m.attachmentRows(), true)
	}
	if m.isNarrow() {
		if m.focus == FocusBoards {
			return m.singlePanel(t, width, height, "Доски", "РАЗДЕЛ / THREADS / НАЗВАНИЕ", m.boardRows(), true)
		}
		return m.singlePanel(t, width, height, m.contentTitle(), m.contentHint(), m.contentRows(), true)
	}
	leftWidth, rightWidth := terminalui.SplitWidth(width, 24, 28)
	left := m.singlePanel(t, leftWidth, height, "Доски", "РАЗДЕЛ / THREADS / НАЗВАНИЕ", m.boardRows(), m.focus == FocusBoards)
	right := m.singlePanel(t, rightWidth, height, m.contentTitle(), m.contentHint(), m.contentRows(), m.focus == FocusContent)
	return terminalui.JoinPanels(height, left, right)
}

type displayRow = terminalui.Row

func (m Model) singlePanel(t theme, width, height int, title, hint string, rows []displayRow, active bool) []string {
	return terminalui.Panel(t, width, height, title, hint, rows, active)
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
		rows = append(rows, displayRow{Lines: []string{fmt.Sprintf("%s/%s/ [%d] %s", marker, SanitizeTerminalText(board.Slug), board.ThreadCount, SanitizeTerminalText(board.Title))}, Active: i == m.boardIndex})
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
	return "THREAD / REPLIES / UPDATED / PREVIEW"
}

func (m Model) contentRows() []displayRow {
	rows := make([]displayRow, 0, m.visibleRows())
	if m.screen == ThreadScreen {
		end := minInt(len(m.posts), m.contentScroll+m.visibleRows())
		for i := m.contentScroll; i < end; i++ {
			p := m.posts[i]
			meta := postMeta(p, i == m.contentIndex)
			body := "[" + SanitizeTerminalText(string(p.State)) + "]"
			if p.State == generated.Visible && p.Body != nil {
				body = preview(*p.Body)
			}
			rows = append(rows, displayRow{Lines: []string{meta, "  " + body}, Active: i == m.contentIndex})
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
		meta := fmt.Sprintf("%s%s %-18s [%d] %s", marker, state, SanitizeTerminalText(t.Id), t.ReplyCount, t.UpdatedAt.Format("2006-01-02 15:04"))
		body := "(нет текста)"
		if t.InitialPost != nil && t.InitialPost.Body != nil {
			body = preview(*t.InitialPost.Body)
		}
		rows = append(rows, displayRow{Lines: []string{meta, "  " + body}, Active: i == m.contentIndex})
	}
	return rows
}

func postMeta(p generated.Post, active bool) string {
	marker := "  "
	if active {
		marker = "> "
	}
	meta := fmt.Sprintf("%s>>%s  %-12s  %s", marker, SanitizeTerminalText(p.Id), SanitizeTerminalText(p.AuthorId), p.CreatedAt.Format("2006-01-02 15:04"))
	if p.ReplyToId != nil {
		meta += "  → >>" + SanitizeTerminalText(*p.ReplyToId)
	}
	return meta
}

func (m Model) attachmentRows() []displayRow {
	if m.loading {
		return []displayRow{{Lines: []string{"Загрузка и проверка вложения…"}}}
	}
	if m.err != nil {
		return []displayRow{{Lines: []string{"Ошибка: " + SanitizeTerminalText(m.err.Error()), "", "Esc  вернуться к посту"}}}
	}
	lines := []string{}
	if len(m.attachmentData) > 0 && m.attachmentRenderer.Available() {
		lines = append(lines, "Kitty-просмотр закрыт.", "o      показать изображение снова", "Enter  открыть системным приложением")
	} else {
		lines = append(lines, "Inline-графика недоступна в этом терминале.", "Enter  открыть системным приложением")
	}
	if m.attachmentWarning != "" {
		lines = append(lines, "", SanitizeTerminalText(m.attachmentWarning))
	}
	lines = append(lines, "Esc    вернуться к посту")
	return []displayRow{{Lines: lines}}
}

func (m Model) composerLines() []displayRow {
	if m.attachmentPicking {
		return []displayRow{{Lines: []string{"Путь: " + SanitizeTerminalText(m.attachmentPath) + "_", "", "Enter загрузить • Esc отменить"}}}
	}
	attachment := "нет"
	if m.draft.AttachmentID != "" {
		attachment = SanitizeTerminalText(m.draft.AttachmentID)
	}
	lines := []string{m.composerTarget(), ""}
	lines = append(lines, strings.Split(m.editor.View(), "\n")...)
	action := "Ctrl+S ОТПРАВИТЬ • Ctrl+O вложение • Esc оставить черновик"
	if m.loading {
		action = "ОТПРАВКА… дождитесь результата • Esc отменить запрос"
	} else if m.err != nil {
		action = "ОШИБКА • Ctrl+S ПОВТОРИТЬ • Esc оставить черновик"
	}
	lines = append(lines, "", "Команды ниже также доступны мышью", action, "Вложение: "+attachment)
	return []displayRow{{Lines: lines, TrustedANSI: true}}
}

func (m Model) helpLines() []displayRow {
	return []displayRow{{Lines: []string{
		"↑/k ↓/j   навигация / прокрутка полного поста", "PgUp/PgDn  переход на страницу тредов или постов", "Tab        сменить панель", "Enter      открыть тред / полный пост", "Esc        назад / закрыть",
		"n / F7     новый тред (только каталог)", "p / F7     ответ в открытый тред", "P / F8     ответ на выделенный пост", "d / Delete удалить свой пост",
		"r /        обновить / поиск", "o Ctrl+O   открыть / прикрепить", "t / F2     список встроенных и MC-тем", "?          эта справка", "q Ctrl+C   выход",
		"Ответ      Tab — исходный пост; Shift+стрелки — выделить; Ctrl+C — копировать", "Мышь       один клик открывает; drag выделяет текст исходного поста", "F8         ответить на выделенный/открытый пост", "Колесо     навигация или прокрутка",
	}}}
}

func (m Model) currentPost() (generated.Post, bool) {
	if len(m.posts) == 0 {
		return generated.Post{}, false
	}
	return m.posts[clamp(m.contentIndex, 0, len(m.posts)-1)], true
}

func (m Model) postDetailLines() []string {
	post, ok := m.currentPost()
	if !ok {
		return []string{"Пост не найден"}
	}
	lines := []string{
		fmt.Sprintf(">>%s  %s  %s", SanitizeTerminalText(post.Id), SanitizeTerminalText(post.AuthorId), post.CreatedAt.Format("2006-01-02 15:04")),
	}
	if post.ReplyToId != nil {
		lines = append(lines, "Ответ на >>"+SanitizeTerminalText(*post.ReplyToId))
	}
	lines = append(lines, "")
	body := "[" + SanitizeTerminalText(string(post.State)) + "]"
	if post.State == generated.Visible && post.Body != nil {
		body = sanitizeMultilineTerminalText(*post.Body)
		if strings.TrimSpace(body) == "" {
			body = "(пусто)"
		}
	}
	wrapWidth := max(1, m.width-5)
	wrapped := ansi.Hardwrap(ansi.Wordwrap(body, wrapWidth, " "), wrapWidth, true)
	lines = append(lines, strings.Split(wrapped, "\n")...)
	if post.Attachment != nil {
		lines = append(lines, "", "Вложение: "+SanitizeTerminalText(post.Attachment.Id))
	}
	return lines
}

func (m Model) postDetailRows() []displayRow {
	lines := m.postDetailLines()
	start := clamp(m.postScroll, 0, max(0, len(lines)-1))
	return []displayRow{{Lines: lines[start:]}}
}

func (m Model) statusLine(t theme, width int) string {
	return paint(t.Background, t.Foreground, m.statusText(), width)
}

func (m Model) commandBar(t theme, width int) string {
	return terminalui.CommandBar(t, m.commandLabels(), width)
}

func (m Model) compactCommandBar(width int) bool {
	return terminalui.CompactCommands(m.commandLabels(), width)
}

func (m Model) commandLabels() []string {
	if m.screen == ComposerScreen {
		if m.draft.ReplyTo != nil && m.replyPane == replySourcePane {
			return []string{"Ctrl+C Copy", "Tab Reply", "Esc Cancel"}
		}
		if m.draft.ReplyTo != nil {
			return []string{"Ctrl+S Send", "Ctrl+O Attach", "Tab Source", "Esc Cancel"}
		}
		return []string{"Ctrl+S Send", "Ctrl+O Attach", "Esc Cancel"}
	}
	if m.screen == HelpScreen {
		return []string{"Esc Back", "F2 Theme", "F10 Quit"}
	}
	if m.screen == AttachmentScreen {
		return []string{"Enter Open External", "Esc Back", "F10 Quit"}
	}
	if m.screen == ThreadScreen {
		return []string{"F1 Help", "F5 Refresh", "F7 Reply Thread", "F8 Reply Post", "D Delete Own", "F10 Quit"}
	}
	if m.screen == PostScreen {
		return []string{"Esc Back", "O Image", "F7 Reply Thread", "F8 Reply Post", "D Delete Own", "F10 Quit"}
	}
	return []string{"F1 Help", "F2 Theme", "F4 Search", "F5 Refresh", "F7 New Thread", "F10 Quit"}
}

func (m Model) composerTitle() string {
	if m.draft.Thread == "" {
		return "Новый тред"
	}
	if m.draft.ReplyTo != nil {
		return "Ответ на пост >>" + SanitizeTerminalText(*m.draft.ReplyTo)
	}
	return "Ответ в тред " + SanitizeTerminalText(m.draft.Thread)
}

func (m Model) composerTarget() string {
	if m.draft.Thread == "" {
		return "Цель: новый тред в /" + SanitizeTerminalText(m.draft.Board) + "/"
	}
	if m.draft.ReplyTo != nil {
		return "Цель: пост >>" + SanitizeTerminalText(*m.draft.ReplyTo) + " в треде " + SanitizeTerminalText(m.draft.Thread)
	}
	return "Цель: тред " + SanitizeTerminalText(m.draft.Thread)
}

func optionalValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (m Model) location() string {
	if len(m.boards) == 0 {
		return "BORD"
	}
	return "/" + SanitizeTerminalText(m.boards[m.boardIndex].Slug) + "/ " + m.contentTitle()
}

func (m Model) selectionCount() string {
	if m.screen == PostScreen {
		lines := m.postDetailLines()
		return fmt.Sprintf("строка %d/%d", minInt(m.postScroll+1, len(lines)), len(lines))
	}
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

func textWidth(value string) int { return terminalui.TextWidth(value) }

func fit(value string, width int, fill rune) string {
	return fitWidth(SanitizeTerminalText(value), width, fill)
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

// Theme returns the active theme identifier.
func (m Model) Theme() ThemeName { return m.theme }

// ThemeSetting returns a rediscoverable name or path for local-state persistence.
func (m Model) ThemeSetting() string { return m.themes.Setting(m.theme) }

// Close cancels public HTTP/SSE work owned by this model.
func (m Model) Close() {
	if m.streamCancel != nil {
		m.streamCancel()
	}
	if m.requestCancel != nil {
		m.requestCancel()
	}
	if m.cancel != nil {
		m.cancel()
	}
}

// errorsAs is kept tiny so View never risks exposing the wrapped error text.
func errorsAs(err error, target any) bool { return errors.As(err, target) }
