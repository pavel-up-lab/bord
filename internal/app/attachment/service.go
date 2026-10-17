package attachment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"bord/internal/domain"
)

type State string

const (
	Prepared State = "prepared"
	Attached State = "attached"
	Deleted  State = "deleted"
)

var (
	ErrNotFound        = errors.New("attachment: not found")
	ErrAlreadyAttached = errors.New("attachment: post already has an attachment")
	ErrInvalidState    = errors.New("attachment: invalid state")
)

type Record struct {
	ID        domain.AttachmentID
	PostID    domain.PostID
	BlobKey   string
	MediaType string
	Filename  string
	Size      int64
	Width     int
	Height    int
	SHA256    [32]byte
	State     State
	CreatedAt time.Time
	DeletedAt time.Time
}

type Input struct {
	Data      []byte
	MediaType string
	Filename  string
	Width     int
	Height    int
	SHA256    [32]byte
}

type BlobStore interface {
	Put(context.Context, Input) (key string, err error)
	Open(context.Context, string, string) (io.ReadCloser, int64, error)
	Delete(context.Context, string, string) error
}

type Repository interface {
	CreatePrepared(context.Context, Record) error
	ByID(context.Context, domain.AttachmentID) (Record, error)
	// Attach performs the one-attachment-per-post transition atomically.
	Attach(context.Context, domain.AttachmentID, domain.PostID) (Record, error)
	// PublicByID also verifies visibility of the associated post.
	PublicByID(context.Context, domain.AttachmentID) (Record, error)
	MarkDeleted(context.Context, domain.AttachmentID, time.Time) (Record, error)
	ReferencedBlobKeys(context.Context) (map[string]struct{}, error)
}

type Transactor interface {
	WithinTransaction(context.Context, func(context.Context) error) error
}

type Clock interface{ Now() time.Time }

type Service struct {
	Repository Repository
	Blobs      BlobStore
	Tx         Transactor
	Clock      Clock
	Random     io.Reader
}

func (s Service) Prepare(ctx context.Context, in Input) (Record, error) {
	if err := s.valid(); err != nil {
		return Record{}, err
	}
	if len(in.Data) == 0 || in.SHA256 != sha256.Sum256(in.Data) || in.Width <= 0 || in.Height <= 0 || in.MediaType == "" {
		return Record{}, domain.NewError(domain.CodeValidation, "attachment", ErrInvalidState)
	}
	key, err := s.Blobs.Put(ctx, in)
	if err != nil {
		return Record{}, err
	}
	id, err := opaqueID(s.Random)
	if err != nil {
		return Record{}, fmt.Errorf("attachment id: %w", err)
	}
	record := Record{ID: id, BlobKey: key, MediaType: in.MediaType, Filename: in.Filename, Size: int64(len(in.Data)), Width: in.Width, Height: in.Height, SHA256: in.SHA256, State: Prepared, CreatedAt: s.Clock.Now()}
	if err := s.Repository.CreatePrepared(ctx, record); err != nil {
		// The content-addressed blob may be shared, so never eagerly remove it.
		// Reconciliation removes it after the configured grace period if orphaned.
		return Record{}, err
	}
	return record, nil
}

func (s Service) Attach(ctx context.Context, id domain.AttachmentID, postID domain.PostID) (Record, error) {
	if err := s.valid(); err != nil {
		return Record{}, err
	}
	if !id.Valid() || !postID.Valid() {
		return Record{}, domain.NewError(domain.CodeValidation, "attachment_id", ErrInvalidState)
	}
	var result Record
	err := s.Tx.WithinTransaction(ctx, func(txctx context.Context) error {
		var err error
		result, err = s.Repository.Attach(txctx, id, postID)
		return err
	})
	return result, err
}

type Download struct {
	Record Record
	Body   io.ReadCloser
}

func (s Service) Fetch(ctx context.Context, id domain.AttachmentID) (Download, error) {
	if err := s.valid(); err != nil {
		return Download{}, err
	}
	record, err := s.Repository.PublicByID(ctx, id)
	if err != nil {
		return Download{}, err
	}
	body, size, err := s.Blobs.Open(ctx, record.BlobKey, record.MediaType)
	if err != nil {
		return Download{}, err
	}
	if size != record.Size {
		_ = body.Close()
		return Download{}, errors.New("attachment: blob size mismatch")
	}
	return Download{Record: record, Body: body}, nil
}

func (s Service) Delete(ctx context.Context, id domain.AttachmentID) error {
	if err := s.valid(); err != nil {
		return err
	}
	err := s.Tx.WithinTransaction(ctx, func(txctx context.Context) error {
		var err error
		_, err = s.Repository.MarkDeleted(txctx, id, s.Clock.Now())
		return err
	})
	// Physical deletion is intentionally deferred to reconciliation: multiple
	// opaque attachment IDs may reference the same content-addressed blob.
	return err
}

func (s Service) valid() error {
	if s.Repository == nil || s.Blobs == nil || s.Tx == nil || s.Clock == nil || s.Random == nil {
		return errors.New("attachment: incomplete service dependencies")
	}
	return nil
}

func opaqueID(r io.Reader) (domain.AttachmentID, error) {
	var random [16]byte
	if _, err := io.ReadFull(r, random[:]); err != nil {
		return "", err
	}
	return domain.AttachmentID(hex.EncodeToString(random[:])), nil
}
