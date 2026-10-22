package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bord/internal/adminclient"
	"bord/internal/api"
	"bord/internal/security"
	storage "bord/internal/storage/sqlite"
)

func TestSeparateManagementListenerAdminWorkflow(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(t.TempDir() + "/bord.db")
	if err != nil {
		t.Fatal(err)
	}
	if err = storage.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	clock := &testClock{now: time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)}
	runtime, err := New(Config{DB: db, Blobs: t.TempDir(), LookupKey: bytes.Repeat([]byte{1}, 32), EncryptionKey: bytes.Repeat([]byte{2}, 32), StoreRawIP: true, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if err = runtime.SeedBoard(ctx, "general", "General"); err != nil {
		t.Fatal(err)
	}
	credential, err := runtime.Admin.Bootstrap(ctx, "root", security.RoleAdministrator, clock.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	cursors, _ := api.NewCursorCodec(bytes.Repeat([]byte{3}, 32), clock.Now)
	config := api.Config{Public: Public{runtime}, Abuse: runtime.Abuse, Admin: runtime.Admin, CursorCodec: cursors}
	publicServer := httptest.NewServer(api.NewPublicRouter(config))
	defer publicServer.Close()
	managementServer := httptest.NewServer(api.NewManagementRouter(config))
	defer managementServer.Close()

	deleteToken := strings.Repeat("d", 32)
	verifier, err := security.HashSecret([]byte(deleteToken), security.DefaultArgon2Params, bytes.NewReader(bytes.Repeat([]byte("v"), int(security.DefaultArgon2Params.SaltLength))))
	if err != nil {
		t.Fatal(err)
	}
	create := map[string]any{"body": "reported body", "session_proof": strings.Repeat("s", 32), "delete_verifier": base64.RawURLEncoding.EncodeToString([]byte(verifier))}
	response := requestJSON(t, publicServer, http.MethodPost, "/api/v1/boards/general/threads", create, map[string]string{"Idempotency-Key": "admin-flow-thread-1"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create=%d %s", response.StatusCode, read(response))
	}
	var created api.CreateResult
	decode(t, response, &created)
	response = requestJSON(t, publicServer, http.MethodPost, "/api/v1/posts/"+created.PostID+"/reports", map[string]any{"reason": "spam", "detail": "admin workflow"}, nil)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("report=%d %s", response.StatusCode, read(response))
	}
	response = request(t, publicServer, http.MethodGet, "/admin/v1/session", "", nil, map[string]string{"Authorization": "Bearer " + credential})
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("admin route exposed publicly: %d", response.StatusCode)
	}
	response = request(t, publicServer, http.MethodGet, "/admin/v1/boards", "", nil, map[string]string{"Authorization": "Bearer " + credential})
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("admin boards exposed publicly: %d", response.StatusCode)
	}
	_ = response.Body.Close()

	adminHeaders := map[string]string{"Authorization": "Bearer " + credential}
	response = requestJSON(t, managementServer, http.MethodPost, "/admin/v1/boards", map[string]string{"slug": "games", "title": "Games", "reason": "create games section"}, adminHeaders)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("board create=%d %s", response.StatusCode, read(response))
	}
	var managedBoard api.AdminBoard
	decode(t, response, &managedBoard)
	if managedBoard.Slug != "games" || managedBoard.State != "active" {
		t.Fatalf("created board=%+v", managedBoard)
	}
	response = requestJSON(t, publicServer, http.MethodPost, "/api/v1/boards/games/threads", create, map[string]string{"Idempotency-Key": "admin-board-thread-1"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("games thread create=%d %s", response.StatusCode, read(response))
	}
	var gamesCreated api.CreateResult
	decode(t, response, &gamesCreated)
	response = requestJSON(t, managementServer, http.MethodPost, "/admin/v1/boards/"+managedBoard.ID+"/archive", map[string]string{"reason": "archive games section"}, adminHeaders)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("board archive=%d %s", response.StatusCode, read(response))
	}
	decode(t, response, &managedBoard)
	if managedBoard.State != "archived" || managedBoard.ThreadCount != 1 || managedBoard.PostCount != 1 {
		t.Fatalf("archived board=%+v", managedBoard)
	}
	for _, path := range []string{"/api/v1/boards/games/threads", "/api/v1/threads/" + gamesCreated.ThreadID, "/api/v1/threads/" + gamesCreated.ThreadID + "/posts"} {
		response = request(t, publicServer, http.MethodGet, path, "", nil, nil)
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("archived path %s=%d %s", path, response.StatusCode, read(response))
		}
		_ = response.Body.Close()
	}
	response = requestJSON(t, publicServer, http.MethodPost, "/api/v1/threads/"+gamesCreated.ThreadID+"/posts", create, map[string]string{"Idempotency-Key": "admin-board-reply-01"})
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("archived reply=%d %s", response.StatusCode, read(response))
	}
	_ = response.Body.Close()

	client, err := adminclient.New(managementServer.URL, credential, managementServer.Client(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Session(ctx)
	if err != nil || session.Role != "administrator" {
		t.Fatalf("session=%#v err=%v", session, err)
	}
	archivedContext, err := client.PostContext(ctx, gamesCreated.PostID, nil, 10)
	if err != nil || len(archivedContext.Posts) != 1 || archivedContext.Board.Slug != "games" {
		t.Fatalf("archived management context=%#v err=%v", archivedContext, err)
	}
	response = requestJSON(t, managementServer, http.MethodPost, "/admin/v1/boards/"+managedBoard.ID+"/restore", map[string]string{"reason": "restore games section"}, adminHeaders)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("board restore=%d %s", response.StatusCode, read(response))
	}
	_ = response.Body.Close()
	response = request(t, publicServer, http.MethodGet, "/api/v1/threads/"+gamesCreated.ThreadID, "", nil, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("restored thread=%d %s", response.StatusCode, read(response))
	}
	_ = response.Body.Close()
	reports, err := client.Reports(ctx, nil, 10)
	if err != nil || len(reports.Items) != 1 || reports.Items[0].PostId != created.PostID {
		t.Fatalf("reports=%#v err=%v", reports, err)
	}
	postContext, err := client.PostContext(ctx, created.PostID, nil, 10)
	if err != nil || len(postContext.Posts) != 1 || postContext.Posts[0].Body == nil {
		t.Fatalf("context=%#v err=%v", postContext, err)
	}
	if err = client.SetVisibility(ctx, created.PostID, false, "confirmed hide reason"); err != nil {
		t.Fatal(err)
	}
	postContext, err = client.PostContext(ctx, created.PostID, nil, 10)
	if err != nil || postContext.Posts[0].State != "hidden" || postContext.Posts[0].Body == nil || *postContext.Posts[0].Body != "reported body" {
		t.Fatalf("hidden context=%#v err=%v", postContext, err)
	}
	if err = client.SetVisibility(ctx, created.PostID, true, "confirmed restore reason"); err != nil {
		t.Fatal(err)
	}
	ban, err := client.CreatePostBan(ctx, created.PostID, adminclient.PostBanRequest{Operations: []string{"create_post"}, ExpiresAt: time.Now().Add(time.Hour), Reason: "confirmed ban reason"})
	if err != nil {
		t.Fatal(err)
	}
	if err = client.RevokeBan(ctx, ban.Id, "confirmed revoke reason"); err != nil {
		t.Fatal(err)
	}
	audit, err := client.Audit(ctx, nil, 50)
	if err != nil || len(audit.Items) < 4 {
		t.Fatalf("audit=%#v err=%v", audit, err)
	}
	boardAudit := map[string]bool{}
	for _, entry := range audit.Items {
		if entry.Action == "board.create" || entry.Action == "board.archive" || entry.Action == "board.restore" {
			if entry.ActorId == nil || *entry.ActorId != "root" || entry.TargetId == nil || *entry.TargetId != managedBoard.ID || entry.Reason == nil || *entry.Reason == "" || entry.Outcome != "success" || entry.RequestId == "" {
				t.Fatalf("unsafe or incomplete board audit: %#v", entry)
			}
			boardAudit[entry.Action] = true
		}
	}
	for _, action := range []string{"board.create", "board.archive", "board.restore"} {
		if !boardAudit[action] {
			t.Errorf("missing %s audit", action)
		}
	}
	ip, err := client.RevealIP(ctx, created.PostID, "confirmed reveal reason")
	if err != nil || ip == "" {
		t.Fatalf("ip=%q err=%v", ip, err)
	}
	for _, entry := range audit.Items {
		serialized := entry.Action + entry.RequestId
		if strings.Contains(serialized, credential) || strings.Contains(serialized, ip) {
			t.Fatalf("audit leaked sensitive value: %#v", entry)
		}
	}
}
