package admintui

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bord/api/generated"
	"bord/internal/adminclient"
	"bord/internal/terminalui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type serviceFake struct {
	session           adminclient.Session
	storage           adminclient.SystemStorage
	reports           adminclient.Page[adminclient.Report]
	context           adminclient.PostContext
	bans              adminclient.Page[adminclient.BanRecord]
	audit             adminclient.Page[adminclient.AuditEntry]
	boards            adminclient.Page[adminclient.Board]
	err               error
	visibilityCalls   int
	lastReason        string
	createBoardCalls  int
	lastBoardRequest  adminclient.CreateBoardRequest
	archiveBoardCalls int
	restoreBoardCalls int
}

func (s *serviceFake) Session(context.Context) (adminclient.Session, error) { return s.session, s.err }
func (s *serviceFake) Storage(context.Context) (adminclient.SystemStorage, error) {
	return s.storage, s.err
}
func (s *serviceFake) Reports(context.Context, *string, int) (adminclient.Page[adminclient.Report], error) {
	return s.reports, s.err
}
func (s *serviceFake) PostContext(context.Context, string, *string, int) (adminclient.PostContext, error) {
	return s.context, s.err
}
func (*serviceFake) Attachment(context.Context, string) ([]byte, string, error) {
	return []byte("image"), "image/png", nil
}
func (s *serviceFake) Bans(context.Context, string, *string, int) (adminclient.Page[adminclient.BanRecord], error) {
	return s.bans, s.err
}
func (s *serviceFake) Audit(context.Context, *string, int) (adminclient.Page[adminclient.AuditEntry], error) {
	return s.audit, s.err
}
func (s *serviceFake) Boards(context.Context, string, *string, int) (adminclient.Page[adminclient.Board], error) {
	return s.boards, s.err
}
func (s *serviceFake) CreateBoard(_ context.Context, request adminclient.CreateBoardRequest) (adminclient.Board, error) {
	s.createBoardCalls++
	s.lastBoardRequest = request
	return adminclient.Board{Id: "00000003", Slug: request.Slug, Title: request.Title, State: "active"}, s.err
}
func (s *serviceFake) ArchiveBoard(_ context.Context, id, reason string) (adminclient.Board, error) {
	s.archiveBoardCalls++
	s.lastReason = reason
	return adminclient.Board{Id: id, State: "archived"}, s.err
}
func (s *serviceFake) RestoreBoard(_ context.Context, id, reason string) (adminclient.Board, error) {
	s.restoreBoardCalls++
	s.lastReason = reason
	return adminclient.Board{Id: id, State: "active"}, s.err
}
func (s *serviceFake) SetVisibility(_ context.Context, _ string, _ bool, reason string) error {
	s.visibilityCalls++
	s.lastReason = reason
	return s.err
}
func (*serviceFake) CloseThread(context.Context, string, string) error { return nil }
func (*serviceFake) CreatePostBan(context.Context, string, adminclient.PostBanRequest) (adminclient.Ban, error) {
	return adminclient.Ban{Id: "00000009"}, nil
}
func (*serviceFake) RevokeBan(context.Context, string, string) error { return nil }
func (*serviceFake) RevealIP(context.Context, string, string) (string, error) {
	return "192.0.2.1", nil
}

