package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bord/api/generated"
)

func TestHTTPClientContractAndErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/boards":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"boards":[{"id":"board_001","slug":"b","title":"Random"}]}`))
		case "/api/v1/boards/b/threads":
			if r.Header.Get("Idempotency-Key") != "retry-key-0000001" {
				t.Errorf("missing idempotency header")
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"thread_id":"thread_1","post_id":"post_001"}`))
		case "/api/v1/posts/post_001/reports":
			w.Header().Set("Retry-After", "4")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"code":"rate_limited","message":"wait","request_id":"request_123","retry_after":4}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	api, err := New(server.URL, server.Client(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	boards, err := api.Boards(context.Background())
	if err != nil || len(boards) != 1 || boards[0].Slug != "b" {
		t.Fatalf("Boards = %#v, %v", boards, err)
	}
	result, err := api.CreateThread(context.Background(), "b", "retry-key-0000001", generated.CreateThread{})
	if err != nil || result.ThreadId != "thread_1" {
		t.Fatalf("CreateThread = %#v, %v", result, err)
	}
	err = api.Report(context.Background(), "post_001", generated.CreateReport{Reason: generated.Spam})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != generated.ErrorCodeRateLimited || apiErr.RetryAfter != 4*time.Second {
		t.Fatalf("error = %#v", err)
	}
	if strings.Contains(err.Error(), "wait") {
		t.Fatal("safe Error leaked server message")
	}
}

func TestHTTPClientCancellationAndTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	api, _ := New(server.URL, server.Client(), 20*time.Millisecond)
	_, err := api.Boards(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = api.Boards(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestAttachmentBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png"))
	}))
	defer server.Close()
	api, _ := New(server.URL, server.Client(), time.Second)
	body, mediaType, err := api.Attachment(context.Background(), "attachment_1")
	if err != nil || string(body) != "png" || mediaType != "image/png" {
		t.Fatalf("%q %q %v", body, mediaType, err)
	}
}

func TestJSONResponseIsStrictlyBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"boards":[]} ` + strings.Repeat(" ", maxResponseBytes)))
	}))
	defer server.Close()
	api, _ := New(server.URL, server.Client(), time.Second)
	if _, err := api.Boards(context.Background()); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized response error = %v", err)
	}
}

func TestJSONResponseRejectsTrailingValue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"boards":[]} {}`))
	}))
	defer server.Close()
	api, _ := New(server.URL, server.Client(), time.Second)
	if _, err := api.Boards(context.Background()); err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("trailing response error = %v", err)
	}
}
