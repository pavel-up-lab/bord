package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	boardapp "bord/internal/app/board"
	"bord/internal/domain"
	"bord/internal/storage/sqlite/dbgen"
)

type txContextKey struct{}

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

// DB exposes the adapter handle to sibling production adapters. Application
// packages continue to depend only on their narrow ports.
func (r *Repository) DB() *sql.DB { return r.db }

func (r *Repository) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	if _, nested := ctx.Value(txContextKey{}).(*sql.Tx); nested {
		return fn(ctx)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	txCtx := context.WithValue(ctx, txContextKey{}, tx)
	if err := fn(txCtx); err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			return errors.Join(err, rollbackErr)
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func (r *Repository) queries(ctx context.Context) *dbgen.Queries {
	if tx, ok := ctx.Value(txContextKey{}).(*sql.Tx); ok {
		return dbgen.New(tx)
	}
	return dbgen.New(r.db)
}

// ExecContext and QueryRowContext let sibling SQLite adapters participate in
// the transaction carried by ctx instead of accidentally acquiring a second
// database connection. Open deliberately uses one connection, so bypassing
// this boundary from inside WithinTransaction would deadlock.
func (r *Repository) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return qtx(ctx, r.db).ExecContext(ctx, query, args...)
}

func (r *Repository) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if tx, ok := ctx.Value(txContextKey{}).(*sql.Tx); ok {
		return tx.QueryRowContext(ctx, query, args...)
	}
	return r.db.QueryRowContext(ctx, query, args...)
}

func (r *Repository) NextID(ctx context.Context, kind string) (string, error) {
	id, err := r.queries(ctx).NextID(ctx, kind)
	return formatID(id), err
}

func (r *Repository) ListBoards(ctx context.Context) ([]domain.Board, error) {
	rows, err := r.queries(ctx).ListBoards(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]domain.Board, 0, len(rows))
	for _, row := range rows {
		result = append(result, boardFromRow(row))
	}
	return result, nil
}

func (r *Repository) ListManagedBoards(ctx context.Context, state boardapp.BoardState, before domain.BoardID, limit int) (boardapp.ManagedBoardPage, error) {
	if state != boardapp.BoardStateActive && state != boardapp.BoardStateArchived && state != boardapp.BoardStateAll {
		return boardapp.ManagedBoardPage{}, domain.NewError(domain.CodeValidation, "state", nil)
	}
	if limit < 1 || limit > 100 {
		return boardapp.ManagedBoardPage{}, domain.NewError(domain.CodeValidation, "limit", nil)
	}
	var beforeID int64
	var err error
	if before.Valid() {
		beforeID, err = parseID(string(before))
		if err != nil {
			return boardapp.ManagedBoardPage{}, domain.NewError(domain.CodeValidation, "cursor", err)
		}
	}
	rows, err := r.queries(ctx).ListManagedBoards(ctx, dbgen.ListManagedBoardsParams{BeforeID: beforeID, BoardState: string(state), PageSize: int64(limit + 1)})
	if err != nil {
		return boardapp.ManagedBoardPage{}, err
	}
	page := boardapp.ManagedBoardPage{Boards: make([]boardapp.ManagedBoard, 0, min(limit, len(rows)))}
	for index, row := range rows {
		if index == limit {
			page.Next = page.Boards[len(page.Boards)-1].Board.ID
			break
		}
		page.Boards = append(page.Boards, boardapp.ManagedBoard{
			Board:       domain.Board{ID: domain.BoardID(formatID(row.ID)), Slug: row.Slug, Title: row.Title, Enabled: row.Enabled == 1},
			ThreadCount: row.ThreadCount,
			PostCount:   row.PostCount,
		})
	}
	return page, nil
}

func (r *Repository) BoardByID(ctx context.Context, id domain.BoardID) (domain.Board, error) {
	numeric, err := parseID(string(id))
	if err != nil {
		return domain.Board{}, domain.NewError(domain.CodeBoardNotFound, "board_id", err)
	}
	row, err := r.queries(ctx).BoardByID(ctx, numeric)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Board{}, domain.NewError(domain.CodeBoardNotFound, "board_id", err)
	}
	if err != nil {
		return domain.Board{}, err
	}
	return boardFromRow(row), nil
}