func fixtureService() *serviceFake {
	body := "hidden context"
	filename := "safe.png"
	return &serviceFake{
		session: adminclient.Session{Id: "root", Role: "administrator", ExpiresAt: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)},
		storage: adminclient.SystemStorage{TotalBytes: 40 << 30, AvailableBytes: 12 << 30},
		reports: adminclient.Page[adminclient.Report]{Items: []adminclient.Report{{Id: "00000001", PostId: "00000002", Reason: "spam", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}}},
		context: adminclient.PostContext{Board: generated.AdminBoardSummary{Id: "00000001", Slug: "general", Title: "General"}, Thread: generated.AdminThreadSummary{Id: "00000001"}, TargetPostId: "00000002", Posts: []generated.Post{{Id: "00000002", ThreadId: "00000001", AuthorId: "author", State: "hidden", Body: &body, Attachment: &generated.Attachment{Id: "attach_1", MediaType: "image/png", Filename: &filename, Sha256: strings.Repeat("a", 64), Size: 5, Width: 1, Height: 1}, CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}}},
		bans:    adminclient.Page[adminclient.BanRecord]{Items: []adminclient.BanRecord{{Id: "00000009", SubjectType: "lookup", Subject: "fingerprint:abcdef", Operation: "create_post", Reason: "spam", State: "active", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), ExpiresAt: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)}}},
		audit:   adminclient.Page[adminclient.AuditEntry]{Items: []adminclient.AuditEntry{{Id: "00000010", Action: "post.visibility", Outcome: "failure", RequestId: "request-001", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}}},
		boards:  adminclient.Page[adminclient.Board]{Items: []adminclient.Board{{Id: "00000001", Slug: "general", Title: "General", State: "active", ThreadCount: 2, PostCount: 4}, {Id: "00000002", Slug: "archive", Title: "Archive", State: "archived"}}},
	}
}

func loadedModel(t *testing.T) Model {
	t.Helper()
	service := fixtureService()
	model := NewModel(context.Background(), service)
	model.width, model.height = 100, 20
	updated, cmd := model.Update(sessionMsg{id: 1, value: service.session})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("reports command missing")
	}
	updated, cmd = model.Update(cmd())
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("context command missing")
	}
	updated, cmd = model.Update(cmd())
	model = updated.(Model)
	if cmd != nil {
		updated, _ = model.Update(cmd())
		model = updated.(Model)
	}
	return model
}

func TestReducerIgnoresStaleResponsesAndKeepsErrors(t *testing.T) {
	m := loadedModel(t)
	before := len(m.reports)
	stale := reportsMsg{id: m.requestID - 1, page: adminclient.Page[adminclient.Report]{Items: []adminclient.Report{{Id: "stale"}}}}
	updated, _ := m.Update(stale)
	m = updated.(Model)
	if len(m.reports) != before {
		t.Fatalf("stale response changed reports: %#v", m.reports)
	}
	updated, _ = m.Update(reportsMsg{id: m.requestID, err: errors.New("credential=secret backend detail")})
	m = updated.(Model)
	if m.err == nil || strings.Contains(m.View(), "secret") {
		t.Fatalf("unsafe error view=%q", m.View())
	}
}

func TestReportsRoleLoadsQueueWithoutForbiddenContextRequest(t *testing.T) {
	service := fixtureService()
	service.session.Role = "reports"
	model := NewModel(context.Background(), service)
	updated, cmd := model.Update(sessionMsg{id: 1, value: service.session})
	model = updated.(Model)
	updated, next := model.Update(cmd())
	model = updated.(Model)
	if next != nil || len(model.reports) != 1 || model.err != nil || strings.Contains(strings.Join(model.commandLabels(), " "), "Hide") {
		t.Fatalf("reports model=%#v commands=%v", model, model.commandLabels())
	}
}

func TestEnterPaginatesActiveReportAndContextPanels(t *testing.T) {
	m := loadedModel(t)
	cursor := "next"
	m.reportsCursor = &cursor
	m.reportIndex = len(m.reports) - 1
	m.focus = ListFocus
	updated, cmd := m.activateSelection()
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("reports pagination command missing")
	}
	if _, ok := cmd().(reportsMsg); !ok {
		t.Fatalf("reports command returned %T", cmd())
	}
	m.loading = false
	m.contextCursor = &cursor
	m.focus = ContextFocus
	updated, cmd = m.activateSelection()
	if cmd == nil {
		t.Fatal("context pagination command missing")
	}
	if _, ok := cmd().(contextMsg); !ok {
		t.Fatalf("context command returned %T", cmd())
	}
	_ = updated
}

