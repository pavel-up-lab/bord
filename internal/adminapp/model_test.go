package adminapp

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"bord/api/generated"
	"bord/internal/adminclient"
	"bord/internal/admintui"
	"bord/internal/client"
	"bord/internal/terminalui"
	"bord/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

type adminService struct{}

func (adminService) Session(context.Context) (adminclient.Session, error) {
	return adminclient.Session{}, nil
}
func (adminService) Storage(context.Context) (adminclient.SystemStorage, error) {
	return adminclient.SystemStorage{}, nil
}
func (adminService) Reports(context.Context, *string, int) (adminclient.Page[adminclient.Report], error) {
	return adminclient.Page[adminclient.Report]{}, nil
}
func (adminService) PostContext(context.Context, string, *string, int) (adminclient.PostContext, error) {
	return adminclient.PostContext{}, nil
}
func (adminService) Attachment(context.Context, string) ([]byte, string, error) { return nil, "", nil }
func (adminService) Bans(context.Context, string, *string, int) (adminclient.Page[adminclient.BanRecord], error) {
	return adminclient.Page[adminclient.BanRecord]{}, nil
}
func (adminService) Audit(context.Context, *string, int) (adminclient.Page[adminclient.AuditEntry], error) {
	return adminclient.Page[adminclient.AuditEntry]{}, nil
}
func (adminService) Boards(context.Context, string, *string, int) (adminclient.Page[adminclient.Board], error) {
	return adminclient.Page[adminclient.Board]{}, nil
}
func (adminService) CreateBoard(context.Context, adminclient.CreateBoardRequest) (adminclient.Board, error) {
	return adminclient.Board{}, nil
}
func (adminService) ArchiveBoard(context.Context, string, string) (adminclient.Board, error) {
	return adminclient.Board{}, nil
}
func (adminService) RestoreBoard(context.Context, string, string) (adminclient.Board, error) {
	return adminclient.Board{}, nil
}
func (adminService) SetVisibility(context.Context, string, bool, string) error { return nil }
func (adminService) CloseThread(context.Context, string, string) error         { return nil }
func (adminService) CreatePostBan(context.Context, string, adminclient.PostBanRequest) (adminclient.Ban, error) {
	return adminclient.Ban{}, nil
}
func (adminService) RevokeBan(context.Context, string, string) error          { return nil }
func (adminService) RevealIP(context.Context, string, string) (string, error) { return "", nil }

type publicService struct{ created int }

func (*publicService) Boards(context.Context) ([]generated.Board, error) {
	return []generated.Board{{Id: "00000001", Slug: "general", Title: "General"}}, nil
}
func (*publicService) Threads(context.Context, string, *string, int) (generated.ThreadPage, error) {
	return generated.ThreadPage{Threads: []generated.Thread{}}, nil
}
func (*publicService) Posts(context.Context, string, *string, int) (generated.PostPage, error) {
	return generated.PostPage{}, nil
}
func (s *publicService) CreateThread(context.Context, string, string, generated.CreateThread) (generated.CreateResult, error) {
	s.created++
	return generated.CreateResult{ThreadId: "00000001", PostId: "00000001"}, nil
}
func (*publicService) CreatePost(context.Context, string, string, generated.CreatePost) (generated.CreateResult, error) {
	return generated.CreateResult{}, nil
}
func (*publicService) DeleteOwnPost(context.Context, string) error { return nil }
func (*publicService) UploadAttachment(context.Context, string, io.Reader) (generated.Attachment, error) {
	return generated.Attachment{}, nil
}
func (*publicService) Attachment(context.Context, string) ([]byte, string, error) {
	return nil, "", nil
}
func (*publicService) Report(context.Context, string, generated.CreateReport) error { return nil }
func (*publicService) Subscribe(ctx context.Context, _ client.StreamScope, _ client.StreamCallbacks, _ client.Backoff) error {
	<-ctx.Done()
	return ctx.Err()
}

func newApp(t *testing.T) (Model, *publicService) {
	t.Helper()
	themes := terminalui.DiscoverThemes()
	session := adminclient.Session{Id: "root", Role: generated.Administrator, ExpiresAt: time.Now().Add(time.Hour)}
	admin := admintui.NewModel(context.Background(), adminService{}).WithSession(session).WithThemes(themes, terminalui.ThemeClassic)
	service := &publicService{}
	user := tui.NewModel(context.Background(), service).WithThemes(themes, terminalui.ThemeClassic)
	app := New(admin, user, themes, "admin")
	updated, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	app = updated.(Model)
	updated, _ = app.Update(tag(UserMode, app.user.Init())())
	return updated.(Model), service
}

