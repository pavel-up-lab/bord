// Package events provides bounded in-process event fan-out and outbox dispatch.
package events

import (
	"sync"

	"bord/internal/api"
	"bord/internal/domain"
)

type Hub struct {
	mu          sync.Mutex
	historySize int
	queueSize   int
	maxClients  int
	history     []api.Event
	subscribers map[*subscriber]struct{}
	closed      bool
}

const defaultMaxClients = 1024

type subscriber struct {
	hub    *Hub
	board  string
	thread string
	events chan api.Event
	once   sync.Once
}

func NewHub(historySize, queueSize int) *Hub {
	if historySize < 1 {
		historySize = 256
	}
	if queueSize < 1 {
		queueSize = 16
	}
	return &Hub{historySize: historySize, queueSize: queueSize, maxClients: defaultMaxClients, subscribers: make(map[*subscriber]struct{})}
}

// Publish keeps a bounded replay window. A slow subscriber is disconnected instead
// of blocking the dispatcher or consuming an unbounded amount of memory.
func (h *Hub) Publish(event api.Event) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	if len(h.history) > 0 {
		latest := h.history[len(h.history)-1].ID
		if event.ID == latest {
			return true
		}
		if event.ID < latest {
			return false
		}
	}
	h.history = append(h.history, event)
	if len(h.history) > h.historySize {
		copy(h.history, h.history[len(h.history)-h.historySize:])
		h.history = h.history[:h.historySize]
	}
	for subscriber := range h.subscribers {
		if !matches(event, subscriber.board, subscriber.thread) {
			continue
		}
		select {
		case subscriber.events <- event:
		default:
			delete(h.subscribers, subscriber)
			close(subscriber.events)
		}
	}
	return true
}

func (h *Hub) Subscribe(board, thread string, after domain.EventID) ([]api.Event, bool, api.Subscription) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		s := &subscriber{hub: h, events: make(chan api.Event)}
		close(s.events)
		return nil, true, s
	}
	if len(h.subscribers) >= h.maxClients {
		return nil, false, nil
	}
	s := &subscriber{hub: h, board: board, thread: thread, events: make(chan api.Event, h.queueSize)}
	h.subscribers[s] = struct{}{}
	if after == 0 {
		return nil, false, s
	}
	if len(h.history) == 0 || after < h.history[0].ID || after > h.history[len(h.history)-1].ID {
		return nil, true, s
	}
	replay := make([]api.Event, 0)
	for _, event := range h.history {
		if event.ID > after && matches(event, board, thread) {
			replay = append(replay, event)
		}
	}
	return replay, false, s
}

func (h *Hub) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	for subscriber := range h.subscribers {
		close(subscriber.events)
		delete(h.subscribers, subscriber)
	}
	return nil
}

func (s *subscriber) Events() <-chan api.Event { return s.events }
func (s *subscriber) Close() {
	s.once.Do(func() {
		s.hub.mu.Lock()
		defer s.hub.mu.Unlock()
		if _, exists := s.hub.subscribers[s]; exists {
			delete(s.hub.subscribers, s)
			close(s.events)
		}
	})
}

func matches(event api.Event, board, thread string) bool {
	if board == "" && thread == "" {
		return true
	}
	if board != "" {
		return event.BoardID == board
	}
	return event.ThreadID == thread
}
