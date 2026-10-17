// Package app defines use-case boundaries and contains no adapter dependencies.
package app

import (
	"context"
	"io"
	"time"

	"bord/internal/domain"
)

type Clock interface{ Now() time.Time }
type Random interface{ Read([]byte) (int, error) }

type Tx interface {
	Commit() error
	Rollback() error
}
type Transactor interface {
	WithinTransaction(context.Context, func(context.Context) error) error
}

type Board struct {
	ID          domain.BoardID
	Slug, Title string
}
type Thread struct {
	ID      domain.ThreadID
	BoardID domain.BoardID
}
type Post struct {
	ID       domain.PostID
	ThreadID domain.ThreadID
}

type BoardRepository interface {
	List(context.Context) ([]Board, error)
	BySlug(context.Context, string) (Board, error)
}
type ThreadRepository interface {
	ByID(context.Context, domain.ThreadID) (Thread, error)
	Save(context.Context, Thread) error
}
type PostRepository interface {
	ByID(context.Context, domain.PostID) (Post, error)
	Save(context.Context, Post) error
}
type IdempotencyRepository interface {
	Load(context.Context, string) ([]byte, bool, error)
	Store(context.Context, string, []byte) error
}

type BlobInfo struct {
	ID        domain.AttachmentID
	MediaType string
	Size      int64
	SHA256    [32]byte
}
type BlobStore interface {
	Put(context.Context, io.Reader) (BlobInfo, error)
	Open(context.Context, domain.AttachmentID) (io.ReadCloser, BlobInfo, error)
	Delete(context.Context, domain.AttachmentID) error
}

type Event struct {
	ID       domain.EventID
	Version  int
	Type     string
	BoardID  domain.BoardID
	ThreadID domain.ThreadID
	PostID   domain.PostID
}
type EventPublisher interface {
	Publish(context.Context, Event) error
}
