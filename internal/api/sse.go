package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"bord/internal/domain"
)

func (h *Handler) streamEvents(w http.ResponseWriter, r *http.Request) {
	if h.config.Events == nil {
		writeError(w, r, unavailable())
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, r, unavailable())
		return
	}
	board, thread := r.URL.Query().Get("board"), r.URL.Query().Get("thread")
	global := r.URL.Query().Get("global") == "true"
	scopes := 0
	if board != "" {
		scopes++
	}
	if thread != "" {
		scopes++
	}
	if global {
		scopes++
	}
	if scopes != 1 {
		writeError(w, r, validation("board", errors.New("select exactly one stream scope")))
		return
	}
	if board != "" && !validBoardSlug(board) {
		writeError(w, r, validation("board", errors.New("invalid board slug")))
		return
	}
	if thread != "" && !validResourceID(thread) {
		writeError(w, r, validation("thread", errors.New("invalid ID")))
		return
	}
	var after domain.EventID
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 0 {
			writeError(w, r, validation("Last-Event-ID", errors.New("invalid event ID")))
			return
		}
		after = domain.EventID(value)
	}
	replay, reset, subscription := h.config.Events.Subscribe(board, thread, after)
	if subscription == nil {
		writeError(w, r, unavailable())
		return
	}
	defer subscription.Close()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	if reset {
		_, _ = fmt.Fprint(w, "event: stream.reset\ndata: {}\n\n")
		flusher.Flush()
	} else {
		for _, event := range replay {
			if writeSSE(w, event) != nil {
				return
			}
		}
		flusher.Flush()
	}
	ticker := time.NewTicker(h.config.HeartbeatPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-subscription.Events():
			if !open {
				return
			}
			if writeSSE(w, event) != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeSSE(w http.ResponseWriter, event Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.ID, event.Type, data)
	return err
}
