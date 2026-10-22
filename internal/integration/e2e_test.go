package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"bord/internal/api"
	boardapp "bord/internal/app/board"
	"bord/internal/domain"
	"bord/internal/events"
	"bord/internal/media"
	"bord/internal/security"
	storage "bord/internal/storage/sqlite"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func TestArchivedBoardIsHiddenAcrossPublicHTTPReads(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(t.TempDir() + "/bord.db")
	if err != nil {
		t.Fatal(err)
	}
	if err = storage.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	clock := &testClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	runtime, err := New(Config{DB: db, Blobs: t.TempDir(), LookupKey: bytes.Repeat([]byte{1}, 32), Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if err = runtime.SeedBoard(ctx, "general", "General"); err != nil {
		t.Fatal(err)
	}
	created, err := runtime.Board.CreateThread(ctx, boardapp.CreateThreadInput{BoardSlug: "general", Body: "opening", AuthorID: "author", DeleteVerifier: []byte("verifier"), IdempotencyKey: "archived-http"})
	if err != nil {
		t.Fatal(err)
	}
	attachmentID := "0123456789abcdef0123456789abcdef"
	if _, err = runtime.DB.Exec(`INSERT INTO attachments(id,state,media_type,size_bytes,sha256,width,height,safe_filename,created_at,attached_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, attachmentID, "attached", "image/png", 1, make([]byte, 32), 1, 1, "image.png", encodeTime(clock.Now()), encodeTime(clock.Now())); err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.DB.Exec(`UPDATE posts SET attachment_id=? WHERE id=?`, attachmentID, created.Post.ID); err != nil {
		t.Fatal(err)
	}
	if err = runtime.Repo.SetBoardEnabled(ctx, created.Thread.BoardID, true, false); err != nil {
		t.Fatal(err)
	}
	handler := api.NewRouter(api.Config{Public: Public{runtime}, Media: Media{Runtime: runtime, Processor: media.Processor{Limits: media.DefaultLimits()}}, Abuse: runtime.Abuse})
	server := httptest.NewServer(handler)
	defer server.Close()

	response := request(t, server, http.MethodGet, "/api/v1/boards", "", nil, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("boards status=%d", response.StatusCode)
	}
	var boardList struct {
		Boards []api.Board `json:"boards"`
	}
	decode(t, response, &boardList)
	if len(boardList.Boards) != 0 {
		t.Fatalf("archived board leaked: %+v", boardList.Boards)
	}
	for _, path := range []string{
		"/api/v1/boards/general/threads",
		"/api/v1/threads/" + string(created.Thread.ID),
		"/api/v1/threads/" + string(created.Thread.ID) + "/posts",
		"/api/v1/attachments/" + attachmentID,
	} {
		response = request(t, server, http.MethodGet, path, "", nil, nil)
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status=%d body=%s", path, response.StatusCode, read(response))
		}
		_ = response.Body.Close()
	}
	response = requestJSON(t, server, http.MethodPost, "/api/v1/posts/"+string(created.Post.ID)+"/reports", map[string]string{"reason": "spam"}, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("archived report status=%d body=%s", response.StatusCode, read(response))
	}
	_ = response.Body.Close()
}

func (c *testClock) Now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *testClock) Set(v time.Time) { c.mu.Lock(); c.now = v; c.mu.Unlock() }

func TestProductionRuntimeE2E(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := storage.Open(t.TempDir() + "/bord.db")
	if err != nil {
		t.Fatal(err)
	}
	if err = storage.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	clock := &testClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	runtime, err := New(Config{DB: db, Blobs: t.TempDir(), LookupKey: bytes.Repeat([]byte{1}, 32), EncryptionKey: bytes.Repeat([]byte{2}, 32), StoreRawIP: true, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	runtime.UploadSlots <- struct{}{}
	runtime.UploadSlots <- struct{}{}
	cancelled, cancelUpload := context.WithCancel(ctx)
	cancelUpload()
	_, uploadErr := (Media{Runtime: runtime, Processor: media.Processor{Limits: media.DefaultLimits()}}).Upload(cancelled, "image/png", bytes.NewReader(nil))
	<-runtime.UploadSlots
	<-runtime.UploadSlots
	if !errors.Is(uploadErr, context.Canceled) {
		t.Fatalf("bounded upload cancellation=%v", uploadErr)
	}
	if err = runtime.SeedBoard(ctx, "general", "General"); err != nil {
		t.Fatal(err)
	}
	credential, err := runtime.Admin.Bootstrap(ctx, "root", security.RoleAdministrator, clock.Now().Add(365*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub(64, 8)
	t.Cleanup(func() { _ = hub.Close() })
	dispatch := &events.Dispatcher{Outbox: runtime.Outbox, Hub: hub, Interval: time.Millisecond, Batch: 20}
	go func() { _ = dispatch.Run(ctx) }()
	cursors, _ := api.NewCursorCodec(bytes.Repeat([]byte{3}, 32), clock.Now)
	handler := api.NewRouter(api.Config{Public: Public{runtime}, Media: Media{Runtime: runtime, Processor: media.Processor{Limits: media.DefaultLimits()}}, Abuse: runtime.Abuse, Admin: runtime.Admin, Events: hub, CursorCodec: cursors, HeartbeatPeriod: time.Hour})
	server := httptest.NewServer(handler)
	defer server.Close()

	imageBody := pngFixture(t)
	response := request(t, server, http.MethodPost, "/api/v1/attachments", "image/png", imageBody, nil)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("upload status=%d body=%s", response.StatusCode, read(response))
	}
	var uploaded api.Attachment
	decode(t, response, &uploaded)
	prepared, err := (Media{Runtime: runtime, Processor: media.Processor{Limits: media.DefaultLimits()}}).Upload(ctx, "image/png", bytes.NewReader(imageBody))
	if err != nil {
		t.Fatal(err)
	}
	deleteCtx, cancelPreparedDelete := context.WithTimeout(ctx, time.Second)
	defer cancelPreparedDelete()
	if err = runtime.Attachments.Delete(deleteCtx, domain.AttachmentID(prepared.ID)); err != nil {
		t.Fatalf("transactional prepared attachment delete: %v", err)
	}
	deleteToken := strings.Repeat("d", 32)
	verifierEntropy := bytes.NewReader(bytes.Repeat([]byte("v"), int(security.DefaultArgon2Params.SaltLength)))
	verifier, err := security.HashSecret([]byte(deleteToken), security.DefaultArgon2Params, verifierEntropy)
	if err != nil {
		t.Fatal(err)
	}
	create := map[string]any{"body": "opening", "attachment_id": uploaded.ID, "session_proof": strings.Repeat("s", 32), "delete_verifier": base64.RawURLEncoding.EncodeToString([]byte(verifier))}
	response = requestJSON(t, server, http.MethodPost, "/api/v1/boards/general/threads", create, map[string]string{"Idempotency-Key": "thread-key-0000001", "X-Forwarded-For": "198.51.100.77"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", response.StatusCode, read(response))
	}
	var created api.CreateResult
	decode(t, response, &created)
	response = request(t, server, http.MethodGet, "/api/v1/boards/general/threads", "", nil, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("catalog status=%d body=%s", response.StatusCode, read(response))
	}
	var catalog struct {
		Threads []api.Thread `json:"threads"`
	}
	decode(t, response, &catalog)
	if len(catalog.Threads) != 1 || catalog.Threads[0].InitialPost == nil || catalog.Threads[0].InitialPost.Body == nil || *catalog.Threads[0].InitialPost.Body != "opening" {
		t.Fatalf("catalog omitted opening post: %+v", catalog.Threads)
	}
	if catalog.Threads[0].InitialPost.Attachment == nil || catalog.Threads[0].InitialPost.Attachment.ID != uploaded.ID {
		t.Fatalf("catalog omitted opening attachment: %+v", catalog.Threads[0].InitialPost)
	}
	response = request(t, server, http.MethodGet, "/api/v1/threads/"+created.ThreadID, "", nil, nil)
	var thread api.Thread
	decode(t, response, &thread)
	if thread.InitialPost == nil || thread.InitialPost.Attachment == nil || thread.InitialPost.Attachment.ID != uploaded.ID {
		t.Fatalf("thread image not composed: %+v", thread.InitialPost)
	}

	sseCtx, sseCancel := context.WithCancel(ctx)
	defer sseCancel()
	req, _ := http.NewRequestWithContext(sseCtx, http.MethodGet, server.URL+"/api/v1/events?thread="+created.ThreadID, nil)
	stream, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	reply := map[string]any{"body": "reply", "reply_to_id": created.PostID, "session_proof": strings.Repeat("s", 32), "delete_verifier": base64.RawURLEncoding.EncodeToString([]byte(verifier))}
	response = requestJSON(t, server, http.MethodPost, "/api/v1/threads/"+created.ThreadID+"/posts", reply, map[string]string{"Idempotency-Key": "reply-key-00000001"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("reply=%d %s", response.StatusCode, read(response))
	}
	var replied api.CreateResult
	decode(t, response, &replied)
	response = request(t, server, http.MethodGet, "/api/v1/boards", "", nil, nil)
	var boardList struct {
		Boards []api.Board `json:"boards"`
	}
	decode(t, response, &boardList)
	if len(boardList.Boards) != 1 || boardList.Boards[0].ThreadCount != 1 {
		t.Fatalf("board thread count: %+v", boardList.Boards)
	}
	response = request(t, server, http.MethodGet, "/api/v1/boards/general/threads", "", nil, nil)
	decode(t, response, &catalog)
	if len(catalog.Threads) != 1 || catalog.Threads[0].ReplyCount != 1 {
		t.Fatalf("thread reply count: %+v", catalog.Threads)
	}
	// A moderation mutation must not commit if its mandatory audit append fails.
	if _, err = runtime.DB.Exec(`CREATE TRIGGER reject_test_audit BEFORE INSERT ON moderation_actions WHEN NEW.action='post.visibility' BEGIN SELECT RAISE(ABORT, 'test audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	principal, authErr := runtime.Admin.Authenticate(ctx, credential, "atomic-audit-test")
	if authErr != nil {
		err = authErr
		t.Fatal(err)
	}
	ctx = api.WithAdminPrincipal(ctx, principal)
	if err = runtime.Admin.SetPostVisibility(ctx, replied.PostID, false, "atomic audit check", "atomic-audit-test"); err == nil {
		t.Fatal("moderation succeeded despite rejected audit append")
	}
	if _, err = runtime.DB.Exec(`DROP TRIGGER reject_test_audit`); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = runtime.DB.QueryRow(`SELECT status FROM posts WHERE id=?`, replied.PostID).Scan(&state); err != nil || state != "visible" {
		t.Fatalf("unaudited moderation was not rolled back: state=%q err=%v", state, err)
	}
	eventLines := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stream.Body)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), "event: post.created") {
				eventLines <- scanner.Text()
				return
			}
		}
	}()
	select {
	case <-eventLines:
	case <-time.After(3 * time.Second):
		t.Fatal("SSE did not deliver committed reply")
	}

	// Exhaust the per-operation bucket, obtain a bound PoW, and use it once.
	for i := 0; i < 2; i++ {
		h := map[string]string{"Idempotency-Key": "extra-key-0000000" + string(rune('1'+i))}
		response = requestJSON(t, server, http.MethodPost, "/api/v1/threads/"+created.ThreadID+"/posts", map[string]any{"body": "extra", "session_proof": strings.Repeat("s", 32), "delete_verifier": base64.RawURLEncoding.EncodeToString([]byte(verifier))}, h)
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("extra %d=%d %s", i, response.StatusCode, read(response))
		}
	}
	response = requestJSON(t, server, http.MethodPost, "/api/v1/threads/"+created.ThreadID+"/posts", reply, map[string]string{"Idempotency-Key": "limited-key-00001"})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("challenge status=%d %s", response.StatusCode, read(response))
	}
	_ = response.Body.Close()
	response = requestJSON(t, server, http.MethodPost, "/api/v1/pow/challenges", map[string]string{"purpose": "create_post"}, nil)
	var challenge api.Challenge
	decode(t, response, &challenge)
	solution, err := security.Solve(ctx, challenge.Token, uint8(challenge.Difficulty), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	reply["pow_solution"] = challenge.Token + ":" + strconv.FormatUint(solution, 10)
	response = requestJSON(t, server, http.MethodPost, "/api/v1/threads/"+created.ThreadID+"/posts", reply, map[string]string{"Idempotency-Key": "pow-key-000000001"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("pow status=%d %s", response.StatusCode, read(response))
	}
	_ = response.Body.Close()
	response = requestJSON(t, server, http.MethodPost, "/api/v1/threads/"+created.ThreadID+"/posts", reply, map[string]string{"Idempotency-Key": "pow-replay-000001"})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("PoW replay status=%d %s", response.StatusCode, read(response))
	}
	_ = response.Body.Close()

	response = requestJSON(t, server, http.MethodPost, "/api/v1/posts/"+replied.PostID+"/reports", map[string]string{"reason": "spam"}, nil)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("report=%d %s", response.StatusCode, read(response))
	}
	_ = response.Body.Close()
	adminHeaders := map[string]string{"Authorization": "Bearer " + credential}
	reportsCredential, err := runtime.Admin.Bootstrap(ctx, "reviewer", security.RoleReports, clock.Now().Add(365*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, server, http.MethodPut, "/admin/v1/posts/"+replied.PostID+"/visibility", map[string]any{"visible": false, "reason": "not authorized"}, map[string]string{"Authorization": "Bearer " + reportsCredential})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("role status=%d %s", response.StatusCode, read(response))
	}
	_ = response.Body.Close()
	response = requestJSON(t, server, http.MethodPut, "/admin/v1/posts/"+replied.PostID+"/visibility", map[string]any{"visible": false, "reason": "policy violation"}, adminHeaders)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("hide=%d %s", response.StatusCode, read(response))
	}
	_ = response.Body.Close()
	response = requestJSON(t, server, http.MethodPut, "/admin/v1/posts/"+replied.PostID+"/visibility", map[string]any{"visible": true, "reason": "appeal granted"}, adminHeaders)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("restore=%d %s", response.StatusCode, read(response))
	}
	_ = response.Body.Close()
	var moderationEvents int
	if err = runtime.DB.QueryRow(`SELECT count(*) FROM event_outbox WHERE event_type IN ('post.deleted','post.updated') AND post_id=?`, replied.PostID).Scan(&moderationEvents); err != nil || moderationEvents != 2 {
		t.Fatalf("moderation outbox events=%d err=%v", moderationEvents, err)
	}
	response = requestJSON(t, server, http.MethodPost, "/admin/v1/posts/"+created.PostID+"/ip", map[string]string{"reason": "abuse investigation"}, adminHeaders)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("reveal=%d %s", response.StatusCode, read(response))
	}
	var reveal map[string]string
	decode(t, response, &reveal)
	if reveal["ip"] != "127.0.0.1" {
		t.Fatalf("trusted-proxy spoof accepted: reveal=%q", reveal["ip"])
	}

	response = requestJSON(t, server, http.MethodDelete, "/api/v1/posts/"+created.PostID, map[string]string{"delete_token": deleteToken}, nil)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("delete=%d %s", response.StatusCode, read(response))
	}
	_ = response.Body.Close()
	runtime.Abuse.mu.Lock()
	pendingSubjects := len(runtime.Abuse.pending)
	runtime.Abuse.mu.Unlock()
	if pendingSubjects != 0 {
		t.Fatalf("owner delete leaked %d request-scoped abuse subjects", pendingSubjects)
	}
	response = request(t, server, http.MethodGet, "/api/v1/threads/"+created.ThreadID+"/posts", "", nil, nil)
	var posts struct {
		Posts []api.Post `json:"posts"`
	}
	decode(t, response, &posts)
	if len(posts.Posts) == 0 || posts.Posts[0].State != "deleted" || posts.Posts[0].Body != nil || posts.Posts[0].Attachment != nil {
		t.Fatalf("tombstone=%+v", posts.Posts)
	}
	response = request(t, server, http.MethodGet, "/api/v1/attachments/"+uploaded.ID, "", nil, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted attachment status=%d", response.StatusCode)
	}
	_ = response.Body.Close()

	if err = runtime.Admin.audit(ctx, "root", "test.redaction", "", "source=192.0.2.44", "redaction-request", nil); err != nil {
		t.Fatalf("audit redaction failed: %v", err)
	}
	var auditText string
	if err := runtime.DB.QueryRow(`SELECT group_concat(coalesce(reason,'')||payload,' ') FROM moderation_actions`).Scan(&auditText); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(auditText, "127.0.0.1") || strings.Contains(auditText, "198.51.100.77") || strings.Contains(auditText, "192.0.2.44") {
		t.Fatalf("raw IP leaked to audit: %q", auditText)
	}
	if !strings.Contains(auditText, "[redacted: raw IP address]") {
		t.Fatalf("unsafe audit reason was not explicitly redacted: %q", auditText)
	}
	response = requestJSON(t, server, http.MethodPost, "/admin/v1/bans", map[string]any{"subject_type": "ip", "subject": "127.0.0.1", "operations": []string{"create_thread"}, "expires_at": time.Now().Add(time.Hour).UTC(), "reason": "flood source"}, adminHeaders)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("ban=%d %s", response.StatusCode, read(response))
	}
	_ = response.Body.Close()
	response = requestJSON(t, server, http.MethodPost, "/api/v1/boards/general/threads", create, map[string]string{"Idempotency-Key": "banned-key-000001"})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("banned write=%d %s", response.StatusCode, read(response))
	}
	_ = response.Body.Close()
	if _, err = runtime.DB.Exec(`INSERT INTO request_subjects(ip_lookup,ip_prefix,created_at,expires_at) VALUES(?,?,?,?)`, bytes.Repeat([]byte{9}, 32), []byte("203.0.113.0/24"), encodeTime(clock.Now().Add(-90*24*time.Hour)), encodeTime(clock.Now())); err != nil {
		t.Fatal(err)
	}
	clock.Set(clock.Now().Add(90 * 24 * time.Hour))
	retained, err := runtime.Abuse.Retain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if retained.IP != 1 {
		t.Fatalf("90-day retention removed %d subjects, want only unprotected subject", retained.IP)
	}
	var remaining int
	if err = runtime.DB.QueryRow(`SELECT count(*) FROM request_subjects`).Scan(&remaining); err != nil || remaining == 0 {
		t.Fatalf("active-ban retention exception failed: remaining=%d err=%v", remaining, err)
	}
}

func pngFixture(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func requestJSON(t *testing.T, s *httptest.Server, method, path string, v any, headers map[string]string) *http.Response {
	t.Helper()
	b, _ := json.Marshal(v)
	return request(t, s, method, path, "application/json", b, headers)
}
func request(t *testing.T, s *httptest.Server, method, path, contentType string, b []byte, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, s.URL+path, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	response, err := s.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
func decode(t *testing.T, r *http.Response, v any) {
	t.Helper()
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}
func read(r *http.Response) string {
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return string(b)
}
