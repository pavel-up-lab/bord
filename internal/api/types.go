package api

import (
	"context"
	"io"
	"time"

	"bord/internal/domain"
)

type Board struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	ThreadCount int64  `json:"thread_count"`
}

type Attachment struct {
	ID        string `json:"id"`
	MediaType string `json:"media_type"`
	Filename  string `json:"filename,omitempty"`
	Size      int64  `json:"size"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	SHA256    string `json:"sha256"`
}

type Post struct {
	ID         string      `json:"id"`
	ThreadID   string      `json:"thread_id"`
	AuthorID   string      `json:"author_id"`
	State      string      `json:"state"`
	Body       *string     `json:"body,omitempty"`
	ReplyToID  *string     `json:"reply_to_id,omitempty"`
	Attachment *Attachment `json:"attachment,omitempty"`
	CreatedAt  time.Time   `json:"created_at"`
	DeletedAt  *time.Time  `json:"deleted_at,omitempty"`
}

type Thread struct {
	ID          string    `json:"id"`
	BoardID     string    `json:"board_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Closed      bool      `json:"closed"`
	ReplyCount  int64     `json:"reply_count"`
	InitialPost *Post     `json:"initial_post,omitempty"`
}

type CreateContent struct {
	Body           *string `json:"body,omitempty"`
	AttachmentID   *string `json:"attachment_id,omitempty"`
	SessionProof   *string `json:"session_proof,omitempty"`
	DeleteVerifier *string `json:"delete_verifier,omitempty"`
	PoWSolution    *string `json:"pow_solution,omitempty"`
	ReplyToID      *string `json:"reply_to_id,omitempty"`
}

type CreateResult struct {
	ThreadID string `json:"thread_id"`
	PostID   string `json:"post_id"`
}

type ReportRequest struct {
	Reason string  `json:"reason"`
	Detail *string `json:"detail,omitempty"`
}

type Challenge struct {
	Token      string    `json:"token"`
	Difficulty int       `json:"difficulty"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type Page[T any] struct {
	Items      []T
	NextCursor string
}

// PublicService is the HTTP adapter boundary implemented by board use cases.
type PublicService interface {
	ListBoards(context.Context) ([]Board, error)
	ListThreads(context.Context, string, string, int) (Page[Thread], error)
	GetThread(context.Context, string) (Thread, error)
	ListPosts(context.Context, string, string, int) (Page[Post], error)
	CreateThread(context.Context, string, string, CreateContent) (CreateResult, error)
	CreatePost(context.Context, string, string, CreateContent) (CreateResult, error)
	DeletePost(context.Context, string, string) error
	ReportPost(context.Context, string, ReportRequest) error
}

type MediaService interface {
	Upload(context.Context, string, io.Reader) (Attachment, error)
	Open(context.Context, string) (io.ReadCloser, Attachment, error)
}

type AbuseService interface {
	Check(context.Context, string, string) error
	Challenge(context.Context, string) (Challenge, error)
}

type AdminReport struct {
	ID        string    `json:"id"`
	PostID    string    `json:"post_id"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

type Ban struct {
	ID        string    `json:"id"`
	ExpiresAt time.Time `json:"expires_at"`
}

type BanRequest struct {
	SubjectType string    `json:"subject_type"`
	Subject     string    `json:"subject"`
	Board       *string   `json:"board,omitempty"`
	Operations  []string  `json:"operations"`
	ExpiresAt   time.Time `json:"expires_at"`
	Reason      string    `json:"reason"`
}

// AdminService owns authentication, authorization, moderation and audit.
// requestID is supplied explicitly so every attempt can be correlated in audit.
type AdminService interface {
	Authenticate(context.Context, string, string) error
	ListReports(context.Context, string, int) (Page[AdminReport], error)
	SetPostVisibility(context.Context, string, bool, string, string) error
	CloseThread(context.Context, string, string, string) error
	CreateBan(context.Context, BanRequest, string) (Ban, error)
	RevokeBan(context.Context, string, string, string) error
	RevealPostIP(context.Context, string, string, string) (string, error)
}

type ReadinessProbe interface{ Ready(context.Context) error }
type ReadinessProbeFunc func(context.Context) error

func (f ReadinessProbeFunc) Ready(ctx context.Context) error { return f(ctx) }

type Event struct {
	ID       domain.EventID `json:"id"`
	Version  int            `json:"version"`
	Type     string         `json:"type"`
	BoardID  string         `json:"board_id,omitempty"`
	ThreadID string         `json:"thread_id,omitempty"`
	PostID   string         `json:"post_id,omitempty"`
}

type Subscription interface {
	Events() <-chan Event
	Close()
}

type EventStream interface {
	Subscribe(board, thread string, after domain.EventID) (replay []Event, reset bool, live Subscription)
}
