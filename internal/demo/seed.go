// Package demo installs deterministic data for manual, non-production testing.
package demo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	boardapp "bord/internal/app/board"
	"bord/internal/domain"
	"bord/internal/media"
	storage "bord/internal/storage/sqlite"
)

const (
	boardIDBase  int64 = 91_000_000
	threadIDBase int64 = 92_000_000
	postIDBase   int64 = 93_000_000
)

var baseTime = time.Date(2026, time.January, 15, 9, 0, 0, 0, time.UTC)

// Config names the same database and blob paths used by the production runtime.
type Config struct {
	DB    *sql.DB
	Blobs string
	Reset bool
}

type Result struct {
	Boards   int  `json:"boards"`
	Threads  int  `json:"threads"`
	Posts    int  `json:"posts"`
	Replayed bool `json:"replayed"`
}

type boardFixture struct {
	id    int64
	slug  string
	title string
}

type threadFixture struct {
	id        int64
	boardID   int64
	status    domain.ThreadStatus
	createdAt time.Time
	bumpedAt  time.Time
	posts     []postFixture
}

type postFixture struct {
	id        int64
	replyTo   int64
	author    string
	body      string
	status    domain.PostStatus
	createdAt time.Time
	deletedAt *time.Time
}

var boards = []boardFixture{
	{boardIDBase + 1, "general", "General / Общение"},
	{boardIDBase + 2, "tech", "Technology / Технологии"},
	{boardIDBase + 3, "games", "Games / Игры"},
	{boardIDBase + 4, "meta", "Meta / О борде"},
}

var authors = []string{"demo-alice", "demo-boris", "demo-chloe", "demo-dmitri", "demo-emoji"}

var topics = [][]string{
	{
		"Welcome! Расскажите, из какого города вы читаете Bord 🌍",
		"Share a small win from today — даже маленькая победа считается.",
		"Книжный клуб: что читаем этой осенью? English titles welcome 📚",
	},
	{
		"SQLite WAL in practice: tips, caveats, and benchmarks",
		"Покажите свой домашний сервер: тихий, экономный, надёжный",
		"Unicode test lab: кириллица, 日本語, emoji 🧪 and combining café",
	},
	{
		"Что проходим на выходных? Кооперативные игры без гринда",
		"Retro games that still feel modern — ваши рекомендации",
		"Настольный вечер: правила, которые легко объяснить новичкам 🎲",
	},
	{
		"Правила сообщества: обсуждение формулировок и примеры",
		"Feature ideas / Идеи для развития каталога",
		"Closed demo thread: архив решений по тестовому стенду",
	},
}

var replies = []string{
	"Спасибо за тему! I am following this discussion.",
	"Согласен частично — вот ещё один взгляд на вопрос.",
	"Replying to the previous post: пример ссылки работает → ✓",
	"Итог для тех, кто пролистывает: кратко, спокойно и по делу 🙂",
}

func fixtures() []threadFixture {
	result := make([]threadFixture, 0, len(boards)*len(topics[0]))
	postID := postIDBase
	for boardIndex, board := range boards {
		for topicIndex, topic := range topics[boardIndex] {
			threadIndex := len(result) + 1
			created := baseTime.Add(time.Duration(boardIndex*48+topicIndex*7) * time.Hour)
			thread := threadFixture{
				id:        threadIDBase + int64(threadIndex),
				boardID:   board.id,
				status:    domain.ThreadOpen,
				createdAt: created,
			}
			for replyIndex := 0; replyIndex < 5; replyIndex++ {
				postID++
				post := postFixture{
					id:        postID,
					author:    authors[(threadIndex+replyIndex)%len(authors)],
					body:      topic,
					status:    domain.PostVisible,
					createdAt: created.Add(time.Duration(replyIndex*13) * time.Minute),
				}
				if replyIndex > 0 {
					post.body = replies[replyIndex-1]
					post.replyTo = thread.posts[replyIndex-1].id
				}
				thread.posts = append(thread.posts, post)
			}
			thread.bumpedAt = thread.posts[len(thread.posts)-1].createdAt
			result = append(result, thread)
		}
	}
	// Exercise both public representations supported by the domain.
	result[len(result)-1].status = domain.ThreadClosed
	deleted := result[1].posts[2].createdAt.Add(5 * time.Minute)
	result[1].posts[2].body = ""
	result[1].posts[2].status = domain.PostDeleted
	result[1].posts[2].deletedAt = &deleted
	result[1].posts[2].replyTo = 0
	return result
}

