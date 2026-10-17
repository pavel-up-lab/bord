package client

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseSSEFragmentedHeartbeatUnknownAndReset(t *testing.T) {
	stream := ": ping\n\nid: 1\nevent: future.event\ndata: {}\n\nid: 2\nevent: stream.reset\ndata: {\"why\":\"old\"}\n\n"
	var events []Event
	heartbeats := 0
	seen, err := parseSSE(context.Background(), &oneByteReader{s: stream}, func(e Event) { events = append(events, e) }, func() { heartbeats++ })
	if err == nil || !seen || heartbeats != 1 || len(events) != 2 {
		t.Fatalf("seen=%v heartbeat=%d events=%#v err=%v", seen, heartbeats, events, err)
	}
}

func TestSubscribeReconnectResumeAndCancellation(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			fmt.Fprint(w, "id: 7\nevent: post.created\ndata: {}\n\n")
			return
		}
		if got := r.Header.Get("Last-Event-ID"); got != "7" {
			t.Errorf("resume ID = %q", got)
		}
		fmt.Fprint(w, "id: 8\nevent: stream.reset\ndata: {}\n\n")
	}))
	defer server.Close()
	api, _ := New(server.URL, server.Client(), time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	resets := 0
	err := api.Subscribe(ctx, StreamScope{Board: "b"}, StreamCallbacks{Reset: func() { resets++; cancel() }}, Backoff{Min: time.Millisecond, Max: time.Millisecond})
	if err != context.Canceled || resets != 1 || calls.Load() < 2 {
		t.Fatalf("calls=%d resets=%d err=%v", calls.Load(), resets, err)
	}
}

type oneByteReader struct{ s string }

func (r *oneByteReader) Read(p []byte) (int, error) {
	if r.s == "" {
		return 0, fmt.Errorf("fragment done")
	}
	p[0] = r.s[0]
	r.s = r.s[1:]
	return 1, nil
}

func TestUnknownEventsIgnoredBySubscriber(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: unknown\ndata: nope\n\n")
		<-r.Context().Done()
	}))
	defer server.Close()
	api, _ := New(server.URL, server.Client(), time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	called := false
	err := api.Subscribe(ctx, StreamScope{Thread: "thread_1"}, StreamCallbacks{Event: func(Event) { called = true }}, Backoff{Min: time.Millisecond, Max: time.Millisecond})
	if !strings.Contains(err.Error(), "deadline") || called {
		t.Fatalf("called=%v err=%v", called, err)
	}
}

func TestSubscribeRejectsInvalidScopeAndContentType(t *testing.T) {
	api := NewWithTransport(nil, time.Second)
	if err := api.Subscribe(context.Background(), StreamScope{Board: "b", Thread: "thread_1"}, StreamCallbacks{}, Backoff{}); err == nil {
		t.Fatal("expected invalid scope error")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	api, _ = New(server.URL, server.Client(), time.Second)
	if err := api.Subscribe(context.Background(), StreamScope{}, StreamCallbacks{}, Backoff{}); err == nil || !strings.Contains(err.Error(), "content type") {
		t.Fatalf("content type error = %v", err)
	}
}
