// Package board implements transactional board-content use cases.
package board

import (
	"context"
	"time"

	"bord/internal/domain"
)

// Clock and Transactor are consumer-owned structural views of the frozen
// foundation ports. This package cannot import its parent internal/app package
// under the inward-dependency architecture gate.
type Clock interface{ Now() time.Time }
type Transactor interface {
	WithinTransaction(context.Context, func(context.Context) error) error
}

type CatalogCursor struct {
	BumpedAt time.Time
	ThreadID domain.ThreadID
}

type CatalogPage struct {
	Threads []domain.Thread
	Next    *CatalogCursor
}

type PostPage struct {
	Posts []domain.Post
	Next  domain.PostID
}

type IdempotencyRecord struct {
	RequestHash [32]byte
	Response    []byte
	CreatedAt   time.Time
}

type OutboxEvent struct {
	Version   int
	Type      string
	BoardID   domain.BoardID
	ThreadID  domain.ThreadID
	PostID    domain.PostID
	Payload   []byte
	CreatedAt time.Time
}

// Store is deliberately use-case shaped. Implementations must honor the transaction
// carried by ctx when called from Transactor.WithinTransaction.
type Store interface {
	NextID(context.Context, string) (string, error)
	ListBoards(context.Context) ([]domain.Board, error)
	BoardBySlug(context.Context, string) (domain.Board, error)
	ThreadByID(context.Context, domain.ThreadID) (domain.Thread, error)
	PostByID(context.Context, domain.PostID) (domain.Post, error)
	ListThreads(context.Context, domain.BoardID, *CatalogCursor, int) (CatalogPage, error)
	ListPosts(context.Context, domain.ThreadID, domain.PostID, int) (PostPage, error)
	InsertThread(context.Context, domain.Thread) error
	InsertPost(context.Context, domain.Post, []byte) error
	UpdateThreadBump(context.Context, domain.ThreadID, time.Time) error
	TombstonePost(context.Context, domain.Post) error
	DeleteVerifier(context.Context, domain.PostID) ([]byte, error)
	LoadIdempotency(context.Context, string, string) (IdempotencyRecord, bool, error)
	StoreIdempotency(context.Context, string, string, IdempotencyRecord) error
	AppendOutbox(context.Context, OutboxEvent) error
}

type TokenVerifier interface {
	Verify(deleteToken, encodedVerifier []byte) (bool, error)
}

type Service struct {
	tx       Transactor
	store    Store
	clock    Clock
	verifier TokenVerifier
}

func NewService(tx Transactor, store Store, clock Clock, verifier TokenVerifier) *Service {
	return &Service{tx: tx, store: store, clock: clock, verifier: verifier}
}