func TestScreenSwitchCancelsRequestAndRefreshIsRetrySafe(t *testing.T) {
	m := loadedModel(t)
	updated, cmd := m.selectScreen(BansScreen)
	m = updated.(Model)
	if !m.loading || cmd == nil || m.screen != BansScreen {
		t.Fatalf("screen=%v loading=%v cmd=%v", m.screen, m.loading, cmd != nil)
	}
	firstContext := m.requestContext
	updated, cmd = m.startScreenLoad(false)
	m = updated.(Model)
	select {
	case <-firstContext.Done():
	default:
		t.Fatal("previous request was not cancelled")
	}
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if m.loading || len(m.bans) != 1 {
		t.Fatalf("bans=%#v loading=%v", m.bans, m.loading)
	}
	updated, cmd = m.selectScreen(AuditScreen)
	m = updated.(Model)
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if len(m.audit) != 1 || m.screen != AuditScreen {
		t.Fatalf("audit screen=%#v", m)
	}
}

func TestAdministratorBoardScreenFiltersPaginationAndMouse(t *testing.T) {
	m := loadedModel(t)
	updated, cmd := m.selectScreen(BoardsScreen)
	m = updated.(Model)
	if cmd == nil || m.screen != BoardsScreen {
		t.Fatalf("screen=%v cmd=%v", m.screen, cmd)
	}
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if len(m.boards) != 2 || !strings.Contains(m.View(), "/general/") || !strings.Contains(m.View(), "[archived]") {
		t.Fatalf("boards=%#v view=%q", m.boards, m.View())
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyF6})
	m = updated.(Model)
	if m.boardState != "active" {
		t.Fatalf("state=%q", m.boardState)
	}
	m.loading = false
	cursor := "next-board"
	m.boardsCursor = &cursor
	m.boardIndex = len(m.boards) - 1
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("board pagination command missing")
	}
	if message, ok := cmd().(boardsMsg); !ok || !message.append {
		t.Fatalf("pagination message=%T %#v", cmd(), cmd())
	}
	m = updated.(Model)
	m.loading = false
	updated, _ = m.Update(tea.MouseMsg{X: 5, Y: 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if got := updated.(Model).boardIndex; got != 0 {
		t.Fatalf("wide board mouse index=%d", got)
	}
	m = updated.(Model)
	m.width = 50
	updated, _ = m.Update(tea.MouseMsg{X: 5, Y: 4, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if got := updated.(Model).boardIndex; got != 1 {
		t.Fatalf("narrow board mouse index=%d", got)
	}
}

func TestNonAdministratorCannotOpenBoardScreen(t *testing.T) {
	for _, role := range []string{"reports", "moderator"} {
		service := fixtureService()
		service.session.Role = generated.AdminRole(role)
		m := NewModel(context.Background(), service).WithSession(service.session)
		m.loading = false
		updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
		got := updated.(Model)
		if got.screen == BoardsScreen || cmd != nil || strings.Contains(got.topBar(got.themes.Palette(got.theme), 120), "[4 Доски]") {
			t.Fatalf("role=%s opened board screen", role)
		}
	}
}

func TestCreateBoardModalValidationConfirmationRetryAndMouseFocus(t *testing.T) {
	m := loadedModel(t)
	m.screen = BoardsScreen
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = updated.(Model)
	if m.modal == nil || m.modal.kind != modalCreateBoard || m.modal.field != 0 {
		t.Fatalf("modal=%#v", m.modal)
	}
	updated, _ = m.Update(tea.MouseMsg{X: 10, Y: 6, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if m.modal.field != 1 {
		t.Fatalf("mouse field=%d", m.modal.field)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.modal != nil {
		t.Fatal("Esc did not cancel create modal")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = updated.(Model)
	m.modal.slug.SetValue("Bad Slug")
	m.modal.title.SetValue("Games")
	m.modal.editor.SetValue("create category")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if cmd != nil || !strings.Contains(m.modal.err, "Slug") {
		t.Fatalf("invalid slug submitted cmd=%v err=%q", cmd, m.modal.err)
	}
	m.modal.slug.SetValue("games")
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if cmd != nil || !m.modal.confirmed {
		t.Fatal("first confirmation executed mutation")
	}
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if cmd == nil || !m.modal.pending {
		t.Fatal("confirmed create did not start")
	}
	service := m.service.(*serviceFake)
	service.err = errors.New("backend unavailable")
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if m.modal == nil || m.modal.pending || m.modal.slug.Value() != "games" || service.createBoardCalls != 1 {
		t.Fatalf("retry state modal=%#v calls=%d", m.modal, service.createBoardCalls)
	}
	service.err = nil
	m.modal.confirmed = true
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if m.modal != nil || service.lastBoardRequest.Reason != "create category" || service.createBoardCalls != 2 {
		t.Fatalf("create result modal=%#v request=%#v calls=%d", m.modal, service.lastBoardRequest, service.createBoardCalls)
	}
}

func TestCreateBoardModalIsResponsiveAndLabelsFields(t *testing.T) {
	for _, width := range []int{34, 80, 140} {
		m := loadedModel(t)
		m.screen = BoardsScreen
		updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 22})
		m = updated.(Model)
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
		m = updated.(Model)

		if m.modal.slug.Width > max(1, width-6) || m.modal.title.Width > max(1, width-6) {
			t.Fatalf("width=%d fields slug=%d title=%d", width, m.modal.slug.Width, m.modal.title.Width)
		}
		view := m.View()
		for row, line := range strings.Split(view, "\n") {
			if got := terminalui.TextWidth(line); got != width {
				t.Fatalf("width=%d row=%d rendered=%d", width, row, got)
			}
		}
		plain := ansi.Strip(view)
		if strings.Contains(plain, "┃") {
			t.Fatalf("width=%d contains textarea decoration: %q", width, plain)
		}
		if width >= 80 {
			for _, fragment := range []string{"> 1/3 Slug — адрес", "2/3 Название", "3/3 Причина", "Tab / Shift+Tab — выбрать поле"} {
				if !strings.Contains(plain, fragment) {
					t.Fatalf("width=%d missing %q in %q", width, fragment, plain)
				}
			}
		}
	}
}

func TestArchiveRestoreRequireSecondConfirmationAndPreserveConflictState(t *testing.T) {
	m := loadedModel(t)
	m.screen, m.boards, m.boardIndex = BoardsScreen, fixtureService().boards.Items, 0
	service := m.service.(*serviceFake)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = updated.(Model)
	if m.modal == nil || m.modal.kind != modalArchiveBoard {
		t.Fatalf("archive modal=%#v", m.modal)
	}
	m.modal.editor.SetValue("retire category")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if cmd != nil || service.archiveBoardCalls != 0 || !m.modal.confirmed {
		t.Fatal("archive ran before second confirmation")
	}
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if cmd == nil || !m.modal.pending {
		t.Fatal("archive did not enter pending state")
	}
	blocked, blockedCmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if blockedCmd != nil || blocked.(Model).modal == nil {
		t.Fatal("pending archive accepted input")
	}
	service.err = &adminclient.Error{StatusCode: http.StatusConflict, Code: "conflict"}
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if m.modal == nil || m.boardIndex != 0 || service.archiveBoardCalls != 1 || !strings.Contains(m.modal.err, "conflict") {
		t.Fatalf("conflict lost state modal=%#v index=%d calls=%d", m.modal, m.boardIndex, service.archiveBoardCalls)
	}
	service.err = nil
	m.modal = nil
	m.boards[1].State = "archived"
	m.boardIndex = 1
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	m = updated.(Model)
	if m.modal == nil || m.modal.kind != modalRestoreBoard {
		t.Fatalf("restore modal=%#v", m.modal)
	}
	m.modal.editor.SetValue("reopen category")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if service.restoreBoardCalls != 1 || m.modal != nil {
		t.Fatalf("restore calls=%d modal=%#v", service.restoreBoardCalls, m.modal)
	}
}

func TestDocumentedBoardShortcutsAreWired(t *testing.T) {
	m := loadedModel(t)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	m = updated.(Model)
	if m.screen != BoardsScreen || cmd == nil {
		t.Fatal("4 did not open boards")
	}
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	for _, label := range []string{"F6 All/Active/Archived", "N New", "D Archive", "U Restore"} {
		if !strings.Contains(strings.Join(m.commandLabels(), " "), label) {
			t.Fatalf("missing command label %q", label)
		}
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyF6})
	m = updated.(Model)
	if m.boardState != "active" {
		t.Fatal("F6 did not change board filter")
	}
	m.loading = false
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = updated.(Model)
	if m.modal == nil || m.modal.kind != modalCreateBoard {
		t.Fatal("N did not open create")
	}
	m.modal = nil
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = updated.(Model)
	if m.modal == nil || m.modal.kind != modalArchiveBoard {
		t.Fatal("D did not open archive")
	}
	m.modal = nil
	m.boardIndex = 1
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	m = updated.(Model)
	if m.modal == nil || m.modal.kind != modalRestoreBoard {
		t.Fatal("U did not open restore")
	}
}

func TestPaginationDeduplicatesAndKeyboardMouseNavigate(t *testing.T) {
	m := loadedModel(t)
	cursor := "next-page"
	m.reportsCursor = &cursor
	page := adminclient.Page[adminclient.Report]{Items: []adminclient.Report{m.reports[0], {Id: "00000003", PostId: "00000004", Reason: "duplicate-safe", CreatedAt: time.Now()}}}
	updated, _ := m.Update(reportsMsg{id: m.requestID, page: page, append: true})
	m = updated.(Model)
	if len(m.reports) != 2 {
		t.Fatalf("dedupe reports=%#v", m.reports)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if m.reportIndex != 1 {
		t.Fatalf("keyboard index=%d", m.reportIndex)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if m.focus != ContextFocus {
		t.Fatal("Tab did not switch panel")
	}
	m.width = 60
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	updated, _ = m.Update(tea.MouseMsg{X: 2, Y: 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if m.focus != ListFocus {
		t.Fatal("mouse did not switch narrow panel")
	}
}

func TestAdminLayoutGolden(t *testing.T) {
	for _, test := range []struct {
		name          string
		width, height int
	}{{"wide", 100, 20}, {"narrow", 60, 16}} {
		t.Run(test.name, func(t *testing.T) {
			m := loadedModel(t)
			m.width, m.height = test.width, test.height
			got := ansi.Strip(m.View())
			path := filepath.Join("testdata", test.name+".golden")
			if os.Getenv("UPDATE_GOLDEN") == "1" {
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
			if got != string(want) {
				t.Fatalf("golden mismatch\nwant:\n%s\ngot:\n%s", want, got)
			}
			for _, line := range strings.Split(got, "\n") {
				if terminalui.TextWidth(line) != test.width {
					t.Fatalf("line width=%d want=%d: %q", terminalui.TextWidth(line), test.width, line)
				}
			}
		})
	}
}

func TestStorageCapacityStatusAndRefresh(t *testing.T) {
	m := loadedModel(t)
	status := m.statusLine()
	if !strings.Contains(status, "диск:12.0 GiB свободно из 40.0 GiB") {
		t.Fatalf("status=%q", status)
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyF5})
	m = updated.(Model)
	if cmd == nil || !m.storageRefreshPending {
		t.Fatalf("storage refresh was not scheduled: pending=%v cmd=%v", m.storageRefreshPending, cmd)
	}
}

func TestBanAndAuditViewsPaginateWithoutSensitiveFields(t *testing.T) {
	m := loadedModel(t)
	m.width, m.height = 100, 14
	for _, screen := range []Screen{BansScreen, AuditScreen} {
		updated, cmd := m.selectScreen(screen)
		m = updated.(Model)
		updated, _ = m.Update(cmd())
		m = updated.(Model)
		view := ansi.Strip(m.View())
		if strings.Contains(view, "credential") || strings.Contains(view, "payload") || strings.Contains(view, "192.0.2.") {
			t.Fatalf("sensitive %v view=%q", screen, view)
		}
		if screen == BansScreen && !strings.Contains(view, "fingerprint:abcdef") {
			t.Fatalf("safe fingerprint missing: %q", view)
		}
		if screen == AuditScreen && (!strings.Contains(view, "failure") || !strings.Contains(view, "post.visibility")) {
			t.Fatalf("audit fields missing: %q", view)
		}
	}
	cursor := "cursor-next"
	m.screen = BansScreen
	m.bansCursor = &cursor
	page := adminclient.Page[adminclient.BanRecord]{Items: []adminclient.BanRecord{m.bans[0], {Id: "00000011", SubjectType: "lookup", Subject: "fingerprint:1234", Operation: "report", Reason: "flood", State: "expired", CreatedAt: time.Now(), ExpiresAt: time.Now()}}}
	updated, _ := m.Update(bansMsg{id: m.requestID, page: page, append: true})
	m = updated.(Model)
	if len(m.bans) != 2 {
		t.Fatalf("ban dedupe=%#v", m.bans)
	}
}

type viewerFake struct {
	calls, opens, clears int
	meta                 terminalui.ImageMetadata
	err                  error
}

func (v *viewerFake) Render(meta terminalui.ImageMetadata, _ []byte, _ string, _, _ int) (ImageView, error) {
	v.calls++
	v.meta = meta
	if v.err != nil {
		return ImageView{}, v.err
	}
	return ImageView{Lines: []string{"frame"}, Backend: "fake", Path: "/tmp/image", Control: "upload"}, nil
}
func (v *viewerFake) Clear() string { v.clears++; return "cleanup" }
func (v *viewerFake) Open(context.Context, string, bool) tea.Cmd {
	v.opens++
	return func() tea.Msg { return viewerClosedMsg{} }
}

func TestAttachmentUsesSharedViewerAndReturnsToMouseModel(t *testing.T) {
	m := loadedModel(t)
	viewer := &viewerFake{}
	m.viewer = viewer
	updated, cmd := m.openAttachment()
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("attachment command missing")
	}
	updated, viewerCmd := m.Update(cmd())
	m = updated.(Model)
	if viewer.calls != 1 || viewerCmd == nil || m.screen != AttachmentScreen || !strings.Contains(m.View(), "frame") || !strings.Contains(m.View(), "upload") {
		t.Fatalf("viewer calls=%d command=%v screen=%v view=%q", viewer.calls, viewerCmd != nil, m.screen, m.View())
	}
	updated, _ = m.Update(viewerCmd())
	m = updated.(Model)
	if strings.Contains(m.View(), "upload") || !strings.Contains(m.View(), "Режим: fake") {
		t.Fatalf("control not consumed or frame lost: %q", m.View())
	}
	updated, openCmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if openCmd == nil || viewer.opens != 1 {
		t.Fatal("explicit external open missing")
	}
	updated, _ = m.Update(openCmd())
	m = updated.(Model)
	updated, cleanupCmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if cleanupCmd == nil || m.screen != ReportsScreen || viewer.clears != 1 || !strings.Contains(m.View(), "cleanup") {
		t.Fatalf("cleanup missing: cmd=%v screen=%v clears=%d", cleanupCmd, m.screen, viewer.clears)
	}
	updated, _ = m.Update(tea.MouseMsg{X: 30, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if updated.(Model).screen != BansScreen {
		t.Fatal("mouse handling was not preserved after attachment screen")
	}
}

func TestAttachmentRendererErrorStaysInTUI(t *testing.T) {
	m := loadedModel(t)
	m.viewer = &viewerFake{err: errors.New("render failed")}
	updated, cmd := m.openAttachment()
	m = updated.(Model)
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if m.screen != AttachmentScreen || m.err == nil || !strings.Contains(m.View(), "Ошибка") {
		t.Fatalf("screen=%v err=%v view=%q", m.screen, m.err, m.View())
	}
}
