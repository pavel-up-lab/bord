package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

func TestWindowResizeConsumesExactTerminalAndThemeCycles(t *testing.T) {
	setColorProfile(3)
	m := NewModel(context.Background(), &fakeService{})
	for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 24}, {Width: 132, Height: 31}, {Width: 42, Height: 13}, {Width: 1, Height: 1}} {
		updated, _ := m.Update(size)
		m = updated.(Model)
		assertDimensions(t, m.View(), max(1, size.Width), max(1, size.Height))
	}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)
	for _, want := range []ThemeName{ThemeAmber, ThemeGreen, ThemeMono, ThemeClassic} {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
		m = updated.(Model)
		if m.Theme() != want {
			t.Fatalf("theme=%q want=%q", m.Theme(), want)
		}
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
		program := tea.NewProgram(NewModel(context.Background(), &fakeService{}), tea.WithInput(failingInput{}), tea.WithOutput(&output), tea.WithAltScreen())
		_, err := program.Run()
		if err == nil || !strings.Contains(output.String(), "\x1b[?1049l") {
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
	loaded, err := store.Load()
	if err != nil || loaded.DeleteSecrets[created.result.PostId] == "" || loaded.DeleteSecrets[replied.result.PostId] == "" || len(loaded.PendingDeleteSecrets) != 0 {
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
