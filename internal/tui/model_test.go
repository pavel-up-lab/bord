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
	"bord/internal/client"

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
	second := m.sendDraft()().(sentMsg)
	if len(f.keys) != 2 || f.keys[0] == "" || f.keys[0] != f.keys[1] || second.err == nil {
		t.Fatalf("keys=%#v second=%#v", f.keys, second)
	}
	f.createErr = nil
	updated, cmd := m.Update(sentMsg{id: m.requestID, result: generated.CreateResult{ThreadId: "thread_1", PostId: "post_1"}})
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
	m := NewModel(context.Background(), &fakeService{})
	m.loading = false
	m.boards = []generated.Board{{Slug: "b", Title: "Random"}, {Slug: "tech", Title: "Technology"}}
	m.threads = []generated.Thread{{Id: "thread_0001", UpdatedAt: time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)}}
	m.screen = CatalogScreen
	for _, tc := range []struct {
		name  string
		width int
	}{{"wide", 100}, {"narrow", 60}} {
		t.Run(tc.name, func(t *testing.T) { m.width = tc.width; m.height = 20; assertGolden(t, tc.name+".golden", m.View()) })
	}
	setColorProfile(1) // termenv.ANSI256
	m.width = 100
	assertGolden(t, "wide-ansi256.golden", m.View())
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
}