func (r *Repository) BoardBySlug(ctx context.Context, slug string) (domain.Board, error) {
	row, err := r.queries(ctx).BoardBySlug(ctx, slug)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Board{}, domain.NewError(domain.CodeBoardNotFound, "slug", err)
	}
	if err != nil {
		return domain.Board{}, err
	}
	return boardFromRow(row), nil
}

func (r *Repository) RequireActiveBoardForThread(ctx context.Context, id domain.ThreadID) error {
	numeric, err := parseID(string(id))
	if err == nil {
		_, err = r.queries(ctx).ActiveBoardForThread(ctx, numeric)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || numeric == 0 {
			return domain.NewError(domain.CodeThreadNotFound, "thread_id", err)
		}
		return err
	}
	return nil
}

func (r *Repository) RequireActiveBoardForPost(ctx context.Context, id domain.PostID) error {
	numeric, err := parseID(string(id))
	if err == nil {
		_, err = r.queries(ctx).ActiveBoardForPost(ctx, numeric)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || numeric == 0 {
			return domain.NewError(domain.CodePostNotFound, "post_id", err)
		}
		return err
	}
	return nil
}

func (r *Repository) RequireActiveBoardForAttachment(ctx context.Context, id domain.AttachmentID) error {
	if !id.Valid() {
		return domain.NewError(domain.CodeAttachmentNotFound, "attachment_id", nil)
	}
	_, err := r.queries(ctx).ActiveBoardForAttachment(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.NewError(domain.CodeAttachmentNotFound, "attachment_id", err)
	}
	return err
}

func (r *Repository) ThreadByID(ctx context.Context, id domain.ThreadID) (domain.Thread, error) {
	n, err := parseID(string(id))
	if err != nil {
		return domain.Thread{}, domain.NewError(domain.CodeThreadNotFound, "thread_id", err)
	}
	row, err := r.queries(ctx).ThreadByID(ctx, n)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Thread{}, domain.NewError(domain.CodeThreadNotFound, "thread_id", err)
	}
	if err != nil {
		return domain.Thread{}, err
	}
	return threadFromRow(row)
}

func (r *Repository) PostByID(ctx context.Context, id domain.PostID) (domain.Post, error) {
	n, err := parseID(string(id))
	if err != nil {
		return domain.Post{}, domain.NewError(domain.CodePostNotFound, "post_id", err)
	}
	row, err := r.queries(ctx).PostByID(ctx, n)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Post{}, domain.NewError(domain.CodePostNotFound, "post_id", err)
	}
	if err != nil {
		return domain.Post{}, err
	}
	return postFromRow(row)
}

func (r *Repository) ListThreads(ctx context.Context, boardID domain.BoardID, cursor *boardapp.CatalogCursor, limit int) (boardapp.CatalogPage, error) {
	board, err := parseID(string(boardID))
	if err != nil {
		return boardapp.CatalogPage{}, err
	}
	params := dbgen.ListThreadsParams{BoardID: board, HasCursor: int64(0), PageSize: int64(limit + 1)}
	if cursor != nil {
		params.HasCursor = int64(1)
		params.CursorBumpedAt = encodeTime(cursor.BumpedAt)
		params.CursorID, err = parseID(string(cursor.ThreadID))
		if err != nil {
			return boardapp.CatalogPage{}, domain.NewError(domain.CodeValidation, "cursor", err)
		}
	}
	rows, err := r.queries(ctx).ListThreads(ctx, params)
	if err != nil {
		return boardapp.CatalogPage{}, err
	}
	page := boardapp.CatalogPage{Threads: make([]domain.Thread, 0, min(limit, len(rows)))}
	for i, row := range rows {
		if i == limit {
			last := page.Threads[len(page.Threads)-1]
			page.Next = &boardapp.CatalogCursor{BumpedAt: last.BumpedAt, ThreadID: last.ID}
			break
		}
		thread, err := threadFromRow(row)
		if err != nil {
			return boardapp.CatalogPage{}, err
		}
		page.Threads = append(page.Threads, thread)
	}
	return page, nil
}

