package api

import (
	"context"
	"io"
	"time"

	"bord/internal/domain"
	"bord/internal/security"
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

type AdminBoardSummary struct {
	ID, Slug, Title string
}

type AdminThreadSummary struct {
	ID     string
	Closed bool
}

type AdminPostContext struct {
	Board      AdminBoardSummary
	Thread     AdminThreadSummary
	TargetPost string
	Posts      []Post
	NextCursor string
}

type Ban struct {
	ID        string    `json:"id"`
	ExpiresAt time.Time `json:"expires_at"`
}

type AdminBan struct {
	ID          string     `json:"id"`
	SubjectType string     `json:"subject_type"`
	Subject     string     `json:"subject"`
	Operation   string     `json:"operation"`
	Reason      string     `json:"reason"`
	State       string     `json:"state"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

type AdminAuditEntry struct {
	ID        string    `json:"id"`
	ActorID   string    `json:"actor_id,omitempty"`
	Action    string    `json:"action"`
	TargetID  string    `json:"target_id,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	Outcome   string    `json:"outcome"`
	ErrorCode string    `json:"error_code,omitempty"`
	RequestID string    `json:"request_id"`
	CreatedAt time.Time `json:"created_at"`
}

type BanRequest struct {
	SubjectType string    `json:"subject_type"`
	Subject     string    `json:"subject"`
	Board       *string   `json:"board,omitempty"`
	Operations  []string  `json:"operations"`
	ExpiresAt   time.Time `json:"expires_at"`
	Reason      string    `json:"reason"`
}

type PostBanRequest struct {
	Operations []string  `json:"operations"`
	ExpiresAt  time.Time `json:"expires_at"`
	Reason     string    `json:"reason"`
}

// AdminPrincipal is authorization state for exactly one management request.
// Request IDs remain correlation data and are not used as identity storage.
type AdminPrincipal struct {
	ID        string
	Role      security.Role
	ExpiresAt time.Time
}

// AdminService owns authentication, authorization, moderation and audit.
// requestID is supplied explicitly so every attempt can be correlated in audit.
type AdminService interface {
	Authenticate(context.Context, string, string) (AdminPrincipal, error)
	ListReports(context.Context, string, int) (Page[AdminReport], error)
	GetPostContext(context.Context, string, string, int) (AdminPostContext, error)
	OpenAttachment(context.Context, string) (io.ReadCloser, Attachment, error)
	ListBans(context.Context, string, string, int) (Page[AdminBan], error)
	ListAudit(context.Context, string, int) (Page[AdminAuditEntry], error)
	CreatePostBan(context.Context, string, PostBanRequest, string) (Ban, error)
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
