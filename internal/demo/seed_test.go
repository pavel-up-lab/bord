package demo_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"bord/internal/api"
	"bord/internal/demo"
	"bord/internal/integration"
	storage "bord/internal/storage/sqlite"
)

func TestSeedFirstRunAndRepeatHaveNoDuplicates(t *testing.T) {
	db, blobs := openDatabase(t)
	first, err := demo.Seed(context.Background(), demo.Config{DB: db, Blobs: blobs})
	if err != nil {
		t.Fatal(err)
	}
	if first.Replayed || first.Boards != 4 || first.Threads != 12 || first.Posts != 60 {
		t.Fatalf("first result = %+v", first)
	}
	assertCounts(t, db, 4, 12, 60)

	second, err := demo.Seed(context.Background(), demo.Config{DB: db, Blobs: blobs})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed || second != (demo.Result{Boards: 4, Threads: 12, Posts: 60, Replayed: true}) {
		t.Fatalf("repeat result = %+v", second)
	}
	assertCounts(t, db, 4, 12, 60)
}

func TestSeedIsVisibleThroughProductionPublicAPI(t *testing.T) {
	db, blobs := openDatabase(t)
	if _, err := demo.Seed(context.Background(), demo.Config{DB: db, Blobs: blobs}); err != nil {
		t.Fatal(err)
	}
	runtime, err := integration.New(integration.Config{DB: db, Blobs: blobs, LookupKey: bytes.Repeat([]byte{7}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	cursors, err := api.NewCursorCodec(bytes.Repeat([]byte{8}, 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(api.NewPublicRouter(api.Config{Public: integration.Public{Runtime: runtime}, Abuse: runtime.Abuse, CursorCodec: cursors}))
	defer server.Close()

	var boardPage struct {
		Boards []api.Board `json:"boards"`
	}
	getJSON(t, server.URL+"/api/v1/boards", &boardPage)
	boardTitles := make(map[string]string, len(boardPage.Boards))
	for _, board := range boardPage.Boards {
		boardTitles[board.Slug] = board.Title
	}
	if len(boardPage.Boards) != 4 || !strings.Contains(boardTitles["general"], "Общение") || !strings.Contains(boardTitles["tech"], "Технологии") {
		t.Fatalf("boards = %+v", boardPage.Boards)
	}

	var catalog struct {
		Threads    []api.Thread `json:"threads"`
		NextCursor string       `json:"next_cursor"`
	}
	getJSON(t, server.URL+"/api/v1/boards/general/threads?limit=2", &catalog)
	if len(catalog.Threads) != 2 || catalog.NextCursor == "" {
		t.Fatalf("catalog page = %+v", catalog)
	}

	var posts struct {
		Posts      []api.Post `json:"posts"`
		NextCursor string     `json:"next_cursor"`
	}
	getJSON(t, server.URL+"/api/v1/threads/92000001/posts?limit=2", &posts)
	if len(posts.Posts) != 2 || posts.NextCursor == "" || posts.Posts[1].ReplyToID == nil {
		t.Fatalf("post page = %+v", posts)
	}
	getJSON(t, server.URL+"/api/v1/threads/92000002/posts?limit=100", &posts)
	if len(posts.Posts) != 5 || posts.Posts[2].State != "deleted" || posts.Posts[2].Body != nil {
		t.Fatalf("tombstone page = %+v", posts.Posts)
	}
	var closed api.Thread
	getJSON(t, server.URL+"/api/v1/threads/92000012", &closed)
	if !closed.Closed {
		t.Fatalf("closed thread = %+v", closed)
	}
}

func TestResetOnlyRecreatesRecognizedFixture(t *testing.T) {
	db, blobs := openDatabase(t)
	if _, err := demo.Seed(context.Background(), demo.Config{DB: db, Blobs: blobs}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE posts SET body='changed' WHERE id=93000001`); err != nil {
		t.Fatal(err)
	}
	if _, err := demo.Seed(context.Background(), demo.Config{DB: db, Blobs: blobs, Reset: true}); err == nil {
		t.Fatal("reset accepted an unrecognized/conflicting fixture")
	}
	var body string
	if err := db.QueryRow(`SELECT body FROM posts WHERE id=93000001`).Scan(&body); err != nil || body != "changed" {
		t.Fatalf("failed reset was not atomic: body=%q err=%v", body, err)
	}
}

func TestResetRecreatesUnchangedFixtureWithoutDuplicates(t *testing.T) {
	db, blobs := openDatabase(t)
	if _, err := demo.Seed(context.Background(), demo.Config{DB: db, Blobs: blobs}); err != nil {
		t.Fatal(err)
	}
	result, err := demo.Seed(context.Background(), demo.Config{DB: db, Blobs: blobs, Reset: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Replayed {
		t.Fatalf("reset result = %+v", result)
	}
	assertCounts(t, db, 4, 12, 60)
}

func TestReplayRejectsChangedOwnedRows(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate string
	}{
		{"delete token", `DELETE FROM post_delete_tokens WHERE post_id=93000001`},
		{"extra outbox event", `INSERT INTO event_outbox(schema_version,event_type,board_id,thread_id,post_id,payload,created_at) VALUES(1,'post.updated',91000001,92000001,93000001,'{}','2026-01-15T09:00:00.000000000Z')`},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, blobs := openDatabase(t)
			if _, err := demo.Seed(context.Background(), demo.Config{DB: db, Blobs: blobs}); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(test.mutate); err != nil {
				t.Fatal(err)
			}
			if _, err := demo.Seed(context.Background(), demo.Config{DB: db, Blobs: blobs}); err == nil {
				t.Fatal("replay accepted a changed owned row")
			}
			assertCounts(t, db, 4, 12, 60)
		})
	}
}

func TestResetPreservesRequestSubjectAndRefusesReports(t *testing.T) {
	db, blobs := openDatabase(t)
	if _, err := demo.Seed(context.Background(), demo.Config{DB: db, Blobs: blobs}); err != nil {
		t.Fatal(err)
	}
	result, err := db.Exec(`INSERT INTO request_subjects(ip_lookup,created_at,expires_at,post_id) VALUES(?,?,?,?)`, []byte("synthetic-lookup"), "2026-01-16T00:00:00.000000000Z", "2026-01-17T00:00:00.000000000Z", 93000001)
	if err != nil {
		t.Fatal(err)
	}
	subjectID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := demo.Seed(context.Background(), demo.Config{DB: db, Blobs: blobs, Reset: true}); err != nil {
		t.Fatal(err)
	}
	var linked sql.NullInt64
	if err := db.QueryRow(`SELECT post_id FROM request_subjects WHERE id=?`, subjectID).Scan(&linked); err != nil || linked.Valid {
		t.Fatalf("request subject was not preserved and detached: post_id=%v err=%v", linked, err)
	}

	if _, err := db.Exec(`UPDATE request_subjects SET post_id=93000001 WHERE id=?`, subjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO reports(post_id,subject_id,reason,created_at) VALUES(?,?,?,?)`, 93000001, subjectID, "spam", "2026-01-16T01:00:00.000000000Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := demo.Seed(context.Background(), demo.Config{DB: db, Blobs: blobs, Reset: true}); err == nil {
		t.Fatal("reset deleted a report instead of refusing external dependent data")
	}
	var reports int
	if err := db.QueryRow(`SELECT count(*) FROM reports WHERE post_id=93000001 AND subject_id=?`, subjectID).Scan(&reports); err != nil || reports != 1 {
		t.Fatalf("report count=%d err=%v", reports, err)
	}
	assertCounts(t, db, 4, 12, 60)
}

func openDatabase(t *testing.T) (*sql.DB, string) {
	t.Helper()
	root := t.TempDir()
	db, err := storage.Open(filepath.Join(root, "bord.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Migrate(context.Background(), db); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, filepath.Join(root, "blobs")
}

func assertCounts(t *testing.T, db *sql.DB, boards, threads, posts int) {
	t.Helper()
	for table, want := range map[string]int{"boards": boards, "threads": threads, "posts": posts} {
		var got int
		if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&got); err != nil || got != want {
			t.Fatalf("%s count=%d want=%d err=%v", table, got, want, err)
		}
	}
}

func getJSON(t *testing.T, url string, target any) {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status=%d", url, response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}