func (r *Repository) ListPosts(ctx context.Context, threadID domain.ThreadID, after domain.PostID, limit int) (boardapp.PostPage, error) {
	thread, err := parseID(string(threadID))
	if err != nil {
		return boardapp.PostPage{}, err
	}
	var afterID int64
	if after.Valid() {
		afterID, err = parseID(string(after))
		if err != nil {
			return boardapp.PostPage{}, domain.NewError(domain.CodeValidation, "cursor", err)
		}
	}
	rows, err := r.queries(ctx).ListPosts(ctx, dbgen.ListPostsParams{ThreadID: thread, AfterID: afterID, PageSize: int64(limit + 1)})
	if err != nil {
		return boardapp.PostPage{}, err
	}
	page := boardapp.PostPage{Posts: make([]domain.Post, 0, min(limit, len(rows)))}
	for i, row := range rows {
		if i == limit {
			page.Next = page.Posts[len(page.Posts)-1].ID
			break
		}
		post, err := postFromRow(row)
		if err != nil {
			return boardapp.PostPage{}, err
		}
		page.Posts = append(page.Posts, post)
	}
	return page, nil
}

func (r *Repository) InsertThread(ctx context.Context, thread domain.Thread) error {
	id, err := parseID(string(thread.ID))
	if err != nil {
		return err
	}
	boardID, err := parseID(string(thread.BoardID))
	if err != nil {
		return err
	}
	return r.queries(ctx).InsertThread(ctx, dbgen.InsertThreadParams{
		ID: id, BoardID: boardID, Status: string(thread.Status),
		CreatedAt: encodeTime(thread.CreatedAt), BumpedAt: encodeTime(thread.BumpedAt),
	})
}

func (r *Repository) InsertPost(ctx context.Context, post domain.Post, verifier []byte) error {
	id, err := parseID(string(post.ID))
	if err != nil {
		return err
	}
	threadID, err := parseID(string(post.ThreadID))
	if err != nil {
		return err
	}
	params := dbgen.InsertPostParams{
		ID: id, ThreadID: threadID, AuthorID: string(post.AuthorID), Body: post.Body,
		Status: string(post.Status), CreatedAt: encodeTime(post.CreatedAt),
	}
	if post.ReplyToID.Valid() {
		n, err := parseID(string(post.ReplyToID))
		if err != nil {
			return err
		}
		params.ReplyToID = sql.NullInt64{Int64: n, Valid: true}
	}
	if post.AttachmentID.Valid() {
		params.AttachmentID = sql.NullString{String: string(post.AttachmentID), Valid: true}
	}
	if post.DeletedAt != nil {
		params.DeletedAt = sql.NullString{String: encodeTime(*post.DeletedAt), Valid: true}
	}
	q := r.queries(ctx)
	if err := q.InsertPost(ctx, params); err != nil {
		return err
	}
	if post.AttachmentID.Valid() {
		result, err := qtx(ctx, r.db).ExecContext(ctx, `UPDATE attachments SET state = 'attached', attached_at = ? WHERE id = ? AND state = 'prepared'`, encodeTime(post.CreatedAt), string(post.AttachmentID))
		if err != nil {
			return err
		}
		if rows, _ := result.RowsAffected(); rows != 1 {
			return domain.NewError(domain.CodeConflict, "attachment_id", nil)
		}
	}
	return q.InsertDeleteVerifier(ctx, dbgen.InsertDeleteVerifierParams{PostID: id, Verifier: verifier, CreatedAt: encodeTime(post.CreatedAt)})
}