func TestModeSwitchPreservesChildrenDraftAndRoutesAsyncMessages(t *testing.T) {
	app, public := newApp(t)
	updated, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	app = updated.(Model)
	if app.admin.Screen() != admintui.BoardsScreen || cmd == nil {
		t.Fatal("admin selection not retained")
	}
	updated, _ = app.Update(tea.KeyMsg{Type: tea.KeyF3})
	app = updated.(Model)
	if app.mode != UserMode {
		t.Fatal("did not switch to user")
	}
	updated, cmd = app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = updated.(Model)
	if cmd != nil {
		updated, _ = app.Update(cmd())
		app = updated.(Model)
	}
	updated, _ = app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	app = updated.(Model)
	updated, _ = app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("draft survives")})
	app = updated.(Model)
	if !strings.Contains(app.View(), "draft survives") {
		t.Fatalf("draft missing: %s", app.View())
	}
	updated, _ = app.Update(tea.KeyMsg{Type: tea.KeyF3})
	app = updated.(Model)
	if app.admin.Screen() != admintui.BoardsScreen {
		t.Fatal("admin screen lost")
	}
	updated, _ = app.Update(tea.KeyMsg{Type: tea.KeyF3})
	app = updated.(Model)
	if !strings.Contains(app.View(), "draft survives") {
		t.Fatal("user draft lost after round trip")
	}
	updated, cmd = app.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	app = updated.(Model)
	if cmd == nil {
		t.Fatal("publish command missing")
	}
	updated, _ = app.Update(cmd())
	app = updated.(Model)
	if public.created != 1 {
		t.Fatalf("public creates=%d", public.created)
	}
}

func TestModeBarMouseConsumesClickAndWorksNarrow(t *testing.T) {
	app, _ := newApp(t)
	updated, _ := app.Update(tea.WindowSizeMsg{Width: 24, Height: 6})
	app = updated.(Model)
	updated, _ = app.Update(tea.MouseMsg{X: 20, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	app = updated.(Model)
	if app.mode != UserMode || !strings.Contains(app.View(), "A Admin") {
		t.Fatalf("mode bar failed: %s", app.View())
	}
	updated, _ = app.Update(tea.MouseMsg{X: 1, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if updated.(Model).mode != AdminMode {
		t.Fatal("admin click leaked or failed")
	}
}

func TestThemeSelectionSynchronizesBothPersistentChildren(t *testing.T) {
	app, _ := newApp(t)
	updated, _ := app.Update(tea.KeyMsg{Type: tea.KeyF2})
	app = updated.(Model)
	updated, _ = app.Update(tea.KeyMsg{Type: tea.KeyDown})
	app = updated.(Model)
	updated, _ = app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = updated.(Model)
	if app.admin.ThemeSetting() == "classic" || app.user.ThemeSetting() != app.admin.ThemeSetting() {
		t.Fatalf("admin=%q user=%q", app.admin.ThemeSetting(), app.user.ThemeSetting())
	}
	app.mode = UserMode
	updated, _ = app.Update(tea.KeyMsg{Type: tea.KeyF2})
	app = updated.(Model)
	updated, _ = app.Update(tea.KeyMsg{Type: tea.KeyDown})
	app = updated.(Model)
	updated, _ = app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = updated.(Model)
	if app.user.ThemeSetting() != app.admin.ThemeSetting() {
		t.Fatal("user theme did not flow to admin")
	}
}

func TestImageModeSynchronizesAndModeSwitchQueuesCleanup(t *testing.T) {
	themes := terminalui.DiscoverThemes()
	session := terminalui.NewImageSession(io.Discard)
	capabilities := terminalui.TerminalCapabilities{KittyPlaceholders: true, Sixel: true}
	viewer := admintui.TerminalImageViewer{Renderer: terminalui.KittyRenderer{Capability: capabilities, Mode: terminalui.InlineAuto}, Session: session, Sixel: &terminalui.SixelRenderer{}}
	admin := admintui.NewModel(context.Background(), adminService{}).WithImageViewer(viewer)
	user := tui.NewModel(context.Background(), &publicService{}).WithAttachmentEnvironment(tui.InlineSixel, nil, capabilities, session)
	app := New(admin, user, themes, "user")

	updated, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	app = updated.(Model)
	if app.admin.ImageModeSetting() != "sixel" {
		t.Fatalf("admin image mode=%q", app.admin.ImageModeSetting())
	}

	handle := session.Activate(terminalui.NativeKitty, "active-user-image", 20, 8)
	updated, cmd := app.Update(tea.KeyMsg{Type: tea.KeyF3})
	app = updated.(Model)
	if cmd == nil || app.mode != AdminMode || !strings.Contains(app.View(), "i="+fmt.Sprint(handle.ID)) || !strings.Contains(app.View(), "a=d") {
		t.Fatalf("mode=%v cmd=%v view=%q", app.mode, cmd, app.View())
	}
}

func TestTaggedBatchKeepsChildOwnership(t *testing.T) {
	command := tag(UserMode, tea.Batch(
		func() tea.Msg { return "one" },
		func() tea.Msg { return "two" },
	))
	batch, ok := command().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("tagged batch=%T %#v", command(), command())
	}
	for _, child := range batch {
		message, ok := child().(taggedMsg)
		if !ok || message.mode != UserMode {
			t.Fatalf("child message=%T %#v", child(), child())
		}
	}
}