// Seed validates and inserts the fixture atomically. A complete existing fixture
// is a successful no-op; conflicting rows are never overwritten.
func Seed(ctx context.Context, config Config) (Result, error) {
	var result Result
	if config.DB == nil || config.Blobs == "" {
		return result, errors.New("demo seed: database and blob store are required")
	}
	blobs, err := media.NewStore(config.Blobs, media.SyncData)
	if err != nil {
		return result, fmt.Errorf("demo seed: open blob store: %w", err)
	}
	if err := blobs.Close(); err != nil {
		return result, fmt.Errorf("demo seed: close blob store: %w", err)
	}

	fixtureThreads := fixtures()
	result = Result{Boards: len(boards), Threads: len(fixtureThreads)}
	for _, thread := range fixtureThreads {
		result.Posts += len(thread.posts)
	}
	repo := storage.NewRepository(config.DB)
	err = repo.WithinTransaction(ctx, func(txctx context.Context) error {
		present, err := validateExisting(txctx, repo, fixtureThreads)
		if err != nil {
			return err
		}
		if present && !config.Reset {
			result.Replayed = true
			return nil
		}
		if config.Reset {
			if err := removeFixture(txctx, repo); err != nil {
				return err
			}
		} else if present {
			return nil
		}
		return insertFixture(txctx, repo, fixtureThreads)
	})
	return result, err
}

func validateExisting(ctx context.Context, repo *storage.Repository, threads []threadFixture) (bool, error) {
	var found int
	if err := repo.QueryRowContext(ctx, `SELECT count(*) FROM boards WHERE id>? AND id<=?`, boardIDBase, boardIDBase+int64(len(boards))).Scan(&found); err != nil {
		return false, err
	}
	if found == 0 {
		for _, board := range boards {
			var conflicts int
			if err := repo.QueryRowContext(ctx, `SELECT count(*) FROM boards WHERE slug=?`, board.slug).Scan(&conflicts); err != nil {
				return false, err
			}
			if conflicts != 0 {
				return false, fmt.Errorf("demo seed: board slug %q already exists; refusing to overwrite it", board.slug)
			}
		}
		return false, nil
	}
	if found != len(boards) {
		return false, errors.New("demo seed: reserved board ID range is partially populated")
	}
	for _, board := range boards {
		var slug, title string
		var enabled int
		if err := repo.QueryRowContext(ctx, `SELECT slug,title,enabled FROM boards WHERE id=?`, board.id).Scan(&slug, &title, &enabled); err != nil || slug != board.slug || title != board.title || enabled != 1 {
			return false, fmt.Errorf("demo seed: board %d conflicts with deterministic fixture", board.id)
		}
	}
	for _, thread := range threads {
		var boardID int64
		var status, created, bumped string
		if err := repo.QueryRowContext(ctx, `SELECT board_id,status,created_at,bumped_at FROM threads WHERE id=?`, thread.id).Scan(&boardID, &status, &created, &bumped); err != nil || boardID != thread.boardID || status != string(thread.status) || created != encodeTime(thread.createdAt) || bumped != encodeTime(thread.bumpedAt) {
			return false, fmt.Errorf("demo seed: thread %d conflicts with deterministic fixture", thread.id)
		}
		for _, post := range thread.posts {
			var threadID int64
			var reply sql.NullInt64
			var author, body, postStatus, postCreated string
			var deleted sql.NullString
			err := repo.QueryRowContext(ctx, `SELECT thread_id,reply_to_id,author_id,body,status,created_at,deleted_at FROM posts WHERE id=?`, post.id).Scan(&threadID, &reply, &author, &body, &postStatus, &postCreated, &deleted)
			wantDeleted := ""
			if post.deletedAt != nil {
				wantDeleted = encodeTime(*post.deletedAt)
			}
			if err != nil || threadID != thread.id || reply.Int64 != post.replyTo || author != post.author || body != post.body || postStatus != string(post.status) || postCreated != encodeTime(post.createdAt) || deleted.String != wantDeleted {
				return false, fmt.Errorf("demo seed: post %d conflicts with deterministic fixture", post.id)
			}
		}
	}
	return true, nil
}

