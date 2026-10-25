package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"mime"
	"net/http"
	"strings"
	"time"

	"bord/api/generated"
)

type Event struct {
	ID                                   string
	Type                                 string
	BoardID, ThreadID, PostID, ReplyToID string
	Data                                 json.RawMessage
}

type StreamScope struct {
	Board, Thread string
	Global        bool
}

type StreamCallbacks struct {
	Event     func(Event)
	Reset     func()
	Heartbeat func()
}

type Backoff struct {
	Min, Max time.Duration
	Jitter   func(time.Duration) time.Duration
}

var errInvalidSSEContentType = errors.New("invalid SSE content type")

const maxSSEEventBytes = 1 << 20

var errSSEEventTooLarge = errors.New("SSE event exceeds client limit")

func DefaultBackoff() Backoff {
	return Backoff{Min: 250 * time.Millisecond, Max: 15 * time.Second, Jitter: func(d time.Duration) time.Duration {
		return time.Duration(rand.Int64N(int64(d/2) + 1))
	}}
}

// Subscribe reconnects until ctx is cancelled, resuming from the most recently processed ID.
func (c *API) Subscribe(ctx context.Context, scope StreamScope, callbacks StreamCallbacks, backoff Backoff) error {
	scopes := 0
	if scope.Board != "" {
		scopes++
	}
	if scope.Thread != "" {
		scopes++
	}
	if scope.Global {
		scopes++
	}
	if scopes > 1 {
		return errors.New("SSE scope must select at most one scope")
	}
	if backoff.Min <= 0 {
		backoff = DefaultBackoff()
	}
	if backoff.Max < backoff.Min {
		backoff.Max = backoff.Min
	}
	var lastID string
	delay := backoff.Min
	for {
		params := &generated.StreamEventsParams{}
		if scope.Board != "" {
			params.Board = &scope.Board
		}
		if scope.Thread != "" {
			params.Thread = &scope.Thread
		}
		if scope.Global {
			global := true
			params.Global = &global
		}
		if lastID != "" {
			params.LastEventID = &lastID
		}
		resp, err := c.raw.StreamEvents(ctx, params)
		if err == nil {
			if resp == nil {
				err = errors.New("empty HTTP response")
			} else if resp.StatusCode != http.StatusOK {
				err = decodeAPIError(resp)
			} else if mediaType, _, parseErr := mime.ParseMediaType(resp.Header.Get("Content-Type")); parseErr != nil || mediaType != "text/event-stream" {
				resp.Body.Close()
				err = errInvalidSSEContentType
			}
		}
		if err == nil {
			seen, parseErr := parseSSE(ctx, resp.Body, func(event Event) {
				lastID = event.ID
				var envelope struct {
					BoardID   string `json:"board_id"`
					ThreadID  string `json:"thread_id"`
					PostID    string `json:"post_id"`
					ReplyToID string `json:"reply_to_id"`
				}
				if json.Unmarshal(event.Data, &envelope) == nil {
					event.BoardID, event.ThreadID = envelope.BoardID, envelope.ThreadID
					event.PostID, event.ReplyToID = envelope.PostID, envelope.ReplyToID
				}
				switch event.Type {
				case "stream.reset":
					// A reset means the server cannot resume this history. Never send
					// the reset event's ID (or an older ID) on the next connection.
					lastID = ""
					if callbacks.Reset != nil {
						callbacks.Reset()
					}
				case "post.created", "post.deleted", "thread.created", "thread.closed", "catalog.updated":
					if callbacks.Event != nil {
						callbacks.Event(event)
					}
				}
			}, callbacks.Heartbeat)
			resp.Body.Close()
			if seen {
				delay = backoff.Min
			}
			err = parseErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil && !isRetryableStreamError(err) {
			return err
		}
		wait := delay
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.RetryAfter > wait {
			wait = apiErr.RetryAfter
		}
		if backoff.Jitter != nil {
			wait += backoff.Jitter(delay)
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if delay < backoff.Max/2 {
			delay *= 2
		} else {
			delay = backoff.Max
		}
	}
}

func parseSSE(ctx context.Context, r io.Reader, deliver func(Event), heartbeat func()) (bool, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), maxSSEEventBytes+1)
	var lastID, pendingID, kind string
	var hasPendingID bool
	var data []string
	eventBytes := 0
	seen := false
	dispatch := func() {
		if !hasPendingID && kind == "" && len(data) == 0 {
			eventBytes = 0
			return
		}
		if hasPendingID {
			lastID = pendingID
		}
		seen = true
		deliver(Event{ID: lastID, Type: kind, Data: json.RawMessage(strings.Join(data, "\n"))})
		pendingID, kind, data, hasPendingID, eventBytes = "", "", nil, false, 0
	}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return seen, err
		}
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			dispatch()
			continue
		}
		eventBytes += len(line) + 1
		if eventBytes > maxSSEEventBytes {
			return seen, errSSEEventTooLarge
		}
		if strings.HasPrefix(line, ":") {
			seen = true
			if heartbeat != nil {
				heartbeat()
			}
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "id":
			// The EventSource algorithm ignores ID fields containing NUL.
			if !strings.ContainsRune(value, '\x00') {
				pendingID, hasPendingID = value, true
			}
		case "event":
			kind = value
		case "data":
			data = append(data, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return seen, fmt.Errorf("read SSE stream: %w", err)
	}
	return seen, io.EOF
}

func isRetryableStreamError(err error) bool {
	if errors.Is(err, errInvalidSSEContentType) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode >= 500
	}
	return true
}
