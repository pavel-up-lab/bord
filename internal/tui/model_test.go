package tui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"bord/api/generated"
	serverapi "bord/internal/api"
	"bord/internal/client"
	"bord/internal/events"
	"bord/internal/integration"
	storage "bord/internal/storage/sqlite"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type fakeService struct {
	boards         []generated.Board
	threads        generated.ThreadPage
	posts          generated.PostPage
	createErr      error
	keys           []string
	subscribeEvent bool
	attachmentBody []byte
	attachmentType string
	attachmentErr  error
}

func (f *fakeService) Boards(context.Context) ([]generated.Board, error) { return f.boards, nil }
func (f *fakeService) Threads(context.Context, string, *string, int) (generated.ThreadPage, error) {
	return f.threads, nil
}
func (f *fakeService) Posts(context.Context, string, *string, int) (generated.PostPage, error) {
	return f.posts, nil
}
func (f *fakeService) CreateThread(_ context.Context, _ string, key string, _ generated.CreateThread) (generated.CreateResult, error) {
	f.keys = append(f.keys, key)
	return generated.CreateResult{ThreadId: "thread_1", PostId: "post_1"}, f.createErr
}
func (f *fakeService) CreatePost(_ context.Context, _ string, key string, _ generated.CreatePost) (generated.CreateResult, error) {
	f.keys = append(f.keys, key)
	return generated.CreateResult{ThreadId: "thread_1", PostId: "post_2"}, f.createErr
}
func (f *fakeService) UploadAttachment(context.Context, string, io.Reader) (generated.Attachment, error) {
	return generated.Attachment{Id: "attachment_1"}, nil
}
func (f *fakeService) Attachment(context.Context, string) ([]byte, string, error) {
	return f.attachmentBody, f.attachmentType, f.attachmentErr
}
func (f *fakeService) Subscribe(ctx context.Context, _ client.StreamScope, callbacks client.StreamCallbacks, _ client.Backoff) error {
	if f.subscribeEvent && callbacks.Event != nil {
		callbacks.Event(client.Event{ID: "1", Type: "post.created"})
	}
	return ctx.Err()
}

