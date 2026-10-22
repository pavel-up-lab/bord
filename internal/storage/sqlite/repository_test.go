package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	boardapp "bord/internal/app/board"
	"bord/internal/domain"
	"bord/internal/testkit"
)

var (
	_ boardapp.Clock      = testkit.Clock{}
	_ boardapp.Transactor = testkit.Transactor{}
	_ boardapp.Transactor = (*Repository)(nil)
)

type fixedClock struct{ now time.Time }

func (c *fixedClock) Now() time.Time { return c.now }

type bytesVerifier struct{}

func (bytesVerifier) Verify(token, verifier []byte) (bool, error) {
	return bytes.Equal(token, verifier), nil
}

type fixture struct {
	db      *sql.DB
	repo    *Repository
	service *boardapp.Service
	clock   *fixedClock
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "board.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := Ready(ctx, db); err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(db)
	clock := &fixedClock{now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	board, err := domain.NewBoard("1", "tech", "Technology")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateBoard(ctx, board, clock.now); err != nil {
		t.Fatal(err)
	}
	return fixture{db: db, repo: repo, service: boardapp.NewService(repo, repo, clock, bytesVerifier{}), clock: clock}
}

func createThread(t *testing.T, f fixture, key, body string) boardapp.CreateThreadResult {
	t.Helper()
	result, err := f.service.CreateThread(context.Background(), boardapp.CreateThreadInput{
		BoardSlug: "tech", Body: body, AuthorID: "author-a", DeleteVerifier: []byte("delete-me"), IdempotencyKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestMigrationCreatesFullSchemaWithForeignKeys(t *testing.T) {
	f := newFixture(t)
	for _, table := range []string{"boards", "threads", "posts", "anonymous_sessions", "post_delete_tokens", "idempotency_keys", "attachments", "event_outbox", "reports", "bans", "moderation_actions", "admin_identities", "request_subjects"} {
		var name string
		if err := f.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("table %s: %v", table, err)
		}
	}
	if _, err := f.db.Exec(`INSERT INTO threads(id, board_id, status, created_at, bumped_at) VALUES (99, 999, 'open', 'x', 'x')`); err == nil {
		t.Fatal("foreign key violation accepted")
	}
}

func TestManagedBoardLifecycleRepository(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	second, err := domain.NewBoard("2", "games", "Games")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.CreateBoard(ctx, second, f.clock.now); err != nil {
		t.Fatal(err)
	}
	thread := createThread(t, f, "managed-board-count", "first")
	if _, err := f.service.CreateReply(ctx, boardapp.CreateReplyInput{
		ThreadID: thread.Thread.ID, Body: "reply", AuthorID: "author", DeleteVerifier: []byte("v"), IdempotencyKey: "managed-board-reply",
	}); err != nil {
		t.Fatal(err)
	}

	page, err := f.repo.ListManagedBoards(ctx, boardapp.BoardStateAll, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Boards) != 1 || page.Boards[0].Board.ID != "00000002" || page.Next != "00000002" {
		t.Fatalf("first page = %+v", page)
	}
	page, err = f.repo.ListManagedBoards(ctx, boardapp.BoardStateAll, page.Next, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Boards) != 1 || page.Boards[0].ThreadCount != 1 || page.Boards[0].PostCount != 2 || page.Next.Valid() {
		t.Fatalf("second page = %+v", page)
	}

	if err := f.repo.SetBoardEnabled(ctx, "00000001", true, false); err != nil {
		t.Fatal(err)
	}
	archived, err := f.repo.ListManagedBoards(ctx, boardapp.BoardStateArchived, "", 10)
	if err != nil || len(archived.Boards) != 1 || archived.Boards[0].Board.Enabled {
		t.Fatalf("archived page=%+v err=%v", archived, err)
	}
	if err := f.repo.SetBoardEnabled(ctx, "00000001", true, false); domain.ErrorCode(err) != domain.CodeConflict {
		t.Fatalf("repeat archive code=%q err=%v", domain.ErrorCode(err), err)
	}
	if err := f.repo.SetBoardEnabled(ctx, "00000999", true, false); domain.ErrorCode(err) != domain.CodeBoardNotFound {
		t.Fatalf("missing board code=%q err=%v", domain.ErrorCode(err), err)
	}
	duplicate, _ := domain.NewBoard("3", "games", "Duplicate")
	if err := f.repo.CreateBoard(ctx, duplicate, f.clock.now); domain.ErrorCode(err) != domain.CodeConflict {
		t.Fatalf("duplicate slug code=%q err=%v", domain.ErrorCode(err), err)
	}
	if err := f.repo.SetBoardEnabled(ctx, "00000001", false, true); err != nil {
		t.Fatal(err)
	}
}

func TestActiveParentBoardChecksHideDirectResourceIDs(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	created := createThread(t, f, "parent-checks", "first")
	attachmentID := domain.AttachmentID("0123456789abcdef0123456789abcdef")
	if _, err := f.db.Exec(`INSERT INTO attachments(id,state,media_type,size_bytes,sha256,width,height,safe_filename,created_at,attached_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		attachmentID, "attached", "image/png", 1, make([]byte, 32), 1, 1, "image.png", encodeTime(f.clock.now), encodeTime(f.clock.now)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE posts SET attachment_id=? WHERE id=?`, attachmentID, created.Post.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.RequireActiveBoardForThread(ctx, created.Thread.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.RequireActiveBoardForPost(ctx, created.Post.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.RequireActiveBoardForAttachment(ctx, attachmentID); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.SetBoardEnabled(ctx, "00000001", true, false); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.RequireActiveBoardForThread(ctx, created.Thread.ID); domain.ErrorCode(err) != domain.CodeThreadNotFound {
		t.Fatalf("thread code=%q err=%v", domain.ErrorCode(err), err)
	}
	if err := f.repo.RequireActiveBoardForPost(ctx, created.Post.ID); domain.ErrorCode(err) != domain.CodePostNotFound {
		t.Fatalf("post code=%q err=%v", domain.ErrorCode(err), err)
	}
	if err := f.repo.RequireActiveBoardForAttachment(ctx, attachmentID); domain.ErrorCode(err) != domain.CodeAttachmentNotFound {
		t.Fatalf("attachment code=%q err=%v", domain.ErrorCode(err), err)
	}
}

func TestArchivedBoardIsAbsentFromPublicBoardService(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	created := createThread(t, f, "archive-public", "first")
	if err := f.repo.SetBoardEnabled(ctx, "00000001", true, false); err != nil {
		t.Fatal(err)
	}
	boards, err := f.service.Boards(ctx)
	if err != nil || len(boards) != 0 {
		t.Fatalf("public boards=%+v err=%v", boards, err)
	}
	if _, err := f.service.Catalog(ctx, "tech", nil, 10); domain.ErrorCode(err) != domain.CodeBoardNotFound {
		t.Fatalf("catalog code=%q err=%v", domain.ErrorCode(err), err)
	}
	if _, err := f.service.Posts(ctx, created.Thread.ID, "", 10); domain.ErrorCode(err) != domain.CodeThreadNotFound {
		t.Fatalf("posts code=%q err=%v", domain.ErrorCode(err), err)
	}
	if _, err := f.service.CreateThread(ctx, boardapp.CreateThreadInput{BoardSlug: "tech", Body: "blocked", AuthorID: "author", DeleteVerifier: []byte("v"), IdempotencyKey: "archived-thread"}); domain.ErrorCode(err) != domain.CodeBoardNotFound {
		t.Fatalf("thread create code=%q err=%v", domain.ErrorCode(err), err)
	}
	if _, err := f.service.CreateReply(ctx, boardapp.CreateReplyInput{ThreadID: created.Thread.ID, Body: "blocked", AuthorID: "author", DeleteVerifier: []byte("v"), IdempotencyKey: "archived-reply"}); domain.ErrorCode(err) != domain.CodeThreadNotFound {
		t.Fatalf("reply create code=%q err=%v", domain.ErrorCode(err), err)
	}
	if err := f.service.DeleteOwnPost(ctx, created.Post.ID, []byte("delete-me")); domain.ErrorCode(err) != domain.CodePostNotFound {
		t.Fatalf("delete code=%q err=%v", domain.ErrorCode(err), err)
	}
	var posts, events int
	_ = f.db.QueryRow(`SELECT count(*) FROM posts`).Scan(&posts)
	_ = f.db.QueryRow(`SELECT count(*) FROM event_outbox`).Scan(&events)
	if posts != 1 || events != 1 {
		t.Fatalf("archived writes changed data posts=%d events=%d", posts, events)
	}
	if err := f.repo.SetBoardEnabled(ctx, "00000001", false, true); err != nil {
		t.Fatal(err)
	}
	if page, err := f.service.Posts(ctx, created.Thread.ID, "", 10); err != nil || len(page.Posts) != 1 {
		t.Fatalf("restored posts=%+v err=%v", page, err)
	}
}

func TestCreateReplayConflictAndAtomicOutbox(t *testing.T) {
	f := newFixture(t)
	first := createThread(t, f, "key-1", "hello")
	replay, err := f.service.CreateThread(context.Background(), boardapp.CreateThreadInput{
		BoardSlug: "tech", Body: "hello", AuthorID: "author-a", DeleteVerifier: []byte("delete-me"), IdempotencyKey: "key-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || replay.Thread.ID != first.Thread.ID || replay.Post.ID != first.Post.ID {
		t.Fatalf("bad replay: %+v", replay)
	}
	_, err = f.service.CreateThread(context.Background(), boardapp.CreateThreadInput{
		BoardSlug: "tech", Body: "changed", AuthorID: "author-a", DeleteVerifier: []byte("delete-me"), IdempotencyKey: "key-1",
	})
	if domain.ErrorCode(err) != domain.CodeIdempotencyConflict {
		t.Fatalf("code = %q, err = %v", domain.ErrorCode(err), err)
	}
	var threads, posts, events int
	_ = f.db.QueryRow(`SELECT count(*) FROM threads`).Scan(&threads)
	_ = f.db.QueryRow(`SELECT count(*) FROM posts`).Scan(&posts)
	_ = f.db.QueryRow(`SELECT count(*) FROM event_outbox`).Scan(&events)
	if threads != 1 || posts != 1 || events != 1 {
		t.Fatalf("counts = threads:%d posts:%d events:%d", threads, posts, events)
	}
}

func TestConcurrentIdenticalIdempotencyKeyCreatesOnce(t *testing.T) {
	f := newFixture(t)
	const callers = 8
	var wg sync.WaitGroup
	results := make(chan boardapp.CreateThreadResult, callers)
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := f.service.CreateThread(context.Background(), boardapp.CreateThreadInput{
				BoardSlug: "tech", Body: "same", AuthorID: "author", DeleteVerifier: []byte("v"), IdempotencyKey: "shared-key",
			})
			results <- result
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var id domain.ThreadID
	for result := range results {
		if id == "" {
			id = result.Thread.ID
		}
		if result.Thread.ID != id {
			t.Fatalf("duplicate thread IDs: %s and %s", id, result.Thread.ID)
		}
	}
	var count int
	if err := f.db.QueryRow(`SELECT count(*) FROM threads`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("thread count=%d err=%v", count, err)
	}
}

func TestEmptyPublicationRollsBack(t *testing.T) {
	f := newFixture(t)
	_, err := f.service.CreateThread(context.Background(), boardapp.CreateThreadInput{
		BoardSlug: "tech", AuthorID: "author", DeleteVerifier: []byte("v"), IdempotencyKey: "empty",
	})
	if domain.ErrorCode(err) != domain.CodeValidation {
		t.Fatalf("code = %q", domain.ErrorCode(err))
	}
	for _, table := range []string{"threads", "posts", "event_outbox", "idempotency_keys", "id_sequences"} {
		var count int
		if err := f.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
}

func TestOutboxFailureRollsBackContent(t *testing.T) {
	f := newFixture(t)
	if _, err := f.db.Exec(`CREATE TRIGGER fail_outbox BEFORE INSERT ON event_outbox BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := f.service.CreateThread(context.Background(), boardapp.CreateThreadInput{
		BoardSlug: "tech", Body: "hello", AuthorID: "author", DeleteVerifier: []byte("v"), IdempotencyKey: "rollback",
	})
	if err == nil {
		t.Fatal("expected injected failure")
	}
	for _, table := range []string{"threads", "posts", "event_outbox"} {
		var count int
		_ = f.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count)
		if count != 0 {
			t.Fatalf("%s was not rolled back", table)
		}
	}
}

func TestReplyValidationAndOwnerDelete(t *testing.T) {
	f := newFixture(t)
	one := createThread(t, f, "thread-one", "first")
	two := createThread(t, f, "thread-two", "second")
	_, err := f.service.CreateReply(context.Background(), boardapp.CreateReplyInput{
		ThreadID: one.Thread.ID, ReplyToID: two.Post.ID, Body: "cross", AuthorID: "author", DeleteVerifier: []byte("reply-token"), IdempotencyKey: "cross",
	})
	if domain.ErrorCode(err) != domain.CodeValidation {
		t.Fatalf("cross-thread code = %q", domain.ErrorCode(err))
	}
	if err := f.service.DeleteOwnPost(context.Background(), one.Post.ID, []byte("wrong")); domain.ErrorCode(err) != domain.CodeUnauthorized {
		t.Fatalf("wrong token code = %q", domain.ErrorCode(err))
	}
	if err := f.service.DeleteOwnPost(context.Background(), one.Post.ID, []byte("delete-me")); err != nil {
		t.Fatal(err)
	}
	post, err := f.repo.PostByID(context.Background(), one.Post.ID)
	if err != nil {
		t.Fatal(err)
	}
	if post.Status != domain.PostDeleted || post.Body != "" || post.AttachmentID.Valid() || post.DeletedAt == nil {
		t.Fatalf("not a safe tombstone: %+v", post)
	}
}

func TestCatalogAndPostPaginationStable(t *testing.T) {
	f := newFixture(t)
	first := createThread(t, f, "one", "one")
	f.clock.now = f.clock.now.Add(time.Second)
	second := createThread(t, f, "two", "two")
	f.clock.now = f.clock.now.Add(time.Second)
	third := createThread(t, f, "three", "three")

	page1, err := f.service.Catalog(context.Background(), "tech", nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1.Threads) != 2 || page1.Threads[0].ID != third.Thread.ID || page1.Threads[1].ID != second.Thread.ID || page1.Next == nil {
		t.Fatalf("page1 = %+v", page1)
	}
	page2, err := f.service.Catalog(context.Background(), "tech", page1.Next, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Threads) != 1 || page2.Threads[0].ID != first.Thread.ID || page2.Next != nil {
		t.Fatalf("page2 = %+v", page2)
	}

	for i := 0; i < 3; i++ {
		f.clock.now = f.clock.now.Add(time.Second)
		if _, err := f.service.CreateReply(context.Background(), boardapp.CreateReplyInput{
			ThreadID: first.Thread.ID, Body: fmt.Sprintf("reply-%d", i), AuthorID: "author", DeleteVerifier: []byte("v"), IdempotencyKey: fmt.Sprintf("reply-%d", i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	posts1, err := f.service.Posts(context.Background(), first.Thread.ID, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts1.Posts) != 2 || !posts1.Next.Valid() || posts1.Posts[0].ID >= posts1.Posts[1].ID {
		t.Fatalf("posts1 = %+v", posts1)
	}
	posts2, err := f.service.Posts(context.Background(), first.Thread.ID, posts1.Next, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts2.Posts) != 2 || posts2.Posts[0].ID == posts1.Posts[1].ID {
		t.Fatalf("posts2 = %+v", posts2)
	}
}

func TestClosedThreadAndUnknownResources(t *testing.T) {
	f := newFixture(t)
	thread := createThread(t, f, "closed", "first")
	if _, err := f.db.Exec(`UPDATE threads SET status = 'closed' WHERE id = ?`, thread.Thread.ID); err != nil {
		t.Fatal(err)
	}
	_, err := f.service.CreateReply(context.Background(), boardapp.CreateReplyInput{
		ThreadID: thread.Thread.ID, Body: "reply", AuthorID: "author", DeleteVerifier: []byte("v"), IdempotencyKey: "closed-reply",
	})
	if domain.ErrorCode(err) != domain.CodeThreadClosed {
		t.Fatalf("code = %q", domain.ErrorCode(err))
	}
	_, err = f.service.Catalog(context.Background(), "missing", nil, 10)
	if domain.ErrorCode(err) != domain.CodeBoardNotFound {
		t.Fatalf("board code = %q", domain.ErrorCode(err))
	}
	_, err = f.service.Posts(context.Background(), "999", "", 10)
	if domain.ErrorCode(err) != domain.CodeThreadNotFound {
		t.Fatalf("thread code = %q", domain.ErrorCode(err))
	}
}

func TestConcurrentWritesAreBounded(t *testing.T) {
	f := newFixture(t)
	thread := createThread(t, f, "root", "root")
	const writers = 24
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := f.service.CreateReply(context.Background(), boardapp.CreateReplyInput{
				ThreadID: thread.Thread.ID, Body: fmt.Sprintf("reply-%d", i), AuthorID: domain.AuthorID(fmt.Sprintf("author-%d", i)),
				DeleteVerifier: []byte("v"), IdempotencyKey: fmt.Sprintf("concurrent-%d", i),
			})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := f.db.QueryRow(`SELECT count(*) FROM posts WHERE thread_id = ?`, thread.Thread.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != writers+1 {
		t.Fatalf("post count = %d", count)
	}
}

func TestReadinessRejectsUnmigratedDatabase(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "empty.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Ready(context.Background(), db); err == nil {
		t.Fatal("unmigrated database reported ready")
	}
}

func TestAppendOnlyModerationAudit(t *testing.T) {
	f := newFixture(t)
	_, err := f.db.Exec(`INSERT INTO moderation_actions(action, outcome, request_id, payload, created_at) VALUES ('login', 'denied', 'r1', '{}', ?)`, encodeTime(f.clock.now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`DELETE FROM moderation_actions`); err == nil {
		t.Fatal("audit deletion accepted")
	}
}

func TestRepositoryTransactionRollbackPreservesCause(t *testing.T) {
	f := newFixture(t)
	want := errors.New("stop")
	err := f.repo.WithinTransaction(context.Background(), func(ctx context.Context) error {
		_, err := f.repo.NextID(ctx, "post")
		if err != nil {
			return err
		}
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v", err)
	}
	var count int
	if err := f.db.QueryRow(`SELECT count(*) FROM id_sequences`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("sequence count=%d err=%v", count, err)
	}
}