func insertFixture(ctx context.Context, repo *storage.Repository, threads []threadFixture) error {
	for _, item := range boards {
		board, err := domain.NewBoard(domain.BoardID(formatID(item.id)), item.slug, item.title)
		if err != nil {
			return err
		}
		if err := repo.CreateBoard(ctx, board, baseTime); err != nil {
			return err
		}
	}
	for _, item := range threads {
		thread, err := domain.NewThread(domain.ThreadID(formatID(item.id)), domain.BoardID(formatID(item.boardID)), item.createdAt)
		if err != nil {
			return err
		}
		thread.Status, thread.BumpedAt = item.status, item.bumpedAt
		if err := repo.InsertThread(ctx, thread); err != nil {
			return err
		}
		for postIndex, value := range item.posts {
			post, err := domain.NewPost(domain.PostID(formatID(value.id)), thread.ID, domain.PostID(optionalID(value.replyTo)), domain.AuthorID(value.author), value.body, "", value.createdAt)
			if value.status == domain.PostDeleted {
				// NewPost deliberately rejects empty visible content. Build the
				// persisted tombstone through the same domain transition instead.
				post, err = domain.NewPost(domain.PostID(formatID(value.id)), thread.ID, "", domain.AuthorID(value.author), "demo tombstone", "", value.createdAt)
				if err == nil {
					post, err = post.Tombstone(*value.deletedAt)
				}
			}
			if err != nil {
				return err
			}
			if err := repo.InsertPost(ctx, post, []byte("demo-only-unusable-delete-verifier")); err != nil {
				return err
			}
			eventType := "post.created"
			if postIndex == 0 {
				eventType = "thread.created"
			}
			if err := repo.AppendOutbox(ctx, boardapp.OutboxEvent{Version: 1, Type: eventType, BoardID: thread.BoardID, ThreadID: thread.ID, PostID: post.ID, Payload: []byte(`{"source":"demo-seed"}`), CreatedAt: post.CreatedAt}); err != nil {
				return err
			}
			if post.Status == domain.PostDeleted {
				if err := repo.AppendOutbox(ctx, boardapp.OutboxEvent{Version: 1, Type: "post.deleted", BoardID: thread.BoardID, ThreadID: thread.ID, PostID: post.ID, Payload: []byte(`{"source":"demo-seed"}`), CreatedAt: *post.DeletedAt}); err != nil {
					return err
				}
			}
		}
	}
	for kind, value := range map[string]int64{"board": boardIDBase + int64(len(boards)), "thread": threadIDBase + int64(len(threads)), "post": postIDBase + int64(len(threads)*5)} {
		if _, err := repo.ExecContext(ctx, `INSERT INTO id_sequences(kind,value) VALUES(?,?) ON CONFLICT(kind) DO UPDATE SET value=max(value,excluded.value)`, kind, value); err != nil {
			return err
		}
	}
	return nil
}

func removeFixture(ctx context.Context, repo *storage.Repository) error {
	postMax := postIDBase + int64(len(fixtures())*5)
	// SQLite's RESTRICT action is immediate, so unlink the fixture's internal
	// reply graph before deleting it. Replies outside the reserved range remain
	// linked and intentionally make reset fail rather than deleting user data.
	if _, err := repo.ExecContext(ctx, `UPDATE posts SET reply_to_id=NULL WHERE id>? AND id<=? AND (reply_to_id>? AND reply_to_id<=?)`, postIDBase, postMax, postIDBase, postMax); err != nil {
		return fmt.Errorf("demo seed reset: %w", err)
	}
	for _, query := range []string{
		`DELETE FROM reports WHERE post_id>? AND post_id<=?`,
		`DELETE FROM request_subjects WHERE post_id>? AND post_id<=?`,
		`DELETE FROM event_outbox WHERE post_id>? AND post_id<=?`,
		`DELETE FROM post_delete_tokens WHERE post_id>? AND post_id<=?`,
		`DELETE FROM posts WHERE id>? AND id<=?`,
	} {
		if _, err := repo.ExecContext(ctx, query, postIDBase, postMax); err != nil {
			return fmt.Errorf("demo seed reset: %w", err)
		}
	}
	if _, err := repo.ExecContext(ctx, `DELETE FROM event_outbox WHERE thread_id>? AND thread_id<=?`, threadIDBase, threadIDBase+int64(len(fixtures()))); err != nil {
		return err
	}
	if _, err := repo.ExecContext(ctx, `DELETE FROM threads WHERE id>? AND id<=?`, threadIDBase, threadIDBase+int64(len(fixtures()))); err != nil {
		return fmt.Errorf("demo seed reset: %w", err)
	}
	if _, err := repo.ExecContext(ctx, `DELETE FROM event_outbox WHERE board_id>? AND board_id<=?`, boardIDBase, boardIDBase+int64(len(boards))); err != nil {
		return err
	}
	if _, err := repo.ExecContext(ctx, `DELETE FROM boards WHERE id>? AND id<=?`, boardIDBase, boardIDBase+int64(len(boards))); err != nil {
		return fmt.Errorf("demo seed reset: %w", err)
	}
	return nil
}

func formatID(id int64) string { return fmt.Sprintf("%08d", id) }
func optionalID(id int64) string {
	if id == 0 {
		return ""
	}
	return formatID(id)
}
func encodeTime(value time.Time) string { return value.UTC().Format("2006-01-02T15:04:05.000000000Z") }