func TestReducerTransitionsStaleResponsesAndHelp(t *testing.T) {
	f := &fakeService{boards: []generated.Board{{Id: "board_1", Slug: "b", Title: "Random"}}}
	m := NewModel(context.Background(), f)
	updated, cmd := m.Update(boardsMsg{id: 0, boards: []generated.Board{{Slug: "stale"}}})
	m = updated.(Model)
	if len(m.boards) != 0 || cmd != nil {
		t.Fatal("stale response changed model")
	}
	updated, cmd = m.Update(boardsMsg{id: 1, boards: f.boards})
	m = updated.(Model)
	if m.screen != CatalogScreen || cmd == nil {
		t.Fatalf("screen=%v cmd=%v", m.screen, cmd)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = updated.(Model)
	if m.screen != HelpScreen {
		t.Fatal("help did not open")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.screen != CatalogScreen {
		t.Fatal("help did not restore screen")
	}
}

func TestPersistedBoardSelectionIsRestored(t *testing.T) {
	m := NewModel(context.Background(), &fakeService{}).WithLastBoard("tech")
	updated, cmd := m.Update(boardsMsg{id: 1, boards: []generated.Board{{Slug: "b"}, {Slug: "tech"}}})
	m = updated.(Model)
	if cmd == nil || m.boardIndex != 1 || m.LastBoard() != "tech" {
		t.Fatalf("board index=%d last=%q cmd=%v", m.boardIndex, m.LastBoard(), cmd)
	}
}

func TestComposerRetryReusesIdempotencyAndPreservesDraft(t *testing.T) {
	f := &fakeService{createErr: errors.New("network unavailable")}
	m := NewModel(context.Background(), f)
	m.boards = []generated.Board{{Slug: "b"}}
	m.loading = false
	m.beginComposer("")
	m.editor.SetValue("draft survives")
	first := m.sendDraft()().(sentMsg)
	updated, _ := m.Update(first)
	m = updated.(Model)
	if m.editor.Value() != "draft survives" || m.screen != ComposerScreen {
		t.Fatal("failed send lost draft")
	}
	if !strings.Contains(m.statusText(), "Ctrl+S повторить") || !strings.Contains(m.composerLines()[0].lines[len(m.composerLines()[0].lines)-2], "ОШИБКА") {
		t.Fatal("composer did not expose retry status and key")
	}
	m.loading = true
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if cmd != nil || len(f.keys) != 1 {
		t.Fatal("composer accepted a duplicate send while a request was active")
	}
	m.loading = false
	second := m.sendDraft()().(sentMsg)
	if len(f.keys) != 2 || f.keys[0] == "" || f.keys[0] != f.keys[1] || second.err == nil {
		t.Fatalf("keys=%#v second=%#v", f.keys, second)
	}
	m.editor.SetValue("changed payload")
	third := m.sendDraft()().(sentMsg)
	if third.err == nil || len(f.keys) != 3 || f.keys[2] == f.keys[1] {
		t.Fatalf("changed payload reused key: keys=%#v third=%#v", f.keys, third)
	}
	f.createErr = nil
	updated, cmd = m.Update(sentMsg{id: m.requestID, result: generated.CreateResult{ThreadId: "thread_1", PostId: "post_1"}})
	m = updated.(Model)
	if m.editor.Value() != "" || m.draft.IdempotencyKey != "" || m.screen != ThreadScreen || cmd == nil {
		t.Fatal("successful send did not clear draft")
	}
}

func TestReconcileDeduplicatesAndPreservesSelection(t *testing.T) {
	m := NewModel(context.Background(), &fakeService{})
	m.requestID = 5
	m.activeThread = "thread_1"
	m.screen = ThreadScreen
	m.contentIndex = 1
	m.posts = []generated.Post{{Id: "post_1"}, {Id: "post_2"}}
	updated, _ := m.Update(postsMsg{id: 5, page: generated.PostPage{Posts: []generated.Post{{Id: "post_1"}, {Id: "post_2"}, {Id: "post_3"}}}})
	m = updated.(Model)
	if len(m.posts) != 3 || m.contentIndex != 1 {
		t.Fatalf("posts=%#v selection=%d", m.posts, m.contentIndex)
	}
}

func TestReconcilePreservesSelectedIdentityAfterReorder(t *testing.T) {
	m := NewModel(context.Background(), &fakeService{})
	m.requestID, m.activeThread, m.screen = 7, "thread_1", ThreadScreen
	m.posts = []generated.Post{
		{Id: "post_b", CreatedAt: time.Unix(2, 0)},
		{Id: "post_a", CreatedAt: time.Unix(3, 0)},
	}
	m.contentIndex = 1
	updated, _ := m.Update(postsMsg{id: 7, page: generated.PostPage{Posts: []generated.Post{
		{Id: "post_a", CreatedAt: time.Unix(1, 0)},
		{Id: "post_b", CreatedAt: time.Unix(2, 0)},
	}}})
	m = updated.(Model)
	if got := m.posts[m.contentIndex].Id; got != "post_a" {
		t.Fatalf("selected post changed to %q", got)
	}
}

func TestStaleAttachmentUploadDoesNotMutateNewComposer(t *testing.T) {
	m := NewModel(context.Background(), &fakeService{})
	m.screen, m.requestID = ComposerScreen, 4
	updated, _ := m.Update(AttachmentSelectedMsg{ID: "attachment_old", requestID: 3})
	m = updated.(Model)
	if m.draft.AttachmentID != "" {
		t.Fatalf("stale attachment applied: %#v", m.draft)
	}
}

func TestSSEEventTriggersScopedReconciliation(t *testing.T) {
	f := &fakeService{subscribeEvent: true}
	m := NewModel(context.Background(), f)
	m.requestID = 2
	m.boards = []generated.Board{{Slug: "b"}}
	m.screen = CatalogScreen
	updated, cmd := m.Update(threadsMsg{id: 2, page: generated.ThreadPage{}})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("SSE watch not started")
	}
	msg, ok := cmd().(ReconcileMsg)
	if !ok || msg.Board != "b" {
		t.Fatalf("message=%#v", msg)
	}
	updated, cmd = m.Update(msg)
	_ = updated.(Model)
	if cmd == nil {
		t.Fatal("reconciliation request not scheduled")
	}
}

func TestCatalogRefreshAlsoRefreshesBoardThreadCounts(t *testing.T) {
	f := &fakeService{
		boards:  []generated.Board{{Slug: "b", ThreadCount: 4}},
		threads: generated.ThreadPage{Threads: []generated.Thread{{Id: "thread_1"}}},
	}
	m := NewModel(context.Background(), f)
	m.loading, m.screen, m.focus = false, CatalogScreen, FocusContent
	m.boards = []generated.Board{{Slug: "b", ThreadCount: 1}}
	cmd := m.loadThreads(false)
	updated, _ := m.Update(cmd())
	m = updated.(Model)
	if got := m.boards[0].ThreadCount; got != 4 {
		t.Fatalf("thread count stayed stale: %d", got)
	}
	m.screen, m.activeThread = ThreadScreen, "thread_1"
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if cmd == nil || m.screen != CatalogScreen || m.activeThread != "" {
		t.Fatalf("return from thread did not schedule catalog refresh: screen=%v thread=%q cmd=%v", m.screen, m.activeThread, cmd)
	}
}

func TestKeyboardSearchAndAttachmentSelection(t *testing.T) {
	body := "find this text"
	m := NewModel(context.Background(), &fakeService{})
	m.loading, m.screen, m.focus = false, ThreadScreen, FocusContent
	m.posts = []generated.Post{{Id: "post_1"}, {Id: "post_2", Body: &body}}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)
	for _, r := range "find" {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(Model)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.contentIndex != 1 || m.searching {
		t.Fatalf("index=%d searching=%v", m.contentIndex, m.searching)
	}
	m.screen = ComposerScreen
	updated, _ = m.Update(AttachmentSelectedMsg{ID: "attachment_1"})
	m = updated.(Model)
	if m.draft.AttachmentID != "attachment_1" {
		t.Fatal("attachment selection lost")
	}
}

func TestPageKeysNavigateThreadsAndPosts(t *testing.T) {
	m := NewModel(context.Background(), &fakeService{})
	m.width, m.height, m.loading = 100, 24, false
	m.screen, m.focus = CatalogScreen, FocusContent
	for i := range 30 {
		m.threads = append(m.threads, generated.Thread{Id: fmt.Sprintf("thread_%02d", i)})
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = updated.(Model)
	if m.contentIndex != m.visibleRows() {
		t.Fatalf("thread page down index=%d step=%d", m.contentIndex, m.visibleRows())
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = updated.(Model)
	if m.contentIndex != 0 {
		t.Fatalf("thread page up index=%d", m.contentIndex)
	}
	m.screen = ThreadScreen
	m.posts = make([]generated.Post, 30)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if got := updated.(Model).contentIndex; got != m.visibleRows() {
		t.Fatalf("post page down index=%d", got)
	}
}

func TestLayoutGolden(t *testing.T) {
	setColorProfile(3) // termenv.Ascii, without promoting the transitive module to a direct dependency.
	defer setColorProfile(3)
	previewBody := "Initial post preview with useful catalog metadata"
	m := NewModel(context.Background(), &fakeService{})
	m.loading = false
	m.boards = []generated.Board{{Slug: "b", Title: "Random"}, {Slug: "tech", Title: "Technology"}}
	m.threads = []generated.Thread{{Id: "thread_0001", UpdatedAt: time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC), InitialPost: &generated.Post{Body: &previewBody}}}
	m.screen = CatalogScreen
	for _, tc := range []struct {
		name          string
		width, height int
	}{{"classic-80x24", 80, 24}, {"wide-120x30", 120, 30}, {"tall-80x40", 80, 40}, {"narrow-48x18", 48, 18}} {
		t.Run(tc.name, func(t *testing.T) {
			m.width, m.height = tc.width, tc.height
			view := m.View()
			assertDimensions(t, view, tc.width, tc.height)
			assertGolden(t, tc.name+".golden", view)
		})
	}
	setColorProfile(1) // termenv.ANSI256
	m.width, m.height = 80, 24
	view := m.View()
	assertDimensions(t, view, 80, 24)
	for i, line := range strings.Split(view, "\n") {
		if !strings.Contains(line, "48;5;") {
			t.Fatalf("line %d has no ANSI background fill: %q", i, line)
		}
	}
	assertGolden(t, "classic-ansi256-80x24.golden", view)
}

func TestWindowResizeConsumesExactTerminalAndThemeMenu(t *testing.T) {
	setColorProfile(3)
	m := NewModel(context.Background(), &fakeService{})
	for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 24}, {Width: 132, Height: 31}, {Width: 42, Height: 13}, {Width: 1, Height: 1}} {
		updated, _ := m.Update(size)
		m = updated.(Model)
		assertDimensions(t, m.View(), max(1, size.Width), max(1, size.Height))
	}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)
	for _, want := range []ThemeName{ThemeAmber, ThemeGreen, ThemeMono} {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
		m = updated.(Model)
		if !m.themeMenu {
			t.Fatal("theme menu did not open")
		}
		assertDimensions(t, m.View(), 80, 24)
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(Model)
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = updated.(Model)
		if m.Theme() != want {
			t.Fatalf("theme=%q want=%q", m.Theme(), want)
		}
	}
}

