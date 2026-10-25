package tui

import (
	"context"
	"strings"
	"testing"

	"bord/api/generated"
	"bord/internal/client"

	tea "github.com/charmbracelet/bubbletea"
)

type mentionServiceFake struct {
	*fakeService
	mentions  []client.Mention
	collapsed bool
	dismissed string
}

func (s *mentionServiceFake) OwnsPost(id string) bool { return id == "post_own1" }
func (s *mentionServiceFake) InitialMentions() ([]client.Mention, bool) {
	return append([]client.Mention(nil), s.mentions...), s.collapsed
}
func (s *mentionServiceFake) RememberMention(mention client.Mention) error {
	s.mentions = append(s.mentions, mention)
	return nil
}
func (s *mentionServiceFake) DismissMention(postID string) error {
	s.dismissed = postID
	return nil
}
func (s *mentionServiceFake) SetMentionsCollapsed(collapsed bool) error {
	s.collapsed = collapsed
	return nil
}

func TestMentionPopupCollapsesAndNavigatesToReply(t *testing.T) {
	target := "post_own1"
	text := "reply"
	mention := client.Mention{BoardID: "board_01", ThreadID: "thread_1", PostID: "post_0002", ReplyToID: target}
	service := &mentionServiceFake{
		fakeService: &fakeService{posts: generated.PostPage{Posts: []generated.Post{{Id: target, State: generated.Visible}, {Id: mention.PostID, State: generated.Visible, Body: &text, ReplyToId: &target}}}},
		mentions:    []client.Mention{mention},
	}
	m := NewModel(context.Background(), service).WithVersion("v9.9.9")
	m.width, m.height, m.loading = 100, 24, false
	m.screen, m.focus = CatalogScreen, FocusContent
	view := m.View()
	if !strings.Contains(view, "Упоминания: 1") || !strings.Contains(view, "тред thread_1") || !strings.Contains(view, "v9.9.9") {
		t.Fatalf("popup or version missing: %q", view)
	}

	x, y, _, _, _ := m.mentionGeometry()
	updated, _ := m.Update(tea.MouseMsg{X: x + 2, Y: y + 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if !m.mentionsCollapsed || !service.collapsed || strings.Contains(m.View(), "тред thread_1") {
		t.Fatal("mention popup did not collapse")
	}
	x, y, _, _, _ = m.mentionGeometry()
	updated, _ = m.Update(tea.MouseMsg{X: x + 2, Y: y + 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	x, y, _, _, _ = m.mentionGeometry()
	updated, cmd := m.Update(tea.MouseMsg{X: x + 3, Y: y + 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if cmd == nil || m.activeThread != mention.ThreadID || m.pendingMentionPost != mention.PostID || service.dismissed != mention.PostID {
		t.Fatalf("navigation not scheduled: thread=%q pending=%q dismissed=%q", m.activeThread, m.pendingMentionPost, service.dismissed)
	}
	updated, _ = m.Update(postsMsg{id: m.requestID, page: service.posts})
	m = updated.(Model)
	if m.screen != PostScreen || selectedPost(m.posts, m.contentIndex) != mention.PostID || m.pendingMentionPost != "" || len(m.mentions) != 0 {
		t.Fatalf("mention destination not opened: screen=%v index=%d pending=%q mentions=%d", m.screen, m.contentIndex, m.pendingMentionPost, len(m.mentions))
	}
}

func TestMentionMessageFiltersAndDeduplicates(t *testing.T) {
	service := &mentionServiceFake{fakeService: &fakeService{}}
	m := NewModel(context.Background(), service)
	mention := client.Mention{ThreadID: "thread_1", PostID: "post_0002", ReplyToID: "post_own1"}
	updated, cmd := m.Update(mentionMsg{mention: mention})
	m = updated.(Model)
	if len(m.mentions) != 1 || cmd == nil {
		t.Fatalf("mention=%+v cmd=%v", m.mentions, cmd)
	}
	updated, _ = m.Update(mentionMsg{mention: mention})
	m = updated.(Model)
	if len(m.mentions) != 1 {
		t.Fatalf("duplicate mention count=%d", len(m.mentions))
	}
}

func TestMentionNavigationLoadsPagesUntilReplyIsFound(t *testing.T) {
	service := &mentionServiceFake{fakeService: &fakeService{}}
	mention := client.Mention{ThreadID: "thread_1", PostID: "post_0200", ReplyToID: "post_own1"}
	m := NewModel(context.Background(), service)
	m.width, m.height, m.loading = 80, 20, false
	m.mentions = []client.Mention{mention}
	updated, _, _ := m.openMention(0)
	m = updated.(Model)
	cursor := "next"
	updated, cmd := m.Update(postsMsg{id: m.requestID, page: generated.PostPage{Posts: []generated.Post{{Id: "post_0001", State: generated.Visible}}, NextCursor: &cursor}})
	m = updated.(Model)
	if cmd == nil || m.pendingMentionPost != mention.PostID || m.screen != ThreadScreen {
		t.Fatalf("next page not scheduled: pending=%q screen=%v", m.pendingMentionPost, m.screen)
	}
	updated, _ = m.Update(postsMsg{id: m.requestID, append: true, page: generated.PostPage{Posts: []generated.Post{{Id: mention.PostID, State: generated.Visible}}}})
	m = updated.(Model)
	if m.screen != PostScreen || selectedPost(m.posts, m.contentIndex) != mention.PostID {
		t.Fatalf("mention not opened after pagination: screen=%v selected=%q", m.screen, selectedPost(m.posts, m.contentIndex))
	}
}

func TestMentionStreamErrorsBackOffBeforeRetry(t *testing.T) {
	m := NewModel(context.Background(), &mentionServiceFake{fakeService: &fakeService{}})
	updated, cmd := m.Update(mentionMsg{err: context.DeadlineExceeded})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("mention retry was not scheduled")
	}
}
