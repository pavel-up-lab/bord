package events_test

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bord/internal/api"
	"bord/internal/domain"
	"bord/internal/events"
)

func TestSSEResumeAndReset(t *testing.T) {
	t.Run("resume", func(t *testing.T) {
		hub := events.NewHub(4, 4)
		defer hub.Close()
		hub.Publish(api.Event{ID: 1, BoardID: "test", Type: "post.created"})
		hub.Publish(api.Event{ID: 2, BoardID: "test", Type: "post.created", PostID: "post_1234"})
		server := httptest.NewServer(api.NewRouter(api.Config{Events: hub, HeartbeatPeriod: time.Hour}))
		defer server.Close()
		req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/events?board=test", nil)
		req.Header.Set("Last-Event-ID", "1")
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		reader := bufio.NewReader(response.Body)
		contents := readUntilBlank(t, reader)
		if !strings.Contains(contents, "id: 2") || !strings.Contains(contents, "event: post.created") {
			t.Fatalf("unexpected event: %q", contents)
		}
	})

	t.Run("reset", func(t *testing.T) {
		hub := events.NewHub(1, 2)
		defer hub.Close()
		hub.Publish(api.Event{ID: domain.EventID(1), BoardID: "test"})
		hub.Publish(api.Event{ID: domain.EventID(2), BoardID: "test"})
		server := httptest.NewServer(api.NewRouter(api.Config{Events: hub, HeartbeatPeriod: time.Hour}))
		defer server.Close()
		req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/events?board=test", nil)
		req.Header.Set("Last-Event-ID", "1")
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if got := readUntilBlank(t, bufio.NewReader(response.Body)); !strings.Contains(got, "event: stream.reset") {
			t.Fatalf("expected reset, got %q", got)
		}
	})
}

func readUntilBlank(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	var result strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		result.WriteString(line)
		if line == "\n" {
			return result.String()
		}
	}
}
