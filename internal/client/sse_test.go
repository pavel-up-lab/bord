package client

import (
	"context"
	"errors"
	"fmt"
	"io"
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

func TestSubscribeResetClearsLastEventID(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		switch calls.Add(1) {
		case 1:
			fmt.Fprint(w, "id: 7\nevent: post.created\ndata: {}\n\n")
		case 2:
			if got := r.Header.Get("Last-Event-ID"); got != "7" {
				t.Errorf("resume ID before reset = %q", got)
			}
			fmt.Fprint(w, "id: 8\nevent: stream.reset\ndata: {}\n\n")
		default:
			if got := r.Header.Get("Last-Event-ID"); got != "" {
				t.Errorf("resume ID after reset = %q", got)
			}
			<-r.Context().Done()
		}
	}))
	defer server.Close()
	api, _ := New(server.URL, server.Client(), time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := api.Subscribe(ctx, StreamScope{Board: "b"}, StreamCallbacks{}, Backoff{Min: time.Millisecond, Max: time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) || calls.Load() < 3 {
		t.Fatalf("calls=%d err=%v", calls.Load(), err)
	}
}

func TestParseSSEBlankAndNULIDsAndMultilineData(t *testing.T) {
	stream := "id: 7\nevent: post.created\ndata: one\ndata: two\n\n" +
		"id: bad\x00id\nevent: post.deleted\ndata: {}\n\n" +
		"id:\nevent: catalog.updated\ndata: {}\n\n" +
		"event: thread.closed\ndata: {}\n\n"
	var events []Event
	seen, err := parseSSE(context.Background(), strings.NewReader(stream), func(event Event) { events = append(events, event) }, nil)
	if !seen || !errors.Is(err, io.EOF) || len(events) != 4 {
		t.Fatalf("seen=%v events=%#v err=%v", seen, events, err)
	}
	if events[0].ID != "7" || string(events[0].Data) != "one\ntwo" || events[1].ID != "7" || events[2].ID != "" || events[3].ID != "" {
		t.Fatalf("events=%#v", events)
	}
}

func TestParseSSEBoundsTotalEventSize(t *testing.T) {
	line := "data: " + strings.Repeat("x", 1024) + "\n"
	stream := strings.Repeat(line, maxSSEEventBytes/1024+1) + "\n"
	seen, err := parseSSE(context.Background(), strings.NewReader(stream), func(Event) {}, nil)
	if seen || !errors.Is(err, errSSEEventTooLarge) {
		t.Fatalf("seen=%v err=%v", seen, err)
	}
}

func TestSubscribeCancellationReleasesHandlers(t *testing.T) {
	var active atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		active.Add(1)
		defer active.Add(-1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	api, _ := New(server.URL, server.Client(), time.Second)
	for range 20 {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- api.Subscribe(ctx, StreamScope{Board: "b"}, StreamCallbacks{}, Backoff{Min: time.Millisecond, Max: time.Millisecond})
		}()
		deadline := time.Now().Add(time.Second)
		for active.Load() == 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if active.Load() == 0 {
			cancel()
			t.Fatal("SSE handler did not start")
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("Subscribe returned %v", err)
		}
		// Subscribe returning does not guarantee that httptest has already run
		// the handler defer. Wait before the next iteration so a previous active
		// handler cannot be mistaken for the newly started one.
		deadline = time.Now().Add(time.Second)
		for active.Load() != 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if got := active.Load(); got != 0 {
			t.Fatalf("active SSE handlers between iterations=%d", got)
		}
	}
	deadline := time.Now().Add(time.Second)
	for active.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := active.Load(); got != 0 {
		t.Fatalf("%d SSE handlers leaked after cancellation", got)
	}
}

func FuzzParseSSEFragmentationIDsAndMultiline(f *testing.F) {
	f.Add(uint8(1), "1", "")
	f.Add(uint8(7), "1", "next")
	f.Add(uint8(3), "1", "bad\x00id")
	f.Fuzz(func(t *testing.T, chunk uint8, firstID, nextID string) {
		if chunk == 0 {
			chunk = 1
		}
		if len(firstID) > 1024 || len(nextID) > 1024 {
			t.Skip()
		}
		firstID = strings.NewReplacer("\n", " ", "\r", " ").Replace(firstID)
		nextID = strings.NewReplacer("\n", " ", "\r", " ").Replace(nextID)
		stream := "id: " + firstID + "\nevent: post.created\ndata: first\ndata: second\n\n" +
			"id: " + nextID + "\nevent: post.deleted\ndata: {}\n\n"
		var events []Event
		seen, err := parseSSE(context.Background(), &chunkReader{s: stream, n: int(chunk)}, func(event Event) { events = append(events, event) }, nil)
		wantFirst := firstID
		if strings.ContainsRune(firstID, '\x00') {
			wantFirst = ""
		}
		wantNext := nextID
		if strings.ContainsRune(nextID, '\x00') {
			wantNext = wantFirst
		}
		if !seen || !errors.Is(err, io.EOF) || len(events) != 2 || events[0].ID != wantFirst || events[1].ID != wantNext || string(events[0].Data) != "first\nsecond" {
			t.Fatalf("chunk=%d seen=%v events=%#v err=%v", chunk, seen, events, err)
		}
	})
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

type chunkReader struct {
	s string
	n int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if r.s == "" {
		return 0, io.EOF
	}
	n := min(len(p), r.n, len(r.s))
	copy(p, r.s[:n])
	r.s = r.s[n:]
	return n, nil
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

func TestSubscribeGlobalDecodesMentionMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("global") != "true" {
			t.Errorf("query=%q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "id: 9\nevent: post.created\ndata: {\"board_id\":\"board_1\",\"thread_id\":\"thread_1\",\"post_id\":\"post_2\",\"reply_to_id\":\"post_1\"}\n\n")
	}))
	defer server.Close()
	api, _ := New(server.URL, server.Client(), time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var got Event
	err := api.Subscribe(ctx, StreamScope{Global: true}, StreamCallbacks{Event: func(event Event) {
		got = event
		cancel()
	}}, Backoff{Min: time.Millisecond, Max: time.Millisecond})
	if !errors.Is(err, context.Canceled) || got.ThreadID != "thread_1" || got.PostID != "post_2" || got.ReplyToID != "post_1" {
		t.Fatalf("event=%+v err=%v", got, err)
	}
}
