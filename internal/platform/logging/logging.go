// Package logging configures structured logs which discard sensitive attributes.
package logging

import (
	"context"
	"io"
	"log/slog"
	"strings"
)

var sensitive = map[string]struct{}{
	"ip": {}, "raw_ip": {}, "remote_ip": {}, "post_body": {}, "body": {},
	"authorization": {}, "bearer": {}, "credential": {}, "delete_token": {}, "secret": {},
}

func JSON(w io.Writer, level slog.Leveler) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(&redactingHandler{next: h})
}

type redactingHandler struct{ next slog.Handler }

func (h *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}
func (h *redactingHandler) Handle(ctx context.Context, r slog.Record) error {
	clean := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		if !isSensitive(a.Key) {
			clean.AddAttrs(redactGroup(a))
		}
		return true
	})
	return h.next.Handle(ctx, clean)
}
func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		if !isSensitive(a.Key) {
			clean = append(clean, redactGroup(a))
		}
	}
	return &redactingHandler{next: h.next.WithAttrs(clean)}
}
func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{next: h.next.WithGroup(name)}
}
func isSensitive(key string) bool { _, ok := sensitive[strings.ToLower(key)]; return ok }
func redactGroup(a slog.Attr) slog.Attr {
	if a.Value.Kind() != slog.KindGroup {
		return a
	}
	group := a.Value.Group()
	clean := make([]slog.Attr, 0, len(group))
	for _, child := range group {
		if !isSensitive(child.Key) {
			clean = append(clean, redactGroup(child))
		}
	}
	a.Value = slog.GroupValue(clean...)
	return a
}
