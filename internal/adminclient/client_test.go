package adminclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientCoversManagementOperations(t *testing.T) {
	var boardRequests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer admin.secret" {
			t.Errorf("authorization=%q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /admin/v1/session":
			fmt.Fprint(w, `{"id":"root","role":"administrator","expires_at":"2027-01-01T00:00:00Z"}`)
		case "GET /admin/v1/system/storage":
			fmt.Fprint(w, `{"total_bytes":42949672960,"available_bytes":12884901888}`)
		case "GET /admin/v1/reports":
			fmt.Fprint(w, `{"reports":[{"id":"00000001","post_id":"00000002","reason":"spam","created_at":"2026-01-01T00:00:00Z"}],"next_cursor":"cursor-value-0001"}`)
		case "GET /admin/v1/boards":
			if r.URL.Query().Get("state") != "archived" || r.URL.Query().Get("cursor") != "cursor-board-001" || r.URL.Query().Get("limit") != "12" {
				t.Errorf("board query=%q", r.URL.RawQuery)
			}
			fmt.Fprint(w, `{"boards":[{"id":"00000003","slug":"games","title":"Games","state":"archived","thread_count":2,"post_count":5}],"next_cursor":"cursor-board-002"}`)
		case "POST /admin/v1/boards":
			data, _ := io.ReadAll(r.Body)
			boardRequests = append(boardRequests, string(data))
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id":"00000003","slug":"games","title":"Games","state":"active","thread_count":0,"post_count":0}`)
		case "POST /admin/v1/boards/00000003/archive":
			data, _ := io.ReadAll(r.Body)
			boardRequests = append(boardRequests, string(data))
			fmt.Fprint(w, `{"id":"00000003","slug":"games","title":"Games","state":"archived","thread_count":0,"post_count":0}`)
		case "POST /admin/v1/boards/00000003/restore":
			data, _ := io.ReadAll(r.Body)
			boardRequests = append(boardRequests, string(data))
			fmt.Fprint(w, `{"id":"00000003","slug":"games","title":"Games","state":"active","thread_count":0,"post_count":0}`)
		case "GET /admin/v1/posts/00000002/context":
			fmt.Fprint(w, `{"board":{"id":"00000001","slug":"general","title":"General"},"thread":{"id":"00000001","closed":false},"target_post_id":"00000002","posts":[]}`)
		case "GET /admin/v1/attachments/attach_1":
			w.Header().Set("Content-Type", "image/png")
			fmt.Fprint(w, "png")
		case "GET /admin/v1/bans":
			fmt.Fprint(w, `{"bans":[{"id":"00000001","subject_type":"lookup","subject":"fingerprint:abc","operation":"create_post","reason":"spam","created_at":"2026-01-01T00:00:00Z","expires_at":"2027-01-01T00:00:00Z","state":"active"}]}`)
		case "GET /admin/v1/audit":
			fmt.Fprint(w, `{"entries":[{"id":"00000001","action":"post.visibility","outcome":"success","request_id":"request-001","created_at":"2026-01-01T00:00:00Z"}]}`)
		case "PUT /admin/v1/posts/00000002/visibility", "POST /admin/v1/threads/00000001/close":
			w.WriteHeader(http.StatusNoContent)
		case "POST /admin/v1/bans", "POST /admin/v1/posts/00000002/bans":
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id":"00000009","expires_at":"2027-01-01T00:00:00Z"}`)
		case "DELETE /admin/v1/bans/00000009":
			w.WriteHeader(http.StatusNoContent)
		case "POST /admin/v1/posts/00000002/ip":
			fmt.Fprint(w, `{"ip":"192.0.2.1"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := New(server.URL, "admin.secret", server.Client(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := client.Session(ctx)
	if err != nil || session.Id != "root" {
		t.Fatalf("session=%#v err=%v", session, err)
	}
	storage, err := client.Storage(ctx)
	if err != nil || storage.TotalBytes != 40<<30 || storage.AvailableBytes != 12<<30 {
		t.Fatalf("storage=%#v err=%v", storage, err)
	}
	reports, err := client.Reports(ctx, nil, 25)
	if err != nil || len(reports.Items) != 1 || reports.NextCursor == nil {
		t.Fatalf("reports=%#v err=%v", reports, err)
	}
	cursor := "cursor-board-001"
	boards, err := client.Boards(ctx, "archived", &cursor, 12)
	if err != nil || len(boards.Items) != 1 || boards.Items[0].State != "archived" || boards.NextCursor == nil {
		t.Fatalf("boards=%#v err=%v", boards, err)
	}
	createdBoard, err := client.CreateBoard(ctx, CreateBoardRequest{Slug: "games", Title: "Games", Reason: "new category"})
	if err != nil || createdBoard.State != "active" {
		t.Fatalf("created board=%#v err=%v", createdBoard, err)
	}
	archivedBoard, err := client.ArchiveBoard(ctx, "00000003", "retired category")
	if err != nil || archivedBoard.State != "archived" {
		t.Fatalf("archived board=%#v err=%v", archivedBoard, err)
	}
	restoredBoard, err := client.RestoreBoard(ctx, "00000003", "reopened category")
	if err != nil || restoredBoard.State != "active" {
		t.Fatalf("restored board=%#v err=%v", restoredBoard, err)
	}
	for _, want := range []string{`"slug":"games"`, `"reason":"retired category"`, `"reason":"reopened category"`} {
		found := false
		for _, body := range boardRequests {
			found = found || strings.Contains(body, want)
		}
		if !found {
			t.Errorf("missing board payload %s in %#v", want, boardRequests)
		}
	}
	postContext, err := client.PostContext(ctx, "00000002", nil, 25)
	if err != nil || postContext.TargetPostId != "00000002" {
		t.Fatalf("context=%#v err=%v", postContext, err)
	}
	body, mediaType, err := client.Attachment(ctx, "attach_1")
	if err != nil || string(body) != "png" || mediaType != "image/png" {
		t.Fatalf("attachment=%q %q err=%v", body, mediaType, err)
	}
	bans, err := client.Bans(ctx, "active", nil, 25)
	if err != nil || len(bans.Items) != 1 {
		t.Fatalf("bans=%#v err=%v", bans, err)
	}
	audit, err := client.Audit(ctx, nil, 25)
	if err != nil || len(audit.Items) != 1 {
		t.Fatalf("audit=%#v err=%v", audit, err)
	}
	if err := client.SetVisibility(ctx, "00000002", false, "spam cleanup"); err != nil {
		t.Fatal(err)
	}
	if err := client.CloseThread(ctx, "00000001", "flood cleanup"); err != nil {
		t.Fatal(err)
	}
	expires := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := client.CreateBan(ctx, BanRequest{SubjectType: "lookup", Subject: strings.Repeat("a", 64), Operations: []string{"create_post"}, ExpiresAt: expires, Reason: "spam cleanup"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreatePostBan(ctx, "00000002", PostBanRequest{Operations: []string{"create_post"}, ExpiresAt: expires, Reason: "spam cleanup"}); err != nil {
		t.Fatal(err)
	}
	if err := client.RevokeBan(ctx, "00000009", "appeal accepted"); err != nil {
		t.Fatal(err)
	}
	if ip, err := client.RevealIP(ctx, "00000002", "legal escalation"); err != nil || ip != "192.0.2.1" {
		t.Fatalf("ip=%q err=%v", ip, err)
	}
}

func TestClientReturnsSafeErrorsForHTTPFailures(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				fmt.Fprint(w, `{"code":"forbidden","message":"top-secret server detail","request_id":"request-123","retry_after":2}`)
			}))
			defer server.Close()
			client, err := New(server.URL, "admin.secret", server.Client(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Session(context.Background())
			var apiError *Error
			if !errors.As(err, &apiError) || apiError.StatusCode != status || apiError.RequestID != "request-123" {
				t.Fatalf("error=%#v", err)
			}
			if strings.Contains(err.Error(), "top-secret") || strings.Contains(err.Error(), "admin.secret") {
				t.Fatalf("unsafe error=%q", err)
			}
			if status == http.StatusTooManyRequests && apiError.RetryAfter != 2*time.Second {
				t.Fatalf("retry=%v", apiError.RetryAfter)
			}
		})
	}
}

func TestClientRejectsInvalidConfiguration(t *testing.T) {
	for _, test := range []struct{ server, credential string }{{"file:///tmp/socket", "secret"}, {"https://user:secret@example.test", "secret"}, {"https://example.test?token=secret", "secret"}, {"https://example.test", ""}, {"https://example.test", " secret "}} {
		if _, err := New(test.server, test.credential, nil, time.Second); err == nil {
			t.Fatalf("accepted server=%q credential=%q", test.server, test.credential)
		}
	}
}
