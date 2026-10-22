package client

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"bord/api/generated"
)

func TestPublicHTTPAndSSEPathsNeverCarryManagementCredential(t *testing.T) {
	const credential = "management.super-secret"
	var mutex sync.Mutex
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if value := r.Header.Get("Authorization"); value != "" || bytes.Contains(body, []byte(credential)) {
			t.Errorf("credential reached public %s %s auth=%q", r.Method, r.URL.Path, value)
		}
		mutex.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mutex.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/boards":
			fmt.Fprint(w, `{"boards":[]}`)
		case strings.HasSuffix(r.URL.Path, "/threads") && r.Method == http.MethodGet:
			fmt.Fprint(w, `{"threads":[]}`)
		case strings.HasSuffix(r.URL.Path, "/posts") && r.Method == http.MethodGet:
			fmt.Fprint(w, `{"posts":[]}`)
		case r.URL.Path == "/api/v1/threads/00000001":
			fmt.Fprint(w, `{}`)
		case strings.HasSuffix(r.URL.Path, "/threads") && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"thread_id":"00000001","post_id":"00000001"}`)
		case strings.HasSuffix(r.URL.Path, "/posts") && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"thread_id":"00000001","post_id":"00000002"}`)
		case r.Method == http.MethodDelete || strings.HasSuffix(r.URL.Path, "/reports"):
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/v1/attachments" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id":"attach","media_type":"image/png","size":1,"width":1,"height":1,"sha256":"00"}`)
		case strings.HasPrefix(r.URL.Path, "/api/v1/attachments/"):
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("x"))
		case r.URL.Path == "/api/v1/events":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, ": heartbeat\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	api, err := New(server.URL, server.Client(), 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, _ = api.Boards(ctx)
	_, _ = api.Threads(ctx, "general", nil, 10)
	_, _ = api.Thread(ctx, "00000001")
	_, _ = api.Posts(ctx, "00000001", nil, 10)
	body, reply := "text", "reply"
	_, _ = api.CreateThread(ctx, "general", "key-thread", generated.CreateThread{Body: &body})
	_, _ = api.CreatePost(ctx, "00000001", "key-post", generated.CreatePost{Body: &reply})
	_ = api.DeletePost(ctx, "00000001", "anonymous-delete")
	_ = api.Report(ctx, "00000001", generated.CreateReport{Reason: "spam"})
	_, _ = api.UploadAttachment(ctx, "image/png", strings.NewReader("x"))
	_, _, _ = api.Attachment(ctx, "attach")
	streamCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	_ = api.Subscribe(streamCtx, StreamScope{}, StreamCallbacks{}, Backoff{Min: time.Millisecond, Max: time.Millisecond})
	mutex.Lock()
	joined := strings.Join(requests, "\n")
	mutex.Unlock()
	for _, path := range []string{"/boards", "/threads", "/posts", "/reports", "/attachments", "/events"} {
		if !strings.Contains(joined, path) {
			t.Errorf("missing public path %s in %s", path, joined)
		}
	}
}
