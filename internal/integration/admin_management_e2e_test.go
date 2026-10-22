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

	client, err := adminclient.New(managementServer.URL, credential, managementServer.Client(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Session(ctx)
	if err != nil || session.Role != "administrator" {
		t.Fatalf("session=%#v err=%v", session, err)
	}
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
