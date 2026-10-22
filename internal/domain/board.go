package domain

import (
	"strings"
	"time"
)

type AuthorID string

func (id AuthorID) Valid() bool { return strings.TrimSpace(string(id)) != "" }

type Board struct {
	ID    BoardID
	Slug  string
	Title string
	// Enabled is the lifecycle source of truth: false means archived. Archived
	// boards retain all related content but are treated as absent by public use
	// cases; private management use cases may still inspect and restore them.
	Enabled bool
}

func NewBoard(id BoardID, slug, title string) (Board, error) {
	slug = strings.TrimSpace(slug)
	title = strings.TrimSpace(title)
	if !id.Valid() || slug == "" || title == "" {
		return Board{}, NewError(CodeValidation, "board", nil)
	}
	return Board{ID: id, Slug: slug, Title: title, Enabled: true}, nil
}

type ThreadStatus string

const (
	ThreadOpen     ThreadStatus = "open"
	ThreadClosed   ThreadStatus = "closed"
	ThreadArchived ThreadStatus = "archived"
)

type Thread struct {
	ID        ThreadID
	BoardID   BoardID
	Status    ThreadStatus
	CreatedAt time.Time
	BumpedAt  time.Time
}

func NewThread(id ThreadID, boardID BoardID, now time.Time) (Thread, error) {
	if !id.Valid() || !boardID.Valid() || now.IsZero() {
		return Thread{}, NewError(CodeValidation, "thread", nil)
	}
	return Thread{ID: id, BoardID: boardID, Status: ThreadOpen, CreatedAt: now, BumpedAt: now}, nil
}

func (t Thread) AcceptsReplies() bool { return t.Status == ThreadOpen }

func (t Thread) RequireOpen() error {
	if !t.AcceptsReplies() {
		return NewError(CodeThreadClosed, "thread_id", nil)
	}
	return nil
}

type PostStatus string

const (
	PostVisible PostStatus = "visible"
	PostDeleted PostStatus = "deleted"
	PostHidden  PostStatus = "hidden"
)

type Post struct {
	ID           PostID
	ThreadID     ThreadID
	ReplyToID    PostID
	AuthorID     AuthorID
	Body         string
	AttachmentID AttachmentID
	Status       PostStatus
	CreatedAt    time.Time
	DeletedAt    *time.Time
}

func NewPost(id PostID, threadID ThreadID, replyToID PostID, authorID AuthorID, body string, attachmentID AttachmentID, now time.Time) (Post, error) {
	body = strings.TrimSpace(body)
	if !id.Valid() || !threadID.Valid() || !authorID.Valid() || now.IsZero() {
		return Post{}, NewError(CodeValidation, "post", nil)
	}
	if body == "" && !attachmentID.Valid() {
		return Post{}, NewError(CodeValidation, "body", nil)
	}
	return Post{
		ID: id, ThreadID: threadID, ReplyToID: replyToID, AuthorID: authorID,
		Body: body, AttachmentID: attachmentID, Status: PostVisible, CreatedAt: now,
	}, nil
}

func (p Post) ValidateReplyTarget(target Post) error {
	if !p.ReplyToID.Valid() {
		return nil
	}
	if target.ID != p.ReplyToID || target.ThreadID != p.ThreadID {
		return NewError(CodeValidation, "reply_to_id", nil)
	}
	return nil
}

func (p Post) Tombstone(now time.Time) (Post, error) {
	if p.Status != PostVisible || now.IsZero() {
		return Post{}, NewError(CodeConflict, "post", nil)
	}
	p.Body = ""
	p.AttachmentID = ""
	p.Status = PostDeleted
	p.DeletedAt = &now
	return p, nil
}
