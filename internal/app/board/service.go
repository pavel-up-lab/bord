package board

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"bord/internal/domain"
)

type CreateThreadInput struct {
	BoardSlug      string
	Body           string
	AttachmentID   domain.AttachmentID
	AuthorID       domain.AuthorID
	DeleteVerifier []byte
	IdempotencyKey string
}

type CreateThreadResult struct {
	Thread   domain.Thread
	Post     domain.Post
	Replayed bool
}

type CreateReplyInput struct {
	ThreadID       domain.ThreadID
	ReplyToID      domain.PostID
	Body           string
	AttachmentID   domain.AttachmentID
	AuthorID       domain.AuthorID
	DeleteVerifier []byte
	IdempotencyKey string
}

type CreateReplyResult struct {
	Post     domain.Post
	Replayed bool
}

func (s *Service) Boards(ctx context.Context) ([]domain.Board, error) {
	return s.store.ListBoards(ctx)
}

func (s *Service) Catalog(ctx context.Context, slug string, cursor *CatalogCursor, limit int) (CatalogPage, error) {
	if limit < 1 || limit > 100 {
		return CatalogPage{}, domain.NewError(domain.CodeValidation, "limit", nil)
	}
	b, err := s.store.BoardBySlug(ctx, slug)
	if err != nil {
		return CatalogPage{}, err
	}
	if !b.Enabled {
		return CatalogPage{}, domain.NewError(domain.CodeBoardNotFound, "slug", nil)
	}
	return s.store.ListThreads(ctx, b.ID, cursor, limit)
}

func (s *Service) Posts(ctx context.Context, threadID domain.ThreadID, after domain.PostID, limit int) (PostPage, error) {
	if limit < 1 || limit > 200 {
		return PostPage{}, domain.NewError(domain.CodeValidation, "limit", nil)
	}
	if err := s.store.RequireActiveBoardForThread(ctx, threadID); err != nil {
		return PostPage{}, err
	}
	if _, err := s.store.ThreadByID(ctx, threadID); err != nil {
		return PostPage{}, err
	}
	return s.store.ListPosts(ctx, threadID, after, limit)
}

func (s *Service) CreateThread(ctx context.Context, in CreateThreadInput) (CreateThreadResult, error) {
	if len(in.DeleteVerifier) == 0 {
		return CreateThreadResult{}, domain.NewError(domain.CodeValidation, "delete_verifier", nil)
	}
	hash, err := requestHash("create_thread", in.BoardSlug, in.Body, string(in.AttachmentID), string(in.AuthorID))
	if err != nil {
		return CreateThreadResult{}, err
	}
	var result CreateThreadResult
	err = s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		if replayed, err := s.replay(ctx, "create_thread", in.IdempotencyKey, hash, &result); replayed || err != nil {
			result.Replayed = replayed
			return err
		}
		b, err := s.store.BoardBySlug(ctx, in.BoardSlug)
		if err != nil {
			return err
		}
		if !b.Enabled {
			return domain.NewError(domain.CodeBoardNotFound, "board", nil)
		}
		threadID, err := s.nextThreadID(ctx)
		if err != nil {
			return err
		}
		postID, err := s.nextPostID(ctx)
		if err != nil {
			return err
		}
		now := s.clock.Now().UTC()
		thread, err := domain.NewThread(threadID, b.ID, now)
		if err != nil {
			return err
		}
		post, err := domain.NewPost(postID, threadID, "", in.AuthorID, in.Body, in.AttachmentID, now)
		if err != nil {
			return err
		}
		if err := s.store.InsertThread(ctx, thread); err != nil {
			return err
		}
		if err := s.store.InsertPost(ctx, post, in.DeleteVerifier); err != nil {
			return err
		}
		if err := s.store.AppendOutbox(ctx, contentEvent("thread.created", thread, post, now)); err != nil {
			return err
		}
		result = CreateThreadResult{Thread: thread, Post: post}
		return s.remember(ctx, "create_thread", in.IdempotencyKey, hash, result, now)
	})
	return result, err
}

