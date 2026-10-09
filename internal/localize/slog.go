package localize

import (
	"context"
	"log/slog"
)

type translatingHandler struct {
	next slog.Handler
}

// NewSlogHandler wraps a slog handler and translates static cataloged messages.
func NewSlogHandler(next slog.Handler) slog.Handler {
	return translatingHandler{next: next}
}

func (h translatingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h translatingHandler) Handle(ctx context.Context, record slog.Record) error {
	record.Message = LogMessage(record.Message)
	return h.next.Handle(ctx, record)
}

func (h translatingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return translatingHandler{next: h.next.WithAttrs(attrs)}
}

func (h translatingHandler) WithGroup(name string) slog.Handler {
	return translatingHandler{next: h.next.WithGroup(name)}
}
