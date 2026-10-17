package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"bord/api/generated"
)

type Event struct {
	ID   string
	Type string
	Data json.RawMessage
}

type StreamScope struct{ Board, Thread string }

type StreamCallbacks struct {
	Event     func(Event)
	Reset     func()
	Heartbeat func()
}

type Backoff struct {
	Min, Max time.Duration
	Jitter   func(time.Duration) time.Duration
}

func DefaultBackoff() Backoff {
	return Backoff{Min: 250 * time.Millisecond, Max: 15 * time.Second, Jitter: func(d time.Duration) time.Duration {
		return time.Duration(rand.Int64N(int64(d/2) + 1))
	}}
}

// Subscribe reconnects until ctx is cancelled, resuming from the most recently processed ID.
func (c *API) Subscribe(ctx context.Context, scope StreamScope, callbacks StreamCallbacks, backoff Backoff) error {
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
		if lastID != "" {
			params.LastEventID = &lastID
		}
		resp, err := c.raw.StreamEvents(ctx, params)
		if err == nil && resp.StatusCode != http.StatusOK {
			err = decodeAPIError(resp)
		}
		if err == nil {
			seen, parseErr := parseSSE(ctx, resp.Body, func(event Event) {
				if event.ID != "" {
					lastID = event.ID
				}
				switch event.Type {
				case "stream.reset":
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
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var id, kind string
	var data []string
	seen := false
	dispatch := func() {
		if id == "" && kind == "" && len(data) == 0 {
			return
		}
		seen = true
		deliver(Event{ID: id, Type: kind, Data: json.RawMessage(strings.Join(data, "\n"))})
		id, kind, data = "", "", nil
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
			id = value
		case "event":
			kind = value
		case "data":
			data = append(data, value)
		}
	}
	dispatch()
	if err := scanner.Err(); err != nil {
		return seen, fmt.Errorf("read SSE stream: %w", err)
	}
	return seen, io.EOF
}

func isRetryableStreamError(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode >= 500
	}
	return true
}