func (r *Repository) UpdateThreadBump(ctx context.Context, id domain.ThreadID, at time.Time) error {
	n, err := parseID(string(id))
	if err != nil {
		return err
	}
	rows, err := r.queries(ctx).UpdateThreadBump(ctx, dbgen.UpdateThreadBumpParams{ID: n, BumpedAt: encodeTime(at)})
	if err == nil && rows != 1 {
		return domain.NewError(domain.CodeThreadNotFound, "thread_id", nil)
	}
	return err
}

func (r *Repository) TombstonePost(ctx context.Context, post domain.Post) error {
	id, err := parseID(string(post.ID))
	if err != nil {
		return err
	}
	deletedAt := sql.NullString{}
	if post.DeletedAt != nil {
		deletedAt = sql.NullString{String: encodeTime(*post.DeletedAt), Valid: true}
	}
	if _, err := qtx(ctx, r.db).ExecContext(ctx, `UPDATE attachments SET state = 'deleted', deleted_at = ? WHERE id = (SELECT attachment_id FROM posts WHERE id = ?)`, deletedAt.String, id); err != nil {
		return err
	}
	rows, err := r.queries(ctx).TombstonePost(ctx, dbgen.TombstonePostParams{ID: id, DeletedAt: deletedAt})
	if err == nil && rows != 1 {
		return domain.NewError(domain.CodeConflict, "post", nil)
	}
	return err
}

func qtx(ctx context.Context, db *sql.DB) interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
} {
	if tx, ok := ctx.Value(txContextKey{}).(*sql.Tx); ok {
		return tx
	}
	return db
}

func (r *Repository) DeleteVerifier(ctx context.Context, postID domain.PostID) ([]byte, error) {
	id, err := parseID(string(postID))
	if err != nil {
		return nil, err
	}
	verifier, err := r.queries(ctx).DeleteVerifier(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NewError(domain.CodePostNotFound, "post_id", err)
	}
	return verifier, err
}

func (r *Repository) LoadIdempotency(ctx context.Context, operation, key string) (boardapp.IdempotencyRecord, bool, error) {
	row, err := r.queries(ctx).LoadIdempotency(ctx, dbgen.LoadIdempotencyParams{Operation: operation, Key: key})
	if errors.Is(err, sql.ErrNoRows) {
		return boardapp.IdempotencyRecord{}, false, nil
	}
	if err != nil {
		return boardapp.IdempotencyRecord{}, false, err
	}
	if len(row.RequestHash) != 32 {
		return boardapp.IdempotencyRecord{}, false, errors.New("invalid stored request hash")
	}
	createdAt, err := decodeTime(row.CreatedAt)
	if err != nil {
		return boardapp.IdempotencyRecord{}, false, err
	}
	var hash [32]byte
	copy(hash[:], row.RequestHash)
	return boardapp.IdempotencyRecord{RequestHash: hash, Response: row.Response, CreatedAt: createdAt}, true, nil
}

func (r *Repository) StoreIdempotency(ctx context.Context, operation, key string, record boardapp.IdempotencyRecord) error {
	return r.queries(ctx).StoreIdempotency(ctx, dbgen.StoreIdempotencyParams{
		Operation: operation, Key: key, RequestHash: record.RequestHash[:], Response: record.Response, CreatedAt: encodeTime(record.CreatedAt),
	})
}

func (r *Repository) AppendOutbox(ctx context.Context, event boardapp.OutboxEvent) error {
	params := dbgen.AppendOutboxParams{
		SchemaVersion: int64(event.Version), EventType: event.Type, Payload: event.Payload, CreatedAt: encodeTime(event.CreatedAt),
	}
	var err error
	if event.BoardID.Valid() {
		params.BoardID.Int64, err = parseID(string(event.BoardID))
		params.BoardID.Valid = err == nil
	}
	if err == nil && event.ThreadID.Valid() {
		params.ThreadID.Int64, err = parseID(string(event.ThreadID))
		params.ThreadID.Valid = err == nil
	}
	if err == nil && event.PostID.Valid() {
		params.PostID.Int64, err = parseID(string(event.PostID))
		params.PostID.Valid = err == nil
	}
	if err != nil {
		return err
	}
	return r.queries(ctx).AppendOutbox(ctx, params)
}