func (s *Service) CreateReply(ctx context.Context, in CreateReplyInput) (CreateReplyResult, error) {
	if len(in.DeleteVerifier) == 0 {
		return CreateReplyResult{}, domain.NewError(domain.CodeValidation, "delete_verifier", nil)
	}
	hash, err := requestHash("create_reply", string(in.ThreadID), string(in.ReplyToID), in.Body, string(in.AttachmentID), string(in.AuthorID))
	if err != nil {
		return CreateReplyResult{}, err
	}
	var result CreateReplyResult
	err = s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		if replayed, err := s.replay(ctx, "create_reply", in.IdempotencyKey, hash, &result); replayed || err != nil {
			result.Replayed = replayed
			return err
		}
		thread, err := s.store.ThreadByID(ctx, in.ThreadID)
		if err != nil {
			return err
		}
		if err := thread.RequireOpen(); err != nil {
			return err
		}
		if err := s.store.RequireActiveBoardForThread(ctx, thread.ID); err != nil {
			return err
		}
		postID, err := s.nextPostID(ctx)
		if err != nil {
			return err
		}
		now := s.clock.Now().UTC()
		post, err := domain.NewPost(postID, thread.ID, in.ReplyToID, in.AuthorID, in.Body, in.AttachmentID, now)
		if err != nil {
			return err
		}
		if in.ReplyToID.Valid() {
			target, err := s.store.PostByID(ctx, in.ReplyToID)
			if err != nil {
				return err
			}
			if err := post.ValidateReplyTarget(target); err != nil {
				return err
			}
		}
		if err := s.store.InsertPost(ctx, post, in.DeleteVerifier); err != nil {
			return err
		}
		if err := s.store.UpdateThreadBump(ctx, thread.ID, now); err != nil {
			return err
		}
		if err := s.store.AppendOutbox(ctx, contentEvent("post.created", thread, post, now)); err != nil {
			return err
		}
		result = CreateReplyResult{Post: post}
		return s.remember(ctx, "create_reply", in.IdempotencyKey, hash, result, now)
	})
	return result, err
}

func (s *Service) DeleteOwnPost(ctx context.Context, postID domain.PostID, deleteToken []byte) error {
	if s.verifier == nil || len(deleteToken) == 0 {
		return domain.NewError(domain.CodeUnauthorized, "delete_token", nil)
	}
	return s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		if err := s.store.RequireActiveBoardForPost(ctx, postID); err != nil {
			return err
		}
		post, err := s.store.PostByID(ctx, postID)
		if err != nil {
			return err
		}
		encoded, err := s.store.DeleteVerifier(ctx, postID)
		if err != nil {
			return err
		}
		ok, err := s.verifier.Verify(deleteToken, encoded)
		if err != nil {
			return err
		}
		if !ok {
			return domain.NewError(domain.CodeUnauthorized, "delete_token", nil)
		}
		tombstone, err := post.Tombstone(s.clock.Now().UTC())
		if err != nil {
			return err
		}
		if err := s.store.TombstonePost(ctx, tombstone); err != nil {
			return err
		}
		thread, err := s.store.ThreadByID(ctx, post.ThreadID)
		if err != nil {
			return err
		}
		return s.store.AppendOutbox(ctx, contentEvent("post.deleted", thread, tombstone, *tombstone.DeletedAt))
	})
}

func (s *Service) replay(ctx context.Context, operation, key string, hash [32]byte, dst any) (bool, error) {
	if key == "" {
		return false, domain.NewError(domain.CodeValidation, "idempotency_key", nil)
	}
	record, found, err := s.store.LoadIdempotency(ctx, operation, key)
	if err != nil || !found {
		return false, err
	}
	if record.RequestHash != hash {
		return false, domain.NewError(domain.CodeIdempotencyConflict, "idempotency_key", nil)
	}
	if err := json.Unmarshal(record.Response, dst); err != nil {
		return false, fmt.Errorf("decode idempotency response: %w", err)
	}
	return true, nil
}

func (s *Service) remember(ctx context.Context, operation, key string, hash [32]byte, result any, now time.Time) error {
	response, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return s.store.StoreIdempotency(ctx, operation, key, IdempotencyRecord{RequestHash: hash, Response: response, CreatedAt: now})
}

func requestHash(parts ...string) ([32]byte, error) {
	b, err := json.Marshal(parts)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(b), nil
}

func (s *Service) nextThreadID(ctx context.Context) (domain.ThreadID, error) {
	id, err := s.store.NextID(ctx, "thread")
	return domain.ThreadID(id), err
}

func (s *Service) nextPostID(ctx context.Context) (domain.PostID, error) {
	id, err := s.store.NextID(ctx, "post")
	return domain.PostID(id), err
}

func contentEvent(kind string, thread domain.Thread, post domain.Post, now time.Time) OutboxEvent {
	payload, _ := json.Marshal(struct {
		BoardID  domain.BoardID  `json:"board_id"`
		ThreadID domain.ThreadID `json:"thread_id"`
		PostID   domain.PostID   `json:"post_id"`
	}{thread.BoardID, thread.ID, post.ID})
	return OutboxEvent{Version: 1, Type: kind, BoardID: thread.BoardID, ThreadID: thread.ID, PostID: post.ID, Payload: payload, CreatedAt: now}
}