func TestMouseCoordinatesAcrossLayouts(t *testing.T) {
	body := "preview"
	newModel := func(width, height int) Model {
		m := NewModel(context.Background(), &fakeService{})
		m.width, m.height, m.loading = width, height, false
		m.boards = []generated.Board{{Slug: "b"}, {Slug: "tech"}, {Slug: "art"}, {Slug: "news"}}
		m.threads = []generated.Thread{
			{Id: "thread_1", InitialPost: &generated.Post{Body: &body}},
			{Id: "thread_2", InitialPost: &generated.Post{Body: &body}},
			{Id: "thread_3", InitialPost: &generated.Post{Body: &body}},
			{Id: "thread_4", InitialPost: &generated.Post{Body: &body}},
		}
		m.screen, m.focus = CatalogScreen, FocusBoards
		return m
	}
	click := func(x, y int) tea.MouseMsg {
		return tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
	}
	wheel := func(x, y int, button tea.MouseButton) tea.MouseMsg {
		return tea.MouseMsg{X: x, Y: y, Button: button, Action: tea.MouseActionPress}
	}

	tests := []struct {
		name  string
		model Model
		msgs  []tea.MouseMsg
		check func(*testing.T, Model, []tea.Cmd)
	}{
		{
			name:  "80x24 board single click opens",
			model: newModel(80, 24),
			msgs:  []tea.MouseMsg{click(5, 4)},
			check: func(t *testing.T, m Model, cmds []tea.Cmd) {
				if m.boardIndex != 1 || m.focus != FocusContent || cmds[0] == nil {
					t.Fatalf("board=%d focus=%v commands=%v", m.boardIndex, m.focus, cmds)
				}
			},
		},
		{
			name:  "wide content row single click opens thread",
			model: newModel(120, 30),
			msgs:  []tea.MouseMsg{click(70, 5)},
			check: func(t *testing.T, m Model, cmds []tea.Cmd) {
				if m.contentIndex != 0 || m.screen != ThreadScreen || m.activeThread != "thread_2" || cmds[0] == nil {
					t.Fatalf("index=%d screen=%v thread=%q", m.contentIndex, m.screen, m.activeThread)
				}
			},
		},
		{
			name: "narrow content coordinates",
			model: func() Model {
				m := newModel(48, 18)
				m.focus = FocusContent
				return m
			}(),
			msgs: []tea.MouseMsg{click(20, 7)},
			check: func(t *testing.T, m Model, _ []tea.Cmd) {
				if m.screen != ThreadScreen || m.activeThread != "thread_3" || m.focus != FocusContent {
					t.Fatalf("screen=%v thread=%q focus=%v", m.screen, m.activeThread, m.focus)
				}
			},
		},
		{
			name: "post row single click opens full post",
			model: func() Model {
				m := newModel(80, 24)
				m.screen, m.focus, m.activeThread = ThreadScreen, FocusContent, "thread_1"
				m.posts = []generated.Post{{Id: "post_1"}, {Id: "post_2"}, {Id: "post_3"}}
				return m
			}(),
			msgs: []tea.MouseMsg{click(60, 6)},
			check: func(t *testing.T, m Model, _ []tea.Cmd) {
				if m.contentIndex != 1 || m.screen != PostScreen {
					t.Fatalf("index=%d screen=%v", m.contentIndex, m.screen)
				}
			},
		},
		{
			name:  "wheel uses hovered panel",
			model: newModel(120, 30),
			msgs: []tea.MouseMsg{
				wheel(10, 10, tea.MouseButtonWheelDown),
				wheel(80, 10, tea.MouseButtonWheelDown),
				wheel(80, 10, tea.MouseButtonWheelUp),
			},
			check: func(t *testing.T, m Model, _ []tea.Cmd) {
				if m.boardIndex != 1 || m.contentIndex != 0 || m.focus != FocusContent {
					t.Fatalf("board=%d content=%d focus=%v", m.boardIndex, m.contentIndex, m.focus)
				}
			},
		},
		{
			name: "wheel paginates at content end",
			model: func() Model {
				m := newModel(80, 24)
				cursor := "next"
				m.focus, m.contentIndex, m.nextThreadCursor = FocusContent, 2, &cursor
				return m
			}(),
			msgs: []tea.MouseMsg{wheel(60, 10, tea.MouseButtonWheelDown)},
			check: func(t *testing.T, m Model, cmds []tea.Cmd) {
				if m.contentIndex != 3 || cmds[0] == nil || !m.loading {
					t.Fatalf("index=%d command=%v loading=%v", m.contentIndex, cmds[0], m.loading)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.model
			cmds := make([]tea.Cmd, 0, len(tc.msgs))
			for _, msg := range tc.msgs {
				updated, cmd := m.Update(msg)
				m = updated.(Model)
				cmds = append(cmds, cmd)
			}
			tc.check(t, m, cmds)
		})
	}
}

func TestClickReplyNumberSelectsTargetPost(t *testing.T) {
	target := "post_1"
	m := NewModel(context.Background(), &fakeService{})
	m.width, m.height, m.loading = 120, 24, false
	m.screen, m.focus, m.activeThread = ThreadScreen, FocusContent, "thread_1"
	m.posts = []generated.Post{{Id: target}, {Id: "post_2", ReplyToId: &target}, {Id: "post_3"}}
	m.contentIndex = 1
	meta := postMeta(m.posts[1], true)
	needle := ">>" + target
	byteOffset := strings.LastIndex(meta, needle)
	x := m.leftPanelWidth() + 2 + textWidth(meta[:byteOffset]) + 1
	updated, cmd := m.Update(tea.MouseMsg{X: x, Y: 5, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	got := updated.(Model)
	if cmd != nil || got.screen != ThreadScreen || got.contentIndex != 0 {
		t.Fatalf("reply target click: x=%d screen=%v index=%d cmd=%v", x, got.screen, got.contentIndex, cmd)
	}
}

func TestRowsShowBoardAndReplyCounts(t *testing.T) {
	m := NewModel(context.Background(), &fakeService{})
	m.boards = []generated.Board{{Slug: "general", Title: "Общение", ThreadCount: 12}}
	if got := m.boardRows()[0].lines[0]; !strings.Contains(got, "/general/ [12]") {
		t.Fatalf("board count missing: %q", got)
	}
	m.screen = CatalogScreen
	m.threads = []generated.Thread{{Id: "thread_1", ReplyCount: 7}}
	if got := m.contentRows()[0].lines[0]; !strings.Contains(got, "thread_1           [7]") {
		t.Fatalf("reply count missing: %q", got)
	}
}

func TestOpenFullPostWrapScrollBackAndReply(t *testing.T) {
	body := strings.Repeat("длинный текст публикации ", 30) + "\nпоследняя строка"
	m := NewModel(context.Background(), &fakeService{})
	m.width, m.height, m.loading = 42, 12, false
	m.screen, m.focus, m.activeThread = ThreadScreen, FocusContent, "thread_1"
	m.posts = []generated.Post{{Id: "post_1", AuthorId: "anon_1", State: generated.Visible, Body: &body}}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.screen != PostScreen || !strings.Contains(m.View(), "ПОЛНЫЙ ТЕКСТ") || !strings.Contains(m.View(), "длинный") {
		t.Fatalf("full post was not opened:\n%s", m.View())
	}
	for range 100 {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(Model)
	}
	if m.postScroll == 0 || !strings.Contains(m.View(), "последняя строка") {
		t.Fatalf("full post did not scroll to the end: scroll=%d\n%s", m.postScroll, m.View())
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyF8})
	reply := updated.(Model)
	if reply.screen != ComposerScreen || reply.draft.ReplyTo == nil || *reply.draft.ReplyTo != "post_1" {
		t.Fatalf("reply target=%+v screen=%v", reply.draft, reply.screen)
	}

	m = reply
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.screen != PostScreen {
		t.Fatalf("composer returned to screen=%v", m.screen)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.screen != ThreadScreen || m.postScroll != 0 {
		t.Fatalf("back screen=%v scroll=%d", m.screen, m.postScroll)
	}
}

func TestMouseMenusBackAndComposerControls(t *testing.T) {
	click := func(x, y int) tea.MouseMsg {
		return tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
	}
	m := NewModel(context.Background(), &fakeService{})
	m.width, m.height, m.loading = 80, 24, false
	m.boards = []generated.Board{{Slug: "b"}}
	m.screen = CatalogScreen

	updated, _ := m.Update(click(1, m.height-1))
	m = updated.(Model)
	if m.screen != HelpScreen {
		t.Fatalf("bottom Help zone screen=%v", m.screen)
	}
	updated, _ = m.Update(click(1, m.height-1))
	m = updated.(Model)
	if m.screen != CatalogScreen {
		t.Fatalf("bottom Back zone screen=%v", m.screen)
	}

	updated, _ = m.Update(click(textWidth(" BORD  ")+1, 0))
	m = updated.(Model)
	if !m.themeMenu {
		t.Fatal("top theme click did not open menu")
	}
	updated, _ = m.Update(click(textWidth(" BORD  ")+1, 3))
	m = updated.(Model)
	if m.theme != ThemeAmber {
		t.Fatalf("top theme click selected %q", m.theme)
	}
	updated, _ = m.Update(click(textWidth(" BORD  "+m.themeControlLabel())+1, 0))
	m = updated.(Model)
	if m.screen != HelpScreen {
		t.Fatalf("top help click screen=%v", m.screen)
	}
	updated, _ = m.Update(click(textWidth(" BORD  ")+1, 0))
	m = updated.(Model)
	if m.screen != CatalogScreen {
		t.Fatalf("top back click screen=%v", m.screen)
	}

	m.beginComposer("")
	m.editor.SetValue("draft")
	updated, cmd := m.Update(click(1, m.height-1))
	m = updated.(Model)
	if cmd == nil || !m.loading {
		t.Fatal("composer Send zone did not use send action")
	}
	m.loading = false
	updated, _ = m.Update(click(commandLabelWidth("Ctrl+S Send", false)+1, m.height-1))
	m = updated.(Model)
	if !m.attachmentPicking {
		t.Fatal("composer Attach zone did not open picker")
	}
	m.attachmentPicking = false
	updated, _ = m.Update(click(commandLabelWidth("Ctrl+S Send", false)+1+commandLabelWidth("Ctrl+O Attach", false)+1, m.height-1))
	m = updated.(Model)
	if m.screen != CatalogScreen {
		t.Fatalf("composer Cancel zone screen=%v", m.screen)
	}
}

func TestComposerActionsDistinguishThreadAndPostReplies(t *testing.T) {
	m := NewModel(context.Background(), &fakeService{})
	m.loading = false
	m.boards = []generated.Board{{Slug: "general"}}
	m.screen, m.focus, m.activeThread = ThreadScreen, FocusContent, "thread_1"
	m.posts = []generated.Post{{Id: "post_1"}, {Id: "post_2"}}
	m.contentIndex = 1

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyF7})
	threadReply := updated.(Model)
	if threadReply.screen != ComposerScreen || threadReply.draft.Thread != "thread_1" || threadReply.draft.ReplyTo != nil || !strings.Contains(threadReply.composerTarget(), "thread_1") {
		t.Fatalf("thread reply target=%+v", threadReply.draft)
	}

	m.draft = Draft{}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyF8})
	postReply := updated.(Model)
	if postReply.screen != ComposerScreen || postReply.draft.Thread != "thread_1" || postReply.draft.ReplyTo == nil || *postReply.draft.ReplyTo != "post_2" || !strings.Contains(postReply.composerTarget(), ">>post_2") {
		t.Fatalf("post reply target=%+v", postReply.draft)
	}

	m.screen, m.activeThread, m.draft = ThreadScreen, "thread_1", Draft{}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if got := updated.(Model); got.screen != ThreadScreen || got.draft.Thread != "" {
		t.Fatalf("n unexpectedly created a thread from thread view: screen=%v draft=%+v", got.screen, got.draft)
	}
}

