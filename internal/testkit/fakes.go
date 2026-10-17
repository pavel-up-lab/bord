// Package testkit contains deterministic contract fakes for adapter and use-case tests.
package testkit

import (
	"context"
	"io"
	"time"

	"bord/internal/app"
	"bord/internal/domain"
)

type Clock struct{ Time time.Time }

func (c Clock) Now() time.Time { return c.Time }

type Random struct{ Bytes []byte }

func (r Random) Read(p []byte) (int, error) { return copy(p, r.Bytes), nil }

type Transactor struct{}

func (Transactor) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type Tx struct{}

func (Tx) Commit() error   { return nil }
func (Tx) Rollback() error { return nil }

type BoardRepository struct{}

func (BoardRepository) List(context.Context) ([]app.Board, error)         { return nil, nil }
func (BoardRepository) BySlug(context.Context, string) (app.Board, error) { return app.Board{}, nil }

type ThreadRepository struct{}

func (ThreadRepository) ByID(context.Context, domain.ThreadID) (app.Thread, error) {
	return app.Thread{}, nil
}
func (ThreadRepository) Save(context.Context, app.Thread) error { return nil }

type PostRepository struct{}

func (PostRepository) ByID(context.Context, domain.PostID) (app.Post, error) { return app.Post{}, nil }
func (PostRepository) Save(context.Context, app.Post) error                  { return nil }

type IdempotencyRepository struct{}

func (IdempotencyRepository) Load(context.Context, string) ([]byte, bool, error) {
	return nil, false, nil
}
func (IdempotencyRepository) Store(context.Context, string, []byte) error { return nil }

type BlobStore struct{}

func (BlobStore) Put(context.Context, io.Reader) (app.BlobInfo, error) { return app.BlobInfo{}, nil }
func (BlobStore) Open(context.Context, domain.AttachmentID) (io.ReadCloser, app.BlobInfo, error) {
	return nil, app.BlobInfo{}, nil
}
func (BlobStore) Delete(context.Context, domain.AttachmentID) error { return nil }

type EventPublisher struct{}

func (EventPublisher) Publish(context.Context, app.Event) error { return nil }

var (
	_ app.Clock                 = Clock{}
	_ app.Random                = Random{}
	_ app.Transactor            = Transactor{}
	_ app.Tx                    = Tx{}
	_ app.BoardRepository       = BoardRepository{}
	_ app.ThreadRepository      = ThreadRepository{}
	_ app.PostRepository        = PostRepository{}
	_ app.IdempotencyRepository = IdempotencyRepository{}
	_ app.BlobStore             = BlobStore{}
	_ app.EventPublisher        = EventPublisher{}
)
