package admintui

import (
	"context"
	"errors"
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
	session         adminclient.Session
	reports         adminclient.Page[adminclient.Report]
	context         adminclient.PostContext
	bans            adminclient.Page[adminclient.BanRecord]
	audit           adminclient.Page[adminclient.AuditEntry]
	err             error
	visibilityCalls int
	lastReason      string
}

func (s *serviceFake) Session(context.Context) (adminclient.Session, error) { return s.session, s.err }
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
		reports: adminclient.Page[adminclient.Report]{Items: []adminclient.Report{{Id: "00000001", PostId: "00000002", Reason: "spam", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}}},
		context: adminclient.PostContext{Board: generated.AdminBoardSummary{Id: "00000001", Slug: "general", Title: "General"}, Thread: generated.AdminThreadSummary{Id: "00000001"}, TargetPostId: "00000002", Posts: []generated.Post{{Id: "00000002", ThreadId: "00000001", AuthorId: "author", State: "hidden", Body: &body, Attachment: &generated.Attachment{Id: "attach_1", MediaType: "image/png", Filename: &filename, Sha256: strings.Repeat("a", 64), Size: 5, Width: 1, Height: 1}, CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}}},
		bans:    adminclient.Page[adminclient.BanRecord]{Items: []adminclient.BanRecord{{Id: "00000009", SubjectType: "lookup", Subject: "fingerprint:abcdef", Operation: "create_post", Reason: "spam", State: "active", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), ExpiresAt: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)}}},
		audit:   adminclient.Page[adminclient.AuditEntry]{Items: []adminclient.AuditEntry{{Id: "00000010", Action: "post.visibility", Outcome: "failure", RequestId: "request-001", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}}},
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
	updated, _ = model.Update(cmd())
	return updated.(Model)
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
	calls int
	meta  terminalui.ImageMetadata
}

func (v *viewerFake) View(context.Context, terminalui.ImageMetadata, []byte, string, int, int) tea.Cmd {
	v.calls++
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
	if viewer.calls != 1 || viewerCmd == nil {
		t.Fatalf("viewer calls=%d command=%v", viewer.calls, viewerCmd != nil)
	}
	updated, _ = m.Update(viewerCmd())
	m = updated.(Model)
	updated, _ = m.Update(tea.MouseMsg{X: 30, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if updated.(Model).screen != BansScreen {
		t.Fatal("mouse handling was not restored after viewer")
	}
}