func TestReplyComposerSplitsSourceAndSupportsKeyboardCopy(t *testing.T) {
	body := "alpha beta\ngamma"
	m := NewModel(context.Background(), &fakeService{})
	m.width, m.height, m.loading = 100, 24, false
	m.boards = []generated.Board{{Slug: "general"}}
	m.screen, m.focus, m.activeThread = ThreadScreen, FocusContent, "thread_1"
	m.posts = []generated.Post{{Id: "post_1", AuthorId: "anon", Body: &body, State: generated.Visible}}
	m.beginReplyComposer("post_1")
	view := m.View()
	assertDimensions(t, view, 100, 24)
	if !strings.Contains(view, "Ответ на пост >>post_1") || !strings.Contains(view, "Пост >>post_1") || !strings.Contains(view, "alpha beta") {
		t.Fatalf("split reply composer missing source or editor: %q", view)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if m.replyPane != replySourcePane || m.editor.Focused() {
		t.Fatal("Tab did not focus source post")
	}
	for range 5 {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftRight})
		m = updated.(Model)
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(Model)
	if cmd == nil || !m.clipboardCopied || !strings.Contains(m.clipboardOSC, "YWxwaGE=") {
		t.Fatalf("selection was not copied through OSC52: copied=%v osc=%q cmd=%v", m.clipboardCopied, m.clipboardOSC, cmd)
	}
	m.width = 48
	m.configureEditorWidth()
	assertDimensions(t, m.View(), 48, 24)
}

