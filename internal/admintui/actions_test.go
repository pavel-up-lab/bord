package admintui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bord/internal/adminclient"
	"bord/internal/terminalui"

	tea "github.com/charmbracelet/bubbletea"
)

func TestReasonModalValidationCancelSuccessAndDenied(t *testing.T) {
	m := loadedModel(t)
	service := m.service.(*serviceFake)
	updated, _ := m.openVisibilityModal(false)
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if service.visibilityCalls != 0 || m.modal == nil || m.modal.err == "" {
		t.Fatalf("invalid reason was submitted: %#v", m.modal)
	}
	m.modal.editor.SetValue("confirmed spam cleanup")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if !m.modal.confirmed || service.visibilityCalls != 0 {
		t.Fatal("first confirmation submitted action")
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if cmd == nil || !m.modal.pending {
		t.Fatal("confirmed action was not started")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if service.visibilityCalls != 0 {
		t.Fatal("pending action submitted twice")
	}
	updated, refresh := m.Update(cmd())
	m = updated.(Model)
	if service.visibilityCalls != 1 || service.lastReason != "confirmed spam cleanup" || m.modal != nil || refresh == nil {
		t.Fatalf("calls=%d reason=%q modal=%#v", service.visibilityCalls, service.lastReason, m.modal)
	}

	updated, _ = m.openVisibilityModal(true)
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.modal != nil {
		t.Fatal("Esc did not cancel modal")
	}

	updated, _ = m.openVisibilityModal(false)
	m = updated.(Model)
	m.modal.editor.SetValue("denied reason")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	service.err = &adminclient.Error{StatusCode: 403, Code: "forbidden", RequestID: "request-denied"}
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if m.modal == nil || m.modal.err == "" || !strings.Contains(m.modal.err, "forbidden") || len(m.context.Posts) == 0 {
		t.Fatalf("denied modal=%#v context=%#v", m.modal, m.context)
	}
}

func TestBanFormTTLAndOperationsMouseThenRefreshesBans(t *testing.T) {
	m := loadedModel(t)
	updated, _ := m.openBanModal()
	m = updated.(Model)
	labels := m.commandLabels()
	clickIndex := func(want int) {
		x := -1
		for cell := 0; cell < m.width; cell++ {
			index, ok := terminalui.CommandIndexAt(labels, m.width, cell)
			if ok && index == want {
				x = cell
				break
			}
		}
		if x < 0 {
			t.Fatalf("command %d has no hit zone", want)
		}
		updated, _ = m.Update(tea.MouseMsg{X: x, Y: m.height - 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
		m = updated.(Model)
	}
	originalTTL := m.modal.ttl
	clickIndex(1)
	if m.modal.ttl == originalTTL {
		t.Fatal("TTL did not change")
	}
	originalOperations := strings.Join(m.modal.operations, ",")
	clickIndex(2)
	if strings.Join(m.modal.operations, ",") == originalOperations {
		t.Fatal("operations did not change")
	}
	m.modal.editor.SetValue("ban repeated abuse")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	updated, refresh := m.Update(cmd())
	m = updated.(Model)
	if m.screen != BansScreen || refresh == nil {
		t.Fatalf("screen=%v refresh=%v", m.screen, refresh != nil)
	}
}

func TestRevealOnlyForAdministratorAndClearsFromView(t *testing.T) {
	m := loadedModel(t)
	m.session.Role = "moderator"
	updated, _ := m.openRevealModal()
	m = updated.(Model)
	if m.modal != nil {
		t.Fatal("moderator received reveal modal")
	}
	m.session.Role = "administrator"
	updated, _ = m.openRevealModal()
	m = updated.(Model)
	m.modal.editor.SetValue("security investigation")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if !strings.Contains(m.View(), "192.0.2.1") || strings.Contains(strings.ToLower(m.View()), "copied") {
		t.Fatalf("reveal view=%q", m.View())
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.revealedIP != "" || strings.Contains(m.View(), "192.0.2.1") {
		t.Fatalf("IP survived close: %q", m.View())
	}
}

func TestThemeDropdownTopButtonsAndPersistence(t *testing.T) {
	directory := t.TempDir()
	skin := filepath.Join(directory, "admin.ini")
	if err := os.WriteFile(skin, []byte("[skin]\ndescription=Admin Skin\n[core]\n_default_=white;black\nselected=black;white\nheader=yellow;black\ndisabled=gray;black\n[menu]\n_default_=black;white\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog := terminalui.DiscoverThemes()
	name, err := catalog.Resolve(skin)
	if err != nil {
		t.Fatal(err)
	}
	m := loadedModel(t).WithThemes(catalog, name)
	if m.ThemeSetting() != skin {
		t.Fatalf("theme setting=%q", m.ThemeSetting())
	}
	updated, _ := m.Update(tea.MouseMsg{X: 30, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if m.screen != BansScreen {
		t.Fatal("top button did not switch screen")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyF2})
	m = updated.(Model)
	if !m.themeMenu {
		t.Fatal("theme menu did not open")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.themeMenu {
		t.Fatal("theme menu did not close")
	}
}

func TestModalDoesNotExposeArbitraryServiceErrors(t *testing.T) {
	m := loadedModel(t)
	service := m.service.(*serviceFake)
	service.err = errors.New("credential top-secret")
	updated, _ := m.openVisibilityModal(false)
	m = updated.(Model)
	m.modal.editor.SetValue("valid reason")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if strings.Contains(m.View(), "top-secret") || strings.Contains(m.View(), "credential") {
		t.Fatalf("unsafe modal=%q", m.View())
	}
}