// CreateBoard seeds an operator-defined board while preserving domain validation.
func (r *Repository) CreateBoard(ctx context.Context, board domain.Board, createdAt time.Time) error {
	id, err := parseID(string(board.ID))
	if err != nil {
		return err
	}
	enabled := int64(0)
	if board.Enabled {
		enabled = 1
	}
	err = r.queries(ctx).CreateBoard(ctx, dbgen.CreateBoardParams{ID: id, Slug: board.Slug, Title: board.Title, Enabled: enabled, CreatedAt: encodeTime(createdAt)})
	if isConstraintError(err) {
		return domain.NewError(domain.CodeConflict, "slug", err)
	}
	return err
}

// SetBoardEnabled is a compare-state update: enabled is the desired state and
// expected is the state the caller observed. Zero affected rows are reported as
// conflict after existence is confirmed, making lifecycle retries explicit.
func (r *Repository) SetBoardEnabled(ctx context.Context, id domain.BoardID, expected, enabled bool) error {
	numeric, err := parseID(string(id))
	if err != nil {
		return domain.NewError(domain.CodeBoardNotFound, "board_id", err)
	}
	toInt := func(value bool) int64 {
		if value {
			return 1
		}
		return 0
	}
	rows, err := r.queries(ctx).SetBoardEnabled(ctx, dbgen.SetBoardEnabledParams{ID: numeric, ExpectedEnabled: toInt(expected), Enabled: toInt(enabled)})
	if err != nil {
		return err
	}
	if rows == 1 {
		return nil
	}
	if _, lookupErr := r.BoardByID(ctx, id); lookupErr != nil {
		return lookupErr
	}
	return domain.NewError(domain.CodeConflict, "board", nil)
}

func isConstraintError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "constraint")
}

func boardFromRow(row dbgen.Board) domain.Board {
	return domain.Board{ID: domain.BoardID(formatID(row.ID)), Slug: row.Slug, Title: row.Title, Enabled: row.Enabled == 1}
}

func threadFromRow(row dbgen.Thread) (domain.Thread, error) {
	createdAt, err := decodeTime(row.CreatedAt)
	if err != nil {
		return domain.Thread{}, err
	}
	bumpedAt, err := decodeTime(row.BumpedAt)
	if err != nil {
		return domain.Thread{}, err
	}
	return domain.Thread{ID: domain.ThreadID(formatID(row.ID)), BoardID: domain.BoardID(formatID(row.BoardID)), Status: domain.ThreadStatus(row.Status), CreatedAt: createdAt, BumpedAt: bumpedAt}, nil
}

func postFromRow(row dbgen.Post) (domain.Post, error) {
	createdAt, err := decodeTime(row.CreatedAt)
	if err != nil {
		return domain.Post{}, err
	}
	post := domain.Post{
		ID: domain.PostID(formatID(row.ID)), ThreadID: domain.ThreadID(formatID(row.ThreadID)),
		AuthorID: domain.AuthorID(row.AuthorID), Body: row.Body, Status: domain.PostStatus(row.Status), CreatedAt: createdAt,
	}
	if row.ReplyToID.Valid {
		post.ReplyToID = domain.PostID(formatID(row.ReplyToID.Int64))
	}
	if row.AttachmentID.Valid {
		post.AttachmentID = domain.AttachmentID(row.AttachmentID.String)
	}
	if row.DeletedAt.Valid {
		deletedAt, err := decodeTime(row.DeletedAt.String)
		if err != nil {
			return domain.Post{}, err
		}
		post.DeletedAt = &deletedAt
	}
	return post, nil
}

func parseID(id string) (int64, error) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("invalid public id %q", id)
	}
	return n, nil
}

func formatID(id int64) string { return fmt.Sprintf("%08d", id) }

func encodeTime(t time.Time) string              { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }
func decodeTime(value string) (time.Time, error) { return time.Parse(time.RFC3339Nano, value) }

var _ boardapp.Store = (*Repository)(nil)