func TestReplyComposerMouseDragSelectsSourceText(t *testing.T) {
	body := "select this text"
	m := NewModel(context.Background(), &fakeService{})
	m.width, m.height, m.loading = 100, 24, false
	m.boards = []generated.Board{{Slug: "general"}}
	m.screen, m.focus, m.activeThread = ThreadScreen, FocusContent, "thread_1"
	m.posts = []generated.Post{{Id: "post_1", Body: &body, State: generated.Visible}}
	m.beginReplyComposer("post_1")
	panelX, _ := m.replySourcePanelGeometry()
	updated, _ := m.Update(tea.MouseMsg{X: panelX + 2, Y: 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	updated, _ = m.Update(tea.MouseMsg{X: panelX + 8, Y: 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	m = updated.(Model)
	updated, _ = m.Update(tea.MouseMsg{X: panelX + 8, Y: 3, Button: tea.MouseButtonNone, Action: tea.MouseActionRelease})
	m = updated.(Model)
	start, end := m.replySelection()
	if start != 0 || end != 6 || m.replyPostDragging {
		t.Fatalf("mouse selection=[%d,%d] dragging=%v", start, end, m.replyPostDragging)
	}
}

func TestMouseReplyPostUsesRenderedCommandGeometry(t *testing.T) {
	m := NewModel(context.Background(), &fakeService{})
	m.width, m.height, m.loading = 120, 24, false
	m.screen, m.focus, m.activeThread = ThreadScreen, FocusContent, "thread_1"
	m.posts = []generated.Post{{Id: "post_1"}, {Id: "post_2"}}
	m.contentIndex = 1
	labels := m.commandLabels()
	x := 0
	for _, label := range labels[:3] {
		x += commandLabelWidth(label, false) + 1
	}
	for clickX := x; clickX < x+commandLabelWidth(labels[3], false); clickX++ {
		updated, cmd := m.Update(tea.MouseMsg{X: clickX, Y: m.height - 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
		got := updated.(Model)
		if cmd != nil || got.screen != ComposerScreen || got.draft.ReplyTo == nil || *got.draft.ReplyTo != "post_2" {
			t.Fatalf("Reply Post click mapped incorrectly: x=%d screen=%v reply=%v cmd=%v", clickX, got.screen, got.draft.ReplyTo, cmd)
		}
	}
}

func TestPostScreenF8HitBoxMatchesButtonAndNeverQuits(t *testing.T) {
	m := NewModel(context.Background(), &fakeService{})
	m.width, m.height, m.loading = 120, 24, false
	m.screen, m.focus, m.activeThread = PostScreen, FocusContent, "thread_1"
	m.posts = []generated.Post{{Id: "post_1"}, {Id: "post_2"}}
	m.contentIndex = 1
	labels := m.commandLabels()
	keys := m.commandKeys()
	if len(labels) != len(keys) {
		t.Fatalf("command labels=%d keys=%d", len(labels), len(keys))
	}
	start := 0
	for _, label := range labels[:3] {
		start += commandLabelWidth(label, false) + 1
	}
	width := commandLabelWidth(labels[3], false)
	for x := start; x < start+width; x++ {
		updated, cmd := m.Update(tea.MouseMsg{X: x, Y: m.height - 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
		got := updated.(Model)
		if cmd != nil || got.screen != ComposerScreen || got.draft.ReplyTo == nil || *got.draft.ReplyTo != "post_2" {
			t.Fatalf("F8 hit box x=%d [%d,%d) mapped to screen=%v reply=%v cmd=%v", x, start, start+width, got.screen, got.draft.ReplyTo, cmd)
		}
	}
}

func TestPostScreenBottomButtonsAreClickable(t *testing.T) {
	newModel := func() Model {
		m := NewModel(context.Background(), &fakeService{})
		m.width, m.height, m.loading = 120, 24, false
		m.screen, m.focus, m.activeThread = PostScreen, FocusContent, "thread_1"
		m.posts = []generated.Post{{Id: "post_1"}}
		return m
	}
	clickCommand := func(m Model, index int) (Model, tea.Cmd) {
		x := 0
		for _, label := range m.commandLabels()[:index] {
			x += commandLabelWidth(label, false) + 1
		}
		updated, cmd := m.Update(tea.MouseMsg{X: x, Y: m.height - 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
		return updated.(Model), cmd
	}
	if got, _ := clickCommand(newModel(), 0); got.screen != ThreadScreen {
		t.Fatalf("Back click screen=%v", got.screen)
	}
	if got, _ := clickCommand(newModel(), 1); got.err == nil || got.screen != PostScreen {
		t.Fatalf("Image click screen=%v err=%v", got.screen, got.err)
	}
	if got, _ := clickCommand(newModel(), 2); got.screen != ComposerScreen || got.draft.ReplyTo != nil {
		t.Fatalf("Reply Thread click screen=%v reply=%v", got.screen, got.draft.ReplyTo)
	}
	if got, _ := clickCommand(newModel(), 3); got.screen != ComposerScreen || got.draft.ReplyTo == nil || *got.draft.ReplyTo != "post_1" {
		t.Fatalf("Reply Post click screen=%v reply=%v", got.screen, got.draft.ReplyTo)
	}
	if _, cmd := clickCommand(newModel(), 4); cmd == nil {
		t.Fatal("Quit click returned no command")
	}
}

func TestReplyComposerChromeButtonsBypassTextSelection(t *testing.T) {
	body := "selectable source"
	newModel := func(pane replyPane) Model {
		m := NewModel(context.Background(), &fakeService{})
		m.width, m.height, m.loading = 120, 24, false
		m.boards = []generated.Board{{Slug: "general"}}
		m.screen, m.focus, m.activeThread = ThreadScreen, FocusContent, "thread_1"
		m.posts = []generated.Post{{Id: "post_1", Body: &body, State: generated.Visible}}
		m.beginReplyComposer("post_1")
		m.replyPane = pane
		if pane == replySourcePane {
			m.editor.Blur()
			m.replyPostAnchor, m.replyPostCursor = 0, 6
		}
		return m
	}
	clickCommand := func(m Model, index int) (Model, tea.Cmd) {
		x := 0
		for _, label := range m.commandLabels()[:index] {
			x += commandLabelWidth(label, false) + 1
		}
		updated, cmd := m.Update(tea.MouseMsg{X: x, Y: m.height - 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
		return updated.(Model), cmd
	}
	if got, cmd := clickCommand(newModel(replyEditorPane), 0); cmd == nil || !got.loading {
		t.Fatal("Send click was intercepted by editor pane")
	}
	if got, _ := clickCommand(newModel(replyEditorPane), 1); !got.attachmentPicking {
		t.Fatal("Attach click was intercepted by editor pane")
	}
	if got, _ := clickCommand(newModel(replyEditorPane), 2); got.replyPane != replySourcePane {
		t.Fatal("Source click did not switch pane")
	}
	if got, _ := clickCommand(newModel(replyEditorPane), 3); got.screen != ThreadScreen {
		t.Fatalf("Cancel click screen=%v", got.screen)
	}
	if got, cmd := clickCommand(newModel(replySourcePane), 0); cmd == nil || !got.clipboardCopied {
		t.Fatal("Copy click was intercepted by source pane")
	}
	if got, _ := clickCommand(newModel(replySourcePane), 1); got.replyPane != replyEditorPane {
		t.Fatal("Reply click did not switch pane")
	}
	if got, _ := clickCommand(newModel(replySourcePane), 2); got.screen != ThreadScreen {
		t.Fatalf("source Cancel click screen=%v", got.screen)
	}
	m := newModel(replyEditorPane)
	themeX := textWidth(" BORD  [← Назад] ") + 1
	updated, _ := m.Update(tea.MouseMsg{X: themeX, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if got := updated.(Model); !got.themeMenu {
		t.Fatal("top theme click was intercepted by reply composer")
	}
}

func TestCommandLabelsAndKeysStayAligned(t *testing.T) {
	m := NewModel(context.Background(), &fakeService{})
	for _, screen := range []Screen{CatalogScreen, ThreadScreen, PostScreen, AttachmentScreen, ComposerScreen, HelpScreen} {
		m.screen = screen
		if labels, keys := m.commandLabels(), m.commandKeys(); len(labels) != len(keys) {
			t.Fatalf("screen=%v labels=%d keys=%d", screen, len(labels), len(keys))
		}
	}
	reply := "post_1"
	m.screen, m.draft = ComposerScreen, Draft{ReplyTo: &reply}
	for _, pane := range []replyPane{replyEditorPane, replySourcePane} {
		m.replyPane = pane
		if labels, keys := m.commandLabels(), m.commandKeys(); len(labels) != len(keys) {
			t.Fatalf("reply pane=%v labels=%d keys=%d", pane, len(labels), len(keys))
		}
	}
}

func TestCompactCommandGeometryMatchesRenderedLabels(t *testing.T) {
	m := NewModel(context.Background(), &fakeService{})
	m.width, m.height, m.loading = 30, 24, false
	m.screen = ComposerScreen
	if !m.compactCommandBar(m.width) {
		t.Fatal("expected compact command bar")
	}
	labels := m.commandLabels()
	attachStart := commandLabelWidth(labels[0], true) + 1
	updated, _ := m.Update(tea.MouseMsg{X: attachStart, Y: m.height - 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if got := updated.(Model); !got.attachmentPicking {
		t.Fatalf("compact Attach click mapped incorrectly at x=%d", attachStart)
	}
}

func TestFitPreservesGraphemeWidth(t *testing.T) {
	setColorProfile(3)
	for _, tc := range []struct {
		value string
		width int
	}{
		{"👍🏽", 4},
		{"👨‍👩‍👧‍👦", 4},
		{"e\u0301", 3},
		{"🇺🇦", 3},
		{"1️⃣", 3},
	} {
		got := fit(tc.value, tc.width, ' ')
		if width := lipgloss.Width(got); width != tc.width {
			t.Fatalf("fit(%q, %d) width=%d: %q", tc.value, tc.width, width, got)
		}
		if !strings.HasPrefix(got, tc.value) {
			t.Fatalf("fit(%q, %d) split grapheme: %q", tc.value, tc.width, got)
		}
	}
}

func TestResponsiveScreensAndUnicodeUseExactTerminal(t *testing.T) {
	setColorProfile(1)
	defer setColorProfile(3)
	body := strings.Repeat("👍🏽👨‍👩‍👧‍👦e\u0301", 20) + "\x1b]52;c;c2VjcmV0\a"
	m := NewModel(context.Background(), &fakeService{})
	m.loading = false
	m.boards = []generated.Board{{Slug: body, Title: body}}
	m.threads = []generated.Thread{{Id: body, InitialPost: &generated.Post{Body: &body}}}
	for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 24}, {Width: 120, Height: 30}, {Width: 80, Height: 40}, {Width: 48, Height: 18}} {
		updated, _ := m.Update(size)
		m = updated.(Model)
		for _, screen := range []Screen{CatalogScreen, HelpScreen, ComposerScreen} {
			m.screen = screen
			if screen == ComposerScreen {
				m.editor.SetValue(body)
				m.editor.Focus()
			}
			view := m.View()
			assertDimensions(t, view, size.Width, size.Height)
			if strings.Contains(view, "\x1b]52") {
				t.Fatalf("untrusted escape survived in %v view", screen)
			}
			if screen == ComposerScreen && !strings.Contains(view, "\x1b[") {
				t.Fatal("textarea ANSI styling was stripped instead of width-aware rendering")
			}
			if screen == ComposerScreen && strings.Contains(view, "\uFFFD[") {
				t.Fatal("textarea ANSI styling was rendered as visible replacement text")
			}
		}
	}
}

func TestFocusedAndInactiveSelectionsHaveDistinctContrast(t *testing.T) {
	setColorProfile(1)
	defer setColorProfile(3)
	m := NewModel(context.Background(), &fakeService{})
	rows := []displayRow{{lines: []string{"selected"}, active: true}}
	active := m.singlePanel(palette(ThemeClassic), 20, 4, "Active", "ITEM", rows, true)
	inactive := m.singlePanel(palette(ThemeClassic), 20, 4, "Inactive", "ITEM", rows, false)
	if active[2] == inactive[2] {
		t.Fatal("focused and inactive selections use identical styling")
	}
}

func TestUntrustedTerminalControlsAreNeutralized(t *testing.T) {
	setColorProfile(3)
	malicious := "x\x1b[31m\x1b]52;c;YXR0YWNr\a\x00\u009bred"
	body := malicious + "\nsecond line"
	reply := malicious
	m := NewModel(context.Background(), &fakeService{})
	m.loading, m.width, m.height = false, 100, 20
	m.boards = []generated.Board{{Slug: malicious, Title: malicious}}
	m.activeThread, m.screen, m.focus = malicious, ThreadScreen, FocusContent
	m.posts = []generated.Post{{Id: malicious, AuthorId: malicious, ReplyToId: &reply, Body: &body, State: generated.Visible}}
	view := m.View()
	for _, forbidden := range []string{"\x1b", "\a", "\x00", "\u009b"} {
		if strings.Contains(view, forbidden) {
			t.Fatalf("unsafe control %q survived in view %q", forbidden, view)
		}
	}
	if !strings.Contains(view, "second line") || !strings.Contains(view, "[31m") || !strings.Contains(view, "]52;c;YXR0YWNr") {
		t.Fatalf("sanitizer unexpectedly discarded safe text: %q", view)
	}
}

func TestSanitizeTerminalTextCSIOSC52C0C1(t *testing.T) {
	input := "ok\x1b[2J\x1b]52;c;c2VjcmV0\a\x00\u0085\u009b31m\nline"
	got := SanitizeTerminalText(input)
	if strings.ContainsFunc(got, unsafeTerminalRune) || !strings.Contains(got, "line") {
		t.Fatalf("unsafe sanitized output %q", got)
	}
}

func setColorProfile(profile int64) {
	fn := reflect.ValueOf(lipgloss.SetColorProfile)
	value := reflect.New(fn.Type().In(0)).Elem()
	value.SetInt(profile)
	fn.Call([]reflect.Value{value})
}

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Fatalf("golden mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func assertDimensions(t *testing.T, view string, width, height int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) != height {
		t.Fatalf("height=%d want=%d", len(lines), height)
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got != width {
			t.Fatalf("line %d width=%d want=%d: %q", i, got, width, line)
		}
	}
}

func TestKeyboardOnlySmokeAndGracefulQuit(t *testing.T) {
	f := &fakeService{boards: []generated.Board{{Id: "board_1", Slug: "b", Title: "Random"}}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var output bytes.Buffer
	p := tea.NewProgram(NewModel(ctx, f), tea.WithInput(strings.NewReader("q")), tea.WithOutput(&output), tea.WithoutRenderer(), tea.WithContext(ctx))
	if _, err := p.Run(); err != nil {
		t.Fatal(err)
	}
}

func TestControlCCancelsComposerAndOutstandingWork(t *testing.T) {
	m := NewModel(context.Background(), &fakeService{})
	m.screen = ComposerScreen
	done := m.ctx.Done()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("ctrl+c did not quit composer")
	}
	select {
	case <-done:
	default:
		t.Fatal("model context was not cancelled")
	}
}

type failingInput struct{}

func (failingInput) Read([]byte) (int, error) { return 0, errors.New("injected terminal failure") }

func TestTerminalRestorationSubprocess(t *testing.T) {
	if os.Getenv("BORD_TUI_FAILURE_HELPER") == "1" {
		var output bytes.Buffer
		program := tea.NewProgram(NewModel(context.Background(), &fakeService{}), tea.WithInput(failingInput{}), tea.WithOutput(&output), tea.WithAltScreen(), tea.WithMouseCellMotion())
		_, err := program.Run()
		if err == nil || !strings.Contains(output.String(), "\x1b[?1049l") || !strings.Contains(output.String(), "\x1b[?1002l") || !strings.Contains(output.String(), "\x1b[?1006l") {
			fmt.Fprintf(os.Stderr, "err=%v output=%q", err, output.String())
			os.Exit(2)
		}
		return
	}
	command := exec.Command(os.Args[0], "-test.run=^TestTerminalRestorationSubprocess$")
	command.Env = append(os.Environ(), "BORD_TUI_FAILURE_HELPER=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("subprocess did not restore terminal: %v: %s", err, output)
	}
}

func TestScriptedFlowAgainstMockServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/boards":
			_, _ = w.Write([]byte(`{"boards":[{"id":"board_1","slug":"b","title":"Random"}]}`))
		case "/api/v1/boards/b/threads":
			_, _ = w.Write([]byte(`{"threads":[{"id":"thread_1","board_id":"board_1","created_at":"2026-10-17T12:00:00Z","updated_at":"2026-10-17T12:00:00Z","closed":false}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	api, err := client.New(server.URL, server.Client(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	m := NewModel(context.Background(), api)
	updated, cmd := m.Update(m.Init()())
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("boards did not schedule catalog")
	}
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if len(m.boards) != 1 || len(m.threads) != 1 || m.screen != CatalogScreen {
		t.Fatalf("model=%#v", m)
	}
}

func TestComposerPostsThreadAndReplyThroughProductionRuntime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := storage.Open(filepath.Join(t.TempDir(), "bord.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	runtime, err := integration.New(integration.Config{DB: db, Blobs: t.TempDir(), LookupKey: bytes.Repeat([]byte{1}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if err := runtime.SeedBoard(ctx, "general", "General"); err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub(32, 8)
	t.Cleanup(func() { _ = hub.Close() })
	go func() {
		_ = (&events.Dispatcher{Outbox: runtime.Outbox, Hub: hub, Interval: time.Millisecond, Batch: 20}).Run(ctx)
	}()
	cursors, err := serverapi.NewCursorCodec(bytes.Repeat([]byte{2}, 32), func() time.Time { return time.Now().UTC() })
	if err != nil {
		t.Fatal(err)
	}
	handler := serverapi.NewRouter(serverapi.Config{
		Public: integration.Public{Runtime: runtime}, Abuse: runtime.Abuse, Events: hub,
		CursorCodec: cursors, HeartbeatPeriod: 5 * time.Millisecond,
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	state, err := client.NewState(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	store := client.NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	transport, err := client.New(server.URL, server.Client(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	posting, err := client.NewPostingAPI(transport, store, &state)
	if err != nil {
		t.Fatal(err)
	}
	m := NewModel(ctx, posting)
	m.loading = false
	m.boards = []generated.Board{{Id: "board_1", Slug: "general", Title: "General"}}
	m.beginComposer("")
	m.editor.SetValue("created by composer")
	created := m.sendDraft()().(sentMsg)
	if created.err != nil || created.result.ThreadId == "" || created.result.PostId == "" {
		t.Fatalf("thread create=%#v", created)
	}
	updated, refresh := m.Update(created)
	m = updated.(Model)
	updated, watch := m.Update(refresh())
	m = updated.(Model)
	if len(m.posts) != 1 || watch == nil {
		t.Fatalf("thread not visible after refresh: %#v", m.posts)
	}

	streamReady := make(chan struct{}, 1)
	streamEvent := make(chan client.Event, 1)
	streamCtx, stopStream := context.WithCancel(ctx)
	defer stopStream()
	go func() {
		_ = posting.Subscribe(streamCtx, client.StreamScope{Thread: created.result.ThreadId}, client.StreamCallbacks{
			Heartbeat: func() {
				select {
				case streamReady <- struct{}{}:
				default:
				}
			},
			Event: func(event client.Event) {
				select {
				case streamEvent <- event:
				default:
				}
			},
		}, client.Backoff{Min: time.Millisecond, Max: time.Millisecond})
	}()
	select {
	case <-streamReady:
	case <-time.After(time.Second):
		t.Fatal("SSE stream did not become ready")
	}

	m.loading = false
	m.beginComposer(created.result.ThreadId)
	m.editor.SetValue("reply from composer")
	replied := m.sendDraft()().(sentMsg)
	if replied.err != nil || replied.result.PostId == "" {
		t.Fatalf("reply create=%#v", replied)
	}
	select {
	case event := <-streamEvent:
		if event.Type != "post.created" {
			t.Fatalf("SSE event=%#v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reply was not visible through SSE")
	}
	page, err := posting.Posts(ctx, created.result.ThreadId, nil, 100)
	if err != nil || len(page.Posts) != 2 {
		t.Fatalf("reply not visible after refresh: posts=%#v err=%v", page.Posts, err)
	}
	m.loading = false
	m.screen, m.activeThread = ThreadScreen, created.result.ThreadId
	m.beginReplyComposer(created.result.PostId)
	m.editor.SetValue("addressed reply from composer")
	addressed := m.sendDraft()().(sentMsg)
	if addressed.err != nil || addressed.result.PostId == "" {
		t.Fatalf("addressed reply create=%#v", addressed)
	}
	page, err = posting.Posts(ctx, created.result.ThreadId, nil, 100)
	if err != nil || len(page.Posts) != 3 || page.Posts[2].ReplyToId == nil || *page.Posts[2].ReplyToId != created.result.PostId {
		t.Fatalf("addressed reply target missing: posts=%#v err=%v", page.Posts, err)
	}
	loaded, err := store.Load()
	if err != nil || loaded.DeleteSecrets[created.result.PostId] == "" || loaded.DeleteSecrets[replied.result.PostId] == "" || loaded.DeleteSecrets[addressed.result.PostId] == "" || len(loaded.PendingDeleteSecrets) != 0 {
		t.Fatalf("resource credentials not atomically mapped: err=%v state=%#v", err, loaded)
	}
	if loaded.DeleteSecrets[created.result.PostId] == loaded.DeleteSecrets[replied.result.PostId] {
		t.Fatal("thread and reply reused one delete secret")
	}
}

type staticCapability bool

func (s staticCapability) KittyGraphics() bool { return bool(s) }
func TestKittyRendererAndFallback(t *testing.T) {
	r := KittyRenderer{Capability: staticCapability(false), Mode: InlineAuto}
	if _, ok := r.Render([]byte("image"), 10, 4); ok {
		t.Fatal("unknown terminal must fall back")
	}
	r.Mode = InlineKitty
	got, ok := r.Render([]byte("image"), 10, 4)
	if !ok || got != "\x1b_Ga=T,f=100,c=10,r=4,m=0;aW1hZ2U=\x1b\\" {
		t.Fatalf("%q %v", got, ok)
	}
	r.Mode = InlineOff
	if _, ok := r.Render([]byte("image"), 10, 4); ok {
		t.Fatal("off rendered")
	}
	r.Mode = InlineKitty
	if _, ok := r.Render(nil, 10, 4); ok {
		t.Fatal("empty image rendered")
	}
	if _, ok := r.Render([]byte("image"), 0, 4); ok {
		t.Fatal("invalid dimensions rendered")
	}
}

func TestAttachmentViewerVerifiesRendersAndOpens(t *testing.T) {
	var source bytes.Buffer
	imageData := image.NewRGBA(image.Rect(0, 0, 2, 2))
	imageData.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&source, imageData); err != nil {
		t.Fatal(err)
	}
	body := source.Bytes()
	sum := sha256.Sum256(body)
	meta := generated.Attachment{Id: "attachment_1", MediaType: generated.AttachmentMediaType("image/png"), Size: int64(len(body)), Sha256: hex.EncodeToString(sum[:]), Width: 2, Height: 2}
	f := &fakeService{attachmentBody: body, attachmentType: "image/png"}
	m := NewModel(context.Background(), f)
	m.width, m.height, m.loading = 80, 24, false
	m.screen, m.previous, m.activeThread = PostScreen, ThreadScreen, "thread_1"
	m.posts = []generated.Post{{Id: "post_1", State: generated.Visible, Attachment: &meta}}
	m.attachmentCache = client.NewCache(t.TempDir())
	m.attachmentRenderer = KittyRenderer{Capability: staticCapability(true), Mode: InlineAuto}
	var opened string
	m.openExternal = func(_ context.Context, path string) error { opened = path; return nil }

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m = updated.(Model)
	if m.screen != AttachmentScreen || !m.loading || cmd == nil {
		t.Fatalf("attachment did not start: screen=%v loading=%v cmd=%v", m.screen, m.loading, cmd)
	}
	updated, inlineCmd := m.Update(cmd())
	m = updated.(Model)
	if m.err != nil || m.attachmentFile == "" || filepath.Ext(m.attachmentFile) != ".png" || len(m.attachmentData) == 0 {
		t.Fatalf("attachment load: err=%v file=%q inline=%d", m.err, m.attachmentFile, len(m.attachmentData))
	}
	if inlineCmd == nil {
		t.Fatal("direct Kitty viewer did not start")
	}
	if strings.Contains(m.View(), "\x1b_G") {
		t.Fatal("Kitty image sequence must not pass through Bubble Tea View")
	}

	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil || !m.loading {
		t.Fatal("external opener did not start")
	}
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if m.err != nil || opened != m.attachmentFile {
		t.Fatalf("opened=%q file=%q err=%v", opened, m.attachmentFile, m.err)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.screen != PostScreen || m.attachmentFile != "" || len(m.attachmentData) != 0 {
		t.Fatalf("attachment back did not restore post: screen=%v file=%q", m.screen, m.attachmentFile)
	}
}

func TestAttachmentViewerRejectsChecksumMismatch(t *testing.T) {
	body := []byte("not the expected image")
	meta := generated.Attachment{Id: "attachment_1", MediaType: generated.AttachmentMediaType("image/png"), Size: int64(len(body)), Sha256: strings.Repeat("0", 64)}
	f := &fakeService{attachmentBody: body, attachmentType: "image/png"}
	m := NewModel(context.Background(), f)
	m.screen, m.activeThread = ThreadScreen, "thread_1"
	m.posts = []generated.Post{{Id: "post_1", Attachment: &meta}}
	m.attachmentCache = client.NewCache(t.TempDir())
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m = updated.(Model)
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if m.err == nil || m.attachmentFile != "" {
		t.Fatalf("checksum mismatch accepted: file=%q err=%v", m.attachmentFile, m.err)
	}
}

func TestPrepareKittyImageConvertsJPEGToPNG(t *testing.T) {
	var jpegBody bytes.Buffer
	if err := jpeg.Encode(&jpegBody, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	got, err := PrepareKittyImage(jpegBody.Bytes(), "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if _, format, err := image.Decode(bytes.NewReader(got)); err != nil || format != "png" {
		t.Fatalf("format=%q err=%v", format, err)
	}
}

func TestKittyViewerCommandWritesDirectProtocol(t *testing.T) {
	var output bytes.Buffer
	command := &kittyViewerCommand{data: []byte("image"), columns: 10, rows: 4}
	command.SetStdin(strings.NewReader("\n"))
	command.SetStdout(&output)
	command.SetStderr(io.Discard)
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if !strings.Contains(got, "\x1b_Ga=T,f=100,c=10,r=4,m=0;aW1hZ2U=\x1b\\") {
		t.Fatalf("direct Kitty payload missing: %q", got)
	}
	if !strings.Contains(got, "Enter — вернуться в Bord") || !strings.HasSuffix(got, "\x1b[?25h") {
		t.Fatalf("viewer prompt or cleanup missing: %q", got)
	}
}

func TestKittyViewerRestoresMouseTracking(t *testing.T) {
	m := NewModel(context.Background(), &fakeService{})
	m.loading, m.screen = true, AttachmentScreen
	updated, cmd := m.Update(attachmentInlineClosedMsg{})
	m = updated.(Model)
	if cmd == nil || m.loading || m.err != nil {
		t.Fatalf("mouse restore not scheduled: cmd=%v loading=%v err=%v", cmd, m.loading, m.err)
	}
	if msg := cmd(); reflect.TypeOf(msg) != reflect.TypeOf(tea.EnableMouseCellMotion()) {
		t.Fatalf("restore command returned %T", msg)
	}
}
